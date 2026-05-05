package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/rsshub"
)

// rsshubCacheTTL matches Python's RSSHUB_CACHE_TTL_SEC = 300.
const rsshubCacheTTL = 300 * time.Second

type rsshubCacheEntry struct {
	body      []byte
	fetchedAt time.Time
}

var (
	rsshubCacheMu sync.Mutex
	rsshubCache   = make(map[string]rsshubCacheEntry)
)

// runRSSHubIngestLoop runs a periodic RSSHub ingest. It fires immediately on
// start, then checks every 10 minutes whether a new cycle is due.
func (m *Manager) runRSSHubIngestLoop(ctx context.Context) {
	log.Printf("[rsshub] starting ingest loop")

	// Run immediately on startup.
	m.runRSSHubIngestIfEnabled(ctx)

	// Check every 10 minutes whether a new cycle is due.
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.ingestKick:
			if !m.IsIngestPaused() {
				m.runRSSHubIngestIfEnabled(ctx)
			}
		case <-ticker.C:
			if !m.IsIngestPaused() {
				m.runRSSHubIngestIfEnabled(ctx)
			}
		}
	}
}

func (m *Manager) runRSSHubIngestIfEnabled(ctx context.Context) {
	if m.cfg != nil && !m.cfg.PlatformEnabled("twitter") {
		log.Printf("[rsshub] twitter platform disabled — ingest disabled")
		m.setStatus("rsshub_ingest", workerStatus("rsshub_ingest", false, "disabled: twitter platform not enabled", ""))
		return
	}
	m.runIngestCycle(ctx)
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
	m.EmitFeed("rsshub", "Starting ingest cycle", "info")

	channels, err := m.db.GetSubscribedChannels()
	if err != nil {
		log.Printf("[rsshub] GetSubscribedChannels: %v", err)
		m.setStatus("rsshub_ingest", workerStatus("rsshub_ingest", true, "", err.Error()))
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
		log.Printf("[rsshub] ListFeedSources: %v", err)
		m.setStatus("rsshub_ingest", workerStatus("rsshub_ingest", true, "", err.Error()))
		return
	}
	if len(twitterChannels) == 0 && len(feedSources) == 0 {
		log.Printf("[rsshub] no subscribed Twitter channels or X feed sources")
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
	if len(allHandles) > 0 && m.cfg.RSSHubBase != "" {
		// The effective cycle duration is fetchDelay * totalChannels.
		// Accounts fetched within that window are "not_due".
		cycleInterval = fetchDelay * time.Duration(len(allHandles))
		ready, notDue, cooling = m.db.FilterReadyHandles(allHandles, cycleInterval.Seconds())
	} else if len(allHandles) > 0 {
		log.Printf("[rsshub] RSSHUB_BASE not set — account ingest disabled, X list/community sources still enabled")
	}

	// Build fetch list.
	fetchList := make([]model.Channel, 0, len(ready))
	for _, h := range ready {
		fetchList = append(fetchList, channelByID[h])
	}

	log.Printf("[rsshub] ingest cycle start (delay %s, cycle ~%s): %d ready, %d not_due, %d cooling, %d total",
		fetchDelay, cycleInterval.Round(time.Minute), len(fetchList), notDue, cooling, len(twitterChannels))

	if len(fetchList) == 0 && len(feedSources) == 0 {
		return
	}

	// Set live progress counters for the dashboard.
	atomic.StoreInt32(&m.ingestCycleTotal, int32(len(twitterChannels)))
	atomic.StoreInt32(&m.ingestCycleDone, int32(notDue))

	// Sequential paced fetch.
	const batchSize = 500
	var allItems []model.FeedItem
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
				log.Printf("[rsshub] ingest cycle aborted (paused)")
				m.EmitFeed("rsshub", "Ingest cycle aborted (paused)", "warning")
			}
			break
		}

		handle := strings.TrimPrefix(ch.ChannelID, "twitter_")
		m.EmitFeed("rsshub", fmt.Sprintf("Fetching @%s", handle), "info")
		fetchStart := time.Now()
		items, isStale, fetchErr := m.fetchAndParse(ctx, ch.ChannelID)
		latencyMs := float64(time.Since(fetchStart).Milliseconds())

		atomic.AddInt32(&m.ingestCycleDone, 1)

		if fetchErr != nil {
			if errors.Is(fetchErr, context.Canceled) {
				break
			}
			log.Printf("[rsshub] fetch %s: %v", ch.ChannelID, fetchErr)
			isTimeout := errors.Is(fetchErr, context.DeadlineExceeded)
			if isTimeout {
				m.EmitFeed("rsshub", fmt.Sprintf("@%s — timeout", handle), "warning")
			} else {
				m.EmitFeed("rsshub", fmt.Sprintf("@%s — fetch failed: %s", handle, fetchErr), "error")
			}
			httpStatus := 500
			if e, ok := fetchErr.(*httpError); ok {
				httpStatus = e.Status
			}
			_ = m.db.RecordIngestFailure(ch.ChannelID, fetchErr.Error(), httpStatus)
			cycleFailures[ch.ChannelID] = fetchErr.Error()
			continue
		}

		if isStale {
			log.Printf("[rsshub] stale cache for %s", ch.ChannelID)
		} else {
			_ = m.db.RecordIngestSuccess(ch.ChannelID, float64(time.Now().Unix()), latencyMs)
		}

		// Get channel settings from DB for filtering.
		settings, settErr := m.db.GetChannelSettings(ch.ChannelID)
		if settErr != nil {
			log.Printf("[rsshub] GetChannelSettings %s: %v", ch.ChannelID, settErr)
		}

		filtered := applyChannelFiltersFromSettings(items, settings)

		// Build media jobs only for fresh feeds (not stale cache hits).
		if !isStale {
			pendingJobs = append(pendingJobs, feedMediaJobRowsForItems(filtered, settings)...)
		}

		pendingItems = append(pendingItems, filtered...)
		allItems = append(allItems, filtered...)

		// Periodic batch upsert to avoid holding too much in memory.
		if len(pendingItems) >= batchSize {
			n, upsertErr := m.db.UpsertFeedItems(pendingItems)
			if upsertErr != nil {
				log.Printf("[rsshub] UpsertFeedItems (batch): %v", upsertErr)
			} else {
				totalUpserted += n
				m.primeFeedItemProfiles(ctx, pendingItems)
			}
			// Resolve reply chains for THIS batch so threads are joinable
			// before the next batch becomes visible to readers.
			m.resolveReplyChains(ctx, pendingItems)
			pendingItems = pendingItems[:0]

			if len(pendingJobs) > 0 {
				if jErr := m.db.EnqueueFeedMediaJobs(pendingJobs); jErr != nil {
					log.Printf("[rsshub] EnqueueFeedMediaJobs (batch): %v", jErr)
				} else {
					totalJobs += len(pendingJobs)
					m.KickFeedMedia()
				}
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
		m.EmitFeed("rsshub", fmt.Sprintf("Fetching %s", source.Label), "info")
		if _, err := m.FetchOneFeedSource(ctx, source.SourceID); err != nil {
			log.Printf("[rsshub] feed source %s: %v", source.SourceID, err)
			cycleFailures[source.SourceID] = err.Error()
		}
	}

	// Final batch upsert for remaining items.
	if len(pendingItems) > 0 {
		n, upsertErr := m.db.UpsertFeedItems(pendingItems)
		if upsertErr != nil {
			log.Printf("[rsshub] UpsertFeedItems (final): %v", upsertErr)
		} else {
			totalUpserted += n
			m.primeFeedItemProfiles(ctx, pendingItems)
		}
		m.resolveReplyChains(ctx, pendingItems)
	}

	// Final batch of media jobs.
	if len(pendingJobs) > 0 {
		if jErr := m.db.EnqueueFeedMediaJobs(pendingJobs); jErr != nil {
			log.Printf("[rsshub] EnqueueFeedMediaJobs (final): %v", jErr)
		} else {
			totalJobs += len(pendingJobs)
			m.KickFeedMedia()
		}
	}

	m.lastCycleMu.Lock()
	m.lastCycleAt = time.Now().Unix()
	m.lastCycleFailures = cycleFailures
	m.lastCycleCooling = cooling
	m.lastCycleNotDue = notDue
	m.lastCycleReady = len(fetchList)
	m.lastCycleMu.Unlock()

	if n, err := m.db.SeedChannelProfileRows(); err != nil {
		log.Printf("[rsshub] SeedChannelProfileRows: %v", err)
	} else if n > 0 {
		log.Printf("[rsshub] seeded/updated %d channel profile rows", n)
	}

	// Backfill quote data for truncated retweets ingested this cycle.
	m.backfillTruncatedQuotes(ctx, allItems)

	// Backfill truncated quote body text via fxtwitter API.
	m.backfillTruncatedQuoteText(ctx, allItems)

	// Reply chain resolution runs per-batch inside the fetch loop above so
	// threads are joinable before items become visible to readers, instead
	// of deferring to end-of-cycle (which can be ~3 hours).

	// Periodic sweep that retries any DB rows the per-cycle backfill missed
	// (transient fxtwitter failures, stale "is_note_tweet" data, etc.).
	m.backfillTruncatedQuoteTextSweep(ctx)

	// Periodic sweep that retries any reply rows still missing reply_to_status.
	m.resolveReplyChainsSweep(ctx)

	// Kick scoring so new items get algo_interest + show up in the snapshot
	// without waiting for the next 5-minute worker tick.
	if totalUpserted > 0 {
		m.KickFeedScoring()
	}

	elapsed := time.Since(start)
	detail := fmt.Sprintf("cycle done: %d items, %d jobs, %s", totalUpserted, totalJobs, elapsed.Round(time.Millisecond))
	log.Printf("[rsshub] %s", detail)
	m.Emit("rsshub", fmt.Sprintf("Ingested %d items from %d sources", totalUpserted, len(fetchList)), "done")
	m.setStatus("rsshub_ingest", workerStatus("rsshub_ingest", true, detail, ""))
}

