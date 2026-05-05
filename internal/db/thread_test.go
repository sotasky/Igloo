package db

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestGetFeedItemByTweetID(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "1000000000000000001",
		AuthorHandle: "user_alpha",
		BodyText:     "hello",
		PublishedAt:  &now,
		FetchedAt:    now,
		ContentHash:  "h_abc",
	}})

	got, err := d.GetFeedItemByTweetID("1000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TweetID != "1000000000000000001" {
		t.Fatalf("got %+v", got)
	}

	missing, err := d.GetFeedItemByTweetID("9999999999999999999")
	if err != nil {
		t.Fatalf("missing should not error: %v", err)
	}
	if missing != nil {
		t.Errorf("missing should be nil, got %+v", missing)
	}
}

func TestUpsertGhostFeedItem(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()

	err := d.UpsertGhostFeedItem(model.FeedItem{
		TweetID:           "1000000000000000000",
		AuthorHandle:      "user_beta",
		AuthorDisplayName: "User Beta",
		AuthorAvatarURL:   "https://example.com/b.jpg",
		BodyText:          "parent tweet text",
		Lang:              "en",
		PublishedAt:       &now,
		FetchedAt:         now,
		ContentHash:       "h_ghost_def",
	})
	if err != nil {
		t.Fatalf("UpsertGhostFeedItem: %v", err)
	}

	got, err := d.GetFeedItemByTweetID("1000000000000000000")
	if err != nil || got == nil {
		t.Fatalf("ghost not found: %v", err)
	}
	if !got.IsGhost {
		t.Error("IsGhost should be true on retrieved ghost row")
	}
	if got.AuthorHandle != "user_beta" {
		t.Errorf("AuthorHandle: got %q", got.AuthorHandle)
	}
}

func TestUpdateReplyToStatus(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "1000000000000000010",
		AuthorHandle:  "user_alpha",
		BodyText:      "reply",
		IsReply:       true,
		ReplyToHandle: "user_beta",
		ReplyToStatus: "",
		PublishedAt:   &now,
		FetchedAt:     now,
		ContentHash:   "h_xyz",
	}})

	if err := d.UpdateReplyToStatus("1000000000000000010", "1000000000000000000"); err != nil {
		t.Fatalf("UpdateReplyToStatus: %v", err)
	}
	got, _ := d.GetFeedItemByTweetID("1000000000000000010")
	if got.ReplyToStatus != "1000000000000000000" {
		t.Errorf("ReplyToStatus: got %q", got.ReplyToStatus)
	}
}

func TestGetThreadChain(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()

	// Build a 3-deep chain: root (1) ← reply (2) ← reply (3, the leaf)
	_, _ = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "1000000000000000001", AuthorHandle: "user_alpha", BodyText: "root", PublishedAt: &now, FetchedAt: now, ContentHash: "h_t1"},
		{TweetID: "1000000000000000002", AuthorHandle: "user_beta", BodyText: "mid", IsReply: true, ReplyToHandle: "user_alpha", ReplyToStatus: "1000000000000000001", PublishedAt: &now, FetchedAt: now, ContentHash: "h_t2"},
		{TweetID: "1000000000000000003", AuthorHandle: "user_alpha", BodyText: "leaf", IsReply: true, ReplyToHandle: "user_beta", ReplyToStatus: "1000000000000000002", PublishedAt: &now, FetchedAt: now, ContentHash: "h_t3"},
	})

	chain, err := d.GetThreadChain("1000000000000000003")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain length: got %d, want 3, chain=%+v", len(chain), chain)
	}
	want := []string{"1000000000000000001", "1000000000000000002", "1000000000000000003"}
	for i, item := range chain {
		if item.TweetID != want[i] {
			t.Errorf("chain[%d].TweetID: got %q, want %q", i, item.TweetID, want[i])
		}
	}
}

func TestGetThreadChainMissingParent(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "1000000000000000020",
		AuthorHandle:  "user_alpha",
		BodyText:      "orphan reply",
		IsReply:       true,
		ReplyToHandle: "user_xenon",
		ReplyToStatus: "9999999999999999999",
		PublishedAt:   &now,
		FetchedAt:     now,
		ContentHash:   "h_or1",
	}})
	chain, err := d.GetThreadChain("1000000000000000020")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 || chain[0].TweetID != "1000000000000000020" {
		t.Errorf("expected single-item chain with leaf only, got %+v", chain)
	}
}

func TestFindUnresolvedReplies(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "u_1", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_beta", ReplyToStatus: "", PublishedAt: &now, FetchedAt: now, ContentHash: "h_u1"},
		{TweetID: "u_2", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_beta", ReplyToStatus: "999", PublishedAt: &now, FetchedAt: now, ContentHash: "h_u2"},
		{TweetID: "u_3", AuthorHandle: "user_alpha", IsReply: false, PublishedAt: &now, FetchedAt: now, ContentHash: "h_u3"},
	})
	got, err := d.FindUnresolvedReplies(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TweetID != "u_1" {
		t.Errorf("expected one unresolved reply with tweet_id=u_1, got %+v", got)
	}
}
