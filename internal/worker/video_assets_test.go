package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/storage"
)

func TestPrepareCompletedVideoFilesKeepsMediaExternalAndUsesExactThumbnail(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	mediaRoot := filepath.Join(t.TempDir(), "bulk")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, ".igloo-media-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	layout, err := storage.New(stateRoot, mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	m := &Manager{cfg: &config.Config{Storage: layout}}

	mediaDir := filepath.Join(mediaRoot, "youtube")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(mediaDir, "sample.mp4")
	exactThumbnail := filepath.Join(mediaDir, "producer-thumbnail.jpg")
	decoyThumbnail := filepath.Join(mediaDir, "sample.jpg")
	subtitlePath, err := layout.Path("subtitles/youtube/sample-attempt-123.en.vtt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(subtitlePath), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		videoPath: "video bytes", exactThumbnail: "exact thumbnail", decoyThumbnail: "decoy thumbnail",
		subtitlePath: "WEBVTT\n\nsubtitle",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := m.prepareCompletedVideoFiles(context.Background(), download.MediaLaneBulkForeground, "youtube", "sample-attempt-123", download.CompletedDownload{
		MediaPaths:    []string{videoPath},
		ThumbnailPath: exactThumbnail,
		SubtitlePaths: []string{subtitlePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if files.primaryKey != "media/youtube/sample.mp4" {
		t.Fatalf("primary key = %q", files.primaryKey)
	}
	if len(files.assets) != 2 || files.assets[0].AssetKind != "video_stream" || files.assets[1].AssetKind != "post_thumbnail" {
		t.Fatalf("main asset set = %+v", files.assets)
	}
	thumbnailKey := files.assets[1].FilePath
	if thumbnailKey != "thumbnails/videos/youtube/sample-attempt-123.jpg" {
		t.Fatalf("thumbnail key = %q", thumbnailKey)
	}
	if len(files.subtitleAssets) != 1 || files.subtitleAssets[0].AssetKind != "subtitle" {
		t.Fatalf("subtitle asset set = %+v", files.subtitleAssets)
	}
	storedThumbnail, err := layout.Path(thumbnailKey)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(storedThumbnail)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "exact thumbnail" {
		t.Fatalf("stored thumbnail came from another file: %q", body)
	}
	executor := storage.NewMediaExecutor()
	m.downloader = download.NewDownloader("")
	m.downloader.SetMediaExecutor(executor)
	backgroundHeld := make(chan struct{})
	releaseBackground := make(chan struct{})
	backgroundDone := make(chan struct{})
	go func() {
		defer close(backgroundDone)
		_ = executor.Run(context.Background(), storage.MediaLaneBulkBackground, func() error {
			close(backgroundHeld)
			<-releaseBackground
			return nil
		})
	}()
	<-backgroundHeld
	cleanupDone := make(chan struct{})
	go func() {
		m.removeTransientFiles(context.Background(), download.MediaLaneBulkForeground, files)
		close(cleanupDone)
	}()
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		close(releaseBackground)
		<-backgroundDone
		t.Fatal("foreground cleanup waited for historical media work")
	}
	close(releaseBackground)
	<-backgroundDone
	if _, err := os.Stat(exactThumbnail); !os.IsNotExist(err) {
		t.Fatalf("producer thumbnail sidecar was not retired: %v", err)
	}
	if body, err := os.ReadFile(decoyThumbnail); err != nil || string(body) != "decoy thumbnail" {
		t.Fatalf("same-directory decoy was touched: body=%q err=%v", body, err)
	}
}

func TestReusableCompletedVideoUsesOnlyCanonicalFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"sample.mp4":                   "video",
		"sample.webp":                  "thumbnail",
		"sample.info.json":             `{}`,
		"sample.en.vtt":                "WEBVTT\n",
		"sample-attempt-deadbeef.mp4":  "partial",
		"sample.f137.mp4":              "format fragment",
		"sample_other-not-indexed.jpg": "not canonical",
		"sample2.mp4":                  "another video",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	completed, err := reusableCompletedVideoAdmitted(dir, "sample", "youtube")
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.MediaPaths) != 1 || filepath.Base(completed.MediaPaths[0]) != "sample.mp4" {
		t.Fatalf("media paths = %v", completed.MediaPaths)
	}
	if filepath.Base(completed.ThumbnailPath) != "sample.webp" {
		t.Fatalf("thumbnail path = %q", completed.ThumbnailPath)
	}
	if filepath.Base(completed.InfoJSONPath) != "sample.info.json" {
		t.Fatalf("info path = %q", completed.InfoJSONPath)
	}
	if len(completed.SubtitlePaths) != 1 || filepath.Base(completed.SubtitlePaths[0]) != "sample.en.vtt" {
		t.Fatalf("subtitle paths = %v", completed.SubtitlePaths)
	}
}

func TestReusableCompletedVideoRejectsLoneThumbnail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.webp"), []byte("thumbnail"), 0o644); err != nil {
		t.Fatal(err)
	}
	completed, err := reusableCompletedVideoAdmitted(dir, "sample", "youtube")
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.MediaPaths) != 0 {
		t.Fatalf("lone thumbnail was adopted as media: %v", completed.MediaPaths)
	}
}

