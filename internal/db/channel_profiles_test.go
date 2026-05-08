package db

import (
	"reflect"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func seedCachedProfileVideo(t *testing.T, d *DB, channelID, videoID string) {
	t.Helper()
	if err := d.InsertVideo(
		videoID, channelID, "profile video", "",
		0, "", "media/"+videoID+".mp4", 0,
		time.Now().UnixMilli(), "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}
}

func TestUpsertGetChannelProfile(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	p := model.ChannelProfile{
		ChannelID:    "twitter_alice",
		Platform:     "twitter",
		Handle:       "alice",
		DisplayName:  "Alice",
		Bio:          "hello",
		Followers:    100,
		Following:    50,
		Verified:     true,
		VerifiedType: "blue",
		AvatarURL:    "https://cdn/a.jpg",
		BannerURL:    "https://cdn/b.jpg",
		FetchedAt:    &now,
	}
	if err := d.UpsertChannelProfile(p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := d.GetChannelProfile("twitter_alice")
	if err != nil || got == nil {
		t.Fatalf("get: %v, got=%v", err, got)
	}
	if got.DisplayName != "Alice" || got.Followers != 100 || !got.Verified {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.FetchedAt == nil || got.FetchedAt.UnixMilli() != now.UnixMilli() {
		t.Fatalf("fetched_at lost: %v", got.FetchedAt)
	}
}

func TestMarkChannelProfileRefreshDueClearsFreshness(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	nextRetry := now.Add(time.Hour)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_cached",
		Platform:    "twitter",
		Handle:      "cached",
		DisplayName: "Cached",
		AvatarURL:   "https://pbs.twimg.com/profile_images/777/photo_normal.jpg",
		FetchedAt:   &now,
		FailCount:   3,
		NextRetryAt: &nextRetry,
		Tombstone:   true,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	if err := d.MarkChannelProfileRefreshDue("twitter_cached"); err != nil {
		t.Fatalf("MarkChannelProfileRefreshDue: %v", err)
	}

	got, err := d.GetChannelProfile("twitter_cached")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.AvatarURL != "https://pbs.twimg.com/profile_images/777/photo_normal.jpg" {
		t.Fatalf("avatar URL was not preserved: %+v", got)
	}
	if got.FetchedAt != nil || got.NextRetryAt != nil || got.FailCount != 0 || got.Tombstone {
		t.Fatalf("refresh state was not cleared: %+v", got)
	}
}

func TestUpsertChannelProfilePreservesNonEmpty(t *testing.T) {
	d := openWritableTestDB(t)

	first := model.ChannelProfile{
		ChannelID: "tiktok_bob", Platform: "tiktok", Handle: "bob",
		AvatarURL: "url1", BannerURL: "", DisplayName: "Bob",
	}
	if err := d.UpsertChannelProfile(first); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Upsert with empty AvatarURL — COALESCE should preserve existing.
	second := model.ChannelProfile{
		ChannelID: "tiktok_bob", Platform: "tiktok", Handle: "bob",
		DisplayName: "Bob2",
	}
	if err := d.UpsertChannelProfile(second); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, _ := d.GetChannelProfile("tiktok_bob")
	if got.AvatarURL != "url1" {
		t.Fatalf("avatar URL should be preserved, got %q", got.AvatarURL)
	}
	if got.DisplayName != "Bob2" {
		t.Fatalf("display name should update, got %q", got.DisplayName)
	}
}

func TestNextChannelProfileRefreshCandidateOldestFirst(t *testing.T) {
	d := openWritableTestDB(t)

	old := time.Now().Add(-48 * time.Hour).UTC()
	newer := time.Now().Add(-30 * time.Hour).UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_old", Platform: "twitter", FetchedAt: &old})
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_newer", Platform: "twitter", FetchedAt: &newer})

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "twitter_old" {
		t.Fatalf("expected oldest first, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidateFiltersPlatform(t *testing.T) {
	d := openWritableTestDB(t)

	old := time.Now().Add(-48 * time.Hour).UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_old", Platform: "twitter", FetchedAt: &old})
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "tiktok_old", Platform: "tiktok", FetchedAt: &old})

	got, err := d.NextChannelProfileRefreshCandidateForPlatform(24*time.Hour, "tiktok")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "tiktok_old" {
		t.Fatalf("expected tiktok_old, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidateSkipsTombstoned(t *testing.T) {
	d := openWritableTestDB(t)

	old := time.Now().Add(-48 * time.Hour).UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_ghost", Platform: "twitter", FetchedAt: &old, Tombstone: true})
	got, _ := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if got != "" {
		t.Fatalf("expected empty (tombstoned skipped), got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidatePrioritizesTikTokMissingBanner(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_missing",
		Platform:    "tiktok",
		Handle:      "missing",
		DisplayName: "Missing Banner",
		FetchedAt:   &now,
	})
	seedCachedProfileVideo(t, d, "tiktok_missing", "tiktok_video_1")
	old := time.Now().Add(-48 * time.Hour).UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_old",
		Platform:    "twitter",
		FetchedAt:   &old,
		BannerURL:   "https://example.com/banner.jpg",
		DisplayName: "Old Twitter",
	})

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "tiktok_missing" {
		t.Fatalf("expected tiktok_missing first, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidatePrioritizesInstagramMissingBanner(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_missing",
		Platform:    "instagram",
		Handle:      "missing",
		DisplayName: "Missing Banner",
		FetchedAt:   &now,
	})
	seedCachedProfileVideo(t, d, "instagram_missing", "instagram_video_1")
	old := time.Now().Add(-48 * time.Hour).UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_old",
		Platform:    "twitter",
		FetchedAt:   &old,
		BannerURL:   "https://example.com/banner.jpg",
		DisplayName: "Old Twitter",
	})

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "instagram_missing" {
		t.Fatalf("expected instagram_missing first, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidateSkipsMissingShortBannerWithoutCachedMedia(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_empty",
		Platform:    "instagram",
		Handle:      "empty",
		DisplayName: "Empty",
		FetchedAt:   &now,
	})
	old := time.Now().Add(-48 * time.Hour).UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_old",
		Platform:    "twitter",
		FetchedAt:   &old,
		BannerURL:   "https://example.com/banner.jpg",
		DisplayName: "Old Twitter",
	})

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "twitter_old" {
		t.Fatalf("expected twitter_old first, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidateRespectsRetryBackoffForCachedShortBanner(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC()
	nextRetry := now.Add(time.Hour)
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_waiting",
		Platform:    "instagram",
		Handle:      "waiting",
		DisplayName: "Waiting",
		FetchedAt:   &now,
		NextRetryAt: &nextRetry,
	})
	seedCachedProfileVideo(t, d, "instagram_waiting", "instagram_video_2")
	old := time.Now().Add(-48 * time.Hour).UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_old",
		Platform:    "twitter",
		FetchedAt:   &old,
		BannerURL:   "https://example.com/banner.jpg",
		DisplayName: "Old Twitter",
	})

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "twitter_old" {
		t.Fatalf("expected twitter_old first, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidatePrioritizesCachedShortBannerBeforeUnfetchedRows(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_missing",
		Platform:    "tiktok",
		Handle:      "missing",
		DisplayName: "Missing Banner",
		FetchedAt:   &now,
	})
	seedCachedProfileVideo(t, d, "tiktok_missing", "tiktok_video_2")
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_unfetched",
		Platform:    "instagram",
		Handle:      "unfetched",
		DisplayName: "Unfetched",
	})

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "tiktok_missing" {
		t.Fatalf("expected tiktok_missing first, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidatePrioritizesUnfetchedSourceWindowOwner(t *testing.T) {
	d := openWritableTestDB(t)

	now := time.Now().UTC()
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_missing",
		Platform:    "tiktok",
		Handle:      "missing",
		DisplayName: "Missing Banner",
		FetchedAt:   &now,
	})
	seedCachedProfileVideo(t, d, "tiktok_missing", "tiktok_video_2")
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_by.bansoi",
		Platform:    "instagram",
		Handle:      "by.bansoi",
		DisplayName: "soi",
	})
	publishedAt := now.UnixMilli()
	if err := d.InsertVideo(
		"instagram_post_bansoi", "instagram_by.bansoi", "Dear Father", "",
		0, "", "", 0, publishedAt, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:           "instagram_post_bansoi",
		ReposterChannelID: "instagram_asiancinemaarchive",
		ReposterHandle:    "asiancinemaarchive",
		FirstSeenAtMs:     publishedAt + 1000,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "instagram_by.bansoi" {
		t.Fatalf("expected source-window owner first, got %q", got)
	}
}

