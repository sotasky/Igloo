package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/xfeed"
)

const (
	// replyResolverWorkers caps concurrency on fxtwitter calls. fxtwitter has
	// no published rate limits, but ~8 parallel is comfortably within community
	// observed budgets and bounds wall-clock per cycle.
	replyResolverWorkers = 8

	// replyChainDepthCap bounds how far up a thread the resolver walks on
	// initial ingest. Anything deeper is left for lazy expansion in the UI.
	replyChainDepthCap = 5
)

// ReplyResolver fills reply_to_status on freshly-ingested reply tweets and
// stores any missing ancestors as ghost rows so the thread is fully joinable
// in DB by the time the UI queries it.
type ReplyResolver struct {
	d  *db.DB
	fx *fxtwitter.Client
}

// NewReplyResolver constructs a resolver bound to the given DB and fxtwitter
// client. Pass a real *fxtwitter.Client in production; pass a Client backed by
// httptest.Server in tests.
func NewReplyResolver(d *db.DB, fx *fxtwitter.Client) *ReplyResolver {
	return &ReplyResolver{d: d, fx: fx}
}

func (m *Manager) resolveReplyChains(ctx context.Context, items []model.FeedItem) {
	if m.replyResolver == nil {
		m.replyResolver = NewReplyResolver(m.db, fxtwitter.NewClient())
	}
	if err := m.replyResolver.ResolveCycle(ctx, items); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[reply-resolver] batch: %v", err)
	}
}

// ResolveCycle resolves every reply in items. Safe to call with a mix of
// replies and non-replies — non-replies are skipped. Errors from individual
// resolves are logged but don't fail the cycle.
func (r *ReplyResolver) ResolveCycle(ctx context.Context, items []model.FeedItem) error {
	if r == nil || r.fx == nil {
		return nil
	}

	cache := newResolveCache()
	jobs := make(chan model.FeedItem)

	var wg sync.WaitGroup
	for i := 0; i < replyResolverWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := r.resolveOne(ctx, item, cache); err != nil {
					log.Printf("[reply-resolver] %s: %v", item.TweetID, err)
				}
			}
		}()
	}

	for _, item := range items {
		if !item.IsReply {
			continue
		}
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()
	return nil
}

// resolveOne resolves a single leaf reply and walks up the chain.
func (r *ReplyResolver) resolveOne(ctx context.Context, leaf model.FeedItem, cache *resolveCache) error {
	currentID := strings.TrimSpace(leaf.ReplyToStatus)
	currentHandle := firstNonEmptyHandle(leaf.ReplyToHandle, leaf.AuthorHandle)
	if currentID == "" {
		leafTweet, err := r.fx.FetchTweet(ctx, leaf.AuthorHandle, leaf.TweetID)
		if err != nil {
			return err
		}
		if leafTweet.ReplyToStatus == "" {
			return r.d.UpdateReplyToStatus(leaf.TweetID, "")
		}

		if err := r.d.UpdateReplyToStatus(leaf.TweetID, leafTweet.ReplyToStatus); err != nil {
			return err
		}
		currentID = leafTweet.ReplyToStatus
		currentHandle = firstNonEmptyHandle(leafTweet.ReplyToHandle, leafTweet.AuthorHandle)
	}

	for depth := 0; depth < replyChainDepthCap && currentID != ""; depth++ {
		existing, err := r.d.GetFeedItemByTweetID(currentID)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.ReplyToStatus == "" {
				return nil
			}
			currentID = existing.ReplyToStatus
			currentHandle = firstNonEmptyHandle(existing.ReplyToHandle, existing.AuthorHandle)
			continue
		}

		// In-cycle dedupe: if another worker already claimed this parent, stop.
		if cache.markFetching(currentID) {
			return nil
		}

		parent, err := r.fx.FetchTweet(ctx, currentHandle, currentID)
		if err != nil {
			if errors.Is(err, fxtwitter.ErrNotFound) {
				return nil
			}
			return err
		}

		ghost := tweetToGhostFeedItem(parent)
		if err := r.d.UpsertGhostFeedItem(ghost); err != nil {
			return err
		}

		currentID = parent.ReplyToStatus
		currentHandle = firstNonEmptyHandle(parent.ReplyToHandle, parent.AuthorHandle)
	}
	return nil
}

