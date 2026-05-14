package db

import (
	"fmt"
	"math"
	"strings"
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
	defer func() {
		_ = rows.Close()
	}()
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

func TestReplaceFeedRankSnapshot_ComputedAtIsMonotonic(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"

	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "first", RankPosition: 1, FinalScore: 1},
	}); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	previous := time.Now().UnixMilli() + 10_000
	if _, err := d.conn.Exec(
		"UPDATE feed_rank_snapshot SET computed_at = ? WHERE username = ?",
		previous, user,
	); err != nil {
		t.Fatalf("force previous computed_at: %v", err)
	}

	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "second", RankPosition: 1, FinalScore: 2},
	}); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	got, err := d.SnapshotComputedAt(user)
	if err != nil {
		t.Fatalf("computed_at: %v", err)
	}
	if got != previous+1 {
		t.Fatalf("computed_at = %d, want %d", got, previous+1)
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
	for _, handle := range []string{"starred", "newstarred", "source_starred", "source_seen"} {
		if _, err := d.conn.Exec(
			`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', ?, ?)`,
			"twitter_"+handle, now.UnixMilli(),
		); err != nil {
			t.Fatalf("star %s: %v", handle, err)
		}
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

	// Quote appearances should not make the quoted account count as seen.
	insertItem("fresh_seen_as_quote", "other_quote", "other_quote", "fresh", false)
	markSeen("fresh_seen_as_quote", now.UnixMilli())

	insertItemAt("stale_seen_parent", "stale", "stale", "", false, publishedAt-2*3600000)
	markSeen("stale_seen_parent", now.Add(-36*time.Hour).UnixMilli())
	insertItemAt("capped_seen_parent", "capped", "capped", "", false, publishedAt-2*3600000)
	markSeen("capped_seen_parent", now.Add(-120*time.Hour).UnixMilli())
	insertItemAtWithInterest("starred_candidate", "starred", "starred", "", false, publishedAt, 25)
	insertItemAtWithInterest("newstarred_candidate", "newstarred", "newstarred", "", false, publishedAt, 25)
	insertItemAtWithInterest("starred_old_candidate", "starred", "starred", "", false, now.Add(-100*time.Hour).UnixMilli(), 25)
	insertItemAt("starred_seen_parent", "starred", "starred", "", false, publishedAt-2*3600000)
	markSeen("starred_seen_parent", now.Add(-36*time.Hour).UnixMilli())
	insertItem("source_starred_candidate", "outside", "source_starred", "", true)
	insertItemAt("source_seen_parent", "other_seen", "source_seen", "", true, publishedAt-2*3600000)
	markSeen("source_seen_parent", now.Add(-36*time.Hour).UnixMilli())
	insertItem("source_seen_candidate", "other_candidate", "source_seen", "", true)

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
	if got := baseByID["starred_candidate"]; math.Abs(got-31.25) > 0.2 {
		t.Fatalf("starred candidate base = %.3f, want star score plus shared absence", got)
	}
	if got := baseByID["newstarred_candidate"]; math.Abs(got-50.0) > 0.1 {
		t.Fatalf("never-seen starred candidate base = %.3f, want star score plus fresh-blood boost", got)
	}
	if got := baseByID["source_starred_candidate"]; math.Abs(got-25.0) > 0.1 {
		t.Fatalf("source-starred candidate base = %.3f, want source-account fresh-blood boost", got)
	}
	if got := baseByID["source_seen_candidate"]; math.Abs(got-6.25) > 0.2 {
		t.Fatalf("source-seen candidate base = %.3f, want source-account absence scaled to 36/72h", got)
	}
	if got := freshnessByID["starred_candidate"]; got != 0 {
		t.Fatalf("starred candidate freshness = %.3f, want only regular recency freshness", got)
	}
	if got := freshnessByID["starred_old_candidate"]; got != 0 {
		t.Fatalf("old starred candidate freshness = %.3f, want no starred-specific freshness", got)
	}
	if baseByID["fresh_candidate"] <= baseByID["capped_candidate"] {
		t.Fatalf("fresh blood should outrank capped absence: fresh=%.3f capped=%.3f",
			baseByID["fresh_candidate"], baseByID["capped_candidate"])
	}
}

func TestListPreDiversityRanked_SetsReplyPenalty(t *testing.T) {
	d := openWritableTestDB(t)
	publishedAt := time.Now().Add(-1 * time.Hour).UnixMilli()

	for _, row := range []struct {
		id      string
		isReply int
	}{
		{id: "sample_reply", isReply: 1},
		{id: "sample_post", isReply: 0},
	} {
		if _, err := d.conn.Exec(`INSERT INTO feed_items
				(tweet_id, author_handle, body_text, published_at, algo_interest, algo_scored_at, is_reply)
				VALUES (?, 'sample_author', 'body', ?, 10, 1, ?)`,
			row.id, publishedAt, row.isReply,
		); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	rows, err := d.ListPreDiversityRanked("")
	if err != nil {
		t.Fatalf("ListPreDiversityRanked: %v", err)
	}
	penaltyByID := map[string]float64{}
	for _, row := range rows {
		penaltyByID[row.TweetID] = row.ReplyPenalty
	}
	if got := penaltyByID["sample_reply"]; got != feedReplyPenalty {
		t.Fatalf("reply penalty = %.1f, want %.1f", got, feedReplyPenalty)
	}
	if got := penaltyByID["sample_post"]; got != 0 {
		t.Fatalf("post penalty = %.1f, want 0", got)
	}
}

func TestListPreDiversityRanked_StarredUsesSharedAbsenceAfterRecentAuthorSeen(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"
	now := time.Now()

	if _, err := d.conn.Exec(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'twitter_starred', ?)`,
		now.UnixMilli(),
	); err != nil {
		t.Fatalf("follow starred: %v", err)
	}
	if _, err := d.conn.Exec(
		`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', 'twitter_starred', ?)`,
		now.UnixMilli(),
	); err != nil {
		t.Fatalf("star channel: %v", err)
	}
	if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, author_handle, source_handle, body_text, published_at, algo_interest, algo_scored_at)
			VALUES ('recent_seen', 'starred', 'starred', 'body', ?, 25, 1)`,
		now.Add(-12*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("insert seen parent: %v", err)
	}
	if _, err := d.conn.Exec(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES (?, 'recent_seen', ?)`,
		user, now.Add(-30*time.Minute).UnixMilli(),
	); err != nil {
		t.Fatalf("mark seen parent: %v", err)
	}
	if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, author_handle, source_handle, body_text, published_at, algo_interest, algo_scored_at)
			VALUES ('fresh_starred', 'starred', 'starred', 'body', ?, 25, 1)`,
		now.Add(-10*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("insert fresh starred: %v", err)
	}

	rows, err := d.ListPreDiversityRanked(user)
	if err != nil {
		t.Fatalf("ListPreDiversityRanked: %v", err)
	}
	for _, row := range rows {
		if row.TweetID != "fresh_starred" {
			continue
		}
		if row.FreshnessBonus != 0 {
			t.Fatalf("fresh starred freshness = %.3f, want no starred-specific freshness", row.FreshnessBonus)
		}
		if row.BaseScore <= 25 || row.BaseScore > 25.25 {
			t.Fatalf("fresh starred base = %.3f, want star score plus small shared absence", row.BaseScore)
		}
		return
	}
	t.Fatal("fresh_starred missing from ranked rows")
}

func TestListPreDiversityRanked_HighAffinitySurvivesRecentLowInterestItems(t *testing.T) {
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
	if rows[0].TweetID != "older_high" {
		t.Fatalf("top row = %q, want older high-interest item ahead of fresh low-interest item", rows[0].TweetID)
	}
	freshnessByID := map[string]float64{}
	for _, row := range rows {
		freshnessByID[row.TweetID] = row.FreshnessBonus
	}
	if freshnessByID["fresh_low"] <= freshnessByID["older_high"] {
		t.Fatalf("freshness bonus did not favor fresh item: fresh=%.3f older=%.3f",
			freshnessByID["fresh_low"], freshnessByID["older_high"])
	}
}

func TestListPreDiversityRanked_StarredHighAffinityFiveHoursOldStaysNearTop(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"
	now := time.Now()

	if _, err := d.conn.Exec(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'twitter_sample_target', ?)`,
		now.Add(-7*24*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("follow target: %v", err)
	}
	if _, err := d.conn.Exec(
		`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', 'twitter_sample_target', ?)`,
		now.Add(-7*24*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("star target: %v", err)
	}
	if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, author_handle, source_handle, body_text, published_at, algo_interest, algo_scored_at)
			VALUES ('prior_target_seen', 'sample_target', 'sample_target', 'body', ?, 37.59, 1)`,
		now.Add(-72*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("insert prior target: %v", err)
	}
	if _, err := d.conn.Exec(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES (?, 'prior_target_seen', ?)`,
		user, now.Add(-65*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("mark prior target seen: %v", err)
	}
	if _, err := d.conn.Exec(`INSERT INTO feed_items
			(tweet_id, author_handle, source_handle, body_text, published_at, algo_interest, algo_scored_at)
			VALUES ('target_high_affinity', 'sample_target', 'sample_target', 'body', ?, 37.59, 1)`,
		now.Add(-5*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("insert target: %v", err)
	}

	for i := 0; i < 40; i++ {
		age := time.Duration(30+i*5) * time.Minute
		interest := float64(5 + i%10)
		if _, err := d.conn.Exec(`INSERT INTO feed_items
				(tweet_id, author_handle, source_handle, body_text, published_at, algo_interest, algo_scored_at)
				VALUES (?, ?, ?, 'body', ?, ?, 1)`,
			fmt.Sprintf("newer_low_%02d", i),
			fmt.Sprintf("low_author_%02d", i),
			fmt.Sprintf("low_author_%02d", i),
			now.Add(-age).UnixMilli(),
			interest,
		); err != nil {
			t.Fatalf("insert low item %d: %v", i, err)
		}
	}

	rows, err := d.ListPreDiversityRanked(user)
	if err != nil {
		t.Fatalf("ListPreDiversityRanked: %v", err)
	}
	for i, row := range rows {
		if row.TweetID == "target_high_affinity" {
			if i >= 10 {
				t.Fatalf("target rank = %d, want in first 10", i+1)
			}
			return
		}
	}
	t.Fatal("target_high_affinity missing from ranked rows")
}

