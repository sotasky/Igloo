package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestDeleteVideoFilesKeepsStillReferencedPath(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	dbPath := filepath.Join(tmpDir, "test.db")
	d, err := db.Open(dbPath, dataDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	relPath := filepath.Join("videos", "sample", "shared.mp4")
	thumbRelPath := filepath.Join("videos", "sample", "shared.jpg")
	absPath := filepath.Join(dataDir, relPath)
	thumbAbsPath := filepath.Join(dataDir, thumbRelPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir video dir: %v", err)
	}
	if err := os.WriteFile(absPath, []byte("shared-video"), 0o644); err != nil {
		t.Fatalf("write video file: %v", err)
	}
	if err := os.WriteFile(thumbAbsPath, []byte("shared-thumb"), 0o644); err != nil {
		t.Fatalf("write thumbnail file: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES ('youtube_sample', 'youtube_sample', 'Sample', '', 'youtube', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, file_path, thumbnail_path, file_size, published_at)
		VALUES ('sample_video_kept', 'youtube_sample', 'Kept', ?, ?, 12, 1)
	`, relPath, thumbRelPath); err != nil {
		t.Fatalf("insert kept video: %v", err)
	}

	s := &Server{db: d, cfg: &config.Config{DataDir: dataDir}}
	s.deleteVideoFiles(model.Video{VideoID: "sample_video_deleted", FilePath: relPath})
	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("shared file was removed while still referenced: %v", err)
	}
	if _, err := os.Stat(thumbAbsPath); err != nil {
		t.Fatalf("shared sibling thumbnail was removed while still referenced: %v", err)
	}

	if err := d.ExecRaw(`DELETE FROM videos WHERE video_id = 'sample_video_kept'`); err != nil {
		t.Fatalf("delete kept video row: %v", err)
	}
	s.deleteVideoFiles(model.Video{VideoID: "sample_video_deleted", FilePath: relPath})
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced file still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(thumbAbsPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced sibling thumbnail still exists or stat failed unexpectedly: %v", err)
	}
}
