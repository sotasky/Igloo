package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
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

const (
	downloadBatchSize  = 1
	feedMediaBurstSize = 32
)

var (
	downloadPoolLeaseDuration      = 5 * time.Minute
	downloadPoolLeaseRenewInterval = downloadPoolLeaseDuration / 2
)

// short-form downloads share one pacing clock to avoid rapid cross-platform bursts.
var (
	tiktokDlMu       sync.Mutex
	tiktokDlLastTime time.Time
)

const downloadAuthBackoff = 3 * time.Hour
const shortFormDownloadDelayFallback = 60 * time.Second

type downloadPlatformBackoff struct {
	Until time.Time
	Kind  string
}

// qualityFormats maps quality names to yt-dlp format strings.
var qualityFormats = map[string]string{
	"2160p": "bestvideo[height<=2160]+bestaudio/best[height<=2160]/best",
	"1440p": "bestvideo[height<=1440]+bestaudio/best[height<=1440]/best",
	"1080p": "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best",
	"720p":  "bestvideo[height<=720]+bestaudio/best[height<=720]/best",
	"480p":  "bestvideo[height<=480]+bestaudio/best[height<=480]/best",
	"best":  "best",
}

func (m *Manager) runMediaExecutor(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	log.Printf("[media] executor started")
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.downloadKick:
		case <-m.feedMediaKick:
		case <-timer.C:
		}
		if !m.IsStopRequested() {
			for i := 0; i < feedMediaBurstSize && m.processFeedMediaBatch(ctx); i++ {
			}
			m.processDownloadBatch(ctx)
		}
		delay, err := m.db.NextMediaWorkDelay(time.Now().UnixMilli())
		if err != nil {
			log.Printf("[media] next due: %v", err)
			delay = time.Minute
		}
		if delay < 10*time.Millisecond {
			delay = 10 * time.Millisecond
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}
}

// processDownloadBatch claims one due video mutation for the media executor.
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

		m.downloadVideo(ctx, job, ch.Platform, ch.SourceID, ch.Quality, subtitles)
	}
}