func firstNonEmptyHandle(handles ...string) string {
	for _, handle := range handles {
		if trimmed := strings.TrimSpace(handle); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// tweetToGhostFeedItem converts an fxtwitter Tweet into a feed_items row.
// Marked is_ghost=1 — feed-list queries filter these out, but they're joinable
// by reply_to_status for the thread API.
func tweetToGhostFeedItem(tw *fxtwitter.Tweet) model.FeedItem {
	var pubAt *time.Time
	if !tw.CreatedAt.IsZero() {
		t := tw.CreatedAt
		pubAt = &t
	}
	item := model.FeedItem{
		TweetID:           tw.ID,
		AuthorHandle:      tw.AuthorHandle,
		AuthorDisplayName: tw.AuthorDisplayName,
		AuthorAvatarURL:   tw.AuthorAvatarURL,
		BodyText:          tw.Text,
		Lang:              tw.Lang,
		ReplyToHandle:     tw.ReplyToHandle,
		ReplyToStatus:     tw.ReplyToStatus,
		IsReply:           tw.ReplyToStatus != "",
		IsGhost:           true,
		MediaJSON:         tw.MediaJSON,
		PublishedAt:       pubAt,
		FetchedAt:         tw.CreatedAt,
		ContentHash:       "ghost-" + tw.ID,
	}
	if quote := tw.Quote; quote != nil && xfeed.ValidTweetID(quote.ID) && xfeed.ValidHandle(quote.AuthorHandle) {
		item.QuoteTweetID = quote.ID
		item.QuoteAuthorHandle = xfeed.NormalizeHandle(quote.AuthorHandle)
		item.QuoteAuthorDisplayName = quote.AuthorDisplayName
		item.QuoteAuthorAvatarURL = model.CleanFeedAvatarURL(quote.AuthorAvatarURL)
		item.QuoteBodyText = quote.Text
		item.QuoteLang = quote.Lang
		item.QuoteMediaJSON = trustedTwitterQuoteMediaJSON(quote.MediaJSON)
		if !quote.CreatedAt.IsZero() {
			t := quote.CreatedAt
			item.QuotePublishedAt = &t
		}
	}
	item.ParseMedia()
	return item
}

func trustedTwitterQuoteMediaJSON(raw string) string {
	var refs []model.MediaRef
	if json.Unmarshal([]byte(raw), &refs) != nil {
		return ""
	}
	trusted := refs[:0]
	for _, ref := range refs {
		urlOK := ref.URL == "" || isTrustedTwitterQuoteMediaURL(ref.URL)
		thumbnailOK := ref.ThumbnailURL == "" || isTrustedTwitterQuoteMediaURL(ref.ThumbnailURL)
		if !urlOK || !thumbnailOK || (ref.URL == "" && ref.ThumbnailURL == "") {
			continue
		}
		trusted = append(trusted, ref)
	}
	if len(trusted) == 0 {
		return ""
	}
	data, err := json.Marshal(trusted)
	if err != nil {
		return ""
	}
	return string(data)
}

func isTrustedTwitterQuoteMediaURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	return strings.EqualFold(u.Hostname(), "pbs.twimg.com") || strings.EqualFold(u.Hostname(), "video.twimg.com")
}

// resolveCache prevents two workers from fetching the same parent within one cycle.
type resolveCache struct {
	mu       sync.Mutex
	fetching map[string]bool
}

func newResolveCache() *resolveCache {
	return &resolveCache{fetching: make(map[string]bool)}
}

// markFetching returns true if the id was already claimed by another worker.
func (c *resolveCache) markFetching(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fetching[id] {
		return true
	}
	c.fetching[id] = true
	return false
}
