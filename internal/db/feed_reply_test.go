package db

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestUpsertFeedItemReplyFlags(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC()
	items := []model.FeedItem{{
		TweetID:       "1000000000000000001",
		AuthorHandle:  "user_alpha",
		BodyText:      "Hello",
		IsReply:       true,
		ReplyToHandle: "user_beta",
		ReplyToStatus: "",
		PublishedAt:   &now,
		FetchedAt:     now,
		ContentHash:   "hash_reply_one",
	}}
	n, err := d.UpsertFeedItems(items)
	if err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if n != 1 {
		t.Errorf("upserted: got %d, want 1", n)
	}

	got, err := d.GetFeedItemsForTweetIDs([]string{"1000000000000000001"})
	if err != nil {
		t.Fatalf("GetFeedItemsForTweetIDs: %v", err)
	}
	fi, ok := got["1000000000000000001"]
	if !ok {
		t.Fatal("row not found after upsert")
	}
	if !fi.IsReply {
		t.Error("IsReply: got false, want true")
	}
	if fi.IsGhost {
		t.Error("IsGhost: got true, want false")
	}
	if fi.ReplyToHandle != "user_beta" {
		t.Errorf("ReplyToHandle: got %q, want user_beta", fi.ReplyToHandle)
	}
}

func TestUpsertFeedItemPromotesGhostToReal(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()

	// First insert as ghost (simulates fxtwitter-fetched parent).
	_, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "2000000000000000001",
		AuthorHandle: "user_gamma",
		BodyText:     "ghost body",
		IsGhost:      true,
		PublishedAt:  &now,
		FetchedAt:    now,
		ContentHash:  "hash_ghost_one",
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Then re-ingest the same tweet via normal RSS (is_ghost=0).
	_, err = d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "2000000000000000001",
		AuthorHandle: "user_gamma",
		BodyText:     "real body from RSS",
		IsGhost:      false,
		PublishedAt:  &now,
		FetchedAt:    now,
		ContentHash:  "hash_ghost_one",
	}})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := d.GetFeedItemsForTweetIDs([]string{"2000000000000000001"})
	fi := got["2000000000000000001"]
	if fi.IsGhost {
		t.Error("ghost row should be promoted to non-ghost after real ingest")
	}
	if fi.BodyText != "real body from RSS" {
		t.Errorf("BodyText not updated on real ingest: %q", fi.BodyText)
	}
}
