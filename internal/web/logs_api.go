package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
)

// ── In-memory Android state ───────────────────────────────────────────────────

type androidLogEvent struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Tag       string `json:"tag"`
	Message   string `json:"message"`
}

type androidState struct {
	mu             sync.Mutex
	forceSyncFlag  bool
	fetchRequested bool
	eventBuffer    []androidLogEvent // maxlen 500
	roomQuery      *string
	roomResult     any
	cacheHealth    map[string]any
}

var android = &androidState{}

// ── Route registration ────────────────────────────────────────────────────────

func (s *Server) registerLogsAPIRoutes(mux *http.ServeMux) {
	// Server logs
	mux.HandleFunc("GET /api/logs/server/read", s.handleLogsServer)
	mux.HandleFunc("GET /api/logs/summary", s.handleLogsSummary)
	mux.HandleFunc("POST /api/logs/cleanup", s.handleLogsCleanup)
	mux.HandleFunc("GET /api/logs", s.handleLogsMerged)

	// Analytics
	mux.HandleFunc("POST /api/analytics/events", s.handleAnalyticsEvents)
	mux.HandleFunc("GET /api/analytics/summary", s.handleAnalyticsSummary)

	// Android dashboard
	mux.HandleFunc("POST /api/logs/android", s.handleAndroidLog)
	mux.HandleFunc("POST /api/logs/android/batch", s.handleAndroidBatch)
	mux.HandleFunc("POST /api/logs/android/stats", s.handleAndroidStats)
	mux.HandleFunc("POST /api/logs/android/debug", s.handleAndroidDebugLog)
	mux.HandleFunc("POST /api/logs/android/cache-health", s.handleAndroidCacheHealth)
	mux.HandleFunc("GET /api/logs/android/status", s.handleAndroidStatus)
	mux.HandleFunc("POST /api/logs/android/force-sync", s.handleAndroidForceSync)
	mux.HandleFunc("GET /api/logs/android/force-sync/check", s.handleAndroidForceSyncCheck)
	mux.HandleFunc("POST /api/logs/android/fetch", s.handleAndroidFetch)

	// Room query relay
	mux.HandleFunc("POST /api/logs/android/room-query", s.handleRoomQueryPost)
	mux.HandleFunc("GET /api/logs/android/room-query/check", s.handleRoomQueryCheck)
	mux.HandleFunc("POST /api/logs/android/room-query/result", s.handleRoomQueryResultPost)
	mux.HandleFunc("GET /api/logs/android/room-query/result", s.handleRoomQueryResultGet)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// readLastLines reads up to n lines from the end of a file.
// It reads at most 64KB from the end to avoid loading large files.
func readLastLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	const chunkSize = 64 * 1024
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()

	offset := size - chunkSize
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// If we seeked into the middle, first line may be partial — drop it.
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

var noisePatterns = []string{
	"Transfer-Encoding",
	"PreviewWorker",
	"backfill",
	"preview_debug",
}

func filterNoise(lines []string) []string {
	out := lines[:0]
	for _, l := range lines {
		noisy := false
		for _, p := range noisePatterns {
			if strings.Contains(l, p) {
				noisy = true
				break
			}
		}
		if !noisy {
			out = append(out, l)
		}
	}
	return out
}

// logPathForType resolves a recognized log type to its file path.
func (s *Server) logPathForType(logType string) (string, bool) {
	switch logType {
	case "server", "api", "download", "scheduler", "x_ingest", "error":
		return filepath.Join(s.cfg.DataDir, "logs", "server", logType+".log"), true
	case "android":
		return filepath.Join(s.cfg.DataDir, "logs", "android", "android.log"), true
	case "android-stats":
		return filepath.Join(s.cfg.DataDir, "logs", "android", "stats.jsonl"), true
	default:
		return "", false
	}
}

// knownLogTypes lists all recognized log file names (for summary).
var knownLogTypes = []string{
	"server", "api", "download", "scheduler", "x_ingest", "error",
	"android", "android-stats",
}

// appendToFile opens a file for append (creating dirs if needed) and writes lines.
func appendToFile(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()
	w := bufio.NewWriter(f)
	for _, l := range lines {
		_, _ = w.WriteString(l)
		_ = w.WriteByte('\n')
	}
	return w.Flush()
}

// parseLogLine extracts structured fields from a log line.
// Handles formats like: "2026-04-01 13:44:13,430 [INFO] [android] Android: client_log:FeedSync - DEBUG - message"
var logLineRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}[,.]?\d*)\s+\[(\w+)\]\s+\[(\w+)\]\s+(?:Android:\s+)?(?:client_log:)?(.*)$`)

func parseLogLine(line string) androidLogEvent {
	m := logLineRe.FindStringSubmatch(line)
	if m != nil {
		msg := strings.TrimSpace(m[4])
		tag := m[3]
		level := strings.ToUpper(m[2])
		// Extract inner tag from "FeedSync - DEBUG - actual message"
		if parts := strings.SplitN(msg, " - ", 3); len(parts) >= 3 {
			tag = parts[0]
			level = strings.ToUpper(parts[1])
			msg = parts[2]
		} else if parts := strings.SplitN(msg, " - ", 2); len(parts) == 2 {
			tag = parts[0]
			msg = parts[1]
		}
		return androidLogEvent{
			Timestamp: m[1],
			Level:     level,
			Tag:       tag,
			Message:   msg,
		}
	}
	// Fallback: treat entire line as message
	return androidLogEvent{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Level:     "INFO",
		Tag:       "android",
		Message:   line,
	}
}

// ── Server logs ───────────────────────────────────────────────────────────────

func (s *Server) handleLogsServer(w http.ResponseWriter, r *http.Request) {
	logType := r.URL.Query().Get("type")
	if logType == "" {
		logType = "server"
	}
	nStr := r.URL.Query().Get("lines")
	n, err := strconv.Atoi(nStr)
	if err != nil || n <= 0 {
		n = 100
	}
	filterNoisyParam := r.URL.Query().Get("filter_noise")

	path, ok := s.logPathForType(logType)
	if !ok {
		writeJSON(w, 400, map[string]any{"success": false, "error": "unknown log type"})
		return
	}
	lines, err := readLastLines(path, n)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, 200, map[string]any{"success": true, "content": "", "type": logType})
			return
		}
		slog.Error("readLastLines", "path", path, "err", err)
		writeJSON(w, 500, map[string]any{"error": "could not read log"})
		return
	}

	if filterNoisyParam == "1" {
		lines = filterNoise(lines)
	}
	if lines == nil {
		lines = []string{}
	}

	if r.URL.Query().Get("fmt") == "html" {
		filter := r.URL.Query().Get("raw_filter")
		if filter == "" {
			filter = "all"
		}
		d := components.ServerRawLogData{Filter: filter}
		for _, line := range lines {
			level := ""
			switch {
			case strings.Contains(line, "[ERROR]"):
				level = "ERROR"
			case strings.Contains(line, "[WARNING]"):
				level = "WARNING"
			case strings.Contains(line, "[INFO]"):
				level = "INFO"
			}
			d.Lines = append(d.Lines, components.ServerRawLogLine{Text: line, Level: level})
		}
		w.Header().Set("Content-Type", "text/html")
		_ = components.ServerRawLog(s.pageProps(w, r), d).Render(r.Context(), w)
		return
	}

	writeJSON(w, 200, map[string]any{
		"success": true,
		"content": strings.Join(lines, "\n"),
		"type":    logType,
	})
}

func (s *Server) handleLogsSummary(w http.ResponseWriter, r *http.Request) {
	type fileSummary struct {
		Name       string `json:"name"`
		Exists     bool   `json:"exists"`
		Size       int64  `json:"size"`
		ModifiedMs int64  `json:"modified_ms"`
	}
	var summary []fileSummary
	for _, t := range knownLogTypes {
		var path string
		if t == "android" {
			path = filepath.Join(s.cfg.DataDir, "logs", "android", "android.log")
		} else if t == "android-stats" {
			path = filepath.Join(s.cfg.DataDir, "logs", "android", "stats.jsonl")
		} else {
			path = filepath.Join(s.cfg.DataDir, "logs", "server", t+".log")
		}
		fs := fileSummary{Name: t}
		if fi, err := os.Stat(path); err == nil {
			fs.Exists = true
			fs.Size = fi.Size()
			fs.ModifiedMs = fi.ModTime().UnixMilli()
		}
		summary = append(summary, fs)
	}
	writeJSON(w, 200, map[string]any{"files": summary})
}

func (s *Server) handleLogsCleanup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Days int `json:"days"`
	}
	body.Days = 30
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Days <= 0 {
		body.Days = 30
	}

	cutoff := time.Now().Add(-time.Duration(body.Days) * 24 * time.Hour)
	logsDir := filepath.Join(s.cfg.DataDir, "logs")

	var deleted int
	var freedBytes int64

	err := filepath.Walk(logsDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if fi.ModTime().Before(cutoff) {
			freedBytes += fi.Size()
			if rmErr := os.Remove(path); rmErr == nil {
				deleted++
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		slog.Error("logs cleanup walk", "err", err)
	}

	writeJSON(w, 200, map[string]any{
		"deleted":     deleted,
		"freed_bytes": freedBytes,
	})
}

func (s *Server) handleLogsMerged(w http.ResponseWriter, r *http.Request) {
	const perFile = 200
	paths := []string{
		filepath.Join(s.cfg.DataDir, "logs", "server", "server.log"),
		filepath.Join(s.cfg.DataDir, "logs", "server", "download.log"),
	}

	var all []string
	for _, p := range paths {
		lines, err := readLastLines(p, perFile)
		if err == nil {
			all = append(all, lines...)
		}
	}

	// Sort descending (ISO-prefixed lines sort lexicographically).
	sort.Slice(all, func(i, j int) bool { return all[i] > all[j] })

	if all == nil {
		all = []string{}
	}
	writeJSON(w, 200, map[string]any{"lines": all, "count": len(all)})
}

// ── Analytics ─────────────────────────────────────────────────────────────────

func (s *Server) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []db.AnalyticsEvent `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}
	added, err := s.db.AddAnalyticsEvents(body.Events)
	if err != nil {
		slog.Error("AddAnalyticsEvents", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "added": added})
}

