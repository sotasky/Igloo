package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
	"net/url"
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
	previewMaxSheets          = 64
	previewMaxSpritePixels    = 4 * 1024 * 1024
	previewMaxSpriteDimension = 65535
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

type youtubeStoryboard struct {
	Width      int
	Height     int
	Columns    int
	Rows       int
	FrameCount int
	Fragments  []string
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
	if len(candidates) == 0 {
		m.previewMu.Lock()
		m.previewBackfillNotBefore = now.Add(previewBackfillInterval)
		m.previewMu.Unlock()
		return false, previewBackfillInterval
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

	tmpSprite := filepath.Join(tmpDir, "sprite.jpg")
	jsonContent, frameCount, err := m.downloadYouTubeStoryboard(ctx, req, tmpDir, tmpSprite)
	if err != nil {
		return err
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
	log.Printf("[preview] generated preview for %s (%d frames)", req.VideoID, frameCount)
	return nil
}

func (m *Manager) downloadYouTubeStoryboard(ctx context.Context, req PreviewRequest, tmpDir, spritePath string) ([]byte, int, error) {
	if req.OwnerKind != "youtube_video" {
		return nil, 0, fmt.Errorf("preview owner is not youtube: %s", req.OwnerKind)
	}
	cookies, browser := m.cookiesFor("youtube")
	info, err := m.downloader.YtDlp.FetchInfo(ctx,
		"https://www.youtube.com/watch?v="+url.QueryEscape(req.VideoID),
		download.Opts{Cookies: cookies, CookiesFromBrowser: browser},
	)
	if err != nil {
		return nil, 0, err
	}
	storyboard, err := selectYouTubeStoryboard(info, req.Duration)
	if err != nil {
		return nil, 0, err
	}
	if err := m.renderYouTubeStoryboard(ctx, storyboard, tmpDir, spritePath); err != nil {
		return nil, 0, err
	}
	track, err := buildPreviewTrackJSON(
		req.Duration, storyboard.FrameCount, storyboard.Columns, storyboard.Width, storyboard.Height,
	)
	return track, storyboard.FrameCount, err
}

func selectYouTubeStoryboard(info map[string]any, duration float64) (youtubeStoryboard, error) {
	formats, _ := info["formats"].([]any)
	bestTilePixels := 0
	var best youtubeStoryboard
	for _, raw := range formats {
		format, _ := raw.(map[string]any)
		if format == nil {
			continue
		}
		formatNote, _ := format["format_note"].(string)
		if !strings.Contains(strings.ToLower(formatNote), "storyboard") {
			continue
		}
		width := jsonInt(format["width"])
		height := jsonInt(format["height"])
		columns := jsonInt(format["columns"])
		rows := jsonInt(format["rows"])
		if width <= 0 || height <= 0 || width > 1280 || height > 720 || columns <= 0 || columns > 100 || rows <= 0 || rows > 100 {
			continue
		}
		fragmentsRaw, _ := format["fragments"].([]any)
		fragments := make([]string, 0, len(fragmentsRaw))
		for _, fragmentRaw := range fragmentsRaw {
			fragment, _ := fragmentRaw.(map[string]any)
			fragmentURL, _ := fragment["url"].(string)
			fragmentURL = strings.TrimSpace(fragmentURL)
			lowerURL := strings.ToLower(fragmentURL)
			if fragmentURL == "" || (!strings.Contains(lowerURL, ".jpg") && !strings.Contains(lowerURL, ".jpeg")) {
				fragments = nil
				break
			}
			fragments = append(fragments, fragmentURL)
		}
		if len(fragments) == 0 || len(fragments) > previewMaxSheets {
			continue
		}
		spriteWidth := columns * width
		spriteHeight := rows * height * len(fragments)
		spritePixels := int64(spriteWidth) * int64(spriteHeight)
		if spriteWidth > previewMaxSpriteDimension || spriteHeight > previewMaxSpriteDimension || spritePixels > previewMaxSpritePixels {
			continue
		}
		capacity := len(fragments) * columns * rows
		fps, _ := format["fps"].(float64)
		frameCount := int(math.Round(fps * duration))
		if frameCount <= 0 || frameCount > capacity {
			frameCount = capacity
		}
		if frameCount <= 0 {
			continue
		}
		tilePixels := width * height
		if tilePixels > bestTilePixels {
			bestTilePixels = tilePixels
			best = youtubeStoryboard{
				Width: width, Height: height, Columns: columns, Rows: rows,
				FrameCount: frameCount, Fragments: fragments,
			}
		}
	}
	if best.FrameCount == 0 {
		return youtubeStoryboard{}, fmt.Errorf("youtube did not provide a usable JPEG storyboard")
	}
	return best, nil
}

func (m *Manager) renderYouTubeStoryboard(ctx context.Context, storyboard youtubeStoryboard, tmpDir, spritePath string) error {
	sheetWidth := storyboard.Columns * storyboard.Width
	sheetHeight := storyboard.Rows * storyboard.Height
	sprite := image.NewRGBA(image.Rect(0, 0, sheetWidth, sheetHeight*len(storyboard.Fragments)))
	for i, fragmentURL := range storyboard.Fragments {
		path, err := m.downloader.HTTP.DownloadFileWithOptions(
			ctx, fragmentURL, tmpDir, fmt.Sprintf("sheet-%03d.jpg", i),
			download.HTTPDownloadOptions{MaxBytes: 4 << 20, Timeout: 30 * time.Second},
		)
		if err != nil {
			return fmt.Errorf("download youtube storyboard sheet %d: %w", i, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		sheet, err := jpeg.Decode(file)
		_ = file.Close()
		if err != nil {
			return fmt.Errorf("decode youtube storyboard sheet %d: %w", i, err)
		}
		bounds := sheet.Bounds()
		if bounds.Dx() < sheetWidth || bounds.Dy() < sheetHeight {
			return fmt.Errorf("youtube storyboard sheet %d is %dx%d, expected at least %dx%d", i, bounds.Dx(), bounds.Dy(), sheetWidth, sheetHeight)
		}
		draw.Draw(sprite, image.Rect(0, i*sheetHeight, sheetWidth, (i+1)*sheetHeight), sheet, bounds.Min, draw.Src)
	}
	file, err := os.Create(spritePath)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(file, sprite, &jpeg.Options{Quality: 82}); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func jsonInt(value any) int {
	number, _ := value.(float64)
	return int(number)
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
