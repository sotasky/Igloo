package db

import "testing"

func TestGetVideo(t *testing.T) {
	d := openTestDB(t)
	var videoID string
	err := d.conn.QueryRow("SELECT video_id FROM videos LIMIT 1").Scan(&videoID)
	if err != nil {
		t.Skip("no videos in test DB")
	}

	video, err := d.GetVideo(videoID)
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if video == nil {
		t.Fatal("GetVideo returned nil for existing video")
	}
	if video.VideoID != videoID {
		t.Errorf("video_id mismatch: got %q, want %q", video.VideoID, videoID)
	}
	if video.Title == "" {
		t.Error("title is empty")
	}
}

func TestGetVideoNotFound(t *testing.T) {
	d := openTestDB(t)
	video, err := d.GetVideo("nonexistent_video_id_xyz")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if video != nil {
		t.Error("expected nil for nonexistent video")
	}
}

func TestGetVideos(t *testing.T) {
	d := openTestDB(t)
	videos, err := d.GetVideos(GetVideosOpts{Limit: 5})
	if err != nil {
		t.Fatalf("GetVideos: %v", err)
	}
	if len(videos) > 5 {
		t.Errorf("expected at most 5, got %d", len(videos))
	}
}

func TestGetVideoCount(t *testing.T) {
	d := openTestDB(t)
	count, err := d.GetVideoCount(GetVideosOpts{})
	if err != nil {
		t.Fatalf("GetVideoCount: %v", err)
	}
	if count < 0 {
		t.Errorf("negative count: %d", count)
	}
}

func TestGetVideosByChannel(t *testing.T) {
	d := openTestDB(t)
	var channelID string
	err := d.conn.QueryRow("SELECT channel_id FROM videos LIMIT 1").Scan(&channelID)
	if err != nil {
		t.Skip("no videos in test DB")
	}

	videos, err := d.GetVideos(GetVideosOpts{ChannelID: channelID, Limit: 3})
	if err != nil {
		t.Fatalf("GetVideos by channel: %v", err)
	}
	for _, v := range videos {
		if v.ChannelID != channelID {
			t.Errorf("wrong channel: got %q, want %q", v.ChannelID, channelID)
		}
	}
}

func TestGetVideosIncludesUndownloadedShortsRows(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('tiktok_sample_slides_account', 'sample_slides_account', 'SAMPLE SLIDES', 'tiktok')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'tiktok_sample_slides_account')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, file_path, media_kind, slide_count, published_at
		) VALUES (
			'7201436805915266309', 'tiktok_sample_slides_account', 'Ghost station', 0, '', 'slideshow', 4, 1676678400000
		)
	`); err != nil {
		t.Fatal(err)
	}

	channelVideos, err := d.GetVideos(GetVideosOpts{ChannelID: "tiktok_sample_slides_account", Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos by TikTok channel: %v", err)
	}
	if len(channelVideos) != 1 {
		t.Fatalf("expected 1 TikTok channel video, got %d", len(channelVideos))
	}
	if channelVideos[0].VideoID != "7201436805915266309" {
		t.Fatalf("unexpected TikTok video id: %q", channelVideos[0].VideoID)
	}

	shortsVideos, err := d.GetVideos(GetVideosOpts{Platform: "shorts", Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos shorts: %v", err)
	}
	if len(shortsVideos) != 1 {
		t.Fatalf("expected 1 shorts video, got %d", len(shortsVideos))
	}

	count, err := d.GetVideoCount(GetVideosOpts{ChannelID: "tiktok_sample_slides_account"})
	if err != nil {
		t.Fatalf("GetVideoCount by TikTok channel: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected TikTok channel count 1, got %d", count)
	}
}

func TestGetVideosExcludesNativeStoriesFromNormalMoments(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('tiktok_native_story', 'native_story', 'Native Story', 'tiktok')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'tiktok_native_story')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, file_path, media_kind, published_at, source_kind
		) VALUES
			('regular_short', 'tiktok_native_story', 'regular', 0, '', 'video', 1000, ''),
			('native_story', 'tiktok_native_story', 'story', 0, '', 'video', 2000, 'story')
	`); err != nil {
		t.Fatal(err)
	}

	moments, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "following", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos moments: %v", err)
	}
	if got := videoIDs(moments); len(got) != 1 || got[0] != "regular_short" {
		t.Fatalf("moments ids = %v, want [regular_short]", got)
	}

	stories, err := d.GetVideos(GetVideosOpts{Platform: "shorts", SourceKind: "story", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos stories: %v", err)
	}
	if got := videoIDs(stories); len(got) != 1 || got[0] != "native_story" {
		t.Fatalf("story ids = %v, want [native_story]", got)
	}

	ordinal, ok, err := d.GetShortsOrdinal("native_story", "following")
	if err != nil {
		t.Fatalf("GetShortsOrdinal story: %v", err)
	}
	if ok || ordinal != 0 {
		t.Fatalf("story ordinal = %d/%v, want 0/false", ordinal, ok)
	}
	ordinal, ok, err = d.GetShortsOrdinal("regular_short", "following")
	if err != nil {
		t.Fatalf("GetShortsOrdinal regular: %v", err)
	}
	if !ok || ordinal != 1 {
		t.Fatalf("regular ordinal = %d/%v, want 1/true", ordinal, ok)
	}
}