// httpError carries an HTTP status code alongside a fetch error.
type httpError struct {
	Status  int
	Message string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

// fetchXMLWithCache fetches RSS XML for a Twitter handle with 300s in-memory stale cache.
// Mirrors Python's fetch_feed_xml: pops stale entry before fetching, falls back to stale on any error.
// Returns (body, isStale, err):
//   - isStale=false, err=nil: fresh response
//   - isStale=true,  err=nil: fetch failed, stale cache returned
//   - err!=nil: fetch failed and no stale cache available
func (m *Manager) fetchXMLWithCache(ctx context.Context, handle string) ([]byte, bool, error) {
	rsshubCacheMu.Lock()
	entry, ok := rsshubCache[handle]
	if ok && time.Since(entry.fetchedAt) < rsshubCacheTTL {
		rsshubCacheMu.Unlock()
		return entry.body, false, nil
	}
	// Pop stale entry before attempting fetch (matches Python behaviour).
	staleEntry := entry
	hasStale := ok
	delete(rsshubCache, handle)
	rsshubCacheMu.Unlock()

	feedURL := rsshub.BuildEnrichedURL(m.cfg.RSSHubBase, handle)
	reqCtx, cancel := context.WithTimeout(ctx, m.cfg.RSSHubTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, feedURL, nil)
	if err != nil {
		if hasStale {
			return staleEntry.body, true, nil
		}
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Igloo/1.0")
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if hasStale {
			return staleEntry.body, true, nil
		}
		return nil, false, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		httpErr := &httpError{Status: resp.StatusCode, Message: resp.Status}
		if hasStale {
			return staleEntry.body, true, nil
		}
		return nil, false, httpErr
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if hasStale {
			return staleEntry.body, true, nil
		}
		return nil, false, fmt.Errorf("read body: %w", err)
	}

	rsshubCacheMu.Lock()
	rsshubCache[handle] = rsshubCacheEntry{body: body, fetchedAt: time.Now()}
	rsshubCacheMu.Unlock()

	return body, false, nil
}

// fetchAndParse fetches and parses the RSSHub feed for a Twitter handle.
// handle may be bare ("examplehandle") or prefixed ("twitter_examplehandle").
// Returns (items, isStale, err); see fetchXMLWithCache for isStale semantics.
func (m *Manager) fetchAndParse(ctx context.Context, handle string) ([]model.FeedItem, bool, error) {
	handle = strings.TrimPrefix(handle, "twitter_")
	body, isStale, err := m.fetchXMLWithCache(ctx, handle)
	if err != nil {
		return nil, false, err
	}

	feed, err := rsshub.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, isStale, fmt.Errorf("parse: %w", err)
	}

	items := rsshub.ToFeedItems(feed, handle)
	return items, isStale, nil
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

	items, isStale, err := m.fetchAndParse(ctx, channelID)
	if err != nil {
		_ = m.db.RecordIngestFailure(channelID, err.Error(), 0)
		return 0, err
	}
	if !isStale {
		_ = m.db.RecordIngestSuccess(channelID, float64(time.Now().Unix()), 0)
	}

	settings, _ := m.db.GetChannelSettings(channelID)
	filtered := applyChannelFiltersFromSettings(items, settings)
	if len(filtered) == 0 {
		return 0, nil
	}

	for i := range filtered {
		filtered[i].ParseMedia()
	}

	n, err := m.db.UpsertFeedItems(filtered)
	if err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}
	m.primeFeedItemProfiles(ctx, filtered)

	// Resolve reply chains right away so the just-fetched replies are joinable
	// before the user reloads the feed.
	m.resolveReplyChains(ctx, filtered)

	if !isStale {
		jobs := feedMediaJobRowsForItems(filtered, settings)
		if len(jobs) > 0 {
			if err := m.db.EnqueueFeedMediaJobs(jobs); err != nil {
				log.Printf("[rsshub] EnqueueFeedMediaJobs (single): %v", err)
			} else {
				m.KickFeedMedia()
			}
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
	cookiesFile := m.twitterSourceCookiesFile()
	items, err := m.downloader.GalleryDL.TwitterSource(ctx, source.URL, 100, cookiesFile)
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
	n, err := m.db.UpsertFeedItems(items)
	if err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}
	m.primeFeedItemProfiles(ctx, items)
	for _, item := range items {
		if err := m.db.RecordFeedItemSources(item.TweetID, []string{source.SourceID}); err != nil {
			return n, fmt.Errorf("record source attribution: %w", err)
		}
	}
	m.resolveReplyChains(ctx, items)
	jobs := feedMediaJobRowsForItems(items, nil)
	if len(jobs) > 0 {
		if err := m.db.EnqueueFeedMediaJobs(jobs); err != nil {
			log.Printf("[rsshub] EnqueueFeedMediaJobs (feed source): %v", err)
		} else {
			m.KickFeedMedia()
		}
	}
	m.KickFeedScoring()
	return n, nil
}

