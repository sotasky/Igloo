package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
)

type fakeFetcher struct {
	mu      sync.Mutex
	calls   map[string]int
	results map[string]*fetchprofile.Profile
	errs    map[string]error
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		calls:   map[string]int{},
		results: map[string]*fetchprofile.Profile{},
		errs:    map[string]error{},
	}
}

func (f *fakeFetcher) Fetch(_ context.Context, channelID string) (*fetchprofile.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[channelID]++
	if e, ok := f.errs[channelID]; ok {
		return nil, e
	}
	if p, ok := f.results[channelID]; ok {
		return p, nil
	}
	return nil, fetchprofile.ErrNotFound
}

type twimgAvatarTestTransport struct {
	srvURL string
}

func (t *twimgAvatarTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = t.srvURL
	return http.DefaultTransport.RoundTrip(req2)
}

func testTwimgAvatarDownloader(srv *httptest.Server) *download.Downloader {
	return &download.Downloader{
		HTTP: &download.HTTPDownloader{
			Client:            &http.Client{Transport: &twimgAvatarTestTransport{srvURL: srv.Listener.Addr().String()}},
			AllowPrivateHosts: true,
		},
	}
}

func TestRefreshProfileUpsertsRow(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	f := newFakeFetcher()
	f.results["twitter_alice"] = &fetchprofile.Profile{
		ChannelID:   "twitter_alice",
		Platform:    "twitter",
		Handle:      "alice",
		DisplayName: "Alice",
		Followers:   100,
		Following:   50,
	}
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_alice", Platform: "twitter"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "twitter_alice", avDir, bnDir)
	got, _ := d.GetChannelProfile("twitter_alice")
	if got == nil {
		t.Fatalf("row missing")
	}
	if got.DisplayName != "Alice" || got.Followers != 100 {
		t.Fatalf("not updated: %+v", got)
	}
	if got.FetchedAt == nil {
		t.Fatalf("fetched_at not set")
	}
}

func TestRefreshProfileSeedsBioMentions(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	f := newFakeFetcher()
	f.results["tiktok_artist"] = &fetchprofile.Profile{
		ChannelID:   "tiktok_artist",
		Platform:    "tiktok",
		Handle:      "artist",
		DisplayName: "Artist",
		Bio:         "with @Guest.One and email a@b.com",
	}
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "tiktok_artist", Platform: "tiktok"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "tiktok_artist", avDir, bnDir)

	got, err := d.GetChannelProfile("tiktok_guest.one")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile(tiktok_guest.one): %v / %+v", err, got)
	}
	if got.Platform != "tiktok" || got.Handle != "guest.one" {
		t.Fatalf("profile row mismatch: %+v", got)
	}
	if skipped, _ := d.GetChannelProfile("twitter_guest.one"); skipped != nil {
		t.Fatalf("bio mention should not be seeded as twitter: %+v", skipped)
	}
	if skipped, _ := d.GetChannelProfile("tiktok_b"); skipped != nil {
		t.Fatalf("email domain mention should not be seeded: %+v", skipped)
	}
}

func TestRefreshProfileTombstonesOnNotFound(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	f := newFakeFetcher()
	f.errs["twitter_ghost"] = fetchprofile.ErrNotFound
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_ghost", Platform: "twitter"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "twitter_ghost", avDir, bnDir)
	got, _ := d.GetChannelProfile("twitter_ghost")
	if got == nil {
		t.Fatalf("row missing")
	}
	if !got.Tombstone {
		t.Fatalf("expected tombstone, got %+v", got)
	}
}

func TestRefreshProfileBackoffOnTransientError(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	f := newFakeFetcher()
	f.errs["twitter_flaky"] = errTransient
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_flaky", Platform: "twitter"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "twitter_flaky", avDir, bnDir)
	got, _ := d.GetChannelProfile("twitter_flaky")
	if got == nil {
		t.Fatalf("row missing")
	}
	if got.Tombstone {
		t.Fatalf("transient should not tombstone")
	}
	if got.FailCount == 0 || got.NextRetryAt == nil {
		t.Fatalf("backoff not applied: %+v", got)
	}
	if !got.NextRetryAt.After(time.Now()) {
		t.Fatalf("next_retry_at should be in future")
	}
}

func TestRefreshProfileDoesNotBackoffOnCanceledContext(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	f := newFakeFetcher()
	f.errs["twitter_cancelled"] = context.Canceled
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "twitter_cancelled", Platform: "twitter"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "twitter_cancelled", avDir, bnDir)
	got, _ := d.GetChannelProfile("twitter_cancelled")
	if got == nil {
		t.Fatalf("row missing")
	}
	if got.FailCount != 0 || got.NextRetryAt != nil || got.Tombstone {
		t.Fatalf("canceled fetch should not back off profile row: %+v", got)
	}
}

func TestCanDownloadStoredAvatarRejectsUnsafeNonTwitterURLs(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		avatarURL string
		want      bool
	}{
		{name: "empty", channelID: "instagram_a", avatarURL: "", want: false},
		{name: "localhost", channelID: "instagram_a", avatarURL: "http://localhost/avatar.jpg", want: false},
		{name: "localhost trailing dot", channelID: "instagram_a", avatarURL: "http://localhost./avatar.jpg", want: false},
		{name: "localhost suffix", channelID: "instagram_a", avatarURL: "https://foo.localhost/a.jpg", want: false},
		{name: "loopback ip", channelID: "instagram_a", avatarURL: "http://127.0.0.1/a.jpg", want: false},
		{name: "private ip", channelID: "instagram_a", avatarURL: "http://10.0.0.2/a.jpg", want: false},
		{name: "link local ip", channelID: "instagram_a", avatarURL: "http://169.254.169.254/latest/meta-data", want: false},
		{name: "unresolved hostname", channelID: "instagram_a", avatarURL: "https://does-not-resolve.invalid/a.jpg", want: false},
		{name: "public ip", channelID: "instagram_a", avatarURL: "https://93.184.216.34/a.jpg", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := canDownloadStoredAvatar(tc.channelID, tc.avatarURL); got != tc.want {
				t.Fatalf("canDownloadStoredAvatar(%q, %q)=%v want %v", tc.channelID, tc.avatarURL, got, tc.want)
			}
		})
	}
}

func TestCanDownloadStoredAvatarRejectsHostnameWithUnsafeDNS(t *testing.T) {
	oldLookup := lookupStoredMediaHost
	t.Cleanup(func() { lookupStoredMediaHost = oldLookup })
	lookupStoredMediaHost = func(host string) ([]netip.Addr, error) {
		switch host {
		case "cdn.example":
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		case "metadata.example":
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		case "mixed.example":
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("127.0.0.1"),
			}, nil
		default:
			t.Fatalf("unexpected DNS lookup for %q", host)
			return nil, nil
		}
	}

	tests := []struct {
		name      string
		avatarURL string
		want      bool
	}{
		{name: "public hostname", avatarURL: "https://cdn.example/a.jpg", want: true},
		{name: "metadata hostname", avatarURL: "https://metadata.example/a.jpg", want: false},
		{name: "mixed public and loopback hostname", avatarURL: "https://mixed.example/a.jpg", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := canDownloadStoredAvatar("instagram_a", tc.avatarURL); got != tc.want {
				t.Fatalf("canDownloadStoredAvatar(%q)=%v want %v", tc.avatarURL, got, tc.want)
			}
		})
	}
}

