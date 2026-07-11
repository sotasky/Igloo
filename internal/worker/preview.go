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

	"github.com/screwys/igloo/internal/db"
)

const (
	previewTileColumns = 5
	previewTileWidth   = 160
	previewTileHeight  = 90
)

// PreviewRequest is a request to generate a sprite sheet preview for a video.
type PreviewRequest struct {
	VideoID     string
	OwnerKind   string  // exact canonical video owner kind
	FilePath    string  // absolute path to the canonical video stream
	InputSHA256 string  // canonical stream fingerprint this preview derives from
	Duration    float64 // seconds
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
	if req.Duration <= 0 || req.OwnerKind == "" || req.InputSHA256 == "" || !previewPathLooksVideo(req.FilePath) {
		log.Printf("[preview] skipping preview for non-video media %s", req.VideoID)
		return
	}
	select {
	case m.previewChan <- req:
	default:
		log.Printf("[preview] queue full, dropping preview for %s", req.VideoID)
	}
}

// generatePreview publishes an immutable sprite/track pair derived from one
// exact canonical stream fingerprint.
func (m *Manager) generatePreview(ctx context.Context, req PreviewRequest) error {
	if req.Duration <= 0 || req.OwnerKind == "" || req.InputSHA256 == "" || !previewPathLooksVideo(req.FilePath) {
		return nil
	}
	ready, current, err := m.previewState(req)
	if err != nil {
		return err
	}
	if !current || ready {
		return nil
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
	tmpParent, err := m.cfg.Storage.WritePath("tmp")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(tmpParent, 0o755)
	safeVideoID, err := safeVideoFileName(req.VideoID)
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(tmpParent, "preview_"+safeVideoID+"_")
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

	previewRoot, err := m.cfg.Storage.WritePath("thumbnails/previews")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(previewRoot, 0o755); err != nil {
		return err
	}
	shaPrefix := req.InputSHA256
	if len(shaPrefix) > 12 {
		shaPrefix = shaPrefix[:12]
	}
	outDir, err := os.MkdirTemp(previewRoot, safeVideoID+"-"+shaPrefix+"-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(outDir)
		}
	}()
	spriteDst := filepath.Join(outDir, "sprite.jpg")
	jsonDst := filepath.Join(outDir, "track.json")
	if err := os.Rename(tmpSprite, spriteDst); err != nil {
		return fmt.Errorf("rename sprite: %w", err)
	}
	if err := os.WriteFile(jsonDst, jsonContent, 0o644); err != nil {
		return fmt.Errorf("write preview track json: %w", err)
	}

	trackKey, err := m.cfg.Storage.Key(jsonDst)
	if err != nil {
		return err
	}
	spriteKey, err := m.cfg.Storage.Key(spriteDst)
	if err != nil {
		return err
	}
	if err := m.db.StoreVideoPreviewAssets(req.VideoID, req.InputSHA256, trackKey, spriteKey, 0); err != nil {
		return err
	}
	published = true
	log.Printf("[preview] generated preview for %s (%d frames, %dx%d tile)", req.VideoID, frameCount, previewTileColumns, rows)
	return nil
}

func (m *Manager) previewState(req PreviewRequest) (ready, current bool, err error) {
	assets, err := m.db.ListReadyAssetsForOwners(
		[]db.AssetOwnerRef{{OwnerKind: req.OwnerKind, OwnerID: req.VideoID}},
		[]string{"video_stream", "preview_track_json", "preview_sprite"},
	)
	if err != nil {
		return false, false, err
	}
	streamKey, err := m.cfg.Storage.Key(req.FilePath)
	if err != nil {
		return false, false, err
	}
	previewSource := "sha256:" + req.InputSHA256
	trackReady, spriteReady := false, false
	for _, asset := range assets {
		switch asset.AssetKind {
		case "video_stream":
			if asset.MediaIndex == 0 && asset.FilePath == streamKey && asset.SHA256 == req.InputSHA256 {
				current = true
			}
		case "preview_track_json":
			trackReady = asset.MediaIndex == 0 && asset.SourceURL == previewSource && m.canonicalAssetFileReady(asset)
		case "preview_sprite":
			spriteReady = asset.MediaIndex == 0 && asset.SourceURL == previewSource && m.canonicalAssetFileReady(asset)
		}
	}
	return trackReady && spriteReady, current, nil
}

func (m *Manager) canonicalAssetFileReady(asset db.Asset) bool {
	path, err := m.cfg.Storage.Path(asset.FilePath)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return (asset.SizeBytes == 0 || asset.SizeBytes == info.Size()) &&
		(asset.FileMtimeNs == 0 || asset.FileMtimeNs == info.ModTime().UnixNano())
}

func previewPathLooksVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".webm", ".mkv", ".mov", ".m4v":
		return true
	default:
		return false
	}
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
