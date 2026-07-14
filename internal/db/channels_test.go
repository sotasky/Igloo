package db

import (
	"testing"

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

func TestGetSubscribedChannelsIncludesFollowWithoutChannelRow(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name)
		VALUES ('twitter_follow_only', 'twitter', 'follow_only', 'Follow Only')
	`); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('twitter_follow_only', 1)
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

func TestNextSubscribedChannelReturnsOldestAndPlatformCount(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, last_checked, created_at)
		VALUES
			('youtube_sample_second', 'Zero', 'youtube', 0, 1),
			('youtube_sample_old', 'Old', 'youtube', 100, 1),
			('tiktok_sample_1', 'Other', 'tiktok', 0, 1);
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES
			('youtube_sample_missing', 1),
			('youtube_sample_second', 1),
			('youtube_sample_old', 1),
			('tiktok_sample_1', 1)
	`); err != nil {
		t.Fatal(err)
	}

	next, total, err := d.NextSubscribedChannel("youtube")
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.ChannelID != "youtube_sample_missing" || total != 3 || !next.IsSubscribed {
		t.Fatalf("first next = %+v total=%d", next, total)
	}
	if err := d.ExecRaw(`DELETE FROM channel_follows WHERE channel_id = 'youtube_sample_missing'`); err != nil {
		t.Fatal(err)
	}
	next, total, err = d.NextSubscribedChannel("youtube")
	if err != nil || next == nil || next.ChannelID != "youtube_sample_second" || total != 2 {
		t.Fatalf("zero next = %+v total=%d err=%v", next, total, err)
	}
	if err := d.ExecRaw(`UPDATE channels SET last_checked = 300 WHERE channel_id = 'youtube_sample_second'`); err != nil {
		t.Fatal(err)
	}
	next, total, err = d.NextSubscribedChannel("youtube")
	if err != nil || next == nil || next.ChannelID != "youtube_sample_old" || total != 2 {
		t.Fatalf("old next = %+v total=%d err=%v", next, total, err)
	}
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
	const channelID = "youtube_star_fixture"

	seedTestFollowedChannel(t, d, channelID)
	ch, err := d.GetChannel(channelID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if ch == nil {
		t.Fatal("seeded channel missing")
	}
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
	const channelID = "youtube_settings_fixture"

	seedTestFollowedChannel(t, d, channelID)
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
	updated, err := d.GetChannelSettings(channelID)
	if err != nil {
		t.Fatalf("GetChannelSettings updated: %v", err)
	}
	if updated == nil {
		t.Fatal("expected updated settings")
	}
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

func TestUnfollowKeepsVideoHistoryAndProfile(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.AddChannel(model.Channel{
		ChannelID: "tiktok_sample_reposter", SourceID: "sample_reposter", Name: "Sample Reposter", Platform: "tiktok",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := d.FollowChannel("tiktok_sample_reposter"); err != nil {
		t.Fatalf("FollowChannel: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "tiktok_sample_reposter", Platform: "tiktok", Handle: "sample_reposter", DisplayName: "Sample Reposter",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('test_short', 'tiktok_sample_reposter', 'tiktok_video', 'Sample short', 10)
	`); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	for _, asset := range []Asset{
		{AssetID: BuildAssetID("tiktok", "tiktok_video", "test_short", "video_stream", 0), AssetKind: "video_stream", OwnerKind: "tiktok_video", OwnerID: "test_short", FilePath: "videos/drop.mp4", State: AssetStateReady},
		{AssetID: BuildAssetID("tiktok", "tiktok_video", "test_short", "post_thumbnail", 0), AssetKind: "post_thumbnail", OwnerKind: "tiktok_video", OwnerID: "test_short", FilePath: "thumbs/drop.jpg", State: AssetStateReady},
		{AssetID: BuildAssetID("tiktok", "tiktok_video", "test_short", "post_media", 0), AssetKind: "post_media", OwnerKind: "tiktok_video", OwnerID: "test_short", FilePath: "slides/drop_0.jpg", State: AssetStateReady},
		{AssetID: BuildAssetID("tiktok", "channel", "tiktok_sample_reposter", "avatar", 0), AssetKind: "avatar", OwnerKind: "channel", OwnerID: "tiktok_sample_reposter", FilePath: "avatars/sample_reposter.jpg", State: AssetStateReady},
	} {
		storeReadyAssetForTest(t, d, asset, 1)
	}
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: "tiktok_sample_reposter", Component: "direct",
		Items: []VideoDesire{{
			VideoID: "test_short", OwnerChannelID: "tiktok_sample_reposter",
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatalf("ReconcileVideoDesires: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES ('test_short', 'tiktok_sample_reposter', 10, 10, 10)
	`); err != nil {
		t.Fatalf("seed source provenance: %v", err)
	}

	if err := d.UnfollowChannel("tiktok_sample_reposter"); err != nil {
		t.Fatalf("UnfollowChannel: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM video_repost_sources WHERE reposter_channel_id='tiktok_sample_reposter'`).Scan(&count); err != nil {
		t.Fatalf("count source provenance: %v", err)
	}
	if count != 1 {
		t.Fatalf("unfollow changed source provenance before maintenance: %d", count)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM videos WHERE video_id='test_short'`).Scan(&count); err != nil {
		t.Fatalf("count video: %v", err)
	}
	if count != 1 {
		t.Fatalf("video was removed before maintenance")
	}
	collected, err := d.MaintainVideoRetention(100)
	if err != nil {
		t.Fatalf("MaintainVideoRetention: %v", err)
	}
	if collected != 0 {
		t.Fatalf("collected = %d, want 0", collected)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM videos WHERE video_id='test_short'`).Scan(&count); err != nil {
		t.Fatalf("count maintained video: %v", err)
	}
	if count != 1 {
		t.Fatalf("historical video was removed by maintenance")
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM video_repost_sources WHERE reposter_channel_id='tiktok_sample_reposter'`).Scan(&count); err != nil {
		t.Fatalf("count maintained source provenance: %v", err)
	}
	if count != 1 {
		t.Fatalf("historical source provenance was removed: %d", count)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM video_desires WHERE source_channel_id='tiktok_sample_reposter' AND video_id='test_short'`).Scan(&count); err != nil {
		t.Fatalf("count historical desire: %v", err)
	}
	if count != 1 {
		t.Fatalf("historical desire was removed: %d", count)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM channels WHERE channel_id='tiktok_sample_reposter'`).Scan(&count); err != nil {
		t.Fatalf("count channel: %v", err)
	}
	if count != 1 {
		t.Fatalf("channel row was pruned on unfollow, count=%d", count)
	}
	if p, _ := d.GetChannelProfile("tiktok_sample_reposter"); p == nil {
		t.Fatalf("profile row should survive unfollow")
	}
	if avatar, err := d.GetAssetByOwnerIdentity("avatar", "channel", "tiktok_sample_reposter", 0); err != nil || avatar == nil {
		t.Fatalf("profile avatar should survive unfollow: %+v / %v", avatar, err)
	}
	if media, err := d.GetAssetByOwnerIdentity("video_stream", "tiktok_video", "test_short", 0); err != nil || media == nil {
		t.Fatalf("video media should survive unfollow: %+v / %v", media, err)
	}
}

func TestUnfollowDoesNotDeleteHistoricalFeedContent(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform)
		VALUES ('twitter_drop_fail', 'Drop Fail', 'twitter')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('twitter_drop_fail', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, published_at, fetched_at)
		VALUES ('tw_drop_fail', 'twitter_drop_fail', 'twitter_drop_fail', 1, 1)
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

	if err := d.UnfollowChannel("twitter_drop_fail"); err != nil {
		t.Fatalf("UnfollowChannel: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM channel_follows WHERE channel_id='twitter_drop_fail'`).Scan(&count); err != nil {
		t.Fatalf("count follow: %v", err)
	}
	if count != 0 {
		t.Fatalf("follow remained after unfollow, count=%d", count)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id='tw_drop_fail'`).Scan(&count); err != nil {
		t.Fatalf("count feed item: %v", err)
	}
	if count != 1 {
		t.Fatalf("unfollow deleted stored feed history, count=%d", count)
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
		ChannelID: "twitter_sample_channel", SourceID: "sample_channel", Name: "Sample Channel",
		URL: "https://x.com/sample_channel", Platform: "twitter",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	// Layer 1 — hardcoded default. No global, no channel override.
	// GetChannelSettings falls back to "1" (true) for include_reposts.
	s, err := d.GetChannelSettings("twitter_sample_channel")
	if err != nil || s == nil {
		t.Fatalf("GetChannelSettings initial: %v / %+v", err, s)
	}
	if !s.IncludeReposts {
		t.Fatalf("default include_reposts = false, want true (hardcoded fallback)")
	}

	// Layer 2 — global overrides default. Channel still has no override.
	if err := d.SetSetting("include_reposts_default", "0"); err != nil {
		t.Fatalf("SetSetting include_reposts_default=0: %v", err)
	}
	s, _ = d.GetChannelSettings("twitter_sample_channel")
	if s.IncludeReposts {
		t.Fatalf("after global=false, include_reposts = true, want false (global should win)")
	}

	// Layer 3 — per-channel override wins over global.
	if err := d.UpdateChannelSettings("twitter_sample_channel", map[string]any{
		"include_reposts": 1,
	}); err != nil {
		t.Fatalf("UpdateChannelSettings include_reposts=1: %v", err)
	}
	s, _ = d.GetChannelSettings("twitter_sample_channel")
	if !s.IncludeReposts {
		t.Fatalf("channel=true + global=false → include_reposts = false, want true (channel should win)")
	}
	var overrideUpdatedAt int64
	if err := d.QueryRow(`SELECT updated_at FROM channel_settings WHERE channel_id = 'twitter_sample_channel'`).Scan(&overrideUpdatedAt); err != nil {
		t.Fatal(err)
	}

	// Clearing per-channel overrides falls back to the global row again.
	if err := d.ClearChannelSettings("twitter_sample_channel"); err != nil {
		t.Fatalf("ClearChannelSettings: %v", err)
	}
	s, _ = d.GetChannelSettings("twitter_sample_channel")
	if s.IncludeReposts {
		t.Fatalf("after clear + global=false, include_reposts = true, want false (should fall back to global)")
	}
	var allNull int
	var clearedAt int64
	if err := d.QueryRow(`
		SELECT max_videos IS NULL AND download_subtitles IS NULL
		   AND media_only IS NULL AND media_download_limit IS NULL
		   AND include_reposts IS NULL,
		       updated_at
		FROM channel_settings WHERE channel_id = 'twitter_sample_channel'
	`).Scan(&allNull, &clearedAt); err != nil {
		t.Fatal(err)
	}
	if allNull != 1 || clearedAt <= overrideUpdatedAt {
		t.Fatalf("clear tombstone = null:%d at:%d, prior:%d", allNull, clearedAt, overrideUpdatedAt)
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
	if err := d.SetSetting("youtube_max_videos", "42"); err != nil {
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

	if err := d.SetSetting("youtube_max_videos", "5"); err != nil {
		t.Fatalf("SetSetting youtube_max_videos=5: %v", err)
	}
	if err := d.SetSetting("instagram_max_videos", "20"); err != nil {
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
