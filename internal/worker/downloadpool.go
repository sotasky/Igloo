package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

const downloadBatchSize = 10

var (
	downloadPoolLeaseDuration      = 5 * time.Minute
	downloadPoolLeaseRenewInterval = downloadPoolLeaseDuration / 2
)

// platformSem limits concurrent downloads per platform. YouTube tolerates
// parallel yt-dlp sessions; short-form platforms stay at 1 because we rely on
// the pacing below to dodge per-IP/session rate limits.
var platformSem = map[string]chan struct{}{
	"youtube":   make(chan struct{}, 3),
	"tiktok":    make(chan struct{}, 1),
	"instagram": make(chan struct{}, 1),
}

// tiktokDownloadDelay enforces a minimum gap between short-form downloads to avoid rate limiting.
var (
	tiktokDlMu       sync.Mutex
	tiktokDlLastTime time.Time
	tiktokDlDelay    = 15 * time.Second
)

// qualityFormats maps quality names to yt-dlp format strings.
var qualityFormats = map[string]string{
	"2160p": "bestvideo[height<=2160]+bestaudio/best[height<=2160]/best",
	"1440p": "bestvideo[height<=1440]+bestaudio/best[height<=1440]/best",
	"1080p": "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best",
	"720p":  "bestvideo[height<=720]+bestaudio/best[height<=720]/best",
	"480p":  "bestvideo[height<=480]+bestaudio/best[height<=480]/best",
	"best":  "best",
}

// runDownloadPool processes video download queue jobs with per-platform concurrency.
func (m *Manager) runDownloadPool(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	log.Printf("[downloadpool] worker started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.downloadKick:
			if m.IsStopRequested() {
				continue
			}
			m.processDownloadBatch(ctx)
		case <-ticker.C:
			if m.IsStopRequested() {
				continue
			}
			m.processDownloadBatch(ctx)
		}
	}
}

