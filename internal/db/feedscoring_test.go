package db

import (
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

func TestListRankedFeedItemsExcludesGhostRows(t *testing.T) {
	d := openWritableTestDB(t)
	publishedAt := time.Now().UnixMilli()

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

	items, err := d.ListRankedFeedItems(10, 0)
	if err != nil {
		t.Fatalf("ListRankedFeedItems: %v", err)
	}
	if len(items) != 1 || items[0].TweetID != "visible_item" {
		t.Fatalf("items = %+v, want only visible_item", items)
	}
}
