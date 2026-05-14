package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/xfeed"
)

const (
	xIngestWorkerName     = "x_ingest"
	xIngestActivitySource = "x_ingest"
)

type xFeedFetcher interface {
	FetchTimeline(ctx context.Context, handle string, limit int) ([]model.FeedItem, error)
	FetchSource(ctx context.Context, rawURL string, limit int) ([]model.FeedItem, error)
	FetchStatus(ctx context.Context, handle, tweetID string) (xfeed.ParseResult, error)
}

// runXIngestLoop runs periodic X ingest. It fires immediately on start, then
// checks every 10 minutes whether a new cycle is due.
func (m *Manager) runXIngestLoop(ctx context.Context) {
	log.Printf("[x_ingest] starting ingest loop")

	m.runXIngestIfEnabled(ctx)

	// Check every 10 minutes whether a new cycle is due.
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.ingestKick:
			if !m.IsIngestPaused() {
				m.runXIngestIfEnabled(ctx)
			}
		case <-ticker.C:
			if !m.IsIngestPaused() {
				m.runXIngestIfEnabled(ctx)
			}
		}
	}
}

func (m *Manager) runXIngestIfEnabled(ctx context.Context) {
	if m.cfg != nil && !m.cfg.PlatformEnabled("twitter") {
		log.Printf("[x_ingest] twitter platform disabled — ingest disabled")
		m.setStatus(xIngestWorkerName, workerStatus(xIngestWorkerName, false, "disabled: twitter platform not enabled", ""))
		return
	}
	m.runIngestCycle(ctx)
}

func (m *Manager) xFeedClient() xFeedFetcher {
	if m.xFeedFetcher != nil {
		return m.xFeedFetcher
	}
	cookiesDir := ""
	if m.cfg != nil {
		cookiesDir = m.cfg.CookiesDir
	}
	client := xfeed.NewClient(cookiesDir)
	client.OperationSink = m.db
	client.StatusEnrichmentSink = m.RequestXStatusEnrichment
	m.xFeedFetcher = client
	return m.xFeedFetcher
}

// xFeedFetchDelay reads x_feed_fetch_delay from the DB, falling back to
// settings.Defaults when absent.
func (m *Manager) xFeedFetchDelay() time.Duration {
	secs := m.db.IntSetting("x_feed_fetch_delay")
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}