func TestReusableCompletedVideoRecognizesNumberedSlideshow(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sample.jpg", "sample_2.jpg", "sample_3.webp", "sample_note.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	completed, err := reusableCompletedVideoAdmitted(dir, "sample", "tiktok")
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.MediaPaths) != 3 {
		t.Fatalf("media paths = %v", completed.MediaPaths)
	}
	for _, path := range completed.MediaPaths {
		if filepath.Base(path) == "sample_note.jpg" {
			t.Fatalf("non-indexed sibling was adopted: %v", completed.MediaPaths)
		}
	}
}

func TestFailedRedownloadPreservesReadyBytesAndRemovesOnlyAttemptOutputs(t *testing.T) {
	stateRoot := t.TempDir()
	m := &Manager{cfg: testCfg(stateRoot), db: newTestWorkerDBAt(t, stateRoot)}
	videoID := "sample_video"
	mediaDir, err := m.cfg.Storage.Path("media/youtube")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldKey := "media/youtube/sample_video-old.mp4"
	oldPath, _ := m.cfg.Storage.Path(oldKey)
	if err := os.WriteFile(oldPath, []byte("ready bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video",
		Assets: []db.Asset{{AssetKind: "video_stream", FilePath: oldKey}},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := m.db.GetAssetByOwnerIdentity("video_stream", "youtube_video", videoID, 0)
	if err != nil || before == nil {
		t.Fatalf("ready stream: %+v %v", before, err)
	}

	attemptID, err := newDownloadAttemptID(videoID)
	if err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(mediaDir, attemptID+".mp4")
	thumbPath := filepath.Join(mediaDir, attemptID+".jpg")
	infoPath := filepath.Join(mediaDir, attemptID+".info.json")
	subtitleDir, _ := m.cfg.Storage.Path("subtitles/youtube")
	if err := os.MkdirAll(subtitleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subtitlePath := filepath.Join(subtitleDir, attemptID+".en.vtt")
	decoyPath := filepath.Join(mediaDir, videoID+"-decoy.mp4")
	for path, body := range map[string]string{
		mediaPath: "new bytes", thumbPath: "new thumb", infoPath: `{}`,
		subtitlePath: "WEBVTT\n\nnew", decoyPath: "decoy",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	completed := download.CompletedDownload{
		MediaPaths: []string{mediaPath}, ThumbnailPath: thumbPath,
		InfoJSONPath: infoPath, SubtitlePaths: []string{subtitlePath},
	}
	files, err := m.prepareCompletedVideoFiles(context.Background(), download.MediaLaneBulkForeground, "youtube", attemptID, completed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(mediaPath); err != nil {
		t.Fatal(err)
	}
	err = m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video", Assets: files.assets,
	})
	if err == nil {
		t.Fatal("redownload unexpectedly committed after its media disappeared")
	}
	m.removeFailedAttempt(context.Background(), download.MediaLaneBulkForeground, files, completed)

	after, err := m.db.GetAssetByOwnerIdentity("video_stream", "youtube_video", videoID, 0)
	if err != nil || after == nil || after.FilePath != oldKey || after.SHA256 != before.SHA256 {
		t.Fatalf("failed attempt changed ready stream: before=%+v after=%+v err=%v", before, after, err)
	}
	if body, err := os.ReadFile(oldPath); err != nil || string(body) != "ready bytes" {
		t.Fatalf("ready bytes changed: body=%q err=%v", body, err)
	}
	for _, path := range append([]string{thumbPath, infoPath, subtitlePath}, files.materialized...) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("attempt output survived cleanup: %s", path)
		}
	}
	if body, err := os.ReadFile(decoyPath); err != nil || string(body) != "decoy" {
		t.Fatalf("unrelated file was touched: body=%q err=%v", body, err)
	}
}
