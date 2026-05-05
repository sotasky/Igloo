package db

import "testing"

func TestStoriesUseCutoffSeenStateAndOwnChannels(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 86_400_000)
	recent := nowMs - 2*3_600_000
	starredRecent := nowMs - 24*3_600_000
	old := nowMs - 72*3_600_000

	if err := d.ExecRaw(`
		INSERT INTO settings (user_id, key, value)
		VALUES ('', 'stories_window_hours', '48')
	`); err != nil {
		t.Fatalf("insert setting: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, source_id, platform, sync_seq) VALUES
			('tiktok_followed', 'Followed', 'followed', 'tiktok', 1),
			('tiktok_author', 'Author', 'author', 'tiktok', 2),
			('tiktok_reposter', 'Reposter', 'reposter', 'tiktok', 3),
			('tiktok_starred', 'Starred', 'starred', 'tiktok', 4)
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'tiktok_followed', ?), ('', 'tiktok_reposter', ?), ('', 'tiktok_starred', ?)
	`, nowMs, nowMs, nowMs); err != nil {
		t.Fatalf("insert follows: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_stars (user_id, channel_id, starred_at)
		VALUES ('', 'tiktok_starred', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert star: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, published_at, source_kind, sync_seq) VALUES
			('followed_recent', 'tiktok_followed', 'recent', ?, 'story', 1),
			('followed_old', 'tiktok_followed', 'old', ?, 'story', 2),
			('author_story', 'tiktok_author', 'author story', ?, 'story', 3),
			('regular_repost', 'tiktok_author', 'regular repost', ?, '', 4),
			('starred_recent', 'tiktok_starred', 'starred recent', ?, 'story', 5),
			('regular_recent', 'tiktok_followed', 'regular recent', ?, '', 6)
	`, recent, old, recent, recent, starredRecent, recent); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO moment_views (username, video_id, viewed_at)
		VALUES ('alice', 'starred_recent', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert starred view: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposter_handle, reposter_display_name,
			reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES ('regular_repost', 'tiktok_reposter', 'reposter', 'Reposter', ?, ?, ?)
	`, recent, recent, recent); err != nil {
		t.Fatalf("insert repost source: %v", err)
	}

	channels, hasUnseen, err := d.ListStoryChannels("alice", nowMs, 10)
	if err != nil {
		t.Fatalf("list story channels: %v", err)
	}
	if !hasUnseen {
		t.Fatalf("expected unseen followed story")
	}
	if len(channels) != 2 || channels[0].ChannelID != "tiktok_followed" || channels[1].ChannelID != "tiktok_starred" {
		t.Fatalf("followed story list should include only followed active own stories, got %#v", channels)
	}
	if channels[0].Count != 1 || channels[0].FirstVideoID != "followed_recent" {
		t.Fatalf("old followed story should be outside cutoff, got %#v", channels[0])
	}
	if channels[1].State != "seen" {
		t.Fatalf("seen starred stories should rank below unseen stories, got %#v", channels)
	}

	statuses, err := d.GetStoryStatusForChannelIDs("alice", []string{"tiktok_author", "tiktok_reposter"}, nowMs)
	if err != nil {
		t.Fatalf("story statuses: %v", err)
	}
	author := statuses["tiktok_author"]
	if author.State != "new" || author.FirstUnseenVideoID != "author_story" || author.Count != 1 {
		t.Fatalf("recent repost-introduced author should have a new story, got %#v", author)
	}
	if status, ok := statuses["tiktok_reposter"]; ok && status.Count > 0 {
		t.Fatalf("repost must not become the reposter's own story, got %#v", status)
	}

	if err := d.ExecRaw(`
		INSERT INTO moment_views (username, video_id, viewed_at)
		VALUES ('alice', 'author_story', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert view: %v", err)
	}
	statuses, err = d.GetStoryStatusForChannelIDs("alice", []string{"tiktok_author"}, nowMs)
	if err != nil {
		t.Fatalf("story statuses after view: %v", err)
	}
	if got := statuses["tiktok_author"].State; got != "seen" {
		t.Fatalf("viewed active story should be seen, got %q", got)
	}
}

