package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/xfeed"
)

const (
	xIngestWorkerName     = "x_ingest"
	xIngestActivitySource = "x_ingest"
	xIngestCycleInterval  = 10 * time.Minute
)

type xFeedFetcher interface {
	FetchTimeline(ctx context.Context, handle string, limit int) ([]model.FeedItem, error)
	FetchSource(ctx context.Context, rawURL string, limit int) ([]model.FeedItem, error)
	FetchStatus(ctx context.Context, handle, tweetID string) (xfeed.ParseResult, error)
}

// runXIngestLoop runs X ingest immediately, then waits between completed cycles.
func (m *Manager) runXIngestLoop(ctx context.Context) {
	log.Printf("[x_ingest] starting ingest loop")

	m.runXIngestIfEnabled(ctx)
	runXIngestSchedule(ctx, m.ingestKick, xIngestCycleInterval, func() {
		if !m.IsIngestPaused() {
			m.runXIngestIfEnabled(ctx)
		}
	})
}

func runXIngestSchedule(ctx context.Context, kick <-chan struct{}, interval time.Duration, run func()) {
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-kick:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
		run()
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

func filterReadyXFeedSources(sources []model.FeedSource, interval time.Duration, now time.Time) ([]model.FeedSource, int) {
	ready := make([]model.FeedSource, 0, len(sources))
	notDue := 0
	for _, source := range sources {
		if !source.Enabled {
			continue
		}
		if interval > 0 && source.LastChecked != nil && now.Sub(*source.LastChecked) < interval {
			notDue++
			continue
		}
		ready = append(ready, source)
	}
	sort.SliceStable(ready, func(i, j int) bool {
		left, right := ready[i].LastChecked, ready[j].LastChecked
		if left == nil {
			return right != nil
		}
		if right == nil {
			return false
		}
		if left.Equal(*right) {
			return ready[i].SourceID < ready[j].SourceID
		}
		return left.Before(*right)
	})
	return ready, notDue
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
	enabledFeedSources := make([]model.FeedSource, 0, len(feedSources))
	for _, source := range feedSources {
		if source.Enabled {
			enabledFeedSources = append(enabledFeedSources, source)
		}
	}
	if len(twitterChannels) == 0 && len(enabledFeedSources) == 0 {
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
	cycleInterval := fetchDelay * time.Duration(len(allHandles)+len(enabledFeedSources))
	if len(allHandles) > 0 {
		ready, notDue, cooling = m.db.FilterReadyHandles(allHandles, cycleInterval.Seconds())
	}
	readyFeedSources, feedSourcesNotDue := filterReadyXFeedSources(enabledFeedSources, cycleInterval, time.Now())
	notDue += feedSourcesNotDue

	// Build fetch list.
	fetchList := make([]model.Channel, 0, len(ready))
	for _, h := range ready {
		fetchList = append(fetchList, channelByID[h])
	}

	log.Printf("[x_ingest] ingest cycle start (delay %s, cycle ~%s): %d ready, %d not_due, %d cooling, %d total",
		fetchDelay, cycleInterval.Round(time.Minute), len(fetchList)+len(readyFeedSources), notDue, cooling,
		len(twitterChannels)+len(enabledFeedSources))

	if len(fetchList) == 0 && len(readyFeedSources) == 0 {
		return
	}

	// Set live progress counters for the dashboard.
	atomic.StoreInt32(&m.ingestCycleTotal, int32(len(twitterChannels)+len(enabledFeedSources)))
	atomic.StoreInt32(&m.ingestCycleDone, int32(notDue))

	// Sequential paced fetch.
	var totalUpserted int
	cycleFailures := make(map[string]string)
	fetchesStarted := 0
	waitForFetch := func() bool {
		if fetchesStarted > 0 {
			delay := m.xFeedFetchDelay()
			select {
			case <-ctx.Done():
			case <-time.After(delay):
			}
		}
		if ctx.Err() != nil || m.IsIngestPaused() {
			return false
		}
		fetchesStarted++
		return true
	}

	for _, ch := range fetchList {
		if !waitForFetch() {
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

		m.enrichExistingGhostQuotes(ctx, items)
		items = filterTimelineItemsForSource(handle, items)
		filtered := applyChannelFiltersFromSettings(items, settings)

		if len(filtered) > 0 {
			result, upsertErr := m.upsertFeedItemsBatch(filtered)
			if upsertErr != nil {
				log.Printf("[x_ingest] UpsertFeedItems: %v", upsertErr)
			} else {
				totalUpserted += result.Processed
				if err := m.reconcileXMediaRetentionChanges(result.XMediaRetentionChanges); err != nil {
					log.Printf("[x_ingest] %v", err)
				} else {
					m.KickMediaWork()
				}
				m.KickFeedScoring()
			}
		}
	}

	for _, source := range readyFeedSources {
		if !waitForFetch() {
			break
		}
		m.EmitFeed(xIngestActivitySource, fmt.Sprintf("Fetching %s", source.Label), "info")
		_, err := m.FetchOneFeedSource(ctx, source.SourceID)
		atomic.AddInt32(&m.ingestCycleDone, 1)
		if err != nil {
			log.Printf("[x_ingest] feed source %s: %v", source.SourceID, err)
			cycleFailures[source.SourceID] = err.Error()
		}
	}

	m.lastCycleMu.Lock()
	m.lastCycleAt = time.Now().Unix()
	m.lastCycleFailures = cycleFailures
	m.lastCycleCooling = cooling
	m.lastCycleNotDue = notDue
	m.lastCycleReady = len(fetchList) + len(readyFeedSources)
	m.lastCycleMu.Unlock()

	elapsed := time.Since(start)
	detail := fmt.Sprintf("cycle done: %d items, %s", totalUpserted, elapsed.Round(time.Millisecond))
	log.Printf("[x_ingest] %s", detail)
	m.Emit(xIngestActivitySource, fmt.Sprintf("Ingested %d items from %d sources", totalUpserted, len(fetchList)+len(readyFeedSources)), "done")
	m.setStatus(xIngestWorkerName, workerStatus(xIngestWorkerName, true, detail, ""))
}

func (m *Manager) upsertFeedItemsBatch(items []model.FeedItem) (db.FeedUpsertResult, error) {
	result, err := m.db.UpsertFeedItemsDetailed(items)
	if err != nil {
		return db.FeedUpsertResult{}, err
	}
	if result.Processed > 0 {
		m.KickProfileJobs()
	}
	return result, nil
}

func (m *Manager) fetchXTimeline(ctx context.Context, handle string, limit int) ([]model.FeedItem, error) {
	handle = strings.TrimPrefix(handle, "twitter_")
	return m.xFeedClient().FetchTimeline(ctx, handle, limit)
}

func xTimelineLimit(settings *db.ChannelSettings) int {
	if settings != nil && settings.MediaDownloadLimit > 0 {
		return settings.MediaDownloadLimit
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

func (m *Manager) enrichExistingGhostQuotes(ctx context.Context, items []model.FeedItem) {
	if m == nil || m.db == nil || ctx.Err() != nil {
		return
	}
	updates := make([]model.FeedItem, 0)
	for _, item := range items {
		if item.QuoteTweetID == "" {
			continue
		}
		existing, err := m.db.GetFeedItemByTweetID(item.TweetID)
		if err != nil {
			log.Printf("[x_ingest] load quote ghost %s: %v", item.TweetID, err)
			continue
		}
		if existing == nil || !existing.IsGhost {
			continue
		}
		updated := *existing
		if !copyQuoteFieldsForStatusEnrichment(&updated, item) {
			continue
		}
		updates = append(updates, updated)
	}
	if len(updates) > 0 {
		m.upsertXStatusEnrichmentItems(ctx, updates)
	}
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

	m.enrichExistingGhostQuotes(ctx, items)
	items = filterTimelineItemsForSource(strings.TrimPrefix(channelID, "twitter_"), items)
	filtered := applyChannelFiltersFromSettings(items, settings)
	if len(filtered) == 0 {
		return 0, nil
	}

	for i := range filtered {
		filtered[i].ParseMedia()
	}

	result, err := m.upsertFeedItemsBatch(filtered)
	if err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}
	if err := m.reconcileXMediaRetentionChanges(result.XMediaRetentionChanges); err != nil {
		return result.Processed, err
	}
	m.KickMediaWork()
	m.KickFeedScoring()
	return result.Processed, nil
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
	limit := m.xMediaDownloadLimit()
	items, err := m.xFeedClient().FetchSource(ctx, source.URL, limit)
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
	result, err := m.upsertFeedItemsBatch(items)
	if err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}
	for _, item := range items {
		if err := m.db.RecordFeedItemSources(item.TweetID, []string{source.SourceID}); err != nil {
			return result.Processed, fmt.Errorf("record source attribution: %w", err)
		}
	}
	if err := m.reconcileXMediaRetentionChanges(result.XMediaRetentionChanges); err != nil {
		return result.Processed, err
	}
	if err := m.enforceXFeedSourceLimit(source.SourceID, m.xMediaDownloadLimit()); err != nil {
		return result.Processed, err
	}
	m.KickMediaWork()
	m.KickFeedScoring()
	return result.Processed, nil
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

func workerStatusWithSummary(name string, running bool, summary, detail, errMsg string) WorkerStatus {
	status := workerStatus(name, running, detail, errMsg)
	status.Summary = summary
	return status
}
