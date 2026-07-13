package worker

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func TestApplyAndroidFeedRetentionReconcilesOnlyChangedWindow(t *testing.T) {
	database := newTestWorkerDB(t)
	manager := &Manager{db: database}

	if err := manager.ApplyAndroidFeedRetention(1); err != nil {
		t.Fatal(err)
	}
	first := requireAndroidFeedRetention(t, database)
	if first.FeedDays != 1 {
		t.Fatalf("first feed days = %d, want 1", first.FeedDays)
	}
	if err := manager.ApplyAndroidFeedRetention(1); err != nil {
		t.Fatal(err)
	}
	skipped := requireAndroidFeedRetention(t, database)
	if skipped.ReconciledAtMs != first.ReconciledAtMs {
		t.Fatalf("same fresh window reconciled again: %d -> %d", first.ReconciledAtMs, skipped.ReconciledAtMs)
	}

	if err := database.RecordAndroidFeedRetention(1, 1234); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyAndroidFeedRetention(1); err != nil {
		t.Fatal(err)
	}
	unchanged := requireAndroidFeedRetention(t, database)
	if unchanged.ReconciledAtMs != 1234 {
		t.Fatalf("unchanged window was reconciled: %+v", unchanged)
	}

	if err := manager.ApplyAndroidFeedRetention(2); err != nil {
		t.Fatal(err)
	}
	changed := requireAndroidFeedRetention(t, database)
	if changed.FeedDays != 2 {
		t.Fatalf("changed feed days = %d, want 2", changed.FeedDays)
	}
}

func TestApplyAndroidFeedRetentionRetriesAfterReconcileFailure(t *testing.T) {
	database := newTestWorkerDB(t)
	manager := &Manager{db: database}
	if err := database.RecordAndroidFeedRetention(1, 1234); err != nil {
		t.Fatal(err)
	}
	if err := database.ExecRaw(`ALTER TABLE feed_items RENAME TO feed_items_unavailable`); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyAndroidFeedRetention(2); err == nil {
		t.Fatal("reconciliation unexpectedly succeeded without feed_items")
	}
	failed := requireAndroidFeedRetention(t, database)
	if failed.FeedDays != 1 || failed.ReconciledAtMs != 1234 {
		t.Fatalf("failed reconciliation advanced state: %+v", failed)
	}
	if err := database.ExecRaw(`ALTER TABLE feed_items_unavailable RENAME TO feed_items`); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyAndroidFeedRetention(2); err != nil {
		t.Fatal(err)
	}
	retried := requireAndroidFeedRetention(t, database)
	if retried.FeedDays != 2 || retried.ReconciledAtMs <= failed.ReconciledAtMs {
		t.Fatalf("retry did not record success: %+v", retried)
	}
}

func TestFreshAndroidFeedRetentionDoesNotWaitForMediaMaintenance(t *testing.T) {
	database := newTestWorkerDB(t)
	manager := &Manager{db: database}
	if err := database.RecordAndroidFeedRetention(2, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	manager.xRetentionMu.Lock()
	defer manager.xRetentionMu.Unlock()
	done := make(chan error, 1)
	go func() { done <- manager.ApplyAndroidFeedRetention(2) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fresh Android feed retention waited for media maintenance")
	}
}

func requireAndroidFeedRetention(t *testing.T, database *db.DB) *db.AndroidFeedRetention {
	t.Helper()
	state, err := database.GetAndroidFeedRetention()
	if err != nil || state == nil {
		t.Fatalf("Android feed retention = %+v / %v", state, err)
	}
	return state
}
