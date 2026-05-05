package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
)

type fakeFetch struct {
	mu     sync.Mutex
	calls  atomic.Int32
	result *fetchprofile.Profile
	err    error
	delay  time.Duration
}

func (f *fakeFetch) Fetch(ctx context.Context, channelID string) (*fetchprofile.Profile, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func waitForFetchCalls(t *testing.T, f *fakeFetch, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f.calls.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least %d fetch calls, got %d", want, f.calls.Load())
}

func TestProfileCardFreshRow(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC()
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_alice",
		Platform:    "twitter",
		Handle:      "alice",
		DisplayName: "Alice",
		Followers:   10,
		FetchedAt:   &now,
	})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_alice", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Alice") {
		t.Fatalf("bad resp: %d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Igloo-Profile-Refreshing") != "" {
		t.Fatalf("fresh row should not be marked refreshing")
	}
}

func TestProfileCardFollowStateUsesFollowSideTableWithoutChannelRow(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC()
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_followed_only",
		Platform:    "twitter",
		Handle:      "followed_only",
		DisplayName: "Followed Only",
		FetchedAt:   &now,
	})
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'twitter_followed_only', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_followed_only", nil))
	body := rr.Body.String()
	if rr.Code != 200 || !strings.Contains(body, `data-following="1"`) {
		t.Fatalf("profile card did not render followed state: %d %s", rr.Code, body)
	}
}

func TestProfileCardFollowedHoverIncludesStarAndMenu(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC()
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_followed",
		Platform:    "tiktok",
		Handle:      "followed",
		DisplayName: "Followed",
		FetchedAt:   &now,
	})
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'tiktok_followed', 1)
	`); err != nil {
		t.Fatalf("seed follow: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_stars (user_id, channel_id, starred_at)
		VALUES ('', 'tiktok_followed', 1)
	`); err != nil {
		t.Fatalf("seed star: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/tiktok_followed", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`data-following="1"`,
		`/api/channels/tiktok_followed/star?ctx=profile`,
		`data-profile-card-menu`,
		`data-profile-card-menu-action="retweets_off"`,
		`Turn off reposts`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in profile card: %d %s", want, rr.Code, body)
		}
	}
}

func TestProfileCardLazyFetch(t *testing.T) {
	srv := newTestServer(t)
	f := &fakeFetch{result: &fetchprofile.Profile{
		ChannelID:   "twitter_bob",
		Platform:    "twitter",
		Handle:      "bob",
		DisplayName: "Bob",
		Followers:   7,
	}}
	srv.profileFetch = f.Fetch

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_bob", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Bob") {
		t.Fatalf("bad resp: %d body=%s", rr.Code, rr.Body.String())
	}
	if f.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 fetch call, got %d", f.calls.Load())
	}
}

func TestProfileCardLazyFetchQueuesProfileMedia(t *testing.T) {
	srv := newTestServer(t)
	f := &fakeFetch{result: &fetchprofile.Profile{
		ChannelID:   "twitter_mediaqueue",
		Platform:    "twitter",
		Handle:      "mediaqueue",
		DisplayName: "Media Queue",
		Bio:         "card body",
		BannerURL:   "https://cdn.example/banner.jpg",
	}}
	srv.profileFetch = f.Fetch
	var queued string
	srv.requestAvatar = func(channelID string) {
		queued = channelID
	}

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_mediaqueue", nil))
	if rr.Code != 200 {
		t.Fatalf("bad resp: %d body=%s", rr.Code, rr.Body.String())
	}
	if queued != "twitter_mediaqueue" {
		t.Fatalf("queued channel = %q, want twitter_mediaqueue", queued)
	}
}

