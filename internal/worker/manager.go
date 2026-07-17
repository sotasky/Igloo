// Package worker provides background workers for feed ingest and media download.
package worker

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/dearrow"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/sponsorblock"
	"github.com/screwys/igloo/internal/xfeed"
)

// WorkerStatus holds last-known state for a named worker.
type WorkerStatus struct {
	Name      string    `json:"name"`
	Running   bool      `json:"running"`
	LastRunAt time.Time `json:"last_run_at,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Manager owns all background goroutines.
type Manager struct {
	db                *db.DB
	cfg               *config.Config
	downloader        *download.Downloader
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	mediaCurrentKick  chan struct{} // buffered(1): coalescing kick for current media
	mediaBackfillKick chan struct{} // buffered(1): coalescing kick for historical media
	discoveryKick     chan struct{} // buffered(1): coalescing kick for platform discovery
	profileKick       chan struct{} // buffered(1): durable profile job wake-up
	xStatusEnrich     chan xfeed.StatusEnrichmentRequest
	ingestKick        chan struct{} // buffered(1): trigger immediate ingest
	feedScoringKick   chan struct{} // buffered(1): trigger immediate scoring
	ingestPaused      int32         // atomic: 1 = paused
	ingestRunning     int32         // atomic: 1 = cycle in progress
	ingestCycleTotal  int32         // atomic: channels to fetch in current cycle
	ingestCycleDone   int32         // atomic: channels fetched so far in current cycle
	stopRequested     int32         // atomic: 1 = stop requested
	statuses          map[string]*atomic.Value
	statusMu          sync.RWMutex
	activity          *ActivityRing // general server activity (200 items)
	dlActivity        *ActivityRing // download-specific activity (100 items)
	feedActivity      *ActivityRing // x_ingest/feed_media per-item activity (200 items)

	dlSessionCompleted       int32        // atomic
	dlSessionFailed          int32        // atomic
	dlLastDownload           atomic.Value // stores *LastDownloadInfo
	downloadBackoffMu        sync.Mutex
	downloadBackoff          map[string]downloadPlatformBackoff
	downloadPlatformMu       sync.Mutex
	downloadPlatformAt       map[db.DownloadLane]int
	mediaCurrentTurn         uint64
	mediaBackgroundTurn      uint64
	youtubeEnrichmentSlots   chan struct{}
	previewMu                sync.Mutex
	previewHints             map[string]struct{}
	previewRetry             map[string]previewRetryState
	previewNotBefore         time.Time
	previewBackfillNotBefore time.Time

	lastCycleAt       int64
	lastCycleFailures map[string]string
	lastCycleCooling  int
	lastCycleNotDue   int
	lastCycleReady    int
	lastCycleMu       sync.RWMutex

	replyResolver *ReplyResolver
	xFeedFetcher  xFeedFetcher
	xStatusMu     sync.Mutex
	xStatusQueued map[string]time.Time
	xRetentionMu  sync.Mutex

	// dearrowFetcher is the configured DeArrow orchestrator. Nil means DeArrow
	// fetching is disabled (e.g. unit tests that don't care about it).
	dearrowFetcher *dearrow.Fetcher

	// sponsorblockClient fetches SponsorBlock segments. Nil means SB fetching
	// is disabled (e.g. unit tests that don't want network calls). The
	// The download-complete hook co-fetches both APIs because they share
	// sponsor.ajay.app.
	sponsorblockClient sponsorblockFetcher

	instagramProfileFetch instagramProfileFetchFn
}

// sponsorblockFetcher is the narrow interface the worker needs from
// sponsorblock.Client. Kept local so tests can stub it without touching the
// real package.
type sponsorblockFetcher interface {
	Fetch(ctx context.Context, videoID string) ([]sponsorblock.Segment, error)
}

// NewManager creates a Manager; call StartAll to launch goroutines.
func NewManager(database *db.DB, cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		db:                     database,
		cfg:                    cfg,
		downloader:             download.NewDownloader(cfg.CookiesDir),
		ctx:                    ctx,
		cancel:                 cancel,
		mediaCurrentKick:       make(chan struct{}, 1),
		mediaBackfillKick:      make(chan struct{}, 1),
		discoveryKick:          make(chan struct{}, 1),
		profileKick:            make(chan struct{}, 1),
		xStatusEnrich:          make(chan xfeed.StatusEnrichmentRequest, 1024),
		ingestKick:             make(chan struct{}, 1),
		feedScoringKick:        make(chan struct{}, 1),
		statuses:               make(map[string]*atomic.Value),
		activity:               NewActivityRing(200),
		dlActivity:             NewActivityRing(100),
		feedActivity:           NewActivityRing(200),
		youtubeEnrichmentSlots: make(chan struct{}, 2),
		xStatusQueued:          make(map[string]time.Time),
		downloadBackoff:        make(map[string]downloadPlatformBackoff),
		downloadPlatformAt: map[db.DownloadLane]int{
			db.DownloadLaneCurrent:  0,
			db.DownloadLaneBackfill: 0,
		},
		previewHints:             make(map[string]struct{}),
		previewRetry:             make(map[string]previewRetryState),
		previewBackfillNotBefore: time.Now().Add(previewBackfillInterval),
	}
	m.dearrowFetcher = &dearrow.Fetcher{
		Client:  dearrow.NewClient(dearrow.DefaultBaseURL),
		Extract: dearrow.ExtractFrame,
		Mutate: func(ctx context.Context, work func() error) error {
			return m.downloader.RunMedia(ctx, download.MediaLaneBulkBackground, work)
		},
		ThumbDir: filepath.Join(cfg.Storage.StateRoot(), "thumbnails", "dearrow"),
	}
	m.sponsorblockClient = sponsorblock.NewClient(sponsorblock.DefaultBaseURL)
	m.downloader.SetMediaExecutor(cfg.Storage.MediaExecutor())
	m.downloader.SetOperationSink(database)
	return m
}

// StartAll clears stale in-flight queue state and launches the normal workers.
func (m *Manager) StartAll() {
	startupStarted := time.Now()
	log.Printf("[worker] startup recovery started")

	if err := m.db.ResetExpiredIngestBackoff(); err != nil {
		log.Printf("[worker] ResetExpiredIngestBackoff: %v", err)
	}
	log.Printf("[worker] startup recovery completed in %s", time.Since(startupStarted).Round(time.Millisecond))

	// One-shot startup tasks — not tracked in status map.
	m.startOnce("feed_bootstrap", m.runFeedBootstrap)
	m.launch(xIngestWorkerName, m.runXIngestLoop)
	m.launch("x_status_enrichment", m.runXStatusEnrichmentLoop)
	m.launch("media_current", m.runMediaCurrentLoop)
	m.launch("media_backfill", m.runMediaBackfillLoop)
	m.launch("profile_refresh", m.runProfileJobLoop)
	m.launch("scheduler", m.runScheduler)
	m.launch("feed_scoring", m.runFeedScoringWorker)
	m.launchDelayed("downloader_operation_prune", 5*time.Minute, m.runDownloaderOperationPruner)
	m.launchDelayed("backup", 5*time.Minute, m.runBackupWorker)

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
	if err := m.db.PruneDownloaderOperations(db.DownloaderOperationMaxRows, db.DownloaderOperationMaxAge); err != nil {
		log.Printf("[worker] PruneDownloaderOperations: %v", err)
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

// KickMediaWork sends a non-blocking signal to wake durable media work immediately.
func (m *Manager) KickMediaWork() {
	for _, kick := range []chan struct{}{m.mediaCurrentKick, m.mediaBackfillKick} {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
}

// KickDiscovery sends a non-blocking signal to wake the platform discovery dispatcher.
func (m *Manager) KickDiscovery() {
	if m == nil || m.discoveryKick == nil {
		return
	}
	select {
	case m.discoveryKick <- struct{}{}:
	default:
	}
}

// KickProfileJobs sends a non-blocking wake-up to the durable identity worker.
func (m *Manager) KickProfileJobs() {
	if m == nil || m.profileKick == nil {
		return
	}
	select {
	case m.profileKick <- struct{}{}:
	default:
	}
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

// TriggerChannelCheck makes a single followed channel immediately due.
func (m *Manager) TriggerChannelCheck(channelID string) {
	channel, err := m.db.GetChannel(channelID)
	if err == nil && channel != nil && channel.Platform == "twitter" {
		if err := m.db.ResetIngestHandle(channelID); err != nil {
			log.Printf("[worker] X channel refresh reset failed: %s: %v", channelID, err)
			return
		}
		m.KickIngest()
		return
	}
	if err := m.db.ClearChannelChecked(channelID); err != nil {
		log.Printf("[worker] channel refresh clear failed: %s: %v", channelID, err)
		return
	}
	m.KickDiscovery()
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

func (m *Manager) PreviewQueueLen() int {
	if m == nil || m.db == nil {
		return 0
	}
	count, _ := m.db.CountPendingVideoPreviews()
	return count
}

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

func (m *Manager) launchDelayed(name string, delay time.Duration, fn func(context.Context)) {
	m.setStatus(name, WorkerStatus{Name: name, Running: true, Detail: "delayed start"})
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		timer := time.NewTimer(delay)
		select {
		case <-m.ctx.Done():
			timer.Stop()
			m.setStatus(name, WorkerStatus{Name: name, Running: false, LastRunAt: time.Now()})
			return
		case <-timer.C:
		}
		m.launch(name, fn)
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

func (m *Manager) startOnceDelayed(name string, delay time.Duration, fn func(context.Context)) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		timer := time.NewTimer(delay)
		select {
		case <-m.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		m.startOnce(name, fn)
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
