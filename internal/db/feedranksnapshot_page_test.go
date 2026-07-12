package db

import "testing"

func TestListSnapshotPageUsesRankCursor(t *testing.T) {
	d := openWritableTestDB(t)
	for _, id := range []string{"first", "second", "third"} {
		if err := d.ExecRaw(`
			INSERT INTO feed_items (tweet_id, channel_id, body_text, published_at, fetched_at)
			VALUES (?, 'twitter_sample_author', 'body', 1, 1)
		`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "first", RankPosition: 1, FinalScore: 3},
		{TweetID: "second", RankPosition: 2, FinalScore: 2},
		{TweetID: "third", RankPosition: 3, FinalScore: 1},
	}); err != nil {
		t.Fatal(err)
	}
	snapshotAt, err := d.SnapshotComputedAt()
	if err != nil {
		t.Fatal(err)
	}

	page, err := d.ListSnapshotPage(snapshotAt, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Item.TweetID != "first" || page[1].Item.TweetID != "second" {
		t.Fatalf("first page = %+v", page)
	}
	page, err = d.ListSnapshotPage(snapshotAt, page[1].RankPosition, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Item.TweetID != "third" {
		t.Fatalf("second page = %+v", page)
	}
}

func TestListSnapshotPageExcludesSeenAndGhostRows(t *testing.T) {
	d := openWritableTestDB(t)
	for _, row := range []struct {
		id      string
		isGhost int
	}{
		{id: "seen"},
		{id: "ghost", isGhost: 1},
		{id: "visible"},
	} {
		if err := d.ExecRaw(`
			INSERT INTO feed_items (tweet_id, channel_id, body_text, is_ghost, published_at, fetched_at)
			VALUES (?, 'twitter_sample_author', 'body', ?, 1, 1)
		`, row.id, row.isGhost); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ExecRaw(`INSERT INTO feed_seen (tweet_id, seen_at) VALUES ('seen', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "seen", RankPosition: 1},
		{TweetID: "ghost", RankPosition: 2},
		{TweetID: "visible", RankPosition: 3},
	}); err != nil {
		t.Fatal(err)
	}
	snapshotAt, err := d.SnapshotComputedAt()
	if err != nil {
		t.Fatal(err)
	}

	page, err := d.ListSnapshotPage(snapshotAt, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Item.TweetID != "visible" {
		t.Fatalf("page = %+v", page)
	}
}
