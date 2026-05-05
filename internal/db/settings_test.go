package db

import (
	"strings"
	"testing"
)

func TestGetSetting(t *testing.T) {
	d := openTestDB(t)
	val, err := d.GetSetting("nonexistent_key_xyz", "fallback")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if val != "fallback" {
		t.Errorf("expected fallback, got %q", val)
	}
}

func TestGetStats(t *testing.T) {
	d := openTestDB(t)
	stats, err := d.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalChannels < 0 {
		t.Errorf("negative channel count: %d", stats.TotalChannels)
	}
}

func TestGetAuthUsers(t *testing.T) {
	d := openTestDB(t)
	users, err := d.GetAuthUsers()
	if err != nil {
		t.Fatalf("GetAuthUsers: %v", err)
	}
	_ = users
}

func TestSetSetting(t *testing.T) {
	d := openWritableTestDB(t)
	err := d.SetSetting("", "test_key_xyz", "test_value")
	if err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	val, err := d.GetSetting("test_key_xyz", "")
	if err != nil {
		t.Fatalf("GetSetting after set: %v", err)
	}
	if val != "test_value" {
		t.Errorf("expected test_value, got %q", val)
	}
}

func TestRecordAndGetSyncChanges(t *testing.T) {
	d := openWritableTestDB(t)

	// Get current max version before inserting
	beforeVersion, _ := d.GetCurrentSyncVersion()

	err := d.RecordSyncChange("like", "tweet_abc", `{"liked":true}`)
	if err != nil {
		t.Fatalf("RecordSyncChange: %v", err)
	}

	// Query only changes since before our insert
	changes, _, err := d.GetSyncChanges(beforeVersion, 100)
	if err != nil {
		t.Fatalf("GetSyncChanges: %v", err)
	}
	found := false
	for _, c := range changes {
		if c.ItemID == "tweet_abc" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find tweet_abc in sync changes")
	}
}

func TestGetMutationSyncChangesFiltersInternalRows(t *testing.T) {
	d := openWritableTestDB(t)

	beforeVersion, _ := d.GetCurrentSyncVersion()
	if err := d.RecordSyncChange("media_ready", "asset_1", `{"ready":true}`); err != nil {
		t.Fatalf("RecordSyncChange media_ready: %v", err)
	}
	if err := d.RecordSyncChange("like", "tweet_delta", `{"action":"set"}`); err != nil {
		t.Fatalf("RecordSyncChange like: %v", err)
	}
	if err := d.RecordSyncChange("seen", "tweet_seen", `{"tweet_ids":["tweet_seen"]}`); err != nil {
		t.Fatalf("RecordSyncChange seen: %v", err)
	}

	if err := d.RecordSyncChange("bookmark_category", "7", `{"action":"set","category_id":7,"user_id":"alice"}`); err != nil {
		t.Fatalf("RecordSyncChange bookmark_category: %v", err)
	}

	changes, truncated, err := d.GetMutationSyncChanges("alice", beforeVersion, 1)
	if err != nil {
		t.Fatalf("GetMutationSyncChanges: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncated page")
	}
	if len(changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(changes))
	}
	if changes[0].Type != "like" || changes[0].ItemID != "tweet_delta" {
		t.Fatalf("first change = %s/%s, want like/tweet_delta", changes[0].Type, changes[0].ItemID)
	}
	if string(changes[0].Value) != `{"action":"set"}` {
		t.Fatalf("raw value = %s", string(changes[0].Value))
	}

	next, nextTruncated, err := d.GetMutationSyncChanges("alice", changes[0].Version, 500)
	if err != nil {
		t.Fatalf("GetMutationSyncChanges next: %v", err)
	}
	if nextTruncated {
		t.Fatal("did not expect second page to be truncated")
	}
	if len(next) != 2 || next[0].Type != "seen" || next[1].Type != "bookmark_category" {
		t.Fatalf("second page = %+v, want seen then bookmark_category", next)
	}
}

func TestGetMutationSyncChangesScopesBookmarkCategoryRows(t *testing.T) {
	d := openWritableTestDB(t)

	beforeVersion, _ := d.GetCurrentSyncVersion()
	aliceID, err := d.CreateBookmarkCategory("alice", "Archive", "/tmp/alice")
	if err != nil {
		t.Fatalf("CreateBookmarkCategory alice: %v", err)
	}
	bobID, err := d.CreateBookmarkCategory("bob", "Private Bob", "/tmp/bob")
	if err != nil {
		t.Fatalf("CreateBookmarkCategory bob: %v", err)
	}
	if err := d.UpdateBookmarkCategory("alice", aliceID, "Renamed", "/tmp/alice-renamed"); err != nil {
		t.Fatalf("UpdateBookmarkCategory alice: %v", err)
	}
	if err := d.DeleteBookmarkCategory("alice", aliceID); err != nil {
		t.Fatalf("DeleteBookmarkCategory alice: %v", err)
	}
	if err := d.DeleteBookmarkCategory("bob", bobID); err != nil {
		t.Fatalf("DeleteBookmarkCategory bob: %v", err)
	}

	changes, truncated, err := d.GetMutationSyncChanges("alice", beforeVersion, 2)
	if err != nil {
		t.Fatalf("GetMutationSyncChanges alice: %v", err)
	}
	if !truncated {
		t.Fatal("expected alice page to be truncated")
	}
	if len(changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2", len(changes))
	}
	for _, c := range changes {
		if c.Type != "bookmark_category" {
			t.Fatalf("change type = %q, want bookmark_category", c.Type)
		}
		if strings.Contains(string(c.Value), "Private Bob") || strings.Contains(string(c.Value), "/tmp/bob") {
			t.Fatalf("alice delta leaked bob category metadata: %s", string(c.Value))
		}
		if !strings.Contains(string(c.Value), `"user_id":"alice"`) {
			t.Fatalf("alice category delta missing user_id scope: %s", string(c.Value))
		}
	}

	next, nextTruncated, err := d.GetMutationSyncChanges("alice", changes[len(changes)-1].Version, 500)
	if err != nil {
		t.Fatalf("GetMutationSyncChanges alice next: %v", err)
	}
	if nextTruncated {
		t.Fatal("did not expect final alice page to be truncated")
	}
	if len(next) != 1 {
		t.Fatalf("next changes = %+v, want alice delete", next)
	}
	if !strings.Contains(string(next[0].Value), `"action":"clear"`) || !strings.Contains(string(next[0].Value), `"user_id":"alice"`) {
		t.Fatalf("delete delta = %s, want alice clear", string(next[0].Value))
	}
}
