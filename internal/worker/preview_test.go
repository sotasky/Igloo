package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func TestPreviewFrameCount(t *testing.T) {
	tests := []struct {
		duration float64
		want     int
	}{
		{1, 1},       // 1 second
		{10, 2},      // 10 seconds
		{60, 12},     // 1 minute
		{600, 80},    // 10 minutes
		{2700, 80},   // 45 minutes
		{2701, 120},  // just over 45 min
		{7200, 120},  // 2 hours
		{10800, 120}, // 3 hours
		{10801, 160}, // just over 3 hours
		{21600, 160}, // 6 hours
		{21601, 200}, // just over 6 hours
		{0, 0},       // zero duration
	}
	for _, tt := range tests {
		got := previewFrameCount(tt.duration)
		if got != tt.want {
			t.Errorf("previewFrameCount(%.0f) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}

func TestBuildPreviewTrackJSON(t *testing.T) {
	raw, err := buildPreviewTrackJSON(30.0, 2, 5, 160, 90)
	if err != nil {
		t.Fatalf("build json: %v", err)
	}
	var track PreviewTrack
	if err := json.Unmarshal(raw, &track); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if track.Version != 1 || track.DurationMs != 30_000 || track.TileWidth != 160 || track.TileHeight != 90 || track.Columns != 5 {
		t.Fatalf("track metadata mismatch: %+v", track)
	}
	if len(track.Cues) != 2 {
		t.Fatalf("cue count = %d, want 2", len(track.Cues))
	}
	if got := track.Cues[0]; got.StartMs != 0 || got.EndMs != 15_000 || got.X != 0 || got.Y != 0 || got.W != 160 || got.H != 90 {
		t.Fatalf("first cue = %+v", got)
	}
	if got := track.Cues[1]; got.StartMs != 15_000 || got.EndMs != 30_000 || got.X != 160 || got.Y != 0 || got.W != 160 || got.H != 90 {
		t.Fatalf("second cue = %+v", got)
	}
}

func TestBuildPreviewTrackJSONUsesEveryFrameAndCoversDuration(t *testing.T) {
	raw, err := buildPreviewTrackJSON(100.0, 80, 5, 160, 90)
	if err != nil {
		t.Fatalf("build json: %v", err)
	}
	var track PreviewTrack
	if err := json.Unmarshal(raw, &track); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(track.Cues) != 80 {
		t.Fatalf("cue count = %d, want 80", len(track.Cues))
	}
	for i, cue := range track.Cues {
		if cue.EndMs <= cue.StartMs {
			t.Fatalf("cue %d has empty range: %+v", i, cue)
		}
		if i > 0 && cue.StartMs != track.Cues[i-1].EndMs {
			t.Fatalf("cue %d starts at %d after previous end %d", i, cue.StartMs, track.Cues[i-1].EndMs)
		}
	}
	if got := track.Cues[len(track.Cues)-1].EndMs; got != 100_000 {
		t.Fatalf("last cue ends at %d, want 100000", got)
	}
}

func TestPreviewReconciliationDoesNotDependOnCompletionHint(t *testing.T) {
	root := t.TempDir()
	m := NewManager(newTestWorkerDBAt(t, root), testCfg(root))
	streamKey := "media/youtube/sample_clip.mp4"
	streamPath, _ := m.cfg.Storage.Path(streamKey)
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamPath, []byte("stream bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: "sample_clip", ChannelID: "youtube_sample", OwnerKind: "youtube_video",
		Assets: []db.Asset{{AssetKind: "video_stream", FilePath: streamKey}},
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := m.db.GetAssetByOwnerIdentity("video_stream", "youtube_video", "sample_clip", 0)
	if err != nil || stream == nil {
		t.Fatalf("stream: %+v %v", stream, err)
	}

	binDir := t.TempDir()
	callsPath := filepath.Join(binDir, "calls")
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffmpeg := "#!/bin/sh\nset -eu\nprintf '1\\n' >> \"$PREVIEW_FFMPEG_CALLS\"\nfor output do :; done\nprintf 'sprite' > \"$output\"\n"
	if err := os.WriteFile(ffmpegPath, []byte(ffmpeg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ffprobe"), []byte("#!/bin/sh\nprintf '10\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PREVIEW_FFMPEG_CALLS", callsPath)

	req := PreviewRequest{
		VideoID: "sample_clip", OwnerKind: "youtube_video", FilePath: streamPath,
		InputRevision: stream.Revision, Duration: 10,
	}
	m.previewBackfillNotBefore = time.Time{}
	worked, _ := m.processPreviewBatch(context.Background(), time.Now())
	if !worked {
		t.Fatal("durable preview demand was not reconciled")
	}
	ready, current, err := m.previewState(req)
	if err != nil || !current || !ready {
		t.Fatalf("reconciled preview = ready %v current %v err %v", ready, current, err)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "1\n" {
		t.Fatalf("ffmpeg calls = %q, want one", calls)
	}
}

func TestRequestedPreviewDoesNotConsumeHistoricalFill(t *testing.T) {
	root := t.TempDir()
	m := NewManager(newTestWorkerDBAt(t, root), testCfg(root))
	streamKey := "media/youtube/sample_historical.mp4"
	streamPath, err := m.cfg.Storage.Path(streamKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamPath, []byte("historical stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: "sample_historical", ChannelID: "youtube_sample", OwnerKind: "youtube_video",
		Assets: []db.Asset{{AssetKind: "video_stream", FilePath: streamKey}},
	}); err != nil {
		t.Fatal(err)
	}

	if worked, _ := m.processRequestedPreview(context.Background(), time.Now()); worked {
		t.Fatal("request-only pass consumed historical preview work")
	}
	pending, err := m.db.GetPendingVideoPreview("sample_historical")
	if err != nil || pending == nil {
		t.Fatalf("historical preview was not left pending: %+v %v", pending, err)
	}
}

func TestPreviewHistoricalFillIsPaced(t *testing.T) {
	root := t.TempDir()
	m := NewManager(newTestWorkerDBAt(t, root), testCfg(root))
	for _, videoID := range []string{"sample_first", "sample_second"} {
		streamKey := filepath.Join("media", "youtube", videoID+".mp4")
		streamPath, err := m.cfg.Storage.Path(streamKey)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(streamPath, []byte("stream:"+videoID), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := m.db.StoreCompletedVideo(db.CompletedVideo{
			VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video",
			Duration: 10, Assets: []db.Asset{{AssetKind: "video_stream", FilePath: streamKey}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	binDir := t.TempDir()
	callsPath := filepath.Join(binDir, "calls")
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffmpeg := "#!/bin/sh\nset -eu\nprintf '1\\n' >> \"$PREVIEW_FFMPEG_CALLS\"\nfor output do :; done\nprintf 'sprite' > \"$output\"\n"
	if err := os.WriteFile(ffmpegPath, []byte(ffmpeg), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PREVIEW_FFMPEG_CALLS", callsPath)

	now := time.Now()
	m.previewBackfillNotBefore = time.Time{}
	if worked, _ := m.processPreviewBatch(context.Background(), now); !worked {
		t.Fatal("first historical preview was not processed")
	}
	worked, delay := m.processPreviewBatch(context.Background(), now.Add(previewMinimumInterval))
	if worked || delay != previewBackfillInterval-previewMinimumInterval {
		t.Fatalf("second historical preview = worked %v delay %s", worked, delay)
	}
	if worked, _ := m.processPreviewBatch(context.Background(), now.Add(previewBackfillInterval)); !worked {
		t.Fatal("paced historical preview did not become eligible")
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "1\n1\n" {
		t.Fatalf("ffmpeg calls = %q, want two paced passes", calls)
	}
}

func TestPreviewBackfillScansPastCoolingCandidates(t *testing.T) {
	root := t.TempDir()
	m := NewManager(newTestWorkerDBAt(t, root), testCfg(root))
	streamKey := "media/youtube/shared-preview-stream.mp4"
	streamPath, err := m.cfg.Storage.Path(streamKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamPath, []byte("stream bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	const coolingCandidates = 129
	for index := 0; index <= coolingCandidates; index++ {
		videoID := fmt.Sprintf("sample_preview_%03d", index)
		if err := m.db.StoreCompletedVideo(db.CompletedVideo{
			VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video",
			Duration: 10, Assets: []db.Asset{{AssetKind: "video_stream", FilePath: streamKey}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := m.db.ExecRaw(`UPDATE videos SET downloaded_at = ? WHERE video_id = ?`, index, videoID); err != nil {
			t.Fatal(err)
		}
	}

	stream, err := m.db.GetAssetByOwnerIdentity("video_stream", "youtube_video", "sample_preview_000", 0)
	if err != nil || stream == nil {
		t.Fatalf("stream: %+v %v", stream, err)
	}
	now := time.Now()
	for index := 1; index <= coolingCandidates; index++ {
		candidate := db.VideoPreviewCandidate{
			VideoID:       fmt.Sprintf("sample_preview_%03d", index),
			InputRevision: stream.Revision,
		}
		m.previewRetry[previewRetryKey(candidate)] = previewRetryState{
			Attempts:  1,
			NotBefore: now.Add(time.Hour),
		}
	}

	binDir := t.TempDir()
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffmpeg := "#!/bin/sh\nset -eu\nfor output do :; done\nprintf 'sprite' > \"$output\"\n"
	if err := os.WriteFile(ffmpegPath, []byte(ffmpeg), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m.previewBackfillNotBefore = time.Time{}
	worked, _ := m.processPreviewBatch(context.Background(), now)
	if !worked {
		t.Fatal("eligible preview behind cooling candidates was not processed")
	}
	pending, err := m.db.GetPendingVideoPreview("sample_preview_000")
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatalf("eligible preview remains pending: %+v", pending)
	}
}

func TestPreviewStateIgnoresConventionalFilesWithoutRevisionBoundRows(t *testing.T) {
	root := t.TempDir()
	m := &Manager{cfg: testCfg(root), db: newTestWorkerDBAt(t, root)}
	streamKey := "media/youtube/test_clip.mp4"
	streamPath, _ := m.cfg.Storage.Path(streamKey)
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamPath, []byte("stream bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: "test_clip", ChannelID: "youtube_sample", OwnerKind: "youtube_video",
		Assets: []db.Asset{{AssetKind: "video_stream", FilePath: streamKey}},
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := m.db.GetAssetByOwnerIdentity("video_stream", "youtube_video", "test_clip", 0)
	if err != nil || stream == nil {
		t.Fatalf("stream: %+v %v", stream, err)
	}
	conventionalDir, _ := m.cfg.Storage.Path("thumbnails/previews/test_clip")
	if err := os.MkdirAll(conventionalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"track.json": `{}`, "sprite.jpg": "sprite"} {
		if err := os.WriteFile(filepath.Join(conventionalDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for kind, spec := range map[string]struct {
		key  string
		body string
	}{
		"preview_track_json": {key: "thumbnails/previews/colliding-owner/track.json", body: `{}`},
		"preview_sprite":     {key: "thumbnails/previews/colliding-owner/sprite.jpg", body: "sprite"},
	} {
		path, _ := m.cfg.Storage.Path(spec.key)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(spec.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := m.db.StoreReadyAsset(db.Asset{
			AssetID: db.BuildAssetID("tiktok", "tiktok_video", "test_clip", kind, 0), AssetKind: kind,
			OwnerKind: "tiktok_video", OwnerID: "test_clip", FilePath: spec.key, SourceURL: "revision:" + strconv.FormatInt(stream.Revision, 10),
		}, 1); err != nil {
			t.Fatal(err)
		}
	}
	req := PreviewRequest{VideoID: "test_clip", OwnerKind: "youtube_video", FilePath: streamPath, InputRevision: stream.Revision, Duration: 30}
	ready, current, err := m.previewState(req)
	if err != nil || !current || ready {
		t.Fatalf("conventional files or colliding owner affected canonical state: ready=%v current=%v err=%v", ready, current, err)
	}

	trackKey := "thumbnails/previews/test_clip-bound/track.json"
	spriteKey := "thumbnails/previews/test_clip-bound/sprite.jpg"
	for key, body := range map[string]string{trackKey: `{}`, spriteKey: "sprite"} {
		path, _ := m.cfg.Storage.Path(key)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.db.StoreVideoPreviewAssets("test_clip", stream.Revision, trackKey, spriteKey, 1); err != nil {
		t.Fatal(err)
	}
	ready, current, err = m.previewState(req)
	if err != nil || !current || !ready {
		t.Fatalf("revision-bound preview not ready: ready=%v current=%v err=%v", ready, current, err)
	}
}
