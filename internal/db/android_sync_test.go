package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSchemaLedgerRunsLegacyAndroidSyncCleanupOnce(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw("DELETE FROM schema_migrations WHERE name = ?", "legacy_android_v3_generation_cleanup"); err != nil {
		t.Fatalf("reset migration ledger: %v", err)
	}

	insertAndroidSyncGenerationFixture(t, d, "android-v3-old", 1)
	insertAndroidSyncGenerationFixture(t, d, "android-sync-new", 2)

	if err := EnsureSchema(d.conn); err != nil {
		t.Fatalf("EnsureSchema first run: %v", err)
	}

	for table, want := range map[string]int{
		"android_sync_generations":    1,
		"android_sync_items":          1,
		"android_sync_assets":         1,
		"android_sync_health_reports": 1,
	} {
		var got int
		if err := d.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
			t.Fatalf("count %s after first run: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s count after first run = %d, want %d", table, got, want)
		}
	}
	assertSchemaMigrationRecorded(t, d, "legacy_android_v3_generation_cleanup")

	if err := EnsureSchema(d.conn); err != nil {
		t.Fatalf("EnsureSchema second run: %v", err)
	}
	assertSchemaMigrationRecorded(t, d, "legacy_android_v3_generation_cleanup")

	insertAndroidSyncGenerationFixture(t, d, "android-v3-reintroduced", 3)
	if err := EnsureSchema(d.conn); err != nil {
		t.Fatalf("EnsureSchema after reintroduced legacy rows: %v", err)
	}

	for table, want := range map[string]int{
		"android_sync_generations":    2,
		"android_sync_items":          2,
		"android_sync_assets":         2,
		"android_sync_health_reports": 2,
	} {
		var got int
		if err := d.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
			t.Fatalf("count %s after reintroduce: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s count after reintroduce = %d, want %d", table, got, want)
		}
	}
	if gen, err := d.GetLatestAndroidSyncGeneration(); err != nil {
		t.Fatalf("latest generation: %v", err)
	} else if gen == nil || gen.GenerationID != "android-v3-reintroduced" {
		t.Fatalf("latest generation = %+v, want android-v3-reintroduced", gen)
	}
}

func insertAndroidSyncGenerationFixture(t *testing.T, d *DB, generationID string, createdAtMs int64) {
	t.Helper()
	if err := d.ExecRaw(`
			INSERT INTO android_sync_generations (
				generation_id, created_at_ms, status, source_version, retention_json,
				item_count, asset_count, ready_asset_count, server_missing_asset_count,
				total_bytes, content_counts_json, asset_counts_json
			) VALUES (?, ?, 'ready', ?, '{}', 1, 1, 1, 0, 1, '{}', '{}')
		`, generationID, createdAtMs, generationID+"-source"); err != nil {
		t.Fatalf("insert generation %s: %v", generationID, err)
	}
	if err := d.ExecRaw(`
			INSERT INTO android_sync_items (generation_id, seq, item_kind, item_id, payload_json)
			VALUES (?, 1, 'videos', ?, '{}')
		`, generationID, generationID+"-video"); err != nil {
		t.Fatalf("insert item %s: %v", generationID, err)
	}
	if err := d.ExecRaw(`
			INSERT INTO android_sync_assets (
				generation_id, seq, asset_id, asset_kind, owner_id, owner_kind,
				bucket, server_url, content_type, size_bytes, sha256, state,
				required_reason, effective_recency_ms
			) VALUES (?, 1, ?, 'video_stream', ?, 'video', 'videos', '/asset', 'video/mp4', 1, 'sha', 'ready', 'retention', 1)
		`, generationID, generationID+"-asset", generationID+"-video"); err != nil {
		t.Fatalf("insert asset %s: %v", generationID, err)
	}
	if err := d.ExecRaw(`
			INSERT INTO android_sync_health_reports (
				generation_id, reported_at_ms, payload_json, verified_assets,
				pending_assets, failed_assets, missing_assets, total_assets, verified_bytes
			) VALUES (?, 1, '{}', 1, 0, 0, 0, 1, 1)
		`, generationID); err != nil {
		t.Fatalf("insert health %s: %v", generationID, err)
	}
}

func assertSchemaMigrationRecorded(t *testing.T, d *DB, name string) {
	t.Helper()
	var got int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&got); err != nil {
		t.Fatalf("lookup migration %s: %v", name, err)
	}
	if got != 1 {
		t.Fatalf("migration %s row count = %d, want 1", name, got)
	}
}