func TestShouldRefreshFullProfileSkipsInstagram(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * profileTTL)
	if shouldRefreshFullProfile("instagram_cinema", &model.ChannelProfile{
		ChannelID: "instagram_cinema",
		Platform:  "instagram",
		FetchedAt: &old,
	}, now) {
		t.Fatal("instagram profile refresh should be fed by source metadata, not extra gallery-dl calls")
	}
}

func TestRefreshInstagramProfileDefersWhileSourceBacklogExists(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.AddChannel(model.Channel{
		ChannelID:    "instagram_waiting",
		Platform:     "instagram",
		Name:         "waiting",
		URL:          "https://instagram.com/waiting",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_waiting",
		Platform:    "instagram",
		Handle:      "waiting",
		DisplayName: "Waiting",
		AvatarURL:   "https://example.test/avatar.jpg",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)
	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_waiting", avDir, bnDir)

	got, err := d.GetChannelProfile("instagram_waiting")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.FetchedAt != nil {
		t.Fatalf("FetchedAt = %v, want deferred profile to remain unfetched", got.FetchedAt)
	}
	if got.NextRetryAt == nil || !got.NextRetryAt.After(time.Now()) {
		t.Fatalf("NextRetryAt = %v, want future retry", got.NextRetryAt)
	}
	if got.AvatarURL != "https://example.test/avatar.jpg" || got.DisplayName != "Waiting" {
		t.Fatalf("stored profile data was not preserved: %+v", got)
	}
}

func TestRefreshInstagramProfileFetchesMissingAvatarDespiteDownloadBacklog(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	nowMs := time.Now().UnixMilli()
	if err := d.AddChannel(model.Channel{
		ChannelID:    "instagram_waiting",
		Platform:     "instagram",
		Name:         "waiting",
		URL:          "https://instagram.com/waiting",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := d.ExecRaw("UPDATE channels SET last_checked = ? WHERE channel_id = ?", nowMs, "instagram_waiting"); err != nil {
		t.Fatalf("mark checked: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_waiting",
		Platform:    "instagram",
		Handle:      "waiting",
		DisplayName: "Waiting",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := d.AddToDownloadQueue("instagram_reel_WAIT", "instagram_waiting", "Waiting Reel"); err != nil {
		t.Fatalf("seed download queue: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)
	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_waiting", avDir, bnDir)

	got, err := d.GetChannelProfile("instagram_waiting")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.FetchedAt == nil {
		t.Fatalf("FetchedAt = nil, want missing-avatar profile to refresh despite media backlog")
	}
	if got.NextRetryAt != nil {
		t.Fatalf("NextRetryAt = %v, want no deferral for missing-avatar profile", got.NextRetryAt)
	}
}

func TestRefreshProfilePromoteTikTokName(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	// Subscribe a tiktok channel whose name is still the raw handle.
	if err := d.AddChannel(model.Channel{
		ChannelID: "tiktok_bob",
		Platform:  "tiktok",
		Name:      "bob",
		URL:       "https://tiktok/@bob",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f := newFakeFetcher()
	f.results["tiktok_bob"] = &fetchprofile.Profile{
		ChannelID:   "tiktok_bob",
		Platform:    "tiktok",
		Handle:      "bob",
		DisplayName: "Bob the Builder",
	}
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "tiktok_bob", Platform: "tiktok"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "tiktok_bob", avDir, bnDir)
	ch, err := d.GetChannelByID("tiktok_bob")
	if err != nil {
		t.Fatalf("GetChannelByID: %v", err)
	}
	if ch.Name != "Bob the Builder" {
		t.Fatalf("channel name not promoted, got %q", ch.Name)
	}
}

func TestRefreshProfileSynthesizesTikTokBanner(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()

	// Seed a TikTok channel + a downloaded video whose cover sits next to the
	// .mp4 file, mirroring the gallery-dl output shape.
	if err := d.AddChannel(model.Channel{
		ChannelID: "tiktok_carol",
		Platform:  "tiktok",
		Name:      "Carol",
		URL:       "https://tiktok/@carol",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(dir, "media", "tiktok", "carol")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	videoPath := filepath.Join(mediaDir, "video1.mp4")
	if err := os.WriteFile(videoPath, []byte("mp4-placeholder"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	// Sibling thumbnail — gallery-dl stores TikTok video covers as ".image"
	// files, so the synthesizer needs to detect that suffix too.
	thumbPath := filepath.Join(mediaDir, "video1.image")
	jpeg := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
	}
	if err := os.WriteFile(thumbPath, jpeg, 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}
	if err := d.InsertVideo(
		"video1", "tiktok_carol", "hi", "",
		0, "", "media/tiktok/carol/video1.mp4", 0,
		1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	f := newFakeFetcher()
	f.results["tiktok_carol"] = &fetchprofile.Profile{
		ChannelID:   "tiktok_carol",
		Platform:    "tiktok",
		Handle:      "carol",
		DisplayName: "Carol",
		// BannerURL empty — the TikTok fetcher never returns one.
	}
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "tiktok_carol", Platform: "tiktok"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "tiktok_carol", avDir, bnDir)

	got, err := d.GetChannelProfile("tiktok_carol")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.BannerURL != "synth:latest-video:video1" {
		t.Fatalf("BannerURL = %q, want synth:latest-video:video1", got.BannerURL)
	}
	bannerFile := filepath.Join(bnDir, "tiktok_carol.jpg")
	if _, err := os.Stat(bannerFile); err != nil {
		t.Fatalf("expected banner file at %s: %v", bannerFile, err)
	}
}

func TestRefreshProfileSynthesizesInstagramBanner(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.AddChannel(model.Channel{
		ChannelID: "instagram_cinema",
		Platform:  "instagram",
		Name:      "Cinema",
		URL:       "https://instagram.com/cinema",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(dir, "media", "instagram", "cinema")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	imagePath := filepath.Join(mediaDir, "post1.jpg")
	if err := os.WriteFile(imagePath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_post_POST123", "instagram_cinema", "hi", "",
		0, "", "media/instagram/cinema/post1.jpg", 0,
		1_700_000_000_000, "", "image", 1, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	f := newFakeFetcher()
	f.results["instagram_cinema"] = &fetchprofile.Profile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
	}
	_ = d.UpsertChannelProfile(model.ChannelProfile{ChannelID: "instagram_cinema", Platform: "instagram"})

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), f.Fetch, "instagram_cinema", avDir, bnDir)
	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.BannerURL != "synth:latest-video:instagram_post_POST123" {
		t.Fatalf("BannerURL = %q", got.BannerURL)
	}
	if !hasConventionalMediaFile(bnDir, "instagram_cinema") {
		t.Fatal("expected instagram banner file on disk")
	}
}

func TestRefreshInstagramProfileSynthesizesBannerWhileBacklogDefersNetwork(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.AddChannel(model.Channel{
		ChannelID:    "instagram_cinema",
		Platform:     "instagram",
		Name:         "Cinema",
		URL:          "https://instagram.com/cinema",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(dir, "media", "instagram", "cinema")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	imagePath := filepath.Join(mediaDir, "post1.jpg")
	if err := os.WriteFile(imagePath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_post_POST123", "instagram_cinema", "hi", "",
		0, "", "media/instagram/cinema/post1.jpg", 0,
		1_700_000_000_000, "", "image", 1, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
		AvatarURL:   "https://example.test/avatar.jpg",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_cinema", avDir, bnDir)

	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.FetchedAt != nil {
		t.Fatalf("FetchedAt = %v, want deferred profile to remain unfetched", got.FetchedAt)
	}
	if got.NextRetryAt == nil || !got.NextRetryAt.After(time.Now()) {
		t.Fatalf("NextRetryAt = %v, want future retry", got.NextRetryAt)
	}
	if got.BannerURL != "synth:latest-video:instagram_post_POST123" {
		t.Fatalf("BannerURL = %q", got.BannerURL)
	}
	if !hasConventionalMediaFile(bnDir, "instagram_cinema") {
		t.Fatal("expected instagram banner file on disk")
	}
}

func TestRefreshInstagramProfilePreservesSynthesizedBannerWhenProfileFetchFails(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.AddChannel(model.Channel{
		ChannelID: "instagram_cinema",
		Platform:  "instagram",
		Name:      "Cinema",
		URL:       "https://instagram.com/cinema",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(dir, "media", "instagram", "cinema")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	imagePath := filepath.Join(mediaDir, "post1.jpg")
	if err := os.WriteFile(imagePath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_post_POST123", "instagram_cinema", "hi", "",
		0, "", "media/instagram/cinema/post1.jpg", 0,
		1_700_000_000_000, "", "image", 1, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{
		db:  d,
		cfg: testCfg(dir),
		instagramProfileFetch: func(context.Context, string, string) (*model.ChannelProfile, error) {
			return nil, errTransient
		},
	}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_cinema", avDir, bnDir)

	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.FetchedAt != nil {
		t.Fatalf("FetchedAt = %v, want failed profile fetch to remain unfetched", got.FetchedAt)
	}
	if got.NextRetryAt == nil || !got.NextRetryAt.After(time.Now()) {
		t.Fatalf("NextRetryAt = %v, want transient retry", got.NextRetryAt)
	}
	if got.BannerURL != "synth:latest-video:instagram_post_POST123" {
		t.Fatalf("BannerURL = %q", got.BannerURL)
	}
	if !hasConventionalMediaFile(bnDir, "instagram_cinema") {
		t.Fatal("expected instagram banner file on disk")
	}
}

func TestRefreshInstagramProfileBackoffCreatesBannerWithoutNetworkFetch(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.AddChannel(model.Channel{
		ChannelID: "instagram_cinema",
		Platform:  "instagram",
		Name:      "Cinema",
		URL:       "https://instagram.com/cinema",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(dir, "media", "instagram", "cinema")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	imagePath := filepath.Join(mediaDir, "post1.jpg")
	if err := os.WriteFile(imagePath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_post_POST123", "instagram_cinema", "hi", "",
		0, "", "media/instagram/cinema/post1.jpg", 0,
		1_700_000_000_000, "", "image", 1, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	nextRetry := now.Add(time.Hour)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
		FetchedAt:   &now,
		NextRetryAt: &nextRetry,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	fetchCalls := 0
	m := &Manager{
		db:  d,
		cfg: testCfg(dir),
		instagramProfileFetch: func(context.Context, string, string) (*model.ChannelProfile, error) {
			fetchCalls++
			return nil, errTransient
		},
	}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_cinema", avDir, bnDir)

	if fetchCalls != 0 {
		t.Fatalf("instagram profile fetch calls = %d, want 0", fetchCalls)
	}
	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.BannerURL != "synth:latest-video:instagram_post_POST123" {
		t.Fatalf("BannerURL = %q", got.BannerURL)
	}
	if got.NextRetryAt == nil || !got.NextRetryAt.Equal(nextRetry) {
		t.Fatalf("NextRetryAt = %v, want preserved %v", got.NextRetryAt, nextRetry)
	}
}

func TestRefreshInstagramProfileWithAvatarCreatesBannerBeforeStaleNetworkFetch(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.AddChannel(model.Channel{
		ChannelID: "instagram_cinema",
		Platform:  "instagram",
		Name:      "Cinema",
		URL:       "https://instagram.com/cinema",
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mediaDir := filepath.Join(dir, "media", "instagram", "cinema")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	imagePath := filepath.Join(mediaDir, "post1.jpg")
	if err := os.WriteFile(imagePath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_post_POST123", "instagram_cinema", "hi", "",
		0, "", "media/instagram/cinema/post1.jpg", 0,
		1_700_000_000_000, "", "image", 1, false,
	); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	old := time.Now().Add(-2 * profileTTL).UTC().Truncate(time.Millisecond)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
		AvatarURL:   "https://example.test/avatar.jpg",
		FetchedAt:   &old,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	fetchCalls := 0
	m := &Manager{
		db:  d,
		cfg: testCfg(dir),
		instagramProfileFetch: func(context.Context, string, string) (*model.ChannelProfile, error) {
			fetchCalls++
			return nil, errTransient
		},
	}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_cinema", avDir, bnDir)

	if fetchCalls != 0 {
		t.Fatalf("instagram profile fetch calls = %d, want 0", fetchCalls)
	}
	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.BannerURL != "synth:latest-video:instagram_post_POST123" {
		t.Fatalf("BannerURL = %q", got.BannerURL)
	}
	if got.NextRetryAt == nil || !got.NextRetryAt.After(time.Now()) {
		t.Fatalf("NextRetryAt = %v, want deferred profile retry", got.NextRetryAt)
	}
}

func TestRefreshInstagramProfileClearsCaptionDerivedBio(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
		Bio:         "This is a post caption, not a profile bio.",
		AvatarURL:   "https://example.test/old-avatar.jpg",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{
		db:  d,
		cfg: testCfg(dir),
		instagramProfileFetch: func(context.Context, string, string) (*model.ChannelProfile, error) {
			return &model.ChannelProfile{
				ChannelID:   "instagram_cinema",
				Platform:    "instagram",
				Handle:      "cinema",
				DisplayName: "Cinema",
				AvatarURL:   "https://example.test/new-avatar.jpg",
			}, nil
		},
	}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_cinema", avDir, bnDir)

	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.Bio != "" {
		t.Fatalf("Bio = %q, want cleared when safe profile has no bio", got.Bio)
	}
	if got.AvatarURL != "https://example.test/new-avatar.jpg" {
		t.Fatalf("AvatarURL = %q", got.AvatarURL)
	}
}

func TestRefreshInstagramProfileClearsUntrustedMediaAvatar(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
		Bio:         "This is a post caption, not a profile bio.",
		AvatarURL:   "https://example.test/media-avatar.jpg",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{
		db:  d,
		cfg: testCfg(dir),
		instagramProfileFetch: func(context.Context, string, string) (*model.ChannelProfile, error) {
			return &model.ChannelProfile{
				ChannelID:   "instagram_cinema",
				Platform:    "instagram",
				Handle:      "cinema",
				DisplayName: "Cinema",
			}, nil
		},
	}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)
	if err := os.WriteFile(filepath.Join(avDir, "instagram_cinema.jpg"), []byte("old avatar"), 0o644); err != nil {
		t.Fatalf("write cached avatar: %v", err)
	}

	m.refreshProfile(context.Background(), newFakeFetcher().Fetch, "instagram_cinema", avDir, bnDir)

	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.Bio != "" || got.AvatarURL != "" {
		t.Fatalf("profile fields survived trusted empty refresh: %+v", got)
	}
	if hasConventionalMediaFile(avDir, "instagram_cinema") {
		t.Fatal("stale cached avatar survived trusted empty refresh")
	}
}

func TestRememberInstagramProfileFromRefsPreservesRichProfile(t *testing.T) {
	d := newTestWorkerDB(t)
	fullFetchedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_cinema",
		Platform:    "instagram",
		Handle:      "cinema",
		DisplayName: "Cinema",
		Bio:         "full profile bio",
		Website:     "https://example.test",
		Followers:   123,
		AvatarURL:   "https://cdn.example/old-avatar.jpg",
		BannerURL:   "synth:latest-video:instagram_post_OLD",
		FetchedAt:   &fullFetchedAt,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(t.TempDir())}
	m.rememberInstagramProfileFromRefs(model.Channel{
		ChannelID: "instagram_cinema",
		Platform:  "instagram",
		Name:      "Cinema",
		URL:       "https://instagram.com/cinema",
	}, []download.VideoRef{{
		ChannelID:         "instagram_cinema",
		AuthorHandle:      "cinema",
		AuthorDisplayName: "Cinema Clips",
		AuthorAvatarURL:   "https://cdn.example/new-avatar.jpg",
	}})

	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.Bio != "full profile bio" || got.Website != "https://example.test" || got.Followers != 123 {
		t.Fatalf("rich profile fields were clobbered: %+v", got)
	}
	if got.DisplayName != "Cinema Clips" || got.AvatarURL != "https://cdn.example/new-avatar.jpg" {
		t.Fatalf("ref metadata was not applied: %+v", got)
	}
	if got.FetchedAt == nil || !got.FetchedAt.Equal(fullFetchedAt) {
		t.Fatalf("FetchedAt = %v, want preserved %v", got.FetchedAt, fullFetchedAt)
	}
	if got.BannerURL != "synth:latest-video:instagram_post_OLD" {
		t.Fatalf("BannerURL = %q", got.BannerURL)
	}
}

func TestRememberInstagramProfileFromRefsLeavesNewProfileUnfetched(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir())}

	m.rememberInstagramProfileFromRefs(model.Channel{
		ChannelID: "instagram_cinema",
		Platform:  "instagram",
		Name:      "Cinema",
		URL:       "https://instagram.com/cinema",
	}, []download.VideoRef{{
		ChannelID:         "instagram_cinema",
		AuthorHandle:      "cinema",
		AuthorDisplayName: "Cinema Clips",
		AuthorAvatarURL:   "https://cdn.example/avatar.jpg",
	}})

	got, err := d.GetChannelProfile("instagram_cinema")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.FetchedAt != nil {
		t.Fatalf("FetchedAt = %v, want partial ref metadata to stay unfetched", got.FetchedAt)
	}
	if got.DisplayName != "Cinema Clips" || got.AvatarURL != "https://cdn.example/avatar.jpg" {
		t.Fatalf("ref metadata was not applied: %+v", got)
	}
}

func TestRefreshStaleProfilesBatchHonorsLimit(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	f := newFakeFetcher()
	for _, channelID := range []string{"twitter_alpha", "twitter_beta", "twitter_gamma"} {
		f.results[channelID] = &fetchprofile.Profile{
			ChannelID:   channelID,
			Platform:    "twitter",
			Handle:      channelID[len("twitter_"):],
			DisplayName: channelID,
		}
		if err := d.UpsertChannelProfile(model.ChannelProfile{
			ChannelID: channelID,
			Platform:  "twitter",
		}); err != nil {
			t.Fatalf("seed %s: %v", channelID, err)
		}
	}

	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshStaleProfilesBatch(context.Background(), f.Fetch, avDir, bnDir, 2); !worked {
		t.Fatal("expected batch to do work")
	}
	if got := f.calls["twitter_alpha"]; got != 1 {
		t.Fatalf("alpha fetch calls = %d, want 1", got)
	}
	if got := f.calls["twitter_beta"]; got != 1 {
		t.Fatalf("beta fetch calls = %d, want 1", got)
	}
	if got := f.calls["twitter_gamma"]; got != 0 {
		t.Fatalf("gamma fetch calls = %d, want 0", got)
	}

	next, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("NextChannelProfileRefreshCandidate: %v", err)
	}
	if next != "twitter_gamma" {
		t.Fatalf("next candidate = %q, want twitter_gamma", next)
	}
}

type gatedProfileFetcher struct {
	started chan string
	release chan struct{}
	results map[string]*fetchprofile.Profile
}

func newGatedProfileFetcher(results map[string]*fetchprofile.Profile) *gatedProfileFetcher {
	return &gatedProfileFetcher{
		started: make(chan string, len(results)),
		release: make(chan struct{}),
		results: results,
	}
}

func (f *gatedProfileFetcher) Fetch(ctx context.Context, channelID string) (*fetchprofile.Profile, error) {
	select {
	case f.started <- channelID:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-f.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p, ok := f.results[channelID]; ok {
		return p, nil
	}
	return &fetchprofile.Profile{
		ChannelID:   channelID,
		Platform:    platformForChannelID(channelID),
		Handle:      strings.TrimPrefix(channelID, platformForChannelID(channelID)+"_"),
		DisplayName: channelID,
	}, nil
}

func waitForProfileStarts(t *testing.T, started <-chan string, want int, release chan struct{}) []string {
	t.Helper()
	ids := make([]string, 0, want)
	for len(ids) < want {
		select {
		case id := <-started:
			ids = append(ids, id)
		case <-time.After(500 * time.Millisecond):
			close(release)
			t.Fatalf("profile fetch starts = %v, want %d concurrent platform starts", ids, want)
		}
	}
	return ids
}

func TestRefreshStaleProfilesBatchRunsPlatformsInParallel(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	for _, channelID := range []string{"twitter_parallel", "tiktok_parallel"} {
		if err := d.UpsertChannelProfile(model.ChannelProfile{
			ChannelID: channelID,
			Platform:  platformForChannelID(channelID),
		}); err != nil {
			t.Fatalf("seed %s: %v", channelID, err)
		}
	}

	f := newGatedProfileFetcher(map[string]*fetchprofile.Profile{
		"twitter_parallel": {ChannelID: "twitter_parallel", Platform: "twitter", Handle: "parallel"},
		"tiktok_parallel":  {ChannelID: "tiktok_parallel", Platform: "tiktok", Handle: "parallel"},
	})
	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	done := make(chan bool, 1)
	go func() {
		done <- m.refreshStaleProfilesBatch(context.Background(), f.Fetch, avDir, bnDir, 1)
	}()
	started := waitForProfileStarts(t, f.started, 2, f.release)
	close(f.release)

	got := map[string]bool{}
	for _, id := range started {
		got[id] = true
	}
	if !got["twitter_parallel"] || !got["tiktok_parallel"] {
		t.Fatalf("started profile fetches = %v, want twitter and tiktok", started)
	}
	select {
	case worked := <-done:
		if !worked {
			t.Fatal("expected stale profile batch to do work")
		}
	case <-time.After(time.Second):
		t.Fatal("refreshStaleProfilesBatch did not finish after release")
	}
}

func TestRefreshFeedProfileCompletenessBatchRunsPlatformsInParallel(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES ('tweet_parallel', 'profile_parallel', 100, 100)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	if err := d.InsertVideo(
		"tiktok_parallel_video", "tiktok_profile_parallel", "clip", "",
		0, "", "media/tiktok/profile/video.mp4", 0,
		200, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert tiktok video: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	f := newGatedProfileFetcher(map[string]*fetchprofile.Profile{
		"twitter_profile_parallel": {ChannelID: "twitter_profile_parallel", Platform: "twitter", Handle: "profile_parallel"},
		"tiktok_profile_parallel":  {ChannelID: "tiktok_profile_parallel", Platform: "tiktok", Handle: "profile_parallel"},
	})
	m := &Manager{db: d, cfg: testCfg(dir)}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	done := make(chan bool, 1)
	go func() {
		done <- m.refreshFeedProfileCompletenessBatch(context.Background(), f.Fetch, avDir, bnDir, 1)
	}()
	started := waitForProfileStarts(t, f.started, 2, f.release)
	close(f.release)

	got := map[string]bool{}
	for _, id := range started {
		got[id] = true
	}
	if !got["twitter_profile_parallel"] || !got["tiktok_profile_parallel"] {
		t.Fatalf("started profile fetches = %v, want twitter and tiktok", started)
	}
	select {
	case worked := <-done:
		if !worked {
			t.Fatal("expected feed profile completeness batch to do work")
		}
	case <-time.After(time.Second):
		t.Fatal("refreshFeedProfileCompletenessBatch did not finish after release")
	}
}

func TestRefreshFeedProfileCompletenessFetchesReplyParent(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()

	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("initial seed: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, reply_to_handle, published_at, fetched_at
		) VALUES ('tweet_reply', 'author_a', 'reply_parent', 1, 1)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	f := newFakeFetcher()
	f.results["twitter_reply_parent"] = &fetchprofile.Profile{
		ChannelID: "twitter_reply_parent",
		Platform:  "twitter",
		Handle:    "reply_parent",
		AvatarURL: avatarServer.URL + "/avatar.png",
		Followers: 1,
		Following: 1,
	}

	m := &Manager{db: d, cfg: testCfg(dir), downloader: testDownloader()}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), f.Fetch, avDir, bnDir, 10); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	if got := f.calls["twitter_reply_parent"]; got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
	if !hasConventionalMediaFile(avDir, "twitter_reply_parent") {
		t.Fatal("expected reply parent avatar file on disk")
	}
}

func TestRefreshFeedProfileCompletenessDownloadsStoredTwitterAvatarForFreshRow(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()

	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES ('tweet_cryptokid', 'cryptokid', 200, 200)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_cryptokid",
		Platform:    "twitter",
		Handle:      "cryptokid",
		DisplayName: "Crypto Kid",
		AvatarURL:   "https://pbs.twimg.com/profile_images/1998822702501838848/OUPVuvCJ_normal.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	f := newFakeFetcher()
	m := &Manager{db: d, cfg: testCfg(dir), downloader: testTwimgAvatarDownloader(avatarServer)}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), f.Fetch, avDir, bnDir, 10); !worked {
		t.Fatal("expected feed profile completeness batch to download stored avatar")
	}
	if got := f.calls["twitter_cryptokid"]; got != 0 {
		t.Fatalf("full profile fetch calls = %d, want 0 for fresh stored-avatar recovery", got)
	}
	if !hasConventionalMediaFile(avDir, "twitter_cryptokid") {
		t.Fatal("expected cryptokid avatar file on disk")
	}
}

func TestRefreshFeedProfileCompletenessScansStoredAvatarsBeyondProfileFetchLimit(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()

	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, published_at, fetched_at
		) VALUES
			('tweet_cryptokid', 'cryptokid', 100, 100),
			('tweet_newer_1', 'newer_1', 300, 300),
			('tweet_newer_2', 'newer_2', 290, 290)
	`); err != nil {
		t.Fatalf("seed feed items: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_cryptokid",
		Platform:    "twitter",
		Handle:      "cryptokid",
		DisplayName: "Crypto Kid",
		AvatarURL:   "https://pbs.twimg.com/profile_images/1998822702501838848/OUPVuvCJ_normal.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	f := newFakeFetcher()
	m := &Manager{db: d, cfg: testCfg(dir), downloader: testTwimgAvatarDownloader(avatarServer)}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), f.Fetch, avDir, bnDir, 1); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	if !hasConventionalMediaFile(avDir, "twitter_cryptokid") {
		t.Fatal("expected fresh stored avatar row to be recovered while profile fetches are rate-limited")
	}
	if got := f.calls["twitter_newer_1"] + f.calls["twitter_newer_2"]; got != 1 {
		t.Fatalf("newer backlog fetch calls = %d, want exactly one rate-limited profile fetch", got)
	}
}

func TestRefreshFeedProfileCompletenessFetchesInstagramSourceWindowProfile(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()
	installGalleryDLAvatarStub(t, dir)

	now := time.Now().UnixMilli()
	if err := d.InsertVideo(
		"instagram_source_video", "instagram_source.owner", "source window", "",
		0, "", "media/instagram/source/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:           "instagram_source_video",
		ReposterChannelID: "instagram_followed",
		ReposterHandle:    "followed",
		FirstSeenAtMs:     now,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	calls := make(chan string, 1)
	m := &Manager{
		db:         d,
		cfg:        testCfg(dir),
		downloader: testDownloader(),
		instagramProfileFetch: func(ctx context.Context, channelID, handle string) (*model.ChannelProfile, error) {
			calls <- channelID
			if channelID != "instagram_source.owner" || handle != "source.owner" {
				t.Fatalf("unexpected instagram profile request: %s/%s", channelID, handle)
			}
			return &model.ChannelProfile{
				ChannelID:   channelID,
				Platform:    "instagram",
				Handle:      handle,
				DisplayName: "Source Owner",
				Bio:         "real bio",
				AvatarURL:   avatarServer.URL + "/avatar.png",
			}, nil
		},
	}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), newFakeFetcher().Fetch, avDir, bnDir, 1); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	select {
	case got := <-calls:
		if got != "instagram_source.owner" {
			t.Fatalf("profile fetch = %q, want instagram_source.owner", got)
		}
	default:
		t.Fatal("expected instagram source-window profile fetch")
	}
	profile, err := d.GetChannelProfile("instagram_source.owner")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if profile == nil || profile.FetchedAt == nil || profile.AvatarURL != avatarServer.URL+"/avatar.png" || profile.Bio != "real bio" {
		t.Fatalf("source-window profile was not fetched: %+v", profile)
	}
	if !hasConventionalMediaFile(avDir, "instagram_source.owner") {
		t.Fatal("expected instagram source-window avatar file on disk")
	}
}

func TestRefreshFeedProfileCompletenessFetchesCaptionMentionProfile(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	installGalleryDLAvatarStub(t, dir)

	now := time.Now().UnixMilli()
	if err := d.InsertVideo(
		"instagram_caption_mentions", "instagram_owner", "caption mentions", "with @rinn_xc and @dear.chuu",
		0, "", "media/instagram/owner/post.mp4", 0,
		now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	m := &Manager{
		db:         d,
		cfg:        testCfg(dir),
		downloader: testDownloader(),
		instagramProfileFetch: func(ctx context.Context, channelID, handle string) (*model.ChannelProfile, error) {
			if channelID == "instagram_rinn_xc" {
				return &model.ChannelProfile{
					ChannelID:   channelID,
					Platform:    "instagram",
					Handle:      handle,
					DisplayName: "rinn_xc",
					Bio:         "real mention profile",
					AvatarURL:   "https://cdn.example/rinn.jpg",
				}, nil
			}
			return &model.ChannelProfile{
				ChannelID:   channelID,
				Platform:    "instagram",
				Handle:      handle,
				DisplayName: handle,
			}, nil
		},
	}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), newFakeFetcher().Fetch, avDir, bnDir, 10); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	profile, err := d.GetChannelProfile("instagram_rinn_xc")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if profile == nil || profile.FetchedAt == nil || profile.Bio != "real mention profile" {
		t.Fatalf("caption mention profile was not fetched: %+v", profile)
	}
	if !hasConventionalMediaFile(avDir, "instagram_rinn_xc") {
		t.Fatal("expected caption mention avatar file on disk")
	}
}

func TestRefreshFeedProfileCompletenessFetchesRichProfileEvenWhenAvatarCached(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	bannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer bannerServer.Close()

	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, author_avatar_url, published_at, fetched_at
		) VALUES ('tweet_profile_card', 'profile_card', 'https://pbs.twimg.com/profile_images/111/avatar_normal.jpg', 100, 100)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	f := newFakeFetcher()
	f.results["twitter_profile_card"] = &fetchprofile.Profile{
		ChannelID:   "twitter_profile_card",
		Platform:    "twitter",
		Handle:      "profile_card",
		DisplayName: "Profile Card",
		Bio:         "full card bio",
		BannerURL:   bannerServer.URL + "/banner.png",
	}

	m := &Manager{db: d, cfg: testCfg(dir), downloader: testDownloader()}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)
	if err := os.WriteFile(filepath.Join(avDir, "twitter_profile_card.png"), testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write cached avatar: %v", err)
	}

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), f.Fetch, avDir, bnDir, 10); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	if got := f.calls["twitter_profile_card"]; got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
	profile, err := d.GetChannelProfile("twitter_profile_card")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if profile == nil || profile.Bio != "full card bio" || profile.BannerURL == "" || profile.FetchedAt == nil {
		t.Fatalf("rich profile was not fetched: %+v", profile)
	}
	if !hasConventionalMediaFile(bnDir, "twitter_profile_card") {
		t.Fatal("expected banner file on disk")
	}
}

func TestRefreshFeedProfileCompletenessDownloadsStoredBanner(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	bannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer bannerServer.Close()

	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, author_avatar_url, published_at, fetched_at
		) VALUES ('tweet_banner_cached', 'banner_cached', 'https://pbs.twimg.com/profile_images/111/avatar_normal.jpg', 100, 100)
	`); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_banner_cached",
		Platform:    "twitter",
		Handle:      "banner_cached",
		DisplayName: "Banner Cached",
		Bio:         "already fetched",
		AvatarURL:   "https://pbs.twimg.com/profile_images/111/avatar_normal.jpg",
		BannerURL:   bannerServer.URL + "/banner.png",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	f := newFakeFetcher()
	m := &Manager{db: d, cfg: testCfg(dir), downloader: testDownloader()}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)
	if err := os.WriteFile(filepath.Join(avDir, "twitter_banner_cached.png"), testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write cached avatar: %v", err)
	}

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), f.Fetch, avDir, bnDir, 10); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	if got := f.calls["twitter_banner_cached"]; got != 0 {
		t.Fatalf("fetch calls = %d, want 0", got)
	}
	if !hasConventionalMediaFile(bnDir, "twitter_banner_cached") {
		t.Fatal("expected stored banner file on disk")
	}
}

func TestRefreshFeedProfileCompletenessHonorsLimit(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	for _, handle := range []string{"author_a", "author_b"} {
		if err := d.ExecRaw(`
			INSERT INTO feed_items (
				tweet_id, author_handle, published_at, fetched_at
			) VALUES (?, ?, 1, 1)
		`, "tweet_"+handle, handle); err != nil {
			t.Fatalf("seed feed item %s: %v", handle, err)
		}
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	f := newFakeFetcher()
	for _, channelID := range []string{"twitter_author_a", "twitter_author_b"} {
		f.results[channelID] = &fetchprofile.Profile{
			ChannelID: channelID,
			Platform:  "twitter",
			Handle:    strings.TrimPrefix(channelID, "twitter_"),
		}
	}

	m := &Manager{db: d, cfg: testCfg(dir), downloader: testDownloader()}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), f.Fetch, avDir, bnDir, 1); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	if got := f.calls["twitter_author_a"] + f.calls["twitter_author_b"]; got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

func TestDownloadProfileMediaFallsBackWhenUpgradedTwitterAvatar404s(t *testing.T) {
	dir := t.TempDir()
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_400x400.") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()

	m := &Manager{cfg: testCfg(dir), downloader: testDownloader()}
	avDir := filepath.Join(dir, "avatars")
	_ = os.MkdirAll(avDir, 0o755)

	m.downloadProfileMedia(
		context.Background(),
		"twitter_quote_a",
		"avatar",
		avatarServer.URL+"/profile_images/1/avatar_normal.jpg",
		avDir,
	)

	if !hasConventionalMediaFile(avDir, "twitter_quote_a") {
		t.Fatal("expected avatar file downloaded from original URL fallback")
	}
}

func TestDownloadProfileMediaUsesGalleryDLForInstagramAvatarFirst(t *testing.T) {
	dir := t.TempDir()
	var (
		mu   sync.Mutex
		hits int
	)
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()
	installGalleryDLAvatarStub(t, dir)

	m := &Manager{cfg: testCfg(dir), downloader: testDownloader()}
	avDir := filepath.Join(dir, "avatars")
	if !m.downloadProfileMedia(
		context.Background(),
		"instagram_source.owner",
		"avatar",
		avatarServer.URL+"/avatar.jpg",
		avDir,
	) {
		t.Fatal("expected instagram avatar download to work")
	}
	mu.Lock()
	gotHits := hits
	mu.Unlock()
	if gotHits != 0 {
		t.Fatalf("direct avatar HTTP hits = %d, want gallery-dl first", gotHits)
	}
	if !hasConventionalMediaFile(avDir, "instagram_source.owner") {
		t.Fatal("expected instagram avatar file from gallery-dl")
	}
}

func TestRefreshFeedProfileCompletenessDownloadsInstagramAvatarWithoutStoredURL(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	installGalleryDLAvatarStub(t, dir)

	nowMs := time.Now().UnixMilli()
	if err := d.InsertVideo(
		"instagram_empty_avatar_video", "instagram_empty.avatar", "source window", "",
		0, "", "media/instagram/source/post.mp4", 0,
		nowMs, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:           "instagram_empty_avatar_video",
		ReposterChannelID: "instagram_followed",
		ReposterHandle:    "followed",
		FirstSeenAtMs:     nowMs,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_empty.avatar",
		Platform:    "instagram",
		Handle:      "empty.avatar",
		DisplayName: "Empty Avatar",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(dir), downloader: testDownloader()}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	if worked := m.refreshFeedProfileCompletenessBatch(context.Background(), newFakeFetcher().Fetch, avDir, bnDir, 1); !worked {
		t.Fatal("expected feed profile completeness batch to work")
	}
	if !hasConventionalMediaFile(avDir, "instagram_empty.avatar") {
		t.Fatal("expected instagram avatar file from gallery-dl fallback")
	}
}

func TestInstagramAvatarDownloadRetriesBrowserCookiesAfterCookieFileAuthFailure(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "instagram_cookies.txt"), []byte("# stale\n"), 0o600); err != nil {
		t.Fatalf("write stale cookies: %v", err)
	}
	if err := d.SetSetting("", "cookies_instagram_browser", "firefox"); err != nil {
		t.Fatalf("SetSetting browser: %v", err)
	}
	installGalleryDLAvatarBrowserFallbackStub(t, dir)

	cfg := testCfg(dir)
	cfg.CookiesDir = dir
	m := &Manager{db: d, cfg: cfg, downloader: testDownloader()}
	avDir := filepath.Join(dir, "avatars")

	if downloaded, err := m.downloadInstagramProfileAvatar(context.Background(), "instagram_source.owner", avDir); !downloaded || err != nil {
		t.Fatal("expected instagram avatar download to retry browser cookies")
	}
	if !hasConventionalMediaFile(avDir, "instagram_source.owner") {
		t.Fatal("expected instagram avatar file from browser-cookie retry")
	}
}

func TestRefreshFeedProfileCompletenessBacksOffFailedInstagramAvatarFallback(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	installGalleryDLFailureStub(t, dir)

	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_sample_avatar",
		Platform:    "instagram",
		Handle:      "sample_avatar",
		DisplayName: "Missing Avatar",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := d.InsertVideo(
		"instagram_sample_avatar_video", "instagram_sample_avatar", "source window", "",
		0, "", "media/instagram/source/post.mp4", 0,
		now.UnixMilli(), "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert instagram video: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(dir), downloader: testDownloader()}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")

	m.refreshFeedProfileCompletenessBatch(context.Background(), newFakeFetcher().Fetch, avDir, bnDir, 1)

	got, err := d.GetChannelProfile("instagram_sample_avatar")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if got == nil || got.NextRetryAt == nil || !got.NextRetryAt.After(time.Now()) {
		t.Fatalf("NextRetryAt = %v, want failed avatar fallback to back off", got)
	}
	if got.NextRetryAt.Before(time.Now().Add(10 * time.Minute)) {
		t.Fatalf("NextRetryAt = %v, want instagram avatar fallback backoff", got.NextRetryAt)
	}
	if got.FailCount == 0 {
		t.Fatalf("FailCount = %d, want failed avatar fallback recorded", got.FailCount)
	}
}

func installGalleryDLAvatarStub(t *testing.T, dir string) {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	sourceAvatar := filepath.Join(dir, "source.png")
	if err := os.WriteFile(sourceAvatar, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write source avatar: %v", err)
	}
	script := `#!/bin/sh
set -eu
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D)
      shift
      out="$1"
      ;;
  esac
  shift || true
done
mkdir -p "$out"
cp "$IGLOO_TEST_AVATAR" "$out/avatar.png"
`
	if err := os.WriteFile(filepath.Join(binDir, "gallery-dl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write gallery-dl stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("IGLOO_TEST_AVATAR", sourceAvatar)
}

func installGalleryDLFailureStub(t *testing.T, dir string) {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := `#!/bin/sh
echo '[instagram][error] An unexpected error occurred: KeyError - profile_pic_url' >&2
exit 5
`
	if err := os.WriteFile(filepath.Join(binDir, "gallery-dl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write gallery-dl stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installGalleryDLAvatarBrowserFallbackStub(t *testing.T, dir string) {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	sourceAvatar := filepath.Join(dir, "source.png")
	if err := os.WriteFile(sourceAvatar, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write source avatar: %v", err)
	}
	script := `#!/bin/sh
set -eu
out=""
browser=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D)
      shift
      out="$1"
      ;;
    --cookies)
      shift
      echo '[instagram][error] HTTP redirect to login page (https://www.instagram.com/accounts/login/)' >&2
      exit 4
      ;;
    --cookies-from-browser)
      shift
      browser="$1"
      ;;
  esac
  shift || true
done
if [ "$browser" != "firefox" ]; then
  echo 'missing browser cookies' >&2
  exit 4
fi
mkdir -p "$out"
cp "$IGLOO_TEST_AVATAR" "$out/avatar.png"
`
	if err := os.WriteFile(filepath.Join(binDir, "gallery-dl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write gallery-dl stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("IGLOO_TEST_AVATAR", sourceAvatar)
}

func TestRequestAvatarSeedsProfileWhenQueueFull(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}
	m.avatarRequest <- "twitter_busy"

	m.RequestAvatar("twitter_Mono_mon__")

	got, err := d.GetChannelProfile("twitter_mono_mon__")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if got == nil {
		t.Fatal("expected seeded profile row")
	}
	if got.Platform != "twitter" {
		t.Fatalf("platform = %q, want twitter", got.Platform)
	}
	if got.Handle != "mono_mon__" {
		t.Fatalf("handle = %q, want mono_mon__", got.Handle)
	}

	candidate, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("NextChannelProfileRefreshCandidate: %v", err)
	}
	if candidate != "twitter_mono_mon__" {
		t.Fatalf("candidate = %q, want twitter_mono_mon__", candidate)
	}
}

func TestRequestAvatarMarksExistingAvatarDueWhenQueueFull(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_full",
		Platform:    "twitter",
		Handle:      "full",
		DisplayName: "Full",
		AvatarURL:   "https://pbs.twimg.com/profile_images/777/photo_normal.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}
	m.avatarRequest <- "twitter_busy"

	m.RequestAvatar("twitter_full")

	got, err := d.GetChannelProfile("twitter_full")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.AvatarURL != "https://pbs.twimg.com/profile_images/777/photo_normal.jpg" {
		t.Fatalf("avatar URL was not preserved: %+v", got)
	}
	if got.FetchedAt != nil || got.NextRetryAt != nil || got.FailCount != 0 {
		t.Fatalf("profile was not marked due for background recovery: %+v", got)
	}
	candidate, err := d.NextChannelProfileRefreshCandidate(24 * time.Hour)
	if err != nil {
		t.Fatalf("NextChannelProfileRefreshCandidate: %v", err)
	}
	if candidate != "twitter_full" {
		t.Fatalf("candidate = %q, want twitter_full", candidate)
	}
}

func TestRequestAvatarPreservesExistingRichProfile(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_rich",
		Platform:    "twitter",
		Handle:      "rich",
		DisplayName: "Rich Profile",
		Bio:         "keep me",
		BannerURL:   "https://cdn.example/banner.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}

	m.RequestAvatar("twitter_rich")

	got, err := d.GetChannelProfile("twitter_rich")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if got == nil || got.DisplayName != "Rich Profile" || got.Bio != "keep me" || got.BannerURL == "" || got.FetchedAt == nil {
		t.Fatalf("profile was clobbered: %+v", got)
	}
}

func TestRequestAvatarPreservesYouTubeChannelIDCase(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}

	const channelID = "youtube_UCAbCdEfGhIjKlMnOpQrStUv"
	m.RequestAvatar(channelID)

	got, err := d.GetChannelProfile(channelID)
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if got == nil {
		t.Fatal("expected seeded profile row")
	}
	if got.Platform != "youtube" {
		t.Fatalf("platform = %q, want youtube", got.Platform)
	}
	if got.Handle != "UCAbCdEfGhIjKlMnOpQrStUv" {
		t.Fatalf("handle = %q, want mixed-case channel suffix", got.Handle)
	}

	select {
	case queued := <-m.avatarRequest:
		if queued != channelID {
			t.Fatalf("queued channelID = %q, want %q", queued, channelID)
		}
	default:
		t.Fatal("expected YouTube avatar request to be queued")
	}
}

func TestRequestAvatarRejectsInvalidInstagramHandle(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}

	m.RequestAvatar("instagram_not!valid")

	if got, err := d.GetChannelProfile("instagram_not!valid"); err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	} else if got != nil {
		t.Fatalf("expected no profile row for invalid handle, got %+v", got)
	}
	select {
	case queued := <-m.avatarRequest:
		t.Fatalf("unexpected queued channelID %q", queued)
	default:
	}
}

func TestRequestAvatarSkipsUnknownInstagramProfile(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}

	m.RequestAvatar("instagram_unknownhandle")

	if got, err := d.GetChannelProfile("instagram_unknownhandle"); err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	} else if got != nil {
		t.Fatalf("expected no seeded profile row, got %+v", got)
	}
	select {
	case queued := <-m.avatarRequest:
		t.Fatalf("unexpected queued channelID %q", queued)
	default:
	}
}

func TestRefreshRequestedAvatarFetchesKnownInstagramProfile(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_by.bansoi",
		Platform:    "instagram",
		Handle:      "by.bansoi",
		DisplayName: "soi",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	fetchCalls := 0
	m := &Manager{
		db:  d,
		cfg: testCfg(dir),
		instagramProfileFetch: func(context.Context, string, string) (*model.ChannelProfile, error) {
			fetchCalls++
			return &model.ChannelProfile{
				ChannelID:   "instagram_by.bansoi",
				Platform:    "instagram",
				Handle:      "by.bansoi",
				DisplayName: "soi",
				Bio:         "real profile bio",
				AvatarURL:   "https://cdn.example/profile-avatar.jpg",
			}, nil
		},
	}
	avDir, bnDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)

	m.refreshRequestedAvatar(context.Background(), newFakeFetcher().Fetch, "instagram_by.bansoi", avDir, bnDir)

	if fetchCalls != 1 {
		t.Fatalf("instagram profile fetch calls = %d, want 1", fetchCalls)
	}
	got, err := d.GetChannelProfile("instagram_by.bansoi")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.Bio != "real profile bio" || got.AvatarURL != "https://cdn.example/profile-avatar.jpg" {
		t.Fatalf("profile was not refreshed from queued background request: %+v", got)
	}
}

func TestOnDemandProfileRequestLoopDownloadsStoredTwitterAvatar(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()

	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_cryptokid",
		Platform:    "twitter",
		Handle:      "cryptokid",
		DisplayName: "Crypto Kid",
		AvatarURL:   "https://pbs.twimg.com/profile_images/1998822702501838848/OUPVuvCJ_normal.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	m := &Manager{
		db:            d,
		cfg:           testCfg(dir),
		downloader:    testTwimgAvatarDownloader(avatarServer),
		avatarRequest: make(chan string, 1),
	}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.runOnDemandProfileRequestLoop(ctx, avDir, bnDir)
	}()

	m.avatarRequest <- "twitter_cryptokid"
	deadline := time.After(time.Second)
	for !hasConventionalMediaFile(avDir, "twitter_cryptokid") {
		select {
		case <-deadline:
			t.Fatal("expected on-demand loop to write cryptokid avatar")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("on-demand profile request loop did not stop")
	}
}

func TestOnDemandProfileRequestLoopDoesNotStarveStoredAvatarBehindSlowFetch(t *testing.T) {
	d := newTestWorkerDB(t)
	dir := t.TempDir()
	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	defer avatarServer.Close()

	now := time.Now().UTC()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "instagram_slow.profile",
		Platform:    "instagram",
		Handle:      "slow.profile",
		DisplayName: "Slow",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed slow profile: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_hokutonokeninfo",
		Platform:    "twitter",
		Handle:      "hokutonokeninfo",
		DisplayName: "Quote Author",
		AvatarURL:   "https://pbs.twimg.com/profile_images/1465265701716111360/PeQwzKZv_normal.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatalf("seed twitter profile: %v", err)
	}

	m := &Manager{
		db:         d,
		cfg:        testCfg(dir),
		downloader: testTwimgAvatarDownloader(avatarServer),
		instagramProfileFetch: func(ctx context.Context, channelID, handle string) (*model.ChannelProfile, error) {
			if channelID == "instagram_slow.profile" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return nil, nil
		},
		avatarRequest: make(chan string, 2),
	}
	avDir, bnDir := filepath.Join(dir, "avatars"), filepath.Join(dir, "banners")
	_ = os.MkdirAll(avDir, 0o755)
	_ = os.MkdirAll(bnDir, 0o755)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.runOnDemandProfileRequestLoop(ctx, avDir, bnDir)
	}()

	m.avatarRequest <- "instagram_slow.profile"
	m.avatarRequest <- "twitter_hokutonokeninfo"

	deadline := time.After(time.Second)
	for !hasConventionalMediaFile(avDir, "twitter_hokutonokeninfo") {
		select {
		case <-deadline:
			t.Fatal("stored quote avatar was starved behind slow profile fetch")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("on-demand profile request loop did not stop")
	}
}

func TestRequestAvatarQueuesNonInstagramWhenProfileLookupFails(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}

	m.RequestAvatar("twitter_lookup_error")

	select {
	case queued := <-m.avatarRequest:
		if queued != "twitter_lookup_error" {
			t.Fatalf("queued channelID = %q, want twitter_lookup_error", queued)
		}
	default:
		t.Fatal("expected non-instagram avatar request to be queued despite profile lookup failure")
	}
}

func TestRequestAvatarSyntheticQueuesExistingAvatarRow(t *testing.T) {
	d := newTestWorkerDB(t)
	channelID := model.SyntheticTwitterAvatarChannelID("https://pbs.twimg.com/profile_images/777/photo.jpg")
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: channelID,
		Platform:  "twitter",
		AvatarURL: "https://pbs.twimg.com/profile_images/777/photo.jpg",
	}); err != nil {
		t.Fatalf("upsert synthetic profile: %v", err)
	}

	m := &Manager{
		db:            d,
		cfg:           testCfg(t.TempDir()),
		avatarRequest: make(chan string, 1),
	}

	m.RequestAvatar(channelID)

	select {
	case got := <-m.avatarRequest:
		if got != channelID {
			t.Fatalf("queued channelID = %q, want %q", got, channelID)
		}
	default:
		t.Fatal("expected synthetic avatar request to be queued")
	}
}

func TestNormalizeDownloadedImageKeepsRealExtension(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "twitter_avatar.download")
	png := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
		0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 'B', 0x60, 0x82,
	}
	if err := os.WriteFile(tmpPath, png, 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	finalPath, err := normalizeDownloadedImage(tmpPath, dir, "twitter_avatar")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if finalPath != filepath.Join(dir, "twitter_avatar.png") {
		t.Fatalf("finalPath = %q", finalPath)
	}
	if _, err := os.Stat(filepath.Join(dir, "twitter_avatar.png")); err != nil {
		t.Fatalf("expected png output: %v", err)
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected temp file removed, err=%v", err)
	}
}

