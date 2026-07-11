package db

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestIsBookmarked(t *testing.T) {
	d := openTestDB(t)
	bookmarked, categoryID, err := d.IsBookmarked("nonexistent_xyz")
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
	categories, err := d.GetBookmarkCategories()
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
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES (?, 1)
	`, channelID); err != nil {
		t.Fatalf("insert channel follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES (?, ?, 'tweet', 'X post tweet_followed_bookmark', 0, 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
		VALUES (?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{Limit: 10})
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
		"cinema_one",
		"cinema_two",
		"japan",
		"no_label_null",
		"no_label_empty",
		"no_label_space",
	} {
		seedTestVideo(t, d, videoID, "youtube_bookmark_labels")
	}

	fixtures := []struct {
		videoID     string
		categoryID  int64
		customTitle string
		insertNull  bool
		bookmarked  int64
	}{
		{"cinema_one", 1, " cinema ", false, 10},
		{"cinema_two", 2, "cinema", false, 20},
		{"japan", 1, "japan", false, 30},
		{"no_label_null", 1, "", true, 40},
		{"no_label_empty", 2, "", false, 50},
		{"no_label_space", 2, "   ", false, 60},
	}
	for _, f := range fixtures {
		var err error
		if f.insertNull {
			err = d.ExecRaw(`
				INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
				VALUES (?, ?, ?)
			`, f.videoID, f.categoryID, f.bookmarked)
		} else {
			err = d.ExecRaw(`
				INSERT INTO bookmarks (video_id, category_id, custom_title, bookmarked_at)
				VALUES (?, ?, ?, ?)
			`, f.videoID, f.categoryID, f.customTitle, f.bookmarked)
		}
		if err != nil {
			t.Fatalf("insert bookmark %s: %v", f.videoID, err)
		}
	}

	exactOpts := GetBookmarksOpts{
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
	if got := bookmarkVideoIDs(bookmarks); strings.Join(got, ",") != "cinema_two,cinema_one" {
		t.Fatalf("exact label videos = %v", got)
	}

	noLabelOpts := GetBookmarksOpts{
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
	if got := bookmarkVideoIDs(bookmarks); strings.Join(got, ",") != "no_label_space,no_label_empty,no_label_null" {
		t.Fatalf("no-label videos = %v", got)
	}

	categoryCount, err := d.GetBookmarkCount(GetBookmarksOpts{CategoryID: 1})
	if err != nil {
		t.Fatalf("GetBookmarkCount category: %v", err)
	}
	if categoryCount != 3 {
		t.Fatalf("category count = %d, want 3", categoryCount)
	}

	labelCounts, err := d.GetBookmarkLabelCounts()
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
		INSERT INTO bookmark_categories (id, name, created_at)
		VALUES (1, 'Default', 1)
	`); err != nil {
		t.Fatalf("seed bookmark category: %v", err)
	}

	err := d.AddBookmark(videoID, 1, "", "", "")
	if err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}

	bookmarked, catID, err := d.IsBookmarked(videoID)
	if err != nil {
		t.Fatalf("IsBookmarked: %v", err)
	}
	if !bookmarked {
		t.Error("expected video to be bookmarked")
	}
	_ = catID

	err = d.RemoveBookmark(videoID)
	if err != nil {
		t.Fatalf("RemoveBookmark: %v", err)
	}

	bookmarked, _, _ = d.IsBookmarked(videoID)
	if bookmarked {
		t.Error("expected video to not be bookmarked after remove")
	}
}