// runIngestCycle fetches subscribed Twitter channels sequentially with a fixed delay
// between fetches. The delay is configurable via x_feed_fetch_delay (seconds).
// On restart, recently-fetched accounts are skipped and the remaining handles are
// sorted by staleness (oldest first), so the cycle resumes from where it left off.
func (m *Manager) runIngestCycle(ctx context.Context) {
	fetchDelay := m.xFeedFetchDelay()

	start := time.Now()
	m.SetIngestRunning(true)
	defer m.SetIngestRunning(false)
	m.EmitFeed(xIngestActivitySource, "Starting ingest cycle", "info")

	channels, err := m.db.GetSubscribedChannels()
	if err != nil {
		log.Printf("[x_ingest] GetSubscribedChannels: %v", err)
		m.setStatus(xIngestWorkerName, workerStatus(xIngestWorkerName, true, "", err.Error()))
		return
	}

	// Filter to Twitter channels only.
	var twitterChannels []model.Channel
	for _, ch := range channels {
		if ch.Platform == "twitter" && ch.ChannelID != "" {
			twitterChannels = append(twitterChannels, ch)
		}
	}
	feedSources, err := m.db.ListFeedSources("twitter")
	if err != nil {
		log.Printf("[x_ingest] ListFeedSources: %v", err)
		m.setStatus(xIngestWorkerName, workerStatus(xIngestWorkerName, true, "", err.Error()))
		return
	}
	if len(twitterChannels) == 0 && len(feedSources) == 0 {
		log.Printf("[x_ingest] no subscribed Twitter channels or X feed sources")
		return
	}

	// Collect all handles.
	allHandles := make([]string, 0, len(twitterChannels))
	channelByID := make(map[string]model.Channel, len(twitterChannels))
	for _, ch := range twitterChannels {
		channelByID[ch.ChannelID] = ch
		allHandles = append(allHandles, ch.ChannelID)
	}

	var ready []string
	notDue := 0
	cooling := 0
	cycleInterval := time.Duration(0)
	if len(allHandles) > 0 {
		// The effective cycle duration is fetchDelay * totalChannels.
		// Accounts fetched within that window are "not_due".
		cycleInterval = fetchDelay * time.Duration(len(allHandles))
		ready, notDue, cooling = m.db.FilterReadyHandles(allHandles, cycleInterval.Seconds())
	}

	// Build fetch list.
	fetchList := make([]model.Channel, 0, len(ready))
	for _, h := range ready {
		fetchList = append(fetchList, channelByID[h])
	}

	log.Printf("[x_ingest] ingest cycle start (delay %s, cycle ~%s): %d ready, %d not_due, %d cooling, %d total",
		fetchDelay, cycleInterval.Round(time.Minute), len(fetchList), notDue, cooling, len(twitterChannels))

	if len(fetchList) == 0 && len(feedSources) == 0 {
		return
	}

	// Set live progress counters for the dashboard.
	atomic.StoreInt32(&m.ingestCycleTotal, int32(len(twitterChannels)))
	atomic.StoreInt32(&m.ingestCycleDone, int32(notDue))

	// Sequential paced fetch.
	const batchSize = 500
	var pendingItems []model.FeedItem
	var pendingJobs []db.FeedMediaJobRow
	var totalUpserted int
	var totalJobs int
	cycleFailures := make(map[string]string)

	for i, ch := range fetchList {
		// Pacing delay between fetches (not before the first one).
		// Re-read from DB each time so setting changes apply immediately.
		if i > 0 {
			delay := m.xFeedFetchDelay()
			if delay > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(delay):
				}
			}
		}

		if ctx.Err() != nil || m.IsIngestPaused() {
			if m.IsIngestPaused() {
				log.Printf("[x_ingest] ingest cycle aborted (paused)")
				m.EmitFeed(xIngestActivitySource, "Ingest cycle aborted (paused)", "warning")
			}
			break
		}

		handle := strings.TrimPrefix(ch.ChannelID, "twitter_")
		settings, settErr := m.db.GetChannelSettings(ch.ChannelID)
		if settErr != nil {
			log.Printf("[x_ingest] GetChannelSettings %s: %v", ch.ChannelID, settErr)
		}
		m.EmitFeed(xIngestActivitySource, fmt.Sprintf("Fetching @%s", handle), "info")
		fetchStart := time.Now()
		items, fetchErr := m.fetchXTimeline(ctx, ch.ChannelID, xTimelineLimit(settings))
		latencyMs := float64(time.Since(fetchStart).Milliseconds())

		atomic.AddInt32(&m.ingestCycleDone, 1)

		if fetchErr != nil {
			if errors.Is(fetchErr, context.Canceled) {
				break
			}
			log.Printf("[x_ingest] fetch %s: %v", ch.ChannelID, fetchErr)
			isTimeout := errors.Is(fetchErr, context.DeadlineExceeded)
			if isTimeout {
				m.EmitFeed(xIngestActivitySource, fmt.Sprintf("@%s — timeout", handle), "warning")
			} else {
				m.EmitFeed(xIngestActivitySource, fmt.Sprintf("@%s — fetch failed: %s", handle, fetchErr), "error")
			}
			_ = m.db.RecordIngestFailure(ch.ChannelID, fetchErr.Error(), 500)
			cycleFailures[ch.ChannelID] = fetchErr.Error()
			continue
		}

		_ = m.db.RecordIngestSuccess(ch.ChannelID, float64(time.Now().Unix()), latencyMs)

		items = filterTimelineItemsForSource(handle, items)
		filtered := applyChannelFiltersFromSettings(items, settings)
		pendingJobs = append(pendingJobs, feedMediaJobRowsForItems(filtered, settings)...)

		pendingItems = append(pendingItems, filtered...)

		// Periodic batch upsert to avoid holding too much in memory.
		if len(pendingItems) >= batchSize {
			n, upsertErr := m.upsertFeedItemsBatch(ctx, pendingItems, "batch")
			if upsertErr != nil {
				log.Printf("[x_ingest] UpsertFeedItems (batch): %v", upsertErr)
			} else {
				totalUpserted += n
			}
			pendingItems = pendingItems[:0]

			if len(pendingJobs) > 0 && upsertErr == nil {
				if jErr := m.db.EnqueueFeedMediaJobs(pendingJobs); jErr != nil {
					log.Printf("[x_ingest] EnqueueFeedMediaJobs (batch): %v", jErr)
				} else {
					totalJobs += len(pendingJobs)
					m.KickFeedMedia()
				}
			} else if len(pendingJobs) > 0 {
				log.Printf("[x_ingest] skipping %d media jobs after failed batch upsert", len(pendingJobs))
			}
			if len(pendingJobs) > 0 {
				pendingJobs = pendingJobs[:0]
			}
		}
	}

	for _, source := range feedSources {
		if !source.Enabled {
			continue
		}
		if ctx.Err() != nil || m.IsIngestPaused() {
			break
		}
		m.EmitFeed(xIngestActivitySource, fmt.Sprintf("Fetching %s", source.Label), "info")
		if _, err := m.FetchOneFeedSource(ctx, source.SourceID); err != nil {
			log.Printf("[x_ingest] feed source %s: %v", source.SourceID, err)
			cycleFailures[source.SourceID] = err.Error()
		}
	}

	// Final batch upsert for remaining items.
	finalUpsertOK := true
	if len(pendingItems) > 0 {
		n, upsertErr := m.upsertFeedItemsBatch(ctx, pendingItems, "final")
		if upsertErr != nil {
			log.Printf("[x_ingest] UpsertFeedItems (final): %v", upsertErr)
			finalUpsertOK = false
		} else {
			totalUpserted += n
		}
	}

	// Final batch of media jobs.
	if len(pendingJobs) > 0 && finalUpsertOK {
		if jErr := m.db.EnqueueFeedMediaJobs(pendingJobs); jErr != nil {
			log.Printf("[x_ingest] EnqueueFeedMediaJobs (final): %v", jErr)
		} else {
			totalJobs += len(pendingJobs)
			m.KickFeedMedia()
		}
	} else if len(pendingJobs) > 0 {
		log.Printf("[x_ingest] skipping %d media jobs after failed final upsert", len(pendingJobs))
	}

	m.lastCycleMu.Lock()
	m.lastCycleAt = time.Now().Unix()
	m.lastCycleFailures = cycleFailures
	m.lastCycleCooling = cooling
	m.lastCycleNotDue = notDue
	m.lastCycleReady = len(fetchList)
	m.lastCycleMu.Unlock()

	if n, err := m.db.SeedChannelProfileRows(); err != nil {
		log.Printf("[x_ingest] SeedChannelProfileRows: %v", err)
	} else if n > 0 {
		log.Printf("[x_ingest] seeded/updated %d channel profile rows", n)
	}

	// Reply chain resolution runs per-batch inside the fetch loop above so
	// threads are joinable before items become visible to readers, instead
	// of deferring to end-of-cycle (which can be ~3 hours).

	// Periodic sweep that retries any reply rows still missing reply_to_status.
	m.resolveReplyChainsSweep(ctx)

	// Kick scoring so new items get algo_interest + show up in the snapshot
	// without waiting for the next 5-minute worker tick.
	if totalUpserted > 0 {
		m.KickFeedScoring()
	}

	elapsed := time.Since(start)
	detail := fmt.Sprintf("cycle done: %d items, %d jobs, %s", totalUpserted, totalJobs, elapsed.Round(time.Millisecond))
	log.Printf("[x_ingest] %s", detail)
	m.Emit(xIngestActivitySource, fmt.Sprintf("Ingested %d items from %d sources", totalUpserted, len(fetchList)), "done")
	m.setStatus(xIngestWorkerName, workerStatus(xIngestWorkerName, true, detail, ""))
}

