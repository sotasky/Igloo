package worker

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/exportbundle"
	"github.com/screwys/igloo/internal/settings"
)

const (
	backupCheckInterval = 1 * time.Hour
	backupMinAge        = 24 * time.Hour
	backupPrefix        = "igloo-backup-"
)

func (m *Manager) runBackupWorker(ctx context.Context) {
	m.tryBackup()
	t := time.NewTicker(backupCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tryBackup()
		}
	}
}

func (m *Manager) tryBackup() {
	enabled, _ := m.db.GetSetting("backup_enabled", "false")
	if enabled != "true" && enabled != "1" {
		return
	}
	dir, _ := m.db.GetSetting("backup_dir", "")
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		err := fmt.Errorf("backup dir must be absolute: %s", dir)
		slog.Error("backup: invalid dir", "err", err)
		m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Error: err.Error()})
		m.Emit("backup", "backup failed: "+err.Error(), "error")
		return
	}

	if !m.backupDue(dir) {
		return
	}

	m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, Detail: "creating backup"})
	m.Emit("backup", "starting daily backup", "info")

	if err := m.createBackup(dir); err != nil {
		slog.Error("backup: failed", "err", err)
		m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Error: err.Error()})
		m.Emit("backup", "backup failed: "+err.Error(), "error")
		return
	}

	m.pruneBackups(dir, m.backupKeepCount())
	m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Detail: "idle"})
	m.Emit("backup", "backup completed", "info")
}

func (m *Manager) backupDue(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	var latest time.Time
	for _, e := range entries {
		if !isBackupArchiveName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	if latest.IsZero() {
		return true
	}
	return time.Since(latest) >= backupMinAge
}

func (m *Manager) createBackup(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("backup dir is required")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("backup dir must be absolute: %s", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("%s%s.zip", backupPrefix, stamp)
	tmpPath := filepath.Join(dir, name+".tmp")
	finalPath := filepath.Join(dir, name)

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmpPath)
	}()

	zw := zip.NewWriter(f)

	dbSnapFile, err := os.CreateTemp(dir, ".igloo-db-snapshot-*.db")
	if err != nil {
		return fmt.Errorf("create db snapshot temp path: %w", err)
	}
	dbSnap := dbSnapFile.Name()
	if err := dbSnapFile.Close(); err != nil {
		_ = os.Remove(dbSnap)
		return fmt.Errorf("close db snapshot temp path: %w", err)
	}
	if err := os.Remove(dbSnap); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("prepare db snapshot path: %w", err)
	}
	if err := m.db.VacuumInto(dbSnap); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	defer func() {
		_ = os.Remove(dbSnap)
	}()

	if err := addFileToZip(zw, dbSnap, config.DatabaseFilename); err != nil {
		return fmt.Errorf("zip db: %w", err)
	}

	configDir := m.cfg.ConfDir
	if configDir != "" {
		if err := addDirToZip(zw, configDir, "config", config.RuntimeConfigBackupAllowed); err != nil {
			return fmt.Errorf("zip config: %w", err)
		}
	}

	if m.backupIncludeMedia() {
		cfg, err := m.db.ExportFullData("")
		if err != nil {
			return fmt.Errorf("export bookmark data: %w", err)
		}
		mediaFiles := exportbundle.CollectBookmarkMedia(m.db, m.cfg.DataDir, cfg.Bookmarks)
		mediaFiles = append(mediaFiles, exportbundle.CollectAvatarMedia(m.cfg.DataDir)...)
		for _, file := range mediaFiles {
			if err := addFileToZip(zw, file.SourcePath, file.ArchivePath); err != nil {
				return fmt.Errorf("zip media %s: %w", file.ArchivePath, err)
			}
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zip: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (m *Manager) backupKeepCount() int {
	return settings.ClampBackupKeepCount(m.db.IntSetting("backup_keep_count"))
}

func (m *Manager) backupIncludeMedia() bool {
	return m.db.BoolSetting("backup_include_media")
}

func (m *Manager) pruneBackups(dir string, keepCount int) {
	keepCount = settings.ClampBackupKeepCount(keepCount)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var backups []string
	for _, e := range entries {
		if isBackupArchiveName(e.Name()) {
			backups = append(backups, e.Name())
		}
	}
	sort.Strings(backups)
	if len(backups) <= keepCount {
		return
	}
	for _, name := range backups[:len(backups)-keepCount] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			slog.Warn("backup: prune failed", "file", name, "err", err)
		} else {
			slog.Info("backup: pruned old backup", "file", name)
		}
	}
}

func isBackupArchiveName(name string) bool {
	return strings.HasPrefix(name, backupPrefix) && (strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz"))
}

func addFileToZip(zw *zip.Writer, srcPath, zipName string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(zipName)
	hdr.Method = zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, f)
	return err
}

func addDirToZip(zw *zip.Writer, srcDir, zipPrefix string, include func(string) bool) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if include != nil && !include(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		zipName := filepath.ToSlash(filepath.Join(zipPrefix, rel))

		if info.IsDir() {
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hdr.Name = strings.TrimSuffix(zipName, "/") + "/"
			_, err = zw.CreateHeader(hdr)
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = zipName
		hdr.Method = zip.Deflate
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			_ = f.Close()
		}()
		_, err = io.Copy(dst, f)
		return err
	})
}
