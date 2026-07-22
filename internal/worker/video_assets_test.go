package worker

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/storage"
)

func TestPrepareCompletedVideoFilesKeepsMediaExternalAndDefersExactThumbnail(t *testing.T) {
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

	files, err := m.prepareCompletedVideoFiles(context.Background(), download.MediaLaneBulkForeground, download.CompletedDownload{
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
	if len(files.assets) != 1 || files.assets[0].AssetKind != "video_stream" {
		t.Fatalf("main asset set = %+v", files.assets)
	}
	if files.thumbnailImageSource != exactThumbnail || files.thumbnailVideoSource != videoPath {
		t.Fatalf("thumbnail sources = (%q, %q)", files.thumbnailImageSource, files.thumbnailVideoSource)
	}
	if len(files.subtitleAssets) != 1 || files.subtitleAssets[0].AssetKind != "subtitle" {
		t.Fatalf("subtitle asset set = %+v", files.subtitleAssets)
	}
	storedThumbnail, err := layout.Path("thumbnails/videos/youtube/sample-attempt-123.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(storedThumbnail); !os.IsNotExist(err) {
		t.Fatalf("thumbnail was materialized during primary preparation: %v", err)
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

func TestDownloadOutputIDUsesTheStableVideoID(t *testing.T) {
	got, err := downloadOutputID("sample_video")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sample_video" {
		t.Fatalf("output id = %q, want stable video id", got)
	}
}

func TestDownloadVideoPublishesPrimaryAndQueuesSubtitleBeforeBlockedThumbnail(t *testing.T) {
	root := t.TempDir()
	database := newTestWorkerDBAt(t, root)
	cfg := testCfg(root)
	m := NewManager(database, cfg)
	defer m.Shutdown()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(root, "ffmpeg-started")
	release := filepath.Join(root, "ffmpeg-release")
	frame := filepath.Join(root, "frame.jpg")
	frameFile, err := os.Create(frame)
	if err != nil {
		t.Fatal(err)
	}
	frameImage := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			frameImage.Set(x, y, color.White)
		}
	}
	if err := jpeg.Encode(frameFile, frameImage, nil); err != nil {
		_ = frameFile.Close()
		t.Fatal(err)
	}
	if err := frameFile.Close(); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
: > "$IGLOO_FFMPEG_STARTED"
while [ ! -e "$IGLOO_FFMPEG_RELEASE" ]; do sleep 0.01; done
for output in "$@"; do :; done
cp "$IGLOO_FFMPEG_FRAME" "$output"
`
	if err := os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("IGLOO_FFMPEG_STARTED", started)
	t.Setenv("IGLOO_FFMPEG_RELEASE", release)
	t.Setenv("IGLOO_FFMPEG_FRAME", frame)
	defer func() { _ = os.WriteFile(release, nil, 0o644) }()

	mediaDir, err := cfg.Storage.WritePath("media/instagram/sample_author")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const videoID = "sample_video"
	const attemptID = "sample_attempt"
	videoPath := filepath.Join(mediaDir, attemptID+".mp4")
	if err := os.WriteFile(videoPath, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	completed := download.CompletedDownload{MediaPaths: []string{videoPath}}
	files, err := m.prepareCompletedVideoFiles(context.Background(), download.MediaLaneBulkRegular, completed)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	isAuto := true
	subtitle := db.Asset{
		AssetID:        db.BuildAssetID("instagram", "instagram_reel", videoID, "subtitle", 0),
		AssetKind:      "subtitle",
		OwnerKind:      "instagram_reel",
		OwnerID:        videoID,
		SourceURL:      "https://example.test/post/sample_video",
		ContentType:    "text/vtt",
		DownloadLane:   db.DownloadLaneBackfill,
		IsAuto:         &isAuto,
		RequiredReason: "retention",
	}
	go func() {
		done <- m.storeCompletedVideoOutputs(
			context.Background(), download.MediaLaneBulkRegular, "instagram", attemptID,
			db.CompletedVideo{
				VideoID: videoID, ChannelID: "instagram_sample_author", OwnerKind: "instagram_reel", Assets: files.assets,
			},
			files, completed, &subtitle,
		)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("publication finished before thumbnail extraction: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("thumbnail extraction did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	primary, err := database.GetReadyVideoPrimaryAsset(videoID)
	if err != nil || primary == nil {
		t.Fatalf("primary media was not published before thumbnail extraction: %+v %v", primary, err)
	}
	queuedSubtitle, err := database.GetAsset(subtitle.AssetID, subtitle.AssetKind)
	if err != nil || queuedSubtitle == nil || queuedSubtitle.State != db.AssetStateQueued || queuedSubtitle.IsAuto == nil {
		t.Fatalf("subtitle was not queued after primary publication: %+v %v", queuedSubtitle, err)
	}
	thumbnail, err := database.GetAssetByOwnerIdentity("post_thumbnail", "instagram_reel", videoID, 0)
	if err != nil || thumbnail != nil {
		t.Fatalf("thumbnail was published while extraction was blocked: %+v %v", thumbnail, err)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("download did not finish after thumbnail extraction was released")
	}
	thumbnail, err = database.GetAssetByOwnerIdentity("post_thumbnail", "instagram_reel", videoID, 0)
	if err != nil || thumbnail == nil || thumbnail.State != db.AssetStateReady {
		t.Fatalf("thumbnail was not published after extraction: %+v %v", thumbnail, err)
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

	attemptID, err := downloadOutputID(videoID)
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
	files, err := m.prepareCompletedVideoFiles(context.Background(), download.MediaLaneBulkForeground, completed)
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
	if err != nil || after == nil || after.FilePath != oldKey || after.FileMtimeNs != before.FileMtimeNs {
		t.Fatalf("failed attempt changed ready stream: before=%+v after=%+v err=%v", before, after, err)
	}
	if body, err := os.ReadFile(oldPath); err != nil || string(body) != "ready bytes" {
		t.Fatalf("ready bytes changed: body=%q err=%v", body, err)
	}
	for _, path := range []string{thumbPath, infoPath, subtitlePath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("attempt output survived cleanup: %s", path)
		}
	}
	if body, err := os.ReadFile(decoyPath); err != nil || string(body) != "decoy" {
		t.Fatalf("unrelated file was touched: body=%q err=%v", body, err)
	}
}
