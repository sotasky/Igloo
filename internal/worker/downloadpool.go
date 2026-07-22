package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/subtitlemeta"
)

const mediaNoWorkPollFloor = 5 * time.Second

type mediaWorkClass uint8

const (
	mediaWorkAssetCurrent mediaWorkClass = iota
	mediaWorkVideoCurrent
	mediaWorkAssetBackfill
	mediaWorkVideoBackfill
)

var (
	downloadPoolLeaseDuration      = 5 * time.Minute
	downloadPoolLeaseRenewInterval = downloadPoolLeaseDuration / 2
)

const downloadAuthBackoff = 3 * time.Hour

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

type mediaWorkerKind uint8

const (
	mediaWorkerCurrent mediaWorkerKind = iota
	mediaWorkerBackfill
)

func (m *Manager) runMediaCurrentLoop(ctx context.Context) {
	m.runMediaWorkLoop(ctx, mediaWorkerCurrent)
}

func (m *Manager) runMediaBackfillLoop(ctx context.Context) {
	m.runMediaWorkLoop(ctx, mediaWorkerBackfill)
}

func (m *Manager) runMediaWorkLoop(ctx context.Context, kind mediaWorkerKind) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	kick := m.mediaCurrentKick
	label := "current"
	if kind == mediaWorkerBackfill {
		kick = m.mediaBackfillKick
		label = "backfill"
	}
	log.Printf("[media] %s worker started", label)
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-timer.C:
		}
		if m.IsStopRequested() {
			resetMediaTimer(timer, time.Hour)
			continue
		}
		now := time.Now()
		if delay := m.externalRetryDelay(now); delay > 0 {
			resetMediaTimer(timer, delay)
			continue
		}
		worked, previewDelay := m.processNextMediaWork(ctx, now, kind)
		if worked {
			resetMediaTimer(timer, 10*time.Millisecond)
			continue
		}
		eligiblePlatforms, backoffDelay := m.downloadSchedulingState(now)
		includeTweets := m.cfg == nil || m.cfg.PlatformEnabled("twitter")
		lane := db.DownloadLaneCurrent
		if kind == mediaWorkerBackfill {
			lane = db.DownloadLaneBackfill
		}
		delay, err := m.db.NextMediaWorkDelay(now.UnixMilli(), eligiblePlatforms, includeTweets, lane)
		if err != nil {
			log.Printf("[media] next due: %v", err)
			delay = time.Minute
		}
		if backoffDelay > 0 && backoffDelay < delay {
			delay = backoffDelay
		}
		if previewDelay > 0 && previewDelay < delay {
			delay = previewDelay
		}
		if delay < mediaNoWorkPollFloor {
			delay = mediaNoWorkPollFloor
		}
		resetMediaTimer(timer, delay)
	}
}

func (m *Manager) processNextMediaWork(ctx context.Context, now time.Time, kind mediaWorkerKind) (bool, time.Duration) {
	if kind == mediaWorkerCurrent {
		order := mediaCurrentWorkOrder(m.mediaCurrentTurn)
		for _, workClass := range order {
			worked := false
			switch workClass {
			case mediaWorkAssetCurrent:
				worked = m.processContentAssetBatch(ctx, db.DownloadLaneCurrent)
			case mediaWorkVideoCurrent:
				worked = m.processDownloadBatch(ctx, db.DownloadLaneCurrent)
			}
			if worked {
				m.mediaCurrentTurn++
				return true, 0
			}
		}
		return false, 0
	}

	var previewDelay time.Duration
	if m.hasPreviewHint() {
		worked, delay := m.processRequestedPreview(ctx, now)
		previewDelay = delay
		if worked {
			return true, previewDelay
		}
	}
	for _, workClass := range mediaBackfillPrimaryWorkOrder(m.mediaBackgroundTurn) {
		worked := false
		switch workClass {
		case mediaWorkAssetBackfill:
			worked = m.processContentAssetBatch(ctx, db.DownloadLaneBackfill)
		case mediaWorkVideoBackfill:
			worked = m.processDownloadBatch(ctx, db.DownloadLaneBackfill)
		}
		if !worked {
			continue
		}
		m.mediaBackgroundTurn++
		return true, previewDelay
	}
	worked, delay := m.processPreviewBatch(ctx, now)
	if previewDelay == 0 || delay > 0 && delay < previewDelay {
		previewDelay = delay
	}
	return worked, previewDelay
}

