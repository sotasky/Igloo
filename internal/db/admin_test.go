package db

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

// TestGetAllSettings verifies GetAllSettings returns settings written by SetSetting.
func TestGetAllSettings(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.SetSetting("", "test_key_a", "value_a"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := d.SetSetting("", "test_key_b", "value_b"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	all, err := d.GetAllSettings()
	if err != nil {
		t.Fatalf("GetAllSettings: %v", err)
	}
	if all["test_key_a"] != "value_a" {
		t.Errorf("test_key_a: got %q, want %q", all["test_key_a"], "value_a")
	}
	if all["test_key_b"] != "value_b" {
		t.Errorf("test_key_b: got %q, want %q", all["test_key_b"], "value_b")
	}
}

// TestUpdateSettings verifies batch upsert adds new keys and overwrites existing ones.
func TestUpdateSettings(t *testing.T) {
	d := openWritableTestDB(t)

	// Seed one key
	if err := d.SetSetting("", "existing_key", "old_value"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	updates := map[string]string{
		"existing_key": "new_value",
		"brand_new":    "fresh",
	}
	if err := d.UpdateSettings(updates); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	all, err := d.GetAllSettings()
	if err != nil {
		t.Fatalf("GetAllSettings: %v", err)
	}
	if all["existing_key"] != "new_value" {
		t.Errorf("existing_key: got %q, want %q", all["existing_key"], "new_value")
	}
	if all["brand_new"] != "fresh" {
		t.Errorf("brand_new: got %q, want %q", all["brand_new"], "fresh")
	}
}

// TestAddAndGetChannel verifies adding and then retrieving a channel by ID.
func TestAddAndGetChannel(t *testing.T) {
	d := openWritableTestDB(t)

	ch := model.Channel{
		ChannelID:    "test_ch_001",
		Name:         "Test Channel",
		URL:          "https://example.com/test",
		Platform:     "youtube",
		IsSubscribed: true,
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	// MaxVideos now lives in the channel_settings side table.
	if err := d.UpdateChannelSettings("test_ch_001", map[string]any{"max_videos": 10}); err != nil {
		t.Fatalf("UpdateChannelSettings: %v", err)
	}

	got, err := d.GetChannelByID("test_ch_001")
	if err != nil {
		t.Fatalf("GetChannelByID: %v", err)
	}
	if got.ChannelID != "test_ch_001" {
		t.Errorf("ChannelID: got %q, want %q", got.ChannelID, "test_ch_001")
	}
	if got.Name != "Test Channel" {
		t.Errorf("Name: got %q, want %q", got.Name, "Test Channel")
	}
	if got.Platform != "youtube" {
		t.Errorf("Platform: got %q, want %q", got.Platform, "youtube")
	}
	if !got.IsSubscribed {
		t.Error("IsSubscribed should be true when set")
	}
	if got.IsStarred {
		t.Error("IsStarred should be false by default")
	}
	settings, err := d.GetChannelSettings("test_ch_001")
	if err != nil {
		t.Fatalf("GetChannelSettings: %v", err)
	}
	if settings.MaxVideos != 10 {
		t.Errorf("MaxVideos: got %d, want 10", settings.MaxVideos)
	}
}

// TestAddChannelDuplicate verifies a second insert with the same channel_id errors.
func TestAddChannelDuplicate(t *testing.T) {
	d := openWritableTestDB(t)

	ch := model.Channel{
		ChannelID: "sample_dup_ch_001",
		Name:      "Dup Channel",
		Platform:  "youtube",
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("first AddChannel: %v", err)
	}
	if err := d.AddChannel(ch); err == nil {
		t.Fatal("second AddChannel should have returned an error for duplicate channel_id")
	}
}

// TestDeleteChannel verifies a channel can be added and then deleted.
func TestDeleteChannel(t *testing.T) {
	d := openWritableTestDB(t)

	ch := model.Channel{
		ChannelID: "sample_del_ch_001",
		Name:      "Delete Me",
		Platform:  "youtube",
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	if err := d.DeleteChannel("sample_del_ch_001"); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	// Verify gone
	if _, err := d.GetChannelByID("sample_del_ch_001"); err == nil {
		t.Fatal("GetChannelByID should error after deletion")
	}

	// Delete non-existent channel should error
	if err := d.DeleteChannel("sample_del_ch_001"); err == nil {
		t.Fatal("DeleteChannel of already-deleted channel should error")
	}
}

// TestDeleteMediaFilesByOwner verifies media files are deleted by owner.
func TestDeleteMediaFilesByOwner(t *testing.T) {
	d := openWritableTestDB(t)

	mf := model.MediaFile{
		OwnerType:  "avatar",
		OwnerID:    "test_owner_999",
		MediaIndex: 0,
		FilePath:   "avatars/test_owner_999.jpg",
		MediaType:  "avatar",
	}
	if err := d.InsertMediaFile(mf); err != nil {
		t.Fatalf("InsertMediaFile: %v", err)
	}

	// Verify it was inserted
	if _, err := d.GetMediaFilePath("avatar", "test_owner_999", 0); err != nil {
		t.Fatalf("GetMediaFilePath: expected file, got error: %v", err)
	}

	// Delete by owner
	if err := d.DeleteMediaFilesByOwner("avatar", "test_owner_999"); err != nil {
		t.Fatalf("DeleteMediaFilesByOwner: %v", err)
	}

	// Verify gone
	if _, err := d.GetMediaFilePath("avatar", "test_owner_999", 0); err == nil {
		t.Fatal("GetMediaFilePath should error after deletion")
	}
}

// TestExportConfig verifies ExportConfig returns a valid structure.
func TestExportConfig(t *testing.T) {
	d := openWritableTestDB(t)

	// Add a channel
	ch := model.Channel{
		ChannelID:    "sample_export_ch_001",
		Name:         "Export Test Channel",
		Platform:     "youtube",
		IsSubscribed: true,
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	// Set a setting
	if err := d.SetSetting("", "export_test_key", "export_test_val"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := d.SetSetting("", "youtube_check_interval", "6"); err != nil {
		t.Fatalf("SetSetting legacy interval: %v", err)
	}

	cfg, err := d.ExportConfig("")
	if err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	if cfg.Version != 1 {
		t.Errorf("Version: got %d, want 1", cfg.Version)
	}
	if cfg.ExportedAt.IsZero() {
		t.Error("ExportedAt should not be zero")
	}

	// Verify the new channel appears in subscriptions
	found := false
	for _, ch := range cfg.Subscriptions {
		if ch.ChannelID == "sample_export_ch_001" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ExportConfig: expected sample_export_ch_001 in Subscriptions")
	}

	// Verify settings are exported
	if cfg.Settings["export_test_key"] != "export_test_val" {
		t.Errorf("Settings[export_test_key]: got %q, want %q",
			cfg.Settings["export_test_key"], "export_test_val")
	}
	if _, ok := cfg.Settings["youtube_check_interval"]; ok {
		t.Fatal("ExportConfig should not export retired interval settings")
	}

	// Settings and Subscriptions should be non-nil maps/slices
	if cfg.Settings == nil {
		t.Error("Settings map should not be nil")
	}
}

func TestImportConfigIgnoresRetiredIntervalSettings(t *testing.T) {
	d := openWritableTestDB(t)

	cfg := ConfigExport{
		Version: 1,
		Settings: map[string]string{
			"youtube_fetch_delay":    "12",
			"youtube_check_interval": "6",
			"shorts_check_interval":  "3",
		},
	}
	if _, err := d.ImportConfig(cfg, "alice", false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	if got, err := d.GetSetting("youtube_fetch_delay", ""); err != nil || got != "12" {
		t.Fatalf("youtube_fetch_delay = %q, %v; want 12, nil", got, err)
	}
	for _, key := range []string{"youtube_check_interval", "shorts_check_interval"} {
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, key).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", key, err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0", key, count)
		}
	}
}

func TestImportConfigPreservesDefaultCategoryBookmarkLabel(t *testing.T) {
	d := openWritableTestDB(t)

	cfg := ConfigExport{
		Version: 1,
		Bookmarks: []BookmarkExport{
			{VideoID: "sample_default_bookmark", CustomTitle: "Saved Label"},
		},
	}
	result, err := d.ImportConfig(cfg, "alice", false)
	if err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}
	if result.AddedBookmarks != 1 {
		t.Fatalf("AddedBookmarks = %d, want 1", result.AddedBookmarks)
	}

	labels, err := d.GetBookmarkLabels("alice", "")
	if err != nil {
		t.Fatalf("GetBookmarkLabels: %v", err)
	}
	if len(labels) != 1 || labels[0] != "Saved Label" {
		t.Fatalf("labels = %#v, want Saved Label", labels)
	}
}

func TestImportConfigPreservesFullExportStateTimestamps(t *testing.T) {
	d := openWritableTestDB(t)

	cfg := ConfigExport{
		Version: 1,
		BookmarkCategories: []BookmarkCatExport{{
			Name: "Saved",
		}},
		Bookmarks: []BookmarkExport{{
			VideoID:        "sample_stateful_bookmark",
			CategoryName:   "Saved",
			CustomTitle:    "clips",
			AccountHandles: "@author",
			MediaIndices:   "0,2",
			BookmarkedAt:   1710000000000,
		}},
		LikedPosts: []LikedPostExport{{
			TweetID:          "sample_stateful_like",
			SourceHandle:     "sample_source",
			AuthorHandle:     "sample_author",
			BodyText:         "liked text",
			Platform:         "twitter",
			PublishedAtMs:    1709000000000,
			CanonicalXLink:   "https://x.com/sample_author/status/stateful_like",
			MediaJSON:        `[{"type":"photo"}]`,
			QuotePayloadJSON: `{"tweet_id":"sample_quote"}`,
			LikedAt:          1710000001000,
			UpdatedAt:        1710000002000,
		}},
		FeedSeen: []FeedSeenExport{{
			TweetID: "sample_stateful_seen",
			SeenAt:  1710000002500,
		}},
		BookmarkedVideos: []BookmarkedVideoExport{{
			VideoID:       "sample_stateful_video",
			ChannelID:     "youtube_sample_UCstateful",
			Title:         "Stateful Video",
			PublishedAtMs: 1708000000000,
			BookmarkedAt:  1710000003000,
		}},
	}
	if _, err := d.ImportConfig(cfg, "alice", false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	var bookmarkedAt int64
	var accountHandles, mediaIndices string
	if err := d.QueryRow(`
		SELECT bookmarked_at, COALESCE(account_handles,''), COALESCE(media_indices,'')
		FROM bookmarks WHERE user_id = 'alice' AND video_id = 'sample_stateful_bookmark'
	`).Scan(&bookmarkedAt, &accountHandles, &mediaIndices); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if bookmarkedAt != 1710000000000 || accountHandles != "@author" || mediaIndices != "0,2" {
		t.Fatalf("bookmark state = %d %q %q", bookmarkedAt, accountHandles, mediaIndices)
	}

	var likedAt, updatedAt, likePublishedAt int64
	var sourceHandle, canonicalLink, mediaJSON, quoteJSON string
	if err := d.QueryRow(`
		SELECT liked_at, updated_at, published_at, COALESCE(source_handle,''),
		       COALESCE(canonical_x_link,''), COALESCE(media_json,''),
		       COALESCE(quote_payload_json,'')
		FROM feed_likes WHERE username = 'alice' AND tweet_id = 'sample_stateful_like'
	`).Scan(&likedAt, &updatedAt, &likePublishedAt, &sourceHandle, &canonicalLink, &mediaJSON, &quoteJSON); err != nil {
		t.Fatalf("read like: %v", err)
	}
	if likedAt != 1710000001000 || updatedAt != 1710000002000 || likePublishedAt != 1709000000000 {
		t.Fatalf("like timestamps = liked:%d updated:%d published:%d", likedAt, updatedAt, likePublishedAt)
	}
	if sourceHandle != "sample_source" || canonicalLink == "" || mediaJSON == "" || quoteJSON == "" {
		t.Fatalf("like metadata = source:%q canonical:%q media:%q quote:%q", sourceHandle, canonicalLink, mediaJSON, quoteJSON)
	}

	var seenAt int64
	if err := d.QueryRow(`
		SELECT seen_at
		FROM feed_seen
		WHERE username = 'alice' AND tweet_id = 'sample_stateful_seen'
	`).Scan(&seenAt); err != nil {
		t.Fatalf("read feed seen: %v", err)
	}
	if seenAt != 1710000002500 {
		t.Fatalf("seen_at = %d, want 1710000002500", seenAt)
	}

	var videoPublishedAt, videoBookmarkedAt int64
	if err := d.QueryRow(`
		SELECT v.published_at, b.bookmarked_at
		FROM videos v
		JOIN bookmarks b ON b.video_id = v.video_id
		WHERE b.user_id = 'alice' AND v.video_id = 'sample_stateful_video'
	`).Scan(&videoPublishedAt, &videoBookmarkedAt); err != nil {
		t.Fatalf("read bookmarked video: %v", err)
	}
	if videoPublishedAt != 1708000000000 || videoBookmarkedAt != 1710000003000 {
		t.Fatalf("bookmarked video timestamps = published:%d bookmarked:%d", videoPublishedAt, videoBookmarkedAt)
	}
}

func TestImportConfigRepairsExistingZeroBookmarkTimestamps(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO bookmark_categories (id, user_id, name) VALUES (7, 'alice', 'Saved')
	`); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
		VALUES ('alice', 'sample_existing_bookmark', 0, 0)
	`); err != nil {
		t.Fatalf("seed bookmark: %v", err)
	}

	cfg := ConfigExport{
		Version: 1,
		BookmarkCategories: []BookmarkCatExport{{
			Name: "Saved",
		}},
		Bookmarks: []BookmarkExport{{
			VideoID:      "sample_existing_bookmark",
			CategoryName: "Saved",
			CustomTitle:  "Recovered title",
			BookmarkedAt: 1710000000000,
		}},
		BookmarkedVideos: []BookmarkedVideoExport{{
			VideoID:      "sample_existing_bookmark",
			CategoryName: "Saved",
			BookmarkedAt: 1710000001000,
		}},
	}
	if _, err := d.ImportConfig(cfg, "alice", false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	var categoryID, bookmarkedAt int64
	var customTitle string
	if err := d.QueryRow(`
		SELECT category_id, COALESCE(custom_title, ''), bookmarked_at
		FROM bookmarks
		WHERE user_id = 'alice' AND video_id = 'sample_existing_bookmark'
	`).Scan(&categoryID, &customTitle, &bookmarkedAt); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if categoryID <= 0 || customTitle != "Recovered title" || bookmarkedAt != 1710000000000 {
		t.Fatalf("bookmark after import = category:%d title:%q at:%d", categoryID, customTitle, bookmarkedAt)
	}
}

func TestImportConfigRepairsExistingBookmarkedTikTokPublishDate(t *testing.T) {
	d := openWritableTestDB(t)

	const videoID = "9000000000000000000" // igloo-hygiene: allow-social-fixture synthetic TikTok snowflake
	const wantPublishedAt int64 = 2095475792000

	if _, err := d.conn.Exec(`
		INSERT INTO videos (video_id, channel_id, title, duration, published_at, sync_seq)
		VALUES (?, ?, 'Old title', 0, 0, 0)
	`, videoID, "tiktok_sample_awesome0day"); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	cfg := ConfigExport{
		Version: 1,
		BookmarkedVideos: []BookmarkedVideoExport{{
			VideoID:      videoID,
			ChannelID:    "tiktok_sample_awesome0day",
			Title:        "Restored title",
			BookmarkedAt: 1710000000000,
		}},
	}
	if _, err := d.ImportConfig(cfg, "alice", false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	var publishedAt, syncSeq int64
	if err := d.QueryRow(`
		SELECT published_at, sync_seq
		FROM videos
		WHERE video_id = ?
	`, videoID).Scan(&publishedAt, &syncSeq); err != nil {
		t.Fatalf("read video: %v", err)
	}
	if publishedAt != wantPublishedAt {
		t.Fatalf("published_at = %d, want %d", publishedAt, wantPublishedAt)
	}
	if syncSeq <= 0 {
		t.Fatalf("sync_seq = %d, want bumped", syncSeq)
	}
}

func TestExportFullDataCarriesStateTimestampsAndMetadata(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO bookmark_categories (id, user_id, name) VALUES (70, 'alice', 'Saved')
	`); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_UCexport', 'Export Channel', 'youtube')
	`); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, published_at)
		VALUES ('export_video', 'youtube_UCexport', 'Export Video', 12, 1707000000000)
	`); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks
			(user_id, video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
		VALUES ('alice', 'export_video', 70, 'clips', '@author', '1', 1711000000000)
	`); err != nil {
		t.Fatalf("seed bookmark: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_likes
			(username, tweet_id, source_handle, author_handle, author_display_name,
			 body_text, canonical_x_link, published_at, media_json, platform,
			 quote_payload_json, liked_at, updated_at)
		VALUES
			('alice', 'export_like', 'source', 'author', 'Author',
			 'liked text', 'https://x.com/sample_author/status/export_like', 1706000000000,
			 '[{"type":"photo"}]', 'twitter', '{"tweet_id":"sample_quote"}', 1712000000000, 1712000001000)
	`); err != nil {
		t.Fatalf("seed like: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_seen (username, tweet_id, seen_at)
		VALUES ('alice', 'export_seen', 1713000000000)
	`); err != nil {
		t.Fatalf("seed seen: %v", err)
	}

	cfg, err := d.ExportFullData("alice")
	if err != nil {
		t.Fatalf("ExportFullData: %v", err)
	}
	if len(cfg.Bookmarks) != 1 {
		t.Fatalf("bookmarks = %d, want 1", len(cfg.Bookmarks))
	}
	bm := cfg.Bookmarks[0]
	if bm.BookmarkedAt != 1711000000000 || bm.AccountHandles != "@author" || bm.MediaIndices != "1" {
		t.Fatalf("exported bookmark = %#v", bm)
	}
	if len(cfg.LikedPosts) != 1 {
		t.Fatalf("liked posts = %d, want 1", len(cfg.LikedPosts))
	}
	lp := cfg.LikedPosts[0]
	if lp.LikedAt != 1712000000000 || lp.UpdatedAt != 1712000001000 || lp.PublishedAtMs != 1706000000000 {
		t.Fatalf("exported like timestamps = %#v", lp)
	}
	if lp.SourceHandle != "source" || lp.CanonicalXLink == "" || lp.MediaJSON == "" || lp.QuotePayloadJSON == "" {
		t.Fatalf("exported like metadata = %#v", lp)
	}
	if len(cfg.FeedSeen) != 1 {
		t.Fatalf("feed seen = %d, want 1", len(cfg.FeedSeen))
	}
	seen := cfg.FeedSeen[0]
	if seen.TweetID != "export_seen" || seen.SeenAt != 1713000000000 {
		t.Fatalf("exported seen = %#v", seen)
	}
	if len(cfg.BookmarkedVideos) != 1 {
		t.Fatalf("bookmarked videos = %d, want 1", len(cfg.BookmarkedVideos))
	}
	bv := cfg.BookmarkedVideos[0]
	if bv.PublishedAtMs != 1707000000000 || bv.BookmarkedAt != 1711000000000 {
		t.Fatalf("exported video timestamps = %#v", bv)
	}
}

func TestImportConfigPublishesImportedRowsToDelta(t *testing.T) {
	d := openWritableTestDB(t)

	cfg := ConfigExport{
		Version: 1,
		Subscriptions: []ChannelExport{{
			ChannelID: "youtube_UCexampleImported",
			Name:      "Imported Channel",
			Platform:  "youtube",
		}},
		BookmarkedVideos: []BookmarkedVideoExport{{
			VideoID:     "sample_imported_video",
			ChannelID:   "youtube_UCexampleImported",
			Title:       "Imported Video",
			Platform:    "youtube",
			Duration:    42,
			PublishedAt: "2026-05-01T12:00:00Z",
		}},
	}
	if _, err := d.ImportConfig(cfg, "alice", false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	channels, _, err := d.ListChannelsForDelta(0, 500)
	if err != nil {
		t.Fatalf("ListChannelsForDelta: %v", err)
	}
	if !hasChannelID(channels, "youtube_UCexampleImported") {
		t.Fatalf("imported channel missing from delta: %#v", channels)
	}
	videos, _, err := d.ListVideosForDelta([]string{"youtube"}, 0, 500)
	if err != nil {
		t.Fatalf("ListVideosForDelta: %v", err)
	}
	if !hasVideoID(videos, "sample_imported_video") {
		t.Fatalf("imported video missing from delta: %#v", videos)
	}
}

func hasChannelID(channels []model.Channel, id string) bool {
	for _, ch := range channels {
		if ch.ChannelID == id {
			return true
		}
	}
	return false
}

func hasVideoID(videos []model.Video, id string) bool {
	for _, video := range videos {
		if video.VideoID == id {
			return true
		}
	}
	return false
}
