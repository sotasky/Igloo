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
