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
	"x_ingest":               "Fetches X posts, stores feed items, and declares canonical content assets",
	"x_status_enrichment":    "Completes missing stored X status metadata without owning media readiness",
	"feed_media":             "Claims and downloads canonical X content assets",
	"profile_refresh":        "Processes durable profile jobs and atomically publishes identity assets",
	"dearrow":                "Fetches DeArrow metadata and publishes canonical thumbnails",
	"scheduler":              "Queues subscribed channels for refresh",
	"download_pool":          "Downloads queued videos and atomically publishes their canonical assets",
	"preview":                "Generates preview assets for canonical video streams",
	"channel_metadata_prune": "Prunes stale unreferenced channel metadata and its canonical assets",
	"feed_bootstrap":         "Triggers immediate ingest when the feed is sparse",
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