// processDownloadBatch claims pending download jobs and processes them
// concurrently with per-platform semaphores.
func (m *Manager) processDownloadBatch(ctx context.Context) {
	owner := downloadPoolLeaseOwner()
	jobs, err := m.db.ClaimDownloadBatchWithLease(db.LeaseOptions{
		Owner:   owner,
		LeaseMs: downloadPoolLeaseDuration.Milliseconds(),
		Limit:   downloadBatchSize,
	})
	if err != nil {
		log.Printf("[downloadpool] ClaimDownloadBatchWithLease: %v", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	log.Printf("[downloadpool] processing %d jobs", len(jobs))

	var wg sync.WaitGroup
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ch, err := m.db.GetChannel(job.ChannelID)
		if err != nil || ch == nil {
			log.Printf("[downloadpool] GetChannel %s: %v", job.ChannelID, err)
			m.failDownloadJob(job, fmt.Errorf("channel not found"))
			continue
		}
		if m.cfg != nil && !m.cfg.PlatformEnabled(ch.Platform) {
			reason := fmt.Sprintf("platform disabled: %s", ch.Platform)
			log.Printf("[downloadpool] skip %s: %s", job.VideoID, reason)
			if err := m.db.UpdateDownloadQueueStatus(job.VideoID, job.LeaseOwner, "failed", reason, job.RetryCount+1, download.ErrorKindPermanentHTTP, download.ErrorStrategyPermanent, 0, time.Now().UnixMilli()); err != nil {
				log.Printf("[downloadpool] UpdateDownloadQueueStatus %s: %v", job.VideoID, err)
			}
			continue
		}

		// download_subtitles lives on channel_settings with global fallback;
		// GetChannelSettings resolves the override chain.
		subtitles := false
		if s, sErr := m.db.GetChannelSettings(ch.ChannelID); sErr == nil && s != nil {
			subtitles = s.DownloadSubtitles
		}

		sem := platformSemFor(ch.Platform)

		wg.Add(1)
		go func(job db.DownloadQueueRow, platform, sourceID, quality string, subtitles bool) {
			defer wg.Done()

			// Acquire platform semaphore.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			m.downloadVideo(ctx, job, platform, sourceID, quality, subtitles)
		}(job, ch.Platform, ch.SourceID, ch.Quality, subtitles)
	}

	wg.Wait()
}

// downloadVideo handles a single video download job.
func (m *Manager) downloadVideo(ctx context.Context, job db.DownloadQueueRow, platform, sourceID, quality string, subtitles bool) {
	if stopRenew := m.startDownloadQueueLeaseRenewal(ctx, job); stopRenew != nil {
		defer stopRenew()
	}

	// Short-form rate-limit pacing: enforce minimum gap between downloads,
	// measured from the END of the previous download attempt.
	if platform == "tiktok" || platform == "instagram" {
		tiktokDlMu.Lock()
		if elapsed := time.Since(tiktokDlLastTime); elapsed < tiktokDlDelay {
			time.Sleep(tiktokDlDelay - elapsed)
		}
		tiktokDlMu.Unlock()
	}
	defer func() {
		if platform == "tiktok" || platform == "instagram" {
			tiktokDlMu.Lock()
			tiktokDlLastTime = time.Now()
			tiktokDlMu.Unlock()
		}
	}()

	start := time.Now()
	m.EmitDownload(fmt.Sprintf("Downloading: %s", job.Title), "start", job.ChannelID, platform)
	safeSourceID := sourceID
	if platform == "tiktok" {
		if h := model.TikTokHandleFromChannelID(job.ChannelID); h != "" {
			safeSourceID = h
		}
	}
	videoDir := filepath.Join(m.cfg.DataDir, "media", platform, safeSourceID)
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		log.Printf("[downloadpool] mkdir %s: %v", videoDir, err)
		m.failDownloadJob(job, fmt.Errorf("mkdir: %w", err))
		return
	}

	sourceURL := buildSourceURL(platform, safeSourceID, job.VideoID)
	formatStr := resolveFormatString(platform, quality)

	cookiesFile, cookiesBrowser := m.cookiesFor(platform)
	opts := download.Opts{
		OutputDir:          videoDir,
		ID:                 job.VideoID,
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		CookieAlternates:   m.cookieSetsFor(platform),
		Format:             formatStr,
		Subtitles:          subtitles,
	}

	var paths []string
	var dlErr error

	if platform == "tiktok" || platform == "instagram" {
		paths, dlErr = m.downloader.Download(ctx, sourceURL, "video", opts)
	} else {
		paths, dlErr = m.downloader.YtDlp.Download(ctx, sourceURL, opts)
	}

	if dlErr != nil {
		log.Printf("[downloadpool] download %s: %v", job.VideoID, dlErr)
		m.EmitDownload(fmt.Sprintf("Failed: %s — %v", job.Title, dlErr), "error", job.ChannelID, platform)
		m.failDownloadJob(job, fmt.Errorf("download: %w", dlErr))
		return
	}

	if len(paths) == 0 {
		log.Printf("[downloadpool] no files returned for %s", job.VideoID)
		m.EmitDownload(fmt.Sprintf("Failed: %s — no files returned", job.Title), "error", job.ChannelID, platform)
		m.failDownloadJob(job, fmt.Errorf("no files returned"))
		return
	}

	videoPath := paths[0]
	relVideoPath := toRelPath(m.cfg.DataDir, videoPath)

	// Get file size.
	var fileSize int64
	if fi, err := os.Stat(videoPath); err == nil {
		fileSize = fi.Size()
	}

	// Find sibling thumbnail.
	thumbPath := findSiblingThumbnail(videoPath, job.VideoID)

	// Load .info.json sidecar for metadata.
	metadata := loadInfoJSON(videoPath, job.VideoID)
	if thumbPath == "" {
		thumbPath = m.downloadSiblingThumbnailFromMetadata(ctx, videoPath, job.VideoID, metadata)
	}

	relThumbPath := ""
	if thumbPath != "" {
		relThumbPath = toRelPath(m.cfg.DataDir, thumbPath)
	}

	// Short-form slideshows: gallery-dl returns multiple image files.
	// Build a slides array in metadata so EnrichForCard detects slideshow.
	if (platform == "tiktok" || platform == "instagram") && len(paths) > 1 {
		var imagePaths []string
		for _, p := range paths {
			ext := strings.ToLower(filepath.Ext(p))
			switch ext {
			case ".jpg", ".jpeg", ".png", ".webp":
				imagePaths = append(imagePaths, p)
			}
		}
		if len(imagePaths) > 1 {
			slides := make([]any, len(imagePaths))
			for i, p := range imagePaths {
				slides[i] = map[string]any{"path": toRelPath(m.cfg.DataDir, p)}
			}
			if metadata == nil {
				metadata = map[string]any{}
			}
			metadata["slides"] = slides
			metadata["vcodec"] = "none"
		}
	}

	publishedAt := publishedAtForJob(metadata, job)

	var duration int
	var description string
	var metadataJSON string
	var mediaKind string
	var slideCount int

	if metadata != nil {
		duration = extractDurationFromMetadata(metadata)
		description = videoDescriptionFromMetadata(metadata)
		stripped := model.StripVideoMetadata(metadata)
		if stripped != nil {
			if data, err := json.Marshal(stripped); err == nil {
				metadataJSON = string(data)
			}
		}
	}

	// Compute media_kind from stripped metadata
	if metadataJSON != "" {
		var meta model.VideoMetadata
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			mediaKind, slideCount = model.ComputeMediaKind(&meta, relVideoPath)
		}
	}
	if mediaKind == "" {
		mediaKind, slideCount = model.ComputeMediaKind(nil, relVideoPath)
	}

	// Use richer post metadata when a flat channel check only carried a
	// truncated social caption.
	title := videoTitleFromMetadata(metadata, job.Title)

	// Insert into videos table.
	if err := m.db.InsertVideo(
		job.VideoID, job.ChannelID, title, description,
		duration, relThumbPath, relVideoPath, fileSize,
		publishedAt, metadataJSON, mediaKind, slideCount, false,
	); err != nil {
		log.Printf("[downloadpool] InsertVideo %s: %v", job.VideoID, err)
		m.failDownloadJob(job, fmt.Errorf("db insert: %w", err))
		return
	}

	// Remove from download queue.
	if err := m.db.RemoveFromDownloadQueue(job.VideoID, job.LeaseOwner); err != nil {
		log.Printf("[downloadpool] RemoveFromDownloadQueue %s: %v", job.VideoID, err)
	}

	// Enqueue preview generation.
	if duration > 0 {
		absVideoPath := videoPath
		if !filepath.IsAbs(absVideoPath) {
			absVideoPath = filepath.Join(m.cfg.DataDir, absVideoPath)
		}
		m.EnqueuePreview(PreviewRequest{
			VideoID:  job.VideoID,
			FilePath: absVideoPath,
			Duration: float64(duration),
		})
	}

	// Fetch comments in background — only YouTube supports yt-dlp comment extraction.
	if platform == "youtube" {
		capturedURL := sourceURL
		capturedID := job.VideoID
		capturedOpts := opts
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			comments, err := m.downloader.YtDlp.FetchComments(bgCtx, capturedURL, download.DefaultCommentFetchLimit, capturedOpts)
			if err != nil {
				log.Printf("[downloadpool] comments fetch failed for %s: %v", capturedID, err)
				return
			}
			inserted, _ := m.db.AddComments(capturedID, comments, "youtube")
			// yt-dlp includes commenter thumbnails in the comment payload; commenters
			// are not clickable Igloo profiles, so avoid profile/avatar recovery here.
			log.Printf("[downloadpool] fetched %d comments for %s", inserted, capturedID)
		}()
	}

	// Fetch DeArrow branding + SponsorBlock segments in background — YouTube only.
	if platform == "youtube" {
		capturedID := job.VideoID
		capturedPath := relVideoPath
		capturedPlatform := platform
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			bgCtx, cancel := context.WithTimeout(m.ctx, 60*time.Second)
			defer cancel()
			m.triggerYoutubeEnrichFetch(bgCtx, capturedID, capturedPath, capturedPlatform)
		}()
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	log.Printf("[downloadpool] completed %s (%s, %d bytes, %s)", job.VideoID, title, fileSize, elapsed)
	m.EmitDownload(fmt.Sprintf("Completed: %s", title), "done", job.ChannelID, platform)
	atomic.AddInt32(&m.dlSessionCompleted, 1)
	m.dlLastDownload.Store(&LastDownloadInfo{
		Channel:   job.ChannelID,
		Platform:  platform,
		Timestamp: time.Now().Unix(),
	})
}

