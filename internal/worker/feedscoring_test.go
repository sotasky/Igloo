package worker

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
)

func TestRankSnapshotPublishesOnlyCompleteReplyChains(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	if _, err := d.MutateFollow("twitter_sample_source", "set", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	items := []model.FeedItem{
		{
			TweetID: "sample_complete", SourceHandle: "sample_source", AuthorHandle: "sample_source",
			BodyText: "complete", PublishedAt: &now, FetchedAt: now,
			ContentHash: "sample_complete", CanonicalTweetID: "sample_complete",
		},
		{
			TweetID: "sample_reply", SourceHandle: "sample_source", AuthorHandle: "sample_source",
			BodyText: "reply", IsReply: true, ReplyToHandle: "sample_parent_author", ReplyToStatus: "sample_parent",
			PublishedAt: &now, FetchedAt: now, ContentHash: "sample_reply", CanonicalTweetID: "sample_reply",
		},
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateAlgoInterest(map[string]float64{"sample_complete": 10, "sample_reply": 11}); err != nil {
		t.Fatal(err)
	}

	failing := httptest.NewServer(fxtMockHandler(t, nil))
	m := NewManager(d, testCfg(t.TempDir()))
	m.replyResolver = NewReplyResolver(d, &fxtwitter.Client{BaseURL: failing.URL, HTTP: failing.Client(), Timeout: time.Second})
	stats := m.runSnapshotPhaseStats(
		context.Background(), []string{"sample_complete", "sample_reply"}, 0, false,
	)
	failing.Close()
	if stats.replyAttempts != 1 || stats.replyBlocked != 1 {
		t.Fatalf("reply readiness = %d attempts, %d blocked", stats.replyAttempts, stats.replyBlocked)
	}
	var snapshotIDs string
	if err := d.QueryRow(`SELECT COALESCE(GROUP_CONCAT(tweet_id, ','), '') FROM feed_rank_snapshot`).Scan(&snapshotIDs); err != nil {
		t.Fatal(err)
	}
	if snapshotIDs != "sample_complete" {
		t.Fatalf("snapshot with unresolved reply = %q", snapshotIDs)
	}

	resolved := httptest.NewServer(fxtMockHandler(t, map[string]string{
		"/sample_source/status/sample_parent": tweetFixture("sample_parent", "sample_parent_author", "parent", "", ""),
	}))
	defer resolved.Close()
	m.replyResolver = NewReplyResolver(d, &fxtwitter.Client{BaseURL: resolved.URL, HTTP: resolved.Client(), Timeout: time.Second})
	stats = m.runSnapshotPhaseStats(
		context.Background(), []string{"sample_complete", "sample_reply"}, 0, false,
	)
	if stats.replyBlocked != 0 || stats.count != 2 {
		parent, _ := d.GetFeedItemByTweetID("sample_parent")
		t.Fatalf("resolved snapshot = %+v, parent = %+v", stats, parent)
	}
	chain, err := d.GetThreadChain("sample_reply")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || chain[0].TweetID != "sample_parent" || chain[1].TweetID != "sample_reply" {
		t.Fatalf("resolved chain = %+v", chain)
	}
}

func TestRankSnapshotContinuesAReplyChainThatMadeProgress(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	if _, err := d.MutateFollow("twitter_sample_source", "set", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID: "sample_leaf", SourceHandle: "sample_source", AuthorHandle: "sample_source",
		BodyText: "leaf", IsReply: true, ReplyToHandle: "sample_parent_1", ReplyToStatus: "sample_parent_1",
		PublishedAt: &now, FetchedAt: now, ContentHash: "sample_leaf", CanonicalTweetID: "sample_leaf",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateAlgoInterest(map[string]float64{"sample_leaf": 10}); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"/sample_parent_1/status/sample_parent_1": tweetFixture("sample_parent_1", "sample_parent_1", "one", "sample_parent_2", "sample_parent_2"),
		"/sample_parent_2/status/sample_parent_2": tweetFixture("sample_parent_2", "sample_parent_2", "two", "sample_parent_3", "sample_parent_3"),
		"/sample_parent_3/status/sample_parent_3": tweetFixture("sample_parent_3", "sample_parent_3", "three", "sample_parent_4", "sample_parent_4"),
		"/sample_parent_4/status/sample_parent_4": tweetFixture("sample_parent_4", "sample_parent_4", "four", "sample_parent_5", "sample_parent_5"),
		"/sample_parent_5/status/sample_parent_5": tweetFixture("sample_parent_5", "sample_parent_5", "five", "sample_parent_6", "sample_parent_6"),
	}
	srv := httptest.NewServer(fxtMockHandler(t, fixtures))
	defer srv.Close()
	m := NewManager(d, testCfg(t.TempDir()))
	m.replyResolver = NewReplyResolver(d, &fxtwitter.Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: time.Second})
	stats := m.runSnapshotPhaseStats(context.Background(), []string{"sample_leaf"}, 0, false)
	if stats.replyAttempts != 1 || stats.replyBlocked != 1 || stats.count != 0 {
		t.Fatalf("partial chain snapshot = %+v", stats)
	}
	var scoredAt int64
	if err := d.QueryRow(`SELECT algo_scored_at FROM feed_items WHERE tweet_id = 'sample_leaf'`).Scan(&scoredAt); err != nil {
		t.Fatal(err)
	}
	if scoredAt != 0 {
		incomplete, _ := d.ListIncompleteReplyChainsContext(context.Background(), []string{"sample_leaf"})
		t.Fatalf("partial chain scored_at = %d, incomplete = %+v", scoredAt, incomplete)
	}
	select {
	case <-m.feedScoringKick:
	default:
		t.Fatal("partial chain did not schedule its next bounded pass")
	}
}
