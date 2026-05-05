package worker

import (
	"context"
	"log"
	"time"
)

// buildSearchIndex rebuilds the FTS5 search index and updates worker status.
func (m *Manager) buildSearchIndex(ctx context.Context) {
	start := time.Now()
	log.Printf("[search_index] rebuilding FTS5 index")

	count, err := m.db.RebuildSearchIndex(ctx)
	elapsed := time.Since(start).Round(time.Millisecond)

	if err != nil {
		log.Printf("[search_index] error: %v", err)
		return
	}

	log.Printf("[search_index] indexed %d rows in %s", count, elapsed)
}
