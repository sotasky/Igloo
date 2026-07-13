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

func TestUpsertFeedItemsDeclaresDirectAndQuoteAssetsWithoutLegacyJobs(t *testing.T) {
	d := newTestWorkerDB(t)
	items := []model.FeedItem{{
		TweetID:        "sample_outer",
		SourceHandle:   "sample_source",
		AuthorHandle:   "sample_author",
		MediaJSON:      `[{"url":"https://cdn.example/direct.jpg","type":"photo"}]`,
		QuoteTweetID:   "sample_quote",
		QuoteMediaJSON: `[{"url":"https://cdn.example/quote.jpg","type":"photo"},{"url":"https://cdn.example/quote.mp4","type":"video","thumbnail_url":"https://cdn.example/quote-thumb.jpg"}]`,
	}}
	if n, err := d.UpsertFeedItems(items); err != nil || n != 1 {
		t.Fatalf("UpsertFeedItems = (%d, %v), want (1, nil)", n, err)
	}

	for _, expected := range []struct {
		owner string
		kind  string
		index int
	}{
		{"sample_outer", "post_media", 0},
		{"sample_quote", "post_media", 0},
		{"sample_quote", "post_media", 1},
		{"sample_quote", "post_thumbnail", 0},
	} {
		asset, err := d.GetAsset(db.BuildAssetID("twitter", "tweet", expected.owner, expected.kind, expected.index), expected.kind)
		if err != nil {
			t.Fatalf("GetAsset %s/%s/%d: %v", expected.owner, expected.kind, expected.index, err)
		}
		if asset == nil || asset.State != db.AssetStateQueued || asset.SourceURL == "" {
			t.Fatalf("asset %s/%s/%d = %+v", expected.owner, expected.kind, expected.index, asset)
		}
	}
}

func TestFetchOneChannelUsesXFeedFetcherAndQueuesMedia(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:         d,
		cfg:        testCfg(t.TempDir()),
		downloader: testDownloader(),
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
	queued, processing, err := d.CountPendingXContentDownloads()
	if err != nil {
		t.Fatalf("CountPendingXContentDownloads: %v", err)
	}
	if queued+processing != 1 {
		t.Fatalf("media jobs = queued %d processing %d, want 1 total", queued, processing)
	}
}

