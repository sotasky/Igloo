package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleThumbnail_Returns404WhenNoRealFile(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/media/thumbnail/unknown_video_id", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 (body: %q)", rr.Code, rr.Body.String())
	}
}

func TestHandleChannelAvatar_Returns404WhenNoRealFile(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/media/avatar/unknown_channel_id", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 (body: %q)", rr.Code, rr.Body.String())
	}
}

func TestHandleChannelAvatar_QueuesTwitterAvatarRecoveryOnMiss(t *testing.T) {
	srv := newTestServer(t)
	var got string
	srv.requestAvatar = func(channelID string) {
		got = channelID
	}

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_UserAlpha", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
	if got != "twitter_useralpha" {
		t.Fatalf("queued channelID: got %q, want %q", got, "twitter_useralpha")
	}
}

func TestHandleChannelAvatarServesCanonicalTwitterPath(t *testing.T) {
	srv := newTestServer(t)
	dir := filepath.Join(srv.cfg.DataDir, "thumbnails", "avatars")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "twitter_milkshake06.jpg"), []byte("canonical"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "twitter_MilkShake06.jpg"), []byte("stale"), 0o644)

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_MilkShake06", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "canonical" {
		t.Fatalf("avatar body = %q, want canonical lowercase file", body)
	}
}

func TestHandleChannelAvatar_QueuesYouTubeAvatarRecoveryWithCasePreserved(t *testing.T) {
	srv := newTestServer(t)
	var got string
	srv.requestAvatar = func(channelID string) {
		got = channelID
	}

	const channelID = "youtube_UCAbCdEfGhIjKlMnOpQrStUv"
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/"+channelID, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
	if got != channelID {
		t.Fatalf("queued channelID: got %q, want %q", got, channelID)
	}
}

// Avatar fetching is the dedicated background worker's job
// (internal/worker/profile.go runProfileRefreshLoop). The request handler
// must NEVER fetch on demand — see comment on handleChannelAvatar.

func TestResolveAvatarPathDiskScan(t *testing.T) {
	srv := newTestServer(t)
	dir := filepath.Join(srv.cfg.DataDir, "thumbnails", "avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "twitter_alice.jpg")
	if err := os.WriteFile(file, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := srv.resolveAvatarPath("twitter_alice"); got != file {
		t.Fatalf("expected %q, got %q", file, got)
	}
	if got := srv.resolveAvatarPath("twitter_missing"); got != "" {
		t.Fatalf("expected empty for missing, got %q", got)
	}
}

func TestResolveBannerPathDiskScan(t *testing.T) {
	srv := newTestServer(t)
	dir := filepath.Join(srv.cfg.DataDir, "thumbnails", "banners")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "youtube_UCxx.jpg")
	if err := os.WriteFile(file, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := srv.resolveBannerPath("youtube_UCxx"); got != file {
		t.Fatalf("expected %q, got %q", file, got)
	}
}

func TestHandleChannelAvatarServes(t *testing.T) {
	srv := newTestServer(t)
	dir := filepath.Join(srv.cfg.DataDir, "thumbnails", "avatars")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "twitter_a.jpg"), []byte("x"), 0o644)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_a", nil))
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleChannelAvatarUsesXAccelBehindReverseProxy(t *testing.T) {
	srv := newTestServer(t)
	dir := filepath.Join(srv.cfg.DataDir, "thumbnails", "avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "twitter_a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/media/avatar/twitter_a", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("X-Accel-Redirect"); got != "/x-accel/igloo-data/thumbnails/avatars/twitter_a.jpg" {
		t.Fatalf("X-Accel-Redirect = %q", got)
	}
	if got := rr.Body.String(); got != "" {
		t.Fatalf("body = %q, want nginx internal redirect only", got)
	}
}

func TestHandleChannelAvatarSniffsPNGWithWrongExtension(t *testing.T) {
	srv := newTestServer(t)
	dir := filepath.Join(srv.cfg.DataDir, "thumbnails", "avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "twitter_png.jpg"), testPNGBytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_png", nil))
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type: got %q, want %q", got, "image/png")
	}
}

