package db

import (
	"testing"
)

func TestInsertVideoPreservesExistingMetadataOnPartialOverwrite(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.InsertVideo(
		"vid_1",
		"youtube_alice",
		"youtube_video",
		"Original title",
		"Original description",
		125,
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
		"youtube_video",
		"",
		"",
		0,
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
	if video.PublishedAt == nil || video.PublishedAt.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("published_at = %v, want preserved value", video.PublishedAt)
	}
	if video.MetadataJSON != `{"duration":125,"thumbnail":"keep"}` {
		t.Fatalf("metadata_json = %q, want preserved value", video.MetadataJSON)
	}
}
