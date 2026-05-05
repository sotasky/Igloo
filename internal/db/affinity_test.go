package db

import "testing"

func TestGetAccountAffinityScores(t *testing.T) {
	d := openWritableTestDB(t)
	scores, err := d.GetAccountAffinityScores("testuser", []string{"unknown_handle"})
	if err != nil {
		t.Fatalf("GetAccountAffinityScores: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("expected empty map, got %d entries", len(scores))
	}
}

func TestUpsertAndGetShareAccountAffinity(t *testing.T) {
	d := openWritableTestDB(t)
	err := d.UpsertShareAccountAffinity("testuser", "handle_a", 1.5, 1700000000000)
	if err != nil {
		t.Fatalf("UpsertShareAccountAffinity: %v", err)
	}
	scores, err := d.GetAccountAffinityScores("testuser", []string{"handle_a"})
	if err != nil {
		t.Fatalf("GetAccountAffinityScores: %v", err)
	}
	if len(scores) == 0 {
		t.Error("expected score for handle_a")
	}
}
