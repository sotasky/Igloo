package db

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestGetMediaFileAudioPathUsesExtension(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type)
		VALUES ('feed_media', 'audio_owner_001', 4, 'media/tiktok/demo/audio_owner_001_0.mp3', 'video')
	`); err != nil {
		t.Fatalf("insert media file: %v", err)
	}

	path, err := d.GetMediaFileAudioPath("feed_media", "audio_owner_001")
	if err != nil {
		t.Fatalf("GetMediaFileAudioPath: %v", err)
	}
	if path != "media/tiktok/demo/audio_owner_001_0.mp3" {
		t.Fatalf("path = %q, want mp3 path", path)
	}
}

func TestInsertMediaFileBatchRepairsVideoSlideshowShape(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, media_kind, slide_count, published_at, sync_seq)
		VALUES ('slide_video_001', 'tiktok_demo_author', 'Slide video', 0, '', '', 0, 1, 1)
	`); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	if err := d.InsertMediaFileBatch([]model.MediaFile{
		{OwnerType: "feed_media", OwnerID: "slide_video_001", MediaIndex: 0, FilePath: "media/tiktok/demo/slide_video_001_0_1.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: "slide_video_001", MediaIndex: 1, FilePath: "media/tiktok/demo/slide_video_001_0_2.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: "slide_video_001", MediaIndex: 2, FilePath: "media/tiktok/demo/slide_video_001_0_3.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: "slide_video_001", MediaIndex: 3, FilePath: "media/tiktok/demo/slide_video_001_0_4.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: "slide_video_001", MediaIndex: 4, FilePath: "media/tiktok/demo/slide_video_001_0.mp3", MediaType: "video"},
	}); err != nil {
		t.Fatalf("InsertMediaFileBatch: %v", err)
	}

	var mediaKind string
	var slideCount int
	if err := d.QueryRow(`
		SELECT COALESCE(media_kind, ''), COALESCE(slide_count, 0)
		FROM videos
		WHERE video_id = 'slide_video_001'
	`).Scan(&mediaKind, &slideCount); err != nil {
		t.Fatalf("query repaired video: %v", err)
	}
	if mediaKind != "slideshow" {
		t.Fatalf("media_kind = %q, want slideshow", mediaKind)
	}
	if slideCount != 4 {
		t.Fatalf("slide_count = %d, want 4", slideCount)
	}
}
