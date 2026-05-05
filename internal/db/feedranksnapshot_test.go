package db

import (
	"math"
	"testing"
	"time"
)

func TestReplaceFeedRankSnapshot_AtomicReplace(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"

	first := []SnapshotRow{
		{TweetID: "t1", RankPosition: 1, BaseScore: 10, DecayFactor: 1, FreshnessBonus: 5, Jitter: 0, FinalScore: 15},
		{TweetID: "t2", RankPosition: 2, BaseScore: 8, DecayFactor: 1, FreshnessBonus: 4, Jitter: 0, FinalScore: 12},
	}
	if err := d.ReplaceFeedRankSnapshot(user, first); err != nil {
		t.Fatalf("first replace: %v", err)
	}

	second := []SnapshotRow{
		{TweetID: "t3", RankPosition: 1, BaseScore: 9, DecayFactor: 1, FreshnessBonus: 3, Jitter: 0, FinalScore: 12},
	}
	if err := d.ReplaceFeedRankSnapshot(user, second); err != nil {
		t.Fatalf("second replace: %v", err)
	}

	rows, err := d.conn.Query("SELECT tweet_id FROM feed_rank_snapshot WHERE username = ?", user)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "t3" {
		t.Fatalf("expected only t3, got %v", ids)
	}
}

func TestSnapshotComputedAt_EmptyAndAfterReplace(t *testing.T) {
	d := openWritableTestDB(t)
	user := "bob"

	age, err := d.SnapshotComputedAt(user)
	if err != nil {
		t.Fatalf("computed_at (empty): %v", err)
	}
	if age != 0 {
		t.Fatalf("expected 0 for empty snapshot, got %d", age)
	}

	before := time.Now().UnixMilli()
	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "x", RankPosition: 1, FinalScore: 1},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	age, err = d.SnapshotComputedAt(user)
	if err != nil {
		t.Fatalf("computed_at (after): %v", err)
	}
	if age < before {
		t.Fatalf("computed_at %d < before %d", age, before)
	}
}

func TestReplaceFeedRankSnapshot_RejectsEmptyUsername(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ReplaceFeedRankSnapshot("", nil); err == nil {
		t.Fatal("expected error for empty username")
	}
}

func TestReplaceFeedRankSnapshot_EmptyRowsPreservesExisting(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"

	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "t1", RankPosition: 1, FinalScore: 5},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := d.ReplaceFeedRankSnapshot(user, nil); err != nil {
		t.Fatalf("empty call: %v", err)
	}

	var count int
	if err := d.conn.QueryRow(
		"SELECT COUNT(*) FROM feed_rank_snapshot WHERE username = ?", user,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("empty-rows call wiped snapshot: have %d rows, want 1", count)
	}
}

