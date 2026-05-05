package worker

import (
	"context"
	"log"
)

const feedBootstrapThreshold = 40

// runFeedBootstrap runs an immediate ingest cycle if the feed_items table has
// fewer than feedBootstrapThreshold rows — useful on a fresh installation.
func (m *Manager) runFeedBootstrap(ctx context.Context) {
	count, err := m.db.CountFeedItems()
	if err != nil {
		log.Printf("[feed_bootstrap] CountFeedItems: %v", err)
		return
	}

	if count >= feedBootstrapThreshold {
		log.Printf("[feed_bootstrap] %d items already present, skipping bootstrap", count)
		return
	}

	log.Printf("[feed_bootstrap] only %d feed items — running initial ingest", count)
	if m.cfg.RSSHubBase != "" {
		m.runIngestCycle(ctx)
	} else {
		log.Printf("[feed_bootstrap] RSSHUB_BASE not set, cannot bootstrap")
	}
}
