package db

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestListAndroidSyncChannelProjectionsReadsContentAndProfileOwners(t *testing.T) {
	d := openWritableTestDB(t)
	channelID := "youtube_sample_channel"
	if err := d.AddChannel(model.Channel{
		ChannelID: channelID, SourceID: "sample_channel", Name: "Stored Channel",
		URL: "https://www.youtube.com/@sample_channel", Platform: "youtube", Quality: "1080",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	fetched := time.UnixMilli(1_700_000_100_000)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: channelID, Platform: "youtube", Handle: "sample_channel",
		DisplayName: "Sample Channel", Bio: "Profile bio", Website: "https://example.com",
		Followers: 12, Following: 3, Verified: true, FetchedAt: &fetched,
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}
	profileOnlyID := "twitter_sample_profile"
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: profileOnlyID, Platform: "twitter", Handle: "sample_profile",
		DisplayName: "Profile Only", FetchedAt: &fetched,
	}); err != nil {
		t.Fatalf("UpsertChannelProfile profile-only: %v", err)
	}

	rows, err := d.ListAndroidSyncChannelProjections([]string{
		profileOnlyID, channelID, channelID, "twitter_missing",
	})
	if err != nil {
		t.Fatalf("ListAndroidSyncChannelProjections: %v", err)
	}
	if len(rows) != 2 || rows[0].ChannelID != profileOnlyID || rows[1].ChannelID != channelID {
		t.Fatalf("projection identities = %+v", rows)
	}
	if rows[0].Channel != nil || rows[0].Profile == nil || rows[0].Profile.DisplayName != "Profile Only" {
		t.Fatalf("profile-only projection = %+v", rows[0])
	}
	row := rows[1]
	if row.Channel == nil || row.Channel.SourceID != "sample_channel" || row.Channel.Name != "Stored Channel" {
		t.Fatalf("channel projection = %+v", row.Channel)
	}
	if row.Profile == nil || row.Profile.DisplayName != "Sample Channel" || row.Profile.Followers != 12 {
		t.Fatalf("profile projection = %+v", row.Profile)
	}
}

func TestListAndroidSyncVideoProjectionsBatchesContentChildren(t *testing.T) {
	d := openWritableTestDB(t)
	channelID := "youtube_sample_channel"
	if err := d.AddChannel(model.Channel{
		ChannelID: channelID, SourceID: "sample_channel", Name: "Sample Channel",
		URL: "https://www.youtube.com/@sample_channel", Platform: "youtube",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: channelID, Platform: "youtube", Handle: "sample_channel", DisplayName: "Profile Name",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}
	videoID := "sample_video"
	if err := d.InsertVideoWithSourceKind(
		videoID, channelID, "youtube_video", "Stored title", "Stored description",
		125, 1_700_000_000_000, `{"view_count":12}`, "video", 1, false, "",
	); err != nil {
		t.Fatalf("InsertVideoWithSourceKind: %v", err)
	}
	if err := d.ExecRaw(`UPDATE videos SET dearrow_title = 'Better title' WHERE video_id = ?`, videoID); err != nil {
		t.Fatalf("update DeArrow title: %v", err)
	}
	if inserted, err := d.AddComments(videoID, []CommentInput{
		{CommentID: "comment_low", Author: "Low", Text: "low", LikeCount: 1, Timestamp: 1_700_000_001},
		{CommentID: "comment_high", Author: "High", Text: "high", LikeCount: 9, Timestamp: 1_700_000_003},
		{CommentID: "comment_mid", Author: "Mid", Text: "mid", LikeCount: 4, Timestamp: 1_700_000_002},
	}); err != nil || inserted != 3 {
		t.Fatalf("AddComments = %d, %v", inserted, err)
	}
	if err := d.SaveSponsorBlockSegments(videoID, []SponsorBlockSegment{{
		Start: 3.5, End: 8.25, Category: "sponsor",
	}}); err != nil {
		t.Fatalf("SaveSponsorBlockSegments: %v", err)
	}
	if err := d.MarkSponsorBlockChecked(videoID, "recent"); err != nil {
		t.Fatalf("MarkSponsorBlockChecked: %v", err)
	}

	rows, err := d.ListAndroidSyncVideoProjections([]string{videoID, "sample_missing", videoID}, 2)
	if err != nil {
		t.Fatalf("ListAndroidSyncVideoProjections: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("projection count = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Video.VideoID != videoID || row.Video.OwnerKind != "youtube_video" || row.Video.Duration != 125 {
		t.Fatalf("video projection = %+v", row.Video)
	}
	if row.Video.ChannelName != "" {
		t.Fatalf("presentation identity leaked into video projection: %+v", row.Video)
	}
	if row.Video.DearrowTitle == nil || *row.Video.DearrowTitle != "Better title" {
		t.Fatalf("DeArrow title = %v", row.Video.DearrowTitle)
	}
	if len(row.Comments) != 2 || row.Comments[0].CommentID != "comment_high" || row.Comments[1].CommentID != "comment_mid" {
		t.Fatalf("bounded comments = %+v", row.Comments)
	}
	if len(row.SponsorBlockSegments) != 1 || row.SponsorBlockSegments[0].Category != "sponsor" {
		t.Fatalf("SponsorBlock segments = %+v", row.SponsorBlockSegments)
	}
	if row.SponsorBlockChecked == nil || row.SponsorBlockChecked.VideoID != videoID || row.SponsorBlockChecked.VideoAgeAtCheck != "recent" {
		t.Fatalf("SponsorBlock checked = %+v", row.SponsorBlockChecked)
	}
}