func TestAndroidSyncSourceVersionChangesWhenMediaFileAppears(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}
	relPath := filepath.Join("media", "twitter", "sample", "tweet_001_0.mp4")

	if err := d.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type)
		VALUES ('feed_media', 'tweet_001', 0, ?, 'video')
	`, relPath); err != nil {
		t.Fatalf("insert media file row: %v", err)
	}

	before, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version before file exists: %v", err)
	}

	absPath := filepath.Join(d.dataDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	if err := os.WriteFile(absPath, []byte("fake-video-bytes"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	after, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after file exists: %v", err)
	}
	if before == after {
		t.Fatalf("source version did not change after media file became serveable: %s", before)
	}
}

func TestAndroidSyncSourceVersionChangesWhenUserStateRowsChange(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}

	current, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version before user state: %v", err)
	}

	for _, step := range []struct {
		name string
		stmt string
		args []any
	}{
		{
			name: "channel star",
			stmt: `INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', 'youtube_starred', 1001)`,
		},
		{
			name: "muted account",
			stmt: `INSERT INTO muted_accounts (handle, muted_at) VALUES ('muted_handle', 1002)`,
		},
		{
			name: "moment view",
			stmt: `INSERT INTO moment_views (username, video_id, viewed_at) VALUES ('alice', 'short_001', 1003)`,
		},
		{
			name: "watch history",
			stmt: `INSERT INTO watch_history (user_id, video_id, playback_position, duration, progress_updated_at_ms, progress_source, last_watched)
			       VALUES ('alice', 'video_001', 12.5, 100.0, 1004, 'test', 1004)`,
		},
	} {
		if err := d.ExecRaw(step.stmt, step.args...); err != nil {
			t.Fatalf("insert %s: %v", step.name, err)
		}

		next, err := d.AndroidSyncSourceVersion("alice", settings)
		if err != nil {
			t.Fatalf("source version after %s: %v", step.name, err)
		}
		if current == next {
			t.Fatalf("source version did not change after %s: %s", step.name, current)
		}
		current = next
	}
}

func TestAndroidSyncSourceVersionIsUsernameScoped(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}

	alice, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("alice source version: %v", err)
	}
	bob, err := d.AndroidSyncSourceVersion("bob", settings)
	if err != nil {
		t.Fatalf("bob source version: %v", err)
	}
	if alice == bob {
		t.Fatalf("source version should differ across usernames: %s", alice)
	}

	aliceAgain, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("alice source version again: %v", err)
	}
	if aliceAgain != alice {
		t.Fatalf("source version for same username changed: before=%s after=%s", alice, aliceAgain)
	}
}

func TestAndroidSyncSourceVersionChangesWhenBookmarkMetadataChanges(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}

	current, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version before bookmark metadata: %v", err)
	}

	catID, err := d.CreateBookmarkCategory("alice", "Archive", "/tmp/archive")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	next, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after category create: %v", err)
	}
	if current == next {
		t.Fatalf("source version did not change after category create: %s", current)
	}
	current = next

	if err := d.UpdateBookmarkCategory("alice", catID, "Updated", "/tmp/updated"); err != nil {
		t.Fatalf("update category: %v", err)
	}
	next, err = d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after category update: %v", err)
	}
	if current == next {
		t.Fatalf("source version did not change after category update: %s", current)
	}
	current = next

	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, bookmarked_at)
		VALUES ('alice', 'bookmarked', ?, 'label', 1000)
	`, catID); err != nil {
		t.Fatalf("insert bookmark label: %v", err)
	}
	next, err = d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after bookmark label: %v", err)
	}
	if current == next {
		t.Fatalf("source version did not change after bookmark label: %s", current)
	}
	current = next

	if err := d.ClearBookmarkLabel("alice", "label"); err != nil {
		t.Fatalf("clear bookmark label: %v", err)
	}
	next, err = d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after label clear: %v", err)
	}
	if current == next {
		t.Fatalf("source version did not change after label clear: %s", current)
	}
}

func TestAndroidSyncSourceVersionChangesWhenProfileMetadataChanges(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}

	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name, followers)
		VALUES ('youtube_UCprofileonly', 'youtube', 'profileonly', 'Profile Only', 1200)
	`); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	before, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version before profile update: %v", err)
	}
	if err := d.ExecRaw(`
		UPDATE channel_profiles
		SET display_name = 'Profile Renamed', followers = 1500
		WHERE channel_id = 'youtube_UCprofileonly'
	`); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	after, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after profile update: %v", err)
	}
	if before == after {
		t.Fatalf("source version did not change after profile metadata changed: %s", before)
	}
}

