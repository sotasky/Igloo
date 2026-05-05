package db

import "testing"

func TestGetAnalyticsRollups(t *testing.T) {
	d := openTestDB(t)
	rollups, err := d.GetAnalyticsRollups(10)
	if err != nil {
		t.Fatalf("GetAnalyticsRollups: %v", err)
	}
	_ = rollups
}

func TestGetAnalyticsRecentEvents(t *testing.T) {
	d := openTestDB(t)
	events, err := d.GetAnalyticsRecentEvents(10)
	if err != nil {
		t.Fatalf("GetAnalyticsRecentEvents: %v", err)
	}
	_ = events
}