func TestNextChannelProfileRefreshCandidatePrioritizesSubscribedUnfetchedRows(t *testing.T) {
	d := openWritableTestDB(t)

	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_feed_author",
		Platform:    "twitter",
		Handle:      "feed_author",
		DisplayName: "Feed Author",
	})
	if _, err := d.conn.Exec(`INSERT INTO channels (channel_id, platform, name, url) VALUES ('instagram_followed', 'instagram', 'Followed', 'https://instagram.com/followed')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_followed",
		Platform:    "instagram",
		Handle:      "followed",
		DisplayName: "Followed",
	})

	got, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "instagram_followed" {
		t.Fatalf("expected instagram_followed first, got %q", got)
	}
}

func TestSeedChannelProfileRows(t *testing.T) {
	d := openWritableTestDB(t)

	// Insert a subscribed channel plus feed/retweet identities; seed should
	// create rows for all of them.
	if _, err := d.conn.Exec(`INSERT INTO channels (channel_id, platform, name, url) VALUES ('youtube_UCxx', 'youtube', 'X', 'https://youtube/UCxx')`); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := d.conn.Exec(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('default', 'youtube_UCxx')`); err != nil {
		t.Fatalf("seed follow: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, author_avatar_url, quote_author_handle,
			quote_author_avatar_url, reply_to_handle, content_hash, published_at, fetched_at
		) VALUES
			('tweet_1', 'author_a', 'https://pbs.twimg.com/profile_images/111/avatar-a_normal.jpg', 'quote_b',
			 'https://pbs.twimg.com/profile_images/222/quote-b_normal.jpg', 'reply_parent', 'hash_1', 1, 1)
	`); err != nil {
		t.Fatalf("seed feed_items: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO retweet_sources (
			content_hash, retweeter_handle, retweeter_display_name, tweet_id, published_at
		) VALUES
			('hash_1', 'reposter_c', 'Reposter C', 'rt_1', 1)
	`); err != nil {
		t.Fatalf("seed retweet_sources: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil || n != 5 {
		t.Fatalf("seed: n=%d err=%v", n, err)
	}
	for _, tc := range []struct {
		channelID string
		platform  string
		handle    string
		avatarURL string
	}{
		{channelID: "youtube_UCxx", platform: "youtube", handle: ""},
		{channelID: "twitter_author_a", platform: "twitter", handle: "author_a", avatarURL: "https://pbs.twimg.com/profile_images/111/avatar-a_normal.jpg"},
		{channelID: "twitter_quote_b", platform: "twitter", handle: "quote_b", avatarURL: "https://pbs.twimg.com/profile_images/222/quote-b_normal.jpg"},
		{channelID: "twitter_reply_parent", platform: "twitter", handle: "reply_parent"},
		{channelID: "twitter_reposter_c", platform: "twitter", handle: "reposter_c"},
	} {
		got, _ := d.GetChannelProfile(tc.channelID)
		if got == nil || got.Platform != tc.platform || got.Handle != tc.handle {
			t.Fatalf("seeded row mismatch for %s: %+v", tc.channelID, got)
		}
		if got.AvatarURL != tc.avatarURL {
			t.Fatalf("avatar_url mismatch for %s: got %q want %q", tc.channelID, got.AvatarURL, tc.avatarURL)
		}
	}
}