// downloadVideo handles a single video download job.
func (m *Manager) downloadVideo(ctx context.Context, job db.DownloadQueueRow, platform, sourceID, quality string, subtitles bool) {
	if stopRenew := m.startDownloadQueueLeaseRenewal(ctx, job); stopRenew != nil {
		defer stopRenew()
	}

	if backoff, ok := m.activeDownloadPlatformBackoff(platform, time.Now()); ok {
		m.postponeDownloadJobForPlatformBackoff(job, platform, backoff)
		return
	}
	ownerKind, ok := db.VideoOwnerKindForPlatform(platform)
	if !ok || ownerKind == "tweet" {
		m.failDownloadJob(job, fmt.Errorf("unsupported download platform %q", platform))
		return
	}

	// Short-form rate-limit pacing: enforce minimum gap between downloads,
	// measured from the END of the previous download attempt.
	if isShortFormDownloadPlatform(platform) {
		downloadDelay := m.shortFormDownloadDelay(platform)
		tiktokDlMu.Lock()
		if elapsed := time.Since(tiktokDlLastTime); elapsed < downloadDelay {
			time.Sleep(downloadDelay - elapsed)
		}
		tiktokDlMu.Unlock()
	}
	defer func() {
		if isShortFormDownloadPlatform(platform) {
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
	videoDir, err := m.cfg.Storage.WritePath("media/" + platform + "/" + safeSourceID)
	if err != nil {
		m.failDownloadJob(job, fmt.Errorf("storage path: %w", err))
		return
	}
	if err := m.downloader.RunMedia(ctx, download.MediaLaneBulk, func() error { return os.MkdirAll(videoDir, 0o755) }); err != nil {
		log.Printf("[downloadpool] mkdir %s: %v", videoDir, err)
		m.failDownloadJob(job, fmt.Errorf("mkdir: %w", err))
		return
	}
	attemptID, err := newDownloadAttemptID(job.VideoID)
	if err != nil {
		m.failDownloadJob(job, fmt.Errorf("allocate download attempt: %w", err))
		return
	}
	subtitleDir := ""
	if subtitles {
		subtitleDir, err = m.cfg.Storage.WritePath("subtitles/" + platform)
		if err != nil {
			m.failDownloadJob(job, fmt.Errorf("subtitle storage path: %w", err))
			return
		}
	}

	sourceURL := buildSourceURL(platform, safeSourceID, job.VideoID)
	formatStr := resolveFormatString(platform, quality)

	cookiesFile, cookiesBrowser := m.cookiesFor(platform)
	opts := download.Opts{
		OutputDir:          videoDir,
		ID:                 attemptID,
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		CookieAlternates:   m.cookieSetsFor(platform),
		Format:             formatStr,
		Subtitles:          subtitles,
		SubtitleDir:        subtitleDir,
	}

	completed, reused, dlErr := m.reusableCompletedVideo(ctx, videoDir, job.VideoID, platform)
	if dlErr == nil && !reused {
		completed, dlErr = m.downloader.DownloadCompleted(ctx, sourceURL, "video", opts)
	}

	if dlErr != nil {
		m.removeFailedAttempt(ctx, completedVideoFiles{}, completed)
		log.Printf("[downloadpool] download %s: %v", job.VideoID, dlErr)
		m.EmitDownload(fmt.Sprintf("Failed: %s — %v", job.Title, dlErr), "error", job.ChannelID, platform)
		m.failDownloadJob(job, fmt.Errorf("download: %w", dlErr))
		return
	}

	if len(completed.MediaPaths) == 0 {
		m.removeFailedAttempt(ctx, completedVideoFiles{}, completed)
		log.Printf("[downloadpool] no files returned for %s", job.VideoID)
		m.EmitDownload(fmt.Sprintf("Failed: %s — no files returned", job.Title), "error", job.ChannelID, platform)
		m.failDownloadJob(job, fmt.Errorf("no files returned"))
		return
	}
	if reused {
		log.Printf("[downloadpool] reusing existing media for %s", job.VideoID)
	}

	metadata := loadInfoJSONFile(completed.InfoJSONPath)
	files, err := m.prepareCompletedVideoFiles(ctx, platform, attemptID, completed)
	if err != nil {
		if !reused {
			m.removeFailedAttempt(ctx, files, completed)
		}
		m.failDownloadJob(job, fmt.Errorf("prepare completed outputs: %w", err))
		return
	}

	// Short-form slideshows: gallery-dl returns multiple image files.
	// Build a slides array in metadata so EnrichForCard detects slideshow.
	if (platform == "tiktok" || platform == "instagram") && len(files.imageKeys) > 1 {
		slides := make([]any, len(files.imageKeys))
		for i, key := range files.imageKeys {
			slides[i] = map[string]any{"path": key}
		}
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["slides"] = slides
		metadata["vcodec"] = "none"
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
			mediaKind, slideCount = model.ComputeMediaKind(&meta, files.primaryKey)
		}
	}
	if mediaKind == "" {
		mediaKind, slideCount = model.ComputeMediaKind(nil, files.primaryKey)
	}

	// Use richer post metadata when a flat channel check only carried a
	// truncated social caption.
	title := videoTitleFromMetadata(metadata, job.Title)

	if err := m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: job.VideoID, ChannelID: job.ChannelID, OwnerKind: ownerKind, Title: title, Description: description,
		Duration: duration, PublishedAtMs: publishedAt, MetadataJSON: metadataJSON,
		MediaKind: mediaKind, SlideCount: slideCount, Assets: files.assets,
	}); err != nil {
		if !reused {
			m.removeFailedAttempt(ctx, files, completed)
		}
		log.Printf("[downloadpool] StoreCompletedVideo %s: %v", job.VideoID, err)
		m.failDownloadJob(job, fmt.Errorf("db insert: %w", err))
		return
	}
	if err := m.storeCompletedSubtitles(ctx, job.VideoID, files, completed, reused); err != nil {
		log.Printf("[downloadpool] StoreVideoSubtitleAssets %s: %v", job.VideoID, err)
	}
	if !reused {
		m.removeTransientFiles(ctx, files)
	}

	// Remove from download queue.
	if err := m.db.RemoveFromDownloadQueue(job.VideoID, job.LeaseOwner); err != nil {
		log.Printf("[downloadpool] RemoveFromDownloadQueue %s: %v", job.VideoID, err)
	}

	m.enqueueCompletedVideoPreview(job.VideoID, platform, files.primaryPath, float64(duration))

	// Fetch comments in background — only YouTube supports yt-dlp comment extraction.
	if platform == "youtube" {
		capturedURL := sourceURL
		capturedID := job.VideoID
		capturedOpts := opts
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			parent := m.ctx
			if parent == nil {
				parent = context.Background()
			}
			bgCtx, cancel := context.WithTimeout(parent, 2*time.Minute)
			defer cancel()
			comments, err := m.downloader.YtDlp.FetchComments(bgCtx, capturedURL, download.DefaultCommentFetchLimit, capturedOpts)
			if err != nil {
				log.Printf("[downloadpool] comments fetch failed for %s: %v", capturedID, err)
				return
			}
			inserted, err := m.db.AddComments(capturedID, comments)
			if err != nil {
				log.Printf("[downloadpool] store comments for %s: %v", capturedID, err)
				return
			}
			m.KickFeedMedia()
			log.Printf("[downloadpool] fetched %d comments for %s", inserted, capturedID)
		}()
	}

	// Fetch DeArrow branding + SponsorBlock segments in background — YouTube only.
	if platform == "youtube" {
		capturedID := job.VideoID
		capturedPath := files.primaryKey
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
	log.Printf("[downloadpool] completed %s (%s, %s)", job.VideoID, title, elapsed)
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
	tool, cookieLabel := download.ErrorOperationContext(err)
	classification = retryShortFormAuthFailure(job, classification)
	m.recordDownloadPlatformBackoff(job, classification)
	strategy := classification.Strategy
	if classification.Permanent || newRetry >= 5 {
		if strategy == "" {
			strategy = download.ErrorStrategyPermanent
		}
		log.Printf("[downloadpool] job %s failed permanently after %d retries: %v", job.VideoID, newRetry, err)
		if err := m.db.UpdateDownloadQueueStatusWithContext(job.VideoID, job.LeaseOwner, "failed", err.Error(), newRetry, classification.Kind, strategy, 0, time.Now().UnixMilli(), tool, cookieLabel); err != nil {
			log.Printf("[downloadpool] UpdateDownloadQueueStatus %s: %v", job.VideoID, err)
		}
		atomic.AddInt32(&m.dlSessionFailed, 1)
		return
	}
	if strategy == "" {
		strategy = download.ErrorStrategyRetry
	}
	log.Printf("[downloadpool] job %s queued for retry %d: %v", job.VideoID, newRetry, err)
	if err := m.db.UpdateDownloadQueueStatusWithContext(job.VideoID, job.LeaseOwner, "pending", err.Error(), newRetry, classification.Kind, strategy, classification.RetryDelay, time.Now().UnixMilli(), tool, cookieLabel); err != nil {
		log.Printf("[downloadpool] UpdateDownloadQueueStatus %s: %v", job.VideoID, err)
	}
}

