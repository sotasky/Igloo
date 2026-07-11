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
	"strings"
	"syscall"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/dearrow"
	"github.com/screwys/igloo/internal/storage"
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
	layout, err := storage.New(dataDir, strings.TrimSpace(os.Getenv("IGLOO_MEDIA_DIR")))
	if err != nil {
		log.Fatalf("storage layout: %v", err)
	}
	if err := layout.Ensure(); err != nil {
		log.Fatalf("storage availability: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	d, err := db.Open(layout)
	if err != nil {
		log.Fatalf("db.Open: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	thumbDir, err := layout.WritePath("thumbnails/dearrow")
	if err != nil {
		log.Fatalf("DeArrow thumbnail path: %v", err)
	}
	fetcher := &dearrow.Fetcher{
		Client:   dearrow.NewClient(dearrow.DefaultBaseURL),
		Extract:  dearrow.ExtractFrame,
		ThumbDir: thumbDir,
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
			asset, err := d.GetReadyVideoPrimaryAsset(id)
			if err != nil {
				log.Printf("skip %s: get media asset: %v", id, err)
				continue
			}
			absPath := ""
			if asset != nil {
				absPath, err = layout.Path(asset.FilePath)
				if err != nil {
					log.Printf("skip %s: resolve video path: %v", id, err)
					continue
				}
			}
			res, err := fetcher.FetchAndProcess(ctx, id, absPath)
			processed++

			if *dry {
				log.Printf("dry %s: title=%v casual=%v thumb=%v err=%v",
					id, ptrStr(res.Title), ptrStr(res.CasualTitle), ptrStr(res.ThumbPath), err)
				if res.ThumbPath != nil {
					_ = os.Remove(*res.ThumbPath)
				}
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
				if saveErr := d.SetDearrowTitles(id, res.Title, res.CasualTitle, time.Now().UnixMilli()); saveErr != nil {
					log.Printf("save partial %s: %v", id, saveErr)
				}
				time.Sleep(*rate)
				continue
			}
			var thumbRel *string
			if res.ThumbPath != nil {
				rel, relErr := layout.Key(*res.ThumbPath)
				if relErr == nil {
					thumbRel = &rel
				} else {
					_ = os.Remove(*res.ThumbPath)
					if saveErr := d.SetDearrowTitles(id, res.Title, res.CasualTitle, time.Now().UnixMilli()); saveErr != nil {
						log.Printf("save partial %s: %v", id, saveErr)
					}
					time.Sleep(*rate)
					continue
				}
			}
			if saveErr := d.SetDearrowData(id, res.Title, res.CasualTitle, thumbRel, time.Now().UnixMilli()); saveErr != nil {
				log.Printf("save %s: %v", id, saveErr)
				if res.ThumbPath != nil {
					_ = os.Remove(*res.ThumbPath)
				}
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