func TestStoriesSkipLegacyInstagramTrayRows(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 86_400_000)
	recent := nowMs - 2*3_600_000

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, source_id, platform, sync_seq) VALUES
			('instagram_cinema', 'Cinema', 'cinema', 'instagram', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'instagram_cinema', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, published_at, source_kind, sync_seq) VALUES
			('instagram_story_TRAY123', 'instagram_cinema', 'tray', 0, ?, 'story', 1),
			('instagram_story_987654321', 'instagram_cinema', 'frame', 0, ?, 'story', 2)
	`, recent, recent); err != nil {
		t.Fatalf("insert stories: %v", err)
	}

	channels, _, err := d.ListStoryChannels("alice", nowMs, 10)
	if err != nil {
		t.Fatalf("list story channels: %v", err)
	}
	if len(channels) != 1 || channels[0].Count != 1 || channels[0].FirstVideoID != "instagram_story_987654321" {
		t.Fatalf("story channel should count only media-id story rows, got %#v", channels)
	}

	statuses, err := d.GetStoryStatusForChannelIDs("alice", []string{"instagram_cinema"}, nowMs)
	if err != nil {
		t.Fatalf("story statuses: %v", err)
	}
	if status := statuses["instagram_cinema"]; status.Count != 1 || status.FirstVideoID != "instagram_story_987654321" {
		t.Fatalf("story status should ignore tray rows, got %#v", status)
	}

	videos, err := d.GetStoryVideos("alice", "instagram_cinema", nowMs)
	if err != nil {
		t.Fatalf("story videos: %v", err)
	}
	if len(videos) != 1 || videos[0].VideoID != "instagram_story_987654321" {
		t.Fatalf("story videos should ignore tray rows, got %#v", videos)
	}

	sets, err := d.ListAndroidSyncDesiredSets("alice", AndroidRetentionSettings{FeedDays: 1, YoutubeDays: 1, MomentsDays: 1, StoryHours: 48}, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.MediaVideos["instagram_story_TRAY123"]; ok {
		t.Fatalf("legacy tray story should not be desired for Android sync")
	}
	if _, ok := sets.MediaVideos["instagram_story_987654321"]; !ok {
		t.Fatalf("media-id story should be desired for Android sync")
	}
}

func TestAndroidSyncDesiredSetsIncludeStoryMediaAndBookmarkedExpiredStory(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 86_400_000)
	recent := nowMs - 12*3_600_000
	expired := nowMs - 72*3_600_000
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 1, StoryHours: 48}

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, source_id, platform, sync_seq) VALUES
			('tiktok_author', 'Author', 'author', 'tiktok', 1),
			('tiktok_reposter', 'Reposter', 'reposter', 'tiktok', 2)
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'tiktok_reposter', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, published_at, source_kind, sync_seq) VALUES
			('recent_author_story', 'tiktok_author', 'recent author story', ?, 'story', 1),
			('expired_bookmarked_story', 'tiktok_author', 'expired bookmarked story', ?, 'story', 2),
			('expired_unprotected_story', 'tiktok_author', 'expired unprotected story', ?, 'story', 3),
			('recent_regular_post', 'tiktok_author', 'recent regular post', ?, '', 4)
	`, recent, expired, expired, recent); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposter_handle, reposter_display_name,
			reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES ('recent_regular_post', 'tiktok_reposter', 'reposter', 'Reposter', ?, ?, ?)
	`, recent, recent, recent); err != nil {
		t.Fatalf("insert repost source: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, bookmarked_at)
		VALUES ('alice', 'expired_bookmarked_story', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.SetSetting("", "moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}

	sets, err := d.ListAndroidSyncDesiredSets("alice", settings, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.MediaVideos["recent_author_story"]; !ok {
		t.Fatalf("recent repost-introduced story should require media")
	}
	if _, ok := sets.MediaVideos["expired_bookmarked_story"]; !ok {
		t.Fatalf("bookmarked expired story should keep media protection")
	}
	if _, ok := sets.MediaVideos["expired_unprotected_story"]; ok {
		t.Fatalf("expired unbookmarked story should not require media")
	}
}
