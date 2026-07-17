package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/worker"
)

func activityToEntry(e worker.ActivityEvent) components.ActivityEntry {
	return components.ActivityEntry{
		Time:      e.Time,
		Timestamp: e.Timestamp,
		Message:   e.Message,
		Status:    e.Status,
		ChannelID: e.ChannelID,
		Platform:  e.Platform,
		Kind:      e.Kind,
	}
}

func reverseActivityEvents(events []worker.ActivityEvent) []worker.ActivityEvent {
	out := make([]worker.ActivityEvent, len(events))
	for i := range events {
		out[i] = events[len(events)-1-i]
	}
	return out
}

func twitterSourceHandle(ch model.Channel) string {
	if ch.Platform != "twitter" {
		return ""
	}
	handle := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(ch.SourceID, "@")))
	handle = strings.TrimPrefix(handle, "twitter_")
	if handle != "" {
		return handle
	}
	channelID := strings.ToLower(strings.TrimSpace(ch.ChannelID))
	if strings.HasPrefix(channelID, "twitter_") && len(channelID) > len("twitter_") {
		return strings.TrimPrefix(channelID, "twitter_")
	}
	return ""
}

var serverStartTime = time.Now()

const (
	serverDashboardInventoryRefreshInterval = 15 * time.Minute
	serverDashboardInventoryCacheKey        = "server-dashboard-inventory-v1.json"
	serverDashboardInventoryCacheVersion    = 1
)

type serverDashboardInventoryFile struct {
	Version   int                            `json:"version"`
	UpdatedAt time.Time                      `json:"updated_at"`
	Data      components.ServerDashboardData `json:"data"`
}

var (
	memoryHistory   []float64
	memoryHistoryMu sync.Mutex
)

func (s *Server) registerStatusAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/sidebar-status", s.handleSidebarStatus)
	mux.HandleFunc("GET /api/sidebar/channels", s.handleSidebarChannels)
	mux.HandleFunc("GET /api/queue", s.handleQueue)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/feed/head", s.handleFeedHead)
	mux.HandleFunc("GET /api/feed/status", s.handleFeedStatus)
	mux.HandleFunc("GET /api/downloads/status", s.handleDownloadsStatus)
	mux.HandleFunc("GET /api/server/status", s.handleServerStatus)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	recent := s.workers.Activity().Last(5)
	recentActivity := make([]map[string]any, len(recent))
	for i, e := range recent {
		// Reverse so index 0 = newest
		recentActivity[len(recent)-1-i] = map[string]any{
			"msg":    e.Message,
			"source": e.Source,
			"status": e.Status,
			"ts":     e.Timestamp,
		}
	}
	writeJSON(w, 200, map[string]any{
		"activity":        "running",
		"workers":         s.workers.Status(),
		"is_downloading":  false,
		"stop_requested":  s.workers.IsStopRequested(),
		"last_updated":    time.Now().UnixMilli(),
		"recent_activity": recentActivity,
	})
}

func (s *Server) handleSidebarStatus(w http.ResponseWriter, r *http.Request) {
	// Activity
	recent := s.workers.Activity().Last(1)
	activityMsg := ""
	if len(recent) > 0 {
		activityMsg = recent[0].Message
	}

	pending, _ := s.db.HasPendingXContentDownloads()
	queueTotal := 0
	if pending {
		queueTotal = 1
	}

	data := components.SidebarStatusData{
		ActivityMsg:   activityMsg,
		IsDownloading: false,
		QueueTotal:    queueTotal,
		StopRequested: s.workers.IsStopRequested(),
	}
	_ = components.SidebarStatusFragment(s.pageProps(w, r), data).Render(r.Context(), w)
}

