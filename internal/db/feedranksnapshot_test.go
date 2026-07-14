package db

import (
	"context"
	"strings"
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
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_rank_snapshot`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty replace count=%d, err=%v", count, err)
	}
}

func TestListPreDiversityRankedUsesPersistedIdentitiesAndThreadState(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	if _, err := d.MutateFollow("twitter_sample_source", "set", now-10); err != nil {
		t.Fatal(err)
	}
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

	rows, err := d.ListPreDiversityRankedCandidatesContext(
		context.Background(),
		[]string{"thread_root", "thread_reply", "context_only"},
		0,
	)
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

func TestFeedSeenRankingProjectionUsesCoveringIndex(t *testing.T) {
	d := openFreshTestDB(t)
	rows, err := d.conn.Query(`EXPLAIN QUERY PLAN
		SELECT fs.tweet_id, fs.seen_at,
		       fi.quote_tweet_id, fi.canonical_tweet_id,
		       fi.channel_id, fi.source_channel_id, fi.is_ghost
		FROM feed_seen fs
		JOIN feed_items fi INDEXED BY idx_feed_items_seen_cover
		  ON fi.tweet_id = fs.tweet_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "USING COVERING INDEX idx_feed_items_seen_cover") {
		t.Fatalf("seen feed plan = %s", plan)
	}
}

func TestFeedOwnershipAfterFollowClearKeepsActiveRepostWrapper(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	for _, channelID := range []string{"twitter_sample_first", "twitter_sample_second"} {
		if _, err := d.MutateFollow(channelID, "set", now-100); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, reposter_channel_id,
			body_text, is_retweet, canonical_tweet_id, content_hash,
			published_at, fetched_at, algo_interest, algo_scored_at
		) VALUES
			('sample_sole', 'twitter_sample_first', 'twitter_sample_first', '',
			 'sole', 0, 'sample_sole', 'sample_sole_hash', ?, ?, 10, 1),
			('sample_first_wrapper', 'twitter_sample_first', 'twitter_sample_author', 'twitter_sample_first',
			 'shared', 1, 'sample_target', 'sample_shared_hash', ?, ?, 10, 1),
			('sample_second_wrapper', 'twitter_sample_second', 'twitter_sample_author', 'twitter_sample_second',
			 'shared', 1, 'sample_target', 'sample_shared_hash', ?, ?, 10, 1);
		INSERT INTO retweet_sources (content_hash, retweeter_channel_id, tweet_id, published_at)
		VALUES
			('sample_shared_hash', 'twitter_sample_first', 'sample_first_wrapper', ?),
			('sample_shared_hash', 'twitter_sample_second', 'sample_second_wrapper', ?)
	`, now, now, now, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "sample_sole", RankPosition: 1, FinalScore: 10},
		{TweetID: "sample_first_wrapper", RankPosition: 2, FinalScore: 9},
	}); err != nil {
		t.Fatal(err)
	}
	snapshotAt, err := d.SnapshotComputedAt()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.MutateFollow("twitter_sample_first", "clear", now); err != nil {
		t.Fatal(err)
	}

	pre, err := d.ListPreDiversityRankedCandidatesContext(
		context.Background(),
		[]string{"sample_sole", "sample_first_wrapper", "sample_second_wrapper"},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	preIDs := make(map[string]bool, len(pre))
	for _, row := range pre {
		preIDs[row.TweetID] = true
	}
	if preIDs["sample_sole"] || preIDs["sample_first_wrapper"] || !preIDs["sample_second_wrapper"] {
		t.Fatalf("rank candidates after clear = %#v", preIDs)
	}

	page, err := d.ListSnapshotPage(snapshotAt, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 {
		t.Fatalf("stale snapshot page after clear = %#v", page)
	}

	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "sample_second_wrapper", RankPosition: 1, FinalScore: 9},
	}); err != nil {
		t.Fatal(err)
	}
	rebuiltAt, err := d.SnapshotComputedAt()
	if err != nil {
		t.Fatal(err)
	}
	page, err = d.ListSnapshotPage(rebuiltAt, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Item.TweetID != "sample_second_wrapper" {
		t.Fatalf("rebuilt snapshot page after clear = %#v", page)
	}
}

func TestRankCandidatesDoNotRediscoverArchiveWithoutRefill(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	if _, err := d.MutateFollow("twitter_sample_channel", "set", now); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, body_text,
			canonical_tweet_id, content_hash, published_at, fetched_at,
			algo_interest, algo_scored_at
		) VALUES
			('sample_window_current', 'twitter_sample_channel', 'twitter_sample_channel', 'current',
			 'sample_window_current', 'sample_window_current_hash', ?, ?, 2, 1),
			('sample_window_dirty', 'twitter_sample_channel', 'twitter_sample_channel', 'dirty',
			 'sample_window_dirty', 'sample_window_dirty_hash', ?, ?, 3, 1),
			('sample_archive_high', 'twitter_sample_channel', 'twitter_sample_channel', 'archive',
			 'sample_archive_high', 'sample_archive_high_hash', ?, ?, 100, 1)
	`, now-3, now-3, now-2, now-2, now-1, now-1); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{{
		TweetID: "sample_window_current", RankPosition: 1, FinalScore: 2,
	}}); err != nil {
		t.Fatal(err)
	}

	rows, err := d.ListPreDiversityRankedCandidatesContext(
		context.Background(), []string{"sample_window_dirty"}, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(rows))
	for _, row := range rows {
		got[row.TweetID] = true
	}
	if !got["sample_window_current"] || !got["sample_window_dirty"] || got["sample_archive_high"] {
		t.Fatalf("bounded candidates = %#v", got)
	}
}

func TestRankCandidateRefillAdvancesPastSeenRows(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	if _, err := d.MutateFollow("twitter_sample_source", "set", now); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, body_text,
			canonical_tweet_id, content_hash, published_at, fetched_at,
			algo_interest, algo_scored_at
		) VALUES
			('sample_refill_1', 'twitter_sample_source', 'twitter_sample_source', 'one', 'sample_refill_1', 'sample_refill_hash_1', ?, ?, 10, 1),
			('sample_refill_2', 'twitter_sample_source', 'twitter_sample_source', 'two', 'sample_refill_2', 'sample_refill_hash_2', ?, ?, 9, 1),
			('sample_refill_3', 'twitter_sample_source', 'twitter_sample_source', 'three', 'sample_refill_3', 'sample_refill_hash_3', ?, ?, 8, 1),
			('sample_refill_4', 'twitter_sample_source', 'twitter_sample_source', 'four', 'sample_refill_4', 'sample_refill_hash_4', ?, ?, 7, 1),
			('sample_refill_5', 'twitter_sample_source', 'twitter_sample_source', 'five', 'sample_refill_5', 'sample_refill_hash_5', ?, ?, 6, 1);
		INSERT INTO feed_seen (tweet_id, seen_at) VALUES
			('sample_refill_1', ?), ('sample_refill_2', ?)
	`,
		now-1, now-1, now-2, now-2, now-3, now-3, now-4, now-4, now-5, now-5,
		now, now,
	); err != nil {
		t.Fatal(err)
	}

	rows, err := d.ListPreDiversityRankedCandidatesContext(context.Background(), nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(rows))
	for _, row := range rows {
		got[row.TweetID] = true
	}
	if len(got) != 2 || !got["sample_refill_3"] || !got["sample_refill_4"] || got["sample_refill_5"] {
		t.Fatalf("refill candidates = %#v", got)
	}
}
