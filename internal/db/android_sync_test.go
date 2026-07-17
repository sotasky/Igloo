package db

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestListAndroidSyncDesiredSetsIncludesOwnersAndExcludesCommentAuthors(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 24 * time.Hour / time.Millisecond)
	recent := nowMs - int64(24*time.Hour/time.Millisecond)
	old := nowMs - int64(5*24*time.Hour/time.Millisecond)
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle) VALUES
			('twitter_sample_author', 'twitter', 'sample_author'),
			('twitter_sample_source', 'twitter', 'sample_source'),
			('twitter_sample_quote', 'twitter', 'sample_quote'),
			('twitter_sample_reply', 'twitter', 'sample_reply'),
			('twitter_sample_reposter', 'twitter', 'sample_reposter'),
			('twitter_sample_retweeter', 'twitter', 'sample_retweeter'),
			('youtube_sample_followed', 'youtube', 'sample_followed');
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('youtube_sample_followed', 'sample_followed', 'Sample Followed', 'youtube');
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('youtube_sample_followed', 1);
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, quote_tweet_id, quote_channel_id,
			reply_channel_id, reposter_channel_id, content_hash, published_at, fetched_at
		) VALUES (
			'sample_tweet', 'twitter_sample_source', 'twitter_sample_author',
			'sample_quote', 'twitter_sample_quote', 'twitter_sample_reply',
			'twitter_sample_reposter', 'sample_hash', ?, ?
		);
		INSERT INTO retweet_sources (
			content_hash, retweeter_channel_id, tweet_id, published_at
		) VALUES ('sample_hash', 'twitter_sample_retweeter', 'sample_tweet', ?);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at) VALUES
			('sample_video', 'youtube_sample_followed', 'youtube_video', 'Sample', ?),
			('sample_old_video', 'youtube_sample_followed', 'youtube_video', 'Sample Old', ?),
			('sample_saved_video', 'youtube_sample_followed', 'youtube_video', 'Sample Saved', ?);
		INSERT INTO bookmarks (video_id, bookmarked_at) VALUES ('sample_saved_video', ?);
		INSERT INTO video_comments (video_id, comment_id, author_id, author_name, published_at)
		VALUES ('sample_video', 'sample_comment', 'comment_author', 'Comment Author', ?);
	`, recent, recent, recent, recent, old, old, recent, recent); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		UPDATE videos SET published_at = ?
		WHERE video_id IN ('sample_old_video', 'sample_saved_video')
	`, old); err != nil {
		t.Fatal(err)
	}
	for _, videoID := range []string{"sample_video", "sample_old_video"} {
		publishAssetMetadataForTest(t, d, Asset{
			AssetID: "asset_" + videoID, AssetKind: "video_stream",
			OwnerKind: "youtube_video", OwnerID: videoID,
			FilePath: "media/test/" + videoID + ".mp4", ContentType: "video/mp4",
		}, nowMs)
	}

	sets, err := d.ListAndroidSyncDesiredSets(AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 3}, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sets.Tweets["sample_tweet"]; !ok {
		t.Fatalf("feed item missing: %+v", sets.SortedTweets())
	}
	if _, ok := sets.TweetAssetOwners["sample_quote"]; !ok {
		t.Fatalf("quote asset owner missing: %+v", sets.SortedTweetAssetOwners())
	}
	if _, ok := sets.Videos["sample_video"]; !ok {
		t.Fatalf("video missing: %+v", sets.SortedVideos())
	}
	if _, ok := sets.Videos["sample_old_video"]; !ok {
		t.Fatalf("old library video missing from content: %+v", sets.SortedVideos())
	}
	if _, ok := sets.MediaVideos["sample_old_video"]; ok {
		t.Fatalf("old unprotected video remained in media: %+v", sets.SortedMediaVideos())
	}
	if _, ok := sets.Videos["sample_saved_video"]; !ok {
		t.Fatalf("saved video missing from metadata: %+v", sets.SortedVideos())
	}
	if _, ok := sets.MediaVideos["sample_saved_video"]; ok {
		t.Fatalf("saved old video remained in automatic media: %+v", sets.SortedMediaVideos())
	}
	for _, id := range []string{
		"twitter_sample_author",
		"twitter_sample_source",
		"twitter_sample_quote",
		"twitter_sample_reply",
		"twitter_sample_reposter",
		"twitter_sample_retweeter",
		"youtube_sample_followed",
	} {
		if _, ok := sets.Channels[id]; !ok {
			t.Fatalf("channel %s missing: %+v", id, sets.SortedChannels())
		}
	}
	if _, ok := sets.Channels["youtube_comment_author"]; ok {
		t.Fatalf("comment author incorrectly became a channel: %+v", sets.SortedChannels())
	}
}

