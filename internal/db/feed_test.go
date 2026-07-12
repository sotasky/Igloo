package db

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestListFeedItemsPage(t *testing.T) {
	d := openTestDB(t)
	items, err := d.ListFeedItemsPage(40, nil, false)
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

func TestListFeedItemsPageExcludesGhostRows(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "visible_item", AuthorHandle: "sample_author", BodyText: "visible", PublishedAt: &now, FetchedAt: now, ContentHash: "hash_visible"},
		{TweetID: "context_parent", AuthorHandle: "sample_parent", BodyText: "context", IsGhost: true, PublishedAt: &now, FetchedAt: now, ContentHash: "hash_context"},
	}); err != nil {
		t.Fatalf("seed feed items: %v", err)
	}

	items, err := d.ListFeedItemsPage(10, nil, false)
	if err != nil {
		t.Fatalf("ListFeedItemsPage: %v", err)
	}
	if len(items) != 1 || items[0].TweetID != "visible_item" {
		t.Fatalf("items = %+v, want only visible_item", items)
	}
}

func TestGetLatestFetchedFeedItemExcludesGhostRows(t *testing.T) {
	d := openWritableTestDB(t)
	base := time.Now().UTC()
	visibleFetched := base.Add(-time.Minute)
	ghostFetched := base
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "visible_head",
			AuthorHandle: "sample_author",
			SourceHandle: "sample_author",
			BodyText:     "visible",
			PublishedAt:  &visibleFetched,
			FetchedAt:    visibleFetched,
			ContentHash:  "hash_visible_head",
		},
		{
			TweetID:      "context_head",
			AuthorHandle: "sample_author_b",
			BodyText:     "context",
			IsGhost:      true,
			PublishedAt:  &ghostFetched,
			FetchedAt:    ghostFetched,
			ContentHash:  "hash_context_head",
		},
	}); err != nil {
		t.Fatalf("seed feed items: %v", err)
	}

	item, err := d.GetLatestFetchedFeedItem()
	if err != nil {
		t.Fatalf("GetLatestFetchedFeedItem: %v", err)
	}
	if item == nil || item.TweetID != "visible_head" {
		t.Fatalf("latest fetched item = %+v, want visible_head", item)
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
	seen, err := d.GetSeenTweetIDs([]string{"fake_id"})
	if err != nil {
		t.Fatalf("GetSeenTweetIDs: %v", err)
	}
	if len(seen) != 0 {
		t.Errorf("expected empty set, got %d items", len(seen))
	}
}