func TestAddAndRemoveBookmarkResolveCanonicalStatusURL(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		originalID = "1000000000000000201"
		repostID   = "1000000000000000202"
	)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, body_text, canonical_url)
		VALUES (?, 'twitter_sample_reposter', 'twitter_sample_author', 'body', ?)`,
		repostID, "https://x.com/sample_author/status/"+originalID,
	); err != nil {
		t.Fatalf("seed repost row: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, fetched_at)
		VALUES (?, 1)`,
		originalID,
	); err != nil {
		t.Fatalf("seed hollow canonical row: %v", err)
	}
	const repostPath = "media/twitter/sample_author/repost_0.jpg"
	absRepostPath, err := d.storage.Path(repostPath)
	if err != nil {
		t.Fatalf("resolve repost media: %v", err)
	}
	writeDBTestFile(t, absRepostPath, []byte("sample repost media"))
	if err := d.StoreReadyAsset(Asset{
		AssetID:   BuildAssetID("twitter", "tweet", repostID, "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: repostID,
		SourceURL: "https://pbs.twimg.com/media/sample_repost_0.jpg",
		FilePath:  repostPath, ContentType: "image/jpeg", RequiredReason: "retention",
	}, 1000); err != nil {
		t.Fatalf("seed repost asset: %v", err)
	}

	if err := d.AddBookmark(repostID, 0, "", "", ""); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}
	bookmarked, _, err := d.IsBookmarked(repostID)
	if err != nil {
		t.Fatalf("IsBookmarked: %v", err)
	}
	if !bookmarked {
		t.Fatalf("repost bookmark state should resolve through canonical status URL")
	}
	var originalRows, repostRows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id = ?`, originalID).Scan(&originalRows); err != nil {
		t.Fatalf("count original bookmark: %v", err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id = ?`, repostID).Scan(&repostRows); err != nil {
		t.Fatalf("count repost bookmark: %v", err)
	}
	if originalRows != 1 || repostRows != 0 {
		t.Fatalf("bookmark rows original=%d repost=%d, want original only", originalRows, repostRows)
	}
	asset, err := d.GetAsset(BuildAssetID("twitter", "tweet", originalID, "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset canonical media: %v", err)
	}
	if asset == nil || asset.OwnerID != originalID || asset.State != AssetStateReady ||
		asset.FilePath != repostPath || asset.SizeBytes <= 0 || len(asset.SHA256) != 64 || asset.FileMtimeNs <= 0 ||
		asset.RequiredReason != "bookmark" {
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

	if err := d.RemoveBookmark(repostID); err != nil {
		t.Fatalf("RemoveBookmark: %v", err)
	}
	bookmarked, _, err = d.IsBookmarked(repostID)
	if err != nil {
		t.Fatalf("IsBookmarked after remove: %v", err)
	}
	if bookmarked {
		t.Fatalf("repost bookmark state should clear canonical bookmark")
	}
	asset, err = d.GetAsset(BuildAssetID("twitter", "tweet", originalID, "post_media", 0), "post_media")
	if err != nil || asset == nil || asset.RequiredReason != "retention" {
		t.Fatalf("removed bookmark asset requirement = %+v, err=%v", asset, err)
	}
}

func TestClearBookmarkLabel(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
		VALUES ('labelled', 7, 'Saved Label', 'alice', '0', 1000)
	`); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.ClearBookmarkLabel("Saved Label"); err != nil {
		t.Fatalf("ClearBookmarkLabel: %v", err)
	}

	var customTitle string
	if err := d.QueryRow(`SELECT COALESCE(custom_title, '') FROM bookmarks WHERE video_id='labelled'`).Scan(&customTitle); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if customTitle != "" {
		t.Fatalf("custom_title = %q, want cleared", customTitle)
	}
	var clockAt, bookmarkedAt int64
	if err := d.QueryRow(`
		SELECT mc.updated_at_ms, b.bookmarked_at
		FROM mutation_clocks mc
		JOIN bookmarks b ON b.video_id = mc.item_key
		WHERE mc.kind = 'bookmark' AND mc.item_key = 'labelled' AND mc.action = 'set'
	`).Scan(&clockAt, &bookmarkedAt); err != nil {
		t.Fatalf("label clear clock: %v", err)
	}
	if bookmarkedAt != clockAt {
		t.Fatalf("bookmark timestamp = %d, clock = %d", bookmarkedAt, clockAt)
	}
	restored := "Saved Label"
	if _, err := d.MutateBookmark(BookmarkMutation{
		VideoID: "labelled", Action: "set", CustomTitle: &restored, UpdatedAtMs: clockAt - 1,
	}); !IsStaleMutation(err) {
		t.Fatalf("older label restore error = %v, want stale mutation", err)
	}
}

func TestCreateAndDeleteBookmarkCategory(t *testing.T) {
	d := openWritableTestDB(t)

	catID, err := d.CreateBookmarkCategory("Test Category", "")
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}
	if catID <= 0 {
		t.Errorf("expected positive category ID, got %d", catID)
	}

	if err := d.UpdateBookmarkCategory(catID, "Updated Category", ""); err != nil {
		t.Fatalf("UpdateBookmarkCategory: %v", err)
	}
	var name string
	if err := d.QueryRow(`SELECT name FROM bookmark_categories WHERE id = ?`, catID).Scan(&name); err != nil {
		t.Fatalf("read updated category: %v", err)
	}
	if name != "Updated Category" {
		t.Fatalf("category name = %q", name)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
		VALUES ('categorized', ?, 1000)
	`, catID); err != nil {
		t.Fatalf("seed categorized bookmark: %v", err)
	}

	err = d.DeleteBookmarkCategory(catID)
	if err != nil {
		t.Fatalf("DeleteBookmarkCategory: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM bookmark_categories WHERE id = ?`, catID).Scan(&count); err != nil {
		t.Fatalf("count deleted category: %v", err)
	}
	if count != 0 {
		t.Fatalf("deleted category rows = %d", count)
	}
	var categoryID, clockAt, bookmarkedAt int64
	if err := d.QueryRow(`
		SELECT b.category_id, b.bookmarked_at, mc.updated_at_ms
		FROM bookmarks b
		JOIN mutation_clocks mc ON mc.kind = 'bookmark' AND mc.item_key = b.video_id
		WHERE b.video_id = 'categorized' AND mc.action = 'set'
	`).Scan(&categoryID, &bookmarkedAt, &clockAt); err != nil {
		t.Fatalf("moved bookmark clock: %v", err)
	}
	if categoryID != 0 || bookmarkedAt != clockAt {
		t.Fatalf("moved bookmark = category:%d at:%d clock:%d", categoryID, bookmarkedAt, clockAt)
	}
}