func (m *Manager) upsertFeedItemsBatch(ctx context.Context, items []model.FeedItem, label string) (int, error) {
	n, err := m.db.UpsertFeedItems(items)
	if err != nil {
		return 0, err
	}
	m.primeFeedItemProfiles(ctx, items)
	// Resolve reply chains for this batch so threads are joinable promptly,
	// instead of waiting for the full ingest cycle to finish.
	m.resolveReplyChains(ctx, items)
	return n, nil
}

func (m *Manager) fetchXTimeline(ctx context.Context, handle string, limit int) ([]model.FeedItem, error) {
	handle = strings.TrimPrefix(handle, "twitter_")
	return m.xFeedClient().FetchTimeline(ctx, handle, limit)
}

func xTimelineLimit(settings *db.ChannelSettings) int {
	if settings != nil && settings.MaxVideos > 0 {
		return settings.MaxVideos
	}
	return 100
}

func filterTimelineItemsForSource(source string, items []model.FeedItem) []model.FeedItem {
	source = strings.ToLower(xfeed.NormalizeHandle(source))
	if source == "" {
		return items
	}
	result := items[:0:0]
	for _, item := range items {
		author := strings.ToLower(xfeed.NormalizeHandle(item.AuthorHandle))
		itemSource := strings.ToLower(xfeed.NormalizeHandle(item.SourceHandle))
		if itemSource == "" {
			itemSource = source
		}
		if author == source || itemSource != source || item.IsRetweet || item.IsReply {
			result = append(result, item)
		}
	}
	return result
}