func TestAndroidSyncSourceVersionChangesWhenFollowStateChanges(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq)
		VALUES ('tiktok_followed', 'Followed', 'tiktok', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'tiktok_followed', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, published_at, sync_seq)
		VALUES ('recent_followed_short', 'tiktok_followed', 'Recent followed short', 1000, 1)
	`); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	before, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version before unfollow: %v", err)
	}
	if err := d.ExecRaw(`DELETE FROM channel_follows WHERE channel_id = 'tiktok_followed'`); err != nil {
		t.Fatalf("delete follow: %v", err)
	}
	after, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after unfollow: %v", err)
	}
	if before == after {
		t.Fatalf("source version did not change after follow state changed: %s", before)
	}
}

func TestAndroidSyncSourceVersionChangesWhenRankSnapshotOrSeenStateChanges(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}

	before, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version before rank: %v", err)
	}
	if err := d.ReplaceFeedRankSnapshot("alice", []SnapshotRow{
		{TweetID: "ranked_tweet", RankPosition: 1, FinalScore: 10},
	}); err != nil {
		t.Fatalf("replace rank snapshot: %v", err)
	}
	afterRank, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after rank: %v", err)
	}
	if before == afterRank {
		t.Fatalf("source version did not change after feed rank snapshot changed: %s", before)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_seen (username, tweet_id, seen_at)
		VALUES ('alice', 'ranked_tweet', 1234)
	`); err != nil {
		t.Fatalf("insert seen: %v", err)
	}
	afterSeen, err := d.AndroidSyncSourceVersion("alice", settings)
	if err != nil {
		t.Fatalf("source version after seen: %v", err)
	}
	if afterRank == afterSeen {
		t.Fatalf("source version did not change after seen state changed: %s", afterRank)
	}
}

