package db

import (
	"strings"
	"testing"
)

func TestInsertVideoPreservesExistingMetadataOnPartialOverwrite(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.InsertVideo(
		"vid_1",
		"youtube_alice",
		"Original title",
		"Original description",
		125,
		"thumbs/original.webp",
		"videos/original.mp4",
		1024,
		1_700_000_000_000,
		`{"duration":125,"thumbnail":"keep"}`,
		"video",
		0,
		false,
	); err != nil {
		t.Fatalf("initial InsertVideo: %v", err)
	}

	if err := d.InsertVideo(
		"vid_1",
		"youtube_alice",
		"",
		"",
		0,
		"",
		"videos/redownload.mp4",
		2048,
		0,
		"",
		"",
		0,
		false,
	); err != nil {
		t.Fatalf("partial InsertVideo: %v", err)
	}

	video, err := d.GetVideo("vid_1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if video == nil {
		t.Fatal("GetVideo returned nil")
	}
	if video.Duration != 125 {
		t.Fatalf("duration = %d, want 125", video.Duration)
	}
	if !strings.HasSuffix(video.ThumbnailPath, "thumbs/original.webp") {
		t.Fatalf("thumbnail_path = %q, want preserved value", video.ThumbnailPath)
	}
	if video.PublishedAt == nil || video.PublishedAt.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("published_at = %v, want preserved value", video.PublishedAt)
	}
	if video.MetadataJSON != `{"duration":125,"thumbnail":"keep"}` {
		t.Fatalf("metadata_json = %q, want preserved value", video.MetadataJSON)
	}
	if !strings.HasSuffix(video.FilePath, "videos/redownload.mp4") {
		t.Fatalf("file_path = %q, want latest non-empty value", video.FilePath)
	}
}
