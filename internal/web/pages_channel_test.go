package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestHandlePageChannelRendersProfileOnlyTikTok(t *testing.T) {
	srv := newTestServer(t)
	srv.staticV = func(path string) string { return "/static/" + path }
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_riki",
		Platform:    "tiktok",
		Handle:      "riki",
		DisplayName: "Riki",
		Bio:         "profile-only account",
		BannerURL:   "https://example.test/banner.jpg",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/channels/tiktok_riki", nil)
	req.SetPathValue("channelID", "tiktok_riki")
	rec := httptest.NewRecorder()
	srv.handlePageChannel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; location=%q", rec.Code, http.StatusOK, rec.Header().Get("Location"))
	}
	html := rec.Body.String()
	for _, want := range []string{
		`profile-card--hero`,
		`data-channel-id="tiktok_riki"`,
		`@riki`,
		`profile-only account`,
		`No channel videos found.`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered page missing %q\n%s", want, html)
		}
	}
	if strings.Contains(html, `hx-delete="/api/unsubscribe/tiktok_riki`) {
		t.Fatal("profile-only unfollowed channel should not render unsubscribe action")
	}
	assertActiveNav(t, html, "/channels")
	assertInactiveNav(t, html, "/videos")
}

func TestHandlePageChannelRendersTwitterChannelWithChannelsNavActive(t *testing.T) {
	srv := newTestServer(t)
	srv.staticV = func(path string) string { return "/static/" + path }
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_alice",
		Platform:    "twitter",
		Handle:      "alice",
		DisplayName: "Alice",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/channels/twitter_alice", nil)
	req.SetPathValue("channelID", "twitter_alice")
	rec := httptest.NewRecorder()
	srv.handlePageChannel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; location=%q", rec.Code, http.StatusOK, rec.Header().Get("Location"))
	}
	html := rec.Body.String()
	assertActiveNav(t, html, "/channels")
	assertInactiveNav(t, html, "/feed")
}

func TestHandlePageShortsStartsAtOldestMoment(t *testing.T) {
	srv := newTestServer(t)
	srv.staticV = func(path string) string { return "/static/" + path }
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
	for i := 1; i <= 3; i++ {
		videoID := "short_00" + string(rune('0'+i))
		if err := srv.db.ExecRaw(
			`INSERT INTO videos (video_id, channel_id, title, duration, published_at)
			 VALUES (?, ?, ?, 0, ?)`,
			videoID, "tiktok_demo", "Short 00"+string(rune('0'+i)), i,
		); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/shorts?tab=following", nil)
	rec := httptest.NewRecorder()
	srv.handlePageShorts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	html := rec.Body.String()
	oldest := strings.Index(html, `data-video-id="short_001"`)
	newest := strings.Index(html, `data-video-id="short_003"`)
	if newest < 0 || oldest < 0 {
		t.Fatalf("rendered page missing seeded shorts\n%s", html)
	}
	if oldest > newest {
		t.Fatalf("shorts order starts newest first; oldest index %d newest index %d\n%s", oldest, newest, html)
	}
}

func TestActiveNavForPathMapsRouteFamilies(t *testing.T) {
	tests := map[string]string{
		"/channels":              "channels",
		"/channels/tiktok_alice": "channels",
		"/videos":                "videos",
		"/player/video-1":        "videos",
		"/feed":                  "feed",
		"/shorts":                "shorts",
		"/bookmarks":             "bookmarks",
	}
	for path, want := range tests {
		if got := activeNavForPath(path); got != want {
			t.Fatalf("activeNavForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func assertActiveNav(t *testing.T, html, href string) {
	t.Helper()
	want := `href="` + href + `" class="nav-item active"`
	if !strings.Contains(html, want) {
		t.Fatalf("expected active nav %s\n%s", href, html)
	}
}

func assertInactiveNav(t *testing.T, html, href string) {
	t.Helper()
	bad := `href="` + href + `" class="nav-item active"`
	if strings.Contains(html, bad) {
		t.Fatalf("expected inactive nav %s\n%s", href, html)
	}
}
