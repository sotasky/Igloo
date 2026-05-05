package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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

func TestEnsurePreviewTrackJSONRepairsExistingSpriteWithoutVTT(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "previews", "video")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "sprite.jpg"), []byte("sprite"), 0o644); err != nil {
		t.Fatalf("write sprite: %v", err)
	}

	if err := ensurePreviewTrackJSON(outDir, PreviewRequest{VideoID: "video", Duration: 30}); err != nil {
		t.Fatalf("repair json: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "track.json")); err != nil {
		t.Fatalf("track.json missing after repair: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "track.vtt")); !os.IsNotExist(err) {
		t.Fatalf("repair must not create track.vtt, stat err=%v", err)
	}
}
