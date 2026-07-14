package db

import (
	"context"
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

func TestGetThreadTreeReturnsRootAndAllReplyBranches(t *testing.T) {
	d := openWritableTestDB(t)
	rootAt := time.Unix(100, 0).UTC()
	bAt := time.Unix(110, 0).UTC()
	bChildAt := time.Unix(120, 0).UTC()
	bSiblingAt := time.Unix(125, 0).UTC()
	cAt := time.Unix(130, 0).UTC()
	cChildAt := time.Unix(140, 0).UTC()
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "root", AuthorHandle: "sample_author_a", BodyText: "root", PublishedAt: &rootAt, FetchedAt: rootAt, ContentHash: "h_root"},
		{TweetID: "reply_b", AuthorHandle: "sample_author_b", BodyText: "reply b", IsReply: true, ReplyToHandle: "sample_author_a", ReplyToStatus: "root", PublishedAt: &bAt, FetchedAt: bAt, ContentHash: "h_b"},
		{TweetID: "reply_b_child", AuthorHandle: "sample_author_d", BodyText: "reply b child", IsReply: true, ReplyToHandle: "sample_author_b", ReplyToStatus: "reply_b", PublishedAt: &bChildAt, FetchedAt: bChildAt, ContentHash: "h_b_child"},
		{TweetID: "reply_b_sibling", AuthorHandle: "sample_author_f", BodyText: "reply b sibling", IsReply: true, ReplyToHandle: "sample_author_b", ReplyToStatus: "reply_b", PublishedAt: &bSiblingAt, FetchedAt: bSiblingAt, ContentHash: "h_b_sibling"},
		{TweetID: "reply_c", AuthorHandle: "sample_author_c", BodyText: "reply c", IsReply: true, ReplyToHandle: "sample_author_a", ReplyToStatus: "root", PublishedAt: &cAt, FetchedAt: cAt, ContentHash: "h_c"},
		{TweetID: "reply_c_child", AuthorHandle: "sample_author_e", BodyText: "reply c child", IsReply: true, ReplyToHandle: "sample_author_c", ReplyToStatus: "reply_c", PublishedAt: &cChildAt, FetchedAt: cChildAt, ContentHash: "h_c_child"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tree, err := d.GetThreadTree("reply_b_child")
	if err != nil {
		t.Fatalf("GetThreadTree: %v", err)
	}
	gotIDs := make([]string, 0, len(tree))
	gotDepths := make([]int, 0, len(tree))
	for _, item := range tree {
		gotIDs = append(gotIDs, item.TweetID)
		gotDepths = append(gotDepths, item.ThreadDepth)
	}
	wantIDs := []string{"root", "reply_b", "reply_b_child", "reply_b_sibling", "reply_c", "reply_c_child"}
	wantDepths := []int{0, 1, 2, 2, 1, 2}
	if !equalStringSlices(gotIDs, wantIDs) {
		t.Fatalf("tree IDs = %v, want %v", gotIDs, wantIDs)
	}
	if !equalIntSlices(gotDepths, wantDepths) {
		t.Fatalf("tree depths = %v, want %v", gotDepths, wantDepths)
	}
}

func TestListIncompleteReplyChainsFindsMissingAncestor(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "sample_leaf", AuthorHandle: "sample_author", IsReply: true, ReplyToHandle: "sample_parent", ReplyToStatus: "sample_parent", PublishedAt: &now, FetchedAt: now, ContentHash: "sample_leaf"},
		{TweetID: "sample_parent", AuthorHandle: "sample_parent", IsReply: true, ReplyToHandle: "sample_root", ReplyToStatus: "", PublishedAt: &now, FetchedAt: now, ContentHash: "sample_parent"},
		{TweetID: "sample_user", AuthorHandle: "sample_user", IsReply: false, PublishedAt: &now, FetchedAt: now, ContentHash: "sample_user"},
	})
	got, err := d.ListIncompleteReplyChainsContext(
		context.Background(), []string{"sample_leaf", "sample_user"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SeedTweetID != "sample_leaf" || got[0].Item.TweetID != "sample_parent" {
		t.Fatalf("incomplete chains = %+v", got)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
