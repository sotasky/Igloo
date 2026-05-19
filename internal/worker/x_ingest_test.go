package worker

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/xfeed"
)

type fakeXFeedFetcher struct {
	timeline func(context.Context, string, int) ([]model.FeedItem, error)
	source   func(context.Context, string, int) ([]model.FeedItem, error)
	status   func(context.Context, string, string) (xfeed.ParseResult, error)
}

func (f fakeXFeedFetcher) FetchTimeline(ctx context.Context, handle string, limit int) ([]model.FeedItem, error) {
	return f.timeline(ctx, handle, limit)
}

func (f fakeXFeedFetcher) FetchSource(ctx context.Context, rawURL string, limit int) ([]model.FeedItem, error) {
	return f.source(ctx, rawURL, limit)
}

func (f fakeXFeedFetcher) FetchStatus(ctx context.Context, handle, tweetID string) (xfeed.ParseResult, error) {
	if f.status != nil {
		return f.status(ctx, handle, tweetID)
	}
	return xfeed.ParseResult{}, errors.New("unexpected status fetch")
}

func TestDownloadNewAuthorAvatars_QueuesProfileFallbackForPlaceholderURL(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}

	m.downloadNewAuthorAvatars(context.Background(), []model.FeedItem{{
		AuthorHandle:    "UserAlpha",
		AuthorAvatarURL: "https://x.com/example_account/status/undefined",
	}})

	select {
	case got := <-m.avatarRequest:
		if got != "twitter_useralpha" {
			t.Fatalf("queued channelID = %q, want %q", got, "twitter_useralpha")
		}
	default:
		t.Fatal("expected profile fallback request")
	}
}

func TestPrimeFeedItemProfilesSeedsProfileRowsAndQueuesRecovery(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 4),
	}

	m.primeFeedItemProfiles(context.Background(), []model.FeedItem{{
		AuthorHandle:      "UserAlpha",
		AuthorDisplayName: "User Alpha",
	}})

	got, err := d.GetChannelProfile("twitter_useralpha")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if got == nil || got.Handle != "useralpha" || got.DisplayName != "User Alpha" || got.FetchedAt != nil {
		t.Fatalf("profile row not primed: %+v", got)
	}
	select {
	case queued := <-m.avatarRequest:
		if queued != "twitter_useralpha" {
			t.Fatalf("queued channelID = %q, want twitter_useralpha", queued)
		}
	default:
		t.Fatal("expected profile recovery request")
	}
}

func TestFeedMediaJobRowsForItemsRespectsMediaDownloadLimit(t *testing.T) {
	items := []model.FeedItem{
		{TweetID: "text_only", SourceHandle: "twitter_alice"},
		{TweetID: "media_1", SourceHandle: "twitter_alice", CanonicalURL: "https://x.com/alice/status/1", MediaJSON: `[{"url":"https://cdn.example/1.jpg","type":"photo"}]`},
		{TweetID: "media_2", SourceHandle: "twitter_alice", CanonicalURL: "https://x.com/alice/status/2", MediaJSON: `[{"url":"https://cdn.example/2.mp4","type":"video"}]`},
		{TweetID: "media_3", SourceHandle: "twitter_alice", CanonicalURL: "https://x.com/alice/status/3", MediaJSON: `[{"url":"https://cdn.example/3.jpg","type":"photo"}]`},
	}

	jobs := feedMediaJobRowsForItems(items, &db.ChannelSettings{MediaDownloadLimit: 2})
	if len(jobs) != 2 {
		t.Fatalf("jobs len = %d, want 2: %+v", len(jobs), jobs)
	}
	if jobs[0].TweetID != "media_1" || jobs[1].TweetID != "media_2" {
		t.Fatalf("job IDs = %q, %q; want media_1, media_2", jobs[0].TweetID, jobs[1].TweetID)
	}
}