func TestSeedChannelProfileRowsSeedsShortVideoOwners(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()

	if err := d.InsertVideo(
		"tiktok_video_owner", "tiktok_creator.one", "clip", "",
		0, "", "media/tiktok/creator/video.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert tiktok video: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_video_owner", "instagram_owner.two", "post", "",
		0, "", "media/instagram/owner/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if err := d.InsertVideo(
		"twitter_video_owner", "twitter_OwnerThree", "post", "",
		0, "", "media/twitter/owner/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert twitter video: %v", err)
	}
	if err := d.AddToDownloadQueueWithPublishedAt("queued_short", "tiktok_queue.owner", "queued", now+100); err != nil {
		t.Fatalf("queue short: %v", err)
	}
	if err := d.AddToDownloadQueueWithPublishedAt("queued_internal", "tiktok_7000000000000000001", "internal", now+200); err != nil {
		t.Fatalf("queue internal: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	if n != 4 {
		t.Fatalf("seeded rows = %d, want 4", n)
	}

	for _, tc := range []struct {
		channelID string
		platform  string
		handle    string
	}{
		{channelID: "tiktok_creator.one", platform: "tiktok", handle: "creator.one"},
		{channelID: "instagram_owner.two", platform: "instagram", handle: "owner.two"},
		{channelID: "twitter_ownerthree", platform: "twitter", handle: "ownerthree"},
		{channelID: "tiktok_queue.owner", platform: "tiktok", handle: "queue.owner"},
	} {
		got, err := d.GetChannelProfile(tc.channelID)
		if err != nil || got == nil {
			t.Fatalf("GetChannelProfile(%s): %v / %+v", tc.channelID, err, got)
		}
		if got.Platform != tc.platform || got.Handle != tc.handle {
			t.Fatalf("profile row mismatch for %s: %+v", tc.channelID, got)
		}
	}
	if skipped, _ := d.GetChannelProfile("tiktok_7000000000000000001"); skipped != nil {
		t.Fatalf("numeric TikTok internal id should not be seeded: %+v", skipped)
	}
}

