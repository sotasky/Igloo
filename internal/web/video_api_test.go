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

func TestHandleVideoWatchedWritesWatchHistory(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ExecRaw(
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration) VALUES (?, ?, ?, ?, ?)`,
		"vid_abc", "youtube_UCtest", "youtube_video", "Hello", 120,
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
		`SELECT playback_position FROM watch_history WHERE video_id = ?`,
		"vid_abc",
	).Scan(&pos)
	if err != nil {
		t.Fatalf("watch_history row missing: %v", err)
	}

	_ = mutationOwnerRevision(t, srv, "watch_history", "vid_abc")
}

func TestHandleShortsHistoryReadsAndroidMomentsCursor(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ApplyMomentsCursorMutation("short_android", 7890, 123456789, "all"); err != nil {
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

func TestHandleShortsHistoryReadsStoriesScope(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ApplyMomentsCursorMutation("story_cursor", 0, 123456789, "stories"); err != nil {
		t.Fatalf("ApplyMomentsCursorMutation: %v", err)
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
	if resp.VideoID != "story_cursor" || resp.UpdatedAtMs != 123456789 {
		t.Fatalf("stories history = video %q updated %d", resp.VideoID, resp.UpdatedAtMs)
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
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`,
		"tiktok_demo",
	); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 205; i++ {
		if err := srv.db.ExecRaw(
			`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
				 VALUES (?, ?, 'tiktok_video', ?, 0, ?)`,
			fmt.Sprintf("short_%03d", i), "tiktok_demo", fmt.Sprintf("Short %03d", i), i,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.db.ApplyMomentsCursorMutation("short_205", 0, 123456789, "all"); err != nil {
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

func TestHandleShortsHistoryFallsBackToNearestVisibleWhenCursorHidden(t *testing.T) {
	srv := newTestServer(t)

	for _, ch := range []string{"tiktok_alpha", "tiktok_beta"} {
		if err := srv.db.ExecRaw(
			`INSERT INTO channels (channel_id, name, platform) VALUES (?, ?, 'tiktok')`,
			ch, ch,
		); err != nil {
			t.Fatal(err)
		}
		if err := srv.db.ExecRaw(
			`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`,
			ch,
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		id        string
		channelID string
		published int
	}{
		{"alpha_old", "tiktok_alpha", 100},
		{"beta_cursor", "tiktok_beta", 200},
		{"alpha_new", "tiktok_alpha", 300},
	} {
		if err := srv.db.ExecRaw(
			`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
				 VALUES (?, ?, 'tiktok_video', ?, 0, ?)`,
			row.id, row.channelID, row.id, row.published,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.db.ApplyMomentsCursorMutation("beta_cursor", 0, 123456789, "all"); err != nil {
		t.Fatalf("ApplyMomentsCursorMutation: %v", err)
	}
	if err := srv.db.ExecRaw(`DELETE FROM channel_follows WHERE channel_id = 'tiktok_beta'`); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/shorts/history?tab=all", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		VideoID     string `json:"video_id"`
		Page        int    `json:"page"`
		Index       int    `json:"index"`
		PageSize    int    `json:"page_size"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.VideoID != "alpha_new" {
		t.Fatalf("fallback video_id=%q, want alpha_new", resp.VideoID)
	}
	if resp.Page != 1 || resp.Index != 1 || resp.PageSize != 10000 {
		t.Fatalf("fallback page hint = page %d index %d size %d, want page 1 index 1 size 10000", resp.Page, resp.Index, resp.PageSize)
	}
	if resp.UpdatedAtMs != 123456789 {
		t.Fatalf("updated_at_ms=%d, want original cursor timestamp", resp.UpdatedAtMs)
	}
}

func TestHandleShortsHistoryUsesStoredSortWhenRepostCursorBecomesFollowed(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("instagram_include_tagged_default", "true"); err != nil {
		t.Fatalf("SetSetting instagram_include_tagged_default: %v", err)
	}

	for _, row := range []struct {
		id       string
		platform string
		followed bool
	}{
		{"instagram_owner", "instagram", false},
		{"instagram_reposter", "instagram", true},
		{"tiktok_direct", "tiktok", true},
	} {
		if err := srv.db.ExecRaw(
			`INSERT INTO channels (channel_id, name, platform) VALUES (?, ?, ?)`,
			row.id, row.id, row.platform,
		); err != nil {
			t.Fatal(err)
		}
		if row.followed {
			if err := srv.db.ExecRaw(
				`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`,
				row.id,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, row := range []struct {
		id        string
		channelID string
		ownerKind string
		published int
	}{
		{"old_tagged_cursor", "instagram_owner", "instagram_reel", 100},
		{"direct_before", "tiktok_direct", "tiktok_video", 900},
		{"direct_after", "tiktok_direct", "tiktok_video", 1200},
	} {
		if err := srv.db.ExecRaw(
			`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
			 VALUES (?, ?, ?, ?, 0, ?)`,
			row.id, row.channelID, row.ownerKind, row.id, row.published,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms
		 ) VALUES (?, ?, 0, 1000, 1000)`,
		"old_tagged_cursor", "instagram_reposter",
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ApplyMomentsCursorMutation("old_tagged_cursor", 0, 123456789, "all"); err != nil {
		t.Fatalf("ApplyMomentsCursorMutation: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('instagram_owner', 2)`,
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/shorts/history?tab=all", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		VideoID            string `json:"video_id"`
		FallbackForVideoID string `json:"fallback_for_video_id"`
		Index              int    `json:"index"`
		SortAtMs           int64  `json:"sort_at_ms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.VideoID != "old_tagged_cursor" {
		t.Fatalf("video_id=%q, want old_tagged_cursor", resp.VideoID)
	}
	if resp.FallbackForVideoID != "" {
		t.Fatalf("fallback_for_video_id=%q, want empty", resp.FallbackForVideoID)
	}
	if resp.Index != 1 {
		t.Fatalf("index=%d, want 1", resp.Index)
	}
	if resp.SortAtMs != 1000 {
		t.Fatalf("sort_at_ms=%d, want 1000", resp.SortAtMs)
	}
}