func mediaCurrentWorkOrder(currentTurn uint64) [2]mediaWorkClass {
	order := [2]mediaWorkClass{mediaWorkAssetCurrent, mediaWorkVideoCurrent}
	if currentTurn%2 == 1 {
		order[0], order[1] = order[1], order[0]
	}
	return order
}

func mediaBackfillPrimaryWorkOrder(backgroundTurn uint64) [2]mediaWorkClass {
	order := [2]mediaWorkClass{mediaWorkAssetBackfill, mediaWorkVideoBackfill}
	if backgroundTurn%2 == 1 {
		order[0], order[1] = order[1], order[0]
	}
	return order
}

func resetMediaTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

// processDownloadBatch claims one due canonical video download.
func (m *Manager) processDownloadBatch(ctx context.Context, lane db.DownloadLane) bool {
	owner := downloadPoolLeaseOwner()
	job, ok, err := m.claimDownloadWorkInLane(owner, lane, time.Now())
	if err != nil {
		log.Printf("[downloadpool] ClaimDownloadWork: %v", err)
		return false
	}
	if !ok {
		return false
	}
	ch, err := m.db.GetChannel(job.OwnerChannelID)
	if err != nil || ch == nil {
		log.Printf("[downloadpool] GetChannel %s: %v", job.OwnerChannelID, err)
		m.failDownloadJob(job, fmt.Errorf("owner channel not found"))
		return true
	}
	platform := job.Platform
	if platform == "" {
		platform = ch.Platform
	}
	if m.cfg != nil && !m.cfg.PlatformEnabled(platform) {
		reason := fmt.Sprintf("platform disabled: %s", platform)
		log.Printf("[downloadpool] postpone %s: %s", job.VideoID, reason)
		if err := m.db.RetryDownloadWork(job.VideoID, job.LeaseOwner, "platform_disabled", reason, 6*time.Hour, time.Now().UnixMilli()); err != nil {
			log.Printf("[downloadpool] RetryDownloadWork %s: %v", job.VideoID, err)
		}
		return true
	}

	quality := ch.Quality
	subtitles := false
	settingsChannelID := job.SourceChannelID
	if settingsChannelID == "" {
		settingsChannelID = job.OwnerChannelID
	}
	if settings, settingsErr := m.db.GetChannelSettings(settingsChannelID); settingsErr == nil && settings != nil {
		subtitles = settings.DownloadSubtitles
		if settings.Quality != "" {
			quality = settings.Quality
		}
	}

	m.downloadVideo(ctx, job, platform, ch.SourceID, quality, subtitles)
	return true
}

func (m *Manager) claimDownloadWorkInLane(owner string, lane db.DownloadLane, now time.Time) (db.DownloadWork, bool, error) {
	m.downloadPlatformMu.Lock()
	defer m.downloadPlatformMu.Unlock()
	platforms := m.enabledDownloadPlatforms()
	if m.downloadPlatformAt == nil {
		m.downloadPlatformAt = make(map[db.DownloadLane]int, 2)
	}
	start := m.downloadPlatformAt[lane]
	for offset := 0; offset < len(platforms); offset++ {
		index := (start + offset) % len(platforms)
		platform := platforms[index]
		if _, cooling := m.activeDownloadPlatformBackoff(platform, now); cooling {
			continue
		}
		work, ok, err := m.db.ClaimDownloadWork(owner, lane, platform, now.UnixMilli(), downloadPoolLeaseDuration)
		if err != nil {
			return db.DownloadWork{}, false, err
		}
		if !ok {
			continue
		}
		m.downloadPlatformAt[lane] = (index + 1) % len(platforms)
		return work, true, nil
	}
	return db.DownloadWork{}, false, nil
}