func testPNGBytes() []byte {
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

func TestHandleSlideResolvesQuoteMediaByParentTweetID(t *testing.T) {
	srv := newTestServer(t)
	const (
		parentID = "tweet_parent"
		quoteID  = "tweet_quote"
		handle   = "quoted_author"
	)
	quoteRelPath := filepath.Join("media", "twitter", handle, quoteID+"_0.jpg")
	quoteAbsPath := filepath.Join(srv.cfg.DataDir, quoteRelPath)
	if err := os.MkdirAll(filepath.Dir(quoteAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(quoteAbsPath, make([]byte, 256), 0o644); err != nil {
		t.Fatalf("write quote media: %v", err)
	}

	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, quote_tweet_id, quote_author_handle, quote_media_json, published_at, fetched_at)
		VALUES (?, 'parent_author', 'parent_author', ?, ?, '[{"type":"photo","url":"https://cdn.example/test.jpg"}]', 1, 1)
	`, parentID, quoteID, handle); err != nil {
		t.Fatalf("insert feed_item: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('quote_media', ?, 0, ?, 'photo', 'https://cdn.example/test.jpg')
	`, quoteID, quoteRelPath); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/slide/"+parentID+"/0", nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/media/thumbnail/"+parentID, nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("thumbnail status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
}

func TestHandleStreamResolvesQuoteVideoByParentTweetID(t *testing.T) {
	srv := newTestServer(t)
	const (
		parentID = "tweet_parent_video"
		quoteID  = "tweet_quote_video"
		handle   = "quoted_video_author"
	)
	quoteRelPath := filepath.Join("media", "twitter", handle, quoteID+"_0.mp4")
	quoteAbsPath := filepath.Join(srv.cfg.DataDir, quoteRelPath)
	if err := os.MkdirAll(filepath.Dir(quoteAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(quoteAbsPath, []byte("fake-mp4"), 0o644); err != nil {
		t.Fatalf("write quote media: %v", err)
	}

	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, quote_tweet_id, quote_author_handle, quote_media_json, published_at, fetched_at)
		VALUES (?, 'parent_video_author', 'parent_video_author', ?, ?, '[{"type":"video","url":"https://cdn.example/test.mp4"}]', 1, 1)
	`, parentID, quoteID, handle); err != nil {
		t.Fatalf("insert feed_item: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('quote_media', ?, 0, ?, 'video', 'https://cdn.example/test.mp4')
	`, quoteID, quoteRelPath); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/stream/"+parentID, nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("content type: got %q, want %q", got, "video/mp4")
	}
}

func TestHandleStreamUsesXAccelBehindReverseProxy(t *testing.T) {
	srv := newTestServer(t)
	const (
		videoID = "sample_stream_xaccel"
		handle  = "sample_author"
	)
	relPath := filepath.Join("media", "twitter", handle, videoID+"_0.mp4")
	absPath := filepath.Join(srv.cfg.DataDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(absPath, []byte("fake-mp4"), 0o644); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, fetched_at)
		VALUES (?, ?, ?, '[{"type":"video","url":"https://cdn.example/test.mp4"}]', 1, 1)
	`, videoID, handle, handle); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('feed_media', ?, 0, ?, 'video', 'https://cdn.example/test.mp4')
	`, videoID, relPath); err != nil {
		t.Fatalf("insert media file: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/media/stream/"+videoID, nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Accel-Redirect"); got != "/x-accel/igloo-data/media/twitter/sample_author/sample_stream_xaccel_0.mp4" {
		t.Fatalf("X-Accel-Redirect = %q", got)
	}
	if got := rr.Body.String(); got != "" {
		t.Fatalf("body = %q, want nginx internal redirect only", got)
	}
}

func TestHandleSlideResolvesQuoteMediaByDirectQuoteTweetID(t *testing.T) {
	srv := newTestServer(t)
	const (
		parentID = "tweet_parent_direct"
		quoteID  = "tweet_quote_direct"
		handle   = "quoted_direct_author"
	)
	quoteRelPath := filepath.Join("media", "twitter", handle, quoteID+"_0.jpg")
	quoteAbsPath := filepath.Join(srv.cfg.DataDir, quoteRelPath)
	if err := os.MkdirAll(filepath.Dir(quoteAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(quoteAbsPath, make([]byte, 256), 0o644); err != nil {
		t.Fatalf("write quote media: %v", err)
	}

	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, quote_tweet_id, quote_author_handle, quote_media_json, published_at, fetched_at)
		VALUES (?, 'parent_direct_author', 'parent_direct_author', ?, ?, '[{"type":"photo","url":"https://cdn.example/direct.jpg"}]', 1, 1)
	`, parentID, quoteID, handle); err != nil {
		t.Fatalf("insert feed_item: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('quote_media', ?, 0, ?, 'photo', 'https://cdn.example/direct.jpg')
	`, quoteID, quoteRelPath); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/slide/"+quoteID+"/0", nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
}

func TestHandleStreamResolvesQuoteVideoByDirectQuoteTweetID(t *testing.T) {
	srv := newTestServer(t)
	const (
		parentID = "tweet_parent_video_direct"
		quoteID  = "tweet_quote_video_direct"
		handle   = "quoted_video_direct_author"
	)
	quoteRelPath := filepath.Join("media", "twitter", handle, quoteID+"_0.mp4")
	quoteAbsPath := filepath.Join(srv.cfg.DataDir, quoteRelPath)
	if err := os.MkdirAll(filepath.Dir(quoteAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(quoteAbsPath, []byte("fake-mp4"), 0o644); err != nil {
		t.Fatalf("write quote media: %v", err)
	}

	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, quote_tweet_id, quote_author_handle, quote_media_json, published_at, fetched_at)
		VALUES (?, 'parent_video_direct_author', 'parent_video_direct_author', ?, ?, '[{"type":"video","url":"https://cdn.example/direct.mp4"}]', 1, 1)
	`, parentID, quoteID, handle); err != nil {
		t.Fatalf("insert feed_item: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('quote_media', ?, 0, ?, 'video', 'https://cdn.example/direct.mp4')
	`, quoteID, quoteRelPath); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/stream/"+quoteID, nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("content type: got %q, want %q", got, "video/mp4")
	}
}

