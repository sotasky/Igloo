package db

import "testing"

func TestDashboardStatsUseCanonicalAssets(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('canonical_video', 'youtube_sample', 'youtube_video', 'Canonical', 1);
		INSERT INTO watch_history (video_id, playback_position, duration, updated_at_ms)
		VALUES ('canonical_video', 100, 100, 2);
		INSERT INTO feed_items (tweet_id, published_at, fetched_at)
		VALUES
			('canonical_feed', 1, 1),
			('queued_feed', 1, 1);
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, content_type, size_bytes, state, created_at_ms, updated_at_ms
		) VALUES
			('canonical_video_stream', 'video_stream', 'youtube_video', 'canonical_video', 0, 'media/youtube/canonical.mp4', 'video/mp4', 100, 'ready', 1, 1),
			('canonical_feed_media', 'post_media', 'tweet', 'canonical_feed', 0, 'media/twitter/sample/feed.jpg', 'image/jpeg', 10, 'ready', 1, 1),
			('queued_feed_media', 'post_media', 'tweet', 'queued_feed', 0, '', 'image/jpeg', 0, 'queued', 1, 1),
			('missing_feed_media', 'post_media', 'tweet', 'missing_feed', 0, '', 'image/jpeg', 0, 'server_missing', 1, 1);
	`); err != nil {
		t.Fatalf("seed dashboard state: %v", err)
	}

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