func TestGetVideosAllMomentsUsesRepostEventForFollowedAuthor(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.SetSetting("", "moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('tiktok_sample_author', 'sample_author', 'Sample Author', 'tiktok'),
			('tiktok_sample_reposter', 'sample_reposter', 'Sample Reposter', 'tiktok'),
			('tiktok_sample_author_b', 'sample_author_b', 'Sample Author B', 'tiktok')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES
			('', 'tiktok_sample_author', 1),
			('', 'tiktok_sample_reposter', 1),
			('', 'tiktok_sample_author_b', 1)
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, media_kind, published_at) VALUES
			('sample_old_author_reposted_late', 'tiktok_sample_author', 'Old author clip', 0, '', 'video', 10),
			('sample_plain_middle_clip', 'tiktok_sample_author_b', 'Plain clip', 0, '', 'video', 50)
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposter_handle, reposter_display_name, reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES (
			'sample_old_author_reposted_late', 'tiktok_sample_reposter', 'sample_reposter', 'Sample Reposter', 100, 90, 100
		)
	`); err != nil {
		t.Fatal(err)
	}

	all, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos all moments: %v", err)
	}
	if got := videoIDs(all); len(got) != 2 || got[0] != "sample_plain_middle_clip" || got[1] != "sample_old_author_reposted_late" {
		t.Fatalf("all moments ids = %v, want [sample_plain_middle_clip sample_old_author_reposted_late]", got)
	}
	reposted := all[1]
	if !reposted.RepostIntroduced || reposted.EffectiveMomentAtMs != 100 {
		t.Fatalf("reposted event fields = introduced %v effective %d, want true/100", reposted.RepostIntroduced, reposted.EffectiveMomentAtMs)
	}

	ordinal, ok, err := d.GetShortsOrdinal("sample_old_author_reposted_late", "all")
	if err != nil {
		t.Fatalf("GetShortsOrdinal all: %v", err)
	}
	if !ok || ordinal != 2 {
		t.Fatalf("reposted ordinal = %d/%v, want 2/true", ordinal, ok)
	}

	following, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "following", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos following moments: %v", err)
	}
	if got := videoIDs(following); len(got) != 2 || got[0] != "sample_old_author_reposted_late" || got[1] != "sample_plain_middle_clip" {
		t.Fatalf("following moments ids = %v, want [sample_old_author_reposted_late sample_plain_middle_clip]", got)
	}
}

func TestGetVideosKeepsUndownloadedYouTubeRowsHidden(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('youtube_test_channel', 'UCtest', 'Test Channel', 'youtube')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'youtube_test_channel')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, file_path, media_kind, slide_count, published_at
		) VALUES (
			'youtube_missing_file', 'youtube_test_channel', 'Missing local file', 0, '', 'video', 0, 1676678400000
		)
	`); err != nil {
		t.Fatal(err)
	}

	videos, err := d.GetVideos(GetVideosOpts{ChannelID: "youtube_test_channel", Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos by YouTube channel: %v", err)
	}
	if len(videos) != 0 {
		t.Fatalf("expected undownloaded YouTube video to stay hidden, got %d rows", len(videos))
	}

	count, err := d.GetVideoCount(GetVideosOpts{ChannelID: "youtube_test_channel"})
	if err != nil {
		t.Fatalf("GetVideoCount by YouTube channel: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected YouTube channel count 0, got %d", count)
	}
}