func TestListPreDiversityRanked_DemotesAlreadySeenUnderlyingTweet(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"
	now := time.Now()
	publishedAt := now.Add(-time.Hour).UnixMilli()

	insertItem := func(tweetID, author, quoteTweetID string, interest float64) {
		t.Helper()
		quoteAuthor := ""
		if quoteTweetID != "" {
			quoteAuthor = "base_author"
		}
		if _, err := d.conn.Exec(`INSERT INTO feed_items
				(tweet_id, author_handle, source_handle, quote_tweet_id, quote_author_handle,
				 body_text, published_at, algo_interest, algo_scored_at)
				VALUES (?, ?, ?, ?, ?, 'body', ?, ?, 1)`,
			tweetID, author, author, quoteTweetID, quoteAuthor, publishedAt, interest,
		); err != nil {
			t.Fatalf("insert %s: %v", tweetID, err)
		}
	}
	markSeen := func(tweetID string) {
		t.Helper()
		if _, err := d.conn.Exec(
			`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?)`,
			user, tweetID, now.UnixMilli(),
		); err != nil {
			t.Fatalf("mark seen %s: %v", tweetID, err)
		}
	}

	insertItem("plain_candidate", "plain_author", "", 30)
	insertItem("seen_base_once", "base_author", "", 0)
	insertItem("quote_candidate_once", "quote_author", "seen_base_once", 30)
	markSeen("seen_base_once")

	insertItem("seen_base_twice", "base_author", "", 0)
	insertItem("seen_quote_wrapper", "prior_quote_author", "seen_base_twice", 0)
	insertItem("quote_candidate_twice", "second_quote_author", "seen_base_twice", 30)
	markSeen("seen_base_twice")
	markSeen("seen_quote_wrapper")

	insertItem("original_candidate_seen_via_quote", "base_author", "", 30)
	insertItem("seen_quote_for_original", "prior_quote_author", "original_candidate_seen_via_quote", 0)
	markSeen("seen_quote_for_original")

	rows, err := d.ListPreDiversityRanked(user)
	if err != nil {
		t.Fatalf("ListPreDiversityRanked: %v", err)
	}
	baseByID := map[string]float64{}
	for _, row := range rows {
		baseByID[row.TweetID] = row.BaseScore
	}

	if got := baseByID["plain_candidate"]; math.Abs(got-30) > 0.1 {
		t.Fatalf("plain candidate base = %.3f, want unchanged 30", got)
	}
	if got := baseByID["quote_candidate_once"]; math.Abs(got-25) > 0.1 {
		t.Fatalf("one-seen quote base = %.3f, want 25", got)
	}
	if got := baseByID["quote_candidate_twice"]; math.Abs(got-18) > 0.1 {
		t.Fatalf("twice-seen quote base = %.3f, want 18", got)
	}
	if got := baseByID["original_candidate_seen_via_quote"]; math.Abs(got-25) > 0.1 {
		t.Fatalf("original seen through quote base = %.3f, want 25", got)
	}
}

