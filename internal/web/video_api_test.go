package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleVideoWatched_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)

	body := strings.NewReader(`{"watched":true}`)
	req := httptest.NewRequest("POST", "/api/videos/vid_x/watched", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthed: got %d, want 401", rr.Code)
	}
}

func TestHandleVideoWatched_WritesWatchHistoryAndSyncChange(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ExecRaw(
		`INSERT INTO videos (video_id, channel_id, title, duration) VALUES (?, ?, ?, ?)`,
		"vid_abc", "youtube_UCtest", "Hello", 120,
	); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"watched": true}`)
	req := httptest.NewRequest("POST", "/api/videos/vid_abc/watched", body)
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d — %s", rr.Code, rr.Body.String())
	}

	var pos float64
	err := srv.db.QueryRow(
		`SELECT playback_position FROM watch_history WHERE user_id = ? AND video_id = ?`,
		"alice", "vid_abc",
	).Scan(&pos)
	if err != nil {
		t.Fatalf("watch_history row missing: %v", err)
	}

	var syncType string
	err = srv.db.QueryRow(
		`SELECT type FROM sync_changes WHERE item_id = ? ORDER BY version DESC LIMIT 1`,
		"vid_abc",
	).Scan(&syncType)
	if err != nil {
		t.Fatalf("sync_changes row missing: %v", err)
	}
	if syncType != "video_watched" {
		t.Errorf("sync type: got %q, want video_watched", syncType)
	}

	var resp struct {
		Success     bool  `json:"success"`
		SyncVersion int64 `json:"sync_version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.SyncVersion <= 0 {
		t.Errorf("sync_version=%d, want > 0", resp.SyncVersion)
	}
}

func TestHandleShortsHistoryReadsAndroidMomentsCursor(t *testing.T) {
	srv := newTestServer(t)

	if _, err := srv.db.ApplyMomentsCursorMutation("alice", "short_android", 7890, 123456789, "all"); err != nil {
		t.Fatalf("ApplyMomentsCursorMutation: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/shorts/history", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		VideoID     string `json:"video_id"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.VideoID != "short_android" {
		t.Fatalf("video_id=%q, want short_android", resp.VideoID)
	}
	if resp.UpdatedAtMs != 123456789 {
		t.Fatalf("updated_at_ms=%d, want 123456789", resp.UpdatedAtMs)
	}
}

func TestHandleShortsHistoryIgnoresStoriesCursor(t *testing.T) {
	srv := newTestServer(t)

	if _, err := srv.db.ApplyMomentsCursorMutation("alice", "story_cursor", 0, 123456789, "stories"); err != nil {
		t.Fatalf("ApplyMomentsCursorMutation: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
		"shorts_cursor_video_id_alice_stories", "stale_story_cursor",
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
		"shorts_cursor_updated_at_ms_alice_stories", "123456789",
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/shorts/history?tab=stories", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		VideoID     string `json:"video_id"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.VideoID != "" || resp.UpdatedAtMs != 0 {
		t.Fatalf("stories history = video %q updated %d, want empty cursor", resp.VideoID, resp.UpdatedAtMs)
	}

	var stored int
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM settings WHERE key = 'shorts_cursor_video_id_alice_stories' AND value = 'story_cursor'`,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("stories mutation wrote resume cursor")
	}
}

func TestHandleShortsHistoryReturnsPageHint(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ExecRaw(
		`INSERT INTO channels (channel_id, name, platform) VALUES (?, ?, ?)`,
		"tiktok_demo", "Demo", "tiktok",
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', ?, 1)`,
		"tiktok_demo",
	); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 205; i++ {
		if err := srv.db.ExecRaw(
			`INSERT INTO videos (video_id, channel_id, title, duration, published_at)
			 VALUES (?, ?, ?, 0, ?)`,
			fmt.Sprintf("short_%03d", i), "tiktok_demo", fmt.Sprintf("Short %03d", i), i,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.db.ApplyMomentsCursorMutation("alice", "short_205", 0, 123456789, "all"); err != nil {
		t.Fatalf("ApplyMomentsCursorMutation: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/shorts/history", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		VideoID  string `json:"video_id"`
		Page     int    `json:"page"`
		Index    int    `json:"index"`
		PageSize int    `json:"page_size"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.VideoID != "short_205" {
		t.Fatalf("video_id=%q, want short_205", resp.VideoID)
	}
	if resp.Page != 1 || resp.Index != 204 || resp.PageSize != 10000 {
		t.Fatalf("page hint = page %d index %d size %d, want page 1 index 204 size 10000", resp.Page, resp.Index, resp.PageSize)
	}
}
