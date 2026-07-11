package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/screwys/igloo/internal/db"
)

func TestPreviewFrameCount(t *testing.T) {
	tests := []struct {
		duration float64
		want     int
	}{
		{60, 80},     // 1 minute
		{600, 80},    // 10 minutes
		{2700, 80},   // 45 minutes
		{2701, 120},  // just over 45 min
		{7200, 120},  // 2 hours
		{10800, 120}, // 3 hours
		{10801, 160}, // just over 3 hours
		{21600, 160}, // 6 hours
		{21601, 200}, // just over 6 hours
		{0, 80},      // zero duration
	}
	for _, tt := range tests {
		got := previewFrameCount(tt.duration)
		if got != tt.want {
			t.Errorf("previewFrameCount(%.0f) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}

func TestPreviewTimestamps(t *testing.T) {
	ts := previewTimestamps(100.0, 4)
	if len(ts) != 4 {
		t.Fatalf("expected 4 timestamps, got %d", len(ts))
	}
	expected := []float64{12.5, 37.5, 62.5, 87.5}
	for i, want := range expected {
		if ts[i] != want {
			t.Errorf("timestamp[%d] = %.1f, want %.1f", i, ts[i], want)
		}
	}
}

func TestBuildPreviewTrackJSON(t *testing.T) {
	raw, err := buildPreviewTrackJSON(30.0, 2, 15, 5, 160, 90)
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

func TestEnqueuePreviewSkipsUnsupportedMedia(t *testing.T) {
	m := &Manager{previewChan: make(chan PreviewRequest, 2)}

	m.EnqueuePreview(PreviewRequest{VideoID: "image", FilePath: "/tmp/image.jpg", Duration: 30})
	if got := len(m.previewChan); got != 0 {
		t.Fatalf("preview queue length after image = %d, want 0", got)
	}

	m.EnqueuePreview(PreviewRequest{VideoID: "clip", OwnerKind: "youtube_video", FilePath: "/tmp/clip.mp4", InputSHA256: "hash", Duration: 30})
	if got := len(m.previewChan); got != 1 {
		t.Fatalf("preview queue length after video = %d, want 1", got)
	}
}

func TestPreviewStateIgnoresConventionalFilesWithoutFingerprintBoundRows(t *testing.T) {
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
			OwnerKind: "tiktok_video", OwnerID: "test_clip", FilePath: spec.key, SourceURL: "sha256:" + stream.SHA256,
		}, 1); err != nil {
			t.Fatal(err)
		}
	}
	req := PreviewRequest{VideoID: "test_clip", OwnerKind: "youtube_video", FilePath: streamPath, InputSHA256: stream.SHA256, Duration: 30}
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
	if err := m.db.StoreVideoPreviewAssets("test_clip", stream.SHA256, trackKey, spriteKey, 1); err != nil {
		t.Fatal(err)
	}
	ready, current, err = m.previewState(req)
	if err != nil || !current || !ready {
		t.Fatalf("fingerprint-bound preview not ready: ready=%v current=%v err=%v", ready, current, err)
	}
}
