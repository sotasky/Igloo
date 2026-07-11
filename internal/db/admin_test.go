package db

import (
	"strconv"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

// TestGetAllSettings verifies GetAllSettings returns settings written by SetSetting.
func TestGetAllSettings(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.SetSetting("test_key_a", "value_a"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := d.SetSetting("test_key_b", "value_b"); err != nil {
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
	if err := d.SetSetting("existing_key", "old_value"); err != nil {
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
	var followedAt int64
	if err := d.QueryRow(`
		SELECT updated_at_ms FROM mutation_clocks
		WHERE kind = 'follow' AND item_key = 'test_ch_001'
	`).Scan(&followedAt); err != nil {
		t.Fatalf("follow clock: %v", err)
	}
	if _, err := d.MutateFollow("test_ch_001", "clear", followedAt-1); !IsStaleMutation(err) {
		t.Fatalf("older unfollow error = %v, want stale mutation", err)
	}
	if !d.IsChannelFollowed("test_ch_001") {
		t.Fatal("older unfollow removed AddChannel follow state")
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
	if err := d.SetSetting("export_test_key", "export_test_val"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := d.SetSetting("youtube_check_interval", "6"); err != nil {
		t.Fatalf("SetSetting legacy interval: %v", err)
	}

	cfg, err := d.ExportConfig()
	if err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	if cfg.Version != ConfigExportVersion {
		t.Errorf("Version: got %d, want %d", cfg.Version, ConfigExportVersion)
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

func TestExportSubscriptionsUsesFollowRows(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.AddChannel(model.Channel{
		ChannelID:    "youtube_sample_followed",
		Name:         "Followed Export",
		Platform:     "youtube",
		IsSubscribed: true,
		IsStarred:    true,
	}); err != nil {
		t.Fatalf("AddChannel followed: %v", err)
	}
	if err := d.UpdateChannelSettings("youtube_sample_followed", map[string]any{"max_videos": 12}); err != nil {
		t.Fatalf("UpdateChannelSettings followed: %v", err)
	}
	if err := d.AddChannel(model.Channel{
		ChannelID:    "youtube_sample_unfollowed",
		Name:         "Unfollowed Export",
		Platform:     "youtube",
		IsSubscribed: false,
	}); err != nil {
		t.Fatalf("AddChannel unfollowed: %v", err)
	}

	cfg, err := d.ExportSubscriptions()
	if err != nil {
		t.Fatalf("ExportSubscriptions: %v", err)
	}
	if cfg.Version != ConfigExportVersion || cfg.Scope != "subscriptions" || cfg.ExportedAt.IsZero() {
		t.Fatalf("export metadata = version:%d scope:%q at:%v", cfg.Version, cfg.Scope, cfg.ExportedAt)
	}
	if len(cfg.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %#v, want one followed channel", cfg.Subscriptions)
	}
	got := cfg.Subscriptions[0]
	if got.ChannelID != "youtube_sample_followed" || !got.IsStarred || got.MaxVideos != 12 {
		t.Fatalf("exported subscription = %#v", got)
	}
	if cfg.Settings != nil || cfg.Bookmarks != nil || cfg.BookmarkCategories != nil {
		t.Fatalf("subscription export carried non-subscription sections: %#v", cfg)
	}
}

func TestImportConfigReplaceSubscriptionsClearsStaleFollows(t *testing.T) {
	d := openWritableTestDB(t)

	for _, ch := range []model.Channel{
		{ChannelID: "youtube_sample_old", Name: "Old Follow", Platform: "youtube", IsSubscribed: true, IsStarred: true},
		{ChannelID: "youtube_sample_existing", Name: "Keep Follow", Platform: "youtube", IsSubscribed: true},
	} {
		if err := d.AddChannel(ch); err != nil {
			t.Fatalf("AddChannel %s: %v", ch.ChannelID, err)
		}
	}
	if err := d.UpdateChannelSettings("youtube_sample_old", map[string]any{"max_videos": 5}); err != nil {
		t.Fatalf("UpdateChannelSettings old: %v", err)
	}
	if err := d.UpdateChannelSettings("youtube_sample_existing", map[string]any{"max_videos": 9}); err != nil {
		t.Fatalf("UpdateChannelSettings keep: %v", err)
	}
	if err := d.SetSetting("preserved_setting", "still-here"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	result, err := d.ImportConfig(ConfigExport{
		Version: ConfigExportVersion,
		Scope:   "subscriptions",
		Subscriptions: []ChannelExport{
			{ChannelID: "youtube_sample_existing", Name: "Keep Follow", Platform: "youtube", MaxVideos: 14},
			{ChannelID: "youtube_sample_new", Name: "New Follow", Platform: "youtube"},
		},
	}, true)
	if err != nil {
		t.Fatalf("ImportConfig replace: %v", err)
	}
	if result.AddedChannels != 2 {
		t.Fatalf("AddedChannels = %d, want 2", result.AddedChannels)
	}

	for channelID, wantFollowed := range map[string]bool{
		"youtube_sample_old":      false,
		"youtube_sample_existing": true,
		"youtube_sample_new":      true,
	} {
		ch, err := d.GetChannelByID(channelID)
		if err != nil {
			t.Fatalf("GetChannelByID %s: %v", channelID, err)
		}
		if ch.IsSubscribed != wantFollowed {
			t.Fatalf("%s subscribed = %v, want %v", channelID, ch.IsSubscribed, wantFollowed)
		}
	}
	var oldStars int
	if err := d.QueryRow(`SELECT COUNT(*) FROM channel_stars WHERE channel_id = 'youtube_sample_old'`).Scan(&oldStars); err != nil {
		t.Fatalf("count old stars: %v", err)
	}
	var oldSettingsNull int
	var oldSettingsAt int64
	if err := d.QueryRow(`
		SELECT max_videos IS NULL AND download_subtitles IS NULL
		   AND media_only IS NULL AND media_download_limit IS NULL
		   AND include_reposts IS NULL,
		       updated_at
		FROM channel_settings WHERE channel_id = 'youtube_sample_old'
	`).Scan(&oldSettingsNull, &oldSettingsAt); err != nil {
		t.Fatalf("read old settings tombstone: %v", err)
	}
	if oldStars != 0 || oldSettingsNull != 1 {
		t.Fatalf("old follow state remained: stars=%d settings_null=%d", oldStars, oldSettingsNull)
	}
	var followAction string
	var followClockAt int64
	if err := d.QueryRow(`
		SELECT action, updated_at_ms FROM mutation_clocks
		WHERE kind = 'follow' AND item_key = 'youtube_sample_old'
	`).Scan(&followAction, &followClockAt); err != nil {
		t.Fatalf("old follow clock: %v", err)
	}
	if followAction != "clear" {
		t.Fatalf("old follow clock action = %q, want clear", followAction)
	}
	if _, err := d.MutateFollow("youtube_sample_old", "set", followClockAt-1); !IsStaleMutation(err) {
		t.Fatalf("older follow error = %v, want stale mutation", err)
	}
	if _, err := d.MutateChannelSetting("youtube_sample_old", "max_videos", 99, oldSettingsAt-1); !IsStaleMutation(err) {
		t.Fatalf("older channel setting error = %v, want stale mutation", err)
	}
	keepSettings, err := d.GetChannelSettings("youtube_sample_existing")
	if err != nil {
		t.Fatalf("GetChannelSettings keep: %v", err)
	}
	if keepSettings.MaxVideos != 14 {
		t.Fatalf("keep max_videos = %d, want 14", keepSettings.MaxVideos)
	}
	if got, err := d.GetSetting("preserved_setting", ""); err != nil || got != "still-here" {
		t.Fatalf("preserved_setting = %q, %v", got, err)
	}
}

func TestImportConfigIgnoresRetiredIntervalSettings(t *testing.T) {
	d := openWritableTestDB(t)

	cfg := ConfigExport{
		Version: ConfigExportVersion,
		Settings: map[string]string{
			"youtube_fetch_delay":    "12",
			"youtube_check_interval": "6",
			"shorts_check_interval":  "3",
		},
	}
	if _, err := d.ImportConfig(cfg, false); err != nil {
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
		Version: ConfigExportVersion,
		Bookmarks: []BookmarkExport{
			{VideoID: "sample_default_bookmark", CustomTitle: "Saved Label"},
		},
	}
	result, err := d.ImportConfig(cfg, false)
	if err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}
	if result.AddedBookmarks != 1 {
		t.Fatalf("AddedBookmarks = %d, want 1", result.AddedBookmarks)
	}

	labels, err := d.GetBookmarkLabels("")
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
		Version: ConfigExportVersion,
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
			TweetID:        "sample_stateful_like",
			SourceHandle:   "sample_source",
			AuthorHandle:   "sample_author",
			BodyText:       "liked text",
			Platform:       "twitter",
			PublishedAtMs:  1709000000000,
			CanonicalXLink: "https://x.com/sample_author/status/stateful_like",
			MediaJSON:      `[{"type":"photo"}]`,
			LikedAt:        1710000001000,
		}},
		FeedSeen: []FeedSeenExport{{
			TweetID: "sample_stateful_seen",
			SeenAt:  1710000002500,
		}},
		BookmarkedVideos: []BookmarkedVideoExport{{
			VideoID:       "sample_stateful_video",
			ChannelID:     "youtube_sample_UCstateful",
			OwnerKind:     "youtube_video",
			Title:         "Stateful Video",
			PublishedAtMs: 1708000000000,
			BookmarkedAt:  1710000003000,
		}},
	}
	if _, err := d.ImportConfig(cfg, false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	var bookmarkedAt int64
	var accountHandles, mediaIndices string
	if err := d.QueryRow(`
		SELECT bookmarked_at, COALESCE(account_handles,''), COALESCE(media_indices,'')
			FROM bookmarks WHERE video_id = 'sample_stateful_bookmark'
	`).Scan(&bookmarkedAt, &accountHandles, &mediaIndices); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if bookmarkedAt != 1710000000000 || accountHandles != "@author" || mediaIndices != "0,2" {
		t.Fatalf("bookmark state = %d %q %q", bookmarkedAt, accountHandles, mediaIndices)
	}

	var likedAt, likePublishedAt int64
	var sourceHandle, canonicalLink, mediaJSON string
	if err := d.QueryRow(`
			SELECT fl.liked_at, fi.published_at, COALESCE(fi.source_handle,''),
			       COALESCE(fi.canonical_url,''), COALESCE(fi.media_json,'')
			FROM feed_likes fl
			JOIN feed_items_resolved fi ON fi.tweet_id = fl.tweet_id
			WHERE fl.tweet_id = 'sample_stateful_like'
		`).Scan(&likedAt, &likePublishedAt, &sourceHandle, &canonicalLink, &mediaJSON); err != nil {
		t.Fatalf("read like: %v", err)
	}
	if likedAt != 1710000001000 || likePublishedAt != 1709000000000 {
		t.Fatalf("like timestamps = liked:%d published:%d", likedAt, likePublishedAt)
	}
	if sourceHandle != "sample_source" || canonicalLink == "" || mediaJSON == "" {
		t.Fatalf("like metadata = source:%q canonical:%q media:%q", sourceHandle, canonicalLink, mediaJSON)
	}

	var seenAt int64
	if err := d.QueryRow(`
		SELECT seen_at
		FROM feed_seen
			WHERE tweet_id = 'sample_stateful_seen'
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
			WHERE v.video_id = 'sample_stateful_video'
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
		INSERT INTO bookmark_categories (id, name) VALUES (7, 'Saved')
	`); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
		VALUES ('sample_existing_bookmark', 0, 0)
	`); err != nil {
		t.Fatalf("seed bookmark: %v", err)
	}

	cfg := ConfigExport{
		Version: ConfigExportVersion,
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
			ChannelID:    "youtube_sample_existing",
			OwnerKind:    "youtube_video",
			Title:        "Existing bookmark",
			CategoryName: "Saved",
			BookmarkedAt: 1710000001000,
		}},
	}
	if _, err := d.ImportConfig(cfg, false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	var categoryID, bookmarkedAt int64
	var customTitle string
	if err := d.QueryRow(`
		SELECT category_id, COALESCE(custom_title, ''), bookmarked_at
		FROM bookmarks
			WHERE video_id = 'sample_existing_bookmark'
	`).Scan(&categoryID, &customTitle, &bookmarkedAt); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if categoryID <= 0 || customTitle != "Recovered title" || bookmarkedAt != 1710000000000 {
		t.Fatalf("bookmark after import = category:%d title:%q at:%d", categoryID, customTitle, bookmarkedAt)
	}
}

func TestImportConfigRepairsExistingBookmarkedTikTokPublishDate(t *testing.T) {
	d := openWritableTestDB(t)

	const wantPublishedAt int64 = 1734000724000
	videoID := strconv.FormatInt((wantPublishedAt/1000)<<32, 10)

	if _, err := d.conn.Exec(`
			INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
			VALUES (?, ?, 'tiktok_video', 'Old title', 0, 0)
	`, videoID, "tiktok_sample"); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	cfg := ConfigExport{
		Version: ConfigExportVersion,
		BookmarkedVideos: []BookmarkedVideoExport{{
			VideoID:      videoID,
			ChannelID:    "tiktok_sample",
			OwnerKind:    "tiktok_video",
			Title:        "Restored title",
			BookmarkedAt: 1710000000000,
		}},
	}
	if _, err := d.ImportConfig(cfg, false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	var publishedAt int64
	if err := d.QueryRow(`
			SELECT published_at
			FROM videos
			WHERE video_id = ?
		`, videoID).Scan(&publishedAt); err != nil {
		t.Fatalf("read video: %v", err)
	}
	if publishedAt != wantPublishedAt {
		t.Fatalf("published_at = %d, want %d", publishedAt, wantPublishedAt)
	}
}

func TestExportFullDataCarriesStateTimestampsAndMetadata(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO bookmark_categories (id, name) VALUES (70, 'Saved')
	`); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_UCexport', 'Export Channel', 'youtube')
	`); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES ('export_video', 'youtube_UCexport', 'youtube_video', 'Export Video', 12, 1707000000000)
	`); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks
			(video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
		VALUES ('export_video', 70, 'clips', '@author', '1', 1711000000000)
	`); err != nil {
		t.Fatalf("seed bookmark: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name)
		VALUES
			('twitter_sample_source', 'twitter', 'sample_source', 'Sample Source'),
			('twitter_sample_author', 'twitter', 'sample_author', 'Sample Author');
		INSERT INTO feed_items
			(tweet_id, source_channel_id, channel_id, body_text, canonical_url,
			 published_at, media_json, fetched_at)
		VALUES
			('export_like', 'twitter_sample_source', 'twitter_sample_author', 'liked text',
			 'https://x.com/sample_author/status/export_like', 1706000000000,
			 '[{"type":"photo"}]', 1706000000000);
		INSERT INTO feed_likes (tweet_id, liked_at)
		VALUES ('export_like', 1712000000000)
	`); err != nil {
		t.Fatalf("seed like: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_seen (tweet_id, seen_at)
		VALUES ('export_seen', 1713000000000)
	`); err != nil {
		t.Fatalf("seed seen: %v", err)
	}

	cfg, err := d.ExportFullData()
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
	if lp.LikedAt != 1712000000000 || lp.UpdatedAt != 1712000000000 || lp.PublishedAtMs != 1706000000000 {
		t.Fatalf("exported like timestamps = %#v", lp)
	}
	if lp.SourceHandle != "sample_source" || lp.AuthorHandle != "sample_author" || lp.CanonicalXLink == "" || lp.MediaJSON == "" {
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

func TestImportConfigPersistsImportedRows(t *testing.T) {
	d := openWritableTestDB(t)

	cfg := ConfigExport{
		Version: ConfigExportVersion,
		Subscriptions: []ChannelExport{{
			ChannelID: "youtube_UCexampleImported",
			Name:      "Imported Channel",
			Platform:  "youtube",
		}},
		BookmarkedVideos: []BookmarkedVideoExport{{
			VideoID:     "sample_imported_video",
			ChannelID:   "youtube_UCexampleImported",
			OwnerKind:   "youtube_video",
			Title:       "Imported Video",
			Duration:    42,
			PublishedAt: "2026-05-01T12:00:00Z",
		}},
	}
	if _, err := d.ImportConfig(cfg, false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	if _, err := d.GetChannelByID("youtube_UCexampleImported"); err != nil {
		t.Fatalf("imported channel missing: %v", err)
	}
	if video, err := d.GetVideo("sample_imported_video"); err != nil || video == nil {
		t.Fatalf("imported video missing: video=%#v err=%v", video, err)
	}
}

func TestImportConfigRequiresCurrentOwnerContract(t *testing.T) {
	for _, test := range []struct {
		name      string
		version   int
		ownerKind string
	}{
		{name: "old version", version: ConfigExportVersion - 1, ownerKind: "youtube_video"},
		{name: "missing owner", version: ConfigExportVersion},
		{name: "noncanonical owner", version: ConfigExportVersion, ownerKind: " youtube_video "},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := openWritableTestDB(t)
			_, err := d.ImportConfig(ConfigExport{
				Version:  test.version,
				Settings: map[string]string{"starting_page": "feed"},
				BookmarkedVideos: []BookmarkedVideoExport{{
					VideoID:   "sample_video",
					OwnerKind: test.ownerKind,
				}},
			}, false)
			if err == nil {
				t.Fatal("ImportConfig accepted an invalid export contract")
			}
			var settings int
			if err := d.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'starting_page'`).Scan(&settings); err != nil {
				t.Fatal(err)
			}
			if settings != 0 {
				t.Fatalf("settings rows = %d, validation happened after mutation", settings)
			}
		})
	}
}