func TestListAndroidSyncDesiredFeedAssetOwnersSharesEffectiveRecencyAndOffSemantics(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(20 * 24 * time.Hour / time.Millisecond)
	recent := nowMs - time.Hour.Milliseconds()
	old := nowMs - 10*24*time.Hour.Milliseconds()
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, channel_id, content_hash, published_at, fetched_at) VALUES
			('sample_recent_direct', 'twitter_sample', 'hash_direct', ?, ?),
			('sample_recent_repost_copy', 'twitter_sample', 'hash_repost', ?, ?),
			('sample_saved_old', 'twitter_sample', 'hash_saved', ?, ?);
		INSERT INTO retweet_sources (content_hash, retweeter_channel_id, tweet_id, published_at)
		VALUES ('hash_repost', 'twitter_sample_reposter', 'sample_recent_repost_copy', ?);
		INSERT INTO bookmarks (video_id, bookmarked_at) VALUES ('sample_saved_old', ?);
	`, recent, recent, old, old, old, old, recent, recent); err != nil {
		t.Fatal(err)
	}

	retained, err := d.ListAndroidSyncDesiredFeedAssetOwners(1, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	retainedSet := stringSet(retained)
	for _, ownerID := range []string{"sample_recent_direct", "sample_recent_repost_copy", "sample_saved_old"} {
		if _, ok := retainedSet[ownerID]; !ok {
			t.Fatalf("effective retained owner %s missing from %v", ownerID, retained)
		}
	}

	off, err := d.ListAndroidSyncDesiredFeedAssetOwners(0, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	offSet := stringSet(off)
	for _, ownerID := range []string{"sample_recent_direct", "sample_recent_repost_copy"} {
		if _, ok := offSet[ownerID]; ok {
			t.Fatalf("FeedDays=0 retained ordinary feed owner %s in %v", ownerID, off)
		}
	}
	if _, ok := offSet["sample_saved_old"]; !ok {
		t.Fatalf("FeedDays=0 dropped protected owner: %v", off)
	}
}

func TestListAndroidSyncDesiredFeedAssetOwnersAmongMatchesCanonicalSelection(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(30 * 24 * time.Hour / time.Millisecond)
	recent := nowMs - time.Hour.Milliseconds()
	old := nowMs - 10*24*time.Hour.Milliseconds()
	if err := d.ExecRaw(`
		INSERT INTO channel_settings (channel_id, include_reposts, updated_at)
		VALUES ('twitter_sample_source', 0, 1);
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, is_retweet, content_hash,
			quote_tweet_id, reply_to_status, published_at, fetched_at
		) VALUES
			('sample_recent_direct', '', 'twitter_sample', 0, 'hash_direct', '', '', ?, ?),
			('sample_old_hash_peer', '', 'twitter_sample', 0, 'hash_direct', '', '', ?, ?),
			('sample_old_repost_hash', '', 'twitter_sample', 0, 'hash_repost', '', '', ?, ?),
			('sample_recent_quote_asset_parent', '', 'twitter_sample', 0, '', 'sample_asset_only_quote', '', ?, ?),
			('sample_recent_quote_hash_parent', '', 'twitter_sample', 0, '', 'sample_quote_target', '', ?, ?),
			('sample_quote_target', '', 'twitter_sample', 0, 'hash_quote', '', '', ?, ?),
			('sample_quote_hash_peer', '', 'twitter_sample', 0, 'hash_quote', '', '', ?, ?),
			('sample_old_reply_parent', '', 'twitter_sample', 0, '', '', '', ?, ?),
			('sample_recent_reply_child', '', 'twitter_sample', 0, '', '', 'sample_old_reply_parent', ?, ?),
			('sample_saved_old', '', 'twitter_sample', 0, 'hash_saved', '', '', ?, ?),
			('sample_saved_hash_peer', '', 'twitter_sample', 0, 'hash_saved', '', '', ?, ?),
			('sample_hidden_parent', '', 'twitter_sample', 0, '', '', '', ?, ?),
			('sample_hidden_recent', 'twitter_sample_source', 'twitter_sample_other', 1, 'hash_hidden', '', 'sample_hidden_parent', ?, ?),
			('sample_decoy_old', '', 'twitter_sample', 0, 'hash_decoy', '', '', ?, ?);
		INSERT INTO retweet_sources (content_hash, retweeter_channel_id, tweet_id, published_at)
		VALUES ('hash_repost', 'twitter_sample_reposter', 'sample_old_repost_hash', ?);
		INSERT INTO bookmarks (video_id, bookmarked_at) VALUES ('sample_saved_old', ?)
	`,
		recent, recent,
		old, old,
		old, old,
		recent, recent,
		recent, recent,
		old, old,
		old, old,
		old, old,
		recent, recent,
		old, old,
		old, old,
		old, old,
		recent, recent,
		old, old,
		recent, recent,
	); err != nil {
		t.Fatal(err)
	}
	candidates := []string{
		"sample_recent_direct", "sample_old_hash_peer", "sample_old_repost_hash",
		"sample_recent_quote_asset_parent", "sample_asset_only_quote",
		"sample_recent_quote_hash_parent", "sample_quote_target", "sample_quote_hash_peer",
		"sample_old_reply_parent", "sample_recent_reply_child",
		"sample_saved_old", "sample_saved_hash_peer",
		"sample_hidden_parent", "sample_hidden_recent", "sample_decoy_old", "sample_missing",
	}
	for _, feedDays := range []int{1, 0} {
		full, err := d.ListAndroidSyncDesiredFeedAssetOwners(feedDays, nowMs)
		if err != nil {
			t.Fatal(err)
		}
		fullSet := stringSet(full)
		var want []string
		for _, id := range candidates {
			if _, ok := fullSet[id]; ok {
				want = append(want, id)
			}
		}
		got, err := d.ListAndroidSyncDesiredFeedAssetOwnersAmong(feedDays, nowMs, candidates)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, ",") != strings.Join(uniqueSortedStrings(want), ",") {
			t.Fatalf("FeedDays=%d candidate owners = %v, want %v from full %v", feedDays, got, uniqueSortedStrings(want), full)
		}
	}
}

func TestListAndroidSyncDesiredContentAmongMatchesCanonicalVideoSelection(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(30 * 24 * time.Hour / time.Millisecond)
	recent := nowMs - time.Hour.Milliseconds()
	old := nowMs - 10*24*time.Hour.Milliseconds()
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('youtube_sample', 'sample', 'Sample', 'youtube'),
			('tiktok_sample', 'sample', 'Sample', 'tiktok'),
			('instagram_sample', 'sample', 'Sample', 'instagram'),
			('tiktok_reposter', 'reposter', 'Reposter', 'tiktok');
		INSERT INTO channel_follows (channel_id, followed_at) VALUES
			('youtube_sample', 1), ('tiktok_sample', 1),
			('instagram_sample', 1), ('tiktok_reposter', 1);
		INSERT INTO videos (video_id, channel_id, owner_kind, source_kind, published_at) VALUES
			('sample_youtube_recent', 'youtube_sample', 'youtube_video', '', ?),
			('sample_youtube_old', 'youtube_sample', 'youtube_video', '', ?),
			('sample_youtube_saved', 'youtube_sample', 'youtube_video', '', ?),
			('sample_moment_recent', 'tiktok_sample', 'tiktok_video', '', ?),
			('sample_moment_reposted', 'tiktok_sample', 'tiktok_video', '', ?),
			('instagram_story_123', 'instagram_sample', 'instagram_reel', 'story', ?),
			('instagram_story_invalid', 'instagram_sample', 'instagram_reel', 'story', ?);
		INSERT INTO bookmarks (video_id, bookmarked_at) VALUES ('sample_youtube_saved', ?);
		INSERT INTO video_desires (source_channel_id, source_component, video_id, source_position, lane) VALUES
			('youtube_sample', 'uploads', 'sample_youtube_recent', 1, 'current'),
			('youtube_sample', 'uploads', 'sample_youtube_old', 2, 'current');
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES ('sample_moment_reposted', 'tiktok_reposter', ?, ?, ?)
	`, recent, old, old, recent, old, recent, recent, recent, recent, recent, recent); err != nil {
		t.Fatal(err)
	}

	settings := AndroidRetentionSettings{FeedDays: 1, YoutubeDays: 1, MomentsDays: 1, StoryHours: 48}
	full, err := d.ListAndroidSyncDesiredContent(settings, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{
		"sample_youtube_recent", "sample_youtube_old", "sample_youtube_saved",
		"sample_moment_recent", "sample_moment_reposted",
		"instagram_story_123", "instagram_story_invalid", "sample_missing",
	}
	selected, err := d.ListAndroidSyncDesiredContentAmong(settings, nowMs, nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, id := range candidates {
		if _, ok := full.Videos[id]; ok {
			want = append(want, id)
		}
	}
	if got := selected.SortedVideos(); strings.Join(got, ",") != strings.Join(uniqueSortedStrings(want), ",") {
		t.Fatalf("candidate videos = %v, want %v from full %v", got, uniqueSortedStrings(want), full.SortedVideos())
	}
}

func TestListAndroidSyncDesiredSetsMatchesWebYoutubeLibrary(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(30 * 24 * time.Hour / time.Millisecond)
	old := nowMs - 10*24*time.Hour.Milliseconds()
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube'),
			('youtube_sample_second', 'sample_second', 'Sample Second', 'youtube'),
			('stale_platform_channel', 'sample_stale', 'Stale Platform', 'twitter'),
			('tiktok_sample_channel', 'sample_channel', 'Sample Channel', 'tiktok');
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('youtube_sample_channel', 1);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, source_kind, published_at) VALUES
			('sample_ready_tracked', 'youtube_sample_channel', 'youtube_video', 'Tracked', '', ?),
			('sample_ready_untracked', 'youtube_sample_second', 'youtube_video', 'Untracked', '', ?),
			('sample_ready_stale_platform', 'stale_platform_channel', 'youtube_video', 'Stale Platform', '', ?),
			('sample_ready_story', 'youtube_sample_channel', 'youtube_video', 'Story', 'story', ?),
			('sample_unready', 'youtube_sample_channel', 'youtube_video', 'Unready', '', ?),
			('sample_other_platform', 'tiktok_sample_channel', 'tiktok_video', 'Other', '', ?);
	`, old, old, old, old, old, old); err != nil {
		t.Fatal(err)
	}
	for _, videoID := range []string{
		"sample_ready_tracked", "sample_ready_untracked", "sample_ready_stale_platform", "sample_ready_story", "sample_other_platform",
	} {
		ownerKind := "youtube_video"
		if videoID == "sample_other_platform" {
			ownerKind = "tiktok_video"
		}
		publishAssetMetadataForTest(t, d, Asset{
			AssetID: "asset_" + videoID, AssetKind: "video_stream",
			OwnerKind: ownerKind, OwnerID: videoID,
			FilePath: "media/test/" + videoID + ".mp4", ContentType: "video/mp4",
		}, nowMs)
	}

	webVideos, err := d.GetVideos(GetVideosOpts{Platform: "youtube", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(webVideos))
	for _, video := range webVideos {
		want = append(want, video.VideoID)
	}
	want = uniqueSortedStrings(want)

	settings := AndroidRetentionSettings{FeedDays: 1, YoutubeDays: 1, MomentsDays: 1, StoryHours: 48}
	full, err := d.ListAndroidSyncDesiredContent(settings, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if got := full.SortedVideos(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Android YouTube library = %v, want web Videos library %v", got, want)
	}

	candidates := []string{
		"sample_ready_tracked", "sample_ready_untracked", "sample_ready_stale_platform", "sample_ready_story", "sample_unready", "sample_other_platform",
	}
	incremental, err := d.ListAndroidSyncDesiredContentAmong(settings, nowMs, nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if got := incremental.SortedVideos(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("incremental Android YouTube library = %v, want web Videos library %v", got, want)
	}
}

func TestListAndroidSyncDesiredContentForModeKeepsLegacyYoutubeWindow(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(30 * 24 * time.Hour / time.Millisecond)
	recent := nowMs - time.Hour.Milliseconds()
	old := nowMs - 10*24*time.Hour.Milliseconds()
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('youtube_sample_followed', 'sample_followed', 'Sample Followed', 'youtube'),
			('youtube_sample_unfollowed', 'sample_unfollowed', 'Sample Unfollowed', 'youtube');
		INSERT INTO channel_follows (channel_id, followed_at) VALUES
			('youtube_sample_followed', 1);
		INSERT INTO videos (video_id, channel_id, owner_kind, source_kind, published_at, is_temp) VALUES
			('sample_recent_followed', 'youtube_sample_followed', 'youtube_video', '', ?, 0),
			('sample_old_followed', 'youtube_sample_followed', 'youtube_video', '', ?, 0),
			('sample_old_unfollowed', 'youtube_sample_unfollowed', 'youtube_video', '', ?, 0),
			('sample_saved_old', 'youtube_sample_followed', 'youtube_video', '', ?, 0),
			('sample_temporary_unfollowed', 'youtube_sample_unfollowed', 'youtube_video', '', ?, 1),
			('sample_unready_recent', 'youtube_sample_followed', 'youtube_video', '', ?, 0);
		INSERT INTO bookmarks (video_id, bookmarked_at) VALUES ('sample_saved_old', ?);
	`, recent, old, old, old, recent, recent, old); err != nil {
		t.Fatal(err)
	}
	for _, videoID := range []string{
		"sample_recent_followed",
		"sample_old_followed",
		"sample_old_unfollowed",
		"sample_saved_old",
	} {
		publishAssetMetadataForTest(t, d, Asset{
			AssetID: "asset_" + videoID, AssetKind: "video_stream",
			OwnerKind: "youtube_video", OwnerID: videoID,
			FilePath: "media/test/" + videoID + ".mp4", ContentType: "video/mp4",
		}, nowMs)
	}

	settings := AndroidRetentionSettings{FeedDays: 1, YoutubeDays: 1, MomentsDays: 1, StoryHours: 48}
	full, err := d.ListAndroidSyncDesiredContentForMode(settings, nowMs, true)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := d.ListAndroidSyncDesiredContentForMode(settings, nowMs, false)
	if err != nil {
		t.Fatal(err)
	}

	assertIDs := func(label string, got []string, want ...string) {
		t.Helper()
		if joinedGot, joinedWant := strings.Join(got, ","), strings.Join(uniqueSortedStrings(want), ","); joinedGot != joinedWant {
			t.Fatalf("%s = %v, want %v", label, got, uniqueSortedStrings(want))
		}
	}
	assertIDs("full videos", full.SortedVideos(),
		"sample_old_followed",
		"sample_old_unfollowed",
		"sample_recent_followed",
		"sample_saved_old",
		"sample_temporary_unfollowed",
	)
	assertIDs("full media videos", full.SortedMediaVideos(), "sample_recent_followed")
	assertIDs("legacy videos", legacy.SortedVideos(),
		"sample_recent_followed",
		"sample_saved_old",
		"sample_unready_recent",
	)
	assertIDs("legacy media videos", legacy.SortedMediaVideos(),
		"sample_recent_followed",
		"sample_saved_old",
		"sample_unready_recent",
	)
	legacySets, err := d.ListAndroidSyncDesiredSetsForMode(settings, nowMs, false)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs("legacy set videos", legacySets.SortedVideos(), legacy.SortedVideos()...)

	candidates := []string{
		"sample_recent_followed",
		"sample_old_followed",
		"sample_old_unfollowed",
		"sample_saved_old",
		"sample_temporary_unfollowed",
		"sample_unready_recent",
	}
	fullIncremental, err := d.ListAndroidSyncDesiredContentAmongForMode(settings, nowMs, nil, candidates, true)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs("full incremental videos", fullIncremental.SortedVideos(), full.SortedVideos()...)
	assertIDs("full incremental media videos", fullIncremental.SortedMediaVideos(), full.SortedMediaVideos()...)

	legacyIncremental, err := d.ListAndroidSyncDesiredContentAmongForMode(settings, nowMs, nil, candidates, false)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs("legacy incremental videos", legacyIncremental.SortedVideos(), legacy.SortedVideos()...)
	assertIDs("legacy incremental media videos", legacyIncremental.SortedMediaVideos(), legacy.SortedMediaVideos()...)
}

