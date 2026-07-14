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
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
)

const (
	previewTileColumns = 5
	previewTileWidth   = 160
	previewTileHeight  = 90
)

const (
	previewMinimumInterval  = 10 * time.Second
	previewBackfillInterval = time.Minute
)

// PreviewRequest is a request to generate a sprite sheet preview for a video.
type PreviewRequest struct {
	VideoID       string
	OwnerKind     string  // exact canonical video owner kind
	FilePath      string  // absolute path to the canonical video stream
	InputRevision int64   // canonical stream asset revision this preview derives from
	Duration      float64 // seconds
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

type previewRetryState struct {
	Attempts  int
	NotBefore time.Time
}

func (m *Manager) RequestVideoPreview(videoID string) {
	videoID = strings.TrimSpace(videoID)
	if m == nil || videoID == "" {
		return
	}
	m.previewMu.Lock()
	if m.previewHints == nil {
		m.previewHints = make(map[string]struct{})
	}
	m.previewHints[videoID] = struct{}{}
	m.previewMu.Unlock()
	m.KickMediaWork()
}

func (m *Manager) processPreviewBatch(ctx context.Context, now time.Time) (bool, time.Duration) {
	return m.processPreviewBatchMode(ctx, now, true)
}

func (m *Manager) processRequestedPreview(ctx context.Context, now time.Time) (bool, time.Duration) {
	return m.processPreviewBatchMode(ctx, now, false)
}

func (m *Manager) processPreviewBatchMode(ctx context.Context, now time.Time, allowBackfill bool) (bool, time.Duration) {
	if m == nil || m.db == nil || m.cfg == nil || m.downloader == nil || ctx.Err() != nil {
		return false, 0
	}
	if delay := m.previewAdmissionDelay(now); delay > 0 {
		return false, delay
	}
	for {
		videoID, ok := m.popPreviewHint()
		if !ok {
			break
		}
		candidate, err := m.db.GetPendingVideoPreview(videoID)
		if err != nil {
			log.Printf("[preview] resolve requested preview %s: %v", videoID, err)
			return false, time.Minute
		}
		if candidate == nil {
			continue
		}
		if delay := m.previewRetryDelay(*candidate, now); delay > 0 {
			continue
		}
		m.setPreviewAdmission(now.Add(previewMinimumInterval), time.Time{})
		return m.processPreviewCandidate(ctx, *candidate, now), previewMinimumInterval
	}
	if !allowBackfill {
		return false, 0
	}

	if delay := m.previewBackfillDelay(now); delay > 0 {
		return false, delay
	}
	candidates, err := m.db.ListPendingVideoPreviews(m.previewScanLimit())
	if err != nil {
		log.Printf("[preview] list pending previews: %v", err)
		return false, time.Minute
	}
	var retryDelay time.Duration
	for _, candidate := range candidates {
		delay := m.previewRetryDelay(candidate, now)
		if delay > 0 {
			if retryDelay == 0 || delay < retryDelay {
				retryDelay = delay
			}
			continue
		}
		m.setPreviewAdmission(now.Add(previewMinimumInterval), now.Add(previewBackfillInterval))
		return m.processPreviewCandidate(ctx, candidate, now), previewBackfillInterval
	}
	return false, retryDelay
}

func (m *Manager) processPreviewCandidate(ctx context.Context, candidate db.VideoPreviewCandidate, now time.Time) bool {
	path, err := m.cfg.Storage.Path(candidate.FilePath)
	if err == nil {
		err = m.downloader.RunMedia(ctx, download.MediaLaneBulkBackground, func() error {
			duration := float64(candidate.Duration)
			if duration <= 0 {
				duration, err = probePreviewDuration(ctx, path)
				if err != nil {
					return err
				}
			}
			return m.generatePreview(ctx, PreviewRequest{
				VideoID: candidate.VideoID, OwnerKind: candidate.OwnerKind,
				FilePath: path, InputRevision: candidate.InputRevision,
				Duration: duration,
			})
		})
	}
	key := previewRetryKey(candidate)
	m.previewMu.Lock()
	defer m.previewMu.Unlock()
	if err == nil {
		delete(m.previewRetry, key)
		return true
	}
	if ctx.Err() == nil {
		state := m.previewRetry[key]
		state.Attempts++
		delay := time.Minute << min(state.Attempts-1, 6)
		state.NotBefore = now.Add(delay)
		if m.previewRetry == nil {
			m.previewRetry = make(map[string]previewRetryState)
		}
		m.previewRetry[key] = state
		log.Printf("[preview] generatePreview %s: %v", candidate.VideoID, err)
	}
	return true
}

func (m *Manager) previewAdmissionDelay(now time.Time) time.Duration {
	m.previewMu.Lock()
	defer m.previewMu.Unlock()
	if now.Before(m.previewNotBefore) {
		return m.previewNotBefore.Sub(now)
	}
	return 0
}

func (m *Manager) previewBackfillDelay(now time.Time) time.Duration {
	m.previewMu.Lock()
	defer m.previewMu.Unlock()
	if now.Before(m.previewBackfillNotBefore) {
		return m.previewBackfillNotBefore.Sub(now)
	}
	return 0
}

func (m *Manager) setPreviewAdmission(notBefore, backfillNotBefore time.Time) {
	m.previewMu.Lock()
	m.previewNotBefore = notBefore
	if !backfillNotBefore.IsZero() {
		m.previewBackfillNotBefore = backfillNotBefore
	}
	m.previewMu.Unlock()
}

func (m *Manager) popPreviewHint() (string, bool) {
	m.previewMu.Lock()
	defer m.previewMu.Unlock()
	for videoID := range m.previewHints {
		delete(m.previewHints, videoID)
		return videoID, true
	}
	return "", false
}

func (m *Manager) hasPreviewHint() bool {
	m.previewMu.Lock()
	defer m.previewMu.Unlock()
	return len(m.previewHints) > 0
}

func (m *Manager) previewRetryDelay(candidate db.VideoPreviewCandidate, now time.Time) time.Duration {
	m.previewMu.Lock()
	defer m.previewMu.Unlock()
	state, ok := m.previewRetry[previewRetryKey(candidate)]
	if ok && now.Before(state.NotBefore) {
		return state.NotBefore.Sub(now)
	}
	return 0
}

func (m *Manager) previewScanLimit() int {
	m.previewMu.Lock()
	defer m.previewMu.Unlock()
	return len(m.previewRetry) + 1
}

func previewRetryKey(candidate db.VideoPreviewCandidate) string {
	return candidate.VideoID + "\x00" + strconv.FormatInt(candidate.InputRevision, 10)
}

func probePreviewDuration(ctx context.Context, path string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=duration:format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe preview duration: %w", err)
	}
	for _, value := range strings.Fields(string(out)) {
		duration, err := strconv.ParseFloat(value, 64)
		if err == nil && duration > 0 && !math.IsNaN(duration) && !math.IsInf(duration, 0) {
			return duration, nil
		}
	}
	return 0, fmt.Errorf("invalid preview duration %q", strings.TrimSpace(string(out)))
}