func TestProbeMediaFileRejectsUnsafeLegacySegments(t *testing.T) {
	srv := newTestServer(t)

	escapedByHandle := filepath.Join(srv.cfg.DataDir, "media", "outside_0.jpg")
	if err := os.MkdirAll(filepath.Dir(escapedByHandle), 0o755); err != nil {
		t.Fatalf("mkdir handle target: %v", err)
	}
	if err := os.WriteFile(escapedByHandle, make([]byte, 256), 0o644); err != nil {
		t.Fatalf("write handle target: %v", err)
	}
	if got := srv.probeMediaFile("..", "outside", 0); got != "" {
		t.Fatalf("probeMediaFile with unsafe handle = %q, want empty", got)
	}

	escapedByTweetID := filepath.Join(srv.cfg.DataDir, "media", "twitter", "outside_0.mp4")
	if err := os.MkdirAll(filepath.Dir(escapedByTweetID), 0o755); err != nil {
		t.Fatalf("mkdir tweet target: %v", err)
	}
	if err := os.WriteFile(escapedByTweetID, []byte("fake-mp4"), 0o644); err != nil {
		t.Fatalf("write tweet target: %v", err)
	}
	if got := srv.probeMediaVideoFile("sample_author", "../outside"); got != "" {
		t.Fatalf("probeMediaVideoFile with unsafe tweet ID = %q, want empty", got)
	}
}

