package fullimport

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/db"
)

func IsZipPayload(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04
}

type RuntimeManifest struct {
	Version   int    `json:"version"`
	DataDir   string `json:"data_dir,omitempty"`
	ConfigDir string `json:"config_dir,omitempty"`
	RepoDir   string `json:"repo_dir,omitempty"`
}

func ReadExportConfig(data []byte) (db.ConfigExport, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return db.ConfigExport{}, fmt.Errorf("open zip: %w", err)
	}
	return readExportConfig(zr)
}

func ImportFullExportZip(store *db.DB, dataDir, configDir, repoDir string, data []byte, userID string, replace bool) (db.ImportResult, int, int, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return db.ImportResult{}, 0, 0, fmt.Errorf("open zip: %w", err)
	}
	cfg, err := readExportConfig(zr)
	if err != nil {
		return db.ImportResult{}, 0, 0, err
	}
	if strings.TrimSpace(userID) == "" && strings.TrimSpace(cfg.UserID) != "" {
		userID = strings.TrimSpace(cfg.UserID)
	}
	sourceRuntime, err := readRuntimeManifest(zr)
	if err != nil {
		return db.ImportResult{}, 0, 0, err
	}
	targetRuntime := RuntimeManifest{
		Version:   1,
		DataDir:   dataDir,
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}
	result, err := store.ImportConfig(cfg, userID, replace)
	if err != nil {
		return result, 0, 0, err
	}
	restoredConfig, err := RestoreFullExportRuntimeConfig(configDir, sourceRuntime, targetRuntime, zr)
	if err != nil {
		return result, 0, restoredConfig, err
	}
	restoredMedia, err := RestoreFullExportBookmarkMedia(store, dataDir, zr)
	return result, restoredMedia, restoredConfig, err
}

func readExportConfig(zr *zip.Reader) (db.ConfigExport, error) {
	var cfg db.ConfigExport
	for _, f := range zr.File {
		if filepath.ToSlash(f.Name) != "export.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return db.ConfigExport{}, fmt.Errorf("open export.json: %w", err)
		}
		raw, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return db.ConfigExport{}, fmt.Errorf("read export.json: %w", readErr)
		}
		if closeErr != nil {
			return db.ConfigExport{}, fmt.Errorf("close export.json: %w", closeErr)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return db.ConfigExport{}, fmt.Errorf("parse export.json: %w", err)
		}
		break
	}
	if cfg.Version == 0 {
		return db.ConfigExport{}, fmt.Errorf("missing export.json")
	}
	return cfg, nil
}

func readRuntimeManifest(zr *zip.Reader) (RuntimeManifest, error) {
	for _, f := range zr.File {
		if filepath.ToSlash(f.Name) != "runtime.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return RuntimeManifest{}, fmt.Errorf("open runtime.json: %w", err)
		}
		raw, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return RuntimeManifest{}, fmt.Errorf("read runtime.json: %w", readErr)
		}
		if closeErr != nil {
			return RuntimeManifest{}, fmt.Errorf("close runtime.json: %w", closeErr)
		}
		var manifest RuntimeManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return RuntimeManifest{}, fmt.Errorf("parse runtime.json: %w", err)
		}
		return manifest, nil
	}
	return RuntimeManifest{}, nil
}

func RestoreFullExportRuntimeConfig(configDir string, source, target RuntimeManifest, zr *zip.Reader) (int, error) {
	if strings.TrimSpace(configDir) == "" {
		return 0, nil
	}
	restored := 0
	for _, f := range zr.File {
		rel, ok := runtimeConfigEntry(f.Name)
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		destAbs := filepath.Join(configDir, rel)
		if !pathWithinDir(configDir, destAbs) {
			return restored, fmt.Errorf("unsafe config path: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destAbs), 0o700); err != nil {
			return restored, fmt.Errorf("create config dir: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			return restored, fmt.Errorf("open config entry %s: %w", f.Name, err)
		}
		raw, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return restored, fmt.Errorf("read config entry %s: %w", f.Name, readErr)
		}
		if closeErr != nil {
			return restored, fmt.Errorf("close config entry %s: %w", f.Name, closeErr)
		}
		raw = rewriteRuntimeConfigPaths(rel, raw, source, target)
		mode := os.FileMode(0o600)
		if err := writeZipEntryBytes(destAbs, raw, ".import-config-*.tmp", mode); err != nil {
			return restored, fmt.Errorf("write config entry %s: %w", f.Name, err)
		}
		restored++
	}
	return restored, nil
}

func runtimeConfigEntry(name string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(name))
	if strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
		return "", false
	}
	if clean == "config" || !strings.HasPrefix(clean, "config/") {
		return "", false
	}
	rel := strings.TrimPrefix(clean, "config/")
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
		return "", false
	}
	return filepath.FromSlash(rel), true
}