func (s *Server) handleAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	rollups, err := s.db.GetAnalyticsRollups(200)
	if err != nil {
		slog.Error("GetAnalyticsRollups", "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	events, err := s.db.GetAnalyticsRecentEvents(50)
	if err != nil {
		slog.Error("GetAnalyticsRecentEvents", "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	if rollups == nil {
		rollups = []db.AnalyticsRollup{}
	}
	if events == nil {
		events = []db.AnalyticsEvent{}
	}
	writeJSON(w, 200, map[string]any{
		"rollups":       rollups,
		"recent_events": events,
	})
}

// ── Android dashboard ─────────────────────────────────────────────────────────

var allowedAndroidTags = map[string]bool{
	"FeedSync": true, "FeedRepo": true, "FeedVM": true,
	"ChannelFeedVM": true, "VideoRepo": true, "VideosVM": true,
	"MediaCache": true, "AuthVM": true, "AuthRepo": true,
	"CrashReport": true, "StatsLogger": true,
}

func hasAllowedTag(line string) bool {
	for tag := range allowedAndroidTags {
		if strings.Contains(line, tag) {
			return true
		}
	}
	return false
}

func (s *Server) handleAndroidLog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Line string `json:"line"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}
	line := strings.TrimSpace(body.Line)
	if line == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "empty line"})
		return
	}

	path := filepath.Join(s.cfg.DataDir, "logs", "android", "android.log")
	_ = appendToFile(path, []string{line})

	evt := parseLogLine(line)
	android.mu.Lock()
	android.eventBuffer = append(android.eventBuffer, evt)
	if len(android.eventBuffer) > 500 {
		android.eventBuffer = android.eventBuffer[len(android.eventBuffer)-500:]
	}
	android.mu.Unlock()

	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleAndroidBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID       string            `json:"device_id"`
		DeviceInfo     string            `json:"device_info"`
		AndroidVersion string            `json:"android_version"`
		Logs           []androidLogEvent `json:"logs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}

	// Filter by allowed tags, cap at 200.
	var filtered []androidLogEvent
	var lines []string
	for _, e := range body.Logs {
		if hasAllowedTag(e.Tag) || hasAllowedTag(e.Message) {
			filtered = append(filtered, e)
			lines = append(lines, fmt.Sprintf("%s [%s] [%s] %s", e.Timestamp, e.Level, e.Tag, e.Message))
		}
		if len(filtered) >= 200 {
			break
		}
	}

	if len(lines) > 0 {
		path := filepath.Join(s.cfg.DataDir, "logs", "android", "android.log")
		_ = appendToFile(path, lines)

		android.mu.Lock()
		android.eventBuffer = append(android.eventBuffer, filtered...)
		if len(android.eventBuffer) > 500 {
			android.eventBuffer = android.eventBuffer[len(android.eventBuffer)-500:]
		}
		android.mu.Unlock()

		// Emit highlight to main activity ring when a sync cycle completes
		for _, e := range filtered {
			if e.Tag == "FeedSync" && strings.HasPrefix(e.Message, "=== Sync complete") {
				s.workers.Emit("android", "Android sync complete", "done")
				break
			}
		}
	}

	writeJSON(w, 200, map[string]any{"success": true, "accepted": len(filtered)})
}

func (s *Server) handleAndroidStats(w http.ResponseWriter, r *http.Request) {
	// Android sends {"device_id":"...","lines":["json_event_str",...]}
	var body struct {
		DeviceID string   `json:"device_id"`
		Lines    []string `json:"lines"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}

	var lines []string
	for _, l := range body.Lines {
		l = strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", "").Replace(l))
		if l != "" {
			lines = append(lines, l)
		}
		if len(lines) >= 500 {
			break
		}
	}

	if len(lines) == 0 {
		writeJSON(w, 200, map[string]any{"success": true, "accepted": 0})
		return
	}

	statsPath := filepath.Join(s.cfg.DataDir, "logs", "android", "stats.jsonl")
	_ = os.MkdirAll(filepath.Dir(statsPath), 0o755)

	// Rotate if over 10MB before appending.
	if fi, err := os.Stat(statsPath); err == nil && fi.Size() > 10*1024*1024 {
		_ = os.Rename(statsPath, statsPath+".1")
	}
	_ = appendToFile(statsPath, lines)

	writeJSON(w, 200, map[string]any{"success": true, "accepted": len(lines)})
}

func (s *Server) handleAndroidDebugLog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Lines []string `json:"lines"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Lines) == 0 {
		writeJSON(w, 400, map[string]any{"success": false, "error": "lines required"})
		return
	}

	debugPath := filepath.Join(s.cfg.DataDir, "logs", "android", "debug.log")
	_ = os.MkdirAll(filepath.Dir(debugPath), 0o755)

	if fi, err := os.Stat(debugPath); err == nil && fi.Size() > 10*1024*1024 {
		_ = os.Rename(debugPath, debugPath+".1")
	}

	_ = appendToFile(debugPath, body.Lines)

	writeJSON(w, 200, map[string]any{"success": true, "accepted": len(body.Lines)})
}

func (s *Server) handleAndroidCacheHealth(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "bad json"})
		return
	}

	// New Android reports verified-present counts under "counts" plus the
	// retention settings that shaped the local cache. Preserve legacy flat
	// category payloads as fallback, but intentionally drop thumbnails: the web
	// modal no longer treats generated thumbnails as a cache-health category.
	health := make(map[string]any, len(body))
	for k, v := range body {
		if k == "thumbnails" {
			continue
		}
		health[k] = v
	}
	if counts, ok := health["counts"].(map[string]any); ok {
		delete(counts, "thumbnails")
	}

	android.mu.Lock()
	android.cacheHealth = health
	android.mu.Unlock()

	p := filepath.Join(s.cfg.DataDir, "logs", "android", "cache_health.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if data, err := json.Marshal(health); err == nil {
		_ = os.WriteFile(p, data, 0o644)
	}

	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) updateAndroidCacheHealthRetention(retention map[string]any, reportedAtMs int64) {
	if len(retention) == 0 {
		return
	}
	health := loadCacheHealthFromDisk(s.cfg.DataDir)
	if health == nil {
		health = map[string]any{}
	}
	health["generated_at_ms"] = reportedAtMs
	health["retention"] = retention

	android.mu.Lock()
	android.cacheHealth = health
	android.mu.Unlock()

	p := filepath.Join(s.cfg.DataDir, "logs", "android", "cache_health.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if data, err := json.Marshal(health); err == nil {
		_ = os.WriteFile(p, data, 0o644)
	}
}

