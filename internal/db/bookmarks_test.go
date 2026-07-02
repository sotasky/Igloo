package db

import (
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestIsBookmarked(t *testing.T) {
	d := openTestDB(t)
	bookmarked, categoryID, err := d.IsBookmarked("nonexistent_xyz", "")
	if err != nil {
		t.Fatalf("IsBookmarked: %v", err)
	}
	if bookmarked {
		t.Error("non-existent video should not be bookmarked")
	}
	if categoryID != 0 {
		t.Errorf("expected category 0, got %d", categoryID)
	}
}

func TestGetBookmarkCategories(t *testing.T) {
	d := openTestDB(t)
	categories, err := d.GetBookmarkCategories("")
	if err != nil {
		t.Fatalf("GetBookmarkCategories: %v", err)
	}
	_ = categories
}

func TestGetBookmarks(t *testing.T) {
	d := openTestDB(t)
	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{Limit: 5})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) > 5 {
		t.Errorf("expected at most 5, got %d", len(bookmarks))
	}
}

func TestGetBookmarksMarksFollowedChannel(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID   = "tweet_followed_bookmark"
		channelID = "twitter_followed_author"
	)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform)
		VALUES (?, 'Followed Author', 'twitter')
	`, channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', ?, 1)
	`, channelID); err != nil {
		t.Fatalf("insert channel follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES (?, ?, 'X post tweet_followed_bookmark', 0, '', 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
		VALUES ('admin', ?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{UserID: "admin", Limit: 10})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if !bookmarks[0].IsSubscribed {
		t.Fatal("expected followed bookmark channel to be marked subscribed")
	}
	if got := bookmarks[0].Platform; got != "twitter" {
		t.Fatalf("Platform = %q, want twitter", got)
	}
}

func TestGetBookmarkCount(t *testing.T) {
	d := openTestDB(t)
	count, err := d.GetBookmarkCount(GetBookmarksOpts{})
	if err != nil {
		t.Fatalf("GetBookmarkCount: %v", err)
	}
	if count < 0 {
		t.Errorf("negative count: %d", count)
	}
}

func TestBookmarkLabelFiltersAndCounts(t *testing.T) {
	d := openFreshTestDB(t)

	for _, videoID := range []string{
		"alice_cinema_one",
		"alice_cinema_two",
		"alice_japan",
		"alice_no_label_null",
		"alice_no_label_empty",
		"alice_no_label_space",
		"bob_cinema",
	} {
		seedTestVideo(t, d, videoID, "youtube_bookmark_labels")
	}

	fixtures := []struct {
		userID      string
		videoID     string
		categoryID  int64
		customTitle string
		insertNull  bool
		bookmarked  int64
	}{
		{"alice", "alice_cinema_one", 1, " cinema ", false, 10},
		{"alice", "alice_cinema_two", 2, "cinema", false, 20},
		{"alice", "alice_japan", 1, "japan", false, 30},
		{"alice", "alice_no_label_null", 1, "", true, 40},
		{"alice", "alice_no_label_empty", 2, "", false, 50},
		{"alice", "alice_no_label_space", 2, "   ", false, 60},
		{"bob", "bob_cinema", 1, "cinema", false, 70},
	}
	for _, f := range fixtures {
		var err error
		if f.insertNull {
			err = d.ExecRaw(`
				INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
				VALUES (?, ?, ?, ?)
			`, f.userID, f.videoID, f.categoryID, f.bookmarked)
		} else {
			err = d.ExecRaw(`
				INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, bookmarked_at)
				VALUES (?, ?, ?, ?, ?)
			`, f.userID, f.videoID, f.categoryID, f.customTitle, f.bookmarked)
		}
		if err != nil {
			t.Fatalf("insert bookmark %s: %v", f.videoID, err)
		}
	}

	exactOpts := GetBookmarksOpts{
		UserID:          "alice",
		CategoryID:      999,
		LabelFilterMode: BookmarkLabelFilterExact,
		Label:           "cinema",
		Limit:           10,
	}
	count, err := d.GetBookmarkCount(exactOpts)
	if err != nil {
		t.Fatalf("GetBookmarkCount exact label: %v", err)
	}
	if count != 2 {
		t.Fatalf("exact label count = %d, want 2", count)
	}
	bookmarks, err := d.GetBookmarks(exactOpts)
	if err != nil {
		t.Fatalf("GetBookmarks exact label: %v", err)
	}
	if got := bookmarkVideoIDs(bookmarks); strings.Join(got, ",") != "alice_cinema_two,alice_cinema_one" {
		t.Fatalf("exact label videos = %v", got)
	}

	noLabelOpts := GetBookmarksOpts{
		UserID:          "alice",
		LabelFilterMode: BookmarkLabelFilterNoLabel,
		Limit:           10,
	}
	count, err = d.GetBookmarkCount(noLabelOpts)
	if err != nil {
		t.Fatalf("GetBookmarkCount no label: %v", err)
	}
	if count != 3 {
		t.Fatalf("no-label count = %d, want 3", count)
	}
	bookmarks, err = d.GetBookmarks(noLabelOpts)
	if err != nil {
		t.Fatalf("GetBookmarks no label: %v", err)
	}
	if got := bookmarkVideoIDs(bookmarks); strings.Join(got, ",") != "alice_no_label_space,alice_no_label_empty,alice_no_label_null" {
		t.Fatalf("no-label videos = %v", got)
	}

	categoryCount, err := d.GetBookmarkCount(GetBookmarksOpts{UserID: "alice", CategoryID: 1})
	if err != nil {
		t.Fatalf("GetBookmarkCount category: %v", err)
	}
	if categoryCount != 3 {
		t.Fatalf("category count = %d, want 3", categoryCount)
	}

	labelCounts, err := d.GetBookmarkLabelCounts("alice")
	if err != nil {
		t.Fatalf("GetBookmarkLabelCounts: %v", err)
	}
	if len(labelCounts) != 3 {
		t.Fatalf("label count rows = %#v, want 3 rows", labelCounts)
	}
	wantLabels := []BookmarkLabelCountRow{
		{Label: "", IsNoLabel: true, BookmarkCount: 3},
		{Label: "cinema", BookmarkCount: 2},
		{Label: "japan", BookmarkCount: 1},
	}
	for i, want := range wantLabels {
		got := labelCounts[i]
		if got.Label != want.Label || got.IsNoLabel != want.IsNoLabel || got.BookmarkCount != want.BookmarkCount {
			t.Fatalf("labelCounts[%d] = %#v, want %#v", i, got, want)
		}
	}
}

func bookmarkVideoIDs(bookmarks []model.Video) []string {
	ids := make([]string, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		ids = append(ids, bookmark.VideoID)
	}
	return ids
}

func TestAddAndRemoveBookmark(t *testing.T) {
	d := openWritableTestDB(t)
	const videoID = "fixture_bookmark_video"

	seedTestVideo(t, d, videoID, "youtube_bookmark_fixture")
	if err := d.ExecRaw(`
		INSERT INTO bookmark_categories (id, user_id, name, created_at)
		VALUES (1, '', 'Default', 1)
	`); err != nil {
		t.Fatalf("seed bookmark category: %v", err)
	}

	err := d.AddBookmark("", videoID, 1, "", "", "")
	if err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}

	bookmarked, catID, err := d.IsBookmarked(videoID, "")
	if err != nil {
		t.Fatalf("IsBookmarked: %v", err)
	}
	if !bookmarked {
		t.Error("expected video to be bookmarked")
	}
	_ = catID

	err = d.RemoveBookmark("", videoID)
	if err != nil {
		t.Fatalf("RemoveBookmark: %v", err)
	}

	bookmarked, _, _ = d.IsBookmarked(videoID, "")
	if bookmarked {
		t.Error("expected video to not be bookmarked after remove")
	}
}