// FetchOneChannel fetches a single Twitter channel out-of-band from the main
// ingest cycle. Used by the per-channel refresh button so a click produces
// visible results within seconds instead of waiting for the multi-hour cycle
// to drain.
//
// channelID is the full ID (e.g. "twitter_examplechannel"). Returns the number
// of items upserted plus any fetch/parse error.
func (m *Manager) FetchOneChannel(ctx context.Context, channelID string) (int, error) {
	if !strings.HasPrefix(channelID, "twitter_") {
		return 0, fmt.Errorf("FetchOneChannel: not a twitter channel: %s", channelID)
	}

	settings, _ := m.db.GetChannelSettings(channelID)
	items, err := m.fetchXTimeline(ctx, channelID, xTimelineLimit(settings))
	if err != nil {
		_ = m.db.RecordIngestFailure(channelID, err.Error(), 0)
		return 0, err
	}
	_ = m.db.RecordIngestSuccess(channelID, float64(time.Now().Unix()), 0)

	items = filterTimelineItemsForSource(strings.TrimPrefix(channelID, "twitter_"), items)
	filtered := applyChannelFiltersFromSettings(items, settings)
	if len(filtered) == 0 {
		return 0, nil
	}

	for i := range filtered {
		filtered[i].ParseMedia()
	}

	n, err := m.upsertFeedItemsBatch(ctx, filtered, "single")
	if err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}

	jobs := feedMediaJobRowsForItems(filtered, settings)
	if len(jobs) > 0 {
		if err := m.db.EnqueueFeedMediaJobs(jobs); err != nil {
			log.Printf("[x_ingest] EnqueueFeedMediaJobs (single): %v", err)
		} else {
			m.KickFeedMedia()
		}
	}

	m.KickFeedScoring()
	return n, nil
}

// FetchOneFeedSource fetches one X list/community source through gallery-dl and
// merges its items into the main feed while recording source attribution.
func (m *Manager) FetchOneFeedSource(ctx context.Context, sourceID string) (int, error) {
	source, err := m.db.GetFeedSource(sourceID)
	if err != nil {
		return 0, err
	}
	if source.Platform != "twitter" {
		return 0, fmt.Errorf("unsupported feed source platform: %s", source.Platform)
	}
	items, err := m.xFeedClient().FetchSource(ctx, source.URL, 100)
	if err != nil {
		_ = m.db.RecordFeedSourceFailure(sourceID, err.Error())
		return 0, err
	}
	n, err := m.upsertFeedSourceItems(ctx, source, items)
	if err != nil {
		_ = m.db.RecordFeedSourceFailure(sourceID, err.Error())
		return 0, err
	}
	_ = m.db.RecordFeedSourceSuccess(sourceID)
	return n, nil
}

