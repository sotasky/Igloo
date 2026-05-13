// Package worker provides background workers for feed ingest and media download.
package worker

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/dearrow"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/sponsorblock"
	"github.com/screwys/igloo/internal/xfeed"
)

// WorkerStatus holds last-known state for a named worker.
type WorkerStatus struct {
	Name      string    `json:"name"`
	Running   bool      `json:"running"`
	LastRunAt time.Time `json:"last_run_at,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Manager owns all background goroutines.
type Manager struct {
	db               *db.DB
	cfg              *config.Config
	downloader       *download.Downloader
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	feedMediaKick    chan struct{} // buffered(1): coalescing kick
	downloadKick     chan struct{} // buffered(1): coalescing kick for download pool
	avatarRequest    chan string   // buffered(256): on-demand avatar/profile fetch requests
	xStatusEnrich    chan xfeed.StatusEnrichmentRequest
	previewChan      chan PreviewRequest // buffered(256): FIFO preview queue
	ingestKick       chan struct{}       // buffered(1): trigger immediate ingest
	feedScoringKick  chan struct{}       // buffered(1): trigger immediate scoring
	ingestPaused     int32               // atomic: 1 = paused
	ingestRunning    int32               // atomic: 1 = cycle in progress
	ingestCycleTotal int32               // atomic: channels to fetch in current cycle
	ingestCycleDone  int32               // atomic: channels fetched so far in current cycle
	stopRequested    int32               // atomic: 1 = stop requested
	statuses         map[string]*atomic.Value
	statusMu         sync.RWMutex
	activity         *ActivityRing // general server activity (200 items)
	dlActivity       *ActivityRing // download-specific activity (100 items)
	feedActivity     *ActivityRing // x_ingest/feed_media per-item activity (200 items)

	dlSessionCompleted int32        // atomic
	dlSessionFailed    int32        // atomic
	dlLastDownload     atomic.Value // stores *LastDownloadInfo

	lastCycleAt       int64
	lastCycleFailures map[string]string
	lastCycleCooling  int
	lastCycleNotDue   int
	lastCycleReady    int
	lastCycleMu       sync.RWMutex

	lastReplySweepAt atomic.Int64 // unix seconds; rate-limits resolveReplyChainsSweep

	replyResolver *ReplyResolver
	xFeedFetcher  xFeedFetcher
	xStatusMu     sync.Mutex
	xStatusQueued map[string]time.Time

	// dearrowFetcher is the configured DeArrow orchestrator. Nil means DeArrow
	// fetching is disabled (e.g. unit tests that don't care about it).
	dearrowFetcher *dearrow.Fetcher

	// sponsorblockClient fetches SponsorBlock segments. Nil means SB fetching
	// is disabled (e.g. unit tests that don't want network calls). The
	// youtube-enrichment worker and download-complete hook piggyback this against
	// the DeArrow rate limit since both APIs share sponsor.ajay.app.
	sponsorblockClient sponsorblockFetcher

	instagramProfileFetch instagramProfileFetchFn
}

// sponsorblockFetcher is the narrow interface the worker needs from
// sponsorblock.Client. Kept local so tests can stub it without touching the
// real package.
type sponsorblockFetcher interface {
	Fetch(ctx context.Context, videoID string) ([]sponsorblock.Segment, error)
}

var instagramHandleRe = regexp.MustCompile(`^[a-z0-9_.]{1,64}$`)

// NewManager creates a Manager; call StartAll to launch goroutines.
func NewManager(database *db.DB, cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		db:              database,
		cfg:             cfg,
		downloader:      download.NewDownloader(cfg.CookiesDir),
		ctx:             ctx,
		cancel:          cancel,
		feedMediaKick:   make(chan struct{}, 1),
		downloadKick:    make(chan struct{}, 1),
		avatarRequest:   make(chan string, 256),
		xStatusEnrich:   make(chan xfeed.StatusEnrichmentRequest, 1024),
		previewChan:     make(chan PreviewRequest, 256),
		ingestKick:      make(chan struct{}, 1),
		feedScoringKick: make(chan struct{}, 1),
		statuses:        make(map[string]*atomic.Value),
		activity:        NewActivityRing(200),
		dlActivity:      NewActivityRing(100),
		feedActivity:    NewActivityRing(200),
		xStatusQueued:   make(map[string]time.Time),
	}
	m.dearrowFetcher = &dearrow.Fetcher{
		Client:   dearrow.NewClient(dearrow.DefaultBaseURL),
		Extract:  dearrow.ExtractFrame,
		ThumbDir: filepath.Join(cfg.DataDir, "thumbnails", "dearrow"),
	}
	m.sponsorblockClient = sponsorblock.NewClient(sponsorblock.DefaultBaseURL)
	m.downloader.SetOperationSink(database)
	return m
}

// StartAll runs sync startup tasks then launches all long-running goroutines.
// Sequence:
//  1. [sync]  migrateMediaPaths — backfill media_files from disk
//  2. [async] buildSearchIndex  — FTS5 rebuild
//  3. [async] runFeedBootstrap  — immediate ingest if sparse
//  4. [async] runRankedQueueWarmup — pre-score feed
//  5. [async] runXIngestLoop       — long-running X ingest
//  6. [async] runFeedMediaWorker   — long-running download
//  7. [async] runProfileRefreshLoop — unified profile/avatar/banner refresh for all platforms
func (m *Manager) StartAll() {
	if err := m.db.ResetExpiredIngestBackoff(); err != nil {
		log.Printf("[worker] ResetExpiredIngestBackoff: %v", err)
	}

	// Reset jobs left in processing state from a previous run.
	if n, err := m.db.ResetStaleDownloadQueueItems(); err != nil {
		log.Printf("[worker] ResetStaleDownloadQueueItems: %v", err)
	} else if n > 0 {
		log.Printf("[worker] reset %d stale download jobs to pending", n)
	}
	if n, err := m.db.ResetStaleFeedMediaJobs(); err != nil {
		log.Printf("[worker] ResetStaleFeedMediaJobs: %v", err)
	} else if n > 0 {
		log.Printf("[worker] reset %d stale feed media jobs to queued", n)
	}
	if n, err := m.db.EnsureBookmarkVideoStubs(); err != nil {
		log.Printf("[worker] EnsureBookmarkVideoStubs: %v", err)
	} else if n > 0 {
		log.Printf("[worker] created %d video stubs for bookmarked feed items", n)
	}
	if n, err := m.db.EnsureProtectedFeedItemStubs(); err != nil {
		log.Printf("[worker] EnsureProtectedFeedItemStubs: %v", err)
	} else if n > 0 {
		log.Printf("[worker] created %d protected feed-item stubs", n)
	}
	if n, err := m.db.EnqueueMissingBookmarkLikeMedia(); err != nil {
		log.Printf("[worker] EnqueueMissingBookmarkLikeMedia: %v", err)
	} else if n > 0 {
		log.Printf("[worker] enqueued %d feed media jobs for bookmarked/liked items", n)
	}
	if n, err := m.db.SeedChannelProfileRows(); err != nil {
		log.Printf("[worker] SeedChannelProfileRows: %v", err)
	} else if n > 0 {
		log.Printf("[worker] seeded/updated %d channel profile rows", n)
	}
	if n, err := m.db.SeedSyntheticTwitterAvatarProfiles(); err != nil {
		log.Printf("[worker] SeedSyntheticTwitterAvatarProfiles: %v", err)
	} else if n > 0 {
		log.Printf("[worker] seeded %d synthetic twitter avatar profile rows", n)
	}

	m.migrateMediaPaths()

	// One-shot startup tasks — not tracked in status map.
	m.startOnce("search_index", m.buildSearchIndex)
	m.startOnce("feed_bootstrap", m.runFeedBootstrap)
	m.startOnce("ranked_queue_warmup", m.runRankedQueueWarmup)
	m.launch(xIngestWorkerName, m.runXIngestLoop)
	m.launch("x_status_enrichment", m.runXStatusEnrichmentLoop)
	m.launch("feed_media", m.runFeedMediaWorker)
	m.launch("profile_refresh", m.runProfileRefreshLoop)
	m.launch("dearrow", m.runDearrowWorker)
	m.launch("scheduler", m.runScheduler)
	m.launch("download_pool", m.runDownloadPool)
	m.launch("preview", m.runPreviewWorker)
	m.launch("downloader_operation_prune", m.runDownloaderOperationPruner)
	m.startOnce("preview_backfill", m.backfillPreviews)
	m.startOnce("thumbnail_backfill", m.backfillThumbnails)
	m.launch("feed_scoring", m.runFeedScoringWorker)
	m.launch("backup", m.runBackupWorker)

}

// Shutdown cancels the context and waits for all goroutines to stop.
func (m *Manager) Shutdown() {
	m.cancel()
	m.wg.Wait()
}

func (m *Manager) runDownloaderOperationPruner(ctx context.Context) {
	if m == nil || m.db == nil {
		return
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.db.PruneDownloaderOperations(db.DownloaderOperationMaxRows, db.DownloaderOperationMaxAge); err != nil {
				log.Printf("[worker] PruneDownloaderOperations: %v", err)
			}
		}
	}
}

// ShutdownTimeout cancels workers and waits up to timeout for them to exit.
// It returns false when a worker ignored cancellation long enough that the
// caller should continue process shutdown instead of waiting indefinitely.
func (m *Manager) ShutdownTimeout(timeout time.Duration) bool {
	m.cancel()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// KickFeedMedia sends a non-blocking signal to wake the feed media worker immediately.
func (m *Manager) KickFeedMedia() {
	select {
	case m.feedMediaKick <- struct{}{}:
	default:
	}
}

// KickDownloadPool sends a non-blocking signal to wake the download pool immediately.
func (m *Manager) KickDownloadPool() {
	select {
	case m.downloadKick <- struct{}{}:
	default:
	}
}

// RequestAvatar enqueues channelID for an on-demand avatar fetch.
// The send is non-blocking; if the buffer is full, a skeleton channel_profiles
// row or stale existing avatar row still ensures the next refresh cycle will
// pick it up.
func (m *Manager) RequestAvatar(channelID string) {
	channelID = normalizeProfileRequestChannelID(channelID)
	if channelID == "" {
		return
	}
	if m == nil || m.db == nil {
		return
	}
	if model.IsSyntheticTwitterAvatarChannelID(channelID) {
		existing, _ := m.db.GetChannelProfile(channelID)
		if existing == nil || existing.AvatarURL == "" {
			return
		}
		if m.avatarRequest == nil {
			return
		}
		select {
		case m.avatarRequest <- channelID:
		default:
			m.markAvatarRecoveryDue(channelID, existing)
		}
		return
	}
	row := model.ChannelProfile{
		ChannelID: channelID,
		Platform:  platformForChannelID(channelID),
		Handle:    trimChannelIDPrefix(channelID),
	}
	if row.Platform == "" {
		return
	}
	if m.cfg != nil && !m.cfg.PlatformEnabled(row.Platform) {
		return
	}
	isInstagram := strings.HasPrefix(channelID, "instagram_")
	existing, err := m.db.GetChannelProfile(channelID)
	if err != nil {
		log.Printf("[profile] GetChannelProfile %s: %v", channelID, err)
		if isInstagram {
			return
		}
	} else if isInstagram && existing == nil {
		return
	} else if existing == nil {
		if err := m.db.UpsertChannelProfile(row); err != nil {
			log.Printf("[profile] seed %s: %v", channelID, err)
		}
	}
	if m.avatarRequest == nil {
		return
	}
	select {
	case m.avatarRequest <- channelID:
	default:
		m.markAvatarRecoveryDue(channelID, existing)
	}
}

func (m *Manager) markAvatarRecoveryDue(channelID string, existing *model.ChannelProfile) {
	if m == nil || m.db == nil || existing == nil || existing.AvatarURL == "" {
		return
	}
	if !profileRetryDue(existing, time.Now()) {
		return
	}
	if err := m.db.MarkChannelProfileRefreshDue(channelID); err != nil {
		log.Printf("[profile] mark avatar recovery due %s: %v", channelID, err)
	}
}

func normalizeProfileRequestChannelID(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ""
	}
	lower := strings.ToLower(channelID)
	switch {
	case strings.HasPrefix(lower, "twitter_"),
		strings.HasPrefix(lower, "tiktok_"):
		return lower
	case strings.HasPrefix(lower, "instagram_"):
		handle := strings.TrimPrefix(lower, "instagram_")
		if !instagramHandleRe.MatchString(handle) {
			return ""
		}
		return lower
	case strings.HasPrefix(lower, "youtube_"):
		idx := strings.IndexByte(channelID, '_')
		if idx < 0 || idx+1 >= len(channelID) {
			return ""
		}
		return "youtube_" + channelID[idx+1:]
	default:
		return channelID
	}
}

func trimChannelIDPrefix(channelID string) string {
	if idx := strings.IndexByte(channelID, '_'); idx >= 0 && idx+1 < len(channelID) {
		return channelID[idx+1:]
	}
	return channelID
}

// KickIngest sends a non-blocking signal to trigger an immediate ingest cycle.
func (m *Manager) KickIngest() {
	select {
	case m.ingestKick <- struct{}{}:
	default:
	}
}

// KickFeedScoring sends a non-blocking signal to trigger immediate feed rescoring.
func (m *Manager) KickFeedScoring() {
	select {
	case m.feedScoringKick <- struct{}{}:
	default:
	}
}

// SetIngestPaused atomically sets the ingest paused state.
func (m *Manager) SetIngestPaused(paused bool) {
	if paused {
		atomic.StoreInt32(&m.ingestPaused, 1)
	} else {
		atomic.StoreInt32(&m.ingestPaused, 0)
	}
}

// IsIngestPaused returns true if ingest is currently paused.
func (m *Manager) IsIngestPaused() bool {
	return atomic.LoadInt32(&m.ingestPaused) == 1
}

// SetIngestRunning marks whether an ingest cycle is currently in progress.
func (m *Manager) SetIngestRunning(running bool) {
	if running {
		atomic.StoreInt32(&m.ingestRunning, 1)
	} else {
		atomic.StoreInt32(&m.ingestRunning, 0)
	}
}

// IsIngestRunning returns true if an ingest cycle is in progress.
func (m *Manager) IsIngestRunning() bool {
	return atomic.LoadInt32(&m.ingestRunning) == 1
}

// TriggerChannelCheck queues a single channel for immediate check.
func (m *Manager) TriggerChannelCheck(channelID string) {
	m.db.AddChannelToQueue(channelID, 10) // high priority
	m.KickDownloadPool()
}

// TriggerPlatformRefresh clears last_checked for all channels of a platform
// so the scheduler picks them up immediately on the next cycle.
func (m *Manager) TriggerPlatformRefresh(platform string) {
	if m.cfg != nil && !m.cfg.PlatformEnabled(platform) {
		log.Printf("[worker] platform refresh skipped; platform disabled: %s", platform)
		return
	}
	channels, _ := m.db.GetSubscribedChannels()
	if platform == "twitter" {
		for _, ch := range channels {
			if ch.Platform == "twitter" {
				_ = m.db.ResetIngestHandle(ch.ChannelID)
			}
		}
		m.KickIngest()
		return
	}
	if n, err := m.db.ClearPlatformChecked(platform); err != nil {
		log.Printf("[worker] platform refresh clear failed: %s: %v", platform, err)
	} else {
		log.Printf("[worker] platform refresh queued: %s (%d channels)", platform, n)
	}
	m.TriggerDownloadCycle(false)
}

// SetStopRequested sets/clears the stop flag.
func (m *Manager) SetStopRequested(stop bool) {
	if stop {
		atomic.StoreInt32(&m.stopRequested, 1)
	} else {
		atomic.StoreInt32(&m.stopRequested, 0)
	}
}

// IsStopRequested returns true if stop has been requested.
func (m *Manager) IsStopRequested() bool {
	return atomic.LoadInt32(&m.stopRequested) == 1
}

// PreviewChan returns the preview channel for status reporting.
func (m *Manager) PreviewChan() <-chan PreviewRequest { return m.previewChan }

// PreviewQueueLen returns the number of preview requests currently queued.
func (m *Manager) PreviewQueueLen() int { return len(m.previewChan) }

// Emit records an activity event. Source identifies the worker (e.g. "x_ingest", "download").
func (m *Manager) Emit(source, message, status string) {
	m.activity.Push(makeEvent(source, message, status))
}

// EmitDownload records a download-specific event with channel/platform info.
func (m *Manager) EmitDownload(message, status, channelID, platform string) {
	e := makeEvent("download", message, status)
	e.ChannelID = channelID
	e.Platform = platform
	e.Kind = "video"
	m.activity.Push(e)
	m.dlActivity.Push(e)
}

// EmitFeed records a feed-specific event (x_ingest/feed_media per-item).
// Always goes to the feed activity ring; errors and warnings also surface in the main ring.
func (m *Manager) EmitFeed(source, message, status string) {
	e := makeEvent(source, message, status)
	m.feedActivity.Push(e)
	if status == "error" || status == "warning" {
		m.activity.Push(e)
	}
}

// Activity returns the general activity ring.
func (m *Manager) Activity() *ActivityRing { return m.activity }

// FeedActivity returns the feed-specific activity ring.
func (m *Manager) FeedActivity() *ActivityRing { return m.feedActivity }

// DownloadActivity returns the download-specific activity ring.
func (m *Manager) DownloadActivity() *ActivityRing { return m.dlActivity }

// emitSchedulerEvent records a scheduler event in the download activity ring
// so channel checks appear in the downloads tab alongside download events.
func (m *Manager) emitSchedulerEvent(message, status, channelID, platform string) {
	e := makeEvent("scheduler", message, status)
	e.ChannelID = channelID
	e.Platform = platform
	e.Kind = "channel"
	m.dlActivity.Push(e)
}

// LastDownloadInfo holds details of the most recent download.
type LastDownloadInfo struct {
	Channel   string `json:"channel"`
	Platform  string `json:"platform"`
	Timestamp int64  `json:"ts"`
}

// LastDownload returns info about the most recent download, or nil.
func (m *Manager) LastDownload() *LastDownloadInfo {
	if v := m.dlLastDownload.Load(); v != nil {
		if info, ok := v.(*LastDownloadInfo); ok {
			return info
		}
	}
	return nil
}

// DownloadSessionCounts returns completed and failed counts since startup.
func (m *Manager) DownloadSessionCounts() (completed, failed int) {
	return int(atomic.LoadInt32(&m.dlSessionCompleted)), int(atomic.LoadInt32(&m.dlSessionFailed))
}

// IngestCycleStats returns the last ingest cycle stats for the feed dashboard.
func (m *Manager) IngestCycleStats() (cycleAt int64, failures map[string]string, cooling, notDue, ready int) {
	m.lastCycleMu.RLock()
	defer m.lastCycleMu.RUnlock()
	fc := make(map[string]string, len(m.lastCycleFailures))
	for k, v := range m.lastCycleFailures {
		fc[k] = v
	}
	return m.lastCycleAt, fc, m.lastCycleCooling, m.lastCycleNotDue, m.lastCycleReady
}

// IngestLiveProgress returns the current cycle's live progress counters.
func (m *Manager) IngestLiveProgress() (total, done int) {
	return int(atomic.LoadInt32(&m.ingestCycleTotal)), int(atomic.LoadInt32(&m.ingestCycleDone))
}

// Downloader exposes the underlying Downloader for use by HTTP handlers.
func (m *Manager) Downloader() *download.Downloader {
	return m.downloader
}

// Status returns a snapshot of all worker statuses.
func (m *Manager) Status() []WorkerStatus {
	m.statusMu.RLock()
	names := make([]string, 0, len(m.statuses))
	for name := range m.statuses {
		names = append(names, name)
	}
	m.statusMu.RUnlock()

	result := make([]WorkerStatus, 0, len(names))
	for _, name := range names {
		m.statusMu.RLock()
		av := m.statuses[name]
		m.statusMu.RUnlock()
		if av == nil {
			continue
		}
		if v := av.Load(); v != nil {
			if s, ok := v.(WorkerStatus); ok {
				result = append(result, s)
			}
		}
	}
	return result
}

// StatusJSON returns worker statuses as a JSON byte slice.
func (m *Manager) StatusJSON() ([]byte, error) {
	return json.Marshal(m.Status())
}

// launch starts fn in a goroutine with panic recovery and status tracking.
// The goroutine is registered with the WaitGroup.
func (m *Manager) launch(name string, fn func(context.Context)) {
	m.setStatus(name, WorkerStatus{Name: name, Running: true})
	m.Emit("system", "Worker started: "+name, "info")
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[worker/%s] panic: %v\n%s", name, r, debug.Stack())
				m.setStatus(name, WorkerStatus{
					Name:      name,
					Running:   false,
					LastRunAt: time.Now(),
					Error:     "panic recovered",
				})
				m.Emit("system", "Worker panic: "+name, "error")
			}
		}()
		fn(m.ctx)
		m.setStatus(name, WorkerStatus{
			Name:      name,
			Running:   false,
			LastRunAt: time.Now(),
		})
		m.Emit("system", "Worker exited: "+name, "info")
	}()
}

// startOnce runs fn in a background goroutine without registering it in the
// worker status map. Use for one-shot startup tasks that exit quickly.
func (m *Manager) startOnce(name string, fn func(context.Context)) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[worker/%s] panic: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn(m.ctx)
	}()
}

// setStatus atomically stores a WorkerStatus for name.
func (m *Manager) setStatus(name string, s WorkerStatus) {
	m.statusMu.Lock()
	av, ok := m.statuses[name]
	if !ok {
		av = &atomic.Value{}
		m.statuses[name] = av
	}
	m.statusMu.Unlock()
	av.Store(s)
}