func (m *Manager) twitterSourceCookiesFile() string {
	if m.cfg == nil || m.cfg.CookiesDir == "" {
		return ""
	}
	for _, name := range []string{"x.com_cookies.txt", "twitter_cookies.txt", "cookies.txt"} {
		p := filepath.Join(m.cfg.CookiesDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
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
		log.Printf("[rsshub] SeedChannelProfileRowsForFeedItems: %v", err)
	} else if n > 0 {
		log.Printf("[rsshub] primed %d feed profile rows", n)
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
	os.MkdirAll(avatarDir, 0o755)

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
		log.Printf("[rsshub] avatar download failed for %s: %v", channelID, err)
		return
	}
	if _, err := normalizeDownloadedImage(tmpPath, avatarDir, channelID); err != nil {
		log.Printf("[rsshub] avatar normalize failed for %s: %v", channelID, err)
	}
}

// backfillTruncatedQuotes processes retweets from the current ingest cycle that
// have truncated body text (ending with "…") and no quote data. For each unique
// original author, it fetches their RSSHub feed and fills in the missing quote.
func (m *Manager) backfillTruncatedQuotes(ctx context.Context, items []model.FeedItem) {
	// Collect retweets that need backfill: truncated body, no quote data.
	type needsQuote struct {
		tweetID      string
		authorHandle string
	}
	var candidates []needsQuote
	for _, item := range items {
		if item.IsRetweet && item.QuoteTweetID == "" && strings.HasSuffix(item.BodyText, "…") {
			candidates = append(candidates, needsQuote{
				tweetID:      item.TweetID,
				authorHandle: item.AuthorHandle,
			})
		}
	}
	if len(candidates) == 0 {
		return
	}

	// Group by author handle — one fetch per author.
	type authorBatch struct {
		handle   string
		tweetIDs []string
	}
	byAuthor := make(map[string]*authorBatch)
	for _, c := range candidates {
		if c.authorHandle == "" {
			continue
		}
		b, ok := byAuthor[c.authorHandle]
		if !ok {
			b = &authorBatch{handle: c.authorHandle}
			byAuthor[c.authorHandle] = b
		}
		b.tweetIDs = append(b.tweetIDs, c.tweetID)
	}

	log.Printf("[rsshub] quote backfill: %d truncated retweets from %d authors", len(candidates), len(byAuthor))

	filled := 0
	for _, batch := range byAuthor {
		if ctx.Err() != nil {
			break
		}
		// Fetch the original author's feed (may not be subscribed).
		authorItems, _, err := m.fetchAndParse(ctx, batch.handle)
		if err != nil {
			// Non-critical — skip silently; the retweeter's feed just had truncated data.
			continue
		}

		// Index fetched items by tweet_id for quick lookup.
		lookup := make(map[string]model.FeedItem, len(authorItems))
		for _, ai := range authorItems {
			if ai.QuoteTweetID != "" {
				lookup[ai.TweetID] = ai
			}
		}

		// Backfill any matching tweets.
		for _, tid := range batch.tweetIDs {
			if rich, ok := lookup[tid]; ok {
				if err := m.db.BackfillQuoteData(tid, rich); err != nil {
					log.Printf("[rsshub] backfill quote %s: %v", tid, err)
					continue
				}
				filled++
				// Enqueue a media job for the newly discovered quote media.
				if rich.QuoteMediaJSON != "" {
					kind := classifyMediaKind(rich.QuoteMediaJSON)
					if kind != "unknown" {
						_ = m.db.EnqueueFeedMediaJobs([]db.FeedMediaJobRow{{
							TweetID:      tid,
							TweetURL:     rich.CanonicalURL,
							SourceHandle: rich.SourceHandle,
							MediaKind:    kind,
						}})
					}
				}
			}
		}
	}

	if filled > 0 {
		log.Printf("[rsshub] quote backfill: filled %d/%d", filled, len(candidates))
		m.KickFeedMedia()
	}
}

// truncQuoteThreshold is the minimum quote_body_text length we treat as
// "potentially truncated" by RSSHub's ~280-char limit.
const truncQuoteThreshold = 270

// truncQuoteSweepMaxLen is the upper bound for the periodic DB sweep — items
// noticeably longer than 280 chars are real long quotes already enriched.
const truncQuoteSweepMaxLen = 285

// quoteSweepMinInterval rate-limits the DB-wide sweep so we don't hit
// fxtwitter on every 5-min ingest cycle.
const quoteSweepMinInterval = time.Hour

// quoteGroup collects all parent feed_items that quote the same source tweet,
// so we only hit fxtwitter once per unique quote.
type quoteGroup struct {
	quoteTweetID string
	quoteAuthor  string
	tweetIDs     []string
}

// backfillTruncatedQuoteText finds quote tweets from the current cycle whose
// quote_body_text was truncated by RSSHub (~280 char limit) and fetches the
// full text from the fxtwitter API.
func (m *Manager) backfillTruncatedQuoteText(ctx context.Context, items []model.FeedItem) {
	groups := map[string]*quoteGroup{}
	for _, item := range items {
		if item.QuoteTweetID == "" || item.QuoteAuthorHandle == "" || len(item.QuoteBodyText) < truncQuoteThreshold {
			continue
		}
		addToQuoteGroup(groups, item.QuoteTweetID, item.QuoteAuthorHandle, item.TweetID)
	}
	m.runQuoteTextBackfill(ctx, groups, "cycle")
}

// backfillTruncatedQuoteTextSweep retries fxtwitter for any DB rows still in
// the RSSHub truncation window, catching items whose per-cycle backfill failed
// (transient fxtwitter errors, deadline exceeded, etc.). Rate-limited to once
// per quoteSweepMinInterval.
func (m *Manager) backfillTruncatedQuoteTextSweep(ctx context.Context) {
	last := time.Unix(m.lastQuoteSweepAt.Load(), 0)
	if time.Since(last) < quoteSweepMinInterval {
		return
	}
	m.lastQuoteSweepAt.Store(time.Now().Unix())

	candidates, err := m.db.FindTruncatedQuoteCandidates(truncQuoteThreshold, truncQuoteSweepMaxLen)
	if err != nil {
		log.Printf("[rsshub] quote text sweep: FindTruncatedQuoteCandidates: %v", err)
		return
	}

	groups := map[string]*quoteGroup{}
	for _, c := range candidates {
		addToQuoteGroup(groups, c.QuoteTweetID, c.QuoteAuthor, c.TweetID)
	}
	m.runQuoteTextBackfill(ctx, groups, "sweep")
}

func addToQuoteGroup(groups map[string]*quoteGroup, quoteTweetID, quoteAuthor, parentTweetID string) {
	g, ok := groups[quoteTweetID]
	if !ok {
		g = &quoteGroup{quoteTweetID: quoteTweetID, quoteAuthor: quoteAuthor}
		groups[quoteTweetID] = g
	}
	g.tweetIDs = append(g.tweetIDs, parentTweetID)
}

// runQuoteTextBackfill is the shared loop for both per-cycle and sweep
// backfills: for each unique quote tweet, fetch the full text from fxtwitter
// and update every parent feed_item that quotes it. label distinguishes the
// two paths in log output.
func (m *Manager) runQuoteTextBackfill(ctx context.Context, groups map[string]*quoteGroup, label string) {
	if len(groups) == 0 {
		return
	}
	log.Printf("[rsshub] quote text %s: %d potentially truncated quotes", label, len(groups))

	updatedRows := 0
	for _, g := range groups {
		if ctx.Err() != nil {
			break
		}

		fullText, err := m.fetchTweetText(ctx, g.quoteAuthor, g.quoteTweetID)
		if err != nil {
			log.Printf("[rsshub] quote text %s %s: %v", label, g.quoteTweetID, err)
			continue
		}

		// Strip trailing t.co URLs that Twitter appends for media/cards.
		fullText = reTrailingTcoURL.ReplaceAllString(fullText, "")
		fullText = strings.TrimSpace(fullText)

		lang := rsshub.DetectLang(fullText)
		for _, tid := range g.tweetIDs {
			n, err := m.db.BackfillQuoteText(tid, fullText, lang)
			if err != nil {
				log.Printf("[rsshub] quote text %s update %s: %v", label, tid, err)
				continue
			}
			updatedRows += int(n)
		}
	}

	if updatedRows > 0 {
		log.Printf("[rsshub] quote text %s: updated %d rows across %d quotes", label, updatedRows, len(groups))
	}
}

// resolveReplyChains delegates to the ReplyResolver. Lazy-init on first use
// so unit tests that construct a Manager without an fxtwitter client still work.
func (m *Manager) resolveReplyChains(ctx context.Context, items []model.FeedItem) {
	if m.replyResolver == nil {
		m.replyResolver = NewReplyResolver(m.db, fxtwitter.NewClient())
	}
	if err := m.replyResolver.ResolveCycle(ctx, items); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[rsshub] reply resolver: %v", err)
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
		log.Printf("[rsshub] reply sweep: FindUnresolvedReplies: %v", err)
		return
	}
	if len(candidates) == 0 {
		return
	}
	log.Printf("[rsshub] reply sweep: retrying %d unresolved replies", len(candidates))
	m.resolveReplyChains(ctx, candidates)
}

// reTrailingTcoURL matches trailing https://t.co/... URLs appended by Twitter.
var reTrailingTcoURL = regexp.MustCompile(`\s*https://t\.co/\S+$`)

// fetchTweetText fetches the full tweet text from the fxtwitter API.
func (m *Manager) fetchTweetText(ctx context.Context, handle, tweetID string) (string, error) {
	apiURL := "https://api.fxtwitter.com/" + handle + "/status/" + tweetID

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fxtwitter API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fxtwitter API status %d for %s/%s", resp.StatusCode, handle, tweetID)
	}

	var result struct {
		Tweet struct {
			Text string `json:"text"`
		} `json:"tweet"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode fxtwitter response: %w", err)
	}
	if result.Tweet.Text == "" {
		return "", fmt.Errorf("fxtwitter returned empty text for %s/%s", handle, tweetID)
	}

	return result.Tweet.Text, nil
}

// upgradeTwimgURL replaces Twitter's _normal suffix (48x48) with _400x400 so
// downloaded avatars are large enough to avoid the placeholder size threshold.
func upgradeTwimgURL(u string) string {
	return strings.Replace(u, "_normal.", "_400x400.", 1)
}
