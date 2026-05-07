package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestMigrateMediaPathsSkipsDiskWalkWhenLegacyFeedMediaIsGone(t *testing.T) {
	d := newTestWorkerDB(t)
	dataDir := t.TempDir()
	m := &Manager{db: d, cfg: testCfg(dataDir)}

	if err := d.InsertMediaFile(model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    "tweet_existing",
		MediaIndex: 0,
		FilePath:   filepath.Join("media", "twitter", "alice", "tweet_existing.jpg"),
		MediaType:  "photo",
	}); err != nil {
		t.Fatalf("seed existing media file: %v", err)
	}

	mediaDir := filepath.Join(dataDir, "media", "twitter", "alice")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "tweet_new.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatalf("write unindexed media file: %v", err)
	}

	m.migrateMediaPaths()

	files, err := d.GetMediaFilesByOwnerType("feed_media")
	if err != nil {
		t.Fatalf("GetMediaFilesByOwnerType: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected startup migration to skip full disk reindex, got %d media rows", len(files))
	}
}

func TestMigrateMediaPathsIndexesLegacyFlatFilesWithoutExistingRows(t *testing.T) {
	d := newTestWorkerDB(t)
	dataDir := t.TempDir()
	m := &Manager{db: d, cfg: testCfg(dataDir)}

	legacyDir := filepath.Join(dataDir, "feed_media")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy media dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "tweet_legacy.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatalf("write legacy media file: %v", err)
	}

	m.migrateMediaPaths()

	files, err := d.GetMediaFilesByOwnerType("feed_media")
	if err != nil {
		t.Fatalf("GetMediaFilesByOwnerType: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected legacy flat media file to be indexed, got %d media rows", len(files))
	}
	if files[0].FilePath != filepath.Join("feed_media", "tweet_legacy.jpg") {
		t.Fatalf("unexpected indexed path %q", files[0].FilePath)
	}
}