func TestFeedMediaJobRowsForItemsUsesQuoteMedia(t *testing.T) {
	items := []model.FeedItem{{
		TweetID:           "quote_only",
		SourceHandle:      "twitter_alice",
		CanonicalURL:      "https://x.com/alice/status/quote",
		QuoteTweetID:      "quoted_post",
		QuoteMediaJSON:    `[{"url":"https://cdn.example/q1.jpg","type":"photo"},{"url":"https://cdn.example/q2.jpg","type":"photo"}]`,
		QuoteAuthorHandle: "bob",
	}}

	jobs := feedMediaJobRowsForItems(items, &db.ChannelSettings{MediaDownloadLimit: 20})
	if len(jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1: %+v", len(jobs), jobs)
	}
	if jobs[0].TweetID != "quote_only" {
		t.Fatalf("job tweet = %q, want quote_only", jobs[0].TweetID)
	}
	if jobs[0].MediaKind != "image" {
		t.Fatalf("job media kind = %q, want image", jobs[0].MediaKind)
	}
	if jobs[0].SlideCount != 2 {
		t.Fatalf("job slide count = %d, want 2", jobs[0].SlideCount)
	}
}

func TestFetchOneChannelUsesXFeedFetcherAndQueuesMedia(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		downloader:    testDownloader(),
		avatarRequest: make(chan string, 1),
		xFeedFetcher: fakeXFeedFetcher{
			timeline: func(_ context.Context, handle string, limit int) ([]model.FeedItem, error) {
				if handle != "sample_user" {
					t.Fatalf("handle = %q, want sample_user", handle)
				}
				if limit != 100 {
					t.Fatalf("limit = %d, want 100", limit)
				}
				return []model.FeedItem{{
					TweetID:          "1000000000000000100",
					SourceHandle:     "sample_user",
					AuthorHandle:     "sample_user",
					BodyText:         "post with media",
					MediaJSON:        `[{"url":"https://pbs.twimg.com/media/sample.jpg","type":"photo"}]`,
					CanonicalURL:     "https://x.com/sample_user/status/1000000000000000100",
					ContentHash:      "hash",
					CanonicalTweetID: "1000000000000000100",
				}}, nil
			},
			source: func(context.Context, string, int) ([]model.FeedItem, error) {
				t.Fatal("source fetch should not be called")
				return nil, nil
			},
		},
	}

	n, err := m.FetchOneChannel(context.Background(), "twitter_sample_user")
	if err != nil {
		t.Fatalf("FetchOneChannel: %v", err)
	}
	if n != 1 {
		t.Fatalf("upserted = %d, want 1", n)
	}
	got, err := d.GetFeedItemByTweetID("1000000000000000100")
	if err != nil {
		t.Fatalf("GetFeedItemByTweetID: %v", err)
	}
	if got == nil || got.BodyText != "post with media" {
		t.Fatalf("feed item = %+v", got)
	}
	queued, processing, err := d.CountPendingFeedMediaJobs()
	if err != nil {
		t.Fatalf("CountPendingFeedMediaJobs: %v", err)
	}
	if queued+processing != 1 {
		t.Fatalf("media jobs = queued %d processing %d, want 1 total", queued, processing)
	}
}