func retryShortFormAuthFailure(job db.DownloadQueueRow, classification download.FailureClassification) download.FailureClassification {
	if classification.Kind != download.ErrorKindAuth {
		return classification
	}
	if !isShortFormDownloadPlatform(platformFromDownloadChannelID(job.ChannelID)) {
		return classification
	}
	classification.Permanent = false
	classification.Strategy = download.ErrorStrategyRetry
	if classification.RetryDelay <= 0 {
		classification.RetryDelay = downloadAuthBackoff
	}
	return classification
}

func (m *Manager) recordDownloadPlatformBackoff(job db.DownloadQueueRow, classification download.FailureClassification) {
	platform := platformFromDownloadChannelID(job.ChannelID)
	if platform == "" {
		return
	}
	var delay time.Duration
	switch classification.Kind {
	case download.ErrorKindRateLimit:
		delay = classification.RetryDelay
		if delay <= 0 {
			delay = time.Hour
		}
	case download.ErrorKindAuth:
		if !isShortFormDownloadPlatform(platform) {
			return
		}
		delay = downloadAuthBackoff
	default:
		return
	}
	until := time.Now().Add(delay)
	extended := m.setDownloadPlatformBackoff(platform, downloadPlatformBackoff{Until: until, Kind: classification.Kind})
	if extended {
		log.Printf("[downloadpool] %s downloads cooling down until %s after %s", platform, until.Format(time.RFC3339), classification.Kind)
		if m.activity != nil && m.dlActivity != nil {
			m.EmitDownload(fmt.Sprintf("%s downloads cooling down until %s after %s", platform, until.Format(time.RFC3339), classification.Kind), "warning", "", platform)
		}
	}
}

