package db

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestLikeMutationRequeuesPrunedDirectAndQuoteAssets(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.RecordAndroidFeedRetention(0, 1); err != nil {
		t.Fatal(err)
	}
	item := model.FeedItem{
		TweetID: "sample_liked_parent", SourceHandle: "sample_source", AuthorHandle: "sample_author",
		MediaJSON:      `[{"url":"https://cdn.example/direct.jpg","type":"photo"}]`,
		QuoteTweetID:   "sample_liked_quote",
		QuoteMediaJSON: `[{"url":"https://cdn.example/quote.jpg","type":"photo"}]`,
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if _, err := d.markXContentAssetsPruned([]string{item.TweetID, item.QuoteTweetID}, 1000); err != nil {
		t.Fatalf("mark assets pruned: %v", err)
	}

	if err := d.ApplyLikeMutation(item.TweetID, "set", 2000); err != nil {
		t.Fatalf("ApplyLikeMutation set: %v", err)
	}
	for _, ownerID := range []string{item.TweetID, item.QuoteTweetID} {
		asset, err := d.GetAsset(BuildAssetID("twitter", "tweet", ownerID, "post_media", 0), "post_media")
		if err != nil {
			t.Fatalf("GetAsset %s: %v", ownerID, err)
		}
		if asset == nil || asset.State != AssetStateQueued || asset.RequiredReason != "like" {
			t.Fatalf("required asset %s = %+v", ownerID, asset)
		}
	}
	retained, err := d.xRetainedMediaOwnerSet(2000, 0, []string{item.TweetID, item.QuoteTweetID})
	if err != nil {
		t.Fatalf("xRetainedMediaOwnerSet: %v", err)
	}
	if _, direct := retained[item.TweetID]; !direct {
		t.Fatalf("liked direct owner missing from retained set: %v", sortedKeys(retained))
	}
	if _, quote := retained[item.QuoteTweetID]; !quote {
		t.Fatalf("liked quote owner missing from retained set: %v", sortedKeys(retained))
	}
	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "x-worker", NowMs: 2001, LeaseMs: time.Minute.Milliseconds(), Limit: 10,
	}, true, DownloadLaneCurrent)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claimed = %+v, err = %v; want direct and quote", claimed, err)
	}

	if err := d.ApplyLikeMutation(item.TweetID, "clear", 3000); err != nil {
		t.Fatalf("ApplyLikeMutation clear: %v", err)
	}
	quote, err := d.GetAsset(BuildAssetID("twitter", "tweet", item.QuoteTweetID, "post_media", 0), "post_media")
	if err != nil {
		t.Fatal(err)
	}
	if quote == nil || quote.State != AssetStateDownloading || quote.RequiredReason != "retention" {
		t.Fatalf("clear eagerly changed claimed quote content: %+v", quote)
	}
}

func TestBookmarkMutationUsesCurrentTimeWhenUpdatedAtMissing(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ApplyBookmarkMutation(BookmarkMutation{
		VideoID:     "missing_timestamp_bookmark",
		Action:      "set",
		UpdatedAtMs: 0,
	}); err != nil {
		t.Fatalf("ApplyBookmarkMutation: %v", err)
	}

	var bookmarkedAt int64
	if err := d.QueryRow(`
		SELECT bookmarked_at
		FROM bookmarks
		WHERE video_id = 'missing_timestamp_bookmark'
	`).Scan(&bookmarkedAt); err != nil {
		t.Fatalf("read bookmark: %v", err)
	}
	if bookmarkedAt <= 0 {
		t.Fatalf("bookmarked_at = %d, want positive timestamp", bookmarkedAt)
	}

}

func TestMomentsCursorMutationKeepsNewerClientTimestamp(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.ApplyMomentsCursorMutationWithSortAt("moment_newer", 0, 2_000, "all", 20_000); err != nil {
		t.Fatalf("newer cursor mutation: %v", err)
	}
	if err := d.ApplyMomentsCursorMutationWithSortAt("moment_older", 0, 1_000, "all", 10_000); !IsStaleMutation(err) {
		t.Fatalf("older cursor mutation error = %v, want stale mutation", err)
	}

	var videoID string
	var updatedAt, sortAt int64
	if err := d.QueryRow(`
		SELECT video_id, updated_at_ms, sort_at_ms
		FROM moments_cursors WHERE scope = 'all'
	`).Scan(&videoID, &updatedAt, &sortAt); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if videoID != "moment_newer" || updatedAt != 2000 || sortAt != 20000 {
		t.Fatalf("cursor = (%q, %d, %d), want newer cursor", videoID, updatedAt, sortAt)
	}
}

