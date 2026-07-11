package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebMomentsCursorUsesClientTimestampForLWW(t *testing.T) {
	srv := newTestServer(t)

	if rr := do(t, srv, "POST", "/api/sync/moments-cursor", strings.NewReader(`{
	  "video_id": "moment_newer",
	  "scope": "all",
	  "updated_at_ms": 2000,
	  "sort_at_ms": 20000
	}`), "alice"); rr.Code != http.StatusOK {
		t.Fatalf("newer cursor: %d - %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, srv, "POST", "/api/sync/moments-cursor", strings.NewReader(`{
	  "video_id": "moment_older",
	  "scope": "all",
	  "updated_at_ms": 1000,
	  "sort_at_ms": 10000
	}`), "alice"); rr.Code != http.StatusConflict {
		t.Fatalf("older cursor status = %d, want 409: %s", rr.Code, rr.Body.String())
	}

	var videoID string
	var updatedAt int64
	if err := srv.db.QueryRow(`SELECT video_id, updated_at_ms FROM moments_cursors WHERE scope = 'all'`).Scan(&videoID, &updatedAt); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if videoID != "moment_newer" || updatedAt != 2000 {
		t.Fatalf("cursor = (%q, %d), want newer client timestamp", videoID, updatedAt)
	}
}

func do(t *testing.T, srv *testServer, method, path string, body io.Reader, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req = attachTestAuth(req, user)
	}
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	return rr
}
