package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestCandidateServerXMediaParentPlanUsesIdentityIndexes(t *testing.T) {
	d := openFreshTestDB(t)
	query, args := candidateServerXMediaParentsQuery([]string{"sample_owner"})
	rows, err := d.conn.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	plan := strings.Join(details, "\n")
	if strings.Contains(plan, "SCAN feed_items") {
		t.Fatalf("candidate owner plan scans feed_items: %s", plan)
	}
	if !strings.Contains(plan, "sqlite_autoindex_feed_items_1") ||
		!strings.Contains(plan, "idx_feed_items_quote") {
		t.Fatalf("candidate owner plan = %s", plan)
	}
}

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
		if asset.State != AssetStatePruned || asset.FilePath != "" || asset.SizeBytes != 0 || asset.FileMtimeNs != 0 {
			t.Fatalf("pruned asset %s retained ready metadata: %+v", ownerID, asset)
		}
	}
	for _, ownerID := range []string{"sample_tweet_new", "sample_tweet_bookmarked", "sample_tweet_liked", "sample_tweet_keep"} {
		asset := readXRetentionAsset(t, d, ownerID)
		if asset.State != AssetStateReady || asset.FilePath == "" || asset.SizeBytes <= 0 || asset.FileMtimeNs <= 0 {
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

func TestPruneXMediaRetentionKeepsAssetsInsideAndroidFeedWindow(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	nowMs := int64(10 * 24 * time.Hour / time.Millisecond)
	items := []model.FeedItem{
		xRetentionFeedItem("sample_android_new", sourceHandle, nowMs-time.Hour.Milliseconds()),
		xRetentionFeedItem("sample_android_recent", sourceHandle, nowMs-12*time.Hour.Milliseconds()),
		xRetentionFeedItem("sample_android_old", sourceHandle, nowMs-48*time.Hour.Milliseconds()),
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		publishXRetentionAsset(t, d, item.TweetID, []byte(item.TweetID))
	}

	recordAndroidFeedRetention(t, d, 1, nowMs)
	result, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: nowMs})
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedItems != 2 || result.AssetsPruned != 1 {
		t.Fatalf("retention result = %+v", result)
	}
	if asset := readXRetentionAsset(t, d, "sample_android_recent"); asset.State != AssetStateReady {
		t.Fatalf("Android-retained asset = %+v", asset)
	}
	if asset := readXRetentionAsset(t, d, "sample_android_old"); asset.State != AssetStatePruned {
		t.Fatalf("expired asset = %+v", asset)
	}
}

func TestPruneXMediaRetentionOnlyNeedsAndroidOwnerWhenPruning(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 2)
	items := []model.FeedItem{
		xRetentionFeedItem("sample_fail_closed_new", sourceHandle, 200),
		xRetentionFeedItem("sample_fail_closed_old", sourceHandle, 100),
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		publishXRetentionAsset(t, d, item.TweetID, []byte(item.TweetID))
	}
	if err := d.ExecRaw(`DELETE FROM android_feed_retention`); err != nil {
		t.Fatal(err)
	}
	if result, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000}); err != nil || result.PrunedItems != 0 {
		t.Fatalf("no-op retention = %+v / %v", result, err)
	}
	if err := d.UpdateChannelSettings(channelID, map[string]any{"media_download_limit": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000}); err == nil {
		t.Fatal("retention succeeded without the Android owner root")
	}
	if asset := readXRetentionAsset(t, d, "sample_fail_closed_old"); asset.State != AssetStateReady {
		t.Fatalf("fail-open pruning changed asset: %+v", asset)
	}
}