func platformFromDownloadChannelID(channelID string) string {
	switch {
	case strings.HasPrefix(channelID, "instagram_"):
		return "instagram"
	case strings.HasPrefix(channelID, "tiktok_"):
		return "tiktok"
	case strings.HasPrefix(channelID, "youtube_"):
		return "youtube"
	default:
		return ""
	}
}

func (m *Manager) setDownloadPlatformBackoff(platform string, backoff downloadPlatformBackoff) bool {
	if m == nil || platform == "" || backoff.Until.IsZero() {
		return false
	}
	m.downloadBackoffMu.Lock()
	defer m.downloadBackoffMu.Unlock()
	if m.downloadBackoff == nil {
		m.downloadBackoff = make(map[string]downloadPlatformBackoff)
	}
	existing := m.downloadBackoff[platform]
	if !existing.Until.IsZero() && !backoff.Until.After(existing.Until) {
		return false
	}
	m.downloadBackoff[platform] = backoff
	return true
}

func (m *Manager) activeDownloadPlatformBackoff(platform string, now time.Time) (downloadPlatformBackoff, bool) {
	if m == nil || platform == "" {
		return downloadPlatformBackoff{}, false
	}
	m.downloadBackoffMu.Lock()
	defer m.downloadBackoffMu.Unlock()
	backoff, ok := m.downloadBackoff[platform]
	if !ok || backoff.Until.IsZero() {
		return downloadPlatformBackoff{}, false
	}
	if !now.Before(backoff.Until) {
		delete(m.downloadBackoff, platform)
		return downloadPlatformBackoff{}, false
	}
	return backoff, true
}

func (m *Manager) postponeDownloadJobForPlatformBackoff(job db.DownloadQueueRow, platform string, backoff downloadPlatformBackoff) {
	if m == nil || m.db == nil {
		return
	}
	now := time.Now()
	delay := backoff.Until.Sub(now)
	if delay <= 0 {
		delay = time.Second
	}
	kind := backoff.Kind
	if kind == "" {
		kind = download.ErrorKindRateLimit
	}
	reason := fmt.Sprintf("%s download backoff active until %s", platform, backoff.Until.Format(time.RFC3339))
	if err := m.db.UpdateDownloadQueueStatusWithContext(job.VideoID, job.LeaseOwner, "pending", reason, job.RetryCount, kind, download.ErrorStrategyRetry, delay, now.UnixMilli(), "", ""); err != nil {
		log.Printf("[downloadpool] postpone %s during %s backoff: %v", job.VideoID, platform, err)
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

func isShortFormDownloadPlatform(platform string) bool {
	return platform == "tiktok" || platform == "instagram"
}

func (m *Manager) shortFormDownloadDelay(platform string) time.Duration {
	if m != nil && m.db != nil {
		if delay := m.platformFetchDelay(platform); delay > 0 {
			return delay
		}
	}
	return shortFormDownloadDelayFallback
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
