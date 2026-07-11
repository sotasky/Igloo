package worker

import (
	"context"
	"log"
	"time"
)

// runRankedQueueWarmup pre-warms the feed by loading the first 2000 items
// from storage, which allows the DB page cache to populate before first request.
func (m *Manager) runRankedQueueWarmup(ctx context.Context) {
	start := time.Now()
	log.Printf("[ranked_queue_warmup] warming feed cache")

	items, err := m.db.ListFeedItemsPage(2000, nil, false)
	elapsed := time.Since(start).Round(time.Millisecond)

	if err != nil {
		log.Printf("[ranked_queue_warmup] error: %v", err)
		return
	}

	log.Printf("[ranked_queue_warmup] warmed %d feed items in %s", len(items), elapsed)
}