func TestFetchOneChannelDropsDetachedForeignTimelineItems(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		downloader:    testDownloader(),
		avatarRequest: make(chan string, 1),
		xFeedFetcher: fakeXFeedFetcher{
			timeline: func(_ context.Context, handle string, limit int) ([]model.FeedItem, error) {
				return []model.FeedItem{
					{
						TweetID:          "sample_source_post",
						SourceHandle:     handle,
						AuthorHandle:     handle,
						BodyText:         "source post",
						ContentHash:      "hash-source",
						CanonicalTweetID: "sample_source_post",
					},
					{
						TweetID:          "sample_post_b",
						SourceHandle:     handle,
						AuthorHandle:     "sample_author_b",
						BodyText:         "unrelated timeline recommendation",
						ContentHash:      "hash-foreign",
						CanonicalTweetID: "sample_post_b",
					},
					{
						TweetID:          "sample_reply_post",
						SourceHandle:     handle,
						AuthorHandle:     "sample_reply_author",
						BodyText:         "@sample_user reply",
						ReplyToHandle:    handle,
						ReplyToStatus:    "sample_source_post",
						IsReply:          true,
						ContentHash:      "hash-reply",
						CanonicalTweetID: "sample_reply_post",
					},
				}, nil
			},
			source: func(context.Context, string, int) ([]model.FeedItem, error) {
				t.Fatal("source fetch should not be called")
				return nil, nil
			},
		},
	}

	n, err := m.FetchOneChannel(context.Background(), "twitter_sample_user")
	if err != nil {
		t.Fatalf("FetchOneChannel: %v", err)
	}
	if n != 2 {
		t.Fatalf("upserted = %d, want 2", n)
	}
	if got, err := d.GetFeedItemByTweetID("sample_post_b"); err != nil {
		t.Fatalf("GetFeedItemByTweetID: %v", err)
	} else if got != nil {
		t.Fatalf("detached foreign item was stored: %+v", got)
	}
}

func TestFetchOneChannelRecordsFailureBackoff(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		downloader:    testDownloader(),
		avatarRequest: make(chan string, 1),
		xFeedFetcher: fakeXFeedFetcher{
			timeline: func(context.Context, string, int) ([]model.FeedItem, error) {
				return nil, errors.New("HTTP 429: Too Many Requests")
			},
			source: func(context.Context, string, int) ([]model.FeedItem, error) {
				t.Fatal("source fetch should not be called")
				return nil, nil
			},
		},
	}

	if _, err := m.FetchOneChannel(context.Background(), "twitter_sample_user"); err == nil {
		t.Fatal("expected fetch error")
	}
	state, err := d.GetIngestState("twitter_sample_user")
	if err != nil {
		t.Fatalf("GetIngestState: %v", err)
	}
	if state.FailCount != 1 || !strings.Contains(state.LastError, "Too Many Requests") {
		t.Fatalf("state = %+v", state)
	}
	if state.NextRetryAt <= float64(time.Now().Unix()) {
		t.Fatalf("next_retry_at = %f, want future", state.NextRetryAt)
	}
}