func (m *Manager) upsertFeedSourceItems(ctx context.Context, source model.FeedSource, items []model.FeedItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	for i := range items {
		items[i].ParseMedia()
	}
	n, err := m.upsertFeedItemsBatch(ctx, items, "source")
	if err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}
	for _, item := range items {
		if err := m.db.RecordFeedItemSources(item.TweetID, []string{source.SourceID}); err != nil {
			return n, fmt.Errorf("record source attribution: %w", err)
		}
	}
	jobs := feedMediaJobRowsForItems(items, nil)
	if len(jobs) > 0 {
		if err := m.db.EnqueueFeedMediaJobs(jobs); err != nil {
			log.Printf("[x_ingest] EnqueueFeedMediaJobs (feed source): %v", err)
		} else {
			m.KickFeedMedia()
		}
	}
	m.KickFeedScoring()
	return n, nil
}

// applyChannelFiltersFromSettings filters items using ChannelSettings
// booleans. settings may be nil (no filtering applied).
//
// Retweet/quote filtering moved to read time so dedup-awareness can see
// every retweeter row and the toggle takes effect on already-ingested
// data without a re-fetch.
func applyChannelFiltersFromSettings(items []model.FeedItem, settings *db.ChannelSettings) []model.FeedItem {
	if settings == nil {
		return items
	}
	result := items[:0:0]
	for _, item := range items {
		if settings.MediaOnly && item.MediaJSON == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func feedMediaJobRowsForItems(items []model.FeedItem, settings *db.ChannelSettings) []db.FeedMediaJobRow {
	limit := 0
	if settings != nil {
		limit = settings.MediaDownloadLimit
	}

	jobs := make([]db.FeedMediaJobRow, 0, len(items))
	for i := range items {
		items[i].ParseMedia()
		kind := classifyMediaKind(items[i].MediaJSON)
		slideCount := len(items[i].Media)
		if kind == "unknown" && items[i].QuoteMediaJSON != "" {
			kind = classifyMediaKind(items[i].QuoteMediaJSON)
			slideCount = len(items[i].QuoteMedia)
		}
		if kind == "unknown" {
			continue
		}
		if limit > 0 && len(jobs) >= limit {
			break
		}
		jobs = append(jobs, db.FeedMediaJobRow{
			TweetID:      items[i].TweetID,
			TweetURL:     items[i].CanonicalURL,
			SourceHandle: items[i].SourceHandle,
			MediaKind:    kind,
			SlideCount:   slideCount,
		})
	}
	return jobs
}

// classifyMediaKind returns "video", "image", or "unknown" based on mediaJSON content.
func classifyMediaKind(mediaJSON string) string {
	if mediaJSON == "" {
		return "unknown"
	}
	if strings.Contains(mediaJSON, `"video"`) || strings.Contains(mediaJSON, `"gif"`) {
		return "video"
	}
	return "image"
}

// workerStatus is a convenience constructor for WorkerStatus.
func workerStatus(name string, running bool, detail, errMsg string) WorkerStatus {
	return WorkerStatus{
		Name:      name,
		Running:   running,
		LastRunAt: time.Now(),
		Detail:    detail,
		Error:     errMsg,
	}
}

// primeFeedItemProfiles creates profile rows for a just-upserted feed batch and
// kicks avatar/profile recovery while those rows are still hot in the ingest path.
func (m *Manager) primeFeedItemProfiles(ctx context.Context, items []model.FeedItem) {
	if len(items) == 0 {
		return
	}
	if n, err := m.db.SeedChannelProfileRowsForFeedItems(items); err != nil {
		log.Printf("[x_ingest] SeedChannelProfileRowsForFeedItems: %v", err)
	} else if n > 0 {
		log.Printf("[x_ingest] primed %d feed profile rows", n)
	}
	if ctx.Err() != nil {
		return
	}
	avatarCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	m.downloadNewAuthorAvatars(avatarCtx, items)
}

// downloadNewAuthorAvatars downloads avatars for non-followed authors that have
// a twimg URL stored in the feed item but no cached file yet.
func (m *Manager) downloadNewAuthorAvatars(ctx context.Context, items []model.FeedItem) {
	seen := make(map[string]bool)
	avatarDir := filepath.Join(m.cfg.DataDir, "thumbnails", "avatars")
	_ = os.MkdirAll(avatarDir, 0o755)

	queueAvatarRecovery := func(handle, avatarURL string) {
		channelID := model.TwitterAvatarChannelID(handle, avatarURL)
		if channelID == "" || seen[channelID] {
			return
		}
		seen[channelID] = true
		if hasConventionalMediaFile(avatarDir, channelID) {
			return
		}
		if strings.Contains(avatarURL, "twimg.com") {
			if model.IsSyntheticTwitterAvatarChannelID(channelID) {
				_ = m.db.UpsertChannelProfile(model.ChannelProfile{
					ChannelID: channelID,
					Platform:  "twitter",
					AvatarURL: avatarURL,
				})
			}
			m.maybeDownloadAvatar(ctx, channelID, avatarURL, avatarDir)
			return
		}
		m.RequestAvatar(channelID)
	}

	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		queueAvatarRecovery(item.AuthorHandle, item.AuthorAvatarURL)
		queueAvatarRecovery(item.QuoteAuthorHandle, item.QuoteAuthorAvatarURL)
	}
}

// maybeDownloadAvatar downloads a twimg avatar for channelID if not already cached.
func (m *Manager) maybeDownloadAvatar(ctx context.Context, channelID, twimgURL, avatarDir string) {
	if hasConventionalMediaFile(avatarDir, channelID) {
		return
	}
	tmpPath, err := m.downloader.HTTP.DownloadFile(ctx, upgradeTwimgURL(twimgURL), avatarDir, channelID+".download")
	if err != nil {
		log.Printf("[x_ingest] avatar download failed for %s: %v", channelID, err)
		return
	}
	if _, err := normalizeDownloadedImage(tmpPath, avatarDir, channelID); err != nil {
		log.Printf("[x_ingest] avatar normalize failed for %s: %v", channelID, err)
	}
}

// resolveReplyChains delegates to the ReplyResolver. Lazy-init on first use
// so unit tests that construct a Manager without an fxtwitter client still work.
func (m *Manager) resolveReplyChains(ctx context.Context, items []model.FeedItem) {
	if m.replyResolver == nil {
		m.replyResolver = NewReplyResolver(m.db, fxtwitter.NewClient())
	}
	if err := m.replyResolver.ResolveCycle(ctx, items); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[x_ingest] reply resolver: %v", err)
	}
}

