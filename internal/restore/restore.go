// Package restore implements pending-restore staging and on-startup application
// for database and config archives produced by the backup worker or manual Full
// Export. The flow is:
//
//  1. Import handler receives a backup archive upload and calls StageZip() to
//     validate and stage it below the state root.
//  2. Process exits; systemd restarts igloo.
//  3. Startup calls ApplyPending() before opening the database. If the
//     marker exists, the staged database and config files replace the live
//     ones, and the staging directory is cleaned up.
package restore

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/storage"
)

const (
	stagingSubdir = "restore-staging"
	markerName    = ".pending-restore"
	configPrefix  = "config/"
	runtimeName   = "runtime.json"
)

var ErrMissingDatabase = errors.New("backup archive missing database")

func stagingDir(dataDir string) string { return filepath.Join(dataDir, stagingSubdir) }
func markerPath(dataDir string) string { return filepath.Join(stagingDir(dataDir), markerName) }

// HasPending reports whether a restore has been staged and is awaiting startup.
func HasPending(dataDir string) bool {
	info, err := os.Lstat(markerPath(dataDir))
	return err == nil && info.Mode().IsRegular()
}

// StageZip extracts and validates a current DB-bearing zip before making it
// visible to startup restore.
func StageZip(readerAt io.ReaderAt, size int64, layout storage.Layout) (stageErr error) {
	if err := layout.Ensure(); err != nil {
		return fmt.Errorf("validate storage layout: %w", err)
	}
	stage := stagingDir(layout.StateRoot())
	if err := cleanupStaging(stage); err != nil {
		return fmt.Errorf("clear staging dir: %w", err)
	}
	defer func() {
		if stageErr != nil {
			stageErr = errors.Join(stageErr, cleanupStaging(stage))
		}
	}()
	if err := storage.EnsureDirectory(stage, 0o755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	dbSeen, err := extractZipBackup(readerAt, size, stage)
	if err != nil {
		return err
	}
	if !dbSeen {
		return ErrMissingDatabase
	}
	stagedDB := filepath.Join(stage, config.DatabaseFilename)
	if err := validateStagedDatabase(stagedDB, layout); err != nil {
		return fmt.Errorf("validate staged db: %w", err)
	}
	if err := validateStagedRestoreConfig(stage); err != nil {
		return fmt.Errorf("validate staged config: %w", err)
	}
	if err := replaceFile(markerPath(layout.StateRoot()), 0o644, func(io.Writer) error { return nil }); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}

func extractZipBackup(readerAt io.ReaderAt, size int64, stage string) (bool, error) {
	zr, err := zip.NewReader(readerAt, size)
	if err != nil {
		return false, fmt.Errorf("open zip: %w", err)
	}
	dbSeen := false
	seen := make(map[string]struct{})
	for _, f := range zr.File {
		clean, dbEntry, ok, err := backupArchiveEntry(f.Name)
		if err != nil {
			return false, err
		}
		if !ok {
			slog.Warn("restore: skipping unexpected zip entry", "name", filepath.Clean(f.Name))
			continue
		}
		dest := filepath.Join(stage, clean)
		info := f.FileInfo()
		if info.IsDir() {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if _, exists := seen[clean]; exists {
			return false, fmt.Errorf("duplicate backup entry: %s", f.Name)
		}
		seen[clean] = struct{}{}
		rc, err := f.Open()
		if err != nil {
			return false, fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		writeErr := replaceFile(dest, mode, func(dst io.Writer) error {
			_, err := io.Copy(dst, rc)
			return err
		})
		closeErr := rc.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return false, err
		}
		if dbEntry {
			dbSeen = true
		}
	}
	return dbSeen, nil
}

func backupArchiveEntry(name string) (clean string, dbEntry bool, ok bool, err error) {
	clean = filepath.Clean(name)
	if clean == "." || clean == "" {
		return "", false, false, nil
	}
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) || strings.Contains(clean, "..") {
		return "", false, false, fmt.Errorf("unsafe backup path: %s", name)
	}
	slash := filepath.ToSlash(clean)
	dbEntry = slash == config.DatabaseFilename
	if dbEntry || slash == runtimeName ||
		strings.HasPrefix(slash+"/", configPrefix) ||
		slash == strings.TrimSuffix(configPrefix, "/") {
		return clean, dbEntry, true, nil
	}
	return clean, false, false, nil
}

func cleanupStaging(stage string) error {
	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("remove staging dir %q: %w", stage, err)
	}
	return nil
}