// generatePreview publishes an immutable sprite/track pair derived from one
// exact canonical stream revision.
func (m *Manager) generatePreview(ctx context.Context, req PreviewRequest) error {
	if req.Duration <= 0 || req.OwnerKind == "" || req.InputRevision <= 0 || !previewPathLooksVideo(req.FilePath) {
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
	jsonContent, err := buildPreviewTrackJSON(
		req.Duration,
		frameCount,
		previewTileColumns,
		previewTileWidth,
		previewTileHeight,
	)
	if err != nil {
		return err
	}
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

	rows := (frameCount + previewTileColumns - 1) / previewTileColumns
	tmpSprite := filepath.Join(tmpDir, "sprite.jpg")
	sampleRate := float64(frameCount) / req.Duration
	filter := fmt.Sprintf(
		"setpts=PTS-STARTPTS,tpad=stop_mode=clone:stop_duration=%.6f,trim=duration=%.6f,"+
			"fps=fps=%.12f:start_time=0:round=up:eof_action=pass,"+
			"scale=%d:%d:force_original_aspect_ratio=decrease,"+
			"pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,"+
			"tile=layout=%dx%d:nb_frames=%d:color=black",
		req.Duration,
		req.Duration,
		sampleRate,
		previewTileWidth,
		previewTileHeight,
		previewTileWidth,
		previewTileHeight,
		previewTileColumns,
		rows,
		frameCount,
	)
	args := []string{
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-skip_frame", "nokey",
		"-i", req.FilePath,
		"-map", "0:v:0",
		"-an", "-sn", "-dn",
		"-vf", filter,
		"-frames:v", "1",
		"-q:v", "4",
		"-strict", "unofficial",
		tmpSprite,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg preview pass: %w\n%s", err, out)
	}

	previewRoot, err := m.cfg.Storage.WritePath("thumbnails/previews")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(previewRoot, 0o755); err != nil {
		return err
	}
	outDir, err := os.MkdirTemp(previewRoot, safeVideoID+"-r"+strconv.FormatInt(req.InputRevision, 10)+"-")
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
	if err := m.db.StoreVideoPreviewAssets(req.VideoID, req.InputRevision, trackKey, spriteKey, 0); err != nil {
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
	previewSource := "revision:" + strconv.FormatInt(req.InputRevision, 10)
	trackReady, spriteReady := false, false
	for _, asset := range assets {
		switch asset.AssetKind {
		case "video_stream":
			if asset.MediaIndex == 0 && asset.FilePath == streamKey && asset.Revision == req.InputRevision {
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

// previewFrameCount targets one frame per five seconds without exceeding the
// existing sprite limits for long videos.
func previewFrameCount(duration float64) int {
	if duration <= 0 {
		return 0
	}
	count := int(math.Ceil(duration / 5))
	limit := 200
	switch {
	case duration <= 2700:
		limit = 80
	case duration <= 10800:
		limit = 120
	case duration <= 21600:
		limit = 160
	}
	if count > limit {
		return limit
	}
	return count
}

func buildPreviewTrackJSON(duration float64, frameCount, columns, tileW, tileH int) ([]byte, error) {
	if duration <= 0 || frameCount <= 0 || columns <= 0 || tileW <= 0 || tileH <= 0 {
		return nil, fmt.Errorf("invalid preview track geometry duration=%.3f frames=%d columns=%d tile=%dx%d", duration, frameCount, columns, tileW, tileH)
	}
	durationMs := int64(math.Round(duration * 1000))
	if durationMs < int64(frameCount) {
		return nil, fmt.Errorf("invalid preview track duration_ms=%d frames=%d", durationMs, frameCount)
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
		startMs := durationMs * int64(i) / int64(frameCount)
		endMs := durationMs * int64(i+1) / int64(frameCount)

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
	raw, err := json.Marshal(track)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
