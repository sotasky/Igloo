package web

import (
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

func TestHandleShortsWatchedUsesMomentViewOwner(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/shorts/watched/9000000000000000001", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 — body: %s", rr.Code, rr.Body.String())
	}

	_ = mutationOwnerRevision(t, srv, "moment_view", "9000000000000000001")

	n, err := srv.db.CountMomentViews()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("moment_views count: got %d, want 1", n)
	}
}

func TestHandleShortsWatchedList_ReturnsRows(t *testing.T) {
	srv := newTestServer(t)
	_, _ = srv.db.UpsertMomentView("A1")
	_, _ = srv.db.UpsertMomentView("A2")

	req := httptest.NewRequest("GET", "/api/shorts/watched", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	got := rr.Body.String()
	if !strings.Contains(got, "A1") || !strings.Contains(got, "A2") {
		t.Errorf("missing watched rows: %s", got)
	}
}