// failDownloadJob increments retry count and either re-queues or fails permanently.
func (m *Manager) failDownloadJob(job db.DownloadQueueRow, err error) {
	if err == nil {
		err = fmt.Errorf("unknown download error")
	}
	newRetry := job.RetryCount + 1
	classification := download.ClassifyFailure(err, nil, newRetry)
	strategy := classification.Strategy
	if classification.Permanent || newRetry >= 5 {
		if strategy == "" {
			strategy = download.ErrorStrategyPermanent
		}
		log.Printf("[downloadpool] job %s failed permanently after %d retries: %v", job.VideoID, newRetry, err)
		if err := m.db.UpdateDownloadQueueStatus(job.VideoID, job.LeaseOwner, "failed", err.Error(), newRetry, classification.Kind, strategy, 0, time.Now().UnixMilli()); err != nil {
			log.Printf("[downloadpool] UpdateDownloadQueueStatus %s: %v", job.VideoID, err)
		}
		atomic.AddInt32(&m.dlSessionFailed, 1)
		return
	}
	if strategy == "" {
		strategy = download.ErrorStrategyRetry
	}
	log.Printf("[downloadpool] job %s queued for retry %d: %v", job.VideoID, newRetry, err)
	if err := m.db.UpdateDownloadQueueStatus(job.VideoID, job.LeaseOwner, "pending", err.Error(), newRetry, classification.Kind, strategy, classification.RetryDelay, time.Now().UnixMilli()); err != nil {
		log.Printf("[downloadpool] UpdateDownloadQueueStatus %s: %v", job.VideoID, err)
	}
}

func downloadPoolLeaseOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("downloadpool:%s:%d", host, os.Getpid())
}

func (m *Manager) startDownloadQueueLeaseRenewal(ctx context.Context, job db.DownloadQueueRow) func() {
	if m == nil || m.db == nil || job.VideoID == "" || job.LeaseOwner == "" {
		return nil
	}
	renewCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(downloadPoolLeaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				if err := m.db.RenewDownloadQueueLease(job.VideoID, job.LeaseOwner, time.Now().UnixMilli(), downloadPoolLeaseDuration); err != nil {
					log.Printf("[downloadpool] RenewDownloadQueueLease %s: %v", job.VideoID, err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// cookiesFor returns (cookiesFile, cookiesFromBrowser) for a platform.
// Prefers an enabled cookies file if one exists; falls back to
// --cookies-from-browser if configured via cookies_{platform}_browser DB setting.
func (m *Manager) cookiesFor(platform string) (string, string) {
	sets := m.cookieSetsFor(platform)
	if len(sets) == 0 {
		return "", ""
	}
	return sets[0].File, sets[0].Browser
}

func (m *Manager) cookieFileAndBrowserFor(platform string) (string, string) {
	return download.CookieFileAndBrowser(m.cookieSetsFor(platform))
}

func (m *Manager) cookieSetsFor(platform string) []download.CookieSet {
	fileEnabled := "1"
	if m.db != nil {
		fileEnabled, _ = m.db.GetSetting("cookies_"+platform+"_enabled", "1")
	}
	browser := ""
	if m.db != nil {
		browser, _ = m.db.GetSetting("cookies_"+platform+"_browser", "")
	}
	cookiesDir := ""
	if m.cfg != nil {
		cookiesDir = m.cfg.CookiesDir
	}
	return download.ResolveCookieSets(cookiesDir, platform, fileEnabled != "0", browser)
}

func cookieFileCandidates(cookiesDir, platform string) []string {
	candidates := download.DiscoverCookieFiles(cookiesDir, platform)
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Path)
	}
	return out
}

// platformSemFor returns the semaphore for the given platform.
// Falls back to the YouTube semaphore (1 concurrent) for unknown platforms.
func platformSemFor(platform string) chan struct{} {
	if sem, ok := platformSem[platform]; ok {
		return sem
	}
	return platformSem["youtube"]
}

// --- Pure helper functions (testable) ---

// buildSourceURL constructs the video URL from platform, source ID, and video ID.
func buildSourceURL(platform, sourceID, videoID string) string {
	switch platform {
	case "tiktok":
		return fmt.Sprintf("https://www.tiktok.com/@%s/video/%s", sourceID, videoID)
	case "instagram":
		raw := strings.TrimPrefix(videoID, "instagram_")
		switch {
		case strings.HasPrefix(raw, "post_"):
			return fmt.Sprintf("https://www.instagram.com/p/%s/", strings.TrimPrefix(raw, "post_"))
		case strings.HasPrefix(raw, "reel_"):
			return fmt.Sprintf("https://www.instagram.com/reel/%s/", strings.TrimPrefix(raw, "reel_"))
		default:
			return fmt.Sprintf("https://www.instagram.com/reel/%s/", raw)
		}
	default:
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	}
}

// resolveFormatString returns the yt-dlp format string for a platform and quality.
// TikTok always uses its own format. YouTube defaults to 1080p if quality is empty or unknown.
func resolveFormatString(platform, quality string) string {
	if platform == "tiktok" || platform == "instagram" {
		return "bv*+ba/bv*/b"
	}
	if f, ok := qualityFormats[quality]; ok {
		return f
	}
	return qualityFormats["1080p"]
}

// extractPublishedAt extracts a published date from yt-dlp or gallery-dl metadata.
// Checks timestamp fields first (unix seconds), then date string fields.
// Returns 0 if no date is found — the DB convention for "unset".
func extractPublishedAt(metadata map[string]any) int64 {
	if metadata == nil {
		return 0
	}

	// 1. Timestamp fields — yt-dlp + gallery-dl already give us unix seconds.
	for _, key := range []string{"release_timestamp", "timestamp", "upload_timestamp", "createTime"} {
		if v, ok := metadata[key]; ok {
			var ts int64
			switch n := v.(type) {
			case float64:
				ts = int64(n)
			case int64:
				ts = n
			case int:
				ts = int64(n)
			case string:
				// gallery-dl stores createTime as a string
				if parsed, err := strconv.ParseInt(n, 10, 64); err == nil {
					ts = parsed
				}
			default:
				continue
			}
			if ts <= 0 {
				continue
			}
			return ts * 1000
		}
	}

	// 2. Date string fields.
	for _, key := range []string{"release_date", "upload_date", "published_at", "created_at", "date"} {
		if v, ok := metadata[key].(string); ok && v != "" {
			if ms := parseDateString(v); ms != 0 {
				return ms
			}
		}
	}

	return 0
}

func publishedAtForJob(metadata map[string]any, job db.DownloadQueueRow) int64 {
	if publishedAt := extractPublishedAt(metadata); publishedAt > 0 {
		return publishedAt
	}
	return job.PublishedAtMs
}

func videoDescriptionFromMetadata(metadata map[string]any) string {
	return metadataString(metadata, "description", "desc", "caption")
}

func videoTitleFromMetadata(metadata map[string]any, fallback string) string {
	if title := metadataString(metadata, "title"); title != "" {
		return title
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" || looksTruncatedSocialTitle(fallback) {
		if description := videoDescriptionFromMetadata(metadata); description != "" {
			return description
		}
	}
	return fallback
}

func metadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadata[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func looksTruncatedSocialTitle(title string) bool {
	title = strings.TrimSpace(title)
	return strings.HasSuffix(title, "...") || strings.HasSuffix(title, "…")
}

// eightDigitDate matches YYYYMMDD format.
var eightDigitDate = regexp.MustCompile(`^\d{8}$`)

// parseDateString attempts to parse a date string in various formats.
// parseDateString parses a metadata date string and returns unix-millis.
// Returns 0 when the string can't be parsed.
func parseDateString(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	// YYYYMMDD (8 digits) → midnight UTC that day.
	if eightDigitDate.MatchString(s) {
		if t, err := time.Parse("20060102", s); err == nil {
			return t.UTC().UnixMilli()
		}
	}

	// ISO date "YYYY-MM-DD" → midnight UTC.
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC().UnixMilli()
	}

	// ISO datetime — try common layouts.
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().UnixMilli()
		}
	}

	return 0
}