func TestProgressMutationRejectsStaleAndDoesNotReviseExactRetry(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.MutateProgress("sample_video", 30, 120, 2_000); err != nil {
		t.Fatalf("initial progress: %v", err)
	}
	var revision int64
	if err := d.QueryRow(`
		SELECT revision FROM android_sync_heads
		WHERE owner_kind = 'watch_history' AND owner_id = 'sample_video'
	`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MutateProgress("sample_video", 30, 120, 2_000); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	var retryRevision int64
	if err := d.QueryRow(`
		SELECT revision FROM android_sync_heads
		WHERE owner_kind = 'watch_history' AND owner_id = 'sample_video'
	`).Scan(&retryRevision); err != nil {
		t.Fatal(err)
	}
	if retryRevision != revision {
		t.Fatalf("exact retry revision = %d, want %d", retryRevision, revision)
	}
	if _, err := d.MutateProgress("sample_video", 10, 120, 1_000); !IsStaleMutation(err) {
		t.Fatalf("older progress error = %v, want stale mutation", err)
	}
	var position float64
	if err := d.QueryRow(`SELECT playback_position FROM watch_history WHERE video_id = 'sample_video'`).Scan(&position); err != nil {
		t.Fatal(err)
	}
	if position != 30 {
		t.Fatalf("stale progress changed position to %v", position)
	}
}

func TestWebProgressClearRejectsOlderAndroidProgress(t *testing.T) {
	d := openWritableTestDB(t)
	if _, err := d.MutateProgress("sample_video_two", 30, 120, 2_000); err != nil {
		t.Fatalf("initial Android progress: %v", err)
	}
	if err := d.DeleteWatchHistory("sample_video_two", 3_000); err != nil {
		t.Fatalf("web clear: %v", err)
	}
	if _, err := d.MutateProgress("sample_video_two", 45, 120, 2_500); !IsStaleMutation(err) {
		t.Fatalf("older Android progress error = %v, want stale mutation", err)
	}
	var rows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM watch_history WHERE video_id = 'sample_video_two'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("watch history rows = %d, want cleared", rows)
	}
	var action string
	var updatedAt int64
	if err := d.QueryRow(`
		SELECT action, updated_at_ms FROM mutation_clocks
		WHERE kind = 'progress' AND item_key = 'sample_video_two'
	`).Scan(&action, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if action != "clear" || updatedAt != 3_000 {
		t.Fatalf("progress clock = %s/%d, want clear/3000", action, updatedAt)
	}
	if err := d.UpsertWatchHistoryFullyWatched("sample_video_two", 4_000); err != nil {
		t.Fatalf("newer web set: %v", err)
	}
}

func TestBookmarkMutationCreatesVideoStubForFeedItem(t *testing.T) {
	d := openWritableTestDB(t)

	const (
		tweetID       = "sample_feed_bookmark"
		authorHandle  = "sample_author"
		publishedAtMs = int64(1745100000000)
	)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, body_text,
			canonical_url, published_at, fetched_at
		) VALUES (?, ?, ?, 'sample body', ?, ?, ?)`,
		tweetID,
		"twitter_"+authorHandle,
		"twitter_"+authorHandle,
		"https://x.com/sample_author/status/sample_feed_bookmark",
		publishedAtMs,
		publishedAtMs,
	); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}

	if err := d.ApplyBookmarkMutation(BookmarkMutation{
		VideoID:     tweetID,
		Action:      "set",
		UpdatedAtMs: publishedAtMs + 1000,
	}); err != nil {
		t.Fatalf("ApplyBookmarkMutation: %v", err)
	}

	var channelID string
	if err := d.QueryRow(`
		SELECT channel_id
		FROM videos
		WHERE video_id = ?
	`, tweetID).Scan(&channelID); err != nil {
		t.Fatalf("read video stub: %v", err)
	}
	if channelID != "twitter_sample_author" {
		t.Fatalf("channel_id = %q, want twitter_sample_author", channelID)
	}
	bookmarks, err := d.GetBookmarks(GetBookmarksOpts{Limit: 10})
	if err != nil {
		t.Fatalf("GetBookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("bookmarks = %d, want 1", len(bookmarks))
	}
	if got := bookmarks[0].VideoID; got != tweetID {
		t.Fatalf("bookmark VideoID = %q, want %q", got, tweetID)
	}
	if got := bookmarks[0].Title; got != "sample body" {
		t.Fatalf("bookmark Title = %q, want sample body", got)
	}
}