func uniqueSortedStrings(values []string) []string {
	out := uniqueStrings(values)
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func TestListAndroidSyncFeedRankRowsKeepsOnlyDesiredVisibleItems(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at) VALUES ('twitter_sample_source', 1);
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, published_at, fetched_at, is_ghost) VALUES
			('sample_visible', 'twitter_sample_source', 'twitter_sample_source', 1, 1, 0),
			('sample_other', 'twitter_sample_source', 'twitter_sample_source', 1, 1, 0),
			('sample_seen', 'twitter_sample_source', 'twitter_sample_source', 1, 1, 0),
			('sample_ghost', 'twitter_sample_source', 'twitter_sample_source', 1, 1, 1);
		INSERT INTO feed_seen (tweet_id, seen_at) VALUES ('sample_seen', 1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "sample_visible", RankPosition: 1},
		{TweetID: "sample_other", RankPosition: 2},
		{TweetID: "sample_seen", RankPosition: 3},
		{TweetID: "sample_ghost", RankPosition: 4},
	}); err != nil {
		t.Fatal(err)
	}

	snapshotAt, rows, err := d.ListAndroidSyncFeedRankRows(
		[]string{"sample_visible", "sample_seen", "sample_ghost"},
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotAt == 0 || len(rows) != 1 || rows[0].TweetID != "sample_visible" || rows[0].RankPosition != 1 {
		t.Fatalf("snapshot_at=%d rows=%+v", snapshotAt, rows)
	}
}
