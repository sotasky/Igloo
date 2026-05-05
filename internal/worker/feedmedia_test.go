package worker

import (
	"os"
	"testing"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestToRelPath(t *testing.T) {
	tests := []struct {
		baseDir string
		absPath string
		want    string
	}{
		{"/data", "/data/media/twitter/user/file.jpg", "media/twitter/user/file.jpg"},
		{"/data", "/other/path/file.jpg", "/other/path/file.jpg"},
		{"/data/", "/data/media/file.jpg", "media/file.jpg"},
		{"/data", "/data/file.jpg", "file.jpg"},
	}
	for _, tt := range tests {
		got := toRelPath(tt.baseDir, tt.absPath)
		if got != tt.want {
			t.Errorf("toRelPath(%q, %q) = %q, want %q", tt.baseDir, tt.absPath, got, tt.want)
		}
	}
}

// TestQuoteMediaDBInsertion verifies that quote_media records can be inserted
// and queried via the same media_files table used for feed_media.
func TestQuoteMediaDBInsertion(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "igloo-feedmedia-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	d, err := db.Open(tmpPath, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	// Insert parent media + quote media for the same parent tweet.
	files := []model.MediaFile{
		{
			OwnerType:  "feed_media",
			OwnerID:    "1234567890",
			MediaIndex: 0,
			FilePath:   "media/twitter/userA/1234567890_0.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/parent.jpg",
		},
		{
			OwnerType:  "quote_media",
			OwnerID:    "9876543210",
			MediaIndex: 0,
			FilePath:   "media/twitter/userB/9876543210_0.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/quote.jpg",
		},
		{
			OwnerType:  "quote_media",
			OwnerID:    "9876543210",
			MediaIndex: 1,
			FilePath:   "media/twitter/userB/9876543210_1.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/quote2.jpg",
		},
	}

	if err := d.InsertMediaFileBatch(files); err != nil {
		t.Fatalf("InsertMediaFileBatch: %v", err)
	}

	// Verify feed_media records.
	feedFiles, err := d.GetMediaFilesByOwnerType("feed_media")
	if err != nil {
		t.Fatalf("GetMediaFilesByOwnerType(feed_media): %v", err)
	}
	if len(feedFiles) != 1 {
		t.Errorf("expected 1 feed_media file, got %d", len(feedFiles))
	}

	// Verify quote_media records.
	quoteFiles, err := d.GetMediaFilesByOwnerType("quote_media")
	if err != nil {
		t.Fatalf("GetMediaFilesByOwnerType(quote_media): %v", err)
	}
	if len(quoteFiles) != 2 {
		t.Errorf("expected 2 quote_media files, got %d", len(quoteFiles))
	}

	// Verify individual lookup works for quote_media.
	path, err := d.GetMediaFilePath("quote_media", "9876543210", 0)
	if err != nil {
		t.Fatalf("GetMediaFilePath(quote_media, 0): %v", err)
	}
	if path != "media/twitter/userB/9876543210_0.jpg" {
		t.Errorf("unexpected path: %s", path)
	}

	// Verify UNIQUE constraint: re-inserting same (owner_type, owner_id, media_index) is ignored.
	dupeFiles := []model.MediaFile{
		{
			OwnerType:  "quote_media",
			OwnerID:    "9876543210",
			MediaIndex: 0,
			FilePath:   "media/twitter/userB/different.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/different.jpg",
		},
	}
	if err := d.InsertMediaFileBatch(dupeFiles); err != nil {
		t.Fatalf("InsertMediaFileBatch (dupe): %v", err)
	}

	// Path should remain the original (INSERT OR IGNORE).
	path, err = d.GetMediaFilePath("quote_media", "9876543210", 0)
	if err != nil {
		t.Fatalf("GetMediaFilePath after dupe: %v", err)
	}
	if path != "media/twitter/userB/9876543210_0.jpg" {
		t.Errorf("UNIQUE violation: path changed to %s", path)
	}
}

// TestParseMediaIncludesQuoteMedia verifies that ParseMedia() populates
// both Media and QuoteMedia slices from their respective JSON fields.
func TestParseMediaIncludesQuoteMedia(t *testing.T) {
	item := model.FeedItem{
		MediaJSON:      `[{"url":"https://pbs.twimg.com/media/parent.jpg","type":"photo"}]`,
		QuoteMediaJSON: `[{"url":"https://pbs.twimg.com/media/quote.jpg","type":"photo"},{"url":"https://pbs.twimg.com/media/quote2.jpg","type":"photo"}]`,
	}
	item.ParseMedia()

	if len(item.Media) != 1 {
		t.Errorf("expected 1 parent media ref, got %d", len(item.Media))
	}
	if len(item.QuoteMedia) != 2 {
		t.Errorf("expected 2 quote media refs, got %d", len(item.QuoteMedia))
	}
	if len(item.QuoteMedia) > 0 && item.QuoteMedia[0].Type != "photo" {
		t.Errorf("expected quote media type 'photo', got %q", item.QuoteMedia[0].Type)
	}
}
