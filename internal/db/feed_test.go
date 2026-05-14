package db

import (
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestListFeedItemsPage(t *testing.T) {
	d := openTestDB(t)
	items, err := d.ListFeedItemsPage(40, nil, "")
	if err != nil {
		t.Fatalf("ListFeedItemsPage: %v", err)
	}
	if len(items) > 40 {
		t.Errorf("expected at most 40, got %d", len(items))
	}
	if len(items) > 0 {
		item := items[0]
		if item.TweetID == "" {
			t.Error("tweet_id is empty")
		}
		if item.AuthorHandle == "" {
			t.Error("author_handle is empty")
		}
	}
}

func TestGetFeedItemsForTweetIDs(t *testing.T) {
	d := openTestDB(t)
	var tweetID string
	_ = d.conn.QueryRow("SELECT tweet_id FROM feed_items LIMIT 1").Scan(&tweetID)
	if tweetID == "" {
		t.Skip("no feed items in test DB")
	}

	items, err := d.GetFeedItemsForTweetIDs([]string{tweetID})
	if err != nil {
		t.Fatalf("GetFeedItemsForTweetIDs: %v", err)
	}
	if _, ok := items[tweetID]; !ok {
		t.Errorf("expected item for %q", tweetID)
	}
}

func TestGetSeenTweetIDs(t *testing.T) {
	d := openTestDB(t)
	seen, err := d.GetSeenTweetIDs("nonexistent_user_xyz", []string{"fake_id"})
	if err != nil {
		t.Fatalf("GetSeenTweetIDs: %v", err)
	}
	if len(seen) != 0 {
		t.Errorf("expected empty set, got %d items", len(seen))
	}
}

func TestGetFeedLikesForTweetIDs(t *testing.T) {
	d := openTestDB(t)
	liked, err := d.GetFeedLikesForTweetIDs("nonexistent_xyz", []string{"fake_id"})
	if err != nil {
		t.Fatalf("GetFeedLikesForTweetIDs: %v", err)
	}
	if len(liked) != 0 {
		t.Errorf("expected empty set, got %d", len(liked))
	}
}

func TestGetMutedAccounts(t *testing.T) {
	d := openTestDB(t)
	muted, err := d.GetMutedAccounts()
	if err != nil {
		t.Fatalf("GetMutedAccounts: %v", err)
	}
	_ = muted
}

func TestGetFeedLikedPage(t *testing.T) {
	d := openTestDB(t)
	var username string
	_ = d.conn.QueryRow("SELECT username FROM feed_likes LIMIT 1").Scan(&username)
	if username == "" {
		t.Skip("no feed likes in test DB")
	}

	likes, err := d.GetFeedLikedPage(username, 10, nil)
	if err != nil {
		t.Fatalf("GetFeedLikedPage: %v", err)
	}
	if len(likes) > 10 {
		t.Errorf("expected at most 10, got %d", len(likes))
	}
}

func TestGetFeedMediaJobs(t *testing.T) {
	d := openTestDB(t)
	jobs, err := d.GetFeedMediaJobs([]string{"nonexistent_xyz"})
	if err != nil {
		t.Fatalf("GetFeedMediaJobs: %v", err)
	}
	if jobs == nil {
		t.Error("expected empty map, got nil")
	}
}

func TestGetRetweetSources(t *testing.T) {
	d := openTestDB(t)
	sources, err := d.GetRetweetSources([]string{"nonexistent_hash"})
	if err != nil {
		t.Fatalf("GetRetweetSources: %v", err)
	}
	if sources == nil {
		t.Error("expected empty map, got nil")
	}
}

func TestInsertAndDeleteFeedLike(t *testing.T) {
	d := openWritableTestDB(t)

	err := d.InsertFeedLike("test_user", "tweet_123", map[string]string{
		"source_handle":       "user_a",
		"author_handle":       "user_b",
		"author_display_name": "User B",
		"body_text":           "hello world",
		"platform":            "twitter",
	})
	if err != nil {
		t.Fatalf("InsertFeedLike: %v", err)
	}

	likes, err := d.GetFeedLikesForTweetIDs("test_user", []string{"tweet_123"})
	if err != nil {
		t.Fatalf("GetFeedLikesForTweetIDs: %v", err)
	}
	if !likes["tweet_123"] {
		t.Error("expected tweet_123 to be liked")
	}

	err = d.DeleteFeedLike("test_user", "tweet_123")
	if err != nil {
		t.Fatalf("DeleteFeedLike: %v", err)
	}

	likes, _ = d.GetFeedLikesForTweetIDs("test_user", []string{"tweet_123"})
	if likes["tweet_123"] {
		t.Error("expected tweet_123 to be unliked after delete")
	}
}

func TestMarkSeen(t *testing.T) {
	d := openWritableTestDB(t)
	count, err := d.MarkSeen("test_user", []string{"tweet_a", "tweet_b"})
	if err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 marked, got %d", count)
	}

	var changeType, itemID, value string
	if err := d.QueryRow(
		`SELECT type, item_id, value FROM sync_changes WHERE type = 'seen' ORDER BY version DESC LIMIT 1`,
	).Scan(&changeType, &itemID, &value); err != nil {
		t.Fatalf("seen sync_change missing: %v", err)
	}
	if changeType != "seen" || itemID != "tweet_a" {
		t.Fatalf("sync_change = %s/%s, want seen/tweet_a", changeType, itemID)
	}
	if !strings.Contains(value, `"tweet_a"`) || !strings.Contains(value, `"tweet_b"`) {
		t.Fatalf("sync_change value missing ids: %s", value)
	}
}