func TestSeedChannelProfileRowsSeedsShortQueueTitleMentions(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()

	if err := d.AddToDownloadQueueWithPublishedAt(
		"queued_mention", "instagram_owner", "new reel with @sample.artist and @other_creator",
		now,
	); err != nil {
		t.Fatalf("queue instagram mention: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	if n != 3 {
		t.Fatalf("seeded rows = %d, want owner plus two mentions", n)
	}
	for _, tc := range []struct {
		channelID string
		handle    string
	}{
		{channelID: "instagram_sample.artist", handle: "sample.artist"},
		{channelID: "instagram_other_creator", handle: "other_creator"},
	} {
		got, err := d.GetChannelProfile(tc.channelID)
		if err != nil || got == nil {
			t.Fatalf("GetChannelProfile(%s): %v / %+v", tc.channelID, err, got)
		}
		if got.Platform != "instagram" || got.Handle != tc.handle {
			t.Fatalf("profile row mismatch for %s: %+v", tc.channelID, got)
		}
	}
}

func TestSeedChannelProfileRowsSeedsShortDescriptionMentions(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()

	if err := d.InsertVideo(
		"tiktok_video_mention", "tiktok_owner", "clip", "watch this with @Guest.One and ignore @1234567890123456",
		0, "", "media/tiktok/owner/video.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert tiktok video: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_video_mention", "instagram_owner", "post", "with @sample.creator plus email a@b.com and trailing @bad.",
		0, "", "media/instagram/owner/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if err := d.InsertVideo(
		"twitter_video_mention", "twitter_owner", "post", "with @Guest_User plus domain @skip.com",
		0, "", "media/twitter/owner/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert twitter video: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	if n != 7 {
		t.Fatalf("seeded rows = %d, want 7", n)
	}

	for _, tc := range []struct {
		channelID string
		platform  string
		handle    string
	}{
		{channelID: "tiktok_owner", platform: "tiktok", handle: "owner"},
		{channelID: "tiktok_guest.one", platform: "tiktok", handle: "guest.one"},
		{channelID: "instagram_owner", platform: "instagram", handle: "owner"},
		{channelID: "instagram_sample.creator", platform: "instagram", handle: "sample.creator"},
		{channelID: "instagram_bad", platform: "instagram", handle: "bad"},
		{channelID: "twitter_owner", platform: "twitter", handle: "owner"},
		{channelID: "twitter_guest_user", platform: "twitter", handle: "guest_user"},
	} {
		got, err := d.GetChannelProfile(tc.channelID)
		if err != nil || got == nil {
			t.Fatalf("GetChannelProfile(%s): %v / %+v", tc.channelID, err, got)
		}
		if got.Platform != tc.platform || got.Handle != tc.handle {
			t.Fatalf("profile row mismatch for %s: %+v", tc.channelID, got)
		}
	}

	if skipped, _ := d.GetChannelProfile("tiktok_1234567890123456"); skipped != nil {
		t.Fatalf("numeric TikTok internal id should not be seeded: %+v", skipped)
	}
	if skipped, _ := d.GetChannelProfile("instagram_b"); skipped != nil {
		t.Fatalf("email domain mention should not be seeded: %+v", skipped)
	}
	if skipped, _ := d.GetChannelProfile("instagram_bad."); skipped != nil {
		t.Fatalf("trailing punctuation should not be part of the seeded handle: %+v", skipped)
	}
	if skipped, _ := d.GetChannelProfile("twitter_skip.com"); skipped != nil {
		t.Fatalf("domain-looking twitter mention should not be seeded: %+v", skipped)
	}

	candidate, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("NextChannelProfileRefreshCandidate: %v", err)
	}
	wantCandidates := map[string]bool{
		"tiktok_owner":             true,
		"tiktok_guest.one":         true,
		"instagram_owner":          true,
		"instagram_sample.creator": true,
		"instagram_bad":            true,
		"twitter_owner":            true,
		"twitter_guest_user":       true,
	}
	if !wantCandidates[candidate] {
		t.Fatalf("refresh candidate = %q, want seeded short-form profile row", candidate)
	}
}

func TestListFeedAvatarProfileIDsIncludesShortDescriptionMentions(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()

	if err := d.InsertVideo(
		"instagram_caption_mentions", "instagram_owner", "caption mentions", "with @rinn_xc and @dear.chuu",
		0, "", "media/instagram/owner/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	found := map[string]bool{
		"instagram_rinn_xc":   false,
		"instagram_dear.chuu": false,
	}
	for _, id := range got {
		if _, ok := found[id]; ok {
			found[id] = true
		}
	}
	for id, seen := range found {
		if !seen {
			t.Fatalf("profile ids = %v, missing caption mention %s", got, id)
		}
	}
}

func TestSeedChannelProfileRowsSeedsProfileBioMentionsByPlatform(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()

	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_source",
		Platform:    "instagram",
		Handle:      "source",
		DisplayName: "Source",
		Bio:         "team @Sample.Creator and @Other_User, email a@b.com",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed instagram profile: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_source",
		Platform:    "twitter",
		Handle:      "source",
		DisplayName: "Source",
		Bio:         "with @Twitter_User and domain @skip.com",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed twitter profile: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	if n != 3 {
		t.Fatalf("seeded rows = %d, want 3", n)
	}

	for _, tc := range []struct {
		channelID string
		platform  string
		handle    string
	}{
		{channelID: "instagram_sample.creator", platform: "instagram", handle: "sample.creator"},
		{channelID: "instagram_other_user", platform: "instagram", handle: "other_user"},
		{channelID: "twitter_twitter_user", platform: "twitter", handle: "twitter_user"},
	} {
		got, err := d.GetChannelProfile(tc.channelID)
		if err != nil || got == nil {
			t.Fatalf("GetChannelProfile(%s): %v / %+v", tc.channelID, err, got)
		}
		if got.Platform != tc.platform || got.Handle != tc.handle {
			t.Fatalf("profile row mismatch for %s: %+v", tc.channelID, got)
		}
	}
	for _, channelID := range []string{"twitter_sample.creator", "twitter_skip.com", "instagram_b"} {
		if skipped, _ := d.GetChannelProfile(channelID); skipped != nil {
			t.Fatalf("non-matching profile bio mention should not be seeded as %s: %+v", channelID, skipped)
		}
	}
}