func ApplyPending(cfg *config.Config) error {
	if !HasPending(cfg.Storage.StateRoot()) {
		return nil
	}
	if err := cfg.Storage.Ensure(); err != nil {
		return fmt.Errorf("validate storage layout: %w", err)
	}
	stage := stagingDir(cfg.Storage.StateRoot())
	stagedDB := filepath.Join(stage, config.DatabaseFilename)
	if _, err := os.Stat(stagedDB); err != nil {
		return fmt.Errorf("staged db missing: %w", err)
	}
	if err := validateStagedDatabase(stagedDB, cfg.Storage); err != nil {
		return fmt.Errorf("validate staged db: %w", err)
	}
	if err := validateStagedRestoreConfig(stage); err != nil {
		return fmt.Errorf("validate staged config: %w", err)
	}
	configFiles, err := collectRestoreConfigFiles(stage, cfg)
	if err != nil {
		return fmt.Errorf("read staged config: %w", err)
	}
	if err := validateEffectiveStartupConfig(cfg, configFiles); err != nil {
		return fmt.Errorf("validate effective startup config: %w", err)
	}
	slog.Info("restore: applying pending restore", "stage", stage)
	for _, file := range configFiles {
		if err := storage.ValidateContainedPath(cfg.ConfDir, file.target); err != nil {
			return fmt.Errorf("revalidate config destination %s: %w", file.target, err)
		}
		if err := installRestoreConfigFile(file); err != nil {
			return fmt.Errorf("restore config %s: %w", file.rel, err)
		}
	}
	if err := cfg.Storage.Ensure(); err != nil {
		return fmt.Errorf("revalidate storage layout: %w", err)
	}
	if err := storage.ValidateContainedPath(cfg.Storage.StateRoot(), cfg.Storage.DatabasePath()); err != nil {
		return fmt.Errorf("validate database destination: %w", err)
	}
	preparedDB, err := prepareFileFromPath(stagedDB, cfg.Storage.DatabasePath(), 0o600)
	if err != nil {
		return fmt.Errorf("prepare restored database: %w", err)
	}
	defer func() { _ = os.Remove(preparedDB) }()
	if err := resetPreparedAndroidSyncIdentity(preparedDB); err != nil {
		return fmt.Errorf("reset restored Android sync identity: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := validateRemovableFile(cfg.Storage.DatabasePath() + suffix); err != nil {
			return fmt.Errorf("validate old database%s: %w", suffix, err)
		}
	}
	if err := os.Rename(preparedDB, cfg.Storage.DatabasePath()); err != nil {
		return fmt.Errorf("restore database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := removeFile(cfg.Storage.DatabasePath() + suffix); err != nil {
			return fmt.Errorf("remove old database%s: %w", suffix, err)
		}
	}
	if err := storage.SyncDirectory(filepath.Dir(cfg.Storage.DatabasePath())); err != nil {
		return fmt.Errorf("sync restored database: %w", err)
	}
	if err := os.Remove(markerPath(cfg.Storage.StateRoot())); err != nil {
		return fmt.Errorf("clear restore marker: %w", err)
	}
	if err := storage.SyncDirectory(stage); err != nil {
		slog.Warn("restore: marker directory sync failed", "err", err)
	}
	if err := cleanupStaging(stage); err != nil {
		slog.Warn("restore: staging cleanup failed", "state_dir", stage, "err", err)
	}
	slog.Info("restore: database swapped", "path", cfg.Storage.DatabasePath())
	if len(configFiles) > 0 {
		slog.Info("restore: config files restored", "count", len(configFiles), "dir", cfg.ConfDir)
	}
	return nil
}

func resetPreparedAndroidSyncIdentity(path string) error {
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(delete)&_pragma=busy_timeout(30000)")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM android_sync_heads`); err != nil {
		return err
	}
	result, err := tx.Exec(`
		UPDATE android_sync_clock
		SET epoch = lower(hex(randomblob(16))), revision = 0
		WHERE id = 1
	`)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed != 1 {
		return fmt.Errorf("android sync clock row is missing")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func validateStagedDatabase(path string, layout storage.Layout) error {
	staged, err := db.OpenReadOnlyLayout(path, layout)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close() }()
	if err := validateDatabaseIntegrity(staged); err != nil {
		return err
	}
	return staged.WithRead(db.ValidateCurrentSchema)
}

