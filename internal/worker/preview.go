package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	previewTileColumns = 5
	previewTileWidth   = 160
	previewTileHeight  = 90
)

// PreviewRequest is a request to generate a sprite sheet preview for a video.
type PreviewRequest struct {
	VideoID  string
	FilePath string  // absolute path to the video file
	Duration float64 // seconds
}

type PreviewTrack struct {
	Version    int          `json:"version"`
	DurationMs int64        `json:"duration_ms"`
	TileWidth  int          `json:"tile_width"`
	TileHeight int          `json:"tile_height"`
	Columns    int          `json:"columns"`
	Cues       []PreviewCue `json:"cues"`
}

type PreviewCue struct {
	StartMs int64 `json:"start_ms"`
	EndMs   int64 `json:"end_ms"`
	X       int   `json:"x"`
	Y       int   `json:"y"`
	W       int   `json:"w"`
	H       int   `json:"h"`
}

// runPreviewWorker reads from m.previewChan and generates preview sprites.
func (m *Manager) runPreviewWorker(ctx context.Context) {
	log.Printf("[preview] worker started")
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-m.previewChan:
			if err := m.generatePreview(ctx, req); err != nil {
				log.Printf("[preview] generatePreview %s: %v", req.VideoID, err)
			}
		}
	}
}

// EnqueuePreview sends a non-blocking preview request.
// If the channel is full the request is dropped and a warning is logged.
func (m *Manager) EnqueuePreview(req PreviewRequest) {
	if req.Duration <= 0 || !previewPathLooksVideo(req.FilePath) {
		log.Printf("[preview] skipping preview for non-video media %s", req.VideoID)
		return
	}
	select {
	case m.previewChan <- req:
	default:
		log.Printf("[preview] queue full, dropping preview for %s", req.VideoID)
	}
}

// generatePreview produces sprite.jpg + track.json for a video.
// Output goes to {DataDir}/thumbnails/previews/{videoID}/.
// If the output files already exist the call is a no-op.
func (m *Manager) generatePreview(ctx context.Context, req PreviewRequest) error {
	if req.Duration <= 0 || !previewPathLooksVideo(req.FilePath) {
		return nil
	}
	outDir := filepath.Join(m.cfg.DataDir, "thumbnails", "previews", req.VideoID)

	spriteDst := filepath.Join(outDir, "sprite.jpg")
	jsonDst := filepath.Join(outDir, "track.json")

	// Skip if already done.
	if fileExists(spriteDst) && fileExists(jsonDst) {
		return m.maintainPreviewAssets(req.VideoID)
	}
	if fileExists(spriteDst) {
		if err := ensurePreviewTrackJSON(outDir, req); err != nil {
			return err
		}
		return m.maintainPreviewAssets(req.VideoID)
	}

	frameCount := previewFrameCount(req.Duration)
	interval := int(math.Ceil(req.Duration / float64(frameCount)))
	jsonContent, err := buildPreviewTrackJSON(
		req.Duration,
		frameCount,
		interval,
		previewTileColumns,
		previewTileWidth,
		previewTileHeight,
	)
	if err != nil {
		return err
	}
	timestamps := previewTimestamps(req.Duration, frameCount)

	// Create temp directory on the same filesystem as the output to allow os.Rename.
	tmpParent := filepath.Join(m.cfg.DataDir, "tmp")
	_ = os.MkdirAll(tmpParent, 0o755)
	tmpDir, err := os.MkdirTemp(tmpParent, "preview_"+req.VideoID+"_")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Extract one frame per timestamp.
	for i, ts := range timestamps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		outFrame := filepath.Join(tmpDir, fmt.Sprintf("frame_%03d.jpg", i))
		args := []string{
			"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
			"-ss", fmt.Sprintf("%.3f", ts),
			"-i", req.FilePath,
			"-frames:v", "1",
			"-vf", "scale=160:90:force_original_aspect_ratio=decrease,pad=160:90:(ow-iw)/2:(oh-ih)/2:black",
			"-q:v", "5",
			"-strict", "unofficial",
			outFrame,
		}
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[preview] ffmpeg frame %d for %s: %v\n%s", i, req.VideoID, err, out)
			return fmt.Errorf("ffmpeg frame %d: %w", i, err)
		}
	}

	// Stitch frames into sprite sheet.
	rows := (frameCount + previewTileColumns - 1) / previewTileColumns
	tmpSprite := filepath.Join(tmpDir, "sprite.jpg")
	stitchArgs := []string{
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", filepath.Join(tmpDir, "frame_%03d.jpg"),
		"-vf", fmt.Sprintf("tile=%dx%d", previewTileColumns, rows),
		"-q:v", "4",
		"-strict", "unofficial",
		tmpSprite,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", stitchArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg tile stitch: %w\n%s", err, out)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	if err := os.Rename(tmpSprite, spriteDst); err != nil {
		return fmt.Errorf("rename sprite: %w", err)
	}
	if err := os.WriteFile(jsonDst, jsonContent, 0o644); err != nil {
		return fmt.Errorf("write preview track json: %w", err)
	}

	log.Printf("[preview] generated preview for %s (%d frames, %dx%d tile)", req.VideoID, frameCount, previewTileColumns, rows)
	return m.maintainPreviewAssets(req.VideoID)
}

