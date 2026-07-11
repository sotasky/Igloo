package db

import "testing"

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
	if err := d.SetSetting("test_key_xyz", "test_value"); err != nil {
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