func validateDatabaseIntegrity(store *db.DB) error {
	return store.WithRead(func(conn *sql.DB) error {
		var quickCheck string
		if err := conn.QueryRow(`PRAGMA quick_check`).Scan(&quickCheck); err != nil {
			return err
		}
		if quickCheck != "ok" {
			return fmt.Errorf("quick_check failed: %s", quickCheck)
		}
		fkRows, err := conn.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			return err
		}
		defer func() { _ = fkRows.Close() }()
		if fkRows.Next() {
			return fmt.Errorf("foreign_key_check failed")
		}
		return fkRows.Err()
	})
}

func prepareFileFromPath(sourcePath, targetPath string, mode os.FileMode) (string, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	preparedPath, prepareErr := prepareFile(targetPath, mode, func(dst io.Writer) error {
		_, err := io.Copy(dst, source)
		return err
	})
	if err := errors.Join(prepareErr, source.Close()); err != nil {
		_ = os.Remove(preparedPath)
		return "", err
	}
	return preparedPath, nil
}

func replaceFile(target string, mode os.FileMode, write func(io.Writer) error) error {
	tmpPath, err := prepareFile(target, mode, write)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	return storage.SyncDirectory(filepath.Dir(target))
}

func prepareFile(target string, mode os.FileMode, write func(io.Writer) error) (string, error) {
	if target == "" || write == nil {
		return "", fmt.Errorf("restore target and writer are required")
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("restore target %q is not a regular file", target)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(target)
	if err := storage.EnsureDirectory(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".igloo-restore-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	remove := func(err error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := write(tmp); err != nil {
		return remove(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return remove(err)
	}
	if err := tmp.Sync(); err != nil {
		return remove(err)
	}
	if err := tmp.Close(); err != nil {
		return remove(err)
	}
	return tmpPath, nil
}

func validateRemovableFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("restore sidecar %q is not a regular file", path)
	}
	return nil
}