func (m *Manager) maintainPreviewAssets(videoID string) error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.MaintainVideoAssets(videoID, time.Now().UnixMilli())
}

// backfillPreviews runs once at startup and enqueues any downloaded videos
// that are missing sprite previews on disk. This recovers from lost in-memory
// preview requests caused by server restarts.
func (m *Manager) backfillPreviews(ctx context.Context) {
	candidates, err := m.db.GetPreviewCandidates()
	if err != nil {
		log.Printf("[preview] backfill query: %v", err)
		return
	}

	previewDir := filepath.Join(m.cfg.DataDir, "thumbnails", "previews")
	var queued int
	for _, c := range candidates {
		if !previewPathLooksVideo(c.FilePath) {
			continue
		}
		sprite := filepath.Join(previewDir, c.VideoID, "sprite.jpg")
		track := filepath.Join(previewDir, c.VideoID, "track.json")
		if fileExists(sprite) && fileExists(track) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case m.previewChan <- PreviewRequest{
			VideoID:  c.VideoID,
			FilePath: c.FilePath,
			Duration: float64(c.Duration),
		}:
			queued++
		default:
			log.Printf("[preview] backfill: channel full after %d enqueued, stopping", queued)
			return
		}
	}
	if queued > 0 {
		log.Printf("[preview] backfill: enqueued %d videos missing previews", queued)
	}
}

func previewPathLooksVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".webm", ".mkv", ".mov", ".m4v":
		return true
	default:
		return false
	}
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func EnsurePreviewTrackJSON(outDir string, req PreviewRequest) error {
	return ensurePreviewTrackJSON(outDir, req)
}

func ensurePreviewTrackJSON(outDir string, req PreviewRequest) error {
	trackDst := filepath.Join(outDir, "track.json")
	if fileExists(trackDst) {
		return nil
	}
	frameCount := previewFrameCount(req.Duration)
	interval := int(math.Ceil(req.Duration / float64(frameCount)))
	raw, err := buildPreviewTrackJSON(
		req.Duration,
		frameCount,
		interval,
		previewTileColumns,
		previewTileWidth,
		previewTileHeight,
	)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	if err := os.WriteFile(trackDst, raw, 0o644); err != nil {
		return fmt.Errorf("write preview track json: %w", err)
	}
	return nil
}

// previewFrameCount returns the number of frames to extract for the given duration.
//
//	≤ 45 min  → 80 frames
//	≤ 3 h     → 120 frames
//	≤ 6 h     → 160 frames
//	> 6 h     → 200 frames
func previewFrameCount(duration float64) int {
	switch {
	case duration <= 2700:
		return 80
	case duration <= 10800:
		return 120
	case duration <= 21600:
		return 160
	default:
		return 200
	}
}

// previewTimestamps returns count evenly-spaced timestamps across duration,
// centred within each equal-width segment: (i+0.5) * duration/count.
func previewTimestamps(duration float64, count int) []float64 {
	ts := make([]float64, count)
	for i := 0; i < count; i++ {
		ts[i] = (float64(i) + 0.5) * duration / float64(count)
	}
	return ts
}

func buildPreviewTrackJSON(duration float64, frameCount, interval, columns, tileW, tileH int) ([]byte, error) {
	if duration <= 0 || frameCount <= 0 || interval <= 0 || columns <= 0 || tileW <= 0 || tileH <= 0 {
		return nil, fmt.Errorf("invalid preview track geometry duration=%.3f frames=%d interval=%d columns=%d tile=%dx%d", duration, frameCount, interval, columns, tileW, tileH)
	}
	durationMs := int64(math.Round(duration * 1000))
	if durationMs <= 0 {
		return nil, fmt.Errorf("invalid preview track duration_ms=%d", durationMs)
	}
	track := PreviewTrack{
		Version:    1,
		DurationMs: durationMs,
		TileWidth:  tileW,
		TileHeight: tileH,
		Columns:    columns,
		Cues:       make([]PreviewCue, 0, frameCount),
	}
	for i := 0; i < frameCount; i++ {
		startSec := float64(i * interval)
		endSec := float64((i + 1) * interval)
		if endSec > duration {
			endSec = duration
		}
		startMs := int64(math.Round(startSec * 1000))
		endMs := int64(math.Round(endSec * 1000))
		if endMs <= startMs {
			continue
		}

		col := i % columns
		row := i / columns
		track.Cues = append(track.Cues, PreviewCue{
			StartMs: startMs,
			EndMs:   endMs,
			X:       col * tileW,
			Y:       row * tileH,
			W:       tileW,
			H:       tileH,
		})
	}
	if len(track.Cues) == 0 {
		return nil, fmt.Errorf("preview track has no cues")
	}
	raw, err := json.Marshal(track)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