func TestGetLatestVideosPerChannelIncludesUndownloadedShortsOnly(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('tiktok_preview_channel', 'preview_tiktok', 'Preview TikTok', 'tiktok'),
			('youtube_preview_channel', 'UCpreview', 'Preview YouTube', 'youtube')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id) VALUES
			('', 'tiktok_preview_channel'),
			('', 'youtube_preview_channel')
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, file_path, media_kind, slide_count, published_at, source_kind
		) VALUES
			('tiktok_preview_video', 'tiktok_preview_channel', 'Preview slideshow', 0, '', 'slideshow', 3, 1676678400000, ''),
			('tiktok_preview_story', 'tiktok_preview_channel', 'Preview story', 0, '', 'video', 0, 1676678402000, 'story'),
			('youtube_preview_video', 'youtube_preview_channel', 'Preview longform', 0, '', 'video', 0, 1676678401000, '')
	`); err != nil {
		t.Fatal(err)
	}

	previews, err := d.GetLatestVideosPerChannel(5, "tiktok_preview_channel", "youtube_preview_channel")
	if err != nil {
		t.Fatalf("GetLatestVideosPerChannel: %v", err)
	}
	if got := len(previews["tiktok_preview_channel"]); got != 1 {
		t.Fatalf("expected 1 TikTok preview row, got %d", got)
	}
	if got := previews["tiktok_preview_channel"][0].VideoID; got != "tiktok_preview_video" {
		t.Fatalf("latest preview video = %q, want tiktok_preview_video", got)
	}
	if got := len(previews["youtube_preview_channel"]); got != 0 {
		t.Fatalf("expected 0 YouTube preview rows, got %d", got)
	}
}

func TestGetComments(t *testing.T) {
	d := openTestDB(t)
	var videoID string
	_ = d.conn.QueryRow("SELECT video_id FROM video_comments LIMIT 1").Scan(&videoID)
	if videoID == "" {
		t.Skip("no comments in test DB")
	}

	comments, err := d.GetComments(videoID, 10)
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) == 0 {
		t.Error("expected comments but got none")
	}
}

func TestGetPlaybackPosition(t *testing.T) {
	d := openTestDB(t)
	pos, err := d.GetPlaybackPosition("nonexistent_xyz", "")
	if err != nil {
		t.Fatalf("GetPlaybackPosition: %v", err)
	}
	if pos != 0 {
		t.Errorf("expected 0 for unknown video, got %f", pos)
	}
}

func TestMarkWatched(t *testing.T) {
	d := openWritableTestDB(t)
	const videoID = "fixture_mark_watched"

	seedTestVideo(t, d, videoID, "youtube_watch_fixture")
	err := d.MarkWatched(videoID, true)
	if err != nil {
		t.Fatalf("MarkWatched: %v", err)
	}
	var watched int
	if err := d.QueryRow(`SELECT COALESCE(watched, 0) FROM videos WHERE video_id = ?`, videoID).Scan(&watched); err != nil {
		t.Fatalf("read watched: %v", err)
	}
	if watched != 1 {
		t.Fatalf("watched = %d, want 1", watched)
	}
}

func TestSetPinned(t *testing.T) {
	d := openWritableTestDB(t)
	const videoID = "fixture_set_pinned"

	seedTestVideo(t, d, videoID, "youtube_pin_fixture")
	err := d.SetPinned(videoID, true)
	if err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	var pinned int
	if err := d.QueryRow(`SELECT COALESCE(is_pinned, 0) FROM videos WHERE video_id = ?`, videoID).Scan(&pinned); err != nil {
		t.Fatalf("read pinned: %v", err)
	}
	if pinned != 1 {
		t.Fatalf("is_pinned = %d, want 1", pinned)
	}
}

func TestSaveProgress(t *testing.T) {
	d := openWritableTestDB(t)
	const videoID = "fixture_save_progress"

	seedTestVideo(t, d, videoID, "youtube_progress_fixture")
	result, err := d.SaveProgress("test_user", videoID, 30.5, 120.0, 0, "web")
	if err != nil {
		t.Fatalf("SaveProgress: %v", err)
	}
	if !result.Accepted {
		t.Error("expected first save to be accepted")
	}
	if result.ResolvedPosition != 30.5 {
		t.Errorf("expected position 30.5, got %f", result.ResolvedPosition)
	}
	var position, duration float64
	var source string
	if err := d.QueryRow(`
		SELECT playback_position, duration, progress_source
		FROM watch_history
		WHERE user_id = 'test_user' AND video_id = ?
	`, videoID).Scan(&position, &duration, &source); err != nil {
		t.Fatalf("read watch history: %v", err)
	}
	if position != 30.5 || duration != 120 || source != "web" {
		t.Fatalf("watch history = position %f duration %f source %q, want 30.5/120/web", position, duration, source)
	}
}

func TestGetSponsorBlockSegments(t *testing.T) {
	d := openTestDB(t)
	segments, err := d.GetSponsorBlockSegments("nonexistent_xyz")
	if err != nil {
		t.Fatalf("GetSponsorBlockSegments: %v", err)
	}
	if len(segments) != 0 {
		t.Errorf("expected 0 segments, got %d", len(segments))
	}
}
