package web

import (
	"bufio"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
)

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
		return filepath.Join(s.cfg.Storage.StateRoot(), "logs", "server", logType+".log"), true
	case "android":
		return filepath.Join(s.cfg.Storage.StateRoot(), "logs", "android", "android.log"), true
	case "android-stats":
		return filepath.Join(s.cfg.Storage.StateRoot(), "logs", "android", "stats.jsonl"), true
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
			if r.URL.Query().Get("fmt") == "html" {
				filter := r.URL.Query().Get("raw_filter")
				if filter == "" {
					filter = "all"
				}
				w.Header().Set("Content-Type", "text/html")
				_ = components.ServerRawLog(s.pageProps(w, r), components.ServerRawLogData{Filter: filter}).Render(r.Context(), w)
				return
			}
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
			path = filepath.Join(s.cfg.Storage.StateRoot(), "logs", "android", "android.log")
		} else if t == "android-stats" {
			path = filepath.Join(s.cfg.Storage.StateRoot(), "logs", "android", "stats.jsonl")
		} else {
			path = filepath.Join(s.cfg.Storage.StateRoot(), "logs", "server", t+".log")
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
	if err := decodeJSON(w, r, &body); err != nil && requestBodyTooLarge(err) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
		return
	}
	if body.Days <= 0 {
		body.Days = 30
	}

	cutoff := time.Now().Add(-time.Duration(body.Days) * 24 * time.Hour)
	logsDir := filepath.Join(s.cfg.Storage.StateRoot(), "logs")

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
		filepath.Join(s.cfg.Storage.StateRoot(), "logs", "server", "server.log"),
		filepath.Join(s.cfg.Storage.StateRoot(), "logs", "server", "download.log"),
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
