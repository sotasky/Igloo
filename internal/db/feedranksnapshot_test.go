package db

import (
	"testing"
	"time"
)

func TestReplaceFeedRankSnapshotAtomicallyReplacesRows(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "old_a", RankPosition: 1},
		{TweetID: "old_b", RankPosition: 2},
	}); err != nil {
		t.Fatal(err)
	}
	firstAt, err := d.SnapshotComputedAt()
	if err != nil || firstAt <= 0 {
		t.Fatalf("first computed_at = %d, err=%v", firstAt, err)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{{TweetID: "new", RankPosition: 1}}); err != nil {
		t.Fatal(err)
	}
	var count int
	var tweetID string
	if err := d.QueryRow(`SELECT COUNT(*), MIN(tweet_id) FROM feed_rank_snapshot`).Scan(&count, &tweetID); err != nil {
		t.Fatal(err)
	}
	if count != 1 || tweetID != "new" {
		t.Fatalf("snapshot count=%d tweet=%q", count, tweetID)
	}
	secondAt, err := d.SnapshotComputedAt()
	if err != nil || secondAt <= firstAt {
		t.Fatalf("second computed_at = %d after %d, err=%v", secondAt, firstAt, err)
	}
	if err := d.ReplaceFeedRankSnapshot(nil); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_rank_snapshot`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("empty replace count=%d, err=%v", count, err)
	}
}

func TestListPreDiversityRankedUsesPersistedIdentitiesAndThreadState(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, body_text, is_reply,
			reply_to_status, canonical_tweet_id, content_hash,
			published_at, fetched_at, algo_interest, algo_scored_at
		) VALUES
			('thread_root', 'twitter_sample_source', 'twitter_sample_root', 'root', 0, '', 'thread_root', 'root_hash', ?, ?, 10, 1),
			('thread_reply', 'twitter_sample_source', 'twitter_sample_reply', 'reply', 1, 'thread_root', 'thread_reply', 'reply_hash', ?, ?, 10, 1),
			('context_only', 'twitter_sample_source', 'twitter_sample_context', 'ghost', 0, '', 'context_only', 'ghost_hash', ?, ?, 10, 1)
	`, now-2, now-2, now-1, now-1, now, now); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`UPDATE feed_items SET is_ghost = 1 WHERE tweet_id = 'context_only'`); err != nil {
		t.Fatal(err)
	}

	rows, err := d.ListPreDiversityRanked()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]PreDiversitySnapshotRow, len(rows))
	for _, row := range rows {
		byID[row.TweetID] = row
	}
	if _, ok := byID["context_only"]; ok {
		t.Fatalf("ghost row ranked: %+v", rows)
	}
	root, rootOK := byID["thread_root"]
	reply, replyOK := byID["thread_reply"]
	if !rootOK || !replyOK {
		t.Fatalf("ranked rows = %+v", rows)
	}
	if root.ChannelID != "twitter_sample_root" || root.SourceChannelID != "twitter_sample_source" {
		t.Fatalf("root identities = %+v", root)
	}
	if root.ThreadRootID != "thread_root" || reply.ThreadRootID != "thread_root" {
		t.Fatalf("thread roots root=%q reply=%q", root.ThreadRootID, reply.ThreadRootID)
	}
	if !reply.IsReply || reply.ReplyPenalty <= 0 {
		t.Fatalf("reply state = %+v", reply)
	}
}