func removeFile(path string) error {
	if err := validateRemovableFile(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type restoreConfigFile struct {
	rel    string
	source string
	target string
	data   []byte
	mode   os.FileMode
}

func collectRestoreConfigFiles(stage string, cfg *config.Config) ([]restoreConfigFile, error) {
	src := filepath.Join(stage, "config")
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("staged config is not a directory")
	}
	if strings.TrimSpace(cfg.ConfDir) == "" {
		return nil, fmt.Errorf("config directory is not configured")
	}
	sourceRuntime, err := readStagedRuntimeManifest(stage)
	if err != nil {
		return nil, err
	}
	targetRuntime := runtimeManifest{
		Version:   2,
		DataDir:   cfg.Storage.StateRoot(),
		MediaDir:  cfg.Storage.MediaRoot(),
		ConfigDir: cfg.ConfDir,
		RepoDir:   cfg.RepoDir,
	}
	files := make([]restoreConfigFile, 0)
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		target := filepath.Join(cfg.ConfDir, rel)
		if err := storage.ValidateContainedPath(cfg.ConfDir, target); err != nil {
			return fmt.Errorf("staged config path escapes destination: %s: %w", rel, err)
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, restoreConfigFile{
			rel: rel, source: path, target: target, mode: mode,
			data: rewriteRuntimeConfigPaths(rel, data, sourceRuntime, targetRuntime),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func installRestoreConfigFile(file restoreConfigFile) error {
	return replaceFile(file.target, file.mode, func(dst io.Writer) error {
		_, err := dst.Write(file.data)
		return err
	})
}

func validateEffectiveStartupConfig(cfg *config.Config, files []restoreConfigFile) error {
	runtimeConfigPath := strings.TrimSpace(cfg.RuntimeConfigPath)
	if runtimeConfigPath == "" {
		runtimeConfigPath = filepath.Join(cfg.ConfDir, "config.json")
	}
	if err := config.ValidateEffectiveRuntimeConfigFile(effectiveRestorePath(runtimeConfigPath, files)); err != nil {
		return err
	}
	authUsersPath := strings.TrimSpace(cfg.AuthUsersPath)
	if authUsersPath == "" {
		authUsersPath = filepath.Join(cfg.ConfDir, "auth_users.json")
	}
	if _, err := auth.LoadUsers(effectiveRestorePath(authUsersPath, files)); err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("AUTH_SECRET_KEY")) == "" {
		authSecretPath := effectiveRestorePath(filepath.Join(cfg.ConfDir, "auth_secret"), files)
		secret, err := os.ReadFile(authSecretPath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && len(secret) == 0 {
			return fmt.Errorf("effective auth secret is empty")
		}
	}
	return nil
}

func effectiveRestorePath(target string, files []restoreConfigFile) string {
	for _, file := range files {
		if file.target == target {
			return file.source
		}
	}
	return target
}

type runtimeManifest struct {
	Version   int    `json:"version"`
	DataDir   string `json:"data_dir,omitempty"`
	MediaDir  string `json:"media_dir,omitempty"`
	ConfigDir string `json:"config_dir,omitempty"`
	RepoDir   string `json:"repo_dir,omitempty"`
}

func validateStagedRestoreConfig(stage string) error {
	if _, err := readStagedRuntimeManifest(stage); err != nil {
		return err
	}
	runtimeConfigPath := filepath.Join(stage, "config", "config.json")
	if exists, err := stagedRegularFile(runtimeConfigPath); err != nil {
		return err
	} else if exists {
		if err := config.ValidateRuntimeConfigFile(runtimeConfigPath); err != nil {
			return err
		}
	}
	authUsersPath := filepath.Join(stage, "config", "auth_users.json")
	if exists, err := stagedRegularFile(authUsersPath); err != nil {
		return err
	} else if exists {
		if _, err := auth.LoadUsers(authUsersPath); err != nil {
			return err
		}
	}
	authSecretPath := filepath.Join(stage, "config", "auth_secret")
	if exists, err := stagedRegularFile(authSecretPath); err != nil {
		return err
	} else if exists {
		secret, err := os.ReadFile(authSecretPath)
		if err != nil {
			return err
		}
		if len(secret) == 0 {
			return fmt.Errorf("staged auth secret is empty")
		}
	}
	return nil
}

func stagedRegularFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("staged config file %q is not a regular file", path)
	}
	return true, nil
}

func readStagedRuntimeManifest(stage string) (runtimeManifest, error) {
	path := filepath.Join(stage, runtimeName)
	exists, err := stagedRegularFile(path)
	if err != nil || !exists {
		return runtimeManifest{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return runtimeManifest{}, err
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest runtimeManifest
	decodeErr := decoder.Decode(&manifest)
	var trailing any
	if decodeErr == nil {
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				decodeErr = fmt.Errorf("trailing JSON value")
			} else {
				decodeErr = err
			}
		}
	}
	closeErr := file.Close()
	if err := errors.Join(decodeErr, closeErr); err != nil {
		return runtimeManifest{}, fmt.Errorf("parse staged %s: %w", runtimeName, err)
	}
	if manifest.Version != 2 {
		return runtimeManifest{}, fmt.Errorf("unsupported staged %s version %d", runtimeName, manifest.Version)
	}
	return manifest, nil
}

func rewriteRuntimeConfigPaths(rel string, data []byte, source, target runtimeManifest) []byte {
	if filepath.ToSlash(rel) != "nginx.conf" {
		return data
	}
	replacements := [][2]string{
		{runtimeMediaDir(source), runtimeMediaDir(target)},
		{source.DataDir, target.DataDir},
		{source.ConfigDir, target.ConfigDir},
		{source.RepoDir, target.RepoDir},
	}
	args := make([]string, 0, len(replacements)*2)
	for _, pair := range replacements {
		oldPath := cleanReplacementPath(pair[0])
		newPath := cleanReplacementPath(pair[1])
		if oldPath == "" || newPath == "" || oldPath == newPath {
			continue
		}
		args = append(args, oldPath, newPath)
	}
	if len(args) == 0 {
		return data
	}
	return []byte(strings.NewReplacer(args...).Replace(string(data)))
}

func runtimeMediaDir(manifest runtimeManifest) string {
	if strings.TrimSpace(manifest.MediaDir) != "" {
		return manifest.MediaDir
	}
	if strings.TrimSpace(manifest.DataDir) == "" {
		return ""
	}
	return filepath.Join(manifest.DataDir, "media")
}

func cleanReplacementPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) {
		return ""
	}
	return clean
}