func TestProfileCardTombstoned404(t *testing.T) {
	srv := newTestServer(t)
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_ghost",
		Platform:  "twitter",
		Tombstone: true,
	})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_ghost", nil))
	if rr.Code != 404 {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestProfileCardInvalidChannelID400(t *testing.T) {
	srv := newTestServer(t)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/bad", nil))
	if rr.Code != 400 {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestProfileCardTikTok(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC()
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_kei",
		Platform:    "tiktok",
		Handle:      "kei",
		DisplayName: "Kei",
		Bio:         "test",
		FetchedAt:   &now,
	})
	srv.profileFetch = (&fakeFetch{result: &fetchprofile.Profile{
		ChannelID:   "tiktok_kei",
		Platform:    "tiktok",
		Handle:      "kei",
		DisplayName: "Kei",
		Bio:         "test",
	}}).Fetch
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/tiktok_kei", nil))
	if rr.Code != 200 {
		t.Fatalf("bad resp: %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Kei") {
		t.Fatalf("missing nickname: %s", body)
	}
	if !strings.Contains(body, "profile-card--no-banner") {
		t.Fatalf("tiktok card should carry no-banner class, body: %s", body)
	}
	if strings.Contains(body, "Followers") || strings.Contains(body, "Following") {
		t.Fatalf("tiktok card should not render stats, body: %s", body)
	}
}

func TestProfileCardInstagram(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC()
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_kei",
		Platform:    "instagram",
		Handle:      "kei",
		DisplayName: "Kei",
		Bio:         "test",
		FetchedAt:   &now,
	})
	srv.profileFetch = (&fakeFetch{result: &fetchprofile.Profile{
		ChannelID:   "instagram_kei",
		Platform:    "instagram",
		Handle:      "kei",
		DisplayName: "Kei",
		Bio:         "test",
	}}).Fetch
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/instagram_kei", nil))
	if rr.Code != 200 {
		t.Fatalf("bad resp: %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Kei") {
		t.Fatalf("missing nickname: %s", body)
	}
	if !strings.Contains(body, `profile-card--instagram`) {
		t.Fatalf("instagram card should carry platform class, body: %s", body)
	}
}

func TestProfileCardInflightDedup(t *testing.T) {
	srv := newTestServer(t)
	f := &fakeFetch{
		result: &fetchprofile.Profile{
			ChannelID:   "twitter_carol",
			Platform:    "twitter",
			Handle:      "carol",
			DisplayName: "Carol",
		},
		delay: 80 * time.Millisecond,
	}
	srv.profileFetch = f.Fetch

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_carol", nil))
		}()
	}
	wg.Wait()
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("dedup failed: %d fetches (expected 1)", n)
	}
}

func TestProfileCardFetchErrorWithStaleRow(t *testing.T) {
	srv := newTestServer(t)
	stale := time.Now().Add(-48 * time.Hour).UTC()
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_dana",
		Platform:    "twitter",
		Handle:      "dana",
		DisplayName: "Dana",
		FetchedAt:   &stale,
	})
	f := &fakeFetch{err: errors.New("upstream down")}
	srv.profileFetch = f.Fetch

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_dana", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Dana") {
		t.Fatalf("expected stale fallback render, got: %d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Igloo-Profile-Refreshing") != "1" {
		t.Fatalf("expected stale row to be marked refreshing")
	}
	waitForFetchCalls(t, f, 1)
}

