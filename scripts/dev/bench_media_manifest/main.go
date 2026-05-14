// bench_media_manifest times the media manifest data-source function directly
// against the local DB without going through HTTP.
//
// Usage:
//
//	go run ./scripts/dev/bench_media_manifest -scope subscriptions -since 'v2:...'
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/screwys/igloo/internal/db"
)

var (
	scope = flag.String("scope", "subscriptions", "manifest scope")
	user  = flag.String("user", "admin", "username for scoped state")
	since = flag.String("since", "", "opaque manifest cursor")
	limit = flag.Int("limit", 200, "manifest page limit")
)

func main() {
	flag.Parse()

	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".local/share/igloo")
	dbPath := filepath.Join(dataDir, "igloo.db")

	d, err := db.OpenReadOnly(dbPath, dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = d.Close()
	}()

	start := time.Now()
	entries, nextMarker, endOfStream, err := d.GetMediaManifestV2(*scope, *user, *since, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manifest: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("scope=%s entries=%d end=%t duration=%s\n", *scope, len(entries), endOfStream, time.Since(start))
	fmt.Printf("next_marker=%s\n", nextMarker)
	for i, entry := range entries {
		if i >= 12 {
			fmt.Println("...")
			break
		}
		fmt.Printf("  %s %s %s seq=%d url=%s\n", entry.AssetKind, entry.OwnerKind, entry.OwnerID, entry.ManifestSeq, entry.ServerURL)
	}
}
