package worker

import (
	"fmt"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func TestRunAndroidSyncMaintenanceOncePrunesEligibleDerivedCache(t *testing.T) {
	d := openWorkerTestDB(t)
	nowMs := time.Now().UnixMilli()
	oldBaseMs := nowMs - int64(24*time.Hour/time.Millisecond)
	for i := 0; i < 5; i++ {
		generationID := fmt.Sprintf("android-sync-worker-maintenance-%02d", i+1)
		seedWorkerAndroidSyncGeneration(t, d, generationID, oldBaseMs+int64(i))
	}

	m := &Manager{db: d}
	result, err := m.runAndroidSyncMaintenanceOnce(nowMs)
	if err != nil {
		t.Fatalf("runAndroidSyncMaintenanceOnce: %v", err)
	}
	if result.Drain.GenerationsDeleted != 3 ||
		result.Drain.ItemsDeleted != 3 ||
		result.Drain.AssetsDeleted != 3 ||
		result.Drain.HealthReportsDeleted != 3 {
		t.Fatalf("maintenance result = %+v, want three old generations and child rows deleted", result)
	}

	for _, generationID := range []string{
		"android-sync-worker-maintenance-01",
		"android-sync-worker-maintenance-02",
		"android-sync-worker-maintenance-03",
	} {
		assertWorkerAndroidSyncGenerationExists(t, d, generationID, false)
		assertWorkerAndroidSyncChildRows(t, d, generationID, 0, 0, 0)
	}
	for _, generationID := range []string{
		"android-sync-worker-maintenance-04",
		"android-sync-worker-maintenance-05",
	} {
		assertWorkerAndroidSyncGenerationExists(t, d, generationID, true)
		assertWorkerAndroidSyncChildRows(t, d, generationID, 1, 1, 1)
	}
}

func seedWorkerAndroidSyncGeneration(t *testing.T, d *db.DB, generationID string, createdAtMs int64) {
	t.Helper()
	if err := d.ExecRaw(`
		INSERT INTO android_sync_generations (
			generation_id, created_at_ms, status, source_version, retention_json,
			item_count, asset_count, ready_asset_count, server_missing_asset_count,
			total_bytes, content_counts_json, asset_counts_json
		) VALUES (?, ?, 'ready', ?, '{}', 1, 1, 1, 0, 1, '{}', '{}')
	`, generationID, createdAtMs, generationID+"-source"); err != nil {
		t.Fatalf("insert generation %s: %v", generationID, err)
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_items (generation_id, seq, item_kind, item_id, payload_json)
		VALUES (?, 1, 'videos', ?, '{}')
	`, generationID, generationID+"-video"); err != nil {
		t.Fatalf("insert item %s: %v", generationID, err)
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_assets (
			generation_id, seq, asset_id, asset_kind, owner_id, owner_kind,
			bucket, server_url, content_type, size_bytes, sha256, state,
			required_reason, effective_recency_ms
		) VALUES (?, 1, ?, 'video_stream', ?, 'video', 'videos', '/asset', 'video/mp4', 1, 'sha', 'ready', 'retention', 1)
	`, generationID, generationID+"-asset", generationID+"-video"); err != nil {
		t.Fatalf("insert asset %s: %v", generationID, err)
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_health_reports (
			generation_id, reported_at_ms, payload_json, verified_assets,
			pending_assets, failed_assets, missing_assets, total_assets, verified_bytes
		) VALUES (?, 1, '{}', 1, 0, 0, 0, 1, 1)
	`, generationID); err != nil {
		t.Fatalf("insert health %s: %v", generationID, err)
	}
}

func assertWorkerAndroidSyncGenerationExists(t *testing.T, d *db.DB, generationID string, want bool) {
	t.Helper()
	var got int
	if err := d.QueryRow(`SELECT COUNT(*) FROM android_sync_generations WHERE generation_id = ?`, generationID).Scan(&got); err != nil {
		t.Fatalf("count generation %s: %v", generationID, err)
	}
	if (got > 0) != want {
		t.Fatalf("generation %s exists = %v, want %v", generationID, got > 0, want)
	}
}

func assertWorkerAndroidSyncChildRows(t *testing.T, d *db.DB, generationID string, wantItems, wantAssets, wantHealth int) {
	t.Helper()
	for table, want := range map[string]int{
		"android_sync_items":          wantItems,
		"android_sync_assets":         wantAssets,
		"android_sync_health_reports": wantHealth,
	} {
		var got int
		if err := d.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE generation_id = ?", generationID).Scan(&got); err != nil {
			t.Fatalf("count %s for %s: %v", table, generationID, err)
		}
		if got != want {
			t.Fatalf("%s rows for %s = %d, want %d", table, generationID, got, want)
		}
	}
}
