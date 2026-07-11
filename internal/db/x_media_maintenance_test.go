package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestPruneXMediaRetentionUsesAssetsAndKeepsProtectedItems(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 2)

	items := []model.FeedItem{
		xRetentionFeedItem("sample_tweet_new", sourceHandle, 600),
		xRetentionFeedItem("sample_tweet_bookmarked", sourceHandle, 500),
		xRetentionFeedItem("sample_tweet_liked", sourceHandle, 400),
		xRetentionFeedItem("sample_tweet_keep", sourceHandle, 300),
		xRetentionFeedItem("sample_tweet_prune", sourceHandle, 200),
		xRetentionFeedItem("sample_tweet_queued_prune", sourceHandle, 100),
	}
	items[4].QuoteTweetID = "sample_quote_prune"
	items[4].QuoteMediaJSON = `[{"url":"https://cdn.example/sample_quote_prune.jpg","type":"photo"}]`
	if n, err := d.UpsertFeedItems(items); err != nil || n != len(items) {
		t.Fatalf("UpsertFeedItems = (%d, %v), want (%d, nil)", n, err, len(items))
	}

	for _, ownerID := range []string{
		"sample_tweet_new", "sample_tweet_bookmarked", "sample_tweet_liked",
		"sample_tweet_keep", "sample_tweet_prune", "sample_quote_prune",
	} {
		publishXRetentionAsset(t, d, ownerID, []byte(ownerID+"-body"))
	}
	if err := d.ExecRaw(`INSERT INTO bookmarks (video_id) VALUES ('sample_tweet_bookmarked')`); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO feed_likes (tweet_id) VALUES ('sample_tweet_liked')`); err != nil {
		t.Fatalf("insert like: %v", err)
	}

	result, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000})
	if err != nil {
		t.Fatalf("PruneXMediaRetentionForChannel: %v", err)
	}
	if result.SourcesScanned != 1 || result.SourcesOverLimit != 1 || result.ProtectedItems != 2 || result.KeptItems != 2 || result.PrunedItems != 2 {
		t.Fatalf("unexpected retention result: %+v", result)
	}
	if result.AssetsPruned != 3 || result.FileRemoval.Removed != 2 {
		t.Fatalf("unexpected canonical retention writes: %+v", result)
	}
	wantBytes := int64(len("sample_tweet_prune-body") + len("sample_quote_prune-body"))
	if result.CandidateFileBytes != wantBytes || result.FileRemoval.RemovedBytes != wantBytes {
		t.Fatalf("removed bytes = candidate %d actual %d, want %d", result.CandidateFileBytes, result.FileRemoval.RemovedBytes, wantBytes)
	}

	for _, ownerID := range []string{"sample_tweet_prune", "sample_quote_prune", "sample_tweet_queued_prune"} {
		asset := readXRetentionAsset(t, d, ownerID)
		if asset.State != AssetStatePruned || asset.FilePath != "" || asset.SizeBytes != 0 || asset.SHA256 != "" || asset.FileMtimeNs != 0 {
			t.Fatalf("pruned asset %s retained ready metadata: %+v", ownerID, asset)
		}
	}
	for _, ownerID := range []string{"sample_tweet_new", "sample_tweet_bookmarked", "sample_tweet_liked", "sample_tweet_keep"} {
		asset := readXRetentionAsset(t, d, ownerID)
		if asset.State != AssetStateReady || asset.FilePath == "" || len(asset.SHA256) != 64 || asset.FileMtimeNs <= 0 {
			t.Fatalf("retained asset %s = %+v", ownerID, asset)
		}
	}
	for _, ownerID := range []string{"sample_tweet_prune", "sample_quote_prune"} {
		path := filepath.Join(d.storage.StateRoot(), "media", "twitter", sourceHandle, ownerID+".jpg")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pruned file %s still exists or stat failed: %v", ownerID, err)
		}
	}
	var bookmarks, likes int
	if err := d.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id = 'sample_tweet_bookmarked'`).Scan(&bookmarks); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE tweet_id = 'sample_tweet_liked'`).Scan(&likes); err != nil {
		t.Fatal(err)
	}
	if bookmarks != 1 || likes != 1 {
		t.Fatalf("state after retention: bookmarks=%d likes=%d", bookmarks, likes)
	}
}

func TestPruneXMediaRetentionKeepsQuoteSharedByRetainedParent(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	newest := xRetentionFeedItem("sample_parent_new", sourceHandle, 200)
	newest.MediaJSON = ""
	newest.QuoteTweetID = "sample_shared_quote"
	newest.QuoteMediaJSON = `[{"url":"https://cdn.example/shared.jpg","type":"photo"}]`
	oldest := xRetentionFeedItem("sample_parent_old", sourceHandle, 100)
	oldest.MediaJSON = ""
	oldest.QuoteTweetID = newest.QuoteTweetID
	oldest.QuoteMediaJSON = newest.QuoteMediaJSON
	if _, err := d.UpsertFeedItems([]model.FeedItem{newest, oldest}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	publishXRetentionAsset(t, d, newest.QuoteTweetID, []byte("shared-quote"))

	result, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000})
	if err != nil {
		t.Fatalf("PruneXMediaRetentionForChannel: %v", err)
	}
	if result.PrunedItems != 1 || result.AssetsPruned != 0 || result.FileRemoval.Removed != 0 {
		t.Fatalf("shared quote was treated as exclusively pruned: %+v", result)
	}
	asset := readXRetentionAsset(t, d, newest.QuoteTweetID)
	if asset.State != AssetStateReady || asset.FilePath == "" {
		t.Fatalf("shared quote asset = %+v", asset)
	}
}

func TestPruneXMediaRetentionKeepsLikedQuoteOwner(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	newest := xRetentionFeedItem("sample_direct_new", sourceHandle, 200)
	oldest := xRetentionFeedItem("sample_quote_parent_old", sourceHandle, 100)
	oldest.MediaJSON = ""
	oldest.QuoteTweetID = "sample_liked_quote"
	oldest.QuoteMediaJSON = `[{"url":"https://cdn.example/liked-quote.jpg","type":"photo"}]`
	if _, err := d.UpsertFeedItems([]model.FeedItem{newest, oldest}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	publishXRetentionAsset(t, d, newest.TweetID, []byte("newest"))
	publishXRetentionAsset(t, d, oldest.QuoteTweetID, []byte("liked-quote"))
	if err := d.ExecRaw(`INSERT INTO feed_likes (tweet_id) VALUES ('sample_liked_quote')`); err != nil {
		t.Fatalf("insert quote like: %v", err)
	}

	result, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000})
	if err != nil {
		t.Fatalf("PruneXMediaRetentionForChannel: %v", err)
	}
	if result.PrunedItems != 1 || result.AssetsPruned != 0 || result.FileRemoval.Removed != 0 {
		t.Fatalf("liked quote owner was pruned: %+v", result)
	}
	asset := readXRetentionAsset(t, d, oldest.QuoteTweetID)
	if asset.State != AssetStateReady || asset.FilePath == "" {
		t.Fatalf("liked quote asset = %+v", asset)
	}
}

func seedXRetentionChannel(t *testing.T, d *DB, limit int) (string, string) {
	t.Helper()
	channelID, sourceHandle := "twitter_sample_source", "sample_source"
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, ?, 'Sample Source', '', 'twitter', 1)
	`, channelID, sourceHandle); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`, channelID); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO channel_settings (channel_id, media_download_limit, updated_at) VALUES (?, ?, 1)`, channelID, limit); err != nil {
		t.Fatalf("insert settings: %v", err)
	}
	return channelID, sourceHandle
}

