package db

import "testing"

func TestDashboardStatsUseCanonicalAssets(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_video_a', 'youtube_sample', 'youtube_video', 'Canonical', 1);
		INSERT INTO watch_history (video_id, playback_position, duration, updated_at_ms)
		VALUES ('sample_video_a', 100, 100, 2);
		INSERT INTO feed_items (tweet_id, published_at, fetched_at)
		VALUES
			('sample_post_a', 1, 1),
			('sample_post_b', 1, 1)
	`); err != nil {
		t.Fatalf("seed dashboard state: %v", err)
	}
	publishAssetMetadataForTest(t, d, Asset{AssetID: "sample_video_asset_a", AssetKind: "video_stream", OwnerKind: "youtube_video", OwnerID: "sample_video_a", FilePath: "media/youtube/canonical.mp4", ContentType: "video/mp4", SizeBytes: 100}, 1)
	publishAssetMetadataForTest(t, d, Asset{AssetID: "sample_media_a", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post_a", FilePath: "media/twitter/sample/feed.jpg", SizeBytes: 10}, 1)
	upsertAssetForTest(t, d, Asset{AssetID: "sample_media_b", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post_b", SourceURL: "https://example.test/queued.jpg"}, 1)
	upsertAssetForTest(t, d, Asset{AssetID: "sample_missing_media", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_missing", SourceURL: "https://example.test/missing.jpg", State: AssetStateServerMissing}, 1)

	stats, err := d.GetDashboardStats()
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if got := stats["videos_total"]; got != 1 {
		t.Fatalf("videos_total = %v, want one canonical video", got)
	}
	if got := stats["videos_watched"]; got != 1 {
		t.Fatalf("videos_watched = %v, want one canonical watched video", got)
	}
	if got := stats["local_feed_count"]; got != 1 {
		t.Fatalf("local_feed_count = %v, want one canonical ready feed item", got)
	}
	pipeline, ok := stats["media_pipeline"].(map[string]int)
	if !ok {
		t.Fatalf("media_pipeline has unexpected type: %T", stats["media_pipeline"])
	}
	if pipeline["ready"] != 2 || pipeline["queued"] != 1 || pipeline["failed"] != 1 {
		t.Fatalf("media_pipeline = %+v, want canonical state counts 2/1/1", pipeline)
	}
}