func TestAddAndRemoveBookmarkResolveCanonicalStatusURL(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		userID     = "sample_user"
		originalID = "1000000000000000201"
		repostID   = "1000000000000000202"
	)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, body_text, canonical_url)
		VALUES (?, 'sample_reposter', 'sample_author', 'body', ?)`,
		repostID, "https://x.com/sample_author/status/"+originalID,
	); err != nil {
		t.Fatalf("seed repost row: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, author_handle, fetched_at)
		VALUES (?, 'unknown', 1)`,
		originalID,
	); err != nil {
		t.Fatalf("seed hollow canonical row: %v", err)
	}
	if err := d.InsertMediaFileBatch([]model.MediaFile{{
		OwnerType:  "feed_media",
		OwnerID:    repostID,
		MediaIndex: 0,
		FilePath:   "media/twitter/sample_author/repost_0.jpg",
		MediaType:  "photo",
		SourceURL:  "https://pbs.twimg.com/media/sample_repost_0.jpg",
		FileSize:   123,
	}}); err != nil {
		t.Fatalf("seed repost media: %v", err)
	}

	if err := d.AddBookmark(userID, repostID, 0, "", "", ""); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}
	bookmarked, _, err := d.IsBookmarked(repostID, userID)
	if err != nil {
		t.Fatalf("IsBookmarked: %v", err)
	}
	if !bookmarked {
		t.Fatalf("repost bookmark state should resolve through canonical status URL")
	}
	var originalRows, repostRows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE user_id = ? AND video_id = ?`, userID, originalID).Scan(&originalRows); err != nil {
		t.Fatalf("count original bookmark: %v", err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE user_id = ? AND video_id = ?`, userID, repostID).Scan(&repostRows); err != nil {
		t.Fatalf("count repost bookmark: %v", err)
	}
	if originalRows != 1 || repostRows != 0 {
		t.Fatalf("bookmark rows original=%d repost=%d, want original only", originalRows, repostRows)
	}
	if _, err := d.GetMediaFilePath("feed_media", originalID, 0); err != nil {
		t.Fatalf("canonical bookmark lost media: %v", err)
	}
	asset, err := d.GetAsset(BuildManifestAssetID("twitter", "tweet", originalID, "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset canonical media: %v", err)
	}
	if asset == nil || asset.OwnerID != originalID || asset.FilePath == "" {
		t.Fatalf("canonical asset not materialized: %+v", asset)
	}
	var channelID, body string
	if err := d.QueryRow(`
		SELECT v.channel_id, COALESCE(fi.body_text, '')
		FROM videos v
		JOIN feed_items fi ON fi.tweet_id = v.video_id
		WHERE v.video_id = ?
	`, originalID).Scan(&channelID, &body); err != nil {
		t.Fatalf("query canonical bookmark shape: %v", err)
	}
	if channelID != "twitter_sample_author" || body != "body" {
		t.Fatalf("canonical shape channel=%q body=%q, want twitter_sample_author/body", channelID, body)
	}

	if err := d.RemoveBookmark(userID, repostID); err != nil {
		t.Fatalf("RemoveBookmark: %v", err)
	}
	bookmarked, _, err = d.IsBookmarked(repostID, userID)
	if err != nil {
		t.Fatalf("IsBookmarked after remove: %v", err)
	}
	if bookmarked {
		t.Fatalf("repost bookmark state should clear canonical bookmark")
	}
}

