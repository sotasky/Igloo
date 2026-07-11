package worker

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
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
			// fxtwitter doesn't think this is a reply — leave reply_to_status empty.
			return nil
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
	return model.FeedItem{
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
