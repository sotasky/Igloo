package worker

import (
	"context"
	"log"
	"time"
)

// youtubeEnrichBatchLimit is the max number of videos processed per scan.
// Keeps a single tick bounded so startup isn't dominated by a huge backlog.
const youtubeEnrichBatchLimit = 50

// youtubeEnrichTickInterval is how often the worker scans for stale entries.
const youtubeEnrichTickInterval = 15 * time.Minute

// dearrowPerFetchSleep throttles per-video requests to the sponsor.ajay.app
// APIs (DeArrow + SponsorBlock share the host). Declared as a var so tests
// can override it without a seam. Name kept pre-SB for test compatibility.
var dearrowPerFetchSleep = 750 * time.Millisecond

// youtubeEnrichInitialDelay is the wait after StartAll before the first scan.
// Spreads out startup load with other workers (rsshub, download pool, etc).
const youtubeEnrichInitialDelay = 30 * time.Second

// youtubeEnrichOnce runs a single YouTube enrichment pass: for every video
// in the batch, fetches whatever is missing (DeArrow branding, SponsorBlock
// segments, or both) against sponsor.ajay.app. Returns the number of videos
// touched. Exposed for tests.
func (m *Manager) youtubeEnrichOnce(ctx context.Context) int {
	nowMs := time.Now().UnixMilli()
	tasks, err := m.db.ListVideosNeedingYoutubeEnrichment(nowMs, youtubeEnrichBatchLimit)
	if err != nil {
		log.Printf("[youtube-enrich] list: %v", err)
		return 0
	}
	processed := 0
	for i, task := range tasks {
		select {
		case <-ctx.Done():
			return processed
		default:
		}
		if task.NeedsDearrow {
			v, err := m.db.GetVideo(task.VideoID)
			if err == nil && v != nil {
				m.triggerDearrowFetch(ctx, task.VideoID, v.FilePath, v.Platform)
			}
		}
		if task.NeedsSB {
			m.fetchSponsorBlockFor(ctx, task.VideoID, task.PublishedAtMs)
		}
		processed++
		// Sleep between videos — respect context cancellation.
		if i < len(tasks)-1 {
			select {
			case <-ctx.Done():
				return processed
			case <-time.After(dearrowPerFetchSleep):
			}
		}
	}
	return processed
}

// dearrowOnce is kept as a thin alias so existing tests keep working with
// the pre-SB worker name.
//
// Deprecated: use youtubeEnrichOnce.
func (m *Manager) dearrowOnce(ctx context.Context) int {
	return m.youtubeEnrichOnce(ctx)
}

// runDearrowWorker is the long-lived scan loop. Registered via m.launch so
// WaitGroup accounting and m.ctx cancellation are handled by the framework.
// Despite the name (kept for status-map stability) it handles both DeArrow
// and SponsorBlock enrichment for YouTube videos.
func (m *Manager) runDearrowWorker(ctx context.Context) {
	// Initial delay.
	select {
	case <-ctx.Done():
		return
	case <-time.After(youtubeEnrichInitialDelay):
	}
	ticker := time.NewTicker(youtubeEnrichTickInterval)
	defer ticker.Stop()
	for {
		if n := m.youtubeEnrichOnce(ctx); n > 0 {
			log.Printf("[youtube-enrich] processed %d videos", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