func TestAndroidSyncDesiredSetsRequireFollowForRecentVideoChannels(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}
	nowMs := int64(10 * 24 * 60 * 60 * 1000)
	recent := nowMs - int64(24*60*60*1000)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq) VALUES
			('tiktok_followed', 'Followed', 'tiktok', 1),
			('tiktok_unfollowed', 'Unfollowed', 'tiktok', 2),
			('youtube_bookmarked', 'Bookmarked', 'youtube', 3)
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'tiktok_followed', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, published_at, sync_seq) VALUES
			('followed_recent', 'tiktok_followed', 'Followed recent', ?, 1),
			('unfollowed_recent', 'tiktok_unfollowed', 'Unfollowed recent', ?, 2),
			('bookmarked_unfollowed', 'youtube_bookmarked', 'Bookmarked unfollowed', ?, 3)
	`, recent, recent, recent); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, bookmarked_at)
		VALUES ('alice', 'bookmarked_unfollowed', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	sets, err := d.ListAndroidSyncDesiredSets("alice", settings, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.Videos["followed_recent"]; !ok {
		t.Fatalf("recent video from followed channel should be desired")
	}
	if _, ok := sets.Videos["unfollowed_recent"]; ok {
		t.Fatalf("recent video from unfollowed channel should not be desired")
	}
	if _, ok := sets.Videos["bookmarked_unfollowed"]; !ok {
		t.Fatalf("bookmarked video should survive unfollow")
	}
	if _, ok := sets.Channels["tiktok_unfollowed"]; ok {
		t.Fatalf("unfollowed channel without protected videos should not be desired")
	}
	if _, ok := sets.Channels["youtube_bookmarked"]; !ok {
		t.Fatalf("channel profile should remain desired for bookmarked survivor")
	}
}

func TestAndroidSyncDesiredSetsIncludeInstagramTaggedIntroducedVideos(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}
	nowMs := int64(10 * 24 * 60 * 60 * 1000)
	recent := nowMs - int64(24*60*60*1000)

	for _, step := range []struct {
		stmt string
		args []any
	}{
		{stmt: `INSERT INTO channels (channel_id, name, platform, sync_seq) VALUES
			('instagram_followed', 'Followed', 'instagram', 1),
			('instagram_owner', 'Owner', 'instagram', 2)`},
		{stmt: `INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'instagram_followed', 1)`},
		{stmt: `INSERT INTO videos (video_id, channel_id, title, published_at, sync_seq)
		 VALUES ('instagram_post_TAGGED', 'instagram_owner', 'Tagged', ?, 3)`, args: []any{recent}},
		{stmt: `INSERT INTO video_repost_sources (video_id, reposter_channel_id, reposter_handle, first_seen_at_ms, updated_at_ms)
		 VALUES ('instagram_post_TAGGED', 'instagram_followed', 'followed', ?, ?)`, args: []any{recent, recent}},
	} {
		if err := d.ExecRaw(step.stmt, step.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := d.SetSetting("", "instagram_include_tagged_default", "true"); err != nil {
		t.Fatalf("SetSetting instagram_include_tagged_default: %v", err)
	}

	sets, err := d.ListAndroidSyncDesiredSets("alice", settings, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.Videos["instagram_post_TAGGED"]; !ok {
		t.Fatalf("Instagram tagged-introduced video should be desired")
	}
	if _, ok := sets.MediaVideos["instagram_post_TAGGED"]; !ok {
		t.Fatalf("Instagram tagged-introduced media should be desired")
	}
	if _, ok := sets.Channels["instagram_owner"]; !ok {
		t.Fatalf("original owner channel should be desired")
	}
	if _, ok := sets.Channels["instagram_followed"]; !ok {
		t.Fatalf("introducer channel should be desired")
	}
}

func TestAndroidSyncDesiredSetsKeepOldFollowedYouTubeMetadata(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}
	nowMs := int64(10 * 24 * 60 * 60 * 1000)
	recent := nowMs - int64(24*60*60*1000)
	old := nowMs - int64(30*24*60*60*1000)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq)
		VALUES ('youtube_followed', 'Followed', 'youtube', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'youtube_followed', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, published_at, sync_seq) VALUES
			('recent_youtube', 'youtube_followed', 'Recent', ?, 1),
			('old_youtube', 'youtube_followed', 'Old', ?, 2)
	`, recent, old); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_comments (
			video_id, comment_id, author_name, author_id, author_thumbnail, text, published_at, platform, fetched_at
		) VALUES ('old_youtube', 'old_comment', 'Commenter', 'UColdcommenter', 'https://youtube.example/avatar.jpg', 'hello', ?, 'youtube', ?)
	`, old, nowMs); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	sets, err := d.ListAndroidSyncDesiredSets("alice", settings, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.Videos["recent_youtube"]; !ok {
		t.Fatalf("recent followed YouTube video should be desired")
	}
	if _, ok := sets.Videos["old_youtube"]; !ok {
		t.Fatalf("old followed YouTube metadata should stay desired outside retention")
	}
	if _, ok := sets.MediaVideos["old_youtube"]; ok {
		t.Fatalf("old followed YouTube video should not request full media outside retention")
	}
	if _, ok := sets.Channels["youtube_UColdcommenter"]; ok {
		t.Fatalf("YouTube comment author should not be desired as a channel")
	}
}

func TestAndroidSyncDesiredSetsDoNotProtectViewedMoments(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 1}
	nowMs := int64(3 * 24 * 60 * 60 * 1000)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, published_at, sync_seq)
		VALUES ('old_short', 'tiktok_channel', 'Old short', 1, 1)
	`); err != nil {
		t.Fatalf("insert old short: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO moment_views (username, video_id, viewed_at)
		VALUES ('alice', 'old_short', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert moment view: %v", err)
	}

	sets, err := d.ListAndroidSyncDesiredSets("alice", settings, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.Videos["old_short"]; ok {
		t.Fatalf("viewed old short should not be retained by Android sync")
	}
	if _, ok := sets.Channels["tiktok_channel"]; ok {
		t.Fatalf("channel for viewed old short should not be retained by Android sync")
	}
}

func TestAndroidSyncDesiredSetsExcludeYouTubeCommentAuthors(t *testing.T) {
	d := openWritableTestDB(t)
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90}
	nowMs := int64(10 * 24 * 60 * 60 * 1000)
	published := nowMs - int64(24*60*60*1000)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq)
		VALUES ('youtube_UCchannel', 'Video Channel', 'youtube', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'youtube_UCchannel', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, published_at, sync_seq)
		VALUES ('video_1', 'youtube_UCchannel', 'Video', ?, 1)
	`, published); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_comments (
			video_id, comment_id, author_name, author_id, author_thumbnail, text, published_at, platform, fetched_at
		) VALUES ('video_1', 'comment_1', 'Commenter', 'UCcommenter123', 'https://youtube.example/avatar.jpg', 'hello', ?, 'youtube', ?)
	`, published, nowMs); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	sets, err := d.ListAndroidSyncDesiredSets("", settings, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.Channels["youtube_UCcommenter123"]; ok {
		t.Fatalf("comment author should not be a desired channel: %+v", sets.Channels)
	}
}
