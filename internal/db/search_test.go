package db

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestSearchFTSTriggersKeepReadyIndexCurrent(t *testing.T) {
	d := openFreshTestDB(t)

	seedSearchChannel(t, d, "tiktok_sample_channel", "tiktok")
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_sample_channel",
		Platform:    "tiktok",
		Handle:      "sample_handle",
		DisplayName: "Sample Fresh Display",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}
	seedSearchVideo(t, d, "sample_video_1", "tiktok_sample_channel", "Original Title")
	casual := "Sample Fresh Casual"
	if err := d.SetDearrowData("sample_video_1", nil, &casual, nil, 1_700_000_000_000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	channels, err := d.SearchChannelsFast("fresh", 10)
	if err != nil {
		t.Fatalf("SearchChannelsFast: %v", err)
	}
	if len(channels) != 1 || channels[0].ChannelID != "tiktok_sample_channel" {
		t.Fatalf("channels = %+v, want tiktok_sample_channel", channels)
	}

	videos, err := d.SearchVideosFast("casual", 10)
	if err != nil {
		t.Fatalf("SearchVideosFast: %v", err)
	}
	if len(videos) != 1 || videos[0].VideoID != "sample_video_1" {
		t.Fatalf("videos = %+v, want sample_video_1", videos)
	}

	videos, err = d.SearchVideosFast("display", 10)
	if err != nil {
		t.Fatalf("SearchVideosFast profile display name: %v", err)
	}
	if len(videos) != 1 || videos[0].VideoID != "sample_video_1" {
		t.Fatalf("profile display-name videos = %+v, want sample_video_1", videos)
	}

	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_sample_channel",
		Platform:    "tiktok",
		Handle:      "sample_handle",
		DisplayName: "Sample Renamed Creator",
	}); err != nil {
		t.Fatalf("update channel profile: %v", err)
	}
	videos, err = d.SearchVideosFast("renamed", 10)
	if err != nil {
		t.Fatalf("SearchVideosFast updated profile display name: %v", err)
	}
	if len(videos) != 1 || videos[0].VideoID != "sample_video_1" {
		t.Fatalf("updated profile display-name videos = %+v, want sample_video_1", videos)
	}
}

// Test helpers.
func seedSearchChannel(t *testing.T, d *DB, channelID, platform string) {
	t.Helper()
	_, _ = d.conn.Exec(`INSERT OR IGNORE INTO channels (channel_id, name, platform) VALUES (?, ?, ?)`,
		channelID, "Test Channel", platform)
}

func seedSearchVideo(t *testing.T, d *DB, videoID, channelID, title string) {
	t.Helper()
	if err := d.InsertVideo(
		videoID, channelID, "youtube_video", title, "",
		60, 1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo %s: %v", videoID, err)
	}
}

func TestSearchChannelsFast(t *testing.T) {
	d := openTestDB(t)
	results, err := d.SearchChannelsFast("test", 10)
	if err != nil {
		t.Fatalf("SearchChannelsFast: %v", err)
	}
	_ = results
}

func TestSearchVideosFast(t *testing.T) {
	d := openTestDB(t)
	results, err := d.SearchVideosFast("test", 20)
	if err != nil {
		t.Fatalf("SearchVideosFast: %v", err)
	}
	_ = results
}

func TestSearchFeedItems(t *testing.T) {
	d := openTestDB(t)
	results, err := d.SearchFeedItems("test", 20)
	if err != nil {
		t.Fatalf("SearchFeedItems: %v", err)
	}
	_ = results
}