func TestSeedChannelProfileRowsSeedsTwitterTextMentions(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, body_text, quote_body_text, published_at, fetched_at
		) VALUES (
			'tweet_text_mentions', '',
			'thanks @Mention_A, email a@b.com, domain @skip.com, trailing @Punct.',
			'quote from @Quote_B and repeated @mention_a',
			1, 1
		)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	if n != 3 {
		t.Fatalf("seeded rows = %d, want 3", n)
	}

	for _, handle := range []string{"mention_a", "quote_b", "punct"} {
		channelID := "twitter_" + handle
		got, err := d.GetChannelProfile(channelID)
		if err != nil || got == nil {
			t.Fatalf("GetChannelProfile(%s): %v / %+v", channelID, err, got)
		}
		if got.Platform != "twitter" || got.Handle != handle {
			t.Fatalf("profile row mismatch for %s: %+v", channelID, got)
		}
	}
	for _, channelID := range []string{"twitter_b", "twitter_skip.com"} {
		if skipped, _ := d.GetChannelProfile(channelID); skipped != nil {
			t.Fatalf("non-handle text should not be seeded as %s: %+v", channelID, skipped)
		}
	}
}

func TestSeedChannelProfileRowsBackfillsEmptyAvatarOnly(t *testing.T) {
	d := openWritableTestDB(t)

	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_author_a",
		Platform:  "twitter",
		Handle:    "author_a",
	})
	_ = d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_author_b",
		Platform:  "twitter",
		Handle:    "author_b",
		AvatarURL: "https://pbs.twimg.com/profile_images/222/existing-b_normal.jpg",
	})
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, author_avatar_url, published_at, fetched_at
		) VALUES
			('tweet_a', 'author_a', 'https://pbs.twimg.com/profile_images/333/new-a_normal.jpg', 1, 1),
			('tweet_b', 'author_b', 'https://pbs.twimg.com/profile_images/444/new-b_normal.jpg', 1, 1)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil {
		t.Fatalf("seed channel profile rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected one empty avatar update, got %d", n)
	}
	gotA, _ := d.GetChannelProfile("twitter_author_a")
	if gotA == nil || gotA.AvatarURL != "https://pbs.twimg.com/profile_images/333/new-a_normal.jpg" {
		t.Fatalf("author_a avatar_url not backfilled: %+v", gotA)
	}
	gotB, _ := d.GetChannelProfile("twitter_author_b")
	if gotB == nil || gotB.AvatarURL != "https://pbs.twimg.com/profile_images/222/existing-b_normal.jpg" {
		t.Fatalf("author_b existing avatar_url should be preserved: %+v", gotB)
	}
}

func TestSeedChannelProfileRowsForFeedItemsPrimesBatchBeforeFeedRowsExist(t *testing.T) {
	d := openWritableTestDB(t)

	items := []model.FeedItem{{
		AuthorHandle:           "New_Author",
		AuthorDisplayName:      "New Author",
		QuoteAuthorHandle:      "Quote_User",
		QuoteAuthorDisplayName: "Quote User",
		QuoteAuthorAvatarURL:   "https://pbs.twimg.com/profile_images/444/quote_normal.jpg",
		RetweetedByHandle:      "Retweeter_A",
		RetweetedByDisplayName: "Retweeter A",
		ReplyToHandle:          "Reply_Parent",
		IsRetweet:              true,
		SourceHandle:           "Source_A",
	}}

	n, err := d.SeedChannelProfileRowsForFeedItems(items)
	if err != nil {
		t.Fatalf("SeedChannelProfileRowsForFeedItems: %v", err)
	}
	if n != 5 {
		t.Fatalf("seeded rows = %d, want 5", n)
	}

	author, err := d.GetChannelProfile("twitter_new_author")
	if err != nil {
		t.Fatalf("GetChannelProfile author: %v", err)
	}
	if author == nil || author.Handle != "new_author" || author.DisplayName != "New Author" || author.FetchedAt != nil {
		t.Fatalf("author row not primed as lightweight profile: %+v", author)
	}
	quote, err := d.GetChannelProfile("twitter_quote_user")
	if err != nil {
		t.Fatalf("GetChannelProfile quote: %v", err)
	}
	if quote == nil || quote.DisplayName != "Quote User" || quote.AvatarURL != "https://pbs.twimg.com/profile_images/444/quote_normal.jpg" {
		t.Fatalf("quote row not primed with avatar: %+v", quote)
	}
}

