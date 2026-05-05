package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleShortsWatched_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/shorts/watched/9000000000000000001", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthed: got %d, want 401", rr.Code)
	}
}

func TestHandleShortsWatched_WritesMomentViewAndSyncChange(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/shorts/watched/9000000000000000001", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 — body: %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Success     bool  `json:"success"`
		SyncVersion int64 `json:"sync_version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Success {
		t.Error("success=false")
	}
	if body.SyncVersion <= 0 {
		t.Errorf("sync_version: got %d, want > 0", body.SyncVersion)
	}

	n, err := srv.db.CountMomentViews("alice")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("moment_views count: got %d, want 1", n)
	}
}

func TestHandleShortsWatchedList_ReturnsUserRowsOnly(t *testing.T) {
	srv := newTestServer(t)
	_, _ = srv.db.UpsertMomentView("alice", "A1")
	_, _ = srv.db.UpsertMomentView("alice", "A2")
	_, _ = srv.db.UpsertMomentView("bob", "B1")

	req := httptest.NewRequest("GET", "/api/shorts/watched", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	got := rr.Body.String()
	if !strings.Contains(got, "A1") || !strings.Contains(got, "A2") {
		t.Errorf("missing alice's rows: %s", got)
	}
	if strings.Contains(got, "B1") {
		t.Errorf("leaked bob's row: %s", got)
	}
}
