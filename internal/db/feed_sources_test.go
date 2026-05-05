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

	var storedSourceHandle string
	if err := d.QueryRow(`SELECT source_handle FROM feed_items WHERE tweet_id = 'tweet_list_1'`).Scan(&storedSourceHandle); err != nil {
		t.Fatalf("read source_handle: %v", err)
	}
	if storedSourceHandle != "real_author" {
		t.Fatalf("source_handle = %q, want real author handle", storedSourceHandle)
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