func xRetentionFeedItem(tweetID, sourceHandle string, publishedAtMs int64) model.FeedItem {
	publishedAt := time.UnixMilli(publishedAtMs).UTC()
	return model.FeedItem{
		TweetID: tweetID, SourceHandle: sourceHandle, AuthorHandle: sourceHandle,
		MediaJSON:   `[{"url":"https://cdn.example/` + tweetID + `.jpg","type":"photo"}]`,
		PublishedAt: &publishedAt, FetchedAt: publishedAt,
	}
}

func publishXRetentionAsset(t *testing.T, d *DB, ownerID string, body []byte) {
	t.Helper()
	asset := readXRetentionAsset(t, d, ownerID)
	relPath := filepath.Join("media", "twitter", "sample_source", ownerID+".jpg")
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), relPath), body)
	asset.FilePath = relPath
	asset.ContentType = "image/jpeg"
	asset.State = AssetStateReady
	if err := d.StoreReadyAsset(asset, 2000); err != nil {
		t.Fatalf("StoreReadyAsset %s: %v", ownerID, err)
	}
}

func readXRetentionAsset(t *testing.T, d *DB, ownerID string) Asset {
	t.Helper()
	asset, err := d.GetAsset(BuildAssetID("twitter", "tweet", ownerID, "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset %s: %v", ownerID, err)
	}
	if asset == nil {
		t.Fatalf("missing asset %s", ownerID)
	}
	return *asset
}
