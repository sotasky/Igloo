package db

import (
	"testing"
	"time"
)

func TestUpsertMomentView_InsertsAndIsIdempotent(t *testing.T) {
	d := openWritableTestDB(t)

	ts1, err := d.UpsertMomentView("alice", "9000000000000000001")
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if ts1.IsZero() {
		t.Error("expected non-zero viewed_at on insert")
	}

	time.Sleep(10 * time.Millisecond)
	ts2, err := d.UpsertMomentView("alice", "9000000000000000001")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if ts2.Before(ts1) {
		t.Errorf("second viewed_at %v should be >= first %v", ts2, ts1)
	}
}

func TestListMomentViews_FiltersByUserAndRespectsSince(t *testing.T) {
	d := openWritableTestDB(t)

	if _, err := d.UpsertMomentView("alice", "A1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertMomentView("alice", "A2"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertMomentView("bob", "B1"); err != nil {
		t.Fatal(err)
	}

	got, err := d.ListMomentViews("alice", time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListMomentViews: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("alice: got %d rows, want 2", len(got))
	}

	gotBob, _ := d.ListMomentViews("bob", time.Time{}, 100)
	if len(gotBob) != 1 {
		t.Errorf("bob: got %d rows, want 1", len(gotBob))
	}

	future := time.Now().Add(1 * time.Hour)
	gotFuture, _ := d.ListMomentViews("alice", future, 100)
	if len(gotFuture) != 0 {
		t.Errorf("future since: got %d rows, want 0", len(gotFuture))
	}
}

func TestCountMomentViews(t *testing.T) {
	d := openWritableTestDB(t)
	for _, id := range []string{"x1", "x2", "x3"} {
		if _, err := d.UpsertMomentView("alice", id); err != nil {
			t.Fatal(err)
		}
	}
	n, err := d.CountMomentViews("alice")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("count: got %d, want 3", n)
	}
}