func (s *Server) handleSidebarChannels(w http.ResponseWriter, r *http.Request) {
	groupID := strings.TrimSpace(r.URL.Query().Get("group"))
	if groupID == "" {
		http.NotFound(w, r)
		return
	}
	groups := sidebarGroupsFromChannels(s.enrichedChannels())
	for _, group := range groups {
		if group.GroupID != groupID {
			continue
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.SidebarChannelItems(s.pageProps(w, r), group.Channels).Render(r.Context(), w)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	processing, pending, err := s.db.ListPendingXContentDownloads()
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	var procEntries []map[string]any
	for _, j := range processing {
		entry := map[string]any{
			"tweet_id":      j.TweetID,
			"source_handle": j.SourceHandle,
			"media_kind":    j.MediaKind,
			"slide_count":   j.AssetCount,
			"retry_count":   j.Attempts,
			"priority":      0,
		}
		if j.LastError != "" {
			entry["last_error"] = j.LastError
		}
		procEntries = append(procEntries, entry)
	}
	var pendEntries []map[string]any
	for _, j := range pending {
		entry := map[string]any{
			"tweet_id":      j.TweetID,
			"source_handle": j.SourceHandle,
			"media_kind":    j.MediaKind,
			"slide_count":   j.AssetCount,
			"retry_count":   j.Attempts,
			"priority":      0,
		}
		if j.LastError != "" {
			entry["last_error"] = j.LastError
		}
		pendEntries = append(pendEntries, entry)
	}
	if procEntries == nil {
		procEntries = []map[string]any{}
	}
	if pendEntries == nil {
		pendEntries = []map[string]any{}
	}

	writeJSON(w, 200, map[string]any{
		"processing":       procEntries,
		"pending":          pendEntries,
		"total_pending":    len(pendEntries),
		"total_processing": len(procEntries),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, _ := s.db.GetStats()
	unwatched, totalBytes, _ := s.db.GetVideoStats()
	_, processing, _ := s.db.CountPendingXContentDownloads()

	totalGB := float64(totalBytes) / (1024 * 1024 * 1024)

	writeJSON(w, 200, map[string]any{
		"total_channels":    stats.TotalChannels,
		"total_videos":      stats.TotalVideos,
		"unwatched_videos":  unwatched,
		"total_size_bytes":  totalBytes,
		"total_size_gb":     fmt.Sprintf("%.2f", totalGB),
		"pending_downloads": processing,
	})
}

func (s *Server) handleFeedHead(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("fmt") == "html" {
		currentHead := ""
		if headID, err := s.db.GetLatestFetchedFeedItemID(); err == nil {
			currentHead = headID
		}
		knownHead := strings.TrimSpace(r.URL.Query().Get("known_head"))
		hasNew := knownHead != "" && currentHead != "" && knownHead != currentHead

		var avatars []model.NewPosterAvatar
		if hasNew {
			candidates, _ := s.db.GetNewPosterAvatars(knownHead, 12)
			avatars = s.filterNewPosterAvatars(candidates, 3)
		}

		p := s.pageProps(w, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.FeedNewPostsBar(p, hasNew, knownHead, avatars).Render(r.Context(), w)
		return
	}

	item, err := s.db.GetLatestFeedItem()
	if err != nil || item == nil {
		writeJSON(w, 200, map[string]any{
			"success":       true,
			"tweet_id":      nil,
			"published_at":  int64(0),
			"source_handle": nil,
			"has_media":     false,
			"fetched_at":    time.Now().UnixMilli(),
		})
		return
	}

	var publishedAtMs int64
	if item.PublishedAt != nil {
		publishedAtMs = item.PublishedAt.UnixMilli()
	}

	writeJSON(w, 200, map[string]any{
		"success":       true,
		"tweet_id":      item.TweetID,
		"published_at":  publishedAtMs,
		"source_handle": item.SourceHandle,
		"has_media":     item.MediaJSON != "",
		"fetched_at":    item.FetchedAt.UnixMilli(),
	})
}

func (s *Server) filterNewPosterAvatars(candidates []model.NewPosterAvatar, limit int) []model.NewPosterAvatar {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	out := make([]model.NewPosterAvatar, 0, limit)
	for _, a := range candidates {
		handle := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(a.AuthorHandle, "@")))
		if handle == "" {
			continue
		}
		channelID := "twitter_" + handle
		if s.resolveAvatarPath(channelID) == "" {
			continue
		}
		a.AuthorAvatarURL = "/api/media/avatar/" + channelID
		out = append(out, a)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Server) handleFeedStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("fmt") == "html" {
		filter := r.URL.Query().Get("filter")
		if filter == "" {
			filter = "all"
		}
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("part") {
		case "live":
			_ = components.FeedDashboardLive(s.pageProps(w, r), s.feedDashboardLiveData()).Render(r.Context(), w)
		case "sources":
			_ = components.FeedDashboardSources(s.pageProps(w, r), s.feedDashboardSourcesData(filter)).Render(r.Context(), w)
		default:
			d := s.feedDashboardLiveData()
			sources := s.feedDashboardSourcesData(filter)
			d.Filter = sources.Filter
			d.Sources = sources.Sources
			d.XIngestStatus = sources.XIngestStatus
			_ = components.FeedDashboard(s.pageProps(w, r), d).Render(r.Context(), w)
		}
		return
	}

	count, _ := s.db.CountFeedItems()
	queued, processing, _ := s.db.CountPendingXContentDownloads()
	coolingSources, _ := s.db.IngestCoverageCounts()
	totalTwitterChannels := s.db.CountSubscribedTwitterChannels()
	cycleAt, failures, cooling, notDue, lastReady := s.workers.IngestCycleStats()
	liveTotal, liveDone := s.workers.IngestLiveProgress()

	withMedia := s.db.CountFeedItemsWithMedia()
	textOnly := s.db.CountFeedItemsTextOnly()

	_, tzOffset := time.Now().Zone()

	// JSON path
	allActivity := s.workers.FeedActivity().Last(100)
	var feedActivity []map[string]any
	for i := len(allActivity) - 1; i >= 0; i-- {
		e := allActivity[i]
		var kind string
		if e.Source == "feed_media" {
			switch e.Status {
			case "error":
				kind = "error"
			case "done":
				kind = "ok"
			default:
				kind = "media"
			}
		} else {
			switch e.Status {
			case "error":
				kind = "error"
			case "warning":
				kind = "timeout"
			case "done":
				kind = "ok"
			default:
				kind = "ingest"
			}
		}
		feedActivity = append(feedActivity, map[string]any{
			"ts":   e.Timestamp,
			"kind": kind,
			"msg":  e.Message,
		})
	}
	if feedActivity == nil {
		feedActivity = []map[string]any{}
	}

	writeJSON(w, 200, map[string]any{
		"success":               true,
		"ingest_enabled":        s.cfg == nil || s.cfg.PlatformEnabled("twitter"),
		"ingest_running":        s.workers.IsIngestRunning(),
		"ingest_paused":         s.workers.IsIngestPaused(),
		"ready_posts":           count,
		"pending_media_jobs":    queued + processing,
		"failed_media_jobs":     0,
		"total_feed_items":      count,
		"feed_items_with_media": withMedia,
		"feed_items_text_only":  textOnly,
		"bootstrap_state":       "complete",
		"tz_offset_sec":         tzOffset,
		"coverage": map[string]any{
			"due_sources":     lastReady,
			"total_sources":   totalTwitterChannels,
			"last_cycle_at":   cycleAt,
			"cooling_sources": coolingSources,
		},
		"ingest": map[string]any{
			"last_cycle_partial_failures": failures,
			"last_cycle_cooling_sources":  cooling,
			"last_cycle_not_due_sources":  notDue,
		},
		"live_progress": map[string]any{
			"cycle_total": liveTotal,
			"cycle_done":  liveDone,
		},
		"activity": feedActivity,
	})
}

func (s *Server) feedDashboardLiveData() components.FeedDashboardData {
	count, _ := s.db.CountFeedItems()
	queued, processing, _ := s.db.CountPendingXContentDownloads()
	totalTwitterChannels := s.db.CountSubscribedTwitterChannels()
	cycleAt, failures, cooling, notDue, lastReady := s.workers.IngestCycleStats()
	liveTotal, liveDone := s.workers.IngestLiveProgress()
	withMedia := s.db.CountFeedItemsWithMedia()
	textOnly := s.db.CountFeedItemsTextOnly()
	_, tzOffset := time.Now().Zone()

	ingestRunning := s.workers.IsIngestRunning()
	d := components.FeedDashboardData{
		IngestEnabled: s.cfg == nil || s.cfg.PlatformEnabled("twitter"),
		IngestRunning: ingestRunning,
		IngestPaused:  s.workers.IsIngestPaused(),
		ParsedTotal:   count,
		ParsedMedia:   withMedia,
		ParsedText:    textOnly,
		MediaReady:    count,
		MediaPending:  queued + processing,
		MediaTotal:    count + queued + processing,
		TzOffsetSec:   tzOffset,
	}
	if ingestRunning && liveTotal > 0 {
		d.IngestDone = liveDone
		d.IngestTotal = liveTotal
		d.IngestMeta = "fetching"
	} else if ingestRunning {
		d.IngestDone = lastReady
		d.IngestTotal = totalTwitterChannels
		d.IngestMeta = "starting"
	} else {
		d.IngestDone = lastReady
		d.IngestTotal = totalTwitterChannels
		if cycleAt > 0 {
			d.IngestMeta = components.TimeAgo(cycleAt)
		}
	}
	d.IngestFailures = len(failures)
	d.IngestCooling = cooling
	d.IngestNotDue = notDue

	allActivity := s.workers.FeedActivity().Last(100)
	for i := len(allActivity) - 1; i >= 0; i-- {
		e := allActivity[i]
		kind := "ingest"
		if e.Source == "feed_media" {
			switch e.Status {
			case "error":
				kind = "error"
			case "done":
				kind = "ok"
			default:
				kind = "media"
			}
		} else {
			switch e.Status {
			case "error":
				kind = "error"
			case "warning":
				kind = "timeout"
			case "done":
				kind = "ok"
			}
		}
		d.Activity = append(d.Activity, components.FeedActivityEntry{
			Timestamp: e.Timestamp,
			Kind:      kind,
			Message:   e.Message,
		})
		if len(d.Activity) >= 20 {
			break
		}
	}
	return d
}

func (s *Server) feedDashboardSourcesData(filter string) components.FeedDashboardData {
	sources, status := s.buildFeedSources()
	return components.FeedDashboardData{
		Filter:        filter,
		Sources:       sources,
		XIngestStatus: status,
	}
}

func (s *Server) handleDownloadsStatus(w http.ResponseWriter, r *http.Request) {
	queued, processing, _ := s.db.CountPendingXContentDownloads()
	total := queued + processing
	completed, failed := s.workers.DownloadSessionCounts()
	_, tzOffset := time.Now().Zone()

	if r.URL.Query().Get("fmt") == "html" {
		filter := r.URL.Query().Get("filter")
		if filter == "" {
			filter = "all"
		}
		d := components.DownloadsStatusData{
			QueueActive: processing,
			QueueQueued: queued,
			QueueTotal:  total,
			SessionDone: completed,
			SessionFail: failed,
			Filter:      filter,
			TzOffsetSec: tzOffset,
		}
		if info := s.workers.LastDownload(); info != nil {
			elapsed := time.Since(time.Unix(info.Timestamp, 0))
			d.LastElapsed = formatElapsed(int(elapsed.Seconds()))
			d.LastChannel = info.Channel
			d.LastPlatform = info.Platform
		}
		for _, e := range s.workers.DownloadActivity().Last(50) {
			d.Activity = append(d.Activity, activityToEntry(e))
		}
		// Reverse activity so newest is first
		for i, j := 0, len(d.Activity)-1; i < j; i, j = i+1, j-1 {
			d.Activity[i], d.Activity[j] = d.Activity[j], d.Activity[i]
		}
		for _, e := range s.workers.DownloadActivity().LastByKind("video", 50) {
			d.Recent = append(d.Recent, activityToEntry(e))
		}
		for i, j := 0, len(d.Recent)-1; i < j; i, j = i+1, j-1 {
			d.Recent[i], d.Recent[j] = d.Recent[j], d.Recent[i]
		}
		w.Header().Set("Content-Type", "text/html")
		_ = components.DownloadsDashboard(s.pageProps(w, r), d).Render(r.Context(), w)
		return
	}

	var lastDL any
	if info := s.workers.LastDownload(); info != nil {
		elapsed := time.Since(time.Unix(info.Timestamp, 0))
		lastDL = map[string]any{
			"channel":  info.Channel,
			"platform": info.Platform,
			"ts":       info.Timestamp,
			"elapsed":  formatElapsed(int(elapsed.Seconds())),
		}
	}

	writeJSON(w, 200, map[string]any{
		"queue":         map[string]any{"active": processing, "queued": queued, "total": total},
		"session":       map[string]any{"completed": completed, "failed": failed},
		"last_download": lastDL,
		"activity":      s.workers.DownloadActivity().Last(50),
		"recent":        s.workers.DownloadActivity().LastByKind("video", 50),
		"tz_offset_sec": tzOffset,
	})
}

func (s *Server) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("fmt") != "html" {
		s.handleServerStatusJSON(w, r)
		return
	}

	filter := r.URL.Query().Get("log_filter")
	if filter == "" {
		filter = "all"
	}
	w.Header().Set("Content-Type", "text/html")
	switch r.URL.Query().Get("part") {
	case "stats":
		data, ready := s.serverDashboardStaticData()
		if ready {
			_ = components.ServerDashboardStats(s.pageProps(w, r), data).Render(r.Context(), w)
		} else {
			_ = components.ServerDashboardStatsLoading(s.pageProps(w, r)).Render(r.Context(), w)
		}
	default:
		_ = components.ServerDashboardLive(s.pageProps(w, r), s.serverDashboardLiveData(filter)).Render(r.Context(), w)
	}
}

func (s *Server) serverDashboardLiveData(filter string) components.ServerDashboardData {
	upSeconds := int(time.Since(serverStartTime).Seconds())
	memMB := getMemoryRSSMB()

	memoryHistoryMu.Lock()
	memoryHistory = append(memoryHistory, memMB)
	if len(memoryHistory) > 30 {
		memoryHistory = memoryHistory[len(memoryHistory)-30:]
	}
	memHistCopy := make([]float64, len(memoryHistory))
	copy(memHistCopy, memoryHistory)
	memoryHistoryMu.Unlock()

	d := components.ServerDashboardData{
		UptimeText:    components.UptimeText(upSeconds, formatElapsed(upSeconds)),
		UptimeStarted: serverStartTime.Format("2006-01-02 15:04:05"),
		Errors24h:     len(s.workers.Activity().ByStatus("error")),
		ErrorsDelta:   0,
		MemoryMB:      memMB,
		MemoryHistory: memHistCopy,
		LogFilter:     filter,
	}
	for _, e := range reverseActivityEvents(s.workers.Activity().Last(100)) {
		d.Activity = append(d.Activity, components.ServerActivityEntry{
			Time: e.Time, Status: e.Status, Source: e.Source, Message: e.Message,
		})
	}
	for _, e := range s.workers.Activity().ByStatus("error") {
		d.Errors = append(d.Errors, components.ServerErrorEntry{Time: e.Time, Message: e.Message, Count: 1})
	}
	for _, e := range s.workers.Activity().ByStatus("warning") {
		d.Warnings = append(d.Warnings, components.ServerErrorEntry{Time: e.Time, Message: e.Message, Count: 1})
	}
	for _, ws := range s.workers.Status() {
		status := "stopped"
		if ws.Running {
			status = "running"
		}
		d.Processes = append(d.Processes, components.ServerProcessCard{
			Name: ws.Name, Status: status, Summary: ws.Summary, Detail: ws.Detail, Error: ws.Error,
		})
	}
	return d
}

// loadServerDashboardInventory restores the last complete inventory snapshot.
// It is deliberately a tiny sidecar read, not a database aggregation, so a
// process restart can still open the Server tab with useful full detail.
func (s *Server) loadServerDashboardInventory() {
	if s.cfg == nil {
		return
	}
	path, err := s.cfg.Storage.Path(serverDashboardInventoryCacheKey)
	if err != nil {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var stored serverDashboardInventoryFile
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Version != serverDashboardInventoryCacheVersion || stored.UpdatedAt.IsZero() {
		return
	}
	s.dashboardInventoryMu.Lock()
	s.dashboardInventory = &serverDashboardInventory{data: stored.Data, updatedAt: stored.UpdatedAt}
	s.dashboardInventoryMu.Unlock()
}

func (s *Server) saveServerDashboardInventory(data components.ServerDashboardData, updatedAt time.Time) {
	if s.cfg == nil {
		return
	}
	path, err := s.cfg.Storage.WritePath(serverDashboardInventoryCacheKey)
	if err != nil {
		return
	}
	raw, err := json.Marshal(serverDashboardInventoryFile{
		Version:   serverDashboardInventoryCacheVersion,
		UpdatedAt: updatedAt,
		Data:      data,
	})
	if err != nil {
		return
	}
	if err := atomicWrite(path, raw, 0o600); err != nil {
		slog.Warn("save server dashboard inventory", "err", err)
	}
}

// serverDashboardStaticData returns the last complete inventory immediately.
// A stale snapshot is refreshed in the background only after the Server tab
// asks for it, so normal page rendering never pays for database-wide scans.
func (s *Server) serverDashboardStaticData() (components.ServerDashboardData, bool) {
	s.dashboardInventoryMu.RLock()
	snapshot := s.dashboardInventory
	stale := snapshot == nil || time.Since(snapshot.updatedAt) >= serverDashboardInventoryRefreshInterval
	s.dashboardInventoryMu.RUnlock()

	if stale {
		s.refreshServerDashboardInventoryAsync()
	}
	if snapshot == nil {
		return components.ServerDashboardData{}, false
	}
	return snapshot.data, true
}

func (s *Server) refreshServerDashboardInventoryAsync() {
	s.dashboardInventoryMu.Lock()
	if s.dashboardInventoryRefreshing || (s.dashboardInventory != nil && time.Since(s.dashboardInventory.updatedAt) < serverDashboardInventoryRefreshInterval) {
		s.dashboardInventoryMu.Unlock()
		return
	}
	s.dashboardInventoryRefreshing = true
	s.dashboardInventoryMu.Unlock()

	go func() {
		data, err := s.serverDashboardStaticDataFresh()
		if err != nil {
			slog.Warn("refresh server dashboard inventory", "err", err)
			s.dashboardInventoryMu.Lock()
			s.dashboardInventoryRefreshing = false
			s.dashboardInventoryMu.Unlock()
			return
		}
		updatedAt := time.Now()
		s.dashboardInventoryMu.Lock()
		s.dashboardInventory = &serverDashboardInventory{data: data, updatedAt: updatedAt}
		s.dashboardInventoryRefreshing = false
		s.dashboardInventoryMu.Unlock()
		s.saveServerDashboardInventory(data, updatedAt)
	}()
}

func (s *Server) serverDashboardStaticDataFresh() (components.ServerDashboardData, error) {
	d := components.ServerDashboardData{}
	stats, err := s.db.GetDashboardStats()
	if err != nil {
		return d, err
	}
	s.populateServerDashboardStats(&d, stats)
	avatarCount, err := s.countReadyAvatars()
	if err != nil {
		return d, err
	}
	d.AvatarCount = avatarCount
	return d, nil
}

func (s *Server) populateServerDashboardStats(d *components.ServerDashboardData, dbStats map[string]any) {
	d.TableCount = toInt(dbStats["table_count"])
	d.DBSizeMB = parseFloatFromAny(dbStats["db_size_mb"])
	d.WALSizeMB = parseFloatFromAny(dbStats["wal_size_mb"])
	d.ChannelsTotal = toInt(dbStats["channels_total"])
	d.VideosTotal = toInt(dbStats["videos_total"])
	d.VideosWatched = toInt(dbStats["videos_watched"])
	d.FeedItemsCount = toInt(dbStats["feed_items_count"])
	d.LocalFeedCount = toInt(dbStats["local_feed_count"])
	d.BookmarksCount = toInt(dbStats["bookmarks_count"])
	d.CommentsCount = toInt(dbStats["comments_count"])
	d.StorageGB = parseFloatFromAny(dbStats["storage_total_gb"])
	d.VideoStorageGB = parseFloatFromAny(dbStats["video_storage_gb"])
	d.AvgMBPerVideo = parseFloatFromAny(dbStats["avg_mb_per_video"])
	if mp, ok := dbStats["media_pipeline"].(map[string]int); ok {
		d.MediaReady = mp["ready"]
		d.MediaQueued = mp["queued"]
		d.MediaFailed = mp["failed"]
	}
	if sh, ok := dbStats["source_health"].(map[string]any); ok {
		d.SourceHealthOK = toInt(sh["ok"])
		d.SourceHealthCooling = toInt(sh["cooling"])
		d.SourceHealthFailed = toInt(sh["failed"])
		d.SourceAvgLatencyMs = toInt(sh["avg_latency_ms"])
		d.SourcesOK = d.SourceHealthOK
		d.SourcesCool = d.SourceHealthCooling
		d.SourcesFail = d.SourceHealthFailed
	}
	if cbp, ok := dbStats["channels_by_platform"].(map[string]int); ok {
		d.ChannelsByPlat = cbp
	}
	if pq, ok := dbStats["preview_queue"].(map[string]int); ok {
		d.PreviewReady = pq["ready"]
		d.PreviewPending = pq["pending"]
		d.PreviewUnsupported = pq["unsupported"]
	}
	if an, ok := dbStats["analytics_summary"].(map[string]int); ok {
		d.AnalyticsTotal = an["total"]
		d.AnalyticsAppStarts = an["app_starts"]
		d.AnalyticsVideoOpens = an["video_opens"]
		d.AnalyticsSyncs = an["syncs"]
	}
	if dq, ok := dbStats["download_queue"].(map[string]int); ok {
		d.DownloadQueuePending = dq["pending"]
		d.DownloadQueueFailed = dq["failed"]
	}
	if sb, ok := dbStats["sponsorblock"].(map[string]int); ok {
		d.SponsorBlockChecked = sb["checked"]
		d.SponsorBlockSegments = sb["segments"]
	}
}

func (s *Server) handleServerStatusJSON(w http.ResponseWriter, r *http.Request) {
	dbStats, _ := s.db.GetDashboardStats()
	avatarCount, _ := s.countReadyAvatars()
	health := s.productHealth(time.Now()).response()
	upSeconds := int(time.Since(serverStartTime).Seconds())
	memMB := getMemoryRSSMB()

	// Accumulate memory history (max 30 samples)
	memoryHistoryMu.Lock()
	memoryHistory = append(memoryHistory, memMB)
	if len(memoryHistory) > 30 {
		memoryHistory = memoryHistory[len(memoryHistory)-30:]
	}
	memHistCopy := make([]float64, len(memoryHistory))
	copy(memHistCopy, memoryHistory)
	memoryHistoryMu.Unlock()

	// Extract db_size_mb, wal_size_mb, table_count from dbStats for top-level exposure
	dbSizeMB := dbStats["db_size_mb"]
	walSizeMB := dbStats["wal_size_mb"]
	tableCount := dbStats["table_count"]

	// Map worker statuses to process-like entries
	workerStatuses := s.workers.Status()
	var processes []map[string]any
	for _, ws := range workerStatuses {
		statusStr := "stopped"
		if ws.Running {
			statusStr = "running"
		}
		processes = append(processes, map[string]any{
			"name":    ws.Name,
			"status":  statusStr,
			"running": ws.Running,
			"summary": ws.Summary,
			"detail":  ws.Detail,
			"error":   ws.Error,
		})
	}
	if processes == nil {
		processes = []map[string]any{}
	}

	writeJSON(w, 200, map[string]any{
		"uptime": map[string]any{
			"seconds":    upSeconds,
			"elapsed":    formatElapsed(upSeconds),
			"started_ms": serverStartTime.UnixMilli(),
		},
		"memory_mb":      fmt.Sprintf("%.1f", memMB),
		"memory_history": memHistCopy,
		"health":         health,
		"workers":        workerStatuses,
		"db_stats":       dbStats,
		"db_size_mb":     dbSizeMB,
		"wal_size_mb":    walSizeMB,
		"table_count":    tableCount,
		"errors_24h":     len(s.workers.Activity().ByStatus("error")),
		"errors_delta":   0,
		"avatar_count":   avatarCount,
		"activity":       reverseActivityEvents(s.workers.Activity().Last(100)),
		"errors":         s.workers.Activity().ByStatus("error"),
		"warnings":       s.workers.Activity().ByStatus("warning"),
		"error_count":    len(s.workers.Activity().ByStatus("error")),
		"warning_count":  len(s.workers.Activity().ByStatus("warning")),
		"processes":      processes,
	})
}

// toInt converts an any value (int, float64, string) to int.
func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

// parseFloatFromAny parses a value to float64 (accepts string or numeric types).
func parseFloatFromAny(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// buildFeedSources gathers source diagnostics for the feed dashboard.
func (s *Server) buildFeedSources() ([]components.FeedSourceEntry, string) {
	channels, err := s.db.GetSubscribedChannels()
	if err != nil {
		return nil, ""
	}

	type sourceChannel struct {
		Handle    string
		ChannelID string
	}
	var sourceChannels []sourceChannel
	handleSet := make(map[string]bool)
	for _, ch := range channels {
		handle := twitterSourceHandle(ch)
		if handle != "" && !handleSet[handle] {
			sourceChannels = append(sourceChannels, sourceChannel{Handle: handle, ChannelID: ch.ChannelID})
			handleSet[handle] = true
		}
	}

	states, _ := s.db.GetAllIngestStates()
	stateMap := make(map[string]struct {
		Status         string
		LastSuccessAt  int64
		LastError      string
		LastHTTPStatus int
	}, len(states))
	for _, st := range states {
		status := "unknown"
		switch {
		case st.FailCount == 0 && st.LastSuccessAt > 0:
			status = "ok"
		case st.FailCount > 0 && st.FailCount <= 3:
			status = "cooling"
		case st.FailCount > 3:
			status = "failing"
		case st.LastSuccessAt > 0:
			status = "degraded"
		}
		key := strings.TrimPrefix(st.Handle, "twitter_")
		stateMap[key] = struct {
			Status         string
			LastSuccessAt  int64
			LastError      string
			LastHTTPStatus int
		}{status, int64(st.LastSuccessAt), st.LastError, st.LastHTTPStatus}
	}

	itemCounts, _ := s.db.CountFeedItemsBySourceChannel()
	twoDaysAgo := time.Now().Unix() - 2*24*3600

	var sources []components.FeedSourceEntry
	for _, source := range sourceChannels {
		handle := source.Handle
		entry := components.FeedSourceEntry{Handle: handle, Status: "unknown", DisplayStatus: "unknown"}
		if st, ok := stateMap[handle]; ok {
			entry.Status = st.Status
			entry.DisplayStatus = st.Status
			entry.LastSuccessAt = st.LastSuccessAt
			entry.LastError = st.LastError
			entry.LastHTTPStatus = st.LastHTTPStatus
		}
		if c, ok := itemCounts[source.ChannelID]; ok {
			entry.ItemCount = c
		}
		// Relax status: if last OK was <2 days ago, consider it OK
		if (entry.Status == "failing" || entry.Status == "degraded") && entry.LastSuccessAt > twoDaysAgo {
			entry.DisplayStatus = "ok"
		}
		if entry.Status == "unknown" {
			entry.DisplayStatus = "pending"
		}
		sources = append(sources, entry)
	}

	// Sort by LastSuccessAt ascending (oldest / never-ok first) so stale
	// sources always rise to the top.
	sort.SliceStable(sources, func(i, j int) bool {
		return sources[i].LastSuccessAt < sources[j].LastSuccessAt
	})

	return sources, ""
}

func formatElapsed(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func getMemoryRSSMB() float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseFloat(fields[1], 64)
				return kb / 1024
			}
		}
	}
	return 0
}

func (s *Server) countReadyAvatars() (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM assets a JOIN media_objects mo ON mo.object_id = a.object_id
		WHERE a.asset_kind = 'avatar'
		  AND a.owner_kind = 'channel'
		  AND a.lifecycle_state = 'active'
		  AND mo.published_revision > 0 AND mo.file_path != ''
	`).Scan(&count)
	return count, err
}