func TestAddBookmarkSyncChangeCarriesFullMetadata(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.AddBookmark("alice", "bookmark_full_meta", 7, "Saved Label", "alice,bob", "0,2"); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}

	var value string
	if err := d.QueryRow(
		`SELECT value FROM sync_changes WHERE type='bookmark' AND item_id='bookmark_full_meta' ORDER BY version DESC LIMIT 1`,
	).Scan(&value); err != nil {
		t.Fatalf("read sync change: %v", err)
	}
	for _, want := range []string{
		`"bookmarked":true`,
		`"category_id":7`,
		`"custom_title":"Saved Label"`,
		`"account_handles":"alice,bob"`,
		`"media_indices":"0,2"`,
		`"bookmarked_at":`,
	} {
		if !strings.Contains(value, want) {
			t.Fatalf("sync value = %s, missing %s", value, want)
		}
	}
}

func TestClearBookmarkLabelRecordsBookmarkClears(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
		VALUES ('alice', 'labelled', 7, 'Saved Label', 'alice', '0', 1000)
	`); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.ClearBookmarkLabel("alice", "Saved Label"); err != nil {
		t.Fatalf("ClearBookmarkLabel: %v", err)
	}

	var customTitle, value string
	if err := d.QueryRow(`SELECT COALESCE(custom_title, '') FROM bookmarks WHERE video_id='labelled'`).Scan(&customTitle); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if customTitle != "" {
		t.Fatalf("custom_title = %q, want cleared", customTitle)
	}
	if err := d.QueryRow(
		`SELECT value FROM sync_changes WHERE type='bookmark' AND item_id='labelled' ORDER BY version DESC LIMIT 1`,
	).Scan(&value); err != nil {
		t.Fatalf("read sync change: %v", err)
	}
	if !strings.Contains(value, `"custom_title":""`) || !strings.Contains(value, `"bookmarked":true`) {
		t.Fatalf("sync value = %s, want explicit empty custom_title", value)
	}
}

func TestCreateAndDeleteBookmarkCategory(t *testing.T) {
	d := openWritableTestDB(t)

	catID, err := d.CreateBookmarkCategory("", "Test Category", "")
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}
	if catID <= 0 {
		t.Errorf("expected positive category ID, got %d", catID)
	}
	var createValue string
	if err := d.QueryRow(
		`SELECT value FROM sync_changes WHERE type='bookmark_category' AND item_id=? ORDER BY version DESC LIMIT 1`,
		catID,
	).Scan(&createValue); err != nil {
		t.Fatalf("read category create sync change: %v", err)
	}
	if !strings.Contains(createValue, `"action":"set"`) || !strings.Contains(createValue, `"name":"Test Category"`) {
		t.Fatalf("category create sync value = %s", createValue)
	}

	if err := d.UpdateBookmarkCategory("", catID, "Updated Category", ""); err != nil {
		t.Fatalf("UpdateBookmarkCategory: %v", err)
	}
	var updateValue string
	if err := d.QueryRow(
		`SELECT value FROM sync_changes WHERE type='bookmark_category' AND item_id=? ORDER BY version DESC LIMIT 1`,
		catID,
	).Scan(&updateValue); err != nil {
		t.Fatalf("read category update sync change: %v", err)
	}
	if !strings.Contains(updateValue, `"action":"set"`) || !strings.Contains(updateValue, `"name":"Updated Category"`) {
		t.Fatalf("category update sync value = %s", updateValue)
	}

	err = d.DeleteBookmarkCategory("", catID)
	if err != nil {
		t.Fatalf("DeleteBookmarkCategory: %v", err)
	}
	var deleteValue string
	if err := d.QueryRow(
		`SELECT value FROM sync_changes WHERE type='bookmark_category' AND item_id=? ORDER BY version DESC LIMIT 1`,
		catID,
	).Scan(&deleteValue); err != nil {
		t.Fatalf("read category delete sync change: %v", err)
	}
	if !strings.Contains(deleteValue, `"action":"clear"`) {
		t.Fatalf("category delete sync value = %s", deleteValue)
	}
}

func TestGetBookmarksFallsBackToFeedPublishedAtForStubVideos(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID         = "tweet_bookmark_stub"
		channelID       = "twitter_stub_author"
		feedPublishedAt = int64(1776885729547)
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES (?, ?, 'X post tweet_bookmark_stub', 0, '', 0)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, body_text, canonical_url, published_at, fetched_at)
		VALUES (?, 'stub_author', 'stub_author', 'stub body', 'https://x.com/stub_author/status/tweet_bookmark_stub', ?, ?)
	`, videoID, feedPublishedAt, feedPublishedAt); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
		VALUES ('admin', ?, 0, ?)
	`, videoID, feedPublishedAt+1000); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{UserID: "admin", Limit: 10})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if bookmarks[0].PublishedAt == nil {
		t.Fatal("expected PublishedAt to be populated from feed_items fallback")
	}
	if got := bookmarks[0].PublishedAt.UnixMilli(); got != feedPublishedAt {
		t.Fatalf("PublishedAt = %d, want %d", got, feedPublishedAt)
	}
}

func TestGetBookmarksDerivesTikTokSlideshowFromFeedMediaFiles(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID   = "slide_stub_001"
		channelID = "tiktok_demo_author"
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES (?, ?, 'TikTok slideshow', 0, '', 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, fetched_at)
		VALUES (?, 'demo_author', 'demo_author', '[{"type":"video"}]', 1, 1)
	`, videoID); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
		VALUES ('admin', ?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.InsertMediaFileBatch([]model.MediaFile{
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 0, FilePath: "media/tiktok/demo_author/slide_stub_001_0_1.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 1, FilePath: "media/tiktok/demo_author/slide_stub_001_0_2.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 2, FilePath: "media/tiktok/demo_author/slide_stub_001_0_3.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 3, FilePath: "media/tiktok/demo_author/slide_stub_001_0_4.jpg", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 4, FilePath: "media/tiktok/demo_author/slide_stub_001_0.mp3", MediaType: "video"},
	}); err != nil {
		t.Fatalf("insert media files: %v", err)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{UserID: "admin", Limit: 10})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if got := bookmarks[0].MediaKind; got != "slideshow" {
		t.Fatalf("MediaKind = %q, want slideshow", got)
	}
	if got := bookmarks[0].MediaSlideCount; got != 4 {
		t.Fatalf("MediaSlideCount = %d, want 4", got)
	}
}

