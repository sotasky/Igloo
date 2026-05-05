package index

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// WorkerInfo describes a background worker goroutine.
type WorkerInfo struct {
	Name     string
	Kind     string // "long_running" or "one_shot"
	File     string
	FuncName string
	Line     int
	Tables   []string
}

var (
	rLaunch    = regexp.MustCompile(`m\.launch\("(\w+)"\s*,\s*m\.([\w.]+)\)`)
	rStartOnce = regexp.MustCompile(`m\.startOnce\("(\w+)"\s*,\s*m\.([\w.]+)\)`)
)

// ScanWorkerManager parses manager.go StartAll() for worker registrations.
func ScanWorkerManager(source string) []WorkerInfo {
	var workers []WorkerInfo

	for _, m := range rLaunch.FindAllStringSubmatch(source, -1) {
		workers = append(workers, WorkerInfo{
			Name: m[1], Kind: "long_running", FuncName: m[2],
		})
	}
	for _, m := range rStartOnce.FindAllStringSubmatch(source, -1) {
		workers = append(workers, WorkerInfo{
			Name: m[1], Kind: "one_shot", FuncName: m[2],
		})
	}
	return workers
}

// workerDescriptions provides human-readable descriptions for known workers.
var workerDescriptions = map[string]string{
	"rsshub_ingest":       "Fetches Twitter/X posts via RSSHub, stores in feed_items, creates feed_media_jobs",
	"feed_media":          "Downloads images/videos/GIFs for feed items, stores in media_files",
	"avatar_refresh":      "Fetches and caches channel avatars via unavatar.io and yt-dlp",
	"scheduler":           "Periodic channel refresh scheduler — queues channels for download check",
	"download_pool":       "Processes download_queue — downloads videos via yt-dlp, creates preview sprites",
	"preview":             "Generates VTT preview sprite sheets for downloaded videos",
	"search_index":        "Builds FTS5 full-text search index on startup",
	"feed_bootstrap":      "Triggers immediate ingest if feed is sparse (< threshold items)",
	"ranked_queue_warmup": "Pre-scores feed items for ranking on startup",
	"preview_backfill":    "Generates missing preview sprites for existing videos",
	"profile_refresh":     "Refreshes channel profile metadata (names, descriptions, follower counts)",
	"feed_scoring":        "Continuously re-scores feed items for ranking based on engagement and freshness",
	"thumbnail_backfill":  "Generates missing thumbnails for existing feed items",
}

// FormatWorkerMap returns a formatted worker map string.
func FormatWorkerMap(workers []WorkerInfo, fileTables map[string][]string) string {
	var sb strings.Builder
	sb.WriteString("=== Background Workers ===\n\n")

	// Group by kind
	var longRunning, oneShot []WorkerInfo
	for _, w := range workers {
		if w.Kind == "long_running" {
			longRunning = append(longRunning, w)
		} else {
			oneShot = append(oneShot, w)
		}
	}

	if len(longRunning) > 0 {
		sb.WriteString("Long-running workers (goroutines that run for server lifetime):\n\n")
		for _, w := range longRunning {
			writeWorkerEntry(&sb, w, fileTables)
		}
	}

	if len(oneShot) > 0 {
		sb.WriteString("One-shot startup tasks:\n\n")
		for _, w := range oneShot {
			writeWorkerEntry(&sb, w, fileTables)
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func writeWorkerEntry(sb *strings.Builder, w WorkerInfo, fileTables map[string][]string) {
	fmt.Fprintf(sb, "  %s\n", w.Name)
	if desc, ok := workerDescriptions[w.Name]; ok {
		fmt.Fprintf(sb, "    purpose:  %s\n", desc)
	}
	fmt.Fprintf(sb, "    func:     %s\n", w.FuncName)
	if w.File != "" {
		fmt.Fprintf(sb, "    file:     %s\n", w.File)
	}
	if tables := fileTables[w.File]; len(tables) > 0 {
		sort.Strings(tables)
		fmt.Fprintf(sb, "    tables:   %v\n", tables)
	}
	sb.WriteString("\n")
}