func rewriteRuntimeConfigPaths(rel string, data []byte, source, target RuntimeManifest) []byte {
	if filepath.ToSlash(rel) != "nginx.conf" {
		return data
	}
	text := string(data)
	replacements := [][2]string{
		{source.DataDir, target.DataDir},
		{source.ConfigDir, target.ConfigDir},
		{source.RepoDir, target.RepoDir},
	}
	for _, pair := range replacements {
		oldPath := cleanReplacementPath(pair[0])
		newPath := cleanReplacementPath(pair[1])
		if oldPath == "" || newPath == "" || oldPath == newPath {
			continue
		}
		text = strings.ReplaceAll(text, oldPath, newPath)
	}
	return []byte(text)
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

func RestoreFullExportBookmarkMedia(store *db.DB, dataDir string, zr *zip.Reader) (int, error) {
	restored := 0
	for _, f := range zr.File {
		bookmarkID, fileName, ok := bookmarkMediaEntry(f.Name)
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		destRel := filepath.Join("media", "imported", "bookmarks", bookmarkID, fileName)
		destAbs := filepath.Join(dataDir, destRel)
		if !pathWithinDir(dataDir, destAbs) {
			return restored, fmt.Errorf("unsafe media path: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
			return restored, fmt.Errorf("create media dir: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			return restored, fmt.Errorf("open media entry %s: %w", f.Name, err)
		}
		wrote, err := writeZipEntryFile(destAbs, rc)
		closeErr := rc.Close()
		if err != nil {
			return restored, err
		}
		if closeErr != nil {
			return restored, closeErr
		}
		mediaIndex := restoredMediaIndex(fileName)
		mediaType := mediaTypeForPath(fileName)
		if err := recordRestoredBookmarkMedia(store, dataDir, bookmarkID, destRel, mediaIndex, mediaType, wrote); err != nil {
			return restored, err
		}
		restored++
	}
	return restored, nil
}

func bookmarkMediaEntry(name string) (string, string, bool) {
	clean := filepath.ToSlash(filepath.Clean(name))
	if strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
		return "", "", false
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || parts[0] != "media" || parts[1] != "bookmarks" {
		return "", "", false
	}
	bookmarkID := strings.TrimSpace(parts[2])
	fileName := filepath.Base(parts[3])
	if bookmarkID == "" || bookmarkID == "." || bookmarkID == ".." || fileName == "." || fileName == ".." || fileName == "" {
		return "", "", false
	}
	if strings.ContainsAny(bookmarkID, `/\`) || strings.ContainsAny(fileName, `/\`) {
		return "", "", false
	}
	return bookmarkID, fileName, true
}

func pathWithinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

func writeZipEntryFile(dest string, src io.Reader) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".import-media-*.tmp")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			tmp.Close()
		}
		os.Remove(tmpPath)
	}()
	wrote, err := io.Copy(tmp, src)
	if err != nil {
		return wrote, err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return wrote, err
	}
	closed = true
	if err := os.Rename(tmpPath, dest); err != nil {
		return wrote, err
	}
	return wrote, nil
}

func writeZipEntryBytes(dest string, data []byte, tmpPattern string, mode os.FileMode) error {
	if strings.TrimSpace(tmpPattern) == "" {
		tmpPattern = ".import-config-*.tmp"
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), tmpPattern)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			tmp.Close()
		}
		os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if mode != 0 {
		if err := os.Chmod(tmpPath, mode); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}
	return nil
}

func restoredMediaIndex(fileName string) int {
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	idx, err := strconv.Atoi(stem)
	if err != nil || idx < 0 {
		return 0
	}
	return idx
}

func mediaTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".image":
		return "photo"
	case ".gif":
		return "gif"
	case ".mp3", ".m4a", ".ogg", ".aac", ".wav":
		return "audio"
	default:
		return "video"
	}
}

func recordRestoredBookmarkMedia(store *db.DB, dataDir, videoID, relPath string, mediaIndex int, mediaType string, fileSize int64) error {
	return store.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO media_files
				(owner_type, owner_id, media_index, file_path, media_type, file_size)
			VALUES ('feed_media', ?, ?, ?, ?, ?)
			ON CONFLICT(owner_type, owner_id, media_index) DO UPDATE SET
				file_path = excluded.file_path,
				media_type = excluded.media_type,
				file_size = excluded.file_size
		`, videoID, mediaIndex, relPath, mediaType, fileSize); err != nil {
			return err
		}
		if mediaIndex == 0 {
			if _, err := tx.Exec(`
				UPDATE videos
				SET file_path = ?
				WHERE video_id = ?
				  AND TRIM(COALESCE(file_path, '')) = ''
			`, filepath.Join(dataDir, relPath), videoID); err != nil {
				return err
			}
		}
		return nil
	})
}
