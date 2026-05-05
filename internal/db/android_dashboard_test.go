package db

import "testing"

func TestAndroidDashboardExpectationsCountInstagramMoments(t *testing.T) {
	d := openWritableTestDB(t)
	nowMs := int64(10 * 24 * 60 * 60 * 1000)
	recent := nowMs - int64(2*24*60*60*1000)
	old := nowMs - int64(9*24*60*60*1000)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, file_path, file_size, published_at, sync_seq)
		VALUES
			('ig_recent', 'instagram_cinema', 'Recent IG', 'media/instagram/cinema/ig_recent.mp4', 10, ?, 1),
			('tt_recent', 'tiktok_cinema', 'Recent TT', 'media/tiktok/cinema/tt_recent.mp4', 10, ?, 1),
			('ig_old', 'instagram_cinema', 'Old IG', 'media/instagram/cinema/ig_old.mp4', 10, ?, 1),
			('youtube_recent', 'youtube_UCcinema', 'Recent YouTube', 'media/youtube/cinema/youtube_recent.mp4', 10, ?, 1)
	`, recent, recent, old, recent); err != nil {
		t.Fatalf("insert videos: %v", err)
	}

	got, err := d.GetAndroidDashboardExpectations("", AndroidRetentionSettings{
		FeedDays: 7, YoutubeDays: 7, MomentsDays: 7, StoryHours: 48,
	}, nowMs)
	if err != nil {
		t.Fatalf("expectations: %v", err)
	}
	if got.Moments != 2 {
		t.Fatalf("Moments = %d, want 2", got.Moments)
	}
	if got.Videos != 1 {
		t.Fatalf("Videos = %d, want 1", got.Videos)
	}
}

func TestLatestAndroidSyncHealthReportUsesPersistedNewestReport(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO android_sync_generations (
			generation_id, created_at_ms, status, source_version, retention_json,
			item_count, asset_count, ready_asset_count, server_missing_asset_count,
			total_bytes, content_counts_json, asset_counts_json
		) VALUES (
			'android-sync-old', 1000, 'ready', 'old-source', '{}',
			1, 10, 9, 1, 100, '{}', '{}'
		), (
			'android-sync-new', 2000, 'ready', 'new-source', '{"feed_days":3,"youtube_days":2,"moments_days":7,"story_hours":24}',
			2, 20, 18, 2, 200, '{}', '{}'
		)
	`); err != nil {
		t.Fatalf("insert generations: %v", err)
	}
	if err := d.RecordAndroidSyncHealth(
		"android-sync-old",
		3000,
		[]byte(`{"retention":{"feed_days":7,"youtube_days":7,"moments_days":7,"story_hours":48}}`),
		8,
		1,
		0,
		1,
		10,
		8192,
	); err != nil {
		t.Fatalf("old health: %v", err)
	}
	if err := d.RecordAndroidSyncHealth(
		"android-sync-new",
		4000,
		[]byte(`{"retention":{"feed_days":3,"youtube_days":2,"moments_days":7,"story_hours":24}}`),
		18,
		1,
		0,
		1,
		20,
		16384,
	); err != nil {
		t.Fatalf("new health: %v", err)
	}

	got, err := d.GetLatestAndroidSyncHealthReport()
	if err != nil {
		t.Fatalf("latest health: %v", err)
	}
	if got == nil {
		t.Fatal("latest health missing")
	}
	if got.GenerationID != "android-sync-new" || got.ReportedAtMs != 4000 {
		t.Fatalf("latest report = %#v", got)
	}
	if got.VerifiedAssets != 18 || got.TotalAssets != 20 || got.VerifiedBytes != 16384 {
		t.Fatalf("counts = verified %d total %d bytes %d", got.VerifiedAssets, got.TotalAssets, got.VerifiedBytes)
	}
	if !got.HasRetention || got.Retention.FeedDays != 3 || got.Retention.YoutubeDays != 2 || got.Retention.MomentsDays != 7 || got.Retention.StoryHours != 24 {
		t.Fatalf("retention = %+v has=%v", got.Retention, got.HasRetention)
	}
}