func TestNormalizeDownloadedImageRejectsUnsafeKey(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "avatar.download")
	if err := os.WriteFile(tmpPath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	if _, err := normalizeDownloadedImage(tmpPath, dir, "../avatar"); err == nil {
		t.Fatal("normalizeDownloadedImage accepted unsafe key")
	}
}

func TestResolveVideoThumbFileRejectsOutsideDataDir(t *testing.T) {
	dataDir := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "cover.jpg")
	if err := os.WriteFile(outside, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write outside cover: %v", err)
	}

	if got := resolveVideoThumbFile(dataDir, outside); got != "" {
		t.Fatalf("resolveVideoThumbFile outside DataDir = %q, want empty", got)
	}
}

func TestSafeFolderNameRejectsDotDirs(t *testing.T) {
	if got := safeFolderName("."); got != "playlist" {
		t.Fatalf("safeFolderName dot = %q, want playlist", got)
	}
	if got := safeFolderName(".."); got != "playlist" {
		t.Fatalf("safeFolderName dotdot = %q, want playlist", got)
	}
	if got := safeFolderName("Road Trip"); got != "Road Trip" {
		t.Fatalf("safeFolderName valid = %q, want Road Trip", got)
	}
}

func testProfilePNGBytes() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
		0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 'B', 0x60, 0x82,
	}
}

// errTransient is a non-ErrNotFound sentinel used to force a transient path.
var errTransient = &tempErr{}

type tempErr struct{}

func (*tempErr) Error() string { return "transient" }