func TestRepeatedXMediaObservationPreservesPrunedWork(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	items := []model.FeedItem{
		xRetentionFeedItem("sample_repeat_new", sourceHandle, 200),
		xRetentionFeedItem("sample_repeat_old", sourceHandle, 100),
	}
	if _, err := d.UpsertFeedItemsDetailed(items); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000}); err != nil {
		t.Fatal(err)
	}
	result, err := d.UpsertFeedItemsDetailed([]model.FeedItem{items[1]})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.XMediaRetentionChanges) != 0 {
		t.Fatalf("unchanged observation reported retention work: %+v", result.XMediaRetentionChanges)
	}
	changedItem := items[1]
	changedItem.MediaJSON = `[{"url":"https://cdn.example/sample_repeat_old_changed.jpg","type":"photo"}]`
	changedItem.Media = nil
	changed, err := d.UpsertFeedItemsDetailed([]model.FeedItem{changedItem})
	if err != nil {
		t.Fatal(err)
	}
	if got := changed.XMediaRetentionChanges[channelID]; len(got) != 1 || got[0] != changedItem.TweetID {
		t.Fatalf("changed URL retention work = %+v", changed.XMediaRetentionChanges)
	}
	if asset := readXRetentionAsset(t, d, changedItem.TweetID); asset.State != AssetStatePruned {
		t.Fatalf("changed URL reactivated pruned work: %+v", asset)
	}
	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "current-worker", NowMs: time.Now().UnixMilli(), LeaseMs: 1000, Limit: 10,
	}, true, DownloadLaneCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].OwnerID != items[0].TweetID {
		t.Fatalf("current claim included pruned media: %+v", claimed)
	}
}

func TestXMediaRetentionChangeReconcilesOnlyNewAndBoundaryOwners(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 2)
	initial := []model.FeedItem{
		xRetentionFeedItem("sample_window_first", sourceHandle, 300),
		xRetentionFeedItem("sample_window_second", sourceHandle, 200),
		xRetentionFeedItem("sample_historical_overflow", sourceHandle, 100),
	}
	if _, err := d.UpsertFeedItemsDetailed(initial); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000}); err != nil {
		t.Fatal(err)
	}
	var historicalRevision, historicalUpdated int64
	if err := d.QueryRow(`
		SELECT revision, updated_at_ms FROM assets
		WHERE asset_id = ?
	`, BuildAssetID("twitter", "tweet", "sample_historical_overflow", "post_media", 0)).Scan(&historicalRevision, &historicalUpdated); err != nil {
		t.Fatal(err)
	}
	newest := xRetentionFeedItem("sample_window_new", sourceHandle, 400)
	upsert, err := d.UpsertFeedItemsDetailed([]model.FeedItem{newest})
	if err != nil {
		t.Fatal(err)
	}
	changed := upsert.XMediaRetentionChanges[channelID]
	if len(changed) != 1 || changed[0] != newest.TweetID {
		t.Fatalf("retention changes = %+v", upsert.XMediaRetentionChanges)
	}
	result, err := d.ReconcileXMediaRetentionChanges(channelID, changed, XMediaRetentionOptions{NowMs: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedItems != 1 || result.AssetsPruned != 1 {
		t.Fatalf("bounded retention result = %+v", result)
	}
	if asset := readXRetentionAsset(t, d, newest.TweetID); asset.State == AssetStatePruned {
		t.Fatalf("newest asset was pruned: %+v", asset)
	}
	if asset := readXRetentionAsset(t, d, "sample_window_second"); asset.State != AssetStatePruned {
		t.Fatalf("displaced asset = %+v", asset)
	}
	var nextRevision, nextUpdated int64
	if err := d.QueryRow(`
		SELECT revision, updated_at_ms FROM assets
		WHERE asset_id = ?
	`, BuildAssetID("twitter", "tweet", "sample_historical_overflow", "post_media", 0)).Scan(&nextRevision, &nextUpdated); err != nil {
		t.Fatal(err)
	}
	if nextRevision != historicalRevision || nextUpdated != historicalUpdated {
		t.Fatalf("historical overflow was revisited: before=(%d,%d) after=(%d,%d)",
			historicalRevision, historicalUpdated, nextRevision, nextUpdated)
	}
}

func seedXRetentionChannel(t *testing.T, d *DB, limit int) (string, string) {
	t.Helper()
	recordAndroidFeedRetention(t, d, 0, 1)
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

func recordAndroidFeedRetention(t *testing.T, d *DB, feedDays int, reportedAtMs int64) {
	t.Helper()
	if err := d.RecordAndroidFeedRetention(feedDays, reportedAtMs); err != nil {
		t.Fatalf("record Android retention: %v", err)
	}
}