func TestHandleSlideRejectsPrivateCDNURLWhenLocalMissing(t *testing.T) {
	srv := newTestServer(t)
	const (
		parentID = "tweet_parent_cdn_video"
		quoteID  = "tweet_quote_cdn_video"
		handle   = "quoted_cdn_author"
	)

	cdnPayload := []byte("fake-cdn-video")
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/clip.mp4" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(cdnPayload)
	}))
	defer cdn.Close()

	quoteJSON := `[{"type":"video","url":"` + cdn.URL + `/clip.mp4"}]`
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, quote_tweet_id, quote_author_handle, quote_media_json, published_at, fetched_at)
		VALUES (?, 'parent_cdn_author', 'parent_cdn_author', ?, ?, ?, 1, 1)
	`, parentID, quoteID, handle, quoteJSON); err != nil {
		t.Fatalf("insert feed_item: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/slide/"+quoteID+"/0", nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (body: %q)", rr.Code, rr.Body.String())
	}
}

func TestIsPublicAddrRejectsSpecialUseRanges(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1",
		"10.0.0.1",
		"100.64.0.1",
		"192.0.2.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"2001:db8::1",
	} {
		if isPublicAddr(netip.MustParseAddr(raw)) {
			t.Fatalf("isPublicAddr(%s) = true, want false", raw)
		}
	}
	if !isPublicAddr(netip.MustParseAddr("93.184.216.34")) {
		t.Fatal("isPublicAddr(public IPv4) = false, want true")
	}
	if !isPublicAddr(netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")) {
		t.Fatal("isPublicAddr(public IPv6) = false, want true")
	}
}

func TestStageCDNProxyBodyRejectsOversizedBeforeResponseWrite(t *testing.T) {
	body, oversized, err := stageCDNProxyBody(strings.NewReader("123456"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if !oversized {
		t.Fatal("oversized = false, want true")
	}
	if body != nil {
		t.Fatal("body should be nil for oversized responses")
	}
}

func TestStageCDNProxyBodyKeepsSmallResponse(t *testing.T) {
	body, oversized, err := stageCDNProxyBody(strings.NewReader("12345"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if oversized {
		t.Fatal("oversized = true, want false")
	}
	defer func() {
		name := body.Name()
		_ = body.Close()
		_ = os.Remove(name)
	}()
	got := make([]byte, 5)
	if _, err := body.Read(got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "12345" {
		t.Fatalf("body = %q", got)
	}
}

func testJPEGBytes() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
}

// TestHandleThumbnail_DearrowQueryServesDearrowFile: happy path — DB has a path
// and the file exists; ?da=1 should serve it and the response body should start
// with the JPEG magic bytes.
func TestHandleThumbnail_DearrowQueryServesDearrowFile(t *testing.T) {
	srv := newTestServer(t)

	const videoID = "dearrow_happy_vid"

	// Seed a channel and video with dearrow_thumb_path set.
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_test_channel', 'Test Channel', 'youtube')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	relPath := filepath.Join("thumbnails", "dearrow", videoID+".jpg")
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at, dearrow_thumb_path)
		VALUES (?, 'youtube_test_channel', 'DeArrow Happy', 60, '', 1, ?)
	`, videoID, relPath); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// Write a minimal JPEG at the expected path.
	absPath := filepath.Join(srv.cfg.DataDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(absPath, testJPEGBytes(), 0o644); err != nil {
		t.Fatalf("write dearrow thumb: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/media/thumbnail/"+videoID+"?da=1", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
	body := rr.Body.Bytes()
	if len(body) < 3 || body[0] != 0xFF || body[1] != 0xD8 || body[2] != 0xFF {
		t.Errorf("response body does not start with JPEG magic: %v", body[:min(len(body), 4)])
	}
}

// TestHandleThumbnail_DearrowQueryFallsBackWhenFileMissing: DB has a dearrow_thumb_path
// but the file does not exist on disk. Should fall through to regular resolution.
func TestHandleThumbnail_DearrowQueryFallsBackWhenFileMissing(t *testing.T) {
	srv := newTestServer(t)

	const videoID = "dearrow_missing_file"

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_test_channel2', 'Test Channel 2', 'youtube')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	// DB has a dearrow path but we deliberately don't write the file.
	dearrowRelPath := filepath.Join("thumbnails", "dearrow", videoID+".jpg")
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at, dearrow_thumb_path)
		VALUES (?, 'youtube_test_channel2', 'DeArrow Missing', 60, '', 1, ?)
	`, videoID, dearrowRelPath); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// Place a regular thumbnail via media_files so the fallback resolveThumb succeeds.
	regularRelPath := filepath.Join("media", "youtube", "test_channel2", videoID+"_0.jpg")
	regularAbsPath := filepath.Join(srv.cfg.DataDir, regularRelPath)
	if err := os.MkdirAll(filepath.Dir(regularAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(regularAbsPath, testJPEGBytes(), 0o644); err != nil {
		t.Fatalf("write fallback thumb: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('feed_media', ?, 0, ?, 'photo', 'https://cdn.example/fallback.jpg')
	`, videoID, regularRelPath); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/media/thumbnail/"+videoID+"?da=1", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (fallback should succeed) (body: %q)", rr.Code, rr.Body.String())
	}
}

