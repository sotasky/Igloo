package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func TestRunImportsCurrentFullExportZipFreshInstall(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", dataDir)
	t.Setenv("IGLOO_CONFIG_DIR", configDir)
	t.Setenv("IGLOO_REPO_DIR", filepath.Clean("../.."))

	zipPath := filepath.Join(t.TempDir(), "igloo-full-test.zip")
	writeFullExportZipFixture(t, zipPath)

	var stdout, stderr strings.Builder
	if code := run([]string{"--replace", zipPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "format=full_export_zip") {
		t.Fatalf("stdout missing format summary: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "owner=bootstrap") {
		t.Fatalf("stdout missing bootstrap owner summary: %s", stdout.String())
	}

	store, err := db.Open(filepath.Join(dataDir, "igloo.db"), dataDir)
	if err != nil {
		t.Fatalf("open imported db: %v", err)
	}
	defer store.Close()

	var categoryUser, bookmarkUser, likeUser string
	if err := store.QueryRow(`SELECT user_id FROM bookmark_categories WHERE name='Watch Later'`).Scan(&categoryUser); err != nil {
		t.Fatalf("category missing: %v", err)
	}
	if err := store.QueryRow(`SELECT user_id FROM bookmarks WHERE video_id='booked_video'`).Scan(&bookmarkUser); err != nil {
		t.Fatalf("bookmark missing: %v", err)
	}
	if err := store.QueryRow(`SELECT username FROM feed_likes WHERE tweet_id='liked_post'`).Scan(&likeUser); err != nil {
		t.Fatalf("like missing: %v", err)
	}
	if categoryUser != "" || bookmarkUser != "" || likeUser != "" {
		t.Fatalf("fresh import owner = category %q bookmark %q like %q, want bootstrap blanks", categoryUser, bookmarkUser, likeUser)
	}

	var mediaRel, videoPath string
	if err := store.QueryRow(`SELECT file_path FROM media_files WHERE owner_type='feed_media' AND owner_id='booked_video' AND media_index=0`).Scan(&mediaRel); err != nil {
		t.Fatalf("media_files row missing: %v", err)
	}
	if err := store.QueryRow(`SELECT file_path FROM videos WHERE video_id='booked_video'`).Scan(&videoPath); err != nil {
		t.Fatalf("video row missing: %v", err)
	}
	if mediaRel != filepath.ToSlash(filepath.Join("media", "imported", "bookmarks", "booked_video", "000.mp4")) {
		t.Fatalf("media file_path = %q", mediaRel)
	}
	if videoPath != filepath.Join(dataDir, mediaRel) {
		t.Fatalf("video file_path = %q, want %q", videoPath, filepath.Join(dataDir, mediaRel))
	}
	restored, err := os.ReadFile(filepath.Join(dataDir, mediaRel))
	if err != nil {
		t.Fatalf("read restored media: %v", err)
	}
	if string(restored) != "bookmarked-video-bytes" {
		t.Fatalf("restored media = %q", string(restored))
	}

	if code := run([]string{"--replace", zipPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("second run exit = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var bookmarkCount, mediaCount int
	if err := store.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id='booked_video'`).Scan(&bookmarkCount); err != nil {
		t.Fatalf("bookmark count: %v", err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM media_files WHERE owner_type='feed_media' AND owner_id='booked_video'`).Scan(&mediaCount); err != nil {
		t.Fatalf("media count: %v", err)
	}
	if bookmarkCount != 1 || mediaCount != 1 {
		t.Fatalf("after rerun bookmarkCount=%d mediaCount=%d, want 1/1", bookmarkCount, mediaCount)
	}
}

func writeFullExportZipFixture(t *testing.T, path string) {
	t.Helper()

	out, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	exportFile, err := zw.Create("export.json")
	if err != nil {
		t.Fatalf("create export.json: %v", err)
	}
	cfg := db.ConfigExport{
		Version:    1,
		ExportedAt: time.Unix(1700000000, 0).UTC(),
		Settings: map[string]string{
			"starting_page": "feed",
		},
		Subscriptions: []db.ChannelExport{{
			ChannelID: "youtube_UCfresh",
			Name:      "Fresh Channel",
			Platform:  "youtube",
			IsStarred: true,
		}},
		BookmarkCategories: []db.BookmarkCatExport{{
			Name: "Watch Later",
		}},
		Bookmarks: []db.BookmarkExport{{
			VideoID:      "booked_video",
			CategoryName: "Watch Later",
			CustomTitle:  "Saved clip",
		}},
		LikedPosts: []db.LikedPostExport{{
			TweetID:      "liked_post",
			AuthorHandle: "author",
			BodyText:     "liked text",
			Platform:     "twitter",
			PublishedAt:  "2026-05-01T12:00:00Z",
		}},
		BookmarkedVideos: []db.BookmarkedVideoExport{{
			VideoID:      "booked_video",
			ChannelID:    "youtube_UCfresh",
			Title:        "Saved Video",
			Platform:     "youtube",
			Duration:     42,
			PublishedAt:  "2026-05-01T12:00:00Z",
			CategoryName: "Watch Later",
		}},
	}
	if err := json.NewEncoder(exportFile).Encode(cfg); err != nil {
		t.Fatalf("encode export.json: %v", err)
	}
	mediaFile, err := zw.Create("media/bookmarks/booked_video/000.mp4")
	if err != nil {
		t.Fatalf("create media entry: %v", err)
	}
	if _, err := mediaFile.Write([]byte("bookmarked-video-bytes")); err != nil {
		t.Fatalf("write media entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}