func TestFetchOneChannelDropsDetachedForeignTimelineItems(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:         d,
		cfg:        testCfg(t.TempDir()),
		downloader: testDownloader(),
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
		db:         d,
		cfg:        testCfg(t.TempDir()),
		downloader: testDownloader(),
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

func TestRunIngestCycleDoesNotSweepHistoricalRepliesWhenNoChannelsDue(t *testing.T) {
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
		AuthorHandle:  "sample_alpha",
		IsReply:       true,
		ReplyToHandle: "sample_beta",
		ReplyToStatus: "sample_missing_parent",
		PublishedAt:   &now,
		FetchedAt:     now,
		ContentHash:   "sample-known-leaf",
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	candidates, err := d.FindUnresolvedReplies(10)
	if err != nil {
		t.Fatalf("FindUnresolvedReplies: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ReplyToHandle != "sample_beta" {
		t.Fatalf("unresolved reply candidates = %#v", candidates)
	}

	fixtures := map[string]string{
		"/sample_beta/status/sample_missing_parent": tweetFixture("sample_missing_parent", "sample_beta", "parent body", "", ""),
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
	if parent != nil {
		t.Fatalf("historical reply was swept during an unrelated cycle: %+v", parent)
	}
}

func TestRunIngestCycleDoesNotSweepHistoricalRepliesBeforeReadyChannels(t *testing.T) {
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
		AuthorHandle:  "sample_alpha",
		IsReply:       true,
		ReplyToHandle: "sample_beta",
		ReplyToStatus: "sample_ready_parent",
		PublishedAt:   &now,
		FetchedAt:     now,
		ContentHash:   "sample-ready-leaf",
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	fixtures := map[string]string{
		"/sample_beta/status/sample_ready_parent": tweetFixture("sample_ready_parent", "sample_beta", "parent body", "", ""),
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
			if parent != nil {
				t.Fatal("historical reply sweep ran before ready channel fetch")
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
		db:         d,
		cfg:        testCfg(t.TempDir()),
		downloader: testDownloader(),
		xFeedFetcher: fakeXFeedFetcher{
			timeline: func(context.Context, string, int) ([]model.FeedItem, error) {
				t.Fatal("timeline fetch should not be called")
				return nil, nil
			},
			source: func(_ context.Context, rawURL string, limit int) ([]model.FeedItem, error) {
				if rawURL != "https://x.com/i/lists/123" {
					t.Fatalf("rawURL = %q", rawURL)
				}
				if limit != 20 {
					t.Fatalf("limit = %d, want 20", limit)
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

func TestFilterReadyXFeedSourcesUsesLastChecked(t *testing.T) {
	now := time.Unix(10_000, 0)
	recent := now.Add(-10 * time.Minute)
	stale := now.Add(-31 * time.Minute)
	sources := []model.FeedSource{
		{SourceID: "twitter_list_sample_alpha", Enabled: true},
		{SourceID: "twitter_list_sample_beta", Enabled: true, LastChecked: &recent, LastOK: &recent},
		{SourceID: "twitter_list_sample_gamma", Enabled: true, LastChecked: &recent, LastError: "temporary"},
		{SourceID: "twitter_list_sample_delta", Enabled: true, LastChecked: &stale, LastOK: &stale},
	}

	ready, notDue := filterReadyXFeedSources(sources, 30*time.Minute, now)
	if notDue != 2 {
		t.Fatalf("not due = %d, want 2", notDue)
	}
	if len(ready) != 2 || ready[0].SourceID != "twitter_list_sample_alpha" || ready[1].SourceID != "twitter_list_sample_delta" {
		t.Fatalf("ready sources = %#v", ready)
	}
}

func TestFetchOneFeedSourceUsesGlobalLimitAndPrunesPreviousWindow(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.SetSetting("media_download_limit_default", "1"); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordAndroidFeedRetention(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertFeedSource(model.FeedSource{
		SourceID: "twitter_list_sample", Platform: "twitter", SourceType: "list",
		ExternalID: "bounded", Label: "Bounded", URL: "https://x.com/i/lists/bounded", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Unix(100, 0)
	old := model.FeedItem{
		TweetID: "sample_source_old", SourceHandle: "sample_old", AuthorHandle: "sample_old",
		MediaJSON:   `[{"url":"https://cdn.example/old.jpg","type":"photo"}]`,
		PublishedAt: &oldTime,
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{old}); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordFeedItemSources(old.TweetID, []string{"twitter_list_sample"}); err != nil {
		t.Fatal(err)
	}

	newTime := time.Unix(200, 0)
	m := &Manager{
		db: d, cfg: testCfg(t.TempDir()), downloader: testDownloader(),
		xFeedFetcher: fakeXFeedFetcher{
			timeline: func(context.Context, string, int) ([]model.FeedItem, error) {
				t.Fatal("timeline fetch should not be called")
				return nil, nil
			},
			source: func(_ context.Context, _ string, limit int) ([]model.FeedItem, error) {
				if limit != 1 {
					t.Fatalf("source fetch limit = %d, want 1", limit)
				}
				return []model.FeedItem{{
					TweetID: "sample_source_new", SourceHandle: "sample_new", AuthorHandle: "sample_new",
					MediaJSON: `[{"url":"https://cdn.example/new.jpg","type":"photo"}]`, PublishedAt: &newTime,
				}}, nil
			},
		},
	}
	if n, err := m.FetchOneFeedSource(context.Background(), "twitter_list_sample"); err != nil || n != 1 {
		t.Fatalf("FetchOneFeedSource = (%d, %v)", n, err)
	}
	items, err := d.ListFeedItemsBySourceID("twitter_list_sample", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TweetID != "sample_source_new" {
		t.Fatalf("bounded source items = %+v", items)
	}
	asset, err := d.GetAsset(db.BuildAssetID("twitter", "tweet", old.TweetID, "post_media", 0), "post_media")
	if err != nil {
		t.Fatal(err)
	}
	if asset == nil || asset.State != db.AssetStatePruned {
		t.Fatalf("old source asset = %+v", asset)
	}
}

func TestEnforceXMediaLimitsForItemsOnlyTouchesEffectiveSources(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.RecordAndroidFeedRetention(0, 1); err != nil {
		t.Fatal(err)
	}
	for _, handle := range []string{"sample_alpha", "sample_beta", "sample_source", "sample_gamma"} {
		channelID := model.TwitterChannelIDFromHandle(handle)
		if err := d.AddChannel(model.Channel{
			ChannelID: channelID, SourceID: handle, Name: "Sample Channel",
			Platform: "twitter", IsSubscribed: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := d.UpdateChannelSettings(channelID, map[string]any{"media_download_limit": 1}); err != nil {
			t.Fatal(err)
		}
	}
	item := func(tweetID, sourceHandle, reposterHandle string, isRetweet bool, publishedAtMs int64) model.FeedItem {
		publishedAt := time.UnixMilli(publishedAtMs)
		return model.FeedItem{
			TweetID: tweetID, SourceHandle: sourceHandle, AuthorHandle: "sample_author",
			IsRetweet: isRetweet, RetweetedByHandle: reposterHandle,
			MediaJSON:   `[{"url":"https://cdn.example/` + tweetID + `.jpg","type":"photo"}]`,
			PublishedAt: &publishedAt,
		}
	}
	items := []model.FeedItem{
		item("sample_alpha_new", "sample_alpha", "", false, 200),
		item("sample_alpha_old", "sample_alpha", "", false, 100),
		item("sample_beta_new", "sample_source", "sample_beta", true, 200),
		item("sample_beta_old", "sample_source", "sample_beta", true, 100),
		item("sample_source_new", "sample_source", "", false, 200),
		item("sample_source_old", "sample_source", "", false, 100),
		item("sample_gamma_new", "sample_gamma", "", true, 200),
		item("sample_gamma_old", "sample_gamma", "", true, 100),
	}
	if n, err := d.UpsertFeedItems(items); err != nil || n != len(items) {
		t.Fatalf("UpsertFeedItems = (%d, %v), want (%d, nil)", n, err, len(items))
	}

	m := &Manager{db: d}
	m.enforceXMediaLimitsForItems([]model.FeedItem{items[0], items[1], items[2], items[6]})

	for ownerID, wantState := range map[string]string{
		"sample_alpha_old":  db.AssetStatePruned,
		"sample_beta_old":   db.AssetStatePruned,
		"sample_gamma_old":  db.AssetStatePruned,
		"sample_source_old": db.AssetStateQueued,
	} {
		asset, err := d.GetAsset(db.BuildAssetID("twitter", "tweet", ownerID, "post_media", 0), "post_media")
		if err != nil {
			t.Fatal(err)
		}
		if asset == nil || asset.State != wantState {
			t.Fatalf("asset %s = %+v, want state %s", ownerID, asset, wantState)
		}
	}
}

func TestXTimelineLimitUsesMediaRetentionSetting(t *testing.T) {
	settings := &db.ChannelSettings{MediaDownloadLimit: 7, MaxVideos: 200}
	if got := xTimelineLimit(settings); got != 7 {
		t.Fatalf("x timeline limit = %d, want 7", got)
	}
}

func TestTriggerChannelCheckRoutesXThroughIngestState(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.AddChannel(model.Channel{
		ChannelID: "twitter_sample_source", SourceID: "sample_source", Name: "Refresh Source",
		Platform: "twitter", IsSubscribed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordIngestSuccess("twitter_sample_source", float64(time.Now().Unix()), 0); err != nil {
		t.Fatal(err)
	}
	m := NewManager(d, testCfg(t.TempDir()))
	m.TriggerChannelCheck("twitter_sample_source")
	state, err := d.GetIngestState("twitter_sample_source")
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSuccessAt != 0 {
		t.Fatalf("last_success_at = %f, want 0", state.LastSuccessAt)
	}
	if len(m.ingestKick) != 1 {
		t.Fatalf("ingest kicks = %d, want 1", len(m.ingestKick))
	}
}
