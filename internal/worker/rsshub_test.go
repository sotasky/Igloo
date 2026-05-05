package worker

import (
	"context"
	"testing"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

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
