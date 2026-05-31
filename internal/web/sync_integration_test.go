package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSyncRoundTrip_AllPhase1Endpoints(t *testing.T) {
	srv := newTestServer(t)

	if rr := do(t, srv, "POST", "/api/shorts/watched/shortid_1", nil, "alice"); rr.Code != http.StatusOK {
		t.Fatalf("shorts/watched: %d — %s", rr.Code, rr.Body.String())
	}

	if err := srv.db.ExecRaw(
		`INSERT INTO videos (video_id, channel_id, title, duration) VALUES (?, ?, ?, ?)`,
		"vid_1", "youtube_UC1", "t", 60,
	); err != nil {
		t.Fatal(err)
	}
	if rr := do(t, srv, "POST", "/api/videos/vid_1/watched", strings.NewReader(`{"watched":true}`), "alice"); rr.Code != http.StatusOK {
		t.Fatalf("video/watched: %d — %s", rr.Code, rr.Body.String())
	}

	if rr := do(t, srv, "POST", "/api/feed/mute/bob", nil, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("mute unauthed: %d, want 401", rr.Code)
	}
	if rr := do(t, srv, "POST", "/api/feed/mute/bob", nil, "alice"); rr.Code != http.StatusOK {
		t.Fatalf("mute authed: %d — %s", rr.Code, rr.Body.String())
	}

	if rr := do(t, srv, "GET", "/api/feed/bookmarked", nil, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("bookmarked unauthed: %d, want 401", rr.Code)
	}

	// Baseline ping (since<=0) returns version only.
	rr := do(t, srv, "GET", "/api/sync/changes?since=0", nil, "alice")
	if rr.Code != http.StatusOK {
		t.Fatalf("sync/changes baseline: %d — %s", rr.Code, rr.Body.String())
	}
	var baseline struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline.Version < 2 {
		t.Errorf("baseline version: got %d, want >= 2 (moment_view + video_watched)", baseline.Version)
	}

	// Direct DB inspection catches every row regardless of the since-window.
	changes, _, err := srv.db.GetSyncChanges(0, 500)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range changes {
		seen[c.Type] = true
	}
	for _, want := range []string{"moment_view", "video_watched"} {
		if !seen[want] {
			t.Errorf("missing sync_change type %q (all types: %v)", want, seen)
		}
	}
}

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
	}`), "alice"); rr.Code != http.StatusOK {
		t.Fatalf("older cursor: %d - %s", rr.Code, rr.Body.String())
	}

	var videoID, updatedAt string
	if err := srv.db.QueryRow(`SELECT value FROM settings WHERE key = 'shorts_cursor_video_id_alice_all'`).Scan(&videoID); err != nil {
		t.Fatalf("read cursor video: %v", err)
	}
	if err := srv.db.QueryRow(`SELECT value FROM settings WHERE key = 'shorts_cursor_updated_at_ms_alice_all'`).Scan(&updatedAt); err != nil {
		t.Fatalf("read cursor timestamp: %v", err)
	}
	if videoID != "moment_newer" || updatedAt != "2000" {
		t.Fatalf("cursor = (%q, %q), want newer client timestamp", videoID, updatedAt)
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