// findSiblingThumbnail looks for a thumbnail file next to the video file.
// Checks for {videoID}.{jpg,jpeg,webp,png,image} in the same directory.
func findSiblingThumbnail(videoPath, videoID string) string {
	dir := filepath.Dir(videoPath)
	for _, ext := range []string{".jpg", ".jpeg", ".webp", ".png", ".image"} {
		p := filepath.Join(dir, videoID+ext)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func (m *Manager) downloadSiblingThumbnailFromMetadata(ctx context.Context, videoPath, videoID string, metadata map[string]any) string {
	if m.downloader == nil || m.downloader.HTTP == nil {
		return ""
	}
	thumbURL := thumbnailURLFromMetadata(metadata)
	if thumbURL == "" {
		return ""
	}
	p, err := m.downloader.HTTP.DownloadFile(ctx, thumbURL, filepath.Dir(videoPath), videoID+".image")
	if err != nil {
		log.Printf("[downloadpool] thumbnail %s: %v", videoID, err)
		return ""
	}
	return p
}

func thumbnailURLFromMetadata(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if s, ok := metadata["thumbnail"].(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	rawThumbs, ok := metadata["thumbnails"].([]any)
	if !ok {
		return ""
	}
	for _, raw := range rawThumbs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := m["url"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// loadInfoJSON loads the .info.json sidecar file for a video.
// Returns nil if the file doesn't exist or can't be parsed.
func loadInfoJSON(videoPath, videoID string) map[string]any {
	dir := filepath.Dir(videoPath)
	p := filepath.Join(dir, videoID+".info.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// extractDurationFromMetadata extracts duration as an integer from metadata.
// Used internally; exported for testing if needed.
func extractDurationFromMetadata(metadata map[string]any) int {
	if metadata == nil {
		return 0
	}
	if d, ok := metadata["duration"]; ok {
		switch v := d.(type) {
		case float64:
			return int(v)
		case int:
			return v
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 0
}