func TestGetBookmarksFallsBackToFeedPublishedAtForStubVideos(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID         = "sample_bookmark_stub"
		channelID       = "twitter_sample_author"
		feedPublishedAt = int64(1776885729547)
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES (?, ?, 'tweet', 'X post sample_bookmark_stub', 0, 0)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, body_text, canonical_url, published_at, fetched_at)
		VALUES (?, 'twitter_sample_author', 'twitter_sample_author', 'stub body', 'https://x.com/sample_author/status/sample_bookmark_stub', ?, ?)
	`, videoID, feedPublishedAt, feedPublishedAt); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
		VALUES (?, 0, ?)
	`, videoID, feedPublishedAt+1000); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{Limit: 10})
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

func TestGetBookmarksDerivesTikTokSlideshowFromCanonicalAssets(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID   = "slide_stub_001"
		channelID = "tiktok_demo_author"
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES (?, ?, 'tiktok_video', 'TikTok slideshow', 0, 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, media_json, published_at, fetched_at)
		VALUES (?, 'tiktok_demo_author', 'tiktok_demo_author', '[{"type":"video"}]', 1, 1)
	`, videoID); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
		VALUES (?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	for index := 0; index < 4; index++ {
		storeBookmarkReadyAsset(t, d, "tiktok", "tiktok_video", videoID, "post_media", index,
			filepath.ToSlash(filepath.Join("media", "tiktok", "demo_author", fmt.Sprintf("slide_stub_001_%d.jpg", index+1))), "image/jpeg")
	}
	storeBookmarkReadyAsset(t, d, "tiktok", "tiktok_video", videoID, "post_audio", 0,
		"media/tiktok/demo_author/slide_stub_001.mp3", "audio/mpeg")

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{Limit: 10})
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

func TestGetBookmarksDerivesMixedTweetSlideshowFromCanonicalAssets(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID   = "sample_tweet_mixed_media"
		channelID = "twitter_sample_author"
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES (?, ?, 'tweet', 'X post sample_tweet_mixed_media', 0, 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, media_json, published_at, fetched_at)
		VALUES (?, 'twitter_sample_source', 'twitter_sample_author', '[{"type":"photo"},{"type":"video"},{"type":"video"}]', 1, 1)
	`, videoID); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
		VALUES (?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	storeBookmarkReadyAsset(t, d, "twitter", "tweet", videoID, "post_media", 0,
		"media/twitter/sample_source/sample_tweet_mixed_media_0.jpg", "image/jpeg")
	storeBookmarkReadyAsset(t, d, "twitter", "tweet", videoID, "post_media", 1,
		"media/twitter/sample_source/sample_tweet_mixed_media_1.mp4", "video/mp4")
	storeBookmarkReadyAsset(t, d, "twitter", "tweet", videoID, "post_media", 2,
		"media/twitter/sample_source/sample_tweet_mixed_media_2.mp4", "video/mp4")
	storeBookmarkReadyAsset(t, d, "tiktok", "tiktok_video", videoID, "post_media", 3,
		"media/tiktok/sample_source/colliding_owner.jpg", "image/jpeg")

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{Limit: 10})
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

func TestGetBookmarksDerivesImageFromCanonicalQuoteAsset(t *testing.T) {
	d := openFreshTestDB(t)

	const (
		videoID   = "sample_direct_quote_media"
		channelID = "twitter_sample_author"
	)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES (?, ?, 'tweet', 'X post sample_direct_quote_media', 0, 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("insert video stub: %v", err)
	}
	const quoteID = "sample_quote_media"
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, quote_tweet_id, published_at, fetched_at)
		VALUES (?, 'twitter_sample_source', 'twitter_sample_author', ?, 1, 1)
	`, videoID, quoteID); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
		VALUES (?, 0, 2)
	`, videoID); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	storeBookmarkReadyAsset(t, d, "twitter", "tweet", quoteID, "post_media", 0,
		"media/twitter/sample_source/sample_direct_quote_media_0.jpg", "image/jpeg")

	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{Limit: 10})
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

func storeBookmarkReadyAsset(t *testing.T, d *DB, platform, ownerKind, ownerID, assetKind string, index int, key, contentType string) {
	t.Helper()
	path, err := d.storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	writeDBTestFile(t, path, []byte(ownerID+assetKind))
	if err := d.StoreReadyAsset(Asset{
		AssetID:   BuildAssetID(platform, ownerKind, ownerID, assetKind, index),
		AssetKind: assetKind, OwnerKind: ownerKind, OwnerID: ownerID, MediaIndex: index,
		FilePath: key, ContentType: contentType, RequiredReason: "bookmark",
	}, 2_000); err != nil {
		t.Fatal(err)
	}
}