func TestListPreDiversityRankedSetsRelatedContentKey(t *testing.T) {
	d := openWritableTestDB(t)
	publishedAt := time.Now().Add(-time.Hour).UnixMilli()

	for _, row := range []struct {
		id        string
		author    string
		quoteID   string
		canonical string
	}{
		{id: "sample_original", author: "base_author"},
		{id: "sample_quote", author: "quote_author", quoteID: "sample_original"},
	} {
		if _, err := d.conn.Exec(`INSERT INTO feed_items
				(tweet_id, author_handle, source_handle, quote_tweet_id, canonical_tweet_id,
				 body_text, published_at, algo_interest, algo_scored_at)
				VALUES (?, ?, ?, ?, ?, 'body', ?, 30, 1)`,
			row.id, row.author, row.author, row.quoteID, row.canonical, publishedAt,
		); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	rows, err := d.ListPreDiversityRanked("")
	if err != nil {
		t.Fatalf("ListPreDiversityRanked: %v", err)
	}
	keyByID := map[string]string{}
	for _, row := range rows {
		keyByID[row.TweetID] = row.RelatedContentKey
	}
	for _, tweetID := range []string{"sample_original", "sample_quote"} {
		if got := keyByID[tweetID]; got != "tweet:sample_original" {
			t.Fatalf("%s related key = %q, want tweet:sample_original", tweetID, got)
		}
	}
}

func TestFeedRelatedSeenCountSQLUsesPrecomputedSet(t *testing.T) {
	relatedExpr := feedRelatedSeenCountSelect("fi")
	if strings.Contains(strings.ToUpper(relatedExpr), "SELECT COUNT") {
		t.Fatalf("related seen count should not run a per-row correlated count: %s", relatedExpr)
	}
	fromSQL := feedRankingFromSQL(relatedExpr, feedAbsenceBoostSelect("fi"))
	if !strings.Contains(fromSQL, "related_key") || !strings.Contains(fromSQL, "rsc.related_key") {
		t.Fatalf("ranking SQL should precompute related seen counts once: %s", fromSQL)
	}
}
