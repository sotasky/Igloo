package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

	files, err := m.prepareCompletedVideoFiles(context.Background(), "youtube", "sample-attempt-123", download.CompletedDownload{
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
	files.removeTransientFiles()
	if _, err := os.Stat(exactThumbnail); !os.IsNotExist(err) {
		t.Fatalf("producer thumbnail sidecar was not retired: %v", err)
	}
	if body, err := os.ReadFile(decoyThumbnail); err != nil || string(body) != "decoy thumbnail" {
		t.Fatalf("same-directory decoy was touched: body=%q err=%v", body, err)
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

	attemptID, err := newDownloadAttemptID(mediaDir, videoID)
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
	files, err := m.prepareCompletedVideoFiles(context.Background(), "youtube", attemptID, completed)
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
	files.removeFailedAttempt(completed)

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

func TestEnqueueCompletedVideoPreviewUsesExactOwnerKind(t *testing.T) {
	root := t.TempDir()
	m := &Manager{cfg: testCfg(root), db: newTestWorkerDBAt(t, root), previewChan: make(chan PreviewRequest, 2)}
	key := "media/tiktok/sample_video.mp4"
	path, err := m.cfg.Storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tiktok stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.db.StoreReadyAsset(db.Asset{
		AssetID:   db.BuildAssetID("tiktok", "tiktok_video", "sample_video", "video_stream", 0),
		AssetKind: "video_stream", OwnerKind: "tiktok_video", OwnerID: "sample_video", FilePath: key,
	}, 1); err != nil {
		t.Fatal(err)
	}

	m.enqueueCompletedVideoPreview("sample_video", "youtube", path, 30)
	if got := len(m.previewChan); got != 0 {
		t.Fatalf("colliding tiktok asset queued as youtube: queue length = %d", got)
	}
	m.enqueueCompletedVideoPreview("sample_video", "tiktok", path, 30)
	if got := len(m.previewChan); got != 1 {
		t.Fatalf("exact tiktok asset was not queued: queue length = %d", got)
	}
	req := <-m.previewChan
	if req.OwnerKind != "tiktok_video" {
		t.Fatalf("queued owner kind = %q", req.OwnerKind)
	}
}