func TestSeedChannelProfileRowsRejectsInvalidTwitterAvatarURLs(t *testing.T) {
	d := openWritableTestDB(t)

	oldFetched := time.Now().Add(-time.Hour).UTC()
	nextRetry := time.Now().Add(time.Hour).UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_quote_b",
		Platform:    "twitter",
		Handle:      "quote_b",
		AvatarURL:   "https://x.com/source/status/undefined",
		FetchedAt:   &oldFetched,
		FailCount:   3,
		NextRetryAt: &nextRetry,
	}); err != nil {
		t.Fatalf("seed poisoned profile: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, author_avatar_url, quote_author_handle,
			quote_author_avatar_url, published_at, fetched_at
		) VALUES
			('tweet_1', 'author_a', 'https://x.com/author_a/status/undefined',
			 'quote_b', 'https://x.com/quote_b/status/undefined', 1, 1)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}

	n, err := d.SeedChannelProfileRows()
	if err != nil {
		t.Fatalf("seed channel profile rows: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected insert plus poisoned-row repair, got %d", n)
	}

	author, _ := d.GetChannelProfile("twitter_author_a")
	if author == nil {
		t.Fatal("expected author profile row")
	}
	if author.AvatarURL != "" {
		t.Fatalf("invalid author avatar URL should not be seeded, got %q", author.AvatarURL)
	}
	quote, _ := d.GetChannelProfile("twitter_quote_b")
	if quote == nil {
		t.Fatal("expected quote profile row")
	}
	if quote.AvatarURL != "" {
		t.Fatalf("poisoned quote avatar URL should be cleared, got %q", quote.AvatarURL)
	}
	if quote.FetchedAt != nil || quote.FailCount != 0 || quote.NextRetryAt != nil {
		t.Fatalf("poisoned quote retry state should be reset: %+v", quote)
	}
}

func TestListQuoteAvatarProfileIDs(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, quote_author_handle, quote_author_avatar_url,
			published_at, fetched_at
		) VALUES
			('tweet_1', 'author_a', 'quote_a', '', 1, 1),
			('tweet_2', 'author_b', '', 'https://pbs.twimg.com/profile_images/777/photo.jpg', 1, 1)
	`); err != nil {
		t.Fatalf("seed feed_items: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	if _, err := d.SeedSyntheticTwitterAvatarProfiles(); err != nil {
		t.Fatalf("SeedSyntheticTwitterAvatarProfiles: %v", err)
	}

	got, err := d.ListQuoteAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListQuoteAvatarProfileIDs: %v", err)
	}
	wantSynthetic := model.SyntheticTwitterAvatarChannelID("https://pbs.twimg.com/profile_images/777/photo.jpg")
	want := map[string]bool{
		"twitter_quote_a": true,
		wantSynthetic:     true,
	}
	if len(got) != len(want) {
		t.Fatalf("quote avatar profile ids = %v, want %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected quote avatar profile id %q in %v", id, got)
		}
	}
}

func TestListFeedAvatarProfileIDsIncludesThreadAndRepostIdentities(t *testing.T) {
	d := openWritableTestDB(t)
	avatarURL := "https://pbs.twimg.com/profile_images/777/photo.jpg"
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, quote_author_handle,
			quote_author_avatar_url, reply_to_handle, retweeted_by_handle,
			is_retweet, content_hash, published_at, fetched_at
		) VALUES
			('tweet_1', 'source_rt', 'author_a', 'quote_a', '', 'reply_parent', 'legacy_rt', 1, 'hash_1', 1, 1),
			('tweet_2', '', 'author_b', '', ?, '', '', 0, 'hash_2', 2, 2)
	`, avatarURL); err != nil {
		t.Fatalf("seed feed_items: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO retweet_sources (
			content_hash, retweeter_handle, retweeter_display_name, tweet_id, published_at
		) VALUES ('hash_1', 'reposter_c', 'Reposter C', 'tweet_1', 1)
	`); err != nil {
		t.Fatalf("seed retweet_sources: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	if _, err := d.SeedSyntheticTwitterAvatarProfiles(); err != nil {
		t.Fatalf("SeedSyntheticTwitterAvatarProfiles: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO video_comments (
			video_id, comment_id, author_name, author_id, author_thumbnail, text, published_at, platform, fetched_at
		) VALUES ('video_1', 'comment_1', 'Video Commenter', 'UCcommenter123', 'https://youtube.example/avatar.jpg', 'hello', 1, 'youtube', 1)
	`); err != nil {
		t.Fatalf("seed video comments: %v", err)
	}
	if _, err := d.SeedYouTubeCommentAuthorProfiles(); err != nil {
		t.Fatalf("SeedYouTubeCommentAuthorProfiles: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	wantSynthetic := model.SyntheticTwitterAvatarChannelID(avatarURL)
	want := map[string]bool{
		"twitter_author_a":       true,
		"twitter_author_b":       true,
		"twitter_quote_a":        true,
		"twitter_reply_parent":   true,
		"twitter_legacy_rt":      true,
		"twitter_source_rt":      true,
		"twitter_reposter_c":     true,
		wantSynthetic:            true,
		"youtube_UCcommenter123": true,
	}
	if len(got) != len(want) {
		t.Fatalf("feed avatar profile ids = %v, want %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected feed avatar profile id %q in %v", id, got)
		}
	}
}