// replySweepMinInterval rate-limits the DB-wide reply sweep so we don't hammer
// fxtwitter on every 5-min ingest cycle.
const replySweepMinInterval = time.Hour

// replySweepBatchLimit caps how many unresolved replies we touch per sweep.
const replySweepBatchLimit = 200

// resolveReplyChainsSweep retries fxtwitter for any reply rows still missing
// reply_to_status. Rate-limited to once per replySweepMinInterval.
func (m *Manager) resolveReplyChainsSweep(ctx context.Context) {
	last := time.Unix(m.lastReplySweepAt.Load(), 0)
	if time.Since(last) < replySweepMinInterval {
		return
	}
	m.lastReplySweepAt.Store(time.Now().Unix())

	candidates, err := m.db.FindUnresolvedReplies(replySweepBatchLimit)
	if err != nil {
		log.Printf("[x_ingest] reply sweep: FindUnresolvedReplies: %v", err)
		return
	}
	if len(candidates) == 0 {
		return
	}
	log.Printf("[x_ingest] reply sweep: retrying %d unresolved replies", len(candidates))
	m.resolveReplyChains(ctx, candidates)
}

// upgradeTwimgURL replaces Twitter's _normal suffix (48x48) with _400x400 so
// downloaded avatars are large enough to avoid the placeholder size threshold.
func upgradeTwimgURL(u string) string {
	return strings.Replace(u, "_normal.", "_400x400.", 1)
}