func TestRunIngestCycleSweepsRepliesWhenNoChannelsDue(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.AddChannel(model.Channel{
		ChannelID:    "twitter_sample_source",
		SourceID:     "sample_source",
		Name:         "sample_source",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	now := time.Now().UTC()
	if err := d.RecordIngestSuccess("twitter_sample_source", float64(now.Unix()), 0); err != nil {
		t.Fatalf("RecordIngestSuccess: %v", err)
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "sample_known_leaf",
		AuthorHandle:  "sample_author_alpha",
		IsReply:       true,
		ReplyToHandle: "sample_author_beta",
		ReplyToStatus: "sample_missing_parent",
		PublishedAt:   &now,
		FetchedAt:     now,
		ContentHash:   "sample-known-leaf",
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	fixtures := map[string]string{
		"/sample_author_beta/status/sample_missing_parent": tweetFixture("sample_missing_parent", "sample_author_beta", "parent body", "", ""),
	}
	srv := httptest.NewServer(fxtMockHandler(t, fixtures))
	defer srv.Close()

	m := NewManager(d, testCfg(t.TempDir()))
	m.replyResolver = NewReplyResolver(d, &fxtwitter.Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second})
	m.xFeedFetcher = fakeXFeedFetcher{
		timeline: func(context.Context, string, int) ([]model.FeedItem, error) {
			t.Fatal("timeline fetch should not be called when channel is not due")
			return nil, nil
		},
		source: func(context.Context, string, int) ([]model.FeedItem, error) {
			t.Fatal("source fetch should not be called")
			return nil, nil
		},
	}

	m.runIngestCycle(context.Background())

	parent, err := d.GetFeedItemByTweetID("sample_missing_parent")
	if err != nil {
		t.Fatalf("GetFeedItemByTweetID: %v", err)
	}
	if parent == nil || !parent.IsGhost || parent.BodyText != "parent body" {
		t.Fatalf("parent ghost = %+v", parent)
	}
}

func TestRunIngestCycleSweepsRepliesBeforeFetchingReadyChannels(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.AddChannel(model.Channel{
		ChannelID:    "twitter_sample_source",
		SourceID:     "sample_source",
		Name:         "sample_source",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	now := time.Now().UTC()
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "sample_ready_leaf",
		AuthorHandle:  "sample_author_alpha",
		IsReply:       true,
		ReplyToHandle: "sample_author_beta",
		ReplyToStatus: "sample_ready_parent",
		PublishedAt:   &now,
		FetchedAt:     now,
		ContentHash:   "sample-ready-leaf",
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	fixtures := map[string]string{
		"/sample_author_beta/status/sample_ready_parent": tweetFixture("sample_ready_parent", "sample_author_beta", "parent body", "", ""),
	}
	srv := httptest.NewServer(fxtMockHandler(t, fixtures))
	defer srv.Close()

	m := NewManager(d, testCfg(t.TempDir()))
	m.replyResolver = NewReplyResolver(d, &fxtwitter.Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second})
	m.xFeedFetcher = fakeXFeedFetcher{
		timeline: func(context.Context, string, int) ([]model.FeedItem, error) {
			parent, err := d.GetFeedItemByTweetID("sample_ready_parent")
			if err != nil {
				t.Fatalf("GetFeedItemByTweetID: %v", err)
			}
			if parent == nil {
				t.Fatal("reply sweep did not run before ready channel fetch")
			}
			return nil, nil
		},
		source: func(context.Context, string, int) ([]model.FeedItem, error) {
			t.Fatal("source fetch should not be called")
			return nil, nil
		},
	}

	m.runIngestCycle(context.Background())
}

func TestXStatusEnrichmentUpsertsMissingQuoteParent(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:  d,
		cfg: testCfg(t.TempDir()),
		xFeedFetcher: fakeXFeedFetcher{
			status: func(_ context.Context, handle, tweetID string) (xfeed.ParseResult, error) {
				if handle != "sample_source" || tweetID != "9000000000000000100" {
					t.Fatalf("status fetch = %s/%s", handle, tweetID)
				}
				return xfeed.ParseResult{Items: []model.FeedItem{{
					TweetID:           "9000000000000000100",
					SourceHandle:      "sample_source",
					AuthorHandle:      "sample_source",
					BodyText:          "parent quote text",
					QuoteTweetID:      "9000000000000000200",
					QuoteAuthorHandle: "sample_quote",
					QuoteBodyText:     "quoted text",
					ContentHash:       "hash-parent",
					CanonicalTweetID:  "9000000000000000100",
				}}}, nil
			},
		},
	}

	m.runOneXStatusEnrichment(context.Background(), xfeed.StatusEnrichmentRequest{
		Kind: xfeed.StatusEnrichmentMissingQuoteParent,
		Ref:  xfeed.StatusRef{Handle: "sample_source", TweetID: "9000000000000000100"},
	})

	got, err := d.GetFeedItemByTweetID("9000000000000000100")
	if err != nil {
		t.Fatalf("GetFeedItemByTweetID: %v", err)
	}
	if got == nil || got.QuoteTweetID != "9000000000000000200" || got.QuoteBodyText != "quoted text" {
		t.Fatalf("parent enrichment = %+v", got)
	}
}

func TestXStatusEnrichmentFillsRetweetQuoteFields(t *testing.T) {
	d := newTestWorkerDB(t)
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:          "9000000000000000400",
		SourceHandle:     "sample_source",
		AuthorHandle:     "sample_author",
		BodyText:         "original parent text",
		IsRetweet:        true,
		CanonicalTweetID: "9000000000000000300",
		ContentHash:      "hash-retweet",
	}}); err != nil {
		t.Fatalf("seed retweet: %v", err)
	}
	m := &Manager{
		db:  d,
		cfg: testCfg(t.TempDir()),
		xFeedFetcher: fakeXFeedFetcher{
			status: func(_ context.Context, handle, tweetID string) (xfeed.ParseResult, error) {
				if handle != "sample_author" || tweetID != "9000000000000000300" {
					t.Fatalf("status fetch = %s/%s", handle, tweetID)
				}
				return xfeed.ParseResult{Items: []model.FeedItem{{
					TweetID:           "9000000000000000300",
					SourceHandle:      "sample_author",
					AuthorHandle:      "sample_author",
					BodyText:          "original parent text",
					QuoteTweetID:      "9000000000000000500",
					QuoteAuthorHandle: "sample_quote",
					QuoteBodyText:     "quote fallback text",
					ContentHash:       "hash-original",
					CanonicalTweetID:  "9000000000000000300",
				}}}, nil
			},
		},
	}

	m.runOneXStatusEnrichment(context.Background(), xfeed.StatusEnrichmentRequest{
		Kind:          xfeed.StatusEnrichmentRetweetQuote,
		Ref:           xfeed.StatusRef{Handle: "sample_author", TweetID: "9000000000000000300"},
		TargetTweetID: "9000000000000000400",
	})

	got, err := d.GetFeedItemByTweetID("9000000000000000400")
	if err != nil {
		t.Fatalf("GetFeedItemByTweetID: %v", err)
	}
	if got == nil || got.QuoteTweetID != "9000000000000000500" || got.QuoteBodyText != "quote fallback text" {
		t.Fatalf("retweet enrichment = %+v", got)
	}
}

