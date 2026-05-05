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
	d.conn.QueryRow("SELECT video_id FROM video_comments LIMIT 1").Scan(&videoID)
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
	var videoID string
	d.conn.QueryRow("SELECT video_id FROM videos LIMIT 1").Scan(&videoID)
	if videoID == "" {
		t.Skip("no videos")
	}
	err := d.MarkWatched(videoID, true)
	if err != nil {
		t.Fatalf("MarkWatched: %v", err)
	}
}

func TestSetPinned(t *testing.T) {
	d := openWritableTestDB(t)
	var videoID string
	d.conn.QueryRow("SELECT video_id FROM videos LIMIT 1").Scan(&videoID)
	if videoID == "" {
		t.Skip("no videos")
	}
	err := d.SetPinned(videoID, true)
	if err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
}

func TestSaveProgress(t *testing.T) {
	d := openWritableTestDB(t)
	var videoID string
	d.conn.QueryRow("SELECT video_id FROM videos LIMIT 1").Scan(&videoID)
	if videoID == "" {
		t.Skip("no videos")
	}
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
