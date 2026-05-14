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

func TestAddAndRemoveBookmark(t *testing.T) {
	d := openWritableTestDB(t)

	// Use a real video_id from the DB (FK constraint)
	var videoID string
	_ = d.conn.QueryRow("SELECT video_id FROM videos LIMIT 1").Scan(&videoID)
	if videoID == "" {
		t.Skip("no videos in test DB")
	}

	// Use category_id=1 (default, FK constraint requires existing category)
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
