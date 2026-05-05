package db

import (
	"strconv"
	"testing"
	"time"
)

func TestListSnapshotPage_OrderingAndCursor(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"

	// Seed a few feed_items so the JOIN succeeds.
	now := time.Now().UnixMilli()
	for i := 1; i <= 5; i++ {
		tweetID := "t" + strconv.Itoa(i)
		if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, author_handle, body_text, published_at, algo_interest, algo_scored_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			tweetID, "u"+strconv.Itoa(i), "body", now, 1.0, 0); err != nil {
			t.Fatal(err)
		}
	}

	rows := []SnapshotRow{
		{TweetID: "t1", RankPosition: 1, FinalScore: 5},
		{TweetID: "t2", RankPosition: 2, FinalScore: 4},
		{TweetID: "t3", RankPosition: 3, FinalScore: 3},
		{TweetID: "t4", RankPosition: 4, FinalScore: 2},
		{TweetID: "t5", RankPosition: 5, FinalScore: 1},
	}
	if err := d.ReplaceFeedRankSnapshot(user, rows); err != nil {
		t.Fatal(err)
	}

	page1, err := d.ListSnapshotPage(user, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].Item.TweetID != "t1" || page1[1].Item.TweetID != "t2" {
		t.Fatalf("page1 wrong: %+v", page1)
	}
	if page1[0].RankPosition != 1 || page1[1].RankPosition != 2 {
		t.Errorf("page1 positions: %d, %d (want 1, 2)", page1[0].RankPosition, page1[1].RankPosition)
	}

	page2, err := d.ListSnapshotPage(user, page1[len(page1)-1].RankPosition, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].Item.TweetID != "t3" || page2[1].Item.TweetID != "t4" {
		t.Fatalf("page2 wrong: %+v", page2)
	}

	page3, err := d.ListSnapshotPage(user, page2[len(page2)-1].RankPosition, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 1 || page3[0].Item.TweetID != "t5" {
		t.Fatalf("page3 wrong: %+v", page3)
	}
}

func TestListSnapshotPage_EmptySnapshot(t *testing.T) {
	d := openWritableTestDB(t)
	out, err := d.ListSnapshotPage("nobody", 0, 10)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected no rows, got %d", len(out))
	}
}

func TestListSnapshotPage_ExcludesSeenItems(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"

	now := time.Now().UnixMilli()
	for i := 1; i <= 5; i++ {
		tweetID := "t" + strconv.Itoa(i)
		if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, author_handle, body_text, published_at, algo_interest, algo_scored_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			tweetID, "u"+strconv.Itoa(i), "body", now, 1.0, 0); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "t1", RankPosition: 1, FinalScore: 5},
		{TweetID: "t2", RankPosition: 2, FinalScore: 4},
		{TweetID: "t3", RankPosition: 3, FinalScore: 3},
		{TweetID: "t4", RankPosition: 4, FinalScore: 2},
		{TweetID: "t5", RankPosition: 5, FinalScore: 1},
	}); err != nil {
		t.Fatal(err)
	}

	// Mark t2 and t4 seen AFTER snapshot build — this is the "between
	// rebuilds" race that the SQL filter closes.
	for _, id := range []string{"t2", "t4"} {
		if _, err := d.conn.Exec(
			`INSERT INTO feed_seen (username, tweet_id) VALUES (?, ?)`,
			user, id); err != nil {
			t.Fatal(err)
		}
	}

	// A different user's feed_seen must not affect alice's page.
	if _, err := d.conn.Exec(
		`INSERT INTO feed_seen (username, tweet_id) VALUES (?, ?)`,
		"bob", "t1"); err != nil {
		t.Fatal(err)
	}

	page, err := d.ListSnapshotPage(user, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 {
		t.Fatalf("expected 3 unseen rows, got %d: %+v", len(page), page)
	}
	gotIDs := []string{page[0].Item.TweetID, page[1].Item.TweetID, page[2].Item.TweetID}
	wantIDs := []string{"t1", "t3", "t5"}
	for i, want := range wantIDs {
		if gotIDs[i] != want {
			t.Errorf("row %d: got %s, want %s", i, gotIDs[i], want)
		}
	}
	// Ranks stay at their original positions — the caller uses the last
	// returned rank_position as the next cursor.
	if page[0].RankPosition != 1 || page[1].RankPosition != 3 || page[2].RankPosition != 5 {
		t.Errorf("ranks: got %d,%d,%d want 1,3,5",
			page[0].RankPosition, page[1].RankPosition, page[2].RankPosition)
	}
}

func TestListSnapshotPage_ExcludesSeenContentHashSiblings(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"
	now := time.Now().UnixMilli()

	for _, row := range []struct {
		id   string
		hash string
		rank int
	}{
		{id: "seen_original", hash: "same_content", rank: 1},
		{id: "unseen_repost", hash: "same_content", rank: 2},
		{id: "fresh", hash: "fresh_content", rank: 3},
	} {
		if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, author_handle, body_text, published_at, content_hash, algo_interest, algo_scored_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.id, "author_"+row.id, "body", now, row.hash, 1.0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "seen_original", RankPosition: 1, FinalScore: 3},
		{TweetID: "unseen_repost", RankPosition: 2, FinalScore: 2},
		{TweetID: "fresh", RankPosition: 3, FinalScore: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.conn.Exec(
		`INSERT INTO feed_seen (username, tweet_id) VALUES (?, ?)`,
		user, "seen_original",
	); err != nil {
		t.Fatal(err)
	}

	page, err := d.ListSnapshotPage(user, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(page), 1; got != want {
		t.Fatalf("rows = %d, want %d: %+v", got, want, page)
	}
	if got := page[0].Item.TweetID; got != "fresh" {
		t.Fatalf("remaining row = %s, want fresh", got)
	}
}

func TestListSnapshotPage_SkipsItemsWithoutFeedItem(t *testing.T) {
	// Snapshot points at "ghost" but no row in feed_items — should be absent.
	d := openWritableTestDB(t)
	user := "alice"

	now := time.Now().UnixMilli()
	if _, err := d.conn.Exec(`INSERT INTO feed_items
		(tweet_id, author_handle, body_text, published_at, algo_interest, algo_scored_at)
		VALUES (?, ?, ?, ?, ?, ?)`, "real", "u", "b", now, 1.0, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "ghost", RankPosition: 1, FinalScore: 9},
		{TweetID: "real", RankPosition: 2, FinalScore: 5},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := d.ListSnapshotPage(user, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Item.TweetID != "real" {
		t.Fatalf("expected only 'real' (ghost has no feed_items row), got %+v", out)
	}
}
