package db

import "testing"

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
	defer rows.Close()
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
