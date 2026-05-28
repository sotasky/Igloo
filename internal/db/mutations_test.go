package db

import (
	"strings"
	"testing"
)

func TestBookmarkMutationBumpsContentHashSiblings(t *testing.T) {
	d := openWritableTestDB(t)

	fixtures := []struct {
		tweetID     string
		contentHash string
		syncSeq     int64
	}{
		{tweetID: "tw_a_direct", contentHash: "shared_hash", syncSeq: 10},
		{tweetID: "tw_b_sibling", contentHash: "shared_hash", syncSeq: 11},
		{tweetID: "tw_c_unrelated", contentHash: "other_hash", syncSeq: 12},
	}
	for _, row := range fixtures {
		if err := d.ExecRaw(`
			INSERT INTO feed_items (
				tweet_id, source_handle, author_handle, body_text,
				content_hash, published_at, fetched_at, sync_seq
			) VALUES (?, 'source', 'author', 'body', ?, 1, 1, ?)`,
			row.tweetID, row.contentHash, row.syncSeq,
		); err != nil {
			t.Fatalf("insert %s: %v", row.tweetID, err)
		}
	}
	if err := d.initSyncSeq(); err != nil {
		t.Fatalf("init sync seq: %v", err)
	}

	if _, err := d.ApplyBookmarkMutation("admin", BookmarkMutation{
		VideoID:     "tw_a_direct",
		Action:      "set",
		UpdatedAtMs: 99,
	}); err != nil {
		t.Fatalf("ApplyBookmarkMutation: %v", err)
	}

	seqs := map[string]int64{}
	rows, err := d.conn.Query(`SELECT tweet_id, sync_seq FROM feed_items`)
	if err != nil {
		t.Fatalf("query sync seqs: %v", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var tweetID string
		var syncSeq int64
		if err := rows.Scan(&tweetID, &syncSeq); err != nil {
			t.Fatalf("scan sync seq: %v", err)
		}
		seqs[tweetID] = syncSeq
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("sync seq rows: %v", err)
	}

	if seqs["tw_a_direct"] <= 12 {
		t.Fatalf("direct bookmark row was not bumped: got %d", seqs["tw_a_direct"])
	}
	if seqs["tw_b_sibling"] <= 12 {
		t.Fatalf("content-hash sibling was not bumped: got %d", seqs["tw_b_sibling"])
	}
	if seqs["tw_a_direct"] == seqs["tw_b_sibling"] {
		t.Fatalf("direct and sibling rows must receive unique sync_seq values: %v", seqs)
	}
	if seqs["tw_c_unrelated"] != 12 {
		t.Fatalf("unrelated row should not be bumped: got %d", seqs["tw_c_unrelated"])
	}
}

func TestBookmarkMutationUsesCurrentTimeWhenUpdatedAtMissing(t *testing.T) {
	d := openWritableTestDB(t)

	if _, err := d.ApplyBookmarkMutation("admin", BookmarkMutation{
		VideoID:     "missing_timestamp_bookmark",
		Action:      "set",
		UpdatedAtMs: 0,
	}); err != nil {
		t.Fatalf("ApplyBookmarkMutation: %v", err)
	}

	var bookmarkedAt int64
	if err := d.QueryRow(`
		SELECT bookmarked_at
		FROM bookmarks
		WHERE user_id = 'admin' AND video_id = 'missing_timestamp_bookmark'
	`).Scan(&bookmarkedAt); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if bookmarkedAt <= 0 {
		t.Fatalf("bookmarked_at = %d, want positive timestamp", bookmarkedAt)
	}

	var value string
	if err := d.QueryRow(`
		SELECT value
		FROM sync_changes
		WHERE type = 'bookmark' AND item_id = 'missing_timestamp_bookmark'
		ORDER BY version DESC
		LIMIT 1
	`).Scan(&value); err != nil {
		t.Fatalf("read sync change: %v", err)
	}
	if !strings.Contains(value, `"bookmarked_at":`) || strings.Contains(value, `"updated_at_ms":0`) {
		t.Fatalf("sync value did not carry repaired timestamp: %s", value)
	}
}

func TestBookmarkMutationCreatesVideoStubForFeedItem(t *testing.T) {
	d := openWritableTestDB(t)

	const (
		tweetID       = "sample_feed_bookmark"
		authorHandle  = "sample_author"
		publishedAtMs = int64(1745100000000)
	)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, body_text,
			canonical_url, published_at, fetched_at
		) VALUES (?, ?, ?, 'sample body', ?, ?, ?)`,
		tweetID,
		authorHandle,
		authorHandle,
		"https://x.com/sample_author/status/sample_feed_bookmark",
		publishedAtMs,
		publishedAtMs,
	); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}

	if _, err := d.ApplyBookmarkMutation("admin", BookmarkMutation{
		VideoID:     tweetID,
		Action:      "set",
		UpdatedAtMs: publishedAtMs + 1000,
	}); err != nil {
		t.Fatalf("ApplyBookmarkMutation: %v", err)
	}

	var channelID string
	var syncSeq int64
	if err := d.QueryRow(`
		SELECT channel_id, sync_seq
		FROM videos
		WHERE video_id = ?
	`, tweetID).Scan(&channelID, &syncSeq); err != nil {
		t.Fatalf("read video stub: %v", err)
	}
	if channelID != "twitter_sample_author" {
		t.Fatalf("channel_id = %q, want twitter_sample_author", channelID)
	}
	if syncSeq <= 0 {
		t.Fatalf("sync_seq = %d, want bumped stub", syncSeq)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{UserID: "admin", Limit: 10})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("bookmarks = %d, want 1", len(bookmarks))
	}
	if got := bookmarks[0].VideoID; got != tweetID {
		t.Fatalf("bookmark VideoID = %q, want %q", got, tweetID)
	}
	if got := bookmarks[0].Title; got != "sample body" {
		t.Fatalf("bookmark Title = %q, want sample body", got)
	}
}