func TestListPreDiversityRanked_AppliesParentSeenAbsenceBoost(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"
	now := time.Now()
	publishedAt := now.Add(-12 * time.Hour).UnixMilli()

	for _, handle := range []string{"fresh", "stale", "capped", "starred"} {
		if _, err := d.conn.Exec(
			`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', ?, ?)`,
			"twitter_"+handle, now.UnixMilli(),
		); err != nil {
			t.Fatalf("follow %s: %v", handle, err)
		}
	}
	if _, err := d.conn.Exec(
		`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', 'twitter_starred', ?)`,
		now.UnixMilli(),
	); err != nil {
		t.Fatalf("star channel: %v", err)
	}

	insertItemAtWithInterest := func(tweetID, author, source, quoteAuthor string, isRetweet bool, itemPublishedAt int64, interest float64) {
		t.Helper()
		if _, err := d.conn.Exec(`INSERT INTO feed_items
				(tweet_id, author_handle, source_handle, quote_author_handle, is_retweet,
				 body_text, published_at, algo_interest, algo_scored_at)
				VALUES (?, ?, ?, ?, ?, 'body', ?, ?, 1)`,
			tweetID, author, source, quoteAuthor, isRetweet, itemPublishedAt, interest,
		); err != nil {
			t.Fatalf("insert %s: %v", tweetID, err)
		}
	}
	insertItemAt := func(tweetID, author, source, quoteAuthor string, isRetweet bool, itemPublishedAt int64) {
		t.Helper()
		insertItemAtWithInterest(tweetID, author, source, quoteAuthor, isRetweet, itemPublishedAt, 0)
	}
	insertItem := func(tweetID, author, source, quoteAuthor string, isRetweet bool) {
		t.Helper()
		insertItemAt(tweetID, author, source, quoteAuthor, isRetweet, publishedAt)
	}
	markSeen := func(tweetID string, seenAt int64) {
		t.Helper()
		if _, err := d.conn.Exec(
			`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?)`,
			user, tweetID, seenAt,
		); err != nil {
			t.Fatalf("mark seen %s: %v", tweetID, err)
		}
	}

	insertItem("fresh_candidate", "fresh", "fresh", "", false)
	insertItemAt("fresh_older_candidate", "fresh", "fresh", "", false, publishedAt-3600000)
	insertItem("stale_candidate", "stale", "stale", "", false)
	insertItem("capped_candidate", "capped", "capped", "", false)

	// Quote appearances and retweet source appearances should not make the
	// followed account count as parent-seen.
	insertItem("fresh_seen_as_quote", "other_quote", "other_quote", "fresh", false)
	markSeen("fresh_seen_as_quote", now.UnixMilli())
	insertItem("fresh_seen_as_retweet_source", "other_retweeted", "fresh", "", true)
	markSeen("fresh_seen_as_retweet_source", now.UnixMilli())

	insertItemAt("stale_seen_parent", "stale", "stale", "", false, publishedAt-2*3600000)
	markSeen("stale_seen_parent", now.Add(-36*time.Hour).UnixMilli())
	insertItemAt("capped_seen_parent", "capped", "capped", "", false, publishedAt-2*3600000)
	markSeen("capped_seen_parent", now.Add(-120*time.Hour).UnixMilli())
	insertItemAtWithInterest("starred_candidate", "starred", "starred", "", false, publishedAt, 25)
	insertItemAtWithInterest("starred_old_candidate", "starred", "starred", "", false, now.Add(-100*time.Hour).UnixMilli(), 25)
	insertItemAt("starred_seen_parent", "starred", "starred", "", false, publishedAt-2*3600000)
	markSeen("starred_seen_parent", now.Add(-36*time.Hour).UnixMilli())

	rows, err := d.ListPreDiversityRanked(user)
	if err != nil {
		t.Fatalf("ListPreDiversityRanked: %v", err)
	}
	baseByID := map[string]float64{}
	freshnessByID := map[string]float64{}
	for _, row := range rows {
		baseByID[row.TweetID] = row.BaseScore
		freshnessByID[row.TweetID] = row.FreshnessBonus
	}

	if got := baseByID["fresh_candidate"]; math.Abs(got-25.0) > 0.1 {
		t.Fatalf("fresh candidate base = %.3f, want fresh-blood boost 25", got)
	}
	if got := baseByID["fresh_older_candidate"]; math.Abs(got-25.0) > 0.1 {
		t.Fatalf("second fresh candidate base = %.3f, want account-level fresh-blood boost", got)
	}
	if got := baseByID["stale_candidate"]; math.Abs(got-6.25) > 0.2 {
		t.Fatalf("stale candidate base = %.3f, want half-star boost scaled to 36/72h", got)
	}
	if got := baseByID["capped_candidate"]; math.Abs(got-12.5) > 0.1 {
		t.Fatalf("capped candidate base = %.3f, want half-star cap", got)
	}
	if got := baseByID["starred_candidate"]; math.Abs(got-25.0) > 0.1 {
		t.Fatalf("starred candidate base = %.3f, want star score without decayed absence", got)
	}
	if got := freshnessByID["starred_candidate"]; math.Abs(got-3.75) > 0.2 {
		t.Fatalf("starred candidate freshness = %.3f, want direct 0.3x absence scaled to 36/72h", got)
	}
	if got := freshnessByID["starred_old_candidate"]; got != 0 {
		t.Fatalf("old starred candidate freshness = %.3f, want no starred absence outside cap window", got)
	}
	if baseByID["fresh_candidate"] <= baseByID["capped_candidate"] {
		t.Fatalf("fresh blood should outrank capped absence: fresh=%.3f capped=%.3f",
			baseByID["fresh_candidate"], baseByID["capped_candidate"])
	}
}

func TestListPreDiversityRanked_RecencyBoostPrioritizesFreshItems(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()
	insertItem := func(tweetID, author string, age time.Duration, interest float64) {
		t.Helper()
		if _, err := d.conn.Exec(`INSERT INTO feed_items
				(tweet_id, author_handle, source_handle, body_text, published_at, algo_interest, algo_scored_at)
				VALUES (?, ?, ?, 'body', ?, ?, 1)`,
			tweetID, author, author, now.Add(-age).UnixMilli(), interest,
		); err != nil {
			t.Fatalf("insert %s: %v", tweetID, err)
		}
	}
	insertItem("fresh_low", "fresh_low_author", time.Hour, 5)
	insertItem("older_high", "older_high_author", 5*time.Hour, 25)

	rows, err := d.ListPreDiversityRanked("")
	if err != nil {
		t.Fatalf("ListPreDiversityRanked: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("got %d rows, want at least 2", len(rows))
	}
	if rows[0].TweetID != "fresh_low" {
		t.Fatalf("top row = %q, want fresh low-interest item ahead of older high-interest item", rows[0].TweetID)
	}
	if rows[0].FreshnessBonus <= rows[1].FreshnessBonus {
		t.Fatalf("freshness bonus did not favor fresh item: first=%.3f second=%.3f",
			rows[0].FreshnessBonus, rows[1].FreshnessBonus)
	}
}
