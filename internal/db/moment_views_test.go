package db

import (
	"database/sql"
	"testing"
	"time"
)

func TestUpsertMomentView_InsertsAndIsIdempotent(t *testing.T) {
	d := openWritableTestDB(t)

	ts1, err := d.UpsertMomentView("9000000000000000001")
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if ts1.IsZero() {
		t.Error("expected non-zero viewed_at on insert")
	}

	time.Sleep(10 * time.Millisecond)
	ts2, err := d.UpsertMomentView("9000000000000000001")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if ts2.Before(ts1) {
		t.Errorf("second viewed_at %v should be >= first %v", ts2, ts1)
	}
}

func TestUpsertMomentViewUsesSerializedWritePath(t *testing.T) {
	d := openWritableTestDB(t)

	writerEntered := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- d.WithWrite(func(_ *sql.Tx) error {
			close(writerEntered)
			<-releaseWriter
			return nil
		})
	}()

	select {
	case <-writerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not enter serialized section")
	}

	upsertDone := make(chan error, 1)
	go func() {
		_, err := d.UpsertMomentView("serialized_view")
		upsertDone <- err
	}()

	select {
	case err := <-upsertDone:
		close(releaseWriter)
		if err != nil {
			t.Fatalf("upsert returned before serialized writer released: %v", err)
		}
		t.Fatal("upsert completed before serialized writer released")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseWriter)

	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatalf("held writer: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held writer did not finish")
	}

	select {
	case err := <-upsertDone:
		if err != nil {
			t.Fatalf("upsert after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upsert did not finish after serialized writer released")
	}
}

func TestListMomentViewsRespectsSince(t *testing.T) {
	d := openWritableTestDB(t)

	if _, err := d.UpsertMomentView("A1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertMomentView("A2"); err != nil {
		t.Fatal(err)
	}
	got, err := d.ListMomentViews(time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListMomentViews: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want 2", len(got))
	}

	future := time.Now().Add(1 * time.Hour)
	gotFuture, _ := d.ListMomentViews(future, 100)
	if len(gotFuture) != 0 {
		t.Errorf("future since: got %d rows, want 0", len(gotFuture))
	}
}

func TestCountMomentViews(t *testing.T) {
	d := openWritableTestDB(t)
	for _, id := range []string{"x1", "x2", "x3"} {
		if _, err := d.UpsertMomentView(id); err != nil {
			t.Fatal(err)
		}
	}
	n, err := d.CountMomentViews()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("count: got %d, want 3", n)
	}
}