// TestHandleThumbnail_DearrowQueryFallsBackWhenNoPath: video has no dearrow_thumb_path
// in DB. Should fall through to regular resolution without error.
func TestHandleThumbnail_DearrowQueryFallsBackWhenNoPath(t *testing.T) {
	srv := newTestServer(t)

	const videoID = "dearrow_no_path"

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_test_channel3', 'Test Channel 3', 'youtube')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	// No dearrow_thumb_path column value (NULL).
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES (?, 'youtube_test_channel3', 'No DeArrow', 60, '', 1)
	`, videoID); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// Place a regular thumbnail so the fallback succeeds.
	regularRelPath := filepath.Join("media", "youtube", "test_channel3", videoID+"_0.jpg")
	regularAbsPath := filepath.Join(srv.cfg.DataDir, regularRelPath)
	if err := os.MkdirAll(filepath.Dir(regularAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(regularAbsPath, testJPEGBytes(), 0o644); err != nil {
		t.Fatalf("write fallback thumb: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('feed_media', ?, 0, ?, 'photo', 'https://cdn.example/nopath.jpg')
	`, videoID, regularRelPath); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/media/thumbnail/"+videoID+"?da=1", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (fallback should succeed) (body: %q)", rr.Code, rr.Body.String())
	}
}

// TestHandleThumbnail_NoQueryUsesOriginal: without ?da=1, original resolution
// is used. Verifies the normal path still works end-to-end.
func TestHandleThumbnail_NoQueryUsesOriginal(t *testing.T) {
	srv := newTestServer(t)

	const videoID = "no_dearrow_query"

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_test_channel4', 'Test Channel 4', 'youtube')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	// Seed video with a dearrow path — but we only write the regular thumb, not
	// the dearrow file. Without ?da=1 the dearrow branch should never be attempted.
	dearrowRelPath := filepath.Join("thumbnails", "dearrow", videoID+".jpg")
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at, dearrow_thumb_path)
		VALUES (?, 'youtube_test_channel4', 'Normal Query', 60, '', 1, ?)
	`, videoID, dearrowRelPath); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// Write a regular thumbnail (not the dearrow file).
	regularRelPath := filepath.Join("media", "youtube", "test_channel4", videoID+"_0.jpg")
	regularAbsPath := filepath.Join(srv.cfg.DataDir, regularRelPath)
	if err := os.MkdirAll(filepath.Dir(regularAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(regularAbsPath, testJPEGBytes(), 0o644); err != nil {
		t.Fatalf("write regular thumb: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('feed_media', ?, 0, ?, 'photo', 'https://cdn.example/regular.jpg')
	`, videoID, regularRelPath); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/media/thumbnail/"+videoID, nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
}

func TestHandleAudioServesFeedMediaSlideshowTrack(t *testing.T) {
	srv := newTestServer(t)
	const (
		videoID = "slide_audio_001"
		handle  = "demo_author"
	)

	audioRelPath := filepath.Join("media", "tiktok", handle, videoID+"_0.mp3")
	audioAbsPath := filepath.Join(srv.cfg.DataDir, audioRelPath)
	if err := os.MkdirAll(filepath.Dir(audioAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(audioAbsPath, []byte("fake-mp3"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES (?, 'tiktok_demo_author', 'Slide audio', 0, '', 1)
	`, videoID); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, fetched_at)
		VALUES (?, ?, ?, '[{"type":"video"}]', 1, 1)
	`, videoID, handle, handle); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url)
		VALUES ('feed_media', ?, 4, ?, 'video', 'https://cdn.example/audio.mp3')
	`, videoID, audioRelPath); err != nil {
		t.Fatalf("insert media file: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/audio/"+videoID, nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
}
