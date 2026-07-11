package db

import "testing"

func TestStoriesUseCutoffSeenStateAndOwnChannels(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 86_400_000)
	recent := nowMs - 2*3_600_000
	starredRecent := nowMs - 24*3_600_000
	old := nowMs - 72*3_600_000

	if err := d.ExecRaw(`
		INSERT INTO settings (key, value)
		VALUES ('stories_window_hours', '48')
	`); err != nil {
		t.Fatalf("insert setting: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, source_id, platform) VALUES
			('tiktok_followed', 'Followed', 'followed', 'tiktok'),
			('tiktok_author', 'Author', 'author', 'tiktok'),
			('tiktok_reposter', 'Reposter', 'reposter', 'tiktok'),
			('tiktok_starred', 'Starred', 'starred', 'tiktok')
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('tiktok_followed', ?), ('tiktok_reposter', ?), ('tiktok_starred', ?)
	`, nowMs, nowMs, nowMs); err != nil {
		t.Fatalf("insert follows: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_stars (channel_id, starred_at)
		VALUES ('tiktok_starred', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert star: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at, source_kind) VALUES
			('followed_recent', 'tiktok_followed', 'tiktok_video', 'recent', ?, 'story'),
			('followed_old', 'tiktok_followed', 'tiktok_video', 'old', ?, 'story'),
			('author_story', 'tiktok_author', 'tiktok_video', 'author story', ?, 'story'),
			('regular_repost', 'tiktok_author', 'tiktok_video', 'regular repost', ?, ''),
			('starred_recent', 'tiktok_starred', 'tiktok_video', 'starred recent', ?, 'story'),
			('regular_recent', 'tiktok_followed', 'tiktok_video', 'regular recent', ?, '')
	`, recent, old, recent, recent, starredRecent, recent); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO moment_views (video_id, viewed_at)
		VALUES ('starred_recent', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert starred view: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES ('regular_repost', 'tiktok_reposter', ?, ?, ?)
	`, recent, recent, recent); err != nil {
		t.Fatalf("insert repost source: %v", err)
	}

	channels, hasUnseen, err := d.ListStoryChannels(nowMs, 10)
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

	statuses, err := d.GetStoryStatusForChannelIDs([]string{"tiktok_author", "tiktok_reposter"}, nowMs)
	if err != nil {
		t.Fatalf("story statuses: %v", err)
	}
	if status, ok := statuses["tiktok_author"]; ok && status.Count > 0 {
		t.Fatalf("unfollowed repost-introduced author should not have story status, got %#v", status)
	}
	if status, ok := statuses["tiktok_reposter"]; ok && status.Count > 0 {
		t.Fatalf("repost must not become the reposter's own story, got %#v", status)
	}

	if err := d.ExecRaw(`
		INSERT INTO moment_views (video_id, viewed_at)
		VALUES ('followed_recent', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert view: %v", err)
	}
	statuses, err = d.GetStoryStatusForChannelIDs([]string{"tiktok_followed"}, nowMs)
	if err != nil {
		t.Fatalf("story statuses after view: %v", err)
	}
	if got := statuses["tiktok_followed"].State; got != "seen" {
		t.Fatalf("viewed active story should be seen, got %q", got)
	}
}

func TestStoriesSkipLegacyInstagramTrayRows(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 86_400_000)
	recent := nowMs - 2*3_600_000

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, source_id, platform) VALUES
			('instagram_cinema', 'Cinema', 'cinema', 'instagram')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('instagram_cinema', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at, source_kind) VALUES
			('instagram_story_TRAY123', 'instagram_cinema', 'instagram_reel', 'tray', 0, ?, 'story'),
			('instagram_story_987654321', 'instagram_cinema', 'instagram_reel', 'frame', 0, ?, 'story')
	`, recent, recent); err != nil {
		t.Fatalf("insert stories: %v", err)
	}

	channels, _, err := d.ListStoryChannels(nowMs, 10)
	if err != nil {
		t.Fatalf("list story channels: %v", err)
	}
	if len(channels) != 1 || channels[0].Count != 1 || channels[0].FirstVideoID != "instagram_story_987654321" {
		t.Fatalf("story channel should count only media-id story rows, got %#v", channels)
	}

	statuses, err := d.GetStoryStatusForChannelIDs([]string{"instagram_cinema"}, nowMs)
	if err != nil {
		t.Fatalf("story statuses: %v", err)
	}
	if status := statuses["instagram_cinema"]; status.Count != 1 || status.FirstVideoID != "instagram_story_987654321" {
		t.Fatalf("story status should ignore tray rows, got %#v", status)
	}

	videos, err := d.GetStoryVideos("instagram_cinema", nowMs)
	if err != nil {
		t.Fatalf("story videos: %v", err)
	}
	if len(videos) != 1 || videos[0].VideoID != "instagram_story_987654321" {
		t.Fatalf("story videos should ignore tray rows, got %#v", videos)
	}

	sets, err := d.ListAndroidSyncDesiredSets(AndroidRetentionSettings{FeedDays: 1, YoutubeDays: 1, MomentsDays: 1, StoryHours: 48}, nowMs)
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

func TestAndroidSyncDesiredSetsIncludeFollowedStoryMediaAndBookmarkedExpiredStory(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 86_400_000)
	recent := nowMs - 12*3_600_000
	expired := nowMs - 72*3_600_000
	settings := AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 1, StoryHours: 48}

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, source_id, platform) VALUES
			('tiktok_author', 'Author', 'author', 'tiktok'),
			('tiktok_sample_reposter', 'Sample Reposter', 'sample_reposter', 'tiktok'),
			('tiktok_followed', 'Followed', 'followed', 'tiktok')
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('tiktok_sample_reposter', ?), ('tiktok_followed', ?)
	`, nowMs, nowMs); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at, source_kind) VALUES
			('recent_author_story', 'tiktok_author', 'tiktok_video', 'recent author story', ?, 'story'),
			('expired_bookmarked_story', 'tiktok_author', 'tiktok_video', 'expired bookmarked story', ?, 'story'),
			('expired_unprotected_story', 'tiktok_author', 'tiktok_video', 'expired unprotected story', ?, 'story'),
			('sample_post', 'tiktok_author', 'tiktok_video', 'recent regular post', ?, ''),
			('recent_followed_story', 'tiktok_followed', 'tiktok_video', 'recent followed story', ?, 'story')
	`, recent, expired, expired, recent, recent); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES ('sample_post', 'tiktok_sample_reposter', ?, ?, ?)
	`, recent, recent, recent); err != nil {
		t.Fatalf("insert repost source: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO bookmarks (video_id, bookmarked_at)
		VALUES ('expired_bookmarked_story', ?)
	`, nowMs); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.SetSetting("moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}

	sets, err := d.ListAndroidSyncDesiredSets(settings, nowMs)
	if err != nil {
		t.Fatalf("desired sets: %v", err)
	}
	if _, ok := sets.MediaVideos["recent_author_story"]; ok {
		t.Fatalf("unfollowed repost-introduced story should not require media")
	}
	if _, ok := sets.MediaVideos["recent_followed_story"]; !ok {
		t.Fatalf("recent followed story should require media")
	}
	if _, ok := sets.MediaVideos["expired_bookmarked_story"]; !ok {
		t.Fatalf("bookmarked expired story should keep media protection")
	}
	if _, ok := sets.MediaVideos["expired_unprotected_story"]; ok {
		t.Fatalf("expired unbookmarked story should not require media")
	}
}
