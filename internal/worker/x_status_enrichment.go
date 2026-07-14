package worker

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/xfeed"
)

const xStatusEnrichmentDedupWindow = 15 * time.Minute

func (m *Manager) RequestXStatusEnrichment(req xfeed.StatusEnrichmentRequest) {
	if m == nil || m.xStatusEnrich == nil {
		return
	}
	req.Ref.Handle = xfeed.NormalizeHandle(req.Ref.Handle)
	req.Ref.TweetID = strings.TrimSpace(req.Ref.TweetID)
	req.TargetTweetID = strings.TrimSpace(req.TargetTweetID)
	if !xfeed.ValidHandle(req.Ref.Handle) || !xfeed.ValidTweetID(req.Ref.TweetID) {
		return
	}
	if req.Kind == xfeed.StatusEnrichmentRetweetQuote && !xfeed.ValidTweetID(req.TargetTweetID) {
		return
	}
	key := xStatusEnrichmentKey(req)
	now := time.Now()

	m.xStatusMu.Lock()
	if m.xStatusQueued == nil {
		m.xStatusQueued = make(map[string]time.Time)
	}
	if last, ok := m.xStatusQueued[key]; ok && now.Sub(last) < xStatusEnrichmentDedupWindow {
		m.xStatusMu.Unlock()
		return
	}
	m.xStatusQueued[key] = now
	if len(m.xStatusQueued) > 4096 {
		for k, t := range m.xStatusQueued {
			if now.Sub(t) >= xStatusEnrichmentDedupWindow {
				delete(m.xStatusQueued, k)
			}
		}
	}
	m.xStatusMu.Unlock()

	select {
	case m.xStatusEnrich <- req:
	default:
		m.xStatusMu.Lock()
		delete(m.xStatusQueued, key)
		m.xStatusMu.Unlock()
		log.Printf("[x_status_enrichment] queue full; dropped %s", key)
	}
}

func xStatusEnrichmentKey(req xfeed.StatusEnrichmentRequest) string {
	return string(req.Kind) + "|" + req.Ref.Handle + "|" + req.Ref.TweetID + "|" + req.TargetTweetID
}

func (m *Manager) runXStatusEnrichmentLoop(ctx context.Context) {
	log.Printf("[x_status_enrichment] worker started")
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-m.xStatusEnrich:
			m.runOneXStatusEnrichment(ctx, req)
		}
	}
}

func (m *Manager) runOneXStatusEnrichment(ctx context.Context, req xfeed.StatusEnrichmentRequest) {
	if m == nil || m.db == nil {
		return
	}
	status, err := m.xFeedClient().FetchStatus(ctx, req.Ref.Handle, req.Ref.TweetID)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[x_status_enrichment] fetch %s/%s: %v", req.Ref.Handle, req.Ref.TweetID, err)
		}
		return
	}

	switch req.Kind {
	case xfeed.StatusEnrichmentRetweetQuote:
		m.applyRetweetQuoteEnrichment(ctx, req, status)
	default:
		m.upsertXStatusEnrichmentItems(ctx, status.Items)
	}
}

func (m *Manager) applyRetweetQuoteEnrichment(ctx context.Context, req xfeed.StatusEnrichmentRequest, status xfeed.ParseResult) {
	rich := status.Find(req.Ref.TweetID)
	if rich == nil || rich.QuoteTweetID == "" {
		return
	}
	existing, err := m.db.GetFeedItemByTweetID(req.TargetTweetID)
	if err != nil {
		log.Printf("[x_status_enrichment] load retweet target %s: %v", req.TargetTweetID, err)
		return
	}
	if existing == nil {
		return
	}
	updated := *existing
	copyQuoteFieldsForStatusEnrichment(&updated, *rich)
	m.upsertXStatusEnrichmentItems(ctx, []model.FeedItem{updated})
}

func copyQuoteFieldsForStatusEnrichment(dst *model.FeedItem, src model.FeedItem) {
	if dst == nil || src.QuoteTweetID == "" {
		return
	}
	dst.QuoteTweetID = src.QuoteTweetID
	dst.QuoteAuthorHandle = src.QuoteAuthorHandle
	dst.QuoteAuthorDisplayName = src.QuoteAuthorDisplayName
	dst.QuoteAuthorAvatarURL = src.QuoteAuthorAvatarURL
	dst.QuoteBodyText = src.QuoteBodyText
	dst.QuoteLang = src.QuoteLang
	dst.QuotePublishedAt = src.QuotePublishedAt
	dst.QuoteMediaJSON = src.QuoteMediaJSON
	dst.QuoteMedia = src.QuoteMedia
}

func (m *Manager) upsertXStatusEnrichmentItems(ctx context.Context, items []model.FeedItem) {
	if len(items) == 0 || ctx.Err() != nil {
		return
	}
	for i := range items {
		items[i].ParseMedia()
	}
	result, err := m.upsertFeedItemsBatch(items)
	if err != nil {
		log.Printf("[x_status_enrichment] upsert: %v", err)
		return
	}
	if err := m.reconcileXMediaRetentionChanges(result.XMediaRetentionChanges); err != nil {
		log.Printf("[x_status_enrichment] %v", err)
		return
	}
	m.KickMediaWork()
	m.KickFeedScoring()
}
