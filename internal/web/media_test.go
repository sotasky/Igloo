package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestCommentAuthorAvatarUsesCanonicalTypedAsset(t *testing.T) {
	srv := newTestServer(t)
	comments := []model.Comment{{
		AuthorID:        "UC_sample_channel",
		AuthorThumbnail: "https://yt3.example/raw.jpg",
	}}
	srv.projectCommentAuthorAvatars(comments)
	if comments[0].AuthorThumbnail != "" {
		t.Fatalf("missing canonical avatar projected %q", comments[0].AuthorThumbnail)
	}

	const ownerID = "youtube_UC_sample_channel"
	const key = "thumbnails/comment-authors/sample.jpg"
	path, err := srv.cfg.Storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, testJPEGBytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.StoreReadyAsset(db.Asset{
		AssetID:   db.BuildAssetID("youtube", "comment_author", ownerID, "avatar", 0),
		AssetKind: "avatar", OwnerKind: "comment_author", OwnerID: ownerID,
		FilePath: key, ContentType: "image/jpeg", RequiredReason: "comment_avatar",
	}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	srv.projectCommentAuthorAvatars(comments)
	if comments[0].AuthorThumbnail != "/api/media/comment-avatar/"+ownerID {
		t.Fatalf("projected avatar = %q", comments[0].AuthorThumbnail)
	}
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", comments[0].AuthorThumbnail, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("comment avatar status = %d", rr.Code)
	}
}

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
	req := httptest.NewRequest("GET", "/api/media/avatar/twitter_missing", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 (body: %q)", rr.Code, rr.Body.String())
	}
	job, err := srv.db.GetProfileJob("twitter_missing")
	if err != nil || job != nil {
		t.Fatalf("avatar miss queued work: %+v / %v", job, err)
	}
}

func TestHandleChannelAvatarRequiresCanonicalReadyAsset(t *testing.T) {
	srv := newTestServer(t)
	dir := filepath.Join(srv.cfg.Storage.StateRoot(), "thumbnails", "avatars")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "twitter_sample_creator.jpg"), []byte("legacy"), 0o644)

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_sample_creator", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("legacy file status = %d, want 404", rr.Code)
	}

	storeReadyProfileAsset(t, srv, "twitter_sample_creator", "avatar", []byte("canonical"), "image/jpeg")
	rr = httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_sample_creator", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "canonical" {
		t.Fatalf("canonical response = %d %q", rr.Code, rr.Body.String())
	}
}

func TestHandleChannelAvatarCanonicalizesTwitterButPreservesYouTubeCase(t *testing.T) {
	srv := newTestServer(t)
	storeReadyProfileAsset(t, srv, "twitter_samplecase", "avatar", []byte("twitter"), "image/jpeg")
	const channelID = "youtube_test_case"
	storeReadyProfileAsset(t, srv, channelID, "avatar", []byte("youtube"), "image/jpeg")

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_SampleCase", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "twitter" {
		t.Fatalf("twitter response = %d %q", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/"+channelID, nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "youtube" {
		t.Fatalf("youtube response = %d %q", rr.Code, rr.Body.String())
	}
}

func TestHandleChannelAvatarRejectsTweetOwnedContentAsset(t *testing.T) {
	srv := newTestServer(t)
	const quoteID = "quoted_status_123"
	relPath := filepath.Join("thumbnails", "avatars", quoteID+".jpg")
	absPath := filepath.Join(srv.cfg.Storage.StateRoot(), relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("quote-avatar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.StoreReadyAsset(db.Asset{
		AssetID:        db.BuildAssetID("twitter", "tweet", quoteID, "avatar", 0),
		AssetKind:      "avatar",
		OwnerKind:      "tweet",
		OwnerID:        quoteID,
		FilePath:       relPath,
		ContentType:    "image/jpeg",
		RequiredReason: "identity",
	}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/"+quoteID, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("tweet-owned avatar status = %d, want 404", rr.Code)
	}
}

