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

func ImportFullExportZip(store *db.DB, dataDir string, data []byte, userID string, replace bool) (db.ImportResult, int, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return db.ImportResult{}, 0, fmt.Errorf("open zip: %w", err)
	}
	var cfg db.ConfigExport
	for _, f := range zr.File {
		if filepath.ToSlash(f.Name) != "export.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return db.ImportResult{}, 0, fmt.Errorf("open export.json: %w", err)
		}
		raw, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return db.ImportResult{}, 0, fmt.Errorf("read export.json: %w", readErr)
		}
		if closeErr != nil {
			return db.ImportResult{}, 0, fmt.Errorf("close export.json: %w", closeErr)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return db.ImportResult{}, 0, fmt.Errorf("parse export.json: %w", err)
		}
		break
	}
	if cfg.Version == 0 {
		return db.ImportResult{}, 0, fmt.Errorf("missing export.json")
	}
	result, err := store.ImportConfig(cfg, userID, replace)
	if err != nil {
		return result, 0, err
	}
	restored, err := RestoreFullExportBookmarkMedia(store, dataDir, zr)
	return result, restored, err
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
