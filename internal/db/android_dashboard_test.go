package db

import "testing"

func TestLatestAndroidSyncHealthReportUsesPersistedNewestReport(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.RecordAndroidSyncHealth(
		"android-assets-old",
		3000,
		[]byte(`{"retention":{"feed_days":7,"youtube_days":7,"moments_days":7,"story_hours":48}}`),
		8,
		1,
		1,
		10,
		8192,
	); err != nil {
		t.Fatalf("old health: %v", err)
	}
	if err := d.RecordAndroidSyncHealth(
		"android-assets-new",
		4000,
		[]byte(`{"retention":{"feed_days":3,"youtube_days":2,"moments_days":7,"story_hours":24}}`),
		18,
		1,
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
	if got.Cursor != "android-assets-new" || got.ReportedAtMs != 4000 {
		t.Fatalf("latest report = %#v", got)
	}
	if got.VerifiedAssets != 18 || got.TotalAssets != 20 || got.VerifiedBytes != 16384 {
		t.Fatalf("counts = verified %d total %d bytes %d", got.VerifiedAssets, got.TotalAssets, got.VerifiedBytes)
	}
	if !got.HasRetention || got.Retention.FeedDays != 3 || got.Retention.YoutubeDays != 2 || got.Retention.MomentsDays != 7 || got.Retention.StoryHours != 24 {
		t.Fatalf("retention = %+v has=%v", got.Retention, got.HasRetention)
	}
}