func loadCacheHealthFromDisk(dataDir string) map[string]any {
	p := filepath.Join(dataDir, "logs", "android", "cache_health.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var health map[string]any
	if json.Unmarshal(data, &health) != nil {
		return nil
	}
	return health
}

// ── Android sync helpers ──────────────────────────────────────────────────────

// Mirrors the step types actually registered in
// android/app/src/main/java/com/screwy/igloo/sync/SyncRegistry.kt —
// must match exactly so the dashboard's N/total never drifts. Update both
// when Android adds/removes a step.
var androidSyncSteps = []string{
	"purge_deleted", "outbox_drain", "feed_sync", "feed_changes",
	"video_metadata", "channel_metadata", "bookmarks_aliases",
	"ranked_feed", "youtube_videos", "youtube_comments",
	"manifest_subscriptions", "manifest_liked", "manifest_bookmarked",
	"cache_health_pre", "cache_health", "shorts_media", "feed_media",
	"subtitles", "avatars", "sponsorblock", "preview_sprites",
	"prune", "stats_upload",
}

var skipOnLowMemory = map[string]bool{
	"youtube_videos": true, "shorts_media": true,
	"feed_media": true, "subtitles": true,
	"avatars": true, "sponsorblock": true,
}

func formatAgo(seconds float64) string {
	s := int(seconds)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds ago", s)
	case s < 3600:
		return fmt.Sprintf("%dm ago", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh %dm ago", s/3600, (s%3600)/60)
	default:
		return fmt.Sprintf("%dd ago", s/86400)
	}
}

// parseAndroidTimestamp handles "2026-04-01 13:44:13", "2026-04-01 13:44:13,430", and RFC3339.
func parseAndroidTimestamp(ts string) (time.Time, error) {
	clean := ts
	if i := strings.IndexByte(clean, ','); i > 10 {
		clean = clean[:i]
	} else if i := strings.IndexByte(clean, '.'); i > 10 {
		clean = clean[:i]
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", clean, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, strings.Replace(ts, "Z", "+00:00", 1)); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp: %q", ts)
}

type syncStepEntry struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs any    `json:"duration_ms"`
}

type androidClientLogEntry struct {
	TimestampMs   int64             `json:"timestamp_ms"`
	ReceivedAtMs  int64             `json:"received_at_ms"`
	Level         string            `json:"level"`
	Event         string            `json:"event"`
	Fields        map[string]string `json:"fields"`
	RawFields     map[string]any    `json:"-"`
	TimestampTime time.Time         `json:"-"`
}

type androidSyncSummary struct {
	Started     time.Time
	Completed   time.Time
	CompletedAt bool
	Steps       []syncStepEntry
	Footer      string
	Available   bool
}

