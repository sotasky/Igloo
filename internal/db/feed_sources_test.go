package db

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestFeedSourcesPersistAndTrackAttribution(t *testing.T) {
	d := openWritableTestDB(t)

	source := model.FeedSource{
		SourceID:   "twitter_list_12345",
		Platform:   "twitter",
		SourceType: "list",
		ExternalID: "12345",
		Label:      "Test List",
		URL:        "https://x.com/i/lists/12345",
		Enabled:    true,
		CreatedAt:  time.Unix(100, 0),
		UpdatedAt:  time.Unix(100, 0),
	}
	if err := d.UpsertFeedSource(source); err != nil {
		t.Fatalf("UpsertFeedSource: %v", err)
	}

	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "tweet_list_1",
		SourceHandle: "real_author",
		AuthorHandle: "real_author",
		BodyText:     "from a list",
		PublishedAt:  feedSourceTestTime(time.Unix(90, 0)),
		FetchedAt:    time.Unix(101, 0),
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.RecordFeedItemSources("tweet_list_1", []string{"twitter_list_12345"}); err != nil {
		t.Fatalf("RecordFeedItemSources: %v", err)
	}

	sources, err := d.ListFeedSources("twitter")
	if err != nil {
		t.Fatalf("ListFeedSources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d", len(sources))
	}
	got := sources[0]
	if got.SourceID != source.SourceID || got.SourceType != "list" || got.ItemCount != 1 {
		t.Fatalf("source = %#v", got)
	}

	var storedSourceID string
	if err := d.QueryRow(`SELECT source_channel_id FROM feed_items WHERE tweet_id = 'tweet_list_1'`).Scan(&storedSourceID); err != nil {
		t.Fatalf("read source_channel_id: %v", err)
	}
	if storedSourceID != "twitter_real_author" {
		t.Fatalf("source_channel_id = %q", storedSourceID)
	}
}

func TestListFeedItemsBySourceIDReturnsAttributedItems(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.UpsertFeedSource(model.FeedSource{
		SourceID:   "twitter_list_demo",
		Platform:   "twitter",
		SourceType: "list",
		ExternalID: "demo",
		Label:      "Demo List",
		URL:        "https://x.com/i/lists/demo",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("UpsertFeedSource: %v", err)
	}

	first := time.Unix(100, 0)
	second := time.Unix(200, 0)
	items := []model.FeedItem{
		{
			TweetID:      "tweet_source_original",
			SourceHandle: "source_owner",
			AuthorHandle: "original_author",
			BodyText:     "original item",
			PublishedAt:  &first,
			FetchedAt:    time.Unix(300, 0),
		},
		{
			TweetID:           "tweet_source_retweet",
			SourceHandle:      "source_owner",
			AuthorHandle:      "retweeted_author",
			BodyText:          "retweet wrapper",
			IsRetweet:         true,
			RetweetedByHandle: "source_owner",
			CanonicalTweetID:  "tweet_original_elsewhere",
			PublishedAt:       &second,
			FetchedAt:         time.Unix(300, 0),
		},
		{
			TweetID:      "tweet_unattributed",
			SourceHandle: "source_owner",
			AuthorHandle: "other_author",
			BodyText:     "not in the source",
			PublishedAt:  &second,
			FetchedAt:    time.Unix(300, 0),
		},
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	for _, tweetID := range []string{"tweet_source_original", "tweet_source_retweet"} {
		if err := d.RecordFeedItemSources(tweetID, []string{"twitter_list_demo"}); err != nil {
			t.Fatalf("RecordFeedItemSources(%s): %v", tweetID, err)
		}
	}

	got, err := d.ListFeedItemsBySourceID("twitter_list_demo", 10)
	if err != nil {
		t.Fatalf("ListFeedItemsBySourceID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].TweetID != "tweet_source_retweet" || !got[0].IsRetweet {
		t.Fatalf("first item = %#v, want retweet wrapper first", got[0])
	}
	if got[1].TweetID != "tweet_source_original" {
		t.Fatalf("second tweet_id = %q, want tweet_source_original", got[1].TweetID)
	}
}

func feedSourceTestTime(t time.Time) *time.Time {
	return &t
}

func TestDeleteFeedSourceRemovesAttribution(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.UpsertFeedSource(model.FeedSource{
		SourceID:   "twitter_community_98765",
		Platform:   "twitter",
		SourceType: "community",
		ExternalID: "98765",
		Label:      "Test Community",
		URL:        "https://x.com/i/communities/98765",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("UpsertFeedSource: %v", err)
	}
	if err := d.RecordFeedItemSources("tweet_community_1", []string{"twitter_community_98765"}); err != nil {
		t.Fatalf("RecordFeedItemSources: %v", err)
	}
	if err := d.DeleteFeedSource("twitter_community_98765"); err != nil {
		t.Fatalf("DeleteFeedSource: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_item_sources WHERE source_id = 'twitter_community_98765'`).Scan(&count); err != nil {
		t.Fatalf("count attribution: %v", err)
	}
	if count != 0 {
		t.Fatalf("attribution count = %d", count)
	}
}