func TestProfileCardStaleRowDoesNotBlockOnRefresh(t *testing.T) {
	srv := newTestServer(t)
	stale := time.Now().Add(-48 * time.Hour).UTC()
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_erin",
		Platform:    "twitter",
		Handle:      "erin",
		DisplayName: "Erin",
		FetchedAt:   &stale,
	})
	release := make(chan struct{})
	var calls atomic.Int32
	srv.profileFetch = func(ctx context.Context, channelID string) (*fetchprofile.Profile, error) {
		calls.Add(1)
		select {
		case <-release:
			return &fetchprofile.Profile{
				ChannelID:   "twitter_erin",
				Platform:    "twitter",
				Handle:      "erin",
				DisplayName: "Erin Fresh",
			}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	defer close(release)

	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/twitter_erin", nil))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("stale profile-card render blocked on refresh")
	}
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Erin") {
		t.Fatalf("expected stale profile render, got: %d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Igloo-Profile-Refreshing") != "1" {
		t.Fatalf("expected stale row to be marked refreshing")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected background refresh to start once, got %d", calls.Load())
}

func TestRefreshOneProfileSynthesizesTikTokBanner(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: "tiktok_casey",
		Platform:  "tiktok",
		Name:      "Casey",
		URL:       "https://tiktok/@casey",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(srv.cfg.DataDir, "media", "tiktok", "casey")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	videoPath := filepath.Join(mediaDir, "clip1.mp4")
	if err := os.WriteFile(videoPath, []byte("mp4-placeholder"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	thumbPath := filepath.Join(mediaDir, "clip1.image")
	jpeg := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
	}
	if err := os.WriteFile(thumbPath, jpeg, 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}
	if err := srv.db.InsertVideo(
		"clip1", "tiktok_casey", "hello", "",
		0, "", "media/tiktok/casey/clip1.mp4", 0,
		1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	srv.profileFetch = (&fakeFetch{result: &fetchprofile.Profile{
		ChannelID:   "tiktok_casey",
		Platform:    "tiktok",
		Handle:      "casey",
		DisplayName: "Casey",
	}}).Fetch

	srv.refreshOneProfile(context.Background(), "tiktok_casey", nil)

	got, err := srv.db.GetChannelProfile("tiktok_casey")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.BannerURL != "synth:latest-video:clip1" {
		t.Fatalf("BannerURL = %q, want synth:latest-video:clip1", got.BannerURL)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "thumbnails", "banners", "tiktok_casey.jpg")); err != nil {
		t.Fatalf("expected synthesized banner file: %v", err)
	}
}

func TestRefreshOneProfileSynthesizesInstagramBanner(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: "instagram_casey",
		Platform:  "instagram",
		Name:      "Casey",
		URL:       "https://instagram.com/casey",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(srv.cfg.DataDir, "media", "instagram", "casey")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	videoPath := filepath.Join(mediaDir, "clip1.mp4")
	if err := os.WriteFile(videoPath, []byte("mp4-placeholder"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	jpeg := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "clip1.image"), jpeg, 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}
	if err := srv.db.InsertVideo(
		"clip1", "instagram_casey", "hello", "",
		0, "", "media/instagram/casey/clip1.mp4", 0,
		1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	srv.profileFetch = (&fakeFetch{result: &fetchprofile.Profile{
		ChannelID:   "instagram_casey",
		Platform:    "instagram",
		Handle:      "casey",
		DisplayName: "Casey",
	}}).Fetch

	srv.refreshOneProfile(context.Background(), "instagram_casey", nil)

	got, err := srv.db.GetChannelProfile("instagram_casey")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.BannerURL != "synth:latest-video:clip1" {
		t.Fatalf("BannerURL = %q, want synth:latest-video:clip1", got.BannerURL)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "thumbnails", "banners", "instagram_casey.jpg")); err != nil {
		t.Fatalf("expected synthesized instagram banner file: %v", err)
	}
}

func TestRefreshOneProfilePreservesInstagramMetadataFromStubFetch(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC().Add(-2 * time.Hour)
	existing := model.ChannelProfile{
		ChannelID:    "instagram_casey",
		Platform:     "instagram",
		Handle:       "casey",
		DisplayName:  "Casey Rich",
		Bio:          "stored bio",
		Website:      "https://example.test",
		Followers:    42,
		Following:    7,
		Verified:     true,
		VerifiedType: "blue",
		AvatarURL:    "https://cdn.example/avatar.jpg",
		BannerURL:    "synth:latest-video:old",
		FetchedAt:    &now,
	}
	if err := srv.db.UpsertChannelProfile(existing); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	srv.profileFetch = fetchprofile.Fetch

	srv.refreshOneProfile(context.Background(), "instagram_casey", &existing)

	got, err := srv.db.GetChannelProfile("instagram_casey")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.DisplayName != "Casey Rich" || got.Bio != "stored bio" || got.Website != "https://example.test" {
		t.Fatalf("profile metadata was clobbered: %+v", got)
	}
	if got.Followers != 42 || got.Following != 7 || !got.Verified || got.VerifiedType != "blue" {
		t.Fatalf("profile stats were clobbered: %+v", got)
	}
	if got.AvatarURL != "https://cdn.example/avatar.jpg" || got.BannerURL != "synth:latest-video:old" {
		t.Fatalf("profile media was clobbered: %+v", got)
	}
}

func TestProfileCardFreshTikTokWithoutBannerRendersBeforeRefresh(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC()
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: "tiktok_drew",
		Platform:  "tiktok",
		Name:      "Drew",
		URL:       "https://tiktok/@drew",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(srv.cfg.DataDir, "media", "tiktok", "drew")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "clip1.mp4"), []byte("mp4-placeholder"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	jpeg := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "clip1.image"), jpeg, 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}
	if err := srv.db.InsertVideo(
		"clip1", "tiktok_drew", "hello", "",
		0, "", "media/tiktok/drew/clip1.mp4", 0,
		1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	_ = srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_drew",
		Platform:    "tiktok",
		Handle:      "drew",
		DisplayName: "Drew",
		FetchedAt:   &now,
	})
	f := &fakeFetch{result: &fetchprofile.Profile{
		ChannelID:   "tiktok_drew",
		Platform:    "tiktok",
		Handle:      "drew",
		DisplayName: "Drew",
	}}
	srv.profileFetch = f.Fetch

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/profile-card/tiktok_drew", nil))
	if rr.Code != 200 {
		t.Fatalf("bad resp: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "profile-card--no-banner") {
		t.Fatalf("expected immediate no-banner hover card before refresh, body: %s", rr.Body.String())
	}
	if rr.Header().Get("X-Igloo-Profile-Refreshing") != "1" {
		t.Fatalf("expected incomplete TikTok row to be marked refreshing")
	}
	waitForFetchCalls(t, f, 1)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := srv.db.GetChannelProfile("tiktok_drew")
		if err == nil && got != nil && got.BannerURL == "synth:latest-video:clip1" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := srv.db.GetChannelProfile("tiktok_drew")
	t.Fatalf("expected background refresh to synthesize banner, got %+v", got)
}