// downloadVideo handles a single video download job.
func (m *Manager) downloadVideo(ctx context.Context, job db.DownloadWork, platform, sourceID, quality string, subtitles bool) {
	if stopRenew := m.startDownloadWorkLeaseRenewal(ctx, job); stopRenew != nil {
		defer stopRenew()
	}

	ownerKind, ok := db.VideoOwnerKindForPlatform(platform)
	if !ok || ownerKind == "tweet" {
		m.failDownloadJob(job, fmt.Errorf("unsupported download platform %q", platform))
		return
	}
	mediaLane := download.MediaLaneBulkRegular
	if job.Lane == db.DownloadLaneCurrent {
		mediaLane = download.MediaLaneBulkForeground
	}
	isStory := job.SourceComponent == sourceComponentStories

	start := time.Now()
	m.EmitDownload(fmt.Sprintf("Downloading: %s", job.Title), "start", job.SourceChannelID, platform)
	safeSourceID := sourceID
	if platform == "tiktok" {
		if h := model.TikTokHandleFromChannelID(job.OwnerChannelID); h != "" {
			safeSourceID = h
		}
	}
	if isStory {
		_, valid := nativeStoryID(platform, job.VideoID)
		if !valid {
			m.failDownloadJob(job, fmt.Errorf("invalid %s story id %q", platform, job.VideoID))
			return
		}
		switch platform {
		case "tiktok":
			safeSourceID = model.TikTokHandleFromChannelID(job.OwnerChannelID)
		case "instagram":
			safeSourceID = model.InstagramHandleFromChannelID(job.OwnerChannelID)
		}
		if safeSourceID == "" {
			m.failDownloadJob(job, fmt.Errorf("story owner %q has no valid %s handle", job.OwnerChannelID, platform))
			return
		}
		subtitles = false
	}
	videoPath := "media/" + platform + "/" + safeSourceID
	if isStory {
		videoPath += "/stories"
	}
	videoDir, err := m.cfg.Storage.WritePath(videoPath)
	if err != nil {
		m.failDownloadJob(job, fmt.Errorf("storage path: %w", err))
		return
	}
	if err := m.downloader.RunMedia(ctx, mediaLane, func() error { return os.MkdirAll(videoDir, 0o755) }); err != nil {
		log.Printf("[downloadpool] mkdir %s: %v", videoDir, err)
		m.failDownloadJob(job, fmt.Errorf("mkdir: %w", err))
		return
	}
	outputID, err := downloadOutputID(job.VideoID)
	if err != nil {
		m.failDownloadJob(job, fmt.Errorf("prepare download output: %w", err))
		return
	}
	sourceURL := buildSourceURL(platform, safeSourceID, job.VideoID)
	formatStr := resolveFormatString(platform, quality)

	cookiesFile, cookiesBrowser := m.cookiesFor(platform)
	opts := download.Opts{
		OutputDir:          videoDir,
		ID:                 outputID,
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		CookieAlternates:   m.cookieSetsFor(platform),
		Format:             formatStr,
		Subtitles:          false,
	}

	if !m.externalWorkAllowed(time.Now()) {
		if err := m.db.ReleaseDownloadWork(job.VideoID, job.LeaseOwner); err != nil {
			log.Printf("[downloadpool] release %s while network probe is active: %v", job.VideoID, err)
		}
		return
	}
	completed, dlErr := m.downloader.DownloadCompleted(ctx, mediaLane, sourceURL, "video", opts)

	if dlErr != nil {
		m.removeFailedAttempt(ctx, mediaLane, completedVideoFiles{}, completed)
		log.Printf("[downloadpool] download %s: %v", job.VideoID, dlErr)
		if m.ReportExternalResult(dlErr) {
			m.EmitDownload(fmt.Sprintf("Paused: %s — internet unavailable", job.Title), "warning", job.SourceChannelID, platform)
			if err := m.db.ReleaseDownloadWork(job.VideoID, job.LeaseOwner); err != nil {
				log.Printf("[downloadpool] release %s after network failure: %v", job.VideoID, err)
			}
			return
		}
		m.EmitDownload(fmt.Sprintf("Failed: %s — %v", job.Title, dlErr), "error", job.SourceChannelID, platform)
		m.failDownloadJob(job, fmt.Errorf("download: %w", dlErr))
		return
	}
	m.ReportExternalResult(nil)

	if len(completed.MediaPaths) == 0 {
		m.removeFailedAttempt(ctx, mediaLane, completedVideoFiles{}, completed)
		log.Printf("[downloadpool] no files returned for %s", job.VideoID)
		m.EmitDownload(fmt.Sprintf("Failed: %s — no files returned", job.Title), "error", job.SourceChannelID, platform)
		m.failDownloadJob(job, fmt.Errorf("no files returned"))
		return
	}
	metadata := completed.Metadata
	files, err := m.prepareCompletedVideoFiles(ctx, mediaLane, completed)
	if err != nil {
		m.removeFailedAttempt(ctx, mediaLane, files, completed)
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
	if isStory {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["webpage_url"] = sourceURL
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

	sourceKind := ""
	if isStory {
		sourceKind = "story"
	}
	video := db.CompletedVideo{
		VideoID: job.VideoID, ChannelID: job.OwnerChannelID, OwnerKind: ownerKind, Title: title, Description: description,
		Duration: duration, PublishedAtMs: publishedAt, MetadataJSON: metadataJSON,
		MediaKind: mediaKind, SlideCount: slideCount, SourceKind: sourceKind, Assets: files.assets,
	}
	var subtitleAsset *db.Asset
	if subtitles {
		isAuto := subtitlemeta.IsAuto(metadata, "en")
		subtitleAsset = &db.Asset{
			AssetID:        db.BuildAssetID(platform, ownerKind, job.VideoID, "subtitle", 0),
			AssetKind:      "subtitle",
			OwnerKind:      ownerKind,
			OwnerID:        job.VideoID,
			SourceURL:      sourceURL,
			ContentType:    "text/vtt",
			DownloadLane:   db.DownloadLaneBackfill,
			IsAuto:         &isAuto,
			AudioLanguage:  subtitlemeta.Language(metadata),
			RequiredReason: "retention",
		}
	}
	if err := m.storeCompletedVideoOutputs(ctx, mediaLane, platform, outputID, video, files, completed, subtitleAsset); err != nil {
		log.Printf("[downloadpool] StoreCompletedVideo %s: %v", job.VideoID, err)
		m.failDownloadJob(job, fmt.Errorf("db insert: %w", err))
		return
	}

	if err := m.db.CompleteDownloadWork(job.VideoID, job.LeaseOwner); err != nil {
		log.Printf("[downloadpool] CompleteDownloadWork %s: %v", job.VideoID, err)
	}

	if job.Lane == db.DownloadLaneCurrent && platform == "youtube" {
		m.RequestVideoPreview(job.VideoID)
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	log.Printf("[downloadpool] completed %s (%s, %s)", job.VideoID, title, elapsed)
	m.EmitDownload(fmt.Sprintf("Completed: %s", title), "done", job.SourceChannelID, platform)
	atomic.AddInt32(&m.dlSessionCompleted, 1)
	m.dlLastDownload.Store(&LastDownloadInfo{
		Channel:   job.SourceChannelID,
		Platform:  platform,
		Timestamp: time.Now().Unix(),
	})
	if platform == "youtube" {
		m.startYoutubePostDownloadEnrichment(sourceURL, job.VideoID, files.primaryKey, opts)
	}
}

func (m *Manager) startYoutubePostDownloadEnrichment(sourceURL, videoID, videoPath string, opts download.Opts) {
	if m == nil || m.youtubeEnrichmentSlots == nil {
		return
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		select {
		case m.youtubeEnrichmentSlots <- struct{}{}:
		case <-parent.Done():
			return
		}
		defer func() { <-m.youtubeEnrichmentSlots }()

		enrichCtx, enrichCancel := context.WithTimeout(parent, time.Minute)
		m.triggerYoutubeEnrichFetch(enrichCtx, videoID, videoPath, "youtube")
		enrichCancel()

		commentsCtx, commentsCancel := context.WithTimeout(parent, 2*time.Minute)
		comments, err := m.downloader.YtDlp.FetchComments(commentsCtx, sourceURL, download.DefaultCommentFetchLimit, opts)
		commentsCancel()
		if err != nil {
			log.Printf("[downloadpool] comments fetch failed for %s: %v", videoID, err)
			return
		}
		inserted, err := m.db.AddComments(videoID, comments)
		if err != nil {
			log.Printf("[downloadpool] store comments for %s: %v", videoID, err)
			return
		}
		m.KickMediaWork()
		log.Printf("[downloadpool] fetched %d comments for %s", inserted, videoID)
	}()
}

func (m *Manager) failDownloadJob(job db.DownloadWork, err error) {
	if err == nil {
		err = fmt.Errorf("unknown download error")
	}
	newRetry := job.RetryCount + 1
	classification := download.ClassifyFailure(err, nil, newRetry)
	classification = retryCredentialFailure(classification)
	m.recordDownloadPlatformBackoff(job, classification)
	if classification.Kind == download.ErrorKindNotFound && terminalHTTPNotFound(err) {
		log.Printf("[downloadpool] blocking missing video %s: %v", job.VideoID, err)
		if blockErr := m.db.BlockDownloadWork(job.VideoID, job.LeaseOwner, err.Error()); blockErr != nil {
			log.Printf("[downloadpool] BlockDownloadWork %s: %v", job.VideoID, blockErr)
		}
		atomic.AddInt32(&m.dlSessionFailed, 1)
		return
	}
	log.Printf("[downloadpool] job %s queued for retry %d: %v", job.VideoID, newRetry, err)
	if retryErr := m.db.RetryDownloadWork(job.VideoID, job.LeaseOwner, classification.Kind, err.Error(), classification.RetryDelay, time.Now().UnixMilli()); retryErr != nil {
		log.Printf("[downloadpool] RetryDownloadWork %s: %v", job.VideoID, retryErr)
	}
}

func terminalHTTPNotFound(err error) bool {
	var statusErr *download.HTTPStatusError
	return errors.As(err, &statusErr) && (statusErr.StatusCode == 404 || statusErr.StatusCode == 410)
}

func retryCredentialFailure(classification download.FailureClassification) download.FailureClassification {
	if classification.Kind != download.ErrorKindAuth {
		return classification
	}
	classification.Permanent = false
	classification.Strategy = download.ErrorStrategyRetry
	if classification.RetryDelay <= 0 {
		classification.RetryDelay = downloadAuthBackoff
	}
	return classification
}

func (m *Manager) recordDownloadPlatformBackoff(job db.DownloadWork, classification download.FailureClassification) {
	platform := job.Platform
	if platform == "" {
		platform = platformFromDownloadChannelID(job.OwnerChannelID)
	}
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

func (m *Manager) downloadSchedulingState(now time.Time) ([]string, time.Duration) {
	platforms := m.enabledDownloadPlatforms()
	if m == nil {
		return platforms, 0
	}
	m.downloadBackoffMu.Lock()
	defer m.downloadBackoffMu.Unlock()
	ready := make([]string, 0, len(platforms))
	var next time.Duration
	for _, platform := range platforms {
		backoff, cooling := m.downloadBackoff[platform]
		if !cooling || backoff.Until.IsZero() || !now.Before(backoff.Until) {
			delete(m.downloadBackoff, platform)
			ready = append(ready, platform)
			continue
		}
		remaining := backoff.Until.Sub(now)
		if next == 0 || remaining < next {
			next = remaining
		}
	}
	return ready, next
}

func (m *Manager) enabledDownloadPlatforms() []string {
	platforms := discoveryPlatforms()
	if m == nil || m.cfg == nil {
		return platforms
	}
	enabled := platforms[:0]
	for _, platform := range platforms {
		if m.cfg.PlatformEnabled(platform) {
			enabled = append(enabled, platform)
		}
	}
	return enabled
}

// ClearDownloadPlatformBackoff makes credential repairs effective immediately.
func (m *Manager) ClearDownloadPlatformBackoff(platform string) {
	if m == nil {
		return
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return
	}
	m.downloadBackoffMu.Lock()
	delete(m.downloadBackoff, platform)
	m.downloadBackoffMu.Unlock()
}

func downloadPoolLeaseOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("downloadpool:%s:%d", host, os.Getpid())
}

func (m *Manager) startDownloadWorkLeaseRenewal(ctx context.Context, job db.DownloadWork) func() {
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
				if err := m.db.RenewDownloadWorkLease(job.VideoID, job.LeaseOwner, time.Now().UnixMilli(), downloadPoolLeaseDuration); err != nil {
					log.Printf("[downloadpool] RenewDownloadWorkLease %s: %v", job.VideoID, err)
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

// --- Pure helper functions (testable) ---

// buildSourceURL constructs the video URL from platform, source ID, and video ID.
func buildSourceURL(platform, sourceID, videoID string) string {
	switch platform {
	case "tiktok":
		if nativeID, ok := nativeStoryID(platform, videoID); ok {
			videoID = nativeID
		}
		return fmt.Sprintf("https://www.tiktok.com/@%s/video/%s", sourceID, videoID)
	case "instagram":
		if nativeID, ok := nativeStoryID(platform, videoID); ok {
			return fmt.Sprintf("https://www.instagram.com/stories/%s/%s/", sourceID, nativeID)
		}
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

func nativeStoryID(platform, videoID string) (string, bool) {
	prefix := platform + "_story_"
	nativeID := strings.TrimPrefix(strings.TrimSpace(videoID), prefix)
	return nativeID, nativeID != "" && nativeID != videoID
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
			case json.Number:
				ts, _ = n.Int64()
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

func publishedAtForJob(metadata map[string]any, job db.DownloadWork) int64 {
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
		case json.Number:
			if n, err := v.Float64(); err == nil {
				return int(n)
			}
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 0
}