func TestMuteUnmute(t *testing.T) {
	d := openWritableTestDB(t)

	err := d.MuteAccount("user_a_handle")
	if err != nil {
		t.Fatalf("MuteAccount: %v", err)
	}

	muted, _ := d.GetMutedAccounts()
	found := false
	for _, h := range muted {
		if h == "user_a_handle" {
			found = true
		}
	}
	if !found {
		t.Error("expected user_a_handle in muted list")
	}

	err = d.UnmuteAccount("user_a_handle")
	if err != nil {
		t.Fatalf("UnmuteAccount: %v", err)
	}

	var count int
	if err := d.QueryRow(
		`SELECT COUNT(*) FROM sync_changes WHERE type = 'mute' AND item_id = 'user_a_handle'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("mute sync_changes = %d, want 2", count)
	}
}

func TestListFeedItemsSince(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now()
	items := []model.FeedItem{
		{TweetID: "delta_1", AuthorHandle: "user_a", BodyText: "first", PublishedAt: &now},
		{TweetID: "delta_2", AuthorHandle: "user_b", BodyText: "second", PublishedAt: &now},
		{TweetID: "delta_3", AuthorHandle: "user_c", BodyText: "third", PublishedAt: &now},
	}

	n, err := d.UpsertFeedItems(items)
	if err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 upserted, got %d", n)
	}

	// Get all items (after=0) — should return all 3
	all, err := d.ListFeedItemsSince(0, 500)
	if err != nil {
		t.Fatalf("ListFeedItemsSince(0, 500): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 items, got %d", len(all))
	}

	// Verify sync_seq is populated and ordered
	for i, item := range all {
		if item.SyncSeq == 0 {
			t.Errorf("item %d has zero sync_seq", i)
		}
		if i > 0 && item.SyncSeq <= all[i-1].SyncSeq {
			t.Errorf("sync_seq not ascending: [%d]=%d >= [%d]=%d", i-1, all[i-1].SyncSeq, i, item.SyncSeq)
		}
	}

	// Get items after second one's sync_seq — should return 1
	afterSecond, err := d.ListFeedItemsSince(all[1].SyncSeq, 500)
	if err != nil {
		t.Fatalf("ListFeedItemsSince(seq2): %v", err)
	}
	if len(afterSecond) != 1 {
		t.Fatalf("expected 1 item after seq2, got %d", len(afterSecond))
	}
	if afterSecond[0].TweetID != "delta_3" {
		t.Errorf("expected delta_3, got %s", afterSecond[0].TweetID)
	}

	// Test pagination with limit=1
	page1, err := d.ListFeedItemsSince(0, 1)
	if err != nil {
		t.Fatalf("ListFeedItemsSince(0, 1): %v", err)
	}
	if len(page1) != 1 {
		t.Fatalf("expected 1 item with limit=1, got %d", len(page1))
	}
	if page1[0].TweetID != "delta_1" {
		t.Errorf("expected delta_1 as first page, got %s", page1[0].TweetID)
	}

	// Page 2: after page1's sync_seq
	page2, err := d.ListFeedItemsSince(page1[0].SyncSeq, 1)
	if err != nil {
		t.Fatalf("ListFeedItemsSince page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("expected 1 item on page2, got %d", len(page2))
	}
	if page2[0].TweetID != "delta_2" {
		t.Errorf("expected delta_2 on page2, got %s", page2[0].TweetID)
	}

	// Default limit clamping: limit=0 should default to 500 (returns all 3)
	defaulted, err := d.ListFeedItemsSince(0, 0)
	if err != nil {
		t.Fatalf("ListFeedItemsSince(0, 0): %v", err)
	}
	if len(defaulted) != 3 {
		t.Errorf("expected 3 items with default limit, got %d", len(defaulted))
	}
}

func TestSyncSeqAssignment(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now()
	items := []model.FeedItem{
		{TweetID: "sync_test_1", AuthorHandle: "user_a", PublishedAt: &now},
		{TweetID: "sync_test_2", AuthorHandle: "user_b", PublishedAt: &now},
	}

	n, err := d.UpsertFeedItems(items)
	if err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 upserted, got %d", n)
	}

	// Read back sync_seq values.
	var seq1, seq2 int64
	_ = d.conn.QueryRow("SELECT sync_seq FROM feed_items WHERE tweet_id = ?", "sync_test_1").Scan(&seq1)
	_ = d.conn.QueryRow("SELECT sync_seq FROM feed_items WHERE tweet_id = ?", "sync_test_2").Scan(&seq2)

	if seq1 == 0 {
		t.Fatal("sync_seq for item 1 should be non-zero")
	}
	if seq2 == 0 {
		t.Fatal("sync_seq for item 2 should be non-zero")
	}
	if seq2 <= seq1 {
		t.Errorf("sync_seq should be monotonically increasing: seq1=%d seq2=%d", seq1, seq2)
	}

	// Re-upsert item 1 — its sync_seq should bump past item 2's.
	_, err = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "sync_test_1", AuthorHandle: "user_a", PublishedAt: &now},
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	var seq1After int64
	_ = d.conn.QueryRow("SELECT sync_seq FROM feed_items WHERE tweet_id = ?", "sync_test_1").Scan(&seq1After)

	if seq1After <= seq2 {
		t.Errorf("after re-upsert, sync_seq should bump past seq2: seq1After=%d seq2=%d", seq1After, seq2)
	}
}

func TestUpsertFeedItemsNormalizesUnknownDirectAuthorFromSourceHandle(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now()
	tweetID := "sample_placeholder_author"
	sourceHandle := "sample_source"
	placeholderAuthor := "unknown"
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      tweetID,
		SourceHandle: sourceHandle,
		AuthorHandle: placeholderAuthor,
		CanonicalURL: "https://x.com/" + placeholderAuthor + "/status/" + tweetID,
		PublishedAt:  &now,
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	var author, canonical string
	if err := d.QueryRow(
		`SELECT author_handle, COALESCE(canonical_url, '') FROM feed_items WHERE tweet_id = ?`,
		tweetID,
	).Scan(&author, &canonical); err != nil {
		t.Fatalf("read feed item: %v", err)
	}
	if author != sourceHandle {
		t.Fatalf("author_handle = %q, want %s", author, sourceHandle)
	}
	if canonical != "https://x.com/"+sourceHandle+"/status/"+tweetID {
		t.Fatalf("canonical_url = %q", canonical)
	}
}

func TestRepairTwitterPlaceholderAuthorsUpdatesExistingRows(t *testing.T) {
	d := openWritableTestDB(t)

	tweetID := "sample_repair_tweet"
	sourceHandle := "sample_source"
	placeholderAuthor := "unknown"
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, canonical_url, published_at, fetched_at, sync_seq
		) VALUES (?, ?, ?, ?, 1, 1, 1)
	`, tweetID, sourceHandle, placeholderAuthor, "https://x.com/"+placeholderAuthor+"/status/"+tweetID); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO videos (video_id, channel_id, title, published_at, sync_seq)
		VALUES (?, ?, 'Sample', 1, 1)
	`, tweetID, "twitter_"+placeholderAuthor); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if _, err := d.conn.Exec(`DELETE FROM schema_migrations WHERE name = 'twitter_placeholder_author_repair'`); err != nil {
		t.Fatalf("reset repair migration: %v", err)
	}
	if err := d.initSyncSeq(); err != nil {
		t.Fatalf("initSyncSeq: %v", err)
	}

	if err := d.repairTwitterPlaceholderAuthorsOnce(); err != nil {
		t.Fatalf("repairTwitterPlaceholderAuthorsOnce: %v", err)
	}

	var author, canonical, channelID string
	var feedSeq, videoSeq int64
	if err := d.QueryRow(
		`SELECT author_handle, COALESCE(canonical_url, ''), sync_seq FROM feed_items WHERE tweet_id = ?`,
		tweetID,
	).Scan(&author, &canonical, &feedSeq); err != nil {
		t.Fatalf("read feed item: %v", err)
	}
	if err := d.QueryRow(
		`SELECT channel_id, sync_seq FROM videos WHERE video_id = ?`,
		tweetID,
	).Scan(&channelID, &videoSeq); err != nil {
		t.Fatalf("read video: %v", err)
	}
	if author != sourceHandle {
		t.Fatalf("author_handle = %q, want %s", author, sourceHandle)
	}
	if canonical != "https://x.com/"+sourceHandle+"/status/"+tweetID {
		t.Fatalf("canonical_url = %q", canonical)
	}
	if channelID != "twitter_"+sourceHandle {
		t.Fatalf("channel_id = %q, want twitter_%s", channelID, sourceHandle)
	}
	if feedSeq <= 1 || videoSeq <= 1 {
		t.Fatalf("sync_seq values should be bumped, feed=%d video=%d", feedSeq, videoSeq)
	}
}

func TestUpsertFeedItemsPreservesFetchedAtOnRefetch(t *testing.T) {
	d := openWritableTestDB(t)

	publishedAt := time.Now().Add(-48 * time.Hour)
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "stable_fetched_at",
		AuthorHandle: "user_a",
		BodyText:     "first copy",
		PublishedAt:  &publishedAt,
	}}); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}

	var firstFetchedAt int64
	if err := d.QueryRow(
		"SELECT fetched_at FROM feed_items WHERE tweet_id = ?",
		"stable_fetched_at",
	).Scan(&firstFetchedAt); err != nil {
		t.Fatalf("read initial fetched_at: %v", err)
	}
	if firstFetchedAt == 0 {
		t.Fatal("initial fetched_at should be set")
	}

	time.Sleep(2 * time.Millisecond)

	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "stable_fetched_at",
		AuthorHandle: "user_a",
		BodyText:     "refetched copy",
		PublishedAt:  &publishedAt,
	}}); err != nil {
		t.Fatalf("refetch upsert: %v", err)
	}

	var secondFetchedAt int64
	var bodyText string
	if err := d.QueryRow(
		"SELECT fetched_at, body_text FROM feed_items WHERE tweet_id = ?",
		"stable_fetched_at",
	).Scan(&secondFetchedAt, &bodyText); err != nil {
		t.Fatalf("read refetched row: %v", err)
	}
	if secondFetchedAt != firstFetchedAt {
		t.Fatalf("fetched_at changed on refetch: first=%d second=%d", firstFetchedAt, secondFetchedAt)
	}
	if bodyText != "refetched copy" {
		t.Fatalf("body_text was not refreshed: %q", bodyText)
	}
}

func TestUpsertFeedItemsRepairsUnknownLanguages(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "lang_repair",
		AuthorHandle:  "sample_author_a",
		BodyText:      "안녕하세요",
		Lang:          "qme",
		QuoteTweetID:  "quote_a",
		QuoteBodyText: "你好",
		QuoteLang:     "und",
		PublishedAt:   &now,
	}}); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "lang_repair",
		AuthorHandle:  "sample_author_a",
		BodyText:      "안녕하세요",
		Lang:          "ko",
		QuoteTweetID:  "quote_a",
		QuoteBodyText: "你好",
		QuoteLang:     "zh",
		PublishedAt:   &now,
	}}); err != nil {
		t.Fatalf("repair upsert: %v", err)
	}

	var lang, quoteLang string
	if err := d.QueryRow(`SELECT COALESCE(lang,''), COALESCE(quote_lang,'') FROM feed_items WHERE tweet_id = ?`, "lang_repair").Scan(&lang, &quoteLang); err != nil {
		t.Fatalf("read repaired langs: %v", err)
	}
	if lang != "ko" || quoteLang != "zh" {
		t.Fatalf("langs = (%q, %q), want (ko, zh)", lang, quoteLang)
	}
}

func TestUpsertFeedItemsFillsMissingQuoteFields(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:          "quote_fill",
		AuthorHandle:     "sample_author",
		BodyText:         "post",
		QuoteTweetID:     "9000000000000000500",
		CanonicalTweetID: "quote_fill",
		PublishedAt:      &now,
	}}); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:                "quote_fill",
		AuthorHandle:           "sample_author",
		BodyText:               "post",
		QuoteTweetID:           "9000000000000000500",
		QuoteAuthorHandle:      "sample_quote",
		QuoteAuthorDisplayName: "Sample Quote",
		QuoteBodyText:          "quoted text",
		QuoteMediaJSON:         `[{"url":"https://pbs.twimg.com/media/sample.jpg","type":"photo"}]`,
		CanonicalTweetID:       "quote_fill",
		PublishedAt:            &now,
	}}); err != nil {
		t.Fatalf("fill quote upsert: %v", err)
	}

	var handle, display, body, media string
	if err := d.QueryRow(`
		SELECT COALESCE(quote_author_handle,''), COALESCE(quote_author_display_name,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_media_json,'')
		FROM feed_items WHERE tweet_id = ?`, "quote_fill").Scan(&handle, &display, &body, &media); err != nil {
		t.Fatalf("read quote fields: %v", err)
	}
	if handle != "sample_quote" || display != "Sample Quote" || body != "quoted text" || media == "" {
		t.Fatalf("quote fields = handle:%q display:%q body:%q media:%q", handle, display, body, media)
	}
}

func TestUpsertFeedItemsRejectsStatusUndefinedAvatarURLs(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()

	_, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:              "bad_avatar_insert",
		AuthorHandle:         "author_bad",
		AuthorAvatarURL:      "https://x.com/author_bad/status/undefined",
		QuoteTweetID:         "quote_bad",
		QuoteAuthorHandle:    "quote_bad",
		QuoteAuthorAvatarURL: "https://x.com/quote_bad/status/undefined",
		PublishedAt:          &now,
	}})
	if err != nil {
		t.Fatalf("upsert bad avatar insert: %v", err)
	}

	var authorAvatar, quoteAvatar string
	if err := d.QueryRow(`
		SELECT COALESCE(author_avatar_url, ''), COALESCE(quote_author_avatar_url, '')
		FROM feed_items
		WHERE tweet_id = 'bad_avatar_insert'
	`).Scan(&authorAvatar, &quoteAvatar); err != nil {
		t.Fatal(err)
	}
	if authorAvatar != "" || quoteAvatar != "" {
		t.Fatalf("bad avatar URLs should be blank, got author=%q quote=%q", authorAvatar, quoteAvatar)
	}
}

func TestUpsertFeedItemsClearsExistingStatusUndefinedAvatarURLs(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, author_avatar_url, quote_tweet_id,
			quote_author_handle, quote_author_avatar_url, published_at, fetched_at, sync_seq
		) VALUES (
			'bad_avatar_update', 'author_bad', 'https://x.com/author_bad/status/undefined',
			'quote_bad', 'quote_bad', 'https://x.com/quote_bad/status/undefined',
			1, 1, 1
		)
	`); err != nil {
		t.Fatalf("seed bad avatar row: %v", err)
	}

	_, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:              "bad_avatar_update",
		AuthorHandle:         "author_bad",
		AuthorAvatarURL:      "https://x.com/author_bad/status/undefined",
		QuoteTweetID:         "quote_bad",
		QuoteAuthorHandle:    "quote_bad",
		QuoteAuthorAvatarURL: "https://x.com/quote_bad/status/undefined",
		PublishedAt:          &now,
	}})
	if err != nil {
		t.Fatalf("upsert bad avatar update: %v", err)
	}

	var authorAvatar, quoteAvatar string
	if err := d.QueryRow(`
		SELECT COALESCE(author_avatar_url, ''), COALESCE(quote_author_avatar_url, '')
		FROM feed_items
		WHERE tweet_id = 'bad_avatar_update'
	`).Scan(&authorAvatar, &quoteAvatar); err != nil {
		t.Fatal(err)
	}
	if authorAvatar != "" || quoteAvatar != "" {
		t.Fatalf("existing bad avatar URLs should be cleared, got author=%q quote=%q", authorAvatar, quoteAvatar)
	}
}