func TestResolveProfileAssetPathsUseInventory(t *testing.T) {
	srv := newTestServer(t)
	avatar := storeReadyProfileAsset(t, srv, "twitter_sample_paths", "avatar", []byte("avatar"), "image/jpeg")
	banner := storeReadyProfileAsset(t, srv, "twitter_sample_paths", "banner", []byte("banner"), "image/jpeg")
	if got := srv.resolveAvatarPath("twitter_sample_paths"); got != avatar {
		t.Fatalf("avatar path = %q, want %q", got, avatar)
	}
	if got := srv.resolveBannerPath("twitter_sample_paths"); got != banner {
		t.Fatalf("banner path = %q, want %q", got, banner)
	}
	if got := srv.resolveAvatarPath("twitter_missing"); got != "" {
		t.Fatalf("missing avatar path = %q", got)
	}
}

func TestHandleChannelAvatarServes(t *testing.T) {
	srv := newTestServer(t)
	storeReadyProfileAsset(t, srv, "twitter_sample_serve", "avatar", []byte("x"), "image/jpeg")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_sample_serve", nil))
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestHandleChannelAvatarUsesXAccelBehindReverseProxy(t *testing.T) {
	srv := newTestServer(t)
	storeReadyProfileAsset(t, srv, "twitter_sample_accel", "avatar", []byte("x"), "image/jpeg")

	req := httptest.NewRequest("GET", "/api/media/avatar/twitter_sample_accel", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("X-Accel-Redirect"); got != "/x-accel/igloo-state/thumbnails/avatars/twitter_sample_accel.jpg" {
		t.Fatalf("X-Accel-Redirect = %q", got)
	}
	if got := rr.Body.String(); got != "" {
		t.Fatalf("body = %q, want nginx internal redirect only", got)
	}
}

func TestHandleChannelAvatarUsesCanonicalContentType(t *testing.T) {
	srv := newTestServer(t)
	storeReadyProfileAsset(t, srv, "twitter_sample_png", "avatar", testPNGBytes(), "")

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/media/avatar/twitter_sample_png", nil))
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type: got %q, want canonical %q", got, "image/png")
	}
}

func storeReadyProfileAsset(t *testing.T, srv *testServer, channelID, assetKind string, body []byte, contentType string) string {
	t.Helper()
	dir := assetKind + "s"
	relPath := filepath.Join("thumbnails", dir, channelID+".jpg")
	absPath := filepath.Join(srv.cfg.Storage.StateRoot(), relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.StoreReadyAsset(db.Asset{
		AssetID:        db.BuildAssetID(platformOfChannelID(channelID), "channel", channelID, assetKind, 0),
		AssetKind:      assetKind,
		OwnerKind:      "channel",
		OwnerID:        channelID,
		SourceURL:      "https://cdn.example.invalid/" + assetKind + ".jpg",
		FilePath:       relPath,
		ContentType:    contentType,
		RequiredReason: "identity",
	}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return absPath
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

func storeReadyMediaAsset(t *testing.T, srv *testServer, platform, ownerKind, ownerID, kind string, index int, relPath, contentType string, body []byte) string {
	t.Helper()
	if db.IsCanonicalVideoOwnerKind(ownerKind) && ownerKind != "tweet" {
		channelID := platform + "_asset_fixture"
		if err := srv.db.ExecRaw(`
			INSERT OR IGNORE INTO channels (channel_id, name, platform)
			VALUES (?, 'Asset Fixture', ?)
		`, channelID, platform); err != nil {
			t.Fatalf("seed canonical video channel: %v", err)
		}
		if err := srv.db.ExecRaw(`
			INSERT OR IGNORE INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
			VALUES (?, ?, ?, 'Asset Fixture', 0, 1)
		`, ownerID, channelID, ownerKind); err != nil {
			t.Fatalf("seed canonical video owner: %v", err)
		}
		video, err := srv.db.GetVideo(ownerID)
		if err != nil || video == nil || video.OwnerKind != ownerKind {
			t.Fatalf("canonical video owner = %+v, err=%v, want %s", video, err, ownerKind)
		}
	}
	absPath, err := srv.cfg.Storage.Path(relPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.StoreReadyAsset(db.Asset{
		AssetID:   db.BuildAssetID(platform, ownerKind, ownerID, kind, index),
		AssetKind: kind, OwnerKind: ownerKind, OwnerID: ownerID, MediaIndex: index,
		FilePath: relPath, ContentType: contentType, RequiredReason: "retention",
	}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return absPath
}

func TestHandleSlideUsesExactOwnerKind(t *testing.T) {
	srv := newTestServer(t)
	const contentID = "shared_asset_id"
	youtubePath := filepath.Join("media", "youtube", contentID+".jpg")
	tweetPath := filepath.Join("media", "twitter", contentID+".jpg")
	storeReadyMediaAsset(t, srv, "youtube", "youtube_video", contentID, "post_media", 0, youtubePath, "image/jpeg", testJPEGBytes())
	storeReadyMediaAsset(t, srv, "twitter", "tweet", contentID, "post_media", 0, tweetPath, "image/jpeg", testJPEGBytes())

	for _, tc := range []struct {
		url  string
		want string
	}{
		{url: "/api/media/slide/" + contentID + "/0", want: "/x-accel/igloo-media/youtube/" + contentID + ".jpg"},
		{url: "/api/media/slide/" + contentID + "/0?owner_kind=tweet", want: "/x-accel/igloo-media/twitter/" + contentID + ".jpg"},
	} {
		req := httptest.NewRequest("GET", tc.url, nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status: got %d, want 200", tc.url, rr.Code)
		}
		if got := rr.Header().Get("X-Accel-Redirect"); got != tc.want {
			t.Fatalf("%s: X-Accel-Redirect = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestHandleStreamUsesXAccelBehindReverseProxy(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct {
		platform  string
		ownerKind string
		id        string
		query     string
		want      string
	}{
		{platform: "youtube", ownerKind: "youtube_video", id: "sample_video_stream", want: "/x-accel/igloo-media/youtube/sample_video_stream.mp4"},
		{platform: "twitter", ownerKind: "tweet", id: "sample_feed_stream", query: "?owner_kind=tweet", want: "/x-accel/igloo-media/twitter/sample_feed_stream.mp4"},
	} {
		relPath := filepath.Join("media", tc.platform, tc.id+".mp4")
		storeReadyMediaAsset(t, srv, tc.platform, tc.ownerKind, tc.id, "video_stream", 0, relPath, "video/mp4", []byte("fake-mp4"))
		req := httptest.NewRequest("GET", "/api/media/stream/"+tc.id+tc.query, nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status: got %d, want 200 (body: %q)", tc.id, rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("X-Accel-Redirect"); got != tc.want {
			t.Fatalf("%s: X-Accel-Redirect = %q, want %q", tc.id, got, tc.want)
		}
		if got := rr.Body.String(); got != "" {
			t.Fatalf("%s: body = %q, want nginx internal redirect only", tc.id, got)
		}
	}
}

func TestHandleSlideRejectsPrivateCDNURLWhenLocalMissing(t *testing.T) {
	srv := newTestServer(t)
	const (
		parentID = "tweet_parent_cdn_video"
		quoteID  = "tweet_quote_cdn_video"
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
		INSERT INTO feed_items (tweet_id, quote_tweet_id, quote_media_json, published_at, fetched_at)
		VALUES (?, ?, ?, 1, 1)
	`, parentID, quoteID, quoteJSON); err != nil {
		t.Fatalf("insert feed_item: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/slide/"+quoteID+"/0?owner_kind=tweet", nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (body: %q)", rr.Code, rr.Body.String())
	}
}

func testJPEGBytes() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
}

// TestHandleThumbnail_DearrowQueryServesDearrowFile verifies that the selected
// canonical DeArrow asset is served when it is ready.
func TestHandleThumbnail_DearrowQueryServesDearrowFile(t *testing.T) {
	srv := newTestServer(t)
	const videoID = "dearrow_happy_vid"
	relPath := filepath.Join("thumbnails", "dearrow", videoID+".jpg")
	storeReadyMediaAsset(t, srv, "youtube", "youtube_video", videoID, "dearrow_thumbnail", 0, relPath, "image/jpeg", testJPEGBytes())

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

func TestHandleThumbnail_DearrowQueryDoesNotGuessAroundStaleReadyAsset(t *testing.T) {
	srv := newTestServer(t)
	const videoID = "dearrow_missing_file"
	dearrowRelPath := filepath.Join("thumbnails", "dearrow", videoID+".jpg")
	assetID := db.BuildAssetID("youtube", "youtube_video", videoID, "dearrow_thumbnail", 0)
	if err := srv.db.DeclareAsset(db.Asset{AssetID: assetID, AssetKind: "dearrow_thumbnail", OwnerKind: "youtube_video", OwnerID: videoID, SourceURL: "https://example.test/missing.jpg", ContentType: "image/jpeg"}, 1); err != nil {
		t.Fatalf("insert missing canonical asset: %v", err)
	}
	if err := srv.db.ExecRaw(`
		UPDATE media_objects
		SET published_revision = desired_revision, published_source_url = source_url,
		    file_path = ?, content_type = 'image/jpeg', size_bytes = 1, file_mtime_ns = 1, job_state = 'ready'
		WHERE object_id = (SELECT desired_object_id FROM assets WHERE asset_id = ?)
	`, dearrowRelPath, assetID); err != nil {
		t.Fatalf("publish missing canonical metadata: %v", err)
	}
	regularRelPath := filepath.Join("thumbnails", "original", videoID+".jpg")
	storeReadyMediaAsset(t, srv, "youtube", "youtube_video", videoID, "post_thumbnail", 0, regularRelPath, "image/jpeg", testJPEGBytes())

	req := httptest.NewRequest("GET", "/api/media/thumbnail/"+videoID+"?da=1", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 for the selected ready row with missing bytes (body: %q)", rr.Code, rr.Body.String())
	}
}

// TestHandleThumbnail_DearrowQueryFallsBackWhenNoPath verifies that the
// original thumbnail remains usable when there is no DeArrow asset.
func TestHandleThumbnail_DearrowQueryFallsBackWhenNoPath(t *testing.T) {
	srv := newTestServer(t)
	const videoID = "dearrow_no_path"
	regularRelPath := filepath.Join("thumbnails", "original", videoID+".jpg")
	storeReadyMediaAsset(t, srv, "youtube", "youtube_video", videoID, "post_thumbnail", 0, regularRelPath, "image/jpeg", testJPEGBytes())

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
	dearrowRelPath := filepath.Join("thumbnails", "dearrow", videoID+".jpg")
	storeReadyMediaAsset(t, srv, "youtube", "youtube_video", videoID, "dearrow_thumbnail", 0, dearrowRelPath, "image/jpeg", append(testJPEGBytes(), 'd'))
	regularRelPath := filepath.Join("thumbnails", "original", videoID+".jpg")
	storeReadyMediaAsset(t, srv, "youtube", "youtube_video", videoID, "post_thumbnail", 0, regularRelPath, "image/jpeg", testJPEGBytes())

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
	const videoID = "slide_audio_001"

	audioRelPath := filepath.Join("media", "tiktok", "sample_author", videoID+"_0.mp3")
	storeReadyMediaAsset(t, srv, "tiktok", "tiktok_video", videoID, "post_audio", 0, audioRelPath, "audio/mpeg", []byte("fake-mp3"))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/media/audio/"+videoID, nil)
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %q)", rr.Code, rr.Body.String())
	}
}