func TestGetFeedLikesForTweetIDs(t *testing.T) {
	d := openTestDB(t)
	liked, err := d.GetFeedLikesForTweetIDs([]string{"fake_id"})
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
	likes, err := d.GetFeedLikedPage(10, nil)
	if err != nil {
		t.Fatalf("GetFeedLikedPage: %v", err)
	}
	if len(likes) > 10 {
		t.Errorf("expected at most 10, got %d", len(likes))
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

	err := d.InsertFeedLike("tweet_123", map[string]string{
		"source_handle":       "user_a",
		"author_handle":       "user_b",
		"author_display_name": "User B",
		"body_text":           "hello world",
		"platform":            "twitter",
	})
	if err != nil {
		t.Fatalf("InsertFeedLike: %v", err)
	}

	likes, err := d.GetFeedLikesForTweetIDs([]string{"tweet_123"})
	if err != nil {
		t.Fatalf("GetFeedLikesForTweetIDs: %v", err)
	}
	if !likes["tweet_123"] {
		t.Error("expected tweet_123 to be liked")
	}

	err = d.DeleteFeedLike("tweet_123")
	if err != nil {
		t.Fatalf("DeleteFeedLike: %v", err)
	}

	likes, _ = d.GetFeedLikesForTweetIDs([]string{"tweet_123"})
	if likes["tweet_123"] {
		t.Error("expected tweet_123 to be unliked after delete")
	}
}

func TestInsertAndDeleteFeedLikeResolveCanonicalStatusURL(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		originalID = "1000000000000000101"
		repostID   = "1000000000000000102"
	)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, body_text, canonical_url)
		VALUES (?, 'twitter_sample_reposter', 'twitter_sample_author', 'body', ?)`,
		repostID, "https://x.com/sample_author/status/"+originalID,
	); err != nil {
		t.Fatalf("seed repost row: %v", err)
	}

	if err := d.InsertFeedLike(repostID, map[string]string{
		"source_handle": "sample_reposter",
		"author_handle": "sample_author",
		"body_text":     "body",
		"platform":      "twitter",
	}); err != nil {
		t.Fatalf("InsertFeedLike: %v", err)
	}
	likes, err := d.GetFeedLikesForTweetIDs([]string{originalID, repostID})
	if err != nil {
		t.Fatalf("GetFeedLikesForTweetIDs: %v", err)
	}
	if !likes[originalID] || likes[repostID] {
		t.Fatalf("likes after repost like = original:%v repost:%v, want original only", likes[originalID], likes[repostID])
	}

	if err := d.DeleteFeedLike(repostID); err != nil {
		t.Fatalf("DeleteFeedLike: %v", err)
	}
	likes, err = d.GetFeedLikesForTweetIDs([]string{originalID, repostID})
	if err != nil {
		t.Fatalf("GetFeedLikesForTweetIDs after delete: %v", err)
	}
	if likes[originalID] || likes[repostID] {
		t.Fatalf("likes after repost unlike = original:%v repost:%v, want neither", likes[originalID], likes[repostID])
	}
}

func TestMarkSeen(t *testing.T) {
	d := openWritableTestDB(t)
	count, err := d.MarkSeen([]string{"tweet_a", "tweet_b"})
	if err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 marked, got %d", count)
	}

	seen, err := d.GetSeenTweetIDs([]string{"tweet_a", "tweet_b"})
	if err != nil {
		t.Fatalf("GetSeenTweetIDs: %v", err)
	}
	if !seen["tweet_a"] || !seen["tweet_b"] {
		t.Fatalf("seen rows = %#v", seen)
	}
}

func TestMarkSeenExpandsAcrossThreadAndPureRepost(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`INSERT INTO feed_items
		(tweet_id, body_text, is_retweet, quote_tweet_id, is_reply,
		 reply_to_status, canonical_tweet_id, published_at)
		VALUES
			('100', 'root body', 0, '', 0, '', '100', 1000),
			('101', 'reply body', 0, '', 1, '100', '101', 1001),
			('102', 'root body', 1, '', 0, '', '100', 1002),
			('200', 'other body', 0, '', 0, '', '200', 1003)`); err != nil {
		t.Fatal(err)
	}

	if _, err := d.MarkSeen([]string{"101"}); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	seen, err := d.GetSeenTweetIDs([]string{"100", "101", "102", "200"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"100", "101", "102"} {
		if !seen[id] {
			t.Fatalf("related row %s was not marked seen: %#v", id, seen)
		}
	}
	if seen["200"] {
		t.Fatalf("unrelated row was marked seen: %#v", seen)
	}
}

func TestUpsertPureRepostKeepsTargetCanonicalTweetID(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	item := model.FeedItem{
		TweetID:          "102",
		SourceHandle:     "sample_reposter",
		AuthorHandle:     "sample_author",
		BodyText:         "body",
		IsRetweet:        true,
		CanonicalURL:     "https://x.com/sample_author/status/100",
		CanonicalTweetID: "100",
		ContentHash:      "sample_hash",
		PublishedAt:      &now,
		FetchedAt:        now,
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatal(err)
	}
	var canonicalID string
	if err := d.QueryRow(`SELECT canonical_tweet_id FROM feed_items WHERE tweet_id = '102'`).Scan(&canonicalID); err != nil {
		t.Fatal(err)
	}
	if canonicalID != "100" {
		t.Fatalf("canonical_tweet_id = %q, want target 100", canonicalID)
	}
}

func TestMuteUnmute(t *testing.T) {
	d := openWritableTestDB(t)

	err := d.MuteAccount("user_a_handle")
	if err != nil {
		t.Fatalf("MuteAccount: %v", err)
	}

	muted, _ := d.GetMutedChannelIDs()
	if len(muted) != 1 || muted[0] != "twitter_user_a_handle" {
		t.Fatalf("muted channel IDs = %#v", muted)
	}

	err = d.UnmuteAccount("user_a_handle")
	if err != nil {
		t.Fatalf("UnmuteAccount: %v", err)
	}

	muted, _ = d.GetMutedChannelIDs()
	if len(muted) != 0 {
		t.Fatalf("muted channel IDs after unmute = %#v", muted)
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

	var authorID, canonical string
	if err := d.QueryRow(
		`SELECT COALESCE(channel_id, ''), COALESCE(canonical_url, '') FROM feed_items WHERE tweet_id = ?`,
		tweetID,
	).Scan(&authorID, &canonical); err != nil {
		t.Fatalf("read feed item: %v", err)
	}
	if authorID != "twitter_"+sourceHandle {
		t.Fatalf("channel_id = %q, want twitter_%s", authorID, sourceHandle)
	}
	if canonical != "https://x.com/"+sourceHandle+"/status/"+tweetID {
		t.Fatalf("canonical_url = %q", canonical)
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
		FROM feed_items_resolved WHERE tweet_id = ?`, "quote_fill").Scan(&handle, &display, &body, &media); err != nil {
		t.Fatalf("read quote fields: %v", err)
	}
	if handle != "sample_quote" || display != "Sample Quote" || body != "quoted text" || media == "" {
		t.Fatalf("quote fields = handle:%q display:%q body:%q media:%q", handle, display, body, media)
	}
}

func TestResolveFeedStateIDForWriteCopiesCanonicalReadyAsset(t *testing.T) {
	d := openWritableTestDB(t)
	const sourceID = "sample_repost_incomplete"
	const stateID = "1000000000000000999"
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, channel_id, canonical_url, fetched_at)
		VALUES (?, 'twitter_sample_author', ?, 1), (?, NULL, '', 1)
	`, sourceID, "https://x.com/sample_author/status/"+stateID, stateID); err != nil {
		t.Fatalf("seed feed state: %v", err)
	}
	storeReadyAssetForTest(t, d, Asset{
		AssetID:   BuildAssetID("twitter", "tweet", sourceID, "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: sourceID,
		SourceURL: "https://cdn.example/sample.jpg", FilePath: "media/twitter/sample/source.jpg",
		ContentType: "image/jpeg", SizeBytes: 12, State: AssetStateReady,
	}, 1000)

	got, err := d.ResolveFeedStateIDForWrite(sourceID)
	if err != nil {
		t.Fatalf("ResolveFeedStateIDForWrite: %v", err)
	}
	if got != stateID {
		t.Fatalf("state ID = %q, want %q", got, stateID)
	}
	canonical, err := d.GetAsset(BuildAssetID("twitter", "tweet", stateID, "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if canonical == nil || canonical.State != AssetStateReady || canonical.FilePath == "" || canonical.SizeBytes <= 0 {
		t.Fatalf("canonical ready row = %+v", canonical)
	}
	if canonical.SHA256 == "" || canonical.FileMtimeNs <= 0 {
		t.Fatalf("canonical fingerprint missing: %+v", canonical)
	}
}
