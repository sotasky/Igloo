package db

import (
	"context"
	"testing"
	"time"
)

func TestUpdateAlgoInterestStoresScoredAtInMilliseconds(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, channel_id, body_text, published_at, algo_scored_at)
			VALUES ('score_ms', 'twitter_sample_author', 'body', ?, 0)`,
		time.Now().UnixMilli(),
	); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}

	before := time.Now().UnixMilli()
	if err := d.UpdateAlgoInterest(map[string]float64{"score_ms": 12.5}); err != nil {
		t.Fatalf("UpdateAlgoInterest: %v", err)
	}
	after := time.Now().UnixMilli()

	var score float64
	var scoredAt int64
	if err := d.conn.QueryRow(
		`SELECT algo_interest, algo_scored_at FROM feed_items WHERE tweet_id = 'score_ms'`,
	).Scan(&score, &scoredAt); err != nil {
		t.Fatalf("select scored item: %v", err)
	}
	if score != 12.5 {
		t.Fatalf("algo_interest = %.3f, want 12.5", score)
	}
	if scoredAt < before || scoredAt > after {
		t.Fatalf("algo_scored_at = %d, want between %d and %d", scoredAt, before, after)
	}
}

func TestGetUnscoredFeedItemsReturnsNewestBoundedBatch(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.MutateFollow("twitter_sample_author", "set", 1); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, channel_id, body_text, published_at, fetched_at, algo_scored_at
		) VALUES
			('sample_unscored_old', 'twitter_sample_author', 'old', 1, 1, 0),
			('sample_unscored_middle', 'twitter_sample_author', 'middle', 2, 2, 0),
			('sample_unscored_new', 'twitter_sample_author', 'new', 3, 3, 0)
	`); err != nil {
		t.Fatal(err)
	}

	items, err := d.GetUnscoredFeedItems(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("unscored batch length = %d, want 2", len(items))
	}
	got := map[string]bool{items[0].TweetID: true, items[1].TweetID: true}
	if !got["sample_unscored_new"] || !got["sample_unscored_middle"] || got["sample_unscored_old"] {
		t.Fatalf("unscored batch = %#v", got)
	}
}

func TestRankCandidatesExcludeGhostRows(t *testing.T) {
	d := openWritableTestDB(t)
	publishedAt := time.Now().UnixMilli()
	if err := d.ExecRaw(`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('twitter_sample_author', 1)`); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		id      string
		isGhost int
		score   float64
	}{
		{id: "visible_item", score: 10},
		{id: "context_parent", isGhost: 1, score: 100},
	} {
		if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, channel_id, body_text, is_ghost, published_at, fetched_at, algo_interest, algo_scored_at)
			VALUES (?, 'twitter_sample_author', 'body', ?, ?, ?, ?, 1)`,
			row.id, row.isGhost, publishedAt, publishedAt, row.score,
		); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	items, err := d.ListPreDiversityRankedCandidatesContext(
		context.Background(), []string{"visible_item", "context_parent"}, 0,
	)
	if err != nil {
		t.Fatalf("ListPreDiversityRankedCandidatesContext: %v", err)
	}
	if len(items) != 1 || items[0].TweetID != "visible_item" {
		t.Fatalf("items = %+v, want only visible_item", items)
	}
}
