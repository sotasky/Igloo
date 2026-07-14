package db

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestPruneXFeedSourceRetentionBoundsAttributionAndPreservesOtherRoots(t *testing.T) {
	d := openFreshTestDB(t)
	if err := d.SetSetting("media_download_limit_default", "1"); err != nil {
		t.Fatal(err)
	}
	for _, source := range []model.FeedSource{
		{SourceID: "twitter_sample_a", Platform: "twitter", SourceType: "list", ExternalID: "primary", Label: "Primary", URL: "https://x.com/i/lists/1", Enabled: true},
		{SourceID: "twitter_sample_b", Platform: "twitter", SourceType: "list", ExternalID: "shared", Label: "Shared", URL: "https://x.com/i/lists/2", Enabled: true},
	} {
		if err := d.UpsertFeedSource(source); err != nil {
			t.Fatal(err)
		}
	}
	followedChannelID, followedHandle := seedXRetentionChannel(t, d, 1)

	items := []model.FeedItem{
		xRetentionFeedItem("sample_source_new", "list_author", 600),
		xRetentionFeedItem("sample_source_bookmarked", "list_author", 500),
		xRetentionFeedItem("sample_source_followed", followedHandle, 400),
		xRetentionFeedItem("sample_source_shared", "shared_author", 300),
		xRetentionFeedItem("sample_source_prune", "pruned_author", 200),
		xRetentionFeedItem("sample_source_text", "text_author", 100),
	}
	items[len(items)-1].MediaJSON = ""
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if err := d.RecordFeedItemSources(item.TweetID, []string{"twitter_sample_a"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.RecordFeedItemSources("sample_source_shared", []string{"twitter_sample_b"}); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`INSERT INTO bookmarks (video_id) VALUES ('sample_source_bookmarked')`); err != nil {
		t.Fatal(err)
	}
	for _, ownerID := range []string{
		"sample_source_new", "sample_source_bookmarked", "sample_source_followed",
		"sample_source_shared", "sample_source_prune",
	} {
		publishXRetentionAsset(t, d, ownerID, []byte(ownerID))
	}

	result, err := d.PruneXFeedSourceRetention("twitter_sample_a", 1, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourcesScanned != 1 || result.SourcesOverLimit != 1 || result.ProtectedItems != 1 || result.KeptItems != 1 || result.PrunedItems != 4 {
		t.Fatalf("unexpected source retention result: %+v", result)
	}
	if result.AssetsPruned != 1 || result.FileRemoval.Removed != 1 {
		t.Fatalf("unexpected source asset pruning: %+v", result)
	}

	var primaryCount, sharedCount, canonicalCount int
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_item_sources WHERE source_id = 'twitter_sample_a'`).Scan(&primaryCount); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_item_sources WHERE source_id = 'twitter_sample_b'`).Scan(&sharedCount); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id LIKE 'sample_source_%'`).Scan(&canonicalCount); err != nil {
		t.Fatal(err)
	}
	if primaryCount != 2 || sharedCount != 1 || canonicalCount != len(items) {
		t.Fatalf("retained rows = primary %d shared %d canonical %d", primaryCount, sharedCount, canonicalCount)
	}

	for _, ownerID := range []string{"sample_source_new", "sample_source_bookmarked", "sample_source_followed", "sample_source_shared"} {
		if asset := readXRetentionAsset(t, d, ownerID); asset.State != AssetStateReady {
			t.Fatalf("rooted asset %s = %+v", ownerID, asset)
		}
	}
	if asset := readXRetentionAsset(t, d, "sample_source_prune"); asset.State != AssetStatePruned {
		t.Fatalf("unrooted source asset = %+v", asset)
	}
	if settings, err := d.GetChannelSettings(followedChannelID); err != nil || settings.MediaDownloadLimit != 1 {
		t.Fatalf("followed settings = %+v, %v", settings, err)
	}
}

func TestPruneXChannelRetentionPreservesEnabledFeedSourceRoot(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	if err := d.UpsertFeedSource(model.FeedSource{
		SourceID: "twitter_sample_root", Platform: "twitter", SourceType: "community",
		ExternalID: "root", Label: "Root", URL: "https://x.com/i/communities/root", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	items := []model.FeedItem{
		xRetentionFeedItem("sample_channel_new", sourceHandle, 200),
		xRetentionFeedItem("sample_channel_source_root", sourceHandle, 100),
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		publishXRetentionAsset(t, d, item.TweetID, []byte(item.TweetID))
	}
	if err := d.RecordFeedItemSources("sample_channel_source_root", []string{"twitter_sample_root"}); err != nil {
		t.Fatal(err)
	}

	result, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedItems != 1 || result.AssetsPruned != 0 || result.FileRemoval.Removed != 0 {
		t.Fatalf("feed-source root was pruned by channel retention: %+v", result)
	}
	if asset := readXRetentionAsset(t, d, "sample_channel_source_root"); asset.State != AssetStateReady {
		t.Fatalf("feed-source rooted asset = %+v", asset)
	}
}

func TestPruneXFeedSourceRetentionKeepsRecentAndroidAssetRoot(t *testing.T) {
	d := openFreshTestDB(t)
	nowMs := int64(10 * 24 * time.Hour / time.Millisecond)
	if err := d.UpsertFeedSource(model.FeedSource{
		SourceID: "twitter_sample_source", Platform: "twitter", SourceType: "list",
		ExternalID: "android", Label: "Android", URL: "https://x.com/i/lists/android", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	items := []model.FeedItem{
		xRetentionFeedItem("sample_list_android_new", "sample_author", nowMs-time.Hour.Milliseconds()),
		xRetentionFeedItem("sample_list_android_recent", "sample_author", nowMs-2*time.Hour.Milliseconds()),
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if err := d.RecordFeedItemSources(item.TweetID, []string{"twitter_sample_source"}); err != nil {
			t.Fatal(err)
		}
		publishXRetentionAsset(t, d, item.TweetID, []byte(item.TweetID))
	}

	recordAndroidFeedRetention(t, d, 1, nowMs)
	result, err := d.PruneXFeedSourceRetention("twitter_sample_source", 1, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedItems != 1 || result.AssetsPruned != 0 {
		t.Fatalf("retention result = %+v", result)
	}
	if asset := readXRetentionAsset(t, d, "sample_list_android_recent"); asset.State != AssetStateReady {
		t.Fatalf("Android-retained source asset = %+v", asset)
	}
}

func TestRestoreXMediaRetentionForChannelReactivatesOnlyWidenedStoredWindow(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	items := []model.FeedItem{
		xRetentionFeedItem("sample_restore_new", sourceHandle, 200),
		xRetentionFeedItem("sample_restore_old", sourceHandle, 100),
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		publishXRetentionAsset(t, d, item.TweetID, []byte(item.TweetID))
	}
	if _, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: 3000}); err != nil {
		t.Fatal(err)
	}
	if asset := readXRetentionAsset(t, d, "sample_restore_old"); asset.State != AssetStatePruned {
		t.Fatalf("old asset before widening = %+v", asset)
	}
	if err := d.UpdateChannelSettings(channelID, map[string]any{"media_download_limit": 2}); err != nil {
		t.Fatal(err)
	}
	result, err := d.RestoreXMediaRetentionForChannel(channelID, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsRestored != 1 {
		t.Fatalf("restored assets = %d, want 1; result=%+v", result.AssetsRestored, result)
	}
	if asset := readXRetentionAsset(t, d, "sample_restore_old"); asset.State != AssetStateQueued {
		t.Fatalf("old asset after widening = %+v", asset)
	}
}

func TestRestoreXMediaForFeedWindowReactivatesRecentPrunedAssets(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	nowMs := time.Now().UnixMilli()
	items := []model.FeedItem{
		xRetentionFeedItem("sample_feed_restore_new", sourceHandle, nowMs-time.Hour.Milliseconds()),
		xRetentionFeedItem("sample_feed_restore_recent", sourceHandle, nowMs-2*time.Hour.Milliseconds()),
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		publishXRetentionAsset(t, d, item.TweetID, []byte(item.TweetID))
	}
	if _, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: nowMs}); err != nil {
		t.Fatal(err)
	}
	if asset := readXRetentionAsset(t, d, "sample_feed_restore_recent"); asset.State != AssetStatePruned {
		t.Fatalf("asset before Android restore = %+v", asset)
	}
	recordAndroidFeedRetention(t, d, 1, nowMs)
	result, err := d.RestoreXMediaForAndroidFeed(1, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	restoredAsset := readXRetentionAsset(t, d, "sample_feed_restore_recent")
	if result.AssetsRestored != 1 {
		t.Fatalf("restored assets = %d, want 1; asset = %+v", result.AssetsRestored, restoredAsset)
	}
	if restoredAsset.State != AssetStateQueued {
		t.Fatalf("asset after Android restore = %+v", restoredAsset)
	}
}

func TestRestoreXMediaForFeedWindowPromotesReadyDesiredObject(t *testing.T) {
	d := openFreshTestDB(t)
	channelID, sourceHandle := seedXRetentionChannel(t, d, 1)
	nowMs := time.Now().UnixMilli()
	items := []model.FeedItem{
		xRetentionFeedItem("sample_restore_current", sourceHandle, nowMs-time.Hour.Milliseconds()),
		xRetentionFeedItem("sample_target", sourceHandle, nowMs-2*time.Hour.Milliseconds()),
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		publishXRetentionAsset(t, d, item.TweetID, []byte(item.TweetID))
	}
	if _, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{NowMs: nowMs}); err != nil {
		t.Fatal(err)
	}
	current := readXRetentionAsset(t, d, "sample_restore_current")
	if err := d.ExecRaw(`
		UPDATE assets SET desired_object_id = ?
		WHERE owner_kind = 'tweet' AND owner_id = 'sample_target'
	`, current.ObjectID); err != nil {
		t.Fatal(err)
	}

	result, err := d.RestoreXMediaForAndroidFeed(1, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	restored := readXRetentionAsset(t, d, "sample_target")
	if result.AssetsRestored != 1 {
		t.Fatalf("restored assets = %d, want 1", result.AssetsRestored)
	}
	if restored.State != AssetStateReady || restored.ObjectID != restored.DesiredObjectID ||
		restored.SizeBytes <= 0 || restored.FileMtimeNs <= 0 {
		t.Fatalf("restored asset did not promote the ready desired object: %+v", restored)
	}
}