func TestGetBookmarksDerivesMixedTweetSlideshowFromFeedMediaFiles(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID   = "sample_tweet_mixed_media"
		channelID = "twitter_sample_author"
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES (?, ?, 'X post sample_tweet_mixed_media', 0, '', 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, fetched_at)
		VALUES (?, 'sample_source', 'sample_author', '[{"type":"photo"},{"type":"video"},{"type":"video"}]', 1, 1)
	`, videoID); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
		VALUES ('admin', ?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.InsertMediaFileBatch([]model.MediaFile{
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 0, FilePath: "media/twitter/sample_source/sample_tweet_mixed_media_0.jpg", MediaType: "photo"},
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 1, FilePath: "media/twitter/sample_source/sample_tweet_mixed_media_1.mp4", MediaType: "video"},
		{OwnerType: "feed_media", OwnerID: videoID, MediaIndex: 2, FilePath: "media/twitter/sample_source/sample_tweet_mixed_media_2.mp4", MediaType: "video"},
	}); err != nil {
		t.Fatalf("insert media files: %v", err)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{UserID: "admin", Limit: 10})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if got := bookmarks[0].MediaKind; got != "slideshow" {
		t.Fatalf("MediaKind = %q, want slideshow", got)
	}
	if got := bookmarks[0].MediaSlideCount; got != 3 {
		t.Fatalf("MediaSlideCount = %d, want 3", got)
	}
	wantTypes := []string{"image", "video", "video"}
	if got := bookmarks[0].MediaTypes; strings.Join(got, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("MediaTypes = %#v, want %#v", got, wantTypes)
	}
}

func TestGetBookmarksDerivesImageFromDirectQuoteMediaFiles(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID   = "sample_direct_quote_media"
		channelID = "twitter_sample_author"
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES (?, ?, 'X post sample_direct_quote_media', 0, '', 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, published_at, fetched_at)
		VALUES (?, 'sample_source', 'sample_author', 1, 1)
	`, videoID); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
		VALUES ('admin', ?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.InsertMediaFileBatch([]model.MediaFile{
		{OwnerType: "quote_media", OwnerID: videoID, MediaIndex: 0, FilePath: "media/twitter/sample_source/sample_direct_quote_media_0.jpg", MediaType: "photo"},
	}); err != nil {
		t.Fatalf("insert media files: %v", err)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{UserID: "admin", Limit: 10})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if got := bookmarks[0].MediaKind; got != "image" {
		t.Fatalf("MediaKind = %q, want image", got)
	}
	if got := bookmarks[0].MediaSlideCount; got != 1 {
		t.Fatalf("MediaSlideCount = %d, want 1", got)
	}
	wantTypes := []string{"image"}
	if got := bookmarks[0].MediaTypes; strings.Join(got, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("MediaTypes = %#v, want %#v", got, wantTypes)
	}
}