func parseSyncCycle(buf []androidLogEvent) (map[string]any, []syncStepEntry) {
	steps := make([]syncStepEntry, len(androidSyncSteps))
	for i, name := range androidSyncSteps {
		steps[i] = syncStepEntry{Name: name, Status: "pending", DurationMs: nil}
	}

	// Find all "=== Sync started" indices. The most recent completed cycle is
	// either the one before the last start (if >1 start), or the last start
	// if its last FeedSync event is >=2 minutes old.
	var starts []int
	for i, e := range buf {
		if e.Tag == "FeedSync" && strings.HasPrefix(e.Message, "=== Sync started") {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return nil, steps
	}

	var syncStart, syncEnd int
	if len(starts) >= 2 {
		// Previous cycle ended at the last FeedSync event before the final start.
		syncStart = starts[len(starts)-2]
		for i := starts[len(starts)-1] - 1; i > syncStart; i-- {
			if buf[i].Tag == "FeedSync" {
				syncEnd = i
				break
			}
		}
		if syncEnd == 0 {
			syncEnd = starts[len(starts)-1] - 1
		}
	} else {
		// Only one start: treat as complete if last FeedSync event is >=2min old.
		syncStart = starts[0]
		for i := len(buf) - 1; i > syncStart; i-- {
			if buf[i].Tag == "FeedSync" {
				syncEnd = i
				break
			}
		}
		if syncEnd == 0 {
			return nil, steps
		}
		if endT, err := parseAndroidTimestamp(buf[syncEnd].Timestamp); err == nil {
			if time.Since(endT) < 2*time.Minute {
				return nil, steps
			}
		}
	}

	stepIndex := make(map[string]int, len(androidSyncSteps))
	for i, name := range androidSyncSteps {
		stepIndex[name] = i
	}
	foundSteps := make(map[string]bool)
	for _, e := range buf[syncStart+1 : syncEnd+1] {
		msg := e.Message
		for _, name := range androidSyncSteps {
			if strings.HasPrefix(msg, name+": done") || strings.HasPrefix(msg, name+" done") {
				steps[stepIndex[name]].Status = "done"
				foundSteps[name] = true
				break
			} else if strings.HasPrefix(msg, name+" failed") || strings.HasPrefix(msg, name+": failed") {
				steps[stepIndex[name]].Status = "failed"
				foundSteps[name] = true
				break
			}
		}
	}
	for i, s := range steps {
		if !foundSteps[s.Name] && s.Status == "pending" && skipOnLowMemory[s.Name] {
			steps[i].Status = "skipped"
		}
	}

	return map[string]any{
		"started":   buf[syncStart].Timestamp,
		"completed": buf[syncEnd].Timestamp,
	}, steps
}

// parseAndroidFeedItemCounts scans for the latest [FeedVM] loadInitial line
// to extract subscription feed item count.
func parseAndroidFeedItemCounts(buf []androidLogEvent) (total int) {
	for i := len(buf) - 1; i >= 0; i-- {
		e := buf[i]
		if e.Tag == "FeedVM" && strings.HasPrefix(e.Message, "loadInitial scope=subscriptions") {
			// Extract count=NNN
			if idx := strings.Index(e.Message, "count="); idx >= 0 {
				rest := e.Message[idx+6:]
				for j := 0; j < len(rest); j++ {
					if rest[j] < '0' || rest[j] > '9' {
						rest = rest[:j]
						break
					}
				}
				if n, err := strconv.Atoi(rest); err == nil {
					return n
				}
			}
			return 0
		}
	}
	return 0
}

func readAndroidClientLogEntries(dataDir string, limit int) []androidClientLogEntry {
	path := filepath.Join(dataDir, "logs", "android", "server.log")
	lines, err := readLastLines(path, limit)
	if err != nil {
		return nil
	}
	entries := make([]androidClientLogEntry, 0, len(lines))
	for _, line := range lines {
		var raw struct {
			TimestampMs  int64          `json:"timestamp_ms"`
			ReceivedAtMs int64          `json:"received_at_ms"`
			Level        string         `json:"level"`
			Event        string         `json:"event"`
			Fields       map[string]any `json:"fields"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil || raw.Event == "" {
			continue
		}
		tsMs := raw.TimestampMs
		if tsMs <= 0 {
			tsMs = raw.ReceivedAtMs
		}
		fields := make(map[string]string, len(raw.Fields))
		for k, v := range raw.Fields {
			fields[k] = fmt.Sprintf("%v", v)
		}
		entries = append(entries, androidClientLogEntry{
			TimestampMs:   raw.TimestampMs,
			ReceivedAtMs:  raw.ReceivedAtMs,
			Level:         strings.ToLower(raw.Level),
			Event:         raw.Event,
			Fields:        fields,
			RawFields:     raw.Fields,
			TimestampTime: time.UnixMilli(tsMs),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TimestampTime.Before(entries[j].TimestampTime)
	})
	return entries
}

func structuredEventsToLogEvents(entries []androidClientLogEntry) []androidLogEvent {
	out := make([]androidLogEvent, 0, len(entries))
	for _, e := range entries {
		level := strings.ToUpper(e.Level)
		if level == "" {
			level = "INFO"
		}
		if level == "INFO" && androidEventLooksWarning(e.Event) {
			level = "WARN"
		}
		if level == "INFO" && androidEventLooksError(e.Event) {
			level = "ERROR"
		}
		out = append(out, androidLogEvent{
			Timestamp: e.TimestampTime.Format(time.RFC3339),
			Level:     level,
			Tag:       e.Event,
			Message:   androidClientLogMessage(e),
		})
	}
	return out
}

func androidClientLogMessage(e androidClientLogEntry) string {
	if len(e.Fields) == 0 {
		return e.Event
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, min(len(keys), 5))
	for _, k := range keys {
		if len(parts) >= 5 {
			break
		}
		v := e.Fields[k]
		if len(v) > 90 {
			v = v[:87] + "..."
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

func androidEventLooksError(event string) bool {
	event = strings.ToLower(event)
	return strings.Contains(event, "unhandled") ||
		strings.Contains(event, "all_parses_failed") ||
		strings.HasSuffix(event, "_error")
}

func androidEventLooksWarning(event string) bool {
	event = strings.ToLower(event)
	return strings.Contains(event, "failed") ||
		strings.Contains(event, "exception") ||
		strings.Contains(event, "stalled") ||
		strings.Contains(event, "skipped_offline") ||
		strings.Contains(event, "aborted_offline")
}

func parseStructuredSync(entries []androidClientLogEntry, cacheHealth map[string]any, preferredGenerationID string, generationReady bool, healthReportedAtMs int64) androidSyncSummary {
	if summary := parseAndroidMirrorSync(entries, preferredGenerationID, generationReady, healthReportedAtMs); summary.Available {
		return summary
	}
	return parseInboundStructuredSync(entries, cacheHealth)
}

func parseAndroidMirrorSync(entries []androidClientLogEntry, preferredGenerationID string, generationReady bool, healthReportedAtMs int64) androidSyncSummary {
	names := []string{"generation", "asset_manifest", "item_import", "asset_drain", "health_report", "cleanup", "sync_complete"}
	steps := make([]syncStepEntry, len(names))
	for i, name := range names {
		steps[i] = syncStepEntry{Name: name, Status: "pending"}
	}
	stepIndex := make(map[string]int, len(steps))
	for i, step := range steps {
		stepIndex[step.Name] = i
	}
	mark := func(name, status string) {
		if idx, ok := stepIndex[name]; ok {
			if steps[idx].Status == "failed" && status == "done" {
				return
			}
			steps[idx].Status = status
		}
	}
	duration := func(name string, ms int64) {
		if ms <= 0 {
			return
		}
		if idx, ok := stepIndex[name]; ok {
			steps[idx].DurationMs = ms
		}
	}

	targetID := preferredGenerationID
	if targetID == "" {
		for i := len(entries) - 1; i >= 0; i-- {
			if id := entries[i].Fields["generation_id"]; id != "" && strings.HasPrefix(entries[i].Event, "android_sync_") {
				targetID = id
				break
			}
		}
	}
	if targetID == "" && !generationReady && healthReportedAtMs <= 0 {
		return androidSyncSummary{Steps: steps}
	}

	start := time.Time{}
	completed := time.Time{}
	available := generationReady || healthReportedAtMs > 0 || targetID != ""
	if generationReady {
		mark("generation", "done")
	}
	if healthReportedAtMs > 0 {
		mark("health_report", "done")
		completed = time.UnixMilli(healthReportedAtMs)
	}

	for _, e := range entries {
		if !androidMirrorEventMatchesGeneration(e, targetID) {
			continue
		}
		switch e.Event {
		case "android_sync_generation_request":
			if targetID == "" && start.IsZero() {
				start = e.TimestampTime
			}
		case "android_sync_generation_start":
			available = true
			mark("generation", "done")
			if start.IsZero() {
				start = e.TimestampTime
			}
		case "android_sync_assets_imported", "android_sync_assets_import_skipped":
			available = true
			mark("asset_manifest", "done")
		case "android_sync_assets_marker_stalled":
			available = true
			mark("asset_manifest", "failed")
		case "android_sync_items_imported", "android_sync_items_import_skipped":
			available = true
			mark("item_import", "done")
		case "android_sync_items_marker_stalled":
			available = true
			mark("item_import", "failed")
		case "android_sync_asset_drain_done":
			available = true
			mark("asset_drain", "done")
		case "android_sync_health_reported":
			available = true
			if e.Fields["uploaded"] == "false" {
				mark("health_report", "failed")
			} else {
				mark("health_report", "done")
				if completed.IsZero() || e.TimestampTime.After(completed) {
					completed = e.TimestampTime
				}
			}
		case "android_sync_content_pruned", "android_sync_orphan_asset_files_pruned", "android_sync_generations_pruned":
			available = true
			mark("cleanup", "done")
		case "android_sync_generation_done":
			available = true
			mark("generation", "done")
			mark("cleanup", "done")
			mark("sync_complete", "done")
			duration("sync_complete", anyInt64(e.RawFields["duration_ms"]))
			if start.IsZero() {
				start = e.TimestampTime
			}
			if completed.IsZero() || e.TimestampTime.After(completed) {
				completed = e.TimestampTime
			}
		case "android_sync_unhandled":
			available = true
			mark("sync_complete", "failed")
		case "periodic_sync_drain_done":
			available = true
			if e.Fields["completed"] == "true" {
				mark("sync_complete", "done")
				if completed.IsZero() || e.TimestampTime.After(completed) {
					completed = e.TimestampTime
				}
			}
			duration("sync_complete", anyInt64(e.RawFields["elapsed_ms"]))
		}
	}

	summary := androidSyncSummary{Started: start, Steps: steps, Available: available}
	if !completed.IsZero() {
		summary.Completed = completed
		summary.CompletedAt = true
		if !start.IsZero() {
			summary.Footer = "Sync started " + start.Local().Format("15:04:05") + " · completed " + completed.Local().Format("15:04:05") + " · " + formatDuration(completed.Sub(start)) + " total"
		} else if targetID != "" {
			summary.Footer = "Latest health report " + completed.Local().Format("15:04:05") + " · " + shortAndroidGenerationID(targetID)
		}
	} else if targetID != "" {
		summary.Footer = "Latest generation " + shortAndroidGenerationID(targetID)
	}
	return summary
}

func androidMirrorEventMatchesGeneration(e androidClientLogEntry, generationID string) bool {
	if !strings.HasPrefix(e.Event, "android_sync_") && e.Event != "periodic_sync_drain_done" {
		return false
	}
	eventGenerationID := e.Fields["generation_id"]
	if generationID == "" {
		return true
	}
	return eventGenerationID == "" || eventGenerationID == generationID
}

func shortAndroidGenerationID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	const prefix = "android-sync-"
	if rest, ok := strings.CutPrefix(id, prefix); ok && len(rest) > 12 {
		return prefix + rest[:12]
	}
	if len(id) > 24 {
		return id[:24]
	}
	return id
}

func parseInboundStructuredSync(entries []androidClientLogEntry, cacheHealth map[string]any) androidSyncSummary {
	names := []string{"mutation_delta", "inbound_pass", "channels", "feed", "shorts", "youtube_videos", "media", "cache_health"}
	steps := make([]syncStepEntry, len(names))
	for i, name := range names {
		steps[i] = syncStepEntry{Name: name, Status: "pending"}
	}
	stepIndex := make(map[string]int, len(steps))
	for i, step := range steps {
		stepIndex[step.Name] = i
	}
	mark := func(name, status string) {
		if idx, ok := stepIndex[name]; ok {
			steps[idx].Status = status
		}
	}

	startIdx := -1
	doneIdx := -1
	for i, e := range entries {
		switch e.Event {
		case "inbound_pass_start":
			startIdx = i
			doneIdx = -1
		case "inbound_pass_done":
			if startIdx >= 0 {
				doneIdx = i
			}
		}
	}
	if startIdx < 0 {
		return androidSyncSummary{Steps: steps}
	}

	start := entries[startIdx].TimestampTime
	completed := time.Time{}
	if doneIdx >= startIdx {
		completed = entries[doneIdx].TimestampTime
	}
	endIdx := len(entries) - 1
	if doneIdx >= startIdx {
		endIdx = doneIdx
	}

	for _, e := range entries[startIdx : endIdx+1] {
		switch e.Event {
		case "mutation_delta_page_applied":
			mark("mutation_delta", "done")
		case "inbound_pass_start":
			mark("inbound_pass", "done")
		case "inbound_pass_done":
			mark("inbound_pass", "done")
		case "stream_page_applied":
			stream := e.Fields["stream"]
			if e.Fields["end_of_stream"] == "true" {
				mark(stream, "done")
			}
		case "stream_fetch_response_error", "stream_fetch_exception", "stream_marker_stalled":
			mark(e.Fields["stream"], "failed")
		case "media_reconciler_batch_done":
			mark("media", "done")
		case "manifest_sync_scope_failed", "manifest_sync_marker_stalled":
			mark("media", "failed")
		case "android_cache_health_reported":
			mark("cache_health", "done")
			if completed.IsZero() || e.TimestampTime.After(completed) {
				completed = e.TimestampTime
			}
		}
	}
	if t := cacheHealthGeneratedAt(cacheHealth); !t.IsZero() && t.After(start) {
		mark("cache_health", "done")
		if completed.IsZero() || t.After(completed) {
			completed = t
		}
	}

	summary := androidSyncSummary{Started: start, Steps: steps, Available: true}
	if !completed.IsZero() {
		summary.Completed = completed
		summary.CompletedAt = true
		summary.Footer = "Sync started " + start.Local().Format("15:04:05") + " · completed " + completed.Local().Format("15:04:05") + " · " + formatDuration(completed.Sub(start)) + " total"
	}
	return summary
}

func cacheHealthGeneratedAt(health map[string]any) time.Time {
	if health == nil {
		return time.Time{}
	}
	if ms := anyInt64(health["generated_at_ms"]); ms > 0 {
		return time.UnixMilli(ms)
	}
	return time.Time{}
}

func cacheHealthSettings(health map[string]any) db.AndroidRetentionSettings {
	settings := db.AndroidRetentionSettings{FeedDays: 7, YoutubeDays: 7, MomentsDays: 7, StoryHours: 48}
	if health == nil {
		return settings
	}
	ret, _ := health["retention"].(map[string]any)
	if v, ok := ret["feed_days"]; ok {
		settings.FeedDays = max(0, anyInt(v))
	}
	if v, ok := ret["youtube_days"]; ok {
		settings.YoutubeDays = max(0, anyInt(v))
	}
	if v, ok := ret["moments_days"]; ok {
		settings.MomentsDays = max(0, anyInt(v))
	}
	if v, ok := ret["story_hours"]; ok {
		settings.StoryHours = db.NormalizeStoriesWindowHours(anyInt(v))
	}
	return settings
}

func cacheHealthReportedCounts(health map[string]any) map[string]int {
	out := map[string]int{}
	if health == nil {
		return out
	}
	if counts, ok := health["counts"].(map[string]any); ok {
		for _, k := range []string{"videos", "moments", "feed", "avatars"} {
			out[k] = anyInt(counts[k])
		}
		return out
	}
	for _, k := range []string{"videos", "moments", "feed", "avatars"} {
		val, ok := health[k]
		if !ok {
			continue
		}
		if arr, ok := val.([]int); ok && len(arr) >= 1 {
			out[k] = arr[0]
			continue
		}
		if arr, ok := val.([]any); ok && len(arr) >= 1 {
			out[k] = anyInt(arr[0])
		}
	}
	return out
}

func anyInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

func anyInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d / time.Minute)
	seconds := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}

func androidSyncGenerationDuration(entries []androidClientLogEntry, generationID string) time.Duration {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Event != "android_sync_generation_done" {
			continue
		}
		if generationID != "" && e.Fields["generation_id"] != generationID {
			continue
		}
		if ms := anyInt64(e.RawFields["duration_ms"]); ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 0
}

func androidRetentionSettingsFromGeneration(raw map[string]int) (db.AndroidRetentionSettings, bool) {
	if raw == nil {
		return db.AndroidRetentionSettings{}, false
	}
	storyHours := 48
	if v, ok := raw["story_hours"]; ok {
		storyHours = db.NormalizeStoriesWindowHours(v)
	}
	return db.AndroidRetentionSettings{
		FeedDays:    max(0, raw["feed_days"]),
		YoutubeDays: max(0, raw["youtube_days"]),
		MomentsDays: max(0, raw["moments_days"]),
		StoryHours:  storyHours,
	}, true
}

func androidGenerationFeedAssetCount(counts map[string]int) int {
	return counts["post_media"] + counts["post_thumbnail"]
}

func androidPercent(part, total int) int {
	if total <= 0 || part <= 0 {
		return 0
	}
	pct := part * 100 / total
	if pct > 100 {
		return 100
	}
	return pct
}

func formatAndroidBytes(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	case bytes > 0:
		return fmt.Sprintf("%d B", bytes)
	default:
		return ""
	}
}

func androidAssetHealthRows(report *db.AndroidSyncHealthReport, hasGeneration bool, readyAssets, totalAssets int) []components.AndroidCacheRow {
	if report == nil {
		if !hasGeneration || totalAssets <= 0 {
			return nil
		}
		return []components.AndroidCacheRow{
			androidHealthRow("Server ready", readyAssets, totalAssets, "an-cache-bar-good"),
			androidHealthRow("Server missing", max(0, totalAssets-readyAssets), totalAssets, "an-cache-bar-bad"),
		}
	}
	total := report.TotalAssets
	if total <= 0 {
		return nil
	}
	return []components.AndroidCacheRow{
		androidHealthRow("Verified", report.VerifiedAssets, total, "an-cache-bar-good"),
		androidHealthRow("Pending", report.PendingAssets, total, "an-cache-bar-ok"),
		androidHealthRow("Failed", report.FailedAssets, total, "an-cache-bar-bad"),
		androidHealthRow("Server missing", report.MissingAssets, total, "an-cache-bar-bad"),
	}
}

func androidHealthRow(label string, count, total int, barCSS string) components.AndroidCacheRow {
	return components.AndroidCacheRow{
		Label:   label,
		Cached:  count,
		Total:   total,
		Percent: androidPercent(count, total),
		BarCSS:  barCSS,
	}
}

func (s *Server) handleAndroidStatus(w http.ResponseWriter, r *http.Request) {
	android.mu.Lock()
	buf := make([]androidLogEvent, len(android.eventBuffer))
	copy(buf, android.eventBuffer)
	roomResult := android.roomResult
	pending := android.forceSyncFlag
	android.mu.Unlock()

	// Bootstrap from log file if buffer is empty
	if len(buf) == 0 {
		path := filepath.Join(s.cfg.DataDir, "logs", "android", "android.log")
		if lines, err := readLastLines(path, 200); err == nil {
			for _, line := range lines {
				buf = append(buf, parseLogLine(line))
			}
		}
	}

	android.mu.Lock()
	cacheHealth := android.cacheHealth
	if cacheHealth == nil {
		cacheHealth = loadCacheHealthFromDisk(s.cfg.DataDir)
		android.cacheHealth = cacheHealth
	}
	android.mu.Unlock()

	clientEntries := readAndroidClientLogEntries(s.cfg.DataDir, 500)
	if len(clientEntries) > 0 {
		buf = structuredEventsToLogEvents(clientEntries)
	}

	latestGeneration, genErr := s.db.GetLatestAndroidSyncGeneration()
	if genErr != nil {
		slog.Warn("android dashboard latest generation failed", "err", genErr)
	}
	latestHealth, healthErr := s.db.GetLatestAndroidSyncHealthReport()
	if healthErr != nil {
		slog.Warn("android dashboard latest health failed", "err", healthErr)
	}
	latestGenerationID := ""
	if latestGeneration != nil {
		latestGenerationID = latestGeneration.GenerationID
	}
	if latestHealth != nil && latestHealth.GenerationID != "" {
		latestGenerationID = latestHealth.GenerationID
	}
	healthReportedAtMs := int64(0)
	if latestHealth != nil {
		healthReportedAtMs = latestHealth.ReportedAtMs
	}

	lastSync, syncSteps := parseSyncCycle(buf)
	structuredSync := parseStructuredSync(clientEntries, cacheHealth, latestGenerationID, latestGeneration != nil, healthReportedAtMs)
	if structuredSync.Available {
		syncSteps = structuredSync.Steps
	}
	if !structuredSync.Started.IsZero() {
		lastSync = map[string]any{
			"started": structuredSync.Started.Format(time.RFC3339),
		}
		if structuredSync.CompletedAt {
			lastSync["completed"] = structuredSync.Completed.Format(time.RFC3339)
			lastSync["ago"] = formatAgo(time.Since(structuredSync.Completed).Seconds())
			lastSync["elapsed"] = formatDuration(structuredSync.Completed.Sub(structuredSync.Started))
		}
	}

	// Compute elapsed/ago for lastSync
	if lastSync != nil {
		if completedStr, ok := lastSync["completed"].(string); ok && completedStr != "" {
			if completedT, err := parseAndroidTimestamp(completedStr); err == nil {
				if startedStr, ok := lastSync["started"].(string); ok && startedStr != "" {
					if startedT, err := parseAndroidTimestamp(startedStr); err == nil {
						lastSync["elapsed"] = fmt.Sprintf("%.1fs", completedT.Sub(startedT).Seconds())
					}
				}
				lastSync["ago"] = formatAgo(time.Since(completedT).Seconds())
			}
		}
		if _, ok := lastSync["elapsed"]; !ok {
			lastSync["elapsed"] = "?"
		}
		if _, ok := lastSync["ago"]; !ok {
			lastSync["ago"] = "?"
		}
	}
	if latestHealth != nil {
		completed := time.UnixMilli(latestHealth.ReportedAtMs)
		lastSync = map[string]any{
			"completed": completed.Format(time.RFC3339),
			"ago":       formatAgo(time.Since(completed).Seconds()),
		}
		if structuredSync.CompletedAt && !structuredSync.Started.IsZero() {
			lastSync["started"] = structuredSync.Started.Format(time.RFC3339)
			lastSync["elapsed"] = formatDuration(structuredSync.Completed.Sub(structuredSync.Started))
		} else if duration := androidSyncGenerationDuration(clientEntries, latestHealth.GenerationID); duration > 0 {
			lastSync["elapsed"] = formatDuration(duration)
		}
	} else if lastSync == nil && latestGeneration != nil {
		generated := time.UnixMilli(latestGeneration.CreatedAtMs)
		lastSync = map[string]any{
			"completed": generated.Format(time.RFC3339),
			"ago":       "generation ready",
		}
	}

	user := userFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}
	retention := cacheHealthSettings(cacheHealth)
	if latestGeneration != nil {
		if genRetention, ok := androidRetentionSettingsFromGeneration(latestGeneration.Retention); ok {
			retention = genRetention
		}
	}
	if latestHealth != nil && latestHealth.HasRetention {
		retention = latestHealth.Retention
	}
	var expectations db.AndroidDashboardExpectations
	var expErr error
	if latestGeneration == nil {
		expectations, expErr = s.db.GetAndroidDashboardExpectations(username, retention, time.Now().UnixMilli())
		if expErr != nil {
			slog.Warn("android dashboard expectations failed", "err", expErr)
		}
	}
	feedItemsTotal := 0
	feedItemsMedia := 0
	if latestGeneration != nil {
		feedItemsTotal = latestGeneration.ContentCounts["feed_items"]
		feedItemsMedia = androidGenerationFeedAssetCount(latestGeneration.AssetCounts)
	} else {
		feedItemsTotal = expectations.FeedItems
		feedItemsMedia = expectations.FeedMedia
		if feedItemsTotal == 0 {
			feedItemsTotal = parseAndroidFeedItemCounts(buf)
		}
	}
	feedItems := map[string]any{"total": feedItemsTotal, "with_media": feedItemsMedia}

	stepsCompleted := 0
	for _, step := range syncSteps {
		if step.Status == "done" {
			stepsCompleted++
		}
	}

	// Activity: newest first, cap 50
	activityCap := min(50, len(buf))
	activity := make([]androidLogEvent, activityCap)
	for i := range activityCap {
		activity[i] = buf[len(buf)-1-i]
	}

	// Errors: deduplicated; warnings: up to 20
	type errorEntry struct {
		Tag       string `json:"tag"`
		Message   string `json:"message"`
		Timestamp string `json:"timestamp"`
		FirstSeen string `json:"first_seen"`
		Count     int    `json:"count"`
	}
	type errKey struct{ tag, msg string }
	var errors []errorEntry
	errorKeys := make(map[errKey]int)
	var warnings []map[string]any
	warningCount := 0

	for i := len(buf) - 1; i >= 0; i-- {
		e := buf[i]
		switch strings.ToUpper(e.Level) {
		case "ERROR":
			msgKey := e.Message
			if len(msgKey) > 80 {
				msgKey = msgKey[:80]
			}
			k := errKey{e.Tag, msgKey}
			if idx, exists := errorKeys[k]; exists {
				errors[idx].Count++
			} else {
				errorKeys[k] = len(errors)
				errors = append(errors, errorEntry{
					Tag: e.Tag, Message: e.Message,
					Timestamp: e.Timestamp, FirstSeen: e.Timestamp, Count: 1,
				})
			}
		case "WARN", "WARNING":
			warningCount++
			if len(warnings) < 20 {
				warnings = append(warnings, map[string]any{
					"tag": e.Tag, "message": e.Message, "timestamp": e.Timestamp,
				})
			}
		}
	}

	if errors == nil {
		errors = []errorEntry{}
	}
	if warnings == nil {
		warnings = []map[string]any{}
	}

	if r.URL.Query().Get("fmt") == "html" {
		filter := r.URL.Query().Get("filter")
		if filter == "" {
			filter = "all"
		}
		generationItems, generationAssets, generationReady, generationMissing := 0, 0, 0, 0
		if latestGeneration != nil {
			generationItems = latestGeneration.ItemCount
			generationAssets = latestGeneration.AssetCount
			generationReady = latestGeneration.ReadyAssetCount
			generationMissing = latestGeneration.ServerMissingAssetCount
		}
		deviceVerified, devicePending, deviceFailed, deviceMissing, deviceTotal := 0, 0, 0, 0, 0
		deviceBytes := ""
		if latestHealth != nil {
			deviceVerified = latestHealth.VerifiedAssets
			devicePending = latestHealth.PendingAssets
			deviceFailed = latestHealth.FailedAssets
			deviceMissing = latestHealth.MissingAssets
			deviceTotal = latestHealth.TotalAssets
			deviceBytes = formatAndroidBytes(latestHealth.VerifiedBytes)
		} else if latestGeneration != nil {
			deviceTotal = latestGeneration.AssetCount
		}
		d := components.AndroidDashboardData{
			StepsCompleted:    stepsCompleted,
			StepsTotal:        len(syncSteps),
			FeedItemsTotal:    feedItemsTotal,
			FeedItemsMedia:    feedItemsMedia,
			GenerationItems:   generationItems,
			GenerationAssets:  generationAssets,
			GenerationReady:   generationReady,
			GenerationMissing: generationMissing,
			DevicePercent:     androidPercent(deviceVerified, deviceTotal),
			DeviceVerified:    deviceVerified,
			DevicePending:     devicePending,
			DeviceFailed:      deviceFailed,
			DeviceMissing:     deviceMissing,
			DeviceTotal:       deviceTotal,
			DeviceBytes:       deviceBytes,
			ErrorCount:        len(errors),
			WarningCount:      warningCount,
			LogFilter:         filter,
			ForceSyncPending:  pending,
		}
		if lastSync != nil {
			d.SyncAgo, _ = lastSync["ago"].(string)
			d.SyncDuration, _ = lastSync["elapsed"].(string)
			if completedStr, ok := lastSync["completed"].(string); ok && completedStr != "" {
				if t, err := parseAndroidTimestamp(completedStr); err == nil {
					d.SyncCompletedHMS = t.Local().Format("15:04:05")
				}
			}
			if startedStr, ok := lastSync["started"].(string); ok && startedStr != "" {
				startedHMS, endedHMS := "", ""
				if t, err := parseAndroidTimestamp(startedStr); err == nil {
					startedHMS = t.Local().Format("15:04:05")
				}
				if completedStr, ok := lastSync["completed"].(string); ok && completedStr != "" {
					if t, err := parseAndroidTimestamp(completedStr); err == nil {
						endedHMS = t.Local().Format("15:04:05")
					}
				}
				if startedHMS != "" && endedHMS != "" {
					d.PipelineFooter = "Sync started " + startedHMS + " \u00b7 completed " + endedHMS + " \u00b7 " + d.SyncDuration + " total"
				}
			}
		}
		if structuredSync.Footer != "" {
			d.PipelineFooter = structuredSync.Footer
		}
		for _, step := range syncSteps {
			ps := components.AndroidPipelineStep{Name: step.Name, Status: step.Status}
			if step.DurationMs != nil {
				if ms, ok := step.DurationMs.(int); ok {
					if ms >= 1000 {
						ps.Duration = fmt.Sprintf("%.1fs", float64(ms)/1000)
					} else {
						ps.Duration = fmt.Sprintf("%dms", ms)
					}
				} else if ms, ok := step.DurationMs.(float64); ok {
					if ms >= 1000 {
						ps.Duration = fmt.Sprintf("%.1fs", ms/1000)
					} else {
						ps.Duration = fmt.Sprintf("%.0fms", ms)
					}
				} else if ms, ok := step.DurationMs.(int64); ok {
					if ms >= 1000 {
						ps.Duration = fmt.Sprintf("%.1fs", float64(ms)/1000)
					} else {
						ps.Duration = fmt.Sprintf("%dms", ms)
					}
				}
			}
			d.Pipeline = append(d.Pipeline, ps)
		}
		d.CacheHealth = androidAssetHealthRows(latestHealth, latestGeneration != nil, generationReady, generationAssets)
		// Activity with dedup
		prevKey := ""
		for _, e := range activity {
			tsSec := e.Timestamp
			if len(tsSec) > 19 {
				tsSec = tsSec[:19]
			}
			key := tsSec + "|" + e.Tag + "|" + e.Message
			if key == prevKey {
				continue
			}
			prevKey = key
			levelCSS := "log-lvl-info"
			switch strings.ToUpper(e.Level) {
			case "ERROR":
				levelCSS = "log-lvl-err"
			case "WARN", "WARNING":
				levelCSS = "log-lvl-warn"
			}
			tsDisp := e.Timestamp
			if t, err := parseAndroidTimestamp(e.Timestamp); err == nil {
				tsDisp = t.Local().Format("15:04:05")
			}
			d.Activity = append(d.Activity, components.AndroidLogEntry{
				Timestamp: tsDisp, Tag: e.Tag, Message: e.Message, LevelCSS: levelCSS,
			})
		}
		for _, e := range errors {
			tsDisp := e.Timestamp
			if t, err := parseAndroidTimestamp(e.Timestamp); err == nil {
				tsDisp = t.Local().Format("15:04:05")
			}
			firstDisp := ""
			if e.FirstSeen != "" && e.FirstSeen != e.Timestamp {
				if t, err := parseAndroidTimestamp(e.FirstSeen); err == nil {
					firstDisp = t.Local().Format("15:04:05")
				}
			}
			d.Errors = append(d.Errors, components.AndroidErrorEntry{
				Tag: e.Tag, Message: e.Message, Timestamp: tsDisp, FirstSeen: firstDisp, Count: e.Count,
			})
		}
		for _, w := range warnings {
			tag, _ := w["tag"].(string)
			msg, _ := w["message"].(string)
			d.Warnings = append(d.Warnings, components.AndroidWarningEntry{Tag: tag, Message: msg})
		}
		if roomResult != nil {
			rr := components.AndroidRoomResult{}
			if m, ok := roomResult.(map[string]any); ok {
				if s, ok := m["error"].(string); ok {
					rr.Error = s
				}
				if s, ok := m["query"].(string); ok {
					rr.Query = s
				}
				if n, ok := m["row_count"].(int); ok {
					rr.RowCount = n
				} else if n, ok := m["row_count"].(float64); ok {
					rr.RowCount = int(n)
				}
				if cols, ok := m["columns"].([]string); ok {
					rr.Columns = cols
				} else if colsAny, ok := m["columns"].([]any); ok {
					for _, c := range colsAny {
						if s, ok := c.(string); ok {
							rr.Columns = append(rr.Columns, s)
						}
					}
				}
				if rows, ok := m["rows"].([]any); ok {
					for _, row := range rows {
						if ra, ok := row.([]any); ok {
							var rowStr []string
							for _, v := range ra {
								if v == nil {
									rowStr = append(rowStr, "")
								} else {
									rowStr = append(rowStr, fmt.Sprintf("%v", v))
								}
							}
							rr.Rows = append(rr.Rows, rowStr)
						}
					}
				}
			}
			d.RoomQuery = &rr
		}
		w.Header().Set("Content-Type", "text/html")
		_ = components.AndroidDashboard(s.pageProps(w, r), d).Render(r.Context(), w)
		return
	}

	writeJSON(w, 200, map[string]any{
		"last_sync":          lastSync,
		"sync_steps":         syncSteps,
		"feed_items":         feedItems,
		"steps_completed":    stepsCompleted,
		"steps_total":        len(syncSteps),
		"activity":           activity,
		"errors":             errors,
		"warnings":           warnings,
		"error_count":        len(errors),
		"warning_count":      warningCount,
		"force_sync_pending": pending,
		"cache_health":       cacheHealth,
		"cache_expected": map[string]any{
			"videos":  expectations.Videos,
			"moments": expectations.Moments,
			"feed":    expectations.FeedMedia,
			"avatars": expectations.Avatars,
		},
		"room_query": roomResult,
	})
}

func (s *Server) handleAndroidForceSync(w http.ResponseWriter, r *http.Request) {
	android.mu.Lock()
	android.forceSyncFlag = true
	android.mu.Unlock()
	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleAndroidForceSyncCheck(w http.ResponseWriter, r *http.Request) {
	android.mu.Lock()
	pending := android.forceSyncFlag
	android.forceSyncFlag = false
	android.mu.Unlock()
	writeJSON(w, 200, map[string]any{"force_sync": pending})
}

func (s *Server) handleAndroidFetch(w http.ResponseWriter, r *http.Request) {
	android.mu.Lock()
	android.fetchRequested = true
	android.mu.Unlock()
	writeJSON(w, 200, map[string]any{"success": true})
}

// ── Room query relay ──────────────────────────────────────────────────────────

func (s *Server) handleRoomQueryPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}
	q := strings.TrimSpace(body.Query)
	if q == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "empty query"})
		return
	}
	upper := strings.ToUpper(q)
	if !strings.HasPrefix(upper, "SELECT") &&
		!strings.HasPrefix(upper, "PRAGMA") &&
		!strings.HasPrefix(upper, "EXPLAIN") {
		writeJSON(w, 400, map[string]any{"success": false, "error": "only SELECT/PRAGMA/EXPLAIN allowed"})
		return
	}

	android.mu.Lock()
	android.roomQuery = &q
	android.roomResult = nil
	android.mu.Unlock()

	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleRoomQueryCheck(w http.ResponseWriter, r *http.Request) {
	android.mu.Lock()
	q := android.roomQuery
	android.roomQuery = nil
	android.mu.Unlock()

	if q == nil {
		writeJSON(w, 200, map[string]any{"has_query": false})
		return
	}
	writeJSON(w, 200, map[string]any{"has_query": true, "query": *q})
}

func (s *Server) handleRoomQueryResultPost(w http.ResponseWriter, r *http.Request) {
	var body any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}

	android.mu.Lock()
	android.roomResult = body
	android.mu.Unlock()

	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleRoomQueryResultGet(w http.ResponseWriter, r *http.Request) {
	android.mu.Lock()
	result := android.roomResult
	android.mu.Unlock()

	if result == nil {
		writeJSON(w, 200, map[string]any{"has_result": false})
		return
	}
	writeJSON(w, 200, map[string]any{"has_result": true, "result": result})
}