func TestFetchOneFeedSourceRecordsAttribution(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.UpsertFeedSource(model.FeedSource{
		SourceID:   "twitter_list_sample",
		Platform:   "twitter",
		SourceType: "list",
		ExternalID: "sample",
		Label:      "Sample list",
		URL:        "https://x.com/i/lists/123",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("UpsertFeedSource: %v", err)
	}
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		downloader:    testDownloader(),
		avatarRequest: make(chan string, 1),
		xFeedFetcher: fakeXFeedFetcher{
			timeline: func(context.Context, string, int) ([]model.FeedItem, error) {
				t.Fatal("timeline fetch should not be called")
				return nil, nil
			},
			source: func(_ context.Context, rawURL string, limit int) ([]model.FeedItem, error) {
				if rawURL != "https://x.com/i/lists/123" {
					t.Fatalf("rawURL = %q", rawURL)
				}
				if limit != 100 {
					t.Fatalf("limit = %d, want 100", limit)
				}
				return []model.FeedItem{{
					TweetID:          "1000000000000000200",
					SourceHandle:     "source_author",
					AuthorHandle:     "source_author",
					BodyText:         "list post",
					CanonicalURL:     "https://x.com/source_author/status/1000000000000000200",
					ContentHash:      "hash2",
					CanonicalTweetID: "1000000000000000200",
				}}, nil
			},
		},
	}

	n, err := m.FetchOneFeedSource(context.Background(), "twitter_list_sample")
	if err != nil {
		t.Fatalf("FetchOneFeedSource: %v", err)
	}
	if n != 1 {
		t.Fatalf("upserted = %d, want 1", n)
	}
	items, err := d.ListFeedItemsBySourceID("twitter_list_sample", 10)
	if err != nil {
		t.Fatalf("ListFeedItemsBySourceID: %v", err)
	}
	if len(items) != 1 || items[0].TweetID != "1000000000000000200" {
		t.Fatalf("source items = %+v", items)
	}
}