func TestListFeedAvatarProfileIDsIncludesShortFormSourceWindowOwners(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()

	if err := d.InsertVideo(
		"tiktok_repost_video", "tiktok_repost.owner", "repost", "",
		0, "", "media/tiktok/repost/video.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert tiktok video: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_tagged_video", "instagram_tagged.owner", "tagged", "",
		0, "", "media/instagram/tagged/post.mp4", 0,
		now+10, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if err := d.InsertVideo(
		"twitter_clip_video", "twitter_clip_owner", "clip", "",
		0, "", "media/twitter/clip/video.mp4", 0,
		now+15, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert twitter video: %v", err)
	}
	if err := d.AddToDownloadQueueWithPublishedAt("queued_owner_video", "tiktok_queued.owner", "queued", now+20); err != nil {
		t.Fatalf("queue short owner: %v", err)
	}
	if err := d.AddToDownloadQueueWithPublishedAt("queued_twitter_video", "twitter_queued_owner", "queued", now+25); err != nil {
		t.Fatalf("queue twitter owner: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{
		{VideoID: "tiktok_repost_video", ReposterChannelID: "tiktok_followed", ReposterHandle: "followed", FirstSeenAtMs: now + 30},
		{VideoID: "instagram_tagged_video", ReposterChannelID: "instagram_followed", ReposterHandle: "followed", FirstSeenAtMs: now + 40},
	}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	want := map[string]bool{
		"tiktok_repost.owner":    true,
		"instagram_tagged.owner": true,
		"twitter_clip_owner":     true,
		"tiktok_queued.owner":    true,
		"twitter_queued_owner":   true,
	}
	for id := range want {
		want[id] = false
	}
	for _, id := range got {
		if _, ok := want[id]; ok {
			want[id] = true
		}
	}
	for id, seen := range want {
		if !seen {
			t.Fatalf("feed avatar profile ids = %v, missing %s", got, id)
		}
	}
}

func TestListFeedAvatarProfileIDsIncludesUnfetchedInstagramSourceWindowOwners(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()

	if err := d.InsertVideo(
		"instagram_source_video", "instagram_source.owner", "source window", "",
		0, "", "media/instagram/source/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:           "instagram_source_video",
		ReposterChannelID: "instagram_followed",
		ReposterHandle:    "followed",
		FirstSeenAtMs:     now,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES ('tweet_new', 'new_author', ?, ?)
	`, now+10000, now+10000); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	found := false
	for _, id := range got {
		if id == "instagram_source.owner" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("profile ids = %v, missing instagram_source.owner", got)
	}
}

func TestListFeedAvatarProfileIDsIncludesInstagramSourceWindowAvatarCaching(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := time.Now().UnixMilli()

	if err := d.InsertVideo(
		"instagram_cached_source_video", "instagram_cached.source", "source window", "",
		0, "", "media/instagram/source/post.mp4", 0,
		nowMs, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:           "instagram_cached_source_video",
		ReposterChannelID: "instagram_followed",
		ReposterHandle:    "followed",
		FirstSeenAtMs:     nowMs,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES ('tweet_new', 'new_author', ?, ?)
	`, nowMs+10000, nowMs+10000); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cached.source",
		Platform:    "instagram",
		Handle:      "cached.source",
		DisplayName: "Cached Source",
		AvatarURL:   "https://cdn.example/avatar.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed fetched profile: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	found := false
	for _, id := range got {
		if id == "instagram_cached.source" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("profile ids = %v, missing instagram_cached.source", got)
	}
}

func TestListFeedAvatarProfileIDsIncludesRecentUnfetchedRows(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES
			('tweet_old', 'old_author', 100, 100),
			('tweet_new', 'new_author', 200, 200)
	`); err != nil {
		t.Fatalf("seed feed_items: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	found := map[string]bool{
		"twitter_old_author": false,
		"twitter_new_author": false,
	}
	for _, id := range got {
		if _, ok := found[id]; ok {
			found[id] = true
		}
	}
	for id, seen := range found {
		if !seen {
			t.Fatalf("profile ids = %v, missing %s", got, id)
		}
	}
}

func TestListFeedAvatarProfileIDsIncludesRecentRowsAndOldBacklog(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES
			('tweet_old', 'old_backlog', 100, 100),
			('tweet_new', 'new_seen', 200, 200)
	`); err != nil {
		t.Fatalf("seed feed_items: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_new_seen",
		Platform:    "twitter",
		Handle:      "new_seen",
		DisplayName: "New Seen",
		FetchedAt:   &now,
		BannerURL:   "https://cdn.example/banner.jpg",
	}); err != nil {
		t.Fatalf("seed fetched profile: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	found := map[string]bool{
		"twitter_old_backlog": false,
		"twitter_new_seen":    false,
	}
	for _, id := range got {
		if _, ok := found[id]; ok {
			found[id] = true
		}
	}
	for id, seen := range found {
		if !seen {
			t.Fatalf("profile ids = %v, missing %s", got, id)
		}
	}
}

func TestListFeedAvatarProfileIDsUsesStableChannelOrder(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_zed",
		Platform:  "twitter",
		Handle:    "zed",
	}); err != nil {
		t.Fatalf("seed zed profile: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_alpha",
		Platform:  "twitter",
		Handle:    "alpha",
		FetchedAt: &now,
	}); err != nil {
		t.Fatalf("seed alpha profile: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	want := []string{"twitter_alpha", "twitter_zed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile ids = %v, want stable channel-id order %v", got, want)
	}
}

func TestListFeedAvatarProfileIDsIncludesFreshStoredAvatarRecovery(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES
			('tweet_older_avatar', 'cryptokid', 100, 100),
			('tweet_newer_backlog', 'new_backlog', 300, 300)
	`); err != nil {
		t.Fatalf("seed feed_items: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_cryptokid",
		Platform:    "twitter",
		Handle:      "cryptokid",
		DisplayName: "Crypto Kid",
		AvatarURL:   "https://pbs.twimg.com/profile_images/1998822702501838848/OUPVuvCJ_normal.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed fetched profile: %v", err)
	}

	got, err := d.ListFeedAvatarProfileIDs()
	if err != nil {
		t.Fatalf("ListFeedAvatarProfileIDs: %v", err)
	}
	found := false
	for _, id := range got {
		if id == "twitter_cryptokid" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("profile ids = %v, missing twitter_cryptokid", got)
	}
}

func TestSeedYouTubeCommentAuthorProfiles(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.conn.Exec(`
		INSERT INTO video_comments (
			video_id, comment_id, author_name, author_id, author_thumbnail, text, published_at, platform, fetched_at
		) VALUES
			('video_1', 'comment_1', 'Video Commenter', 'UCcommenter123', 'https://youtube.example/avatar.jpg', 'hello', 1, 'youtube', 1),
			('video_1', 'comment_2', 'Handle Only', '@handle', 'https://youtube.example/skip.jpg', 'skip', 1, 'youtube', 1)
	`); err != nil {
		t.Fatalf("seed video comments: %v", err)
	}

	n, err := d.SeedYouTubeCommentAuthorProfiles()
	if err != nil {
		t.Fatalf("SeedYouTubeCommentAuthorProfiles: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected one seeded commenter profile, got %d", n)
	}
	got, err := d.GetChannelProfile("youtube_UCcommenter123")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if got == nil {
		t.Fatal("expected youtube commenter profile")
	}
	if got.Platform != "youtube" || got.Handle != "UCcommenter123" || got.DisplayName != "Video Commenter" {
		t.Fatalf("seeded profile mismatch: %+v", got)
	}
	if got.AvatarURL != "https://youtube.example/avatar.jpg" {
		t.Fatalf("avatar_url = %q", got.AvatarURL)
	}
	if skipped, _ := d.GetChannelProfile("youtube_@handle"); skipped != nil {
		t.Fatalf("handle-only author id should not seed profile: %+v", skipped)
	}
}

func TestSeedSyntheticTwitterAvatarProfiles(t *testing.T) {
	d := openWritableTestDB(t)
	avatarURL := "https://pbs.twimg.com/profile_images/777/photo.jpg"
	if _, err := d.conn.Exec(`
		INSERT INTO feed_items (tweet_id, author_handle, quote_author_avatar_url, published_at, fetched_at)
		VALUES ('tweet_1', 'author_a', ?, 1, 1)
	`, avatarURL); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}

	n, err := d.SeedSyntheticTwitterAvatarProfiles()
	if err != nil {
		t.Fatalf("seed synthetic avatars: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 inserted row, got %d", n)
	}

	channelID := model.SyntheticTwitterAvatarChannelID(avatarURL)
	got, err := d.GetChannelProfile(channelID)
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if got == nil {
		t.Fatal("expected synthetic avatar profile row")
	}
	if got.Platform != "twitter" {
		t.Fatalf("platform = %q, want twitter", got.Platform)
	}
	if got.AvatarURL != avatarURL {
		t.Fatalf("avatar_url = %q, want %q", got.AvatarURL, avatarURL)
	}
	if got.Handle != "" {
		t.Fatalf("handle = %q, want empty for synthetic row", got.Handle)
	}

	n, err = d.SeedSyntheticTwitterAvatarProfiles()
	if err != nil {
		t.Fatalf("seed synthetic avatars second pass: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected second pass to insert 0 rows, got %d", n)
	}
}
