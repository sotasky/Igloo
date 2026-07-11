package db

import (
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
	if _, ok := sets.Videos["sample_old_video"]; ok {
		t.Fatalf("old unprotected video remained in content: %+v", sets.SortedVideos())
	}
	if _, ok := sets.MediaVideos["sample_old_video"]; ok {
		t.Fatalf("old unprotected video remained in media: %+v", sets.SortedMediaVideos())
	}
	if _, ok := sets.MediaVideos["sample_saved_video"]; !ok {
		t.Fatalf("saved video missing from media: %+v", sets.SortedMediaVideos())
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

func TestListAndroidSyncFeedRankRowsKeepsOnlyDesiredVisibleItems(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, published_at, fetched_at, is_ghost) VALUES
			('sample_visible', 1, 1, 0),
			('sample_other', 1, 1, 0),
			('sample_seen', 1, 1, 0),
			('sample_ghost', 1, 1, 1);
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
