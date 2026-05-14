package db

import (
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestGetSubscribedChannels(t *testing.T) {
	d := openTestDB(t)
	channels, err := d.GetSubscribedChannels()
	if err != nil {
		t.Fatalf("GetSubscribedChannels: %v", err)
	}
	if len(channels) == 0 {
		t.Skip("no subscribed channels in test DB")
	}
	ch := channels[0]
	if ch.ChannelID == "" {
		t.Error("channel_id is empty")
	}
	if ch.Name == "" {
		t.Error("name is empty")
	}
	if ch.Platform == "" {
		t.Error("platform is empty")
	}
}

// TestGetSubscribedChannelsDedupesMultiUserFollows pins the contract that
// GetSubscribedChannels returns one row per channel even when the table
// holds legacy non-canonical rows. Single-user mode writes user_id=”
// everywhere; this test seeds an extra row under a real username (the
// pre-canonicalize state) and asserts the channel still appears once.
func TestGetSubscribedChannelsDedupesMultiUserFollows(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.AddChannel(model.Channel{
		ChannelID: "tiktok_dupseed", SourceID: "dupseed", Name: "DupSeed",
		URL: "https://www.tiktok.com/@dupseed", Platform: "tiktok",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	// FollowChannel creates the user_id='' row. Second insert mimics what
	// the Android mutation path does (user_id=<username>) for the same
	// channel — both rows are valid under the (user_id, channel_id) PK.
	if err := d.FollowChannel("tiktok_dupseed"); err != nil {
		t.Fatalf("FollowChannel: %v", err)
	}
	if _, err := d.conn.Exec(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES (?, ?, ?)`,
		"admin", "tiktok_dupseed", 1,
	); err != nil {
		t.Fatalf("seed second follow row: %v", err)
	}

	channels, err := d.GetSubscribedChannels()
	if err != nil {
		t.Fatalf("GetSubscribedChannels: %v", err)
	}
	var hits int
	for _, ch := range channels {
		if ch.ChannelID == "tiktok_dupseed" {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("tiktok_dupseed appeared %d times, want exactly 1", hits)
	}
}

func TestGetSubscribedChannelsIncludesFollowWithoutChannelRow(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name)
		VALUES ('twitter_follow_only', 'twitter', 'follow_only', 'Follow Only')
	`); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'twitter_follow_only', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}

	channels, err := d.GetSubscribedChannels()
	if err != nil {
		t.Fatalf("GetSubscribedChannels: %v", err)
	}
	for _, ch := range channels {
		if ch.ChannelID != "twitter_follow_only" {
			continue
		}
		if !ch.IsSubscribed || ch.Platform != "twitter" || ch.URL != "https://x.com/follow_only" {
			t.Fatalf("unexpected channel defaults: %+v", ch)
		}
		return
	}
	t.Fatalf("twitter_follow_only missing from subscribed channels")
}

func TestGetAllVideoCountsByChannel(t *testing.T) {
	d := openTestDB(t)
	counts, err := d.GetAllVideoCountsByChannel()
	if err != nil {
		t.Fatalf("GetAllVideoCountsByChannel: %v", err)
	}
	if counts == nil {
		t.Error("counts is nil, expected empty map")
	}
}

func TestToggleChannelStar(t *testing.T) {
	d := openWritableTestDB(t)
	var channelID string
	_ = d.conn.QueryRow("SELECT channel_id FROM channel_follows LIMIT 1").Scan(&channelID)
	if channelID == "" {
		t.Skip("no subscribed channels")
	}
	ch, _ := d.GetChannel(channelID)
	oldStarred := ch.IsStarred
	newStarred, err := d.ToggleChannelStar(channelID)
	if err != nil {
		t.Fatalf("ToggleChannelStar: %v", err)
	}
	if newStarred == oldStarred {
		t.Error("expected star state to toggle")
	}
	restored, err := d.ToggleChannelStar(channelID)
	if err != nil {
		t.Fatalf("ToggleChannelStar restore: %v", err)
	}
	if restored != oldStarred {
		t.Error("expected star state to restore")
	}
}

func TestGetAndUpdateChannelSettings(t *testing.T) {
	d := openWritableTestDB(t)
	var channelID string
	_ = d.conn.QueryRow("SELECT channel_id FROM channel_follows LIMIT 1").Scan(&channelID)
	if channelID == "" {
		t.Skip("no subscribed channels")
	}
	settings, err := d.GetChannelSettings(channelID)
	if err != nil {
		t.Fatalf("GetChannelSettings: %v", err)
	}
	if settings == nil {
		t.Fatal("expected non-nil settings")
	}
	err = d.UpdateChannelSettings(channelID, map[string]any{"quality": "720p"})
	if err != nil {
		t.Fatalf("UpdateChannelSettings: %v", err)
	}
	updated, _ := d.GetChannelSettings(channelID)
	if updated.Quality != "720p" {
		t.Errorf("expected quality 720p, got %q", updated.Quality)
	}
}

func TestResolveSubscribeURL(t *testing.T) {
	d := openWritableTestDB(t)

	if _, err := d.conn.Exec(
		`INSERT INTO channels (channel_id, name, url, platform) VALUES (?, ?, ?, ?)`,
		"twitter_alice", "Alice", "https://x.com/alice", "twitter",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := d.ResolveSubscribeURL("twitter_alice"); got != "https://x.com/alice" {
		t.Errorf("stored url: got %q, want https://x.com/alice", got)
	}

	cases := map[string]string{
		"twitter_bob":      "https://x.com/bob",
		"tiktok_carol":     "https://tiktok.com/@carol",
		"instagram_dave":   "https://instagram.com/dave",
		"youtube_UCabc123": "https://youtube.com/channel/UCabc123",
	}
	for channelID, want := range cases {
		if got := d.ResolveSubscribeURL(channelID); got != want {
			t.Errorf("%s: got %q, want %q", channelID, got, want)
		}
	}

	if got := d.ResolveSubscribeURL("weirdprefix_x"); got != "" {
		t.Errorf("unknown prefix: got %q, want empty", got)
	}
	if got := d.ResolveSubscribeURL(""); got != "" {
		t.Errorf("empty id: got %q, want empty", got)
	}
}

func TestDeleteTwitterChannelKeepsProfileAndBanner(t *testing.T) {
	// Profile + banner survive channel delete so the channel page still
	// renders the hero for non-subscribed handles, and re-following within
	// the cache TTL doesn't force a refetch.
	d := openWritableTestDB(t)
	_ = d.AddChannel(model.Channel{
		ChannelID: "twitter_zeta", Name: "Zeta", Platform: "twitter", URL: "https://x.com/zeta",
	})
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_zeta", Platform: "twitter", Handle: "zeta", DisplayName: "Zeta",
	})
	_ = d.InsertMediaFile(model.MediaFile{
		OwnerType: "banner", OwnerID: "twitter_zeta",
		FilePath: "thumbnails/banners/twitter_zeta.jpg", MediaType: "banner",
	})

	if err := d.DeleteChannel("twitter_zeta"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if p, _ := d.GetChannelProfile("twitter_zeta"); p == nil {
		t.Fatalf("profile row should be retained as cache after channel delete")
	}
	if mfPath, _ := d.GetMediaFilePath("banner", "twitter_zeta", 0); mfPath == "" {
		t.Fatalf("banner media_file should be retained after channel delete")
	}
}

func TestPurgeUnfollowedVideoChannelRetainsMetadataWhenNoBookmarkSurvives(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.AddChannel(model.Channel{
		ChannelID: "tiktok_drop", SourceID: "drop", Name: "Drop", Platform: "tiktok",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := d.FollowChannel("tiktok_drop"); err != nil {
		t.Fatalf("FollowChannel: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "tiktok_drop", Platform: "tiktok", Handle: "drop", DisplayName: "Drop",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO videos (video_id, channel_id, title, file_path, thumbnail_path, published_at, sync_seq)
		VALUES ('drop_short', 'tiktok_drop', 'Drop short', 'videos/drop.mp4', 'thumbs/drop.jpg', 10, 1)
	`); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if err := d.InsertMediaFile(model.MediaFile{
		OwnerType: "feed_media", OwnerID: "drop_short", MediaIndex: 0,
		FilePath: "slides/drop_0.jpg", MediaType: "photo",
	}); err != nil {
		t.Fatalf("InsertMediaFile slide: %v", err)
	}
	if err := d.InsertMediaFile(model.MediaFile{
		OwnerType: "avatar", OwnerID: "tiktok_drop", MediaIndex: 0,
		FilePath: "avatars/drop.jpg", MediaType: "photo",
	}); err != nil {
		t.Fatalf("InsertMediaFile avatar: %v", err)
	}

	deleted, err := d.PurgeUnfollowedChannelContent("tiktok_drop", "alice")
	if err != nil {
		t.Fatalf("PurgeUnfollowedChannelContent: %v", err)
	}
	deletedPaths := map[string]bool{}
	for _, v := range deleted {
		deletedPaths[v.FilePath] = true
		deletedPaths[v.ThumbnailPath] = true
	}
	for _, path := range []string{"videos/drop.mp4", "thumbs/drop.jpg", "slides/drop_0.jpg", "avatars/drop.jpg"} {
		if !deletedPaths[path] {
			t.Fatalf("deleted paths missing %q: %+v", path, deletedPaths)
		}
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM videos WHERE video_id='drop_short'`).Scan(&count); err != nil {
		t.Fatalf("count video: %v", err)
	}
	if count != 0 {
		t.Fatalf("video row survived purge")
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM channels WHERE channel_id='tiktok_drop'`).Scan(&count); err != nil {
		t.Fatalf("count channel: %v", err)
	}
	if count != 1 {
		t.Fatalf("channel row was pruned on unfollow, count=%d", count)
	}
	if p, _ := d.GetChannelProfile("tiktok_drop"); p == nil {
		t.Fatalf("profile row should survive unfollow purge")
	}
}

func TestPruneStaleOrphanChannelMetadataDropsOnlyOldUnreferencedRows(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	old := now.Add(-31 * 24 * time.Hour)
	fresh := now.Add(-12 * time.Hour)

	for _, ch := range []model.Channel{
		{ChannelID: "tiktok_sample_old_orphan", SourceID: "sample_old_orphan", Name: "Old Orphan", Platform: "tiktok"},
		{ChannelID: "tiktok_sample_new_orphan", SourceID: "sample_new_orphan", Name: "New Orphan", Platform: "tiktok"},
		{ChannelID: "tiktok_sample_followed_old", SourceID: "sample_followed_old", Name: "Followed Old", Platform: "tiktok"},
		{ChannelID: "tiktok_sample_video_old", SourceID: "sample_video_old", Name: "Video Old", Platform: "tiktok"},
	} {
		if err := d.AddChannel(ch); err != nil {
			t.Fatalf("AddChannel %s: %v", ch.ChannelID, err)
		}
	}
	if err := d.FollowChannel("tiktok_sample_followed_old"); err != nil {
		t.Fatalf("FollowChannel: %v", err)
	}
	if err := d.FollowChannel("tiktok_sample_profile_followed_old"); err != nil {
		t.Fatalf("FollowChannel profile-only: %v", err)
	}
	if err := d.InsertVideoWithSourceKind("sample_protected_video", "tiktok_sample_video_old", "Protected", "", 0, "", "", 0, old.UnixMilli(), "", "video", 0, false, ""); err != nil {
		t.Fatalf("InsertVideoWithSourceKind: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, author_handle, source_handle, published_at, fetched_at)
		VALUES ('sample_profile_ref_tweet', 'sample_profile_ref', 'sample_profile_ref', 1, 1)
	`); err != nil {
		t.Fatalf("insert referenced feed item: %v", err)
	}
	for channelID, fetchedAt := range map[string]time.Time{
		"tiktok_sample_old_orphan":           old,
		"tiktok_sample_new_orphan":           fresh,
		"tiktok_sample_followed_old":         old,
		"tiktok_sample_video_old":            old,
		"tiktok_sample_profile_only_old":     old,
		"tiktok_sample_profile_followed_old": old,
		"twitter_sample_profile_ref":         old,
	} {
		platform := "tiktok"
		if strings.HasPrefix(channelID, "twitter_") {
			platform = "twitter"
		}
		if err := d.UpsertChannelProfile(model.ChannelProfile{
			ChannelID:   channelID,
			Platform:    platform,
			Handle:      strings.TrimPrefix(strings.TrimPrefix(channelID, "tiktok_"), "twitter_"),
			DisplayName: channelID,
			AvatarURL:   "https://example.invalid/avatar.jpg",
			FetchedAt:   &fetchedAt,
		}); err != nil {
			t.Fatalf("UpsertChannelProfile %s: %v", channelID, err)
		}
	}
	if err := d.InsertMediaFile(model.MediaFile{
		OwnerType: "avatar",
		OwnerID:   "tiktok_sample_old_orphan",
		FilePath:  "avatars/tiktok_sample_old_orphan.jpg",
		MediaType: "photo",
	}); err != nil {
		t.Fatalf("InsertMediaFile: %v", err)
	}
	if err := d.InsertMediaFile(model.MediaFile{
		OwnerType: "avatar",
		OwnerID:   "tiktok_sample_profile_only_old",
		FilePath:  "avatars/tiktok_sample_profile_only_old.jpg",
		MediaType: "photo",
	}); err != nil {
		t.Fatalf("InsertMediaFile profile-only: %v", err)
	}

	pruned, err := d.PruneStaleOrphanChannelMetadata(now.Add(-30*24*time.Hour).UnixMilli(), 100)
	if err != nil {
		t.Fatalf("PruneStaleOrphanChannelMetadata: %v", err)
	}
	if pruned.Channels != 1 || pruned.ChannelProfiles != 2 || pruned.MediaFiles != 2 {
		t.Fatalf("pruned = %+v, want one old channel plus two old profiles/media files", pruned)
	}
	for _, channelID := range []string{"tiktok_sample_new_orphan", "tiktok_sample_followed_old", "tiktok_sample_video_old", "tiktok_sample_profile_followed_old", "twitter_sample_profile_ref"} {
		if p, _ := d.GetChannelProfile(channelID); p == nil {
			t.Fatalf("%s profile should survive stale orphan prune", channelID)
		}
	}
	for _, channelID := range []string{"tiktok_sample_old_orphan", "tiktok_sample_profile_only_old"} {
		if p, _ := d.GetChannelProfile(channelID); p != nil {
			t.Fatalf("%s profile survived stale orphan prune", channelID)
		}
	}
}

func TestPurgeUnfollowedChannelContentKeepsFollowClearedWhenPurgeFails(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform)
		VALUES ('twitter_drop_fail', 'Drop Fail', 'twitter')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'twitter_drop_fail', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, author_handle, source_handle, published_at, fetched_at)
		VALUES ('tw_drop_fail', 'drop_fail', 'drop_fail', 1, 1)
	`); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		CREATE TRIGGER fail_drop_fail_delete
		BEFORE DELETE ON feed_items
		WHEN OLD.tweet_id = 'tw_drop_fail'
		BEGIN
			SELECT RAISE(ABORT, 'stop purge');
		END;
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if _, err := d.PurgeUnfollowedChannelContent("twitter_drop_fail", "alice"); err == nil {
		t.Fatal("expected purge failure")
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM channel_follows WHERE channel_id='twitter_drop_fail'`).Scan(&count); err != nil {
		t.Fatalf("count follow: %v", err)
	}
	if count != 0 {
		t.Fatalf("follow row should stay cleared after purge failure, count=%d", count)
	}
}

// TestChannelSettingsOverrideChain walks the full per-channel > global > default
// resolution contract, using include_reposts — the field most
// likely to silently flip behaviour when the chain regresses. NULL in
// channel_settings means "inherit"; clearing overrides must fall back to the
// global row, and the global row falling back must land on the hardcoded
// default. If any one of those layers stops winning, reposts sneak into (or
// out of) a user's feed without their consent.
func TestChannelSettingsOverrideChain(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.AddChannel(model.Channel{
		ChannelID: "twitter_chain", SourceID: "chain", Name: "Chain",
		URL: "https://x.com/chain", Platform: "twitter",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	// Layer 1 — hardcoded default. No global, no channel override.
	// GetChannelSettings falls back to "1" (true) for include_reposts.
	s, err := d.GetChannelSettings("twitter_chain")
	if err != nil || s == nil {
		t.Fatalf("GetChannelSettings initial: %v / %+v", err, s)
	}
	if !s.IncludeReposts {
		t.Fatalf("default include_reposts = false, want true (hardcoded fallback)")
	}

	// Layer 2 — global overrides default. Channel still has no override.
	if err := d.SetSetting("", "include_reposts_default", "0"); err != nil {
		t.Fatalf("SetSetting include_reposts_default=0: %v", err)
	}
	s, _ = d.GetChannelSettings("twitter_chain")
	if s.IncludeReposts {
		t.Fatalf("after global=false, include_reposts = true, want false (global should win)")
	}

	// Layer 3 — per-channel override wins over global.
	if err := d.UpdateChannelSettings("twitter_chain", map[string]any{
		"include_reposts": 1,
	}); err != nil {
		t.Fatalf("UpdateChannelSettings include_reposts=1: %v", err)
	}
	s, _ = d.GetChannelSettings("twitter_chain")
	if !s.IncludeReposts {
		t.Fatalf("channel=true + global=false → include_reposts = false, want true (channel should win)")
	}

	// Clearing per-channel overrides falls back to the global row again.
	if err := d.ClearChannelSettings("twitter_chain"); err != nil {
		t.Fatalf("ClearChannelSettings: %v", err)
	}
	s, _ = d.GetChannelSettings("twitter_chain")
	if s.IncludeReposts {
		t.Fatalf("after clear + global=false, include_reposts = true, want false (should fall back to global)")
	}
}

// TestChannelSettingsMaxVideosZeroIsInheritSentinel covers the specific quirk
// that for max_videos the inherit sentinel is 0, not NULL. Other
// fields use NULL. If the "> 0" check in GetChannelSettings is loosened to
// "IS NOT NULL", a channel with max_videos=0 silently caps ingest at zero
// videos and the user's feed goes empty with no error.
func TestChannelSettingsMaxVideosZeroIsInheritSentinel(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.AddChannel(model.Channel{
		ChannelID: "twitter_user_settings", SourceID: "user_settings", Name: "User Settings",
		URL: "https://x.com/user_settings", Platform: "twitter",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	// Pin the global so we have a known non-default answer. twitter/youtube
	// channels resolve against youtube_max_videos; TikTok and Instagram branch
	// to their own globals — see GetChannelSettings.
	if err := d.SetSetting("", "youtube_max_videos", "42"); err != nil {
		t.Fatalf("SetSetting youtube_max_videos=42: %v", err)
	}

	if err := d.UpdateChannelSettings("twitter_user_settings", map[string]any{
		"max_videos": 0,
	}); err != nil {
		t.Fatalf("UpdateChannelSettings max_videos=0: %v", err)
	}

	s, err := d.GetChannelSettings("twitter_user_settings")
	if err != nil || s == nil {
		t.Fatalf("GetChannelSettings: %v / %+v", err, s)
	}
	if s.MaxVideos != 42 {
		t.Fatalf("max_videos = %d, want 42 (0 should inherit the global, not cap at zero)", s.MaxVideos)
	}

	// Non-zero channel value wins over the global.
	if err := d.UpdateChannelSettings("twitter_user_settings", map[string]any{
		"max_videos": 7,
	}); err != nil {
		t.Fatalf("UpdateChannelSettings max_videos=7: %v", err)
	}
	s, _ = d.GetChannelSettings("twitter_user_settings")
	if s.MaxVideos != 7 {
		t.Fatalf("max_videos = %d, want 7 (per-channel > global)", s.MaxVideos)
	}
}

func TestChannelSettingsInstagramUsesInstagramGlobals(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.AddChannel(model.Channel{
		ChannelID: "instagram_user_settings", SourceID: "user_settings", Name: "User Settings",
		URL: "https://instagram.com/user_settings", Platform: "instagram",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	if err := d.SetSetting("", "youtube_max_videos", "5"); err != nil {
		t.Fatalf("SetSetting youtube_max_videos=5: %v", err)
	}
	if err := d.SetSetting("", "instagram_max_videos", "20"); err != nil {
		t.Fatalf("SetSetting instagram_max_videos=20: %v", err)
	}

	s, err := d.GetChannelSettings("instagram_user_settings")
	if err != nil || s == nil {
		t.Fatalf("GetChannelSettings: %v / %+v", err, s)
	}
	if s.MaxVideos != 20 {
		t.Fatalf("instagram max_videos = %d, want 20", s.MaxVideos)
	}

	if err := d.UpdateChannelSettings("instagram_user_settings", map[string]any{
		"max_videos": 8,
	}); err != nil {
		t.Fatalf("UpdateChannelSettings max_videos=8: %v", err)
	}
	s, _ = d.GetChannelSettings("instagram_user_settings")
	if s.MaxVideos != 8 {
		t.Fatalf("instagram max_videos = %d, want 8 (per-channel override)", s.MaxVideos)
	}
}
