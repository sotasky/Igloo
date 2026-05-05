// scripts/dev/dearrow_backfill/main.go
//
// One-shot backfill: for every YouTube video missing DeArrow data
// (db.ListVideosNeedingDearrow), call the DeArrow API and persist
// whatever branding comes back. Same logic the download-time trigger
// and background worker use; just run locally and bounded by a flag.
//
// Usage:
//
//	go run scripts/dev/dearrow_backfill/main.go              # full run
//	go run scripts/dev/dearrow_backfill/main.go -max=50      # cap at 50
//	go run scripts/dev/dearrow_backfill/main.go -rate=500ms  # faster
//	go run scripts/dev/dearrow_backfill/main.go -dry         # no writes
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/dearrow"
)

func main() {
	rate := flag.Duration("rate", 750*time.Millisecond, "sleep between fetches")
	max := flag.Int("max", 0, "max videos to process (0 = no cap)")
	dry := flag.Bool("dry", false, "log what would happen without writing")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
	}
	dataDir := filepath.Join(home, ".local", "share", "igloo")
	if override := os.Getenv("IGLOO_DATA_DIR"); override != "" {
		dataDir = override
	}
	dbPath := filepath.Join(dataDir, "igloo.db")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	d, err := db.Open(dbPath, dataDir)
	if err != nil {
		log.Fatalf("db.Open: %v", err)
	}
	defer d.Close()

	fetcher := &dearrow.Fetcher{
		Client:   dearrow.NewClient(dearrow.DefaultBaseURL),
		Extract:  dearrow.ExtractFrame,
		ThumbDir: filepath.Join(dataDir, "thumbnails", "dearrow"),
	}

	processed, updated, failed := 0, 0, 0
	defer func() {
		log.Printf("done: processed=%d updated=%d failed=%d", processed, updated, failed)
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		if *max > 0 && processed >= *max {
			return
		}
		nowMs := time.Now().UnixMilli()
		batchLimit := 200
		if *max > 0 && *max-processed < batchLimit {
			batchLimit = *max - processed
		}
		ids, err := d.ListVideosNeedingDearrow(nowMs, batchLimit)
		if err != nil {
			log.Fatalf("ListVideosNeedingDearrow: %v", err)
		}
		if len(ids) == 0 {
			return
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			if *max > 0 && processed >= *max {
				return
			}
			v, err := d.GetVideo(id)
			if err != nil || v == nil {
				log.Printf("skip %s: get: %v", id, err)
				continue
			}
			absPath := v.FilePath
			if absPath != "" && !filepath.IsAbs(absPath) {
				absPath = filepath.Join(dataDir, absPath)
			}
			res, err := fetcher.FetchAndProcess(ctx, id, absPath)
			processed++

			if *dry {
				log.Printf("dry %s: title=%v casual=%v thumb=%v err=%v",
					id, ptrStr(res.Title), ptrStr(res.CasualTitle), ptrStr(res.ThumbPath), err)
				time.Sleep(*rate)
				continue
			}

			if err != nil {
				failed++
				log.Printf("err %s: %v", id, err)
				if res.Title == nil && res.CasualTitle == nil && res.ThumbPath == nil {
					if markErr := d.MarkDearrowChecked(id, time.Now().UnixMilli()); markErr != nil {
						log.Printf("mark %s: %v", id, markErr)
					}
					time.Sleep(*rate)
					continue
				}
				// fall through with partial data
			}
			var thumbRel *string
			if res.ThumbPath != nil {
				rel, relErr := filepath.Rel(dataDir, *res.ThumbPath)
				if relErr == nil {
					thumbRel = &rel
				} else {
					abs := *res.ThumbPath
					thumbRel = &abs
				}
			}
			if saveErr := d.SetDearrowData(id, res.Title, res.CasualTitle, thumbRel, time.Now().UnixMilli()); saveErr != nil {
				log.Printf("save %s: %v", id, saveErr)
			} else if res.Title != nil || res.CasualTitle != nil || thumbRel != nil {
				updated++
				log.Printf("ok %s: title=%v casual=%v thumb=%v",
					id, ptrStr(res.Title), ptrStr(res.CasualTitle), ptrStr(thumbRel))
			}
			time.Sleep(*rate)
		}
	}
}

// ptrStr is a tiny logging helper so we can print *string without nil panics.
func ptrStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
