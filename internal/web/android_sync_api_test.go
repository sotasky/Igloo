package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/worker"
)

func TestAndroidSyncGenerationPublishesServeableAssets(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip.mp4"), []byte("fake-mp4-bytes"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "avatars", "youtube_sample_channel.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})

	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform, created_at, sync_seq)
		VALUES ('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube', ?, 1)
	`, now); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'youtube_sample_channel', ?)
	`, now); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name, avatar_url, fetched_at)
		VALUES ('youtube_sample_channel', 'youtube', 'sample_channel', 'Sample Channel', 'https://example.invalid/a.jpg', ?)
	`, now); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, thumbnail_path, file_path, file_size, published_at, downloaded_at, sync_seq)
		VALUES ('clip', 'youtube_sample_channel', 'Clip', 12, 'videos/youtube/clip.jpg', 'videos/youtube/clip.mp4', 14, ?, ?, 2)
	`, now, now); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/android/sync/generation/latest", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("latest status = %d body=%s", rec.Code, rec.Body.String())
	}
	var latest struct {
		Generation model.AndroidSyncGeneration `json:"generation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &latest); err != nil {
		t.Fatalf("decode latest: %v", err)
	}
	if latest.Generation.ItemCount == 0 {
		t.Fatalf("generation has no items: %+v", latest.Generation)
	}
	if got, want := latest.Generation.GenerationID, "android-sync-"; len(got) < len(want) || got[:len(want)] != want {
		t.Fatalf("generation_id = %q, want %s* prefix", got, want)
	}
	if latest.Generation.ReadyAssetCount < 3 {
		t.Fatalf("ready assets = %d, want at least stream/thumb/avatar; gen=%+v", latest.Generation.ReadyAssetCount, latest.Generation)
	}

	req = httptest.NewRequest("GET", "/api/android/sync/generation/"+latest.Generation.GenerationID+"/assets", nil)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("assets status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Assets []model.AndroidSyncAsset `json:"assets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode assets: %v", err)
	}
	var stream *model.AndroidSyncAsset
	for i := range page.Assets {
		if page.Assets[i].AssetKind == "video_stream" && page.Assets[i].OwnerID == "clip" {
			stream = &page.Assets[i]
			break
		}
	}
	if stream == nil {
		t.Fatalf("video_stream asset missing from page: %+v", page.Assets)
	}
	if stream.SHA256 == "" || stream.SizeBytes != 14 {
		t.Fatalf("stream hash/size not materialized: %+v", *stream)
	}
	if stream.ServerURL != "/api/android/sync/assets/"+stream.AssetID {
		t.Fatalf("stream server_url = %q", stream.ServerURL)
	}

	req = httptest.NewRequest("GET", "/api/android/sync/assets/"+stream.AssetID, nil)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "fake-mp4-bytes" {
		t.Fatalf("asset body = %q", got)
	}
}

func TestAndroidSyncLatestPrunesOldServerState(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	nowMs := time.Now().UnixMilli()
	oldBaseMs := nowMs - int64(24*time.Hour/time.Millisecond)
	generationIDs := []string{
		"android-sync-web-prune-1",
		"android-sync-web-prune-2",
		"android-sync-web-prune-3",
		"android-sync-web-prune-4",
		"android-sync-web-prune-5",
	}
	for i, generationID := range generationIDs {
		storeAndroidSyncGenerationForWebTest(t, srv.db, generationID, oldBaseMs+int64(i))
	}

	req := httptest.NewRequest("GET", "/api/android/sync/generation/latest", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("latest status = %d body=%s", rec.Code, rec.Body.String())
	}
	var latest struct {
		Generation model.AndroidSyncGeneration `json:"generation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &latest); err != nil {
		t.Fatalf("decode latest: %v", err)
	}
	if latest.Generation.GenerationID == "" {
		t.Fatalf("latest generation missing id: %+v", latest.Generation)
	}

	req = httptest.NewRequest("GET", "/api/android/sync/generation/"+latest.Generation.GenerationID+"/items", nil)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("items status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		GenerationID string                  `json:"generation_id"`
		Items        []model.AndroidSyncItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if page.GenerationID != latest.Generation.GenerationID || len(page.Items) == 0 {
		t.Fatalf("items page = %+v, latest=%+v", page, latest.Generation)
	}

	var remainingSeedGenerations int
	if err := srv.db.QueryRow(`
		SELECT COUNT(*)
		FROM android_sync_generations
		WHERE generation_id LIKE 'android-sync-web-prune-%'
	`).Scan(&remainingSeedGenerations); err != nil {
		t.Fatalf("count old generations: %v", err)
	}
	if remainingSeedGenerations != 1 {
		t.Fatalf("remaining old generations = %d, want 1", remainingSeedGenerations)
	}
}

func TestAndroidSyncLatestKeepsStaleSourceReusePageableAfterPrune(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	nowMs := time.Now().UnixMilli()
	retention := db.AndroidRetentionSettings{FeedDays: 7, YoutubeDays: 7, MomentsDays: 7, StoryHours: 48}
	sourceVersion, err := srv.db.AndroidSyncSourceVersion("alice", retention)
	if err != nil {
		t.Fatalf("source version: %v", err)
	}
	staleGenerationID := "android-sync-" + sourceVersion[:16]
	staleCreatedAt := nowMs - int64(48*time.Hour/time.Millisecond)
	storeAndroidSyncGenerationForWebTestWithSource(
		t,
		srv.db,
		staleGenerationID,
		staleCreatedAt,
		sourceVersion,
		map[string]int{
			"feed_days":            retention.FeedDays,
			"youtube_days":         retention.YoutubeDays,
			"moments_days":         retention.MomentsDays,
			"story_hours":          retention.StoryHours,
			"materializer_version": db.AndroidSyncMaterializerVersion,
		},
	)
	for i, generationID := range []string{
		"android-sync-source-reuse-newer-1",
		"android-sync-source-reuse-newer-2",
		"android-sync-source-reuse-newer-3",
	} {
		storeAndroidSyncGenerationForWebTest(t, srv.db, generationID, staleCreatedAt+int64(i+1))
	}

	req := httptest.NewRequest("GET", "/api/android/sync/generation/latest?feed_days=7&youtube_days=7&moments_days=7&story_hours=48", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("latest status = %d body=%s", rec.Code, rec.Body.String())
	}
	var latest struct {
		Generation model.AndroidSyncGeneration `json:"generation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &latest); err != nil {
		t.Fatalf("decode latest: %v", err)
	}
	if latest.Generation.GenerationID != staleGenerationID {
		t.Fatalf("latest generation id = %q, want stale source reuse %q", latest.Generation.GenerationID, staleGenerationID)
	}

	req = httptest.NewRequest("GET", "/api/android/sync/generation/"+latest.Generation.GenerationID+"/items", nil)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("items status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []model.AndroidSyncItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items after latest prune = %d, want returned stale generation to remain pageable", len(page.Items))
	}
}

func TestAndroidSyncLatestRequestPruneDrainsBatchRemainder(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	nowMs := time.Now().UnixMilli()
	oldBaseMs := nowMs - int64(48*time.Hour/time.Millisecond)
	const generationCount = 55
	for i := 0; i < generationCount; i++ {
		generationID := fmt.Sprintf("android-sync-web-batch-prune-%02d", i+1)
		storeAndroidSyncGenerationForWebTest(t, srv.db, generationID, oldBaseMs+int64(i))
	}

	req := httptest.NewRequest("GET", "/api/android/sync/generation/latest", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("latest status = %d body=%s", rec.Code, rec.Body.String())
	}

	remainingSeedGenerations := countAndroidSyncGenerationsForWebTest(t, srv.db, "android-sync-web-batch-prune-%")
	if remainingSeedGenerations != 1 {
		t.Fatalf("remaining old generations after request drain = %d, want 1 newest seed generation", remainingSeedGenerations)
	}
}

func TestAndroidSyncHealthPrunesOldReportsWithoutChangingResponse(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	nowMs := time.Now().UnixMilli()
	generationID := "android-sync-web-health-prune"
	storeAndroidSyncGenerationForWebTest(t, srv.db, generationID, nowMs-int64(24*time.Hour/time.Millisecond))
	for i := 1; i <= 205; i++ {
		if err := srv.db.RecordAndroidSyncHealth(generationID, int64(i), []byte(`{"retention":{"feed_days":7}}`), i, 0, 0, 0, i, int64(i)); err != nil {
			t.Fatalf("RecordAndroidSyncHealth %d: %v", i, err)
		}
	}

	req := httptest.NewRequest("POST", "/api/android/sync/health", strings.NewReader(`{
		"generation_id":"android-sync-web-health-prune",
		"reported_at_ms":1000,
		"counts":{"verified":1,"pending":0,"failed":0,"missing":0,"total":1},
		"bytes":{"verified":123},
		"retention":{"feed_days":7,"youtube_days":7,"moments_days":7,"story_hours":48}
	}`))
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if !body.Success {
		t.Fatalf("health response success = false body=%s", rec.Body.String())
	}

	var reports int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM android_sync_health_reports`).Scan(&reports); err != nil {
		t.Fatalf("count health reports: %v", err)
	}
	if reports != 200 {
		t.Fatalf("health report count = %d, want 200", reports)
	}
	latest, err := srv.db.GetLatestAndroidSyncHealthReport()
	if err != nil {
		t.Fatalf("latest health: %v", err)
	}
	if latest == nil || latest.GenerationID != generationID || latest.ReportedAtMs != 1000 {
		t.Fatalf("latest health = %+v, want generation %s at 1000", latest, generationID)
	}
}

func TestAndroidSyncAssetReturnsTooManyRequestsWhenServeSlotsFull(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir

	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "avatars", "sample_channel_a.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})

	gen := model.AndroidSyncGeneration{
		GenerationID:    "android-sync-throttle",
		CreatedAtMs:     time.Now().UnixMilli(),
		Status:          "ready",
		SourceVersion:   strings.Repeat("a", 64),
		Retention:       map[string]int{"materializer_version": db.AndroidSyncMaterializerVersion},
		AssetCount:      1,
		ReadyAssetCount: 1,
	}
	asset := model.AndroidSyncAsset{
		GenerationID: "android-sync-throttle",
		Seq:          1,
		AssetID:      "sample_channel_a_avatar",
		AssetKind:    "avatar",
		OwnerID:      "sample_channel_a",
		OwnerKind:    "channel",
		Bucket:       "avatar",
		ServerURL:    "/api/android/sync/assets/sample_channel_a_avatar",
		ContentType:  "image/jpeg",
		SizeBytes:    6,
		SHA256:       "unused",
		State:        "ready",
	}
	if err := srv.db.StoreAndroidSyncGeneration(gen, nil, []model.AndroidSyncAsset{asset}); err != nil {
		t.Fatalf("store generation: %v", err)
	}

	for i := 0; i < androidSyncAssetServeLimit; i++ {
		if !srv.tryAcquireAndroidSyncAssetServeSlot() {
			t.Fatalf("acquire slot %d failed", i)
		}
		defer srv.releaseAndroidSyncAssetServeSlot()
	}

	req := httptest.NewRequest("GET", "/api/android/sync/assets/sample_channel_a_avatar", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("asset status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}
}

func TestAndroidSyncAssetUsesXAccelBehindReverseProxy(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir

	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "avatars", "sample_channel_a.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})

	gen := model.AndroidSyncGeneration{
		GenerationID:    "android-sync-xaccel",
		CreatedAtMs:     time.Now().UnixMilli(),
		Status:          "ready",
		SourceVersion:   strings.Repeat("b", 64),
		Retention:       map[string]int{"materializer_version": db.AndroidSyncMaterializerVersion},
		AssetCount:      1,
		ReadyAssetCount: 1,
	}
	asset := model.AndroidSyncAsset{
		GenerationID: "android-sync-xaccel",
		Seq:          1,
		AssetID:      "sample_channel_a_avatar",
		AssetKind:    "avatar",
		OwnerID:      "sample_channel_a",
		OwnerKind:    "channel",
		Bucket:       "avatar",
		ServerURL:    "/api/android/sync/assets/sample_channel_a_avatar",
		ContentType:  "image/jpeg",
		SizeBytes:    6,
		SHA256:       "unused",
		State:        "ready",
	}
	if err := srv.db.StoreAndroidSyncGeneration(gen, nil, []model.AndroidSyncAsset{asset}); err != nil {
		t.Fatalf("store generation: %v", err)
	}

	for i := 0; i < androidSyncAssetServeLimit; i++ {
		if !srv.tryAcquireAndroidSyncAssetServeSlot() {
			t.Fatalf("acquire slot %d failed", i)
		}
		defer srv.releaseAndroidSyncAssetServeSlot()
	}

	req := httptest.NewRequest("GET", "/api/android/sync/assets/sample_channel_a_avatar", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Accel-Redirect"); got != "/x-accel/igloo-data/thumbnails/avatars/sample_channel_a.jpg" {
		t.Fatalf("X-Accel-Redirect = %q", got)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("asset body = %q, want nginx internal redirect only", got)
	}
}

func TestAndroidSyncOldV3RoutesReturn404(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	for _, path := range []string{
		"/api/android/v3/generation/latest",
		"/api/android/v3/generation/android-v3-old/items",
		"/api/android/v3/generation/android-v3-old/assets",
		"/api/android/v3/assets/asset-old",
	} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, attachTestAuth(req, "alice"))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestAndroidSyncItemsIncludesFeedRankSnapshot(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	for _, row := range []struct {
		id     string
		handle string
	}{
		{id: "rank_keep", handle: "alice"},
		{id: "rank_seen", handle: "bob"},
		{id: "rank_outside", handle: "cara"},
	} {
		if err := srv.db.ExecRaw(`
			INSERT INTO feed_items (
				tweet_id, source_handle, author_handle, body_text,
				published_at, fetched_at, sync_seq
			) VALUES (?, ?, ?, 'body', ?, ?, ?)`,
			row.id, row.handle, row.handle, now, now, 1,
		); err != nil {
			t.Fatalf("insert feed item %s: %v", row.id, err)
		}
	}
	if err := srv.db.ReplaceFeedRankSnapshot("alice", []db.SnapshotRow{
		{TweetID: "rank_keep", RankPosition: 1, FinalScore: 10},
		{TweetID: "rank_seen", RankPosition: 2, FinalScore: 9},
		{TweetID: "rank_outside", RankPosition: 3, FinalScore: 8},
	}); err != nil {
		t.Fatalf("replace rank snapshot: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES ('alice', 'rank_seen', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert seen: %v", err)
	}

	items, counts, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"rank_keep": {},
			"rank_seen": {},
		},
		Videos:   map[string]struct{}{},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}
	if counts["feed_rank"] != 1 {
		t.Fatalf("feed_rank count = %d, want 1", counts["feed_rank"])
	}

	var rank deltaBundle
	for _, item := range items {
		if item.ItemKind == "feed_rank" {
			if item.ItemID != "snapshot" {
				t.Fatalf("feed rank item id = %q, want snapshot", item.ItemID)
			}
			if err := json.Unmarshal(item.PayloadJSON, &rank); err != nil {
				t.Fatalf("decode rank payload: %v", err)
			}
			break
		}
	}
	if rank.PrimaryKind != "feed_rank" {
		t.Fatalf("rank primary_kind = %q, want feed_rank", rank.PrimaryKind)
	}
	if got := int(rank.Primary["row_count"].(float64)); got != 1 {
		t.Fatalf("row_count = %d, want 1 payload=%#v", got, rank.Primary)
	}
	rows, ok := rank.Primary["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %#v, want one row", rank.Primary["rows"])
	}
	got := rows[0].(map[string]any)
	if got["tweet_id"] != "rank_keep" || got["rank_position"] != float64(1) {
		t.Fatalf("rank row = %#v, want rank_keep position 1", got)
	}
}

func TestAndroidSyncItemsEmitsEmptyFreshFeedRankSnapshot(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, body_text,
			published_at, fetched_at, sync_seq
		) VALUES ('rank_seen_only', 'alice', 'alice', 'body', ?, ?, 1)`,
		now, now,
	); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := srv.db.ReplaceFeedRankSnapshot("alice", []db.SnapshotRow{
		{TweetID: "rank_seen_only", RankPosition: 1, FinalScore: 10},
	}); err != nil {
		t.Fatalf("replace rank snapshot: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES ('alice', 'rank_seen_only', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert seen: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"rank_seen_only": {},
		},
		Videos:   map[string]struct{}{},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}

	for _, item := range items {
		if item.ItemKind != "feed_rank" {
			continue
		}
		var rank deltaBundle
		if err := json.Unmarshal(item.PayloadJSON, &rank); err != nil {
			t.Fatalf("decode rank payload: %v", err)
		}
		if got := int(rank.Primary["row_count"].(float64)); got != 0 {
			t.Fatalf("row_count = %d, want 0", got)
		}
		rows, ok := rank.Primary["rows"].([]any)
		if !ok || len(rows) != 0 {
			t.Fatalf("rows = %#v, want empty array", rank.Primary["rows"])
		}
		return
	}
	t.Fatalf("feed_rank item missing from payloads: %+v", items)
}

func TestAndroidSyncItemsIncludesBookmarkMetadataSnapshot(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	catID, err := srv.db.CreateBookmarkCategory("alice", "Archive", "/archive/alice")
	if err != nil {
		t.Fatalf("create alice category: %v", err)
	}
	if _, err := srv.db.CreateBookmarkCategory("bob", "Bob Private", "/archive/bob"); err != nil {
		t.Fatalf("create bob category: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, bookmarked_at)
		VALUES
			('alice', 'alice_saved', ?, 'Saved Label', ?),
			('bob', 'bob_saved', 0, 'Bob Label', ?)
	`, catID, now, now); err != nil {
		t.Fatalf("insert bookmark labels: %v", err)
	}

	items, counts, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}
	if counts["bookmark_metadata"] != 1 {
		t.Fatalf("bookmark_metadata count = %d, want 1", counts["bookmark_metadata"])
	}

	var snapshots []deltaBundle
	for _, item := range items {
		if item.ItemKind != "bookmark_metadata" {
			continue
		}
		if item.ItemID != "snapshot" {
			t.Fatalf("bookmark metadata item id = %q, want snapshot", item.ItemID)
		}
		var bundle deltaBundle
		if err := json.Unmarshal(item.PayloadJSON, &bundle); err != nil {
			t.Fatalf("decode bookmark metadata: %v", err)
		}
		snapshots = append(snapshots, bundle)
	}
	if len(snapshots) != 1 {
		t.Fatalf("bookmark_metadata snapshots = %d, want 1; items=%+v", len(snapshots), items)
	}
	snapshot := snapshots[0]
	if snapshot.PrimaryKind != "bookmark_metadata" {
		t.Fatalf("primary_kind = %q, want bookmark_metadata", snapshot.PrimaryKind)
	}
	categories, ok := snapshot.Primary["categories"].([]any)
	if !ok || len(categories) != 1 {
		t.Fatalf("categories = %#v, want one alice category", snapshot.Primary["categories"])
	}
	category := categories[0].(map[string]any)
	if category["category_id"] != float64(catID) ||
		category["name"] != "Archive" ||
		category["archive_path"] != "/archive/alice" {
		t.Fatalf("category = %#v, want alice category only", category)
	}
	labels, ok := snapshot.Primary["labels"].([]any)
	if !ok || len(labels) != 1 {
		t.Fatalf("labels = %#v, want one alice label", snapshot.Primary["labels"])
	}
	if label := labels[0].(map[string]any)["label"]; label != "Saved Label" {
		t.Fatalf("label = %#v, want Saved Label", label)
	}
}

func TestAndroidSyncItemsCapsFeedRankSnapshotRows(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	rows := make([]db.SnapshotRow, 0, 5100)
	desired := make(map[string]struct{}, 5100)
	for i := 1; i <= 5100; i++ {
		tweetID := fmt.Sprintf("rank_%04d", i)
		if err := srv.db.ExecRaw(`
			INSERT INTO feed_items (tweet_id, source_handle, author_handle, body_text, published_at, fetched_at, sync_seq)
			VALUES (?, 'alice', 'alice', 'body', ?, ?, ?)
		`, tweetID, now, now, i); err != nil {
			t.Fatalf("insert feed item %s: %v", tweetID, err)
		}
		rows = append(rows, db.SnapshotRow{TweetID: tweetID, RankPosition: i, FinalScore: float64(6000 - i)})
		desired[tweetID] = struct{}{}
	}
	if err := srv.db.ReplaceFeedRankSnapshot("alice", rows); err != nil {
		t.Fatalf("replace rank snapshot: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{Tweets: desired})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}
	for _, item := range items {
		if item.ItemKind != "feed_rank" {
			continue
		}
		var rank deltaBundle
		if err := json.Unmarshal(item.PayloadJSON, &rank); err != nil {
			t.Fatalf("decode rank payload: %v", err)
		}
		if got := int(rank.Primary["row_count"].(float64)); got != androidSyncFeedRankMaxRows {
			t.Fatalf("row_count=%d want %d", got, androidSyncFeedRankMaxRows)
		}
		rowsAny := rank.Primary["rows"].([]any)
		if len(rowsAny) != androidSyncFeedRankMaxRows {
			t.Fatalf("rows len=%d want %d", len(rowsAny), androidSyncFeedRankMaxRows)
		}
		return
	}
	t.Fatalf("feed_rank item missing")
}

func TestAndroidSyncItemsAnnotatesVideoComments(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	insertChannel(t, srv, "youtube_UCcreator", "youtube", "Creator Channel")
	insertVideo(t, srv, "vid_comments_contract", "youtube_UCcreator")
	if err := srv.db.ExecRaw(`UPDATE videos SET duration = 3726, metadata_json = '{"view_count":182191,"like_count":9051}' WHERE video_id = 'vid_comments_contract'`); err != nil {
		t.Fatalf("update duration: %v", err)
	}
	for _, row := range []struct {
		id       string
		parentID string
		author   string
		authorID string
		likes    int
	}{
		{id: "root", author: "@Creator", authorID: "UCcreator", likes: 12_345},
		{id: "reply", parentID: "root", author: "@Viewer", authorID: "UCviewer", likes: 10},
	} {
		if err := srv.db.ExecRaw(
			`INSERT INTO video_comments (
				video_id, comment_id, parent_id, author_name, author_id,
				author_thumbnail, text, like_count, published_at, platform, fetched_at
			) VALUES (?, ?, ?, ?, ?, '', 'text', ?, ?, 'youtube', ?)`,
			"vid_comments_contract", row.id, row.parentID, row.author, row.authorID, row.likes, now, now,
		); err != nil {
			t.Fatalf("insert comment %s: %v", row.id, err)
		}
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{
			"vid_comments_contract": {},
		},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}
	var bundle deltaBundle
	for _, item := range items {
		if item.ItemID != "vid_comments_contract" {
			continue
		}
		if err := json.Unmarshal(item.PayloadJSON, &bundle); err != nil {
			t.Fatalf("decode video item payload: %v", err)
		}
		break
	}
	if got := bundle.Primary["duration_label"]; got != "1:02:06" {
		t.Fatalf("duration_label = %#v, want 1:02:06", got)
	}
	metadataJSON, _ := bundle.Primary["metadata_json"].(string)
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode metadata_json: %v", err)
	}
	if metadata["view_count_label"] != "182K" || metadata["like_count_label"] != "9.1K" {
		t.Fatalf("metadata labels = %#v", metadata)
	}
	comments, _ := bundle.Attachments["video_comments"].([]any)
	if len(comments) != 2 {
		t.Fatalf("comments = %#v, want two annotated rows", bundle.Attachments["video_comments"])
	}
	root := comments[0].(map[string]any)
	reply := comments[1].(map[string]any)
	if root["id"] != "root" || root["thread_order"] != float64(1) || root["thread_depth"] != float64(0) || root["parent_order"] != float64(0) || root["reply_to_author"] != "" || root["is_creator"] != true || root["like_count_label"] != "12.3K" {
		t.Fatalf("root comment = %#v", root)
	}
	if reply["id"] != "reply" || reply["thread_order"] != float64(2) || reply["thread_depth"] != float64(1) || reply["parent_order"] != float64(1) || reply["reply_to_author"] != "Creator" || reply["is_creator"] != false || reply["like_count_label"] != "10" {
		t.Fatalf("reply comment = %#v", reply)
	}
}

func TestAndroidSyncItemsCarryUserStateAttachments(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	insertFeedItem(t, srv, "tw_stateful", "state_author", now, 300)
	insertChannel(t, srv, "youtube_stateful", "youtube", "Stateful Channel")
	insertVideo(t, srv, "vid_stateful", "youtube_stateful")
	if err := srv.db.ExecRaw(
		`UPDATE videos SET duration = 120 WHERE video_id = 'vid_stateful'`,
	); err != nil {
		t.Fatalf("update video duration: %v", err)
	}

	if err := srv.db.ExecRaw(
		`INSERT INTO feed_likes (username, tweet_id, liked_at) VALUES ('alice', 'tw_stateful', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert like: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES ('alice', 'tw_stateful', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert seen: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at) VALUES ('alice', 'vid_stateful', 42, ?)`,
		now,
	); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'youtube_stateful', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', 'youtube_stateful', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert star: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO muted_accounts (handle, muted_at) VALUES ('state_author', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert mute: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"tw_stateful": {},
		},
		Videos: map[string]struct{}{
			"vid_stateful": {},
		},
		Channels: map[string]struct{}{
			"youtube_stateful": {},
		},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}

	bundles := map[string]deltaBundle{}
	for _, item := range items {
		var b deltaBundle
		if err := json.Unmarshal(item.PayloadJSON, &b); err != nil {
			t.Fatalf("decode %s/%s: %v", item.ItemKind, item.ItemID, err)
		}
		bundles[item.ItemKind+"/"+item.ItemID] = b
	}

	feedBundle := bundles["feed_items/tw_stateful"]
	for _, key := range []string{"is_liked", "is_seen", "is_bookmarked", "channel_is_followed", "channel_is_starred", "is_author_muted"} {
		if _, ok := feedBundle.Primary[key]; ok {
			t.Fatalf("feed primary should not carry inline %s: %#v", key, feedBundle.Primary)
		}
	}
	if rows := userStateRows(t, feedBundle, "feed_likes"); len(rows) != 1 || rows[0]["liked"] != true {
		t.Fatalf("feed like user_state = %#v", rows)
	}
	if rows := userStateRows(t, feedBundle, "feed_seen"); len(rows) != 1 || rows[0]["seen"] != true {
		t.Fatalf("feed seen user_state = %#v", rows)
	}
	if rows := userStateRows(t, feedBundle, "muted_accounts"); len(rows) != 1 || rows[0]["muted"] != true {
		t.Fatalf("feed mute user_state = %#v", rows)
	}

	videoBundle := bundles["videos/vid_stateful"]
	for _, key := range []string{"is_bookmarked", "channel_is_followed", "channel_is_starred"} {
		if _, ok := videoBundle.Primary[key]; ok {
			t.Fatalf("video primary should not carry inline %s: %#v", key, videoBundle.Primary)
		}
	}
	if rows := userStateRows(t, videoBundle, "bookmarks"); len(rows) != 1 || rows[0]["category_id"] != float64(42) {
		t.Fatalf("video bookmark user_state = %#v", rows)
	}
	if rows := userStateRows(t, videoBundle, "channel_follows"); len(rows) != 1 || rows[0]["followed"] != true {
		t.Fatalf("video follow user_state = %#v", rows)
	}
	if rows := userStateRows(t, videoBundle, "channel_stars"); len(rows) != 1 || rows[0]["starred"] != true {
		t.Fatalf("video star user_state = %#v", rows)
	}

	channelBundle := bundles["channels/youtube_stateful"]
	for _, key := range []string{"channel_is_followed", "channel_is_starred"} {
		if _, ok := channelBundle.Primary[key]; ok {
			t.Fatalf("channel primary should not carry inline %s: %#v", key, channelBundle.Primary)
		}
	}
	if rows := userStateRows(t, channelBundle, "channel_follows"); len(rows) != 1 || rows[0]["followed"] != true {
		t.Fatalf("channel follow user_state = %#v", rows)
	}
	if rows := userStateRows(t, channelBundle, "channel_stars"); len(rows) != 1 || rows[0]["starred"] != true {
		t.Fatalf("channel star user_state = %#v", rows)
	}
}

func TestAndroidSyncItemsCarriesChannelProfileCountLabels(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	insertChannel(t, srv, "twitter_counts", "twitter", "Counts")
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_counts",
		Platform:    "twitter",
		Handle:      "counts",
		DisplayName: "Counts",
		Followers:   76_123,
		Following:   2_203,
	}); err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{},
		Channels: map[string]struct{}{
			"twitter_counts": {},
		},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}
	var bundle deltaBundle
	for _, item := range items {
		if item.ItemKind != "channels" || item.ItemID != "twitter_counts" {
			continue
		}
		if err := json.Unmarshal(item.PayloadJSON, &bundle); err != nil {
			t.Fatalf("decode channel item payload: %v", err)
		}
		break
	}
	if bundle.PrimaryKind != "channels" {
		t.Fatalf("channel item missing from payloads: %+v", items)
	}
	profile, _ := bundle.Attachments["channel_profile"].(map[string]any)
	if profile["followers_label"] != "76.1K" || profile["following_label"] != "2,203" {
		t.Fatalf("profile count labels = %#v", profile)
	}
	if profile["profile_url"] != "https://x.com/counts" {
		t.Fatalf("profile_url = %#v", profile["profile_url"])
	}
}

func TestAndroidSyncItemsCarriesProfileOnlyChannelProfile(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "youtube_UCprofileonly",
		Platform:    "youtube",
		Handle:      "profileonly",
		DisplayName: "Profile Only",
		Followers:   1200,
	}); err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{},
		Channels: map[string]struct{}{
			"youtube_UCprofileonly": {},
		},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}

	var profileBundle deltaBundle
	for _, item := range items {
		if item.ItemKind == "channels" && item.ItemID == "youtube_UCprofileonly" {
			t.Fatalf("profile-only channel should not synthesize a channels primary: %+v", item)
		}
		if item.ItemKind != "channel_profiles" || item.ItemID != "youtube_UCprofileonly" {
			continue
		}
		if err := json.Unmarshal(item.PayloadJSON, &profileBundle); err != nil {
			t.Fatalf("decode profile item payload: %v", err)
		}
	}
	if profileBundle.PrimaryKind != "channel_profiles" {
		t.Fatalf("profile item missing from payloads: %+v", items)
	}
	if profileBundle.Primary["display_name"] != "Profile Only" ||
		profileBundle.Primary["followers_label"] != "1,200" ||
		profileBundle.Primary["profile_url"] != "https://www.youtube.com/@profileonly" {
		t.Fatalf("profile primary = %#v", profileBundle.Primary)
	}
}

func TestAndroidSyncVideoRepostSourcesCarryAuthorLabel(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	insertChannel(t, srv, "tiktok_author", "tiktok", "Author")
	insertChannel(t, srv, "tiktok_reposter", "tiktok", "Reposter")
	insertVideo(t, srv, "reposted_clip", "tiktok_author")
	if err := srv.db.ExecRaw(
		`UPDATE videos SET duration = 45 WHERE video_id = 'reposted_clip'`,
	); err != nil {
		t.Fatalf("update video duration: %v", err)
	}
	if _, err := srv.db.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:             "reposted_clip",
		ReposterChannelID:   "tiktok_reposter",
		ReposterHandle:      "reposter",
		ReposterDisplayName: "Reposter",
		FirstSeenAtMs:       now,
		UpdatedAtMs:         now,
	}}); err != nil {
		t.Fatalf("upsert repost source: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Videos: map[string]struct{}{
			"reposted_clip": {},
		},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}

	for _, item := range items {
		if item.ItemKind != "videos" || item.ItemID != "reposted_clip" {
			continue
		}
		var b deltaBundle
		if err := json.Unmarshal(item.PayloadJSON, &b); err != nil {
			t.Fatalf("decode video payload: %v", err)
		}
		rows, ok := b.Attachments["video_repost_sources"].([]any)
		if !ok || len(rows) != 1 {
			t.Fatalf("video_repost_sources = %#v", b.Attachments["video_repost_sources"])
		}
		row := rows[0].(map[string]any)
		if got := row["repost_author_label"]; got != "Reposter" {
			t.Fatalf("repost_author_label = %#v, want Reposter; row=%#v", got, row)
		}
		return
	}
	t.Fatalf("reposted video item missing from payloads: %+v", items)
}

func TestAndroidSyncInstagramRepostSourcesCarryAuthorLabel(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	insertChannel(t, srv, "instagram_author", "instagram", "Author")
	insertChannel(t, srv, "instagram_reposter", "instagram", "Reposter")
	insertVideo(t, srv, "instagram_reposted_clip", "instagram_author")
	if err := srv.db.ExecRaw(
		`UPDATE videos SET duration = 45 WHERE video_id = 'instagram_reposted_clip'`,
	); err != nil {
		t.Fatalf("update video duration: %v", err)
	}
	if _, err := srv.db.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:             "instagram_reposted_clip",
		ReposterChannelID:   "instagram_reposter",
		ReposterHandle:      "reposter",
		ReposterDisplayName: "Reposter",
		FirstSeenAtMs:       now,
		UpdatedAtMs:         now,
	}}); err != nil {
		t.Fatalf("upsert repost source: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Videos: map[string]struct{}{
			"instagram_reposted_clip": {},
		},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}

	for _, item := range items {
		if item.ItemKind != "videos" || item.ItemID != "instagram_reposted_clip" {
			continue
		}
		var b deltaBundle
		if err := json.Unmarshal(item.PayloadJSON, &b); err != nil {
			t.Fatalf("decode video payload: %v", err)
		}
		rows, ok := b.Attachments["video_repost_sources"].([]any)
		if !ok || len(rows) != 1 {
			t.Fatalf("video_repost_sources = %#v", b.Attachments["video_repost_sources"])
		}
		row := rows[0].(map[string]any)
		if got := row["repost_author_label"]; got != "Reposter" {
			t.Fatalf("repost_author_label = %#v, want Reposter; row=%#v", got, row)
		}
		return
	}
	t.Fatalf("reposted video item missing from payloads: %+v", items)
}

func TestAndroidSyncRetentionSettingsFromRequestOverridesCacheHealth(t *testing.T) {
	fallback := db.AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90, StoryHours: 48}
	req := httptest.NewRequest("GET", "/api/android/sync/generation/latest?feed_days=0&youtube_days=2&moments_days=7&story_hours=24", nil)

	got, err := androidSyncRetentionSettingsFromRequest(req, fallback)
	if err != nil {
		t.Fatalf("retention settings: %v", err)
	}

	if got.FeedDays != 0 || got.YoutubeDays != 2 || got.MomentsDays != 7 || got.StoryHours != 24 {
		t.Fatalf("settings = %+v", got)
	}
}

func TestAndroidSyncRetentionSettingsFromRequestRejectsInvalidRetentionDays(t *testing.T) {
	fallback := db.AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90, StoryHours: 48}
	req := httptest.NewRequest("GET", "/api/android/sync/generation/latest?feed_days=999", nil)

	_, err := androidSyncRetentionSettingsFromRequest(req, fallback)
	if err == nil {
		t.Fatalf("expected error for invalid feed_days")
	}
	if !strings.Contains(err.Error(), "feed_days must be one of") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCacheHealthSettingsPreservesZeroRetention(t *testing.T) {
	got := cacheHealthSettings(map[string]any{
		"retention": map[string]any{
			"feed_days":    0,
			"youtube_days": 0,
			"moments_days": 0,
			"story_hours":  24,
		},
	})

	if got.FeedDays != 0 || got.YoutubeDays != 0 || got.MomentsDays != 0 || got.StoryHours != 24 {
		t.Fatalf("settings = %+v", got)
	}
}

func TestCacheHealthSettingsDefaultsToSevenDayRetention(t *testing.T) {
	got := cacheHealthSettings(nil)

	if got.FeedDays != 7 || got.YoutubeDays != 7 || got.MomentsDays != 7 || got.StoryHours != 48 {
		t.Fatalf("settings = %+v", got)
	}
}

func TestAndroidDashboardRetentionLabel(t *testing.T) {
	retention := db.AndroidRetentionSettings{FeedDays: 7, YoutubeDays: 7, MomentsDays: 1}

	if got := androidDashboardRetentionLabel("moments", retention); got != "1 day" {
		t.Fatalf("moments retention label = %q", got)
	}
	if got := androidDashboardRetentionLabel("feed", retention); got != "7 days" {
		t.Fatalf("feed retention label = %q", got)
	}
	if got := androidDashboardRetentionLabel("avatars", retention); got != "" {
		t.Fatalf("avatars retention label = %q", got)
	}
}

func TestAndroidSyncAssetPriorityDownloadsVisibleImagesBeforeBulkProfiles(t *testing.T) {
	priority := func(kind string, reason ...string) int {
		asset := model.AndroidSyncAsset{AssetKind: kind, State: "ready"}
		if len(reason) > 0 {
			asset.RequiredReason = reason[0]
		}
		return androidSyncAssetPriority(asset)
	}
	checks := []struct {
		firstKind    string
		firstReason  string
		secondKind   string
		secondReason string
	}{
		{"post_thumbnail", "", "banner", "retention"},
		{"banner", "retention", "avatar", "retention"},
		{"avatar", "retention", "post_media", ""},
		{"post_media", "", "post_audio", ""},
		{"post_audio", "", "video_stream", ""},
		{"video_stream", "", "subtitle", ""},
		{"subtitle", "", "dearrow_thumbnail", ""},
		{"dearrow_thumbnail", "", "banner", "profile"},
		{"banner", "profile", "avatar", "profile"},
		{"avatar", "profile", "preview_track_json", ""},
		{"preview_track_json", "", "preview_sprite", ""},
	}
	for _, check := range checks {
		firstPriority := priority(check.firstKind, check.firstReason)
		secondPriority := priority(check.secondKind, check.secondReason)
		if firstPriority >= secondPriority {
			t.Fatalf(
				"%s/%s priority=%d should be before %s/%s priority=%d",
				check.firstKind,
				check.firstReason,
				firstPriority,
				check.secondKind,
				check.secondReason,
				secondPriority,
			)
		}
	}
	if got := androidSyncAssetPriority(model.AndroidSyncAsset{AssetKind: "post_thumbnail", State: "server_missing"}); got != 99 {
		t.Fatalf("server_missing priority = %d, want 99", got)
	}
}

func TestAndroidSyncCanonicalVideoURL(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform, created_at, sync_seq)
		VALUES
		  ('youtube_chan', 'UCabc', 'YouTube Channel', 'youtube', ?, 1),
		  ('tiktok_alice', 'alice', 'Alice', 'tiktok', ?, 2),
		  ('tiktok_missing', '', 'Missing', 'tiktok', ?, 3),
		  ('instagram_chan', 'insta', 'Instagram', 'instagram', ?, 4),
		  ('twitter_alice', 'alice', 'Alice X', 'twitter', ?, 5)
	`, now, now, now, now, now); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name, fetched_at)
		VALUES ('tiktok_alice', 'tiktok', 'alice.profile', 'Alice', ?)
	`, now); err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	checks := []struct {
		name  string
		video model.Video
		want  string
	}{
		{"youtube", model.Video{VideoID: "youtube_abc123", ChannelID: "youtube_chan"}, "https://www.youtube.com/watch?v=abc123"},
		{"tiktok profile handle", model.Video{VideoID: "tiktok_vid123", ChannelID: "tiktok_alice"}, "https://www.tiktok.com/@alice.profile/video/vid123"},
		{"tiktok bare numeric id", model.Video{VideoID: "7634713409828818207", ChannelID: "tiktok_alice"}, "https://www.tiktok.com/@alice.profile/video/7634713409828818207"},
		{"tiktok missing handle omitted", model.Video{VideoID: "tiktok_vid123", ChannelID: "tiktok_missing"}, ""},
		{"instagram reel", model.Video{VideoID: "instagram_reel_REEL123", ChannelID: "instagram_chan"}, "https://www.instagram.com/reel/REEL123/"},
		{"instagram post", model.Video{VideoID: "instagram_post_POST123", ChannelID: "instagram_chan"}, "https://www.instagram.com/p/POST123/"},
		{"twitter", model.Video{VideoID: "twitter_12345", ChannelID: "twitter_alice"}, "https://x.com/alice/status/12345"},
		{"unknown", model.Video{VideoID: "other_1", ChannelID: "other_channel"}, ""},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if got := srv.androidSyncCanonicalVideoURL(check.video); got != check.want {
				t.Fatalf("canonical URL = %q, want %q", got, check.want)
			}
		})
	}
}

func TestAndroidSyncLatestServesFreshGenerationWhileRefreshBuilds(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-a.mp4"), []byte("clip-a"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-a.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "avatars", "youtube_chan.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform, created_at, sync_seq)
		VALUES ('youtube_chan', 'chan', 'Channel', 'youtube', ?, 1)
	`, now); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'youtube_chan', ?)
	`, now); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, thumbnail_path, file_path, file_size, published_at, downloaded_at, sync_seq)
		VALUES ('clip-a', 'youtube_sample_channel', 'Clip A', 12, 'videos/youtube/clip-a.jpg', 'videos/youtube/clip-a.mp4', 6, ?, ?, 2)
	`, now, now); err != nil {
		t.Fatalf("insert first video: %v", err)
	}

	retention := db.AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90, StoryHours: 48}
	first, err := srv.ensureAndroidSyncGeneration("alice", retention)
	if err != nil {
		t.Fatalf("first generation: %v", err)
	}

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-b.mp4"), []byte("clip-b"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-b.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, thumbnail_path, file_path, file_size, published_at, downloaded_at, sync_seq)
		VALUES ('clip-b', 'youtube_sample_channel', 'Clip B', 12, 'videos/youtube/clip-b.jpg', 'videos/youtube/clip-b.mp4', 6, ?, ?, 3)
	`, now+1, now+1); err != nil {
		t.Fatalf("insert second video: %v", err)
	}
	nextSource, err := srv.db.AndroidSyncSourceVersion("alice", retention)
	if err != nil {
		t.Fatalf("next source version: %v", err)
	}

	srv.androidSyncGenerationMu.Lock()
	locked := true
	defer func() {
		if locked {
			srv.androidSyncGenerationMu.Unlock()
		}
	}()

	req := httptest.NewRequest("GET", "/api/android/sync/generation/latest?feed_days=3&youtube_days=2&moments_days=90&story_hours=48", nil)
	req = attachTestAuth(req, "alice")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.mux.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		srv.androidSyncGenerationMu.Unlock()
		locked = false
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatalf("latest generation request blocked behind refresh materialization")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var latest struct {
		Generation model.AndroidSyncGeneration `json:"generation"`
		Refreshing bool                        `json:"refreshing"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&latest); err != nil {
		t.Fatalf("decode latest: %v", err)
	}
	if latest.Generation.GenerationID != first.GenerationID {
		t.Fatalf("served generation = %s, want existing fresh %s", latest.Generation.GenerationID, first.GenerationID)
	}
	if !latest.Refreshing {
		t.Fatalf("refreshing = false, want true while source refresh is queued")
	}

	srv.androidSyncGenerationMu.Unlock()
	locked = false
	deadline := time.Now().Add(3 * time.Second)
	for {
		existing, err := srv.db.GetAndroidSyncGenerationBySource(nextSource)
		if err != nil {
			t.Fatalf("lookup refreshed source: %v", err)
		}
		if existing != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background refresh did not store source %s", nextSource)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAndroidSyncLatestCreatesGenerationDuringFreshSourceDrift(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-a.mp4"), []byte("clip-a"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-a.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "avatars", "youtube_sample_channel.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform, created_at, sync_seq)
		VALUES ('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube', ?, 1)
	`, now); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'youtube_sample_channel', ?)
	`, now); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, thumbnail_path, file_path, file_size, published_at, downloaded_at, sync_seq)
		VALUES ('clip-a', 'youtube_sample_channel', 'Clip A', 12, 'videos/youtube/clip-a.jpg', 'videos/youtube/clip-a.mp4', 6, ?, ?, 2)
	`, now, now); err != nil {
		t.Fatalf("insert first video: %v", err)
	}

	retention := db.AndroidRetentionSettings{FeedDays: 3, YoutubeDays: 2, MomentsDays: 90, StoryHours: 48}
	first, err := srv.ensureAndroidSyncGeneration("alice", retention)
	if err != nil {
		t.Fatalf("first generation: %v", err)
	}
	freshButPastShortDriftWindow := now - int64((45 * time.Minute).Milliseconds())
	if err := srv.db.ExecRaw(`
		UPDATE android_sync_generations
		SET created_at_ms = ?
		WHERE generation_id = ?
	`, freshButPastShortDriftWindow, first.GenerationID); err != nil {
		t.Fatalf("age first generation: %v", err)
	}
	first.CreatedAtMs = freshButPastShortDriftWindow

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-b.mp4"), []byte("clip-b"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "clip-b.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, thumbnail_path, file_path, file_size, published_at, downloaded_at, sync_seq)
		VALUES ('clip-b', 'youtube_sample_channel', 'Clip B', 12, 'videos/youtube/clip-b.jpg', 'videos/youtube/clip-b.mp4', 6, ?, ?, 3)
	`, now+1, now+1); err != nil {
		t.Fatalf("insert second video: %v", err)
	}

	second, err := srv.ensureAndroidSyncGeneration("alice", retention)
	if err != nil {
		t.Fatalf("second generation: %v", err)
	}
	if second.GenerationID == first.GenerationID {
		t.Fatalf("fresh source drift reused generation %s", first.GenerationID)
	}
	if second.AssetCount <= first.AssetCount {
		t.Fatalf("new generation asset count = %d, want more than first %d", second.AssetCount, first.AssetCount)
	}
}

func TestAndroidSyncAssetsCoverFeedQuoteAndThumbnailRows(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "media", "twitter", "alice", "tweet_slide_0.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "media", "twitter", "alice", "tweet_slide_1.mp3"), []byte("audio-only"))
	mustWriteFile(t, filepath.Join(dataDir, "media", "twitter", "bob", "quote_a_0.mp4"), []byte("quote-video"))
	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "generated", "quote_a.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})

	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, author_handle, published_at, fetched_at, content_hash, media_json)
		VALUES ('tweet_slide', 'alice', ?, ?, 'hash-slide', '[{"type":"video"}]')
	`, now-10, now); err != nil {
		t.Fatalf("insert slide tweet: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, author_handle, published_at, fetched_at, content_hash, quote_tweet_id, quote_author_handle, quote_media_json)
		VALUES ('tweet_parent', 'alice', ?, ?, 'hash-parent', 'quote_a', 'bob', '[{"type":"video"}]')
	`, now-5, now); err != nil {
		t.Fatalf("insert quote tweet: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_media_jobs (tweet_id, status, media_kind, updated_at)
		VALUES ('tweet_slide', 'completed', 'video', ?)
	`, now); err != nil {
		t.Fatalf("insert media job: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size)
		VALUES
		  ('feed_media', 'tweet_slide', 0, 'media/twitter/alice/tweet_slide_0.jpg', 'video', 6),
		  ('feed_media', 'tweet_slide', 1, 'media/twitter/alice/tweet_slide_1.mp3', 'video', 10),
		  ('quote_media', 'quote_a', 0, 'media/twitter/bob/quote_a_0.mp4', 'video', 11)
	`); err != nil {
		t.Fatalf("insert media files: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"tweet_slide":  {},
			"tweet_parent": {},
		},
		Videos:   map[string]struct{}{},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	byID := map[string]model.AndroidSyncAsset{}
	for _, asset := range assets {
		byID[asset.AssetID] = asset
	}

	slideID := db.BuildManifestAssetID("twitter", "tweet", "tweet_slide", "post_media", 0)
	if asset, ok := byID[slideID]; !ok {
		t.Fatalf("slide post_media missing: %+v", assets)
	} else if asset.ServerURL != "/api/media/slide/tweet_slide/0" || asset.State != "ready" {
		t.Fatalf("slide post_media = %+v", asset)
	}
	audioID := db.BuildManifestAssetID("twitter", "tweet", "tweet_slide", "post_media", 1)
	if _, ok := byID[audioID]; ok {
		t.Fatalf("audio-only sidecar should not be published as post_media: %+v", byID[audioID])
	}
	slideThumbID := db.BuildManifestAssetID("twitter", "tweet", "tweet_slide", "post_thumbnail", 0)
	if asset, ok := byID[slideThumbID]; !ok || asset.State != "ready" {
		t.Fatalf("slide thumbnail missing/not ready: %+v", asset)
	}

	quoteMediaID := db.BuildManifestAssetID("twitter", "tweet", "quote_a", "post_media", 0)
	if asset, ok := byID[quoteMediaID]; !ok {
		t.Fatalf("quote post_media missing: %+v", assets)
	} else if asset.ServerURL != "/api/media/stream/quote_a" || asset.EffectiveRecencyMs != now-5 || asset.State != "ready" {
		t.Fatalf("quote post_media = %+v", asset)
	}
	quoteThumbID := db.BuildManifestAssetID("twitter", "tweet", "quote_a", "post_thumbnail", 0)
	if asset, ok := byID[quoteThumbID]; !ok || asset.State != "ready" {
		t.Fatalf("quote thumbnail missing/not ready: %+v", asset)
	}
}

func TestAndroidSyncStillMediaPublishesSlidesNotVideoStreams(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "shorts", "tiktok", "still.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "shorts", "tiktok", "still.mp3"), []byte("audio"))
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, thumbnail_path, file_path,
			file_size, media_kind, slide_count, published_at, sync_seq
		) VALUES (
			'still_clip', 'tiktok_chan', 'Still', 0,
			'shorts/tiktok/still.jpg', 'shorts/tiktok/still.jpg',
			6, 'image', 1, ?, 1
		)
	`, now); err != nil {
		t.Fatalf("insert still video: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{
			"still_clip": {},
		},
		MediaVideos: map[string]struct{}{
			"still_clip": {},
		},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	byID := map[string]model.AndroidSyncAsset{}
	for _, asset := range assets {
		byID[asset.AssetID] = asset
		if asset.OwnerID == "still_clip" && asset.AssetKind == "video_stream" {
			t.Fatalf("still media must not publish video_stream: %+v", asset)
		}
	}

	mediaID := db.BuildManifestAssetID("tiktok", "tiktok_video", "still_clip", "post_media", 0)
	if asset, ok := byID[mediaID]; !ok {
		t.Fatalf("still post_media missing: %+v", assets)
	} else if asset.ServerURL != "/api/media/slide/still_clip/0" || asset.ContentType != "image/jpeg" || asset.State != "ready" {
		t.Fatalf("still post_media = %+v", asset)
	}
	audioID := db.BuildManifestAssetID("tiktok", "tiktok_video", "still_clip", "post_audio", 0)
	if asset, ok := byID[audioID]; !ok {
		t.Fatalf("still post_audio missing: %+v", assets)
	} else if asset.ServerURL != "/api/media/audio/still_clip" || asset.State != "ready" {
		t.Fatalf("still post_audio = %+v", asset)
	}

	items, _, err := srv.buildAndroidSyncItems("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{
			"still_clip": {},
		},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build items: %v", err)
	}
	var bundle deltaBundle
	for _, item := range items {
		if item.ItemID != "still_clip" {
			continue
		}
		if err := json.Unmarshal(item.PayloadJSON, &bundle); err != nil {
			t.Fatalf("decode still item payload: %v", err)
		}
		break
	}
	if bundle.Primary == nil {
		t.Fatalf("still_clip item missing from sync payloads: %+v", items)
	}
	if got := bundle.Primary["slide_count"]; got != float64(1) {
		t.Fatalf("slide_count = %#v, want 1", got)
	}
	if got := bundle.Primary["media_mode"]; got != "image" {
		t.Fatalf("media_mode = %#v, want image", got)
	}
}

func TestAndroidSyncAlwaysPublishesVideoThumbnailsOutsideDesiredSets(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "old-thumb.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "old.mp4"), []byte("old-video"))
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, thumbnail_path, file_path,
			file_size, media_kind, published_at, sync_seq
		) VALUES (
			'old_youtube', 'youtube_chan', 'Old', 12,
			'videos/youtube/old-thumb.jpg', 'videos/youtube/old.mp4',
			9, 'video', ?, 1
		)
	`, now-90*24*60*60*1000); err != nil {
		t.Fatalf("insert old video: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets:   map[string]struct{}{},
		Videos:   map[string]struct{}{},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	thumbID := db.BuildManifestAssetID("youtube", "youtube_video", "old_youtube", "post_thumbnail", 0)
	for _, asset := range assets {
		if asset.OwnerID == "old_youtube" && asset.AssetKind == "video_stream" {
			t.Fatalf("old out-of-window video must not publish stream: %+v", asset)
		}
		if asset.AssetID == thumbID {
			if asset.State != "ready" || asset.ServerURL != "/api/media/thumbnail/old_youtube" {
				t.Fatalf("thumbnail asset = %+v", asset)
			}
			return
		}
	}
	t.Fatalf("old YouTube thumbnail missing from always-thumbnail set: %+v", assets)
}

func TestAndroidSyncMetadataVideosPublishNonStreamAssetsWithoutStream(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "old-thumb.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "old.mp4"), []byte("old-video"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "old.en.vtt"), []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nhello\n"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "old.info.json"), []byte(`{"language":"en","automatic_captions":{"en":[{"url":"https://example.test/auto.vtt"}]}}`))
	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "previews", "old_youtube", "sprite.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, thumbnail_path, file_path,
			file_size, media_kind, published_at, sync_seq
		) VALUES (
			'old_youtube', 'youtube_chan', 'Old', 12,
			'videos/youtube/old-thumb.jpg', 'videos/youtube/old.mp4',
			9, 'video', ?, 1
		)
	`, now-90*24*60*60*1000); err != nil {
		t.Fatalf("insert old video: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{
			"old_youtube": {},
		},
		MediaVideos: map[string]struct{}{},
		Channels:    map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	found := map[string]model.AndroidSyncAsset{}
	for _, asset := range assets {
		if asset.OwnerID != "old_youtube" {
			continue
		}
		found[asset.AssetKind] = asset
	}
	if stream, ok := found["video_stream"]; ok {
		t.Fatalf("metadata-only YouTube video must not publish stream: %+v", stream)
	}
	if thumb, ok := found["post_thumbnail"]; !ok || thumb.State != "ready" {
		t.Fatalf("metadata thumbnail missing/not ready: %+v", found)
	}
	if sub, ok := found["subtitle"]; !ok {
		t.Fatalf("metadata subtitle missing: %+v", found)
	} else if sub.State != "ready" || sub.RequiredReason != "metadata" {
		t.Fatalf("metadata subtitle mismatch: %+v", sub)
	} else if sub.IsAuto == nil || !*sub.IsAuto || sub.AudioLanguage != "en" {
		t.Fatalf("metadata subtitle auto/audio metadata mismatch: %+v", sub)
	}
	if track, ok := found["preview_track_json"]; !ok {
		t.Fatalf("metadata preview JSON track missing: %+v", found)
	} else if track.State != "ready" || track.RequiredReason != "metadata" {
		t.Fatalf("metadata preview JSON track mismatch: %+v", track)
	} else if track.ContentType != "application/json" || track.ServerURL != "/api/media/preview-track-json/old_youtube" {
		t.Fatalf("metadata preview JSON contract mismatch: %+v", track)
	}
	if legacy, ok := found["preview_track"]; ok {
		t.Fatalf("legacy VTT preview track must not be published: %+v", legacy)
	}
	if sprite, ok := found["preview_sprite"]; !ok {
		t.Fatalf("metadata preview sprite missing: %+v", found)
	} else if sprite.State != "ready" || sprite.RequiredReason != "metadata" {
		t.Fatalf("metadata preview sprite mismatch: %+v", sprite)
	}
}

func TestAndroidSyncSubtitleAssetPrefersManualTrack(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()
	autoSubtitle := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nauto\n")
	manualSubtitle := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nmanual\n")

	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "manual-thumb.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "manual.mp4"), []byte("video"))
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "manual.en.vtt"), autoSubtitle)
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "manual.tr.vtt"), manualSubtitle)
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "manual.info.json"), []byte(`{"language":"tr","subtitles":{"tr":[{"url":"https://example.test/manual.vtt"}]},"automatic_captions":{"en":[{"url":"https://example.test/auto.vtt"}]}}`))
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, thumbnail_path, file_path,
			file_size, media_kind, published_at, sync_seq
		) VALUES (
			'manual_video', 'youtube_chan', 'Manual', 12,
			'videos/youtube/manual-thumb.jpg', 'videos/youtube/manual.mp4',
			5, 'video', ?, 1
		)
	`, now); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{
			"manual_video": {},
		},
		MediaVideos: map[string]struct{}{
			"manual_video": {},
		},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	for _, asset := range assets {
		if asset.OwnerID != "manual_video" || asset.AssetKind != "subtitle" {
			continue
		}
		if asset.IsAuto == nil || *asset.IsAuto {
			t.Fatalf("subtitle asset should use manual metadata: %+v", asset)
		}
		if asset.SizeBytes != int64(len(manualSubtitle)) {
			t.Fatalf("subtitle size = %d, want manual size %d", asset.SizeBytes, len(manualSubtitle))
		}
		return
	}
	t.Fatalf("subtitle asset missing: %+v", assets)
}

func TestAndroidSyncPublishesDearrowThumbnailAsset(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	dearrowRel := filepath.Join("thumbnails", "dearrow", "dearrow_youtube.jpg")
	dearrowAbs := filepath.Join(dataDir, dearrowRel)
	mustWriteFile(t, dearrowAbs, []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "videos", "youtube", "regular-thumb.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, duration, thumbnail_path, file_path,
			file_size, media_kind, published_at, sync_seq, dearrow_thumb_path
		) VALUES (
			'dearrow_youtube', 'youtube_chan', 'Dearrow', 12,
			'videos/youtube/regular-thumb.jpg', '',
			0, 'video', ?, 1, ?
		)
	`, now, dearrowRel); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{
			"dearrow_youtube": {},
		},
		MediaVideos: map[string]struct{}{},
		Channels:    map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	for _, asset := range assets {
		if asset.OwnerID != "dearrow_youtube" || asset.AssetKind != "dearrow_thumbnail" {
			continue
		}
		if asset.State != "ready" || asset.ContentType != "image/jpeg" || asset.RequiredReason != "metadata" {
			t.Fatalf("dearrow thumbnail asset mismatch: %+v", asset)
		}
		if asset.ServerURL != "/api/media/thumbnail/dearrow_youtube?da=1" {
			t.Fatalf("ServerURL = %q", asset.ServerURL)
		}
		if path := srv.androidSyncAssetPath(asset); path != dearrowAbs {
			t.Fatalf("asset path = %q, want %q", path, dearrowAbs)
		}
		return
	}
	t.Fatalf("dearrow_thumbnail asset missing: %+v", assets)
}

func TestAndroidSyncMaterializesReadyAssetInventoryRows(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	inventoryRel := filepath.Join("media", "twitter", "sample", "sample_tweet_inventory_0.jpg")
	mustWriteFile(t, filepath.Join(dataDir, inventoryRel), []byte("inventory-image"))
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, sync_seq)
		VALUES ('sample_tweet_inventory', 'sample', 'sample', '[{"type":"photo"}]', ?, 1)
	`, now); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	assetID := db.BuildManifestAssetID("twitter", "tweet", "sample_tweet_inventory", "post_media", 0)
	if err := srv.db.UpsertAsset(db.Asset{
		AssetID:        assetID,
		AssetKind:      "post_media",
		OwnerKind:      "tweet",
		OwnerID:        "sample_tweet_inventory",
		MediaIndex:     0,
		FilePath:       inventoryRel,
		ContentType:    "image/jpeg",
		State:          db.AssetStateReady,
		RequiredReason: "retention",
	}, now); err != nil {
		t.Fatalf("upsert inventory asset: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"sample_tweet_inventory": {},
		},
		Videos:      map[string]struct{}{},
		MediaVideos: map[string]struct{}{},
		Channels:    map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	for _, asset := range assets {
		if asset.AssetID != assetID || asset.AssetKind != "post_media" {
			continue
		}
		if asset.ServerURL != "/api/media/slide/sample_tweet_inventory/0" || asset.Bucket != "twitter_media" || asset.OwnerKind != "tweet" {
			t.Fatalf("inventory asset wire shape mismatch: %+v", asset)
		}
		if asset.State != "ready" || asset.SizeBytes != int64(len("inventory-image")) || asset.SHA256 == "" {
			t.Fatalf("inventory asset was not finalized from inventory file: %+v", asset)
		}
		return
	}
	t.Fatalf("inventory post_media asset missing: %+v", assets)
}

func TestAndroidSyncReadyLegacyMediaOverridesStaleMissingInventory(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	legacyRel := filepath.Join("media", "twitter", "sample", "sample_tweet_stale_inventory_0.jpg")
	mustWriteFile(t, filepath.Join(dataDir, legacyRel), []byte("legacy-ready-image"))
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, sync_seq)
		VALUES ('sample_tweet_stale_inventory', 'sample', 'sample', '[{"type":"photo"}]', ?, 1)
	`, now); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_media_jobs (tweet_id, status, media_kind)
		VALUES ('sample_tweet_stale_inventory', 'completed', 'image')
	`); err != nil {
		t.Fatalf("insert media job: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size)
		VALUES ('feed_media', 'sample_tweet_stale_inventory', 0, ?, 'photo', 'https://example.test/legacy.jpg', 18)
	`, legacyRel); err != nil {
		t.Fatalf("insert media file: %v", err)
	}
	assetID := db.BuildManifestAssetID("twitter", "tweet", "sample_tweet_stale_inventory", "post_media", 0)
	if err := srv.db.UpsertAsset(db.Asset{
		AssetID:        assetID,
		AssetKind:      "post_media",
		OwnerKind:      "tweet",
		OwnerID:        "sample_tweet_stale_inventory",
		MediaIndex:     0,
		FilePath:       filepath.Join("media", "twitter", "sample", "missing_inventory_0.jpg"),
		ContentType:    "image/jpeg",
		State:          db.AssetStateServerMissing,
		RequiredReason: "retention",
	}, now-1000); err != nil {
		t.Fatalf("upsert stale inventory asset: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"sample_tweet_stale_inventory": {},
		},
		Videos:      map[string]struct{}{},
		MediaVideos: map[string]struct{}{},
		Channels:    map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	for _, asset := range assets {
		if asset.AssetID != assetID || asset.AssetKind != "post_media" {
			continue
		}
		if asset.State != "ready" || asset.SizeBytes != int64(len("legacy-ready-image")) || asset.SHA256 == "" {
			t.Fatalf("stale missing inventory overrode ready legacy media: %+v", asset)
		}
		return
	}
	t.Fatalf("post_media asset missing: %+v", assets)
}

func TestAndroidSyncDoesNotPublishProfileChannelAssetsOutsideRetentionSets(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "avatars", "tiktok_profile_only.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "banners", "tiktok_profile_only.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	fetchedAt := time.UnixMilli(now)
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_profile_only",
		Platform:    "tiktok",
		Handle:      "profile_only",
		DisplayName: "Profile Only",
		AvatarURL:   "https://example.invalid/avatar.jpg",
		BannerURL:   "igloo:synth-banner:profile-only",
		FetchedAt:   &fetchedAt,
	}); err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets:   map[string]struct{}{},
		Videos:   map[string]struct{}{},
		Channels: map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	for _, asset := range assets {
		if asset.OwnerID != "tiktok_profile_only" {
			continue
		}
		t.Fatalf("profile-only channel outside retention should not publish bulk assets: %+v", asset)
	}
}

func TestAndroidSyncRetainedChannelAssetsKeepVisiblePriority(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	dataDir := srv.cfg.DataDir
	now := time.Now().UnixMilli()

	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "avatars", "tiktok_visible.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	mustWriteFile(t, filepath.Join(dataDir, "thumbnails", "banners", "tiktok_visible.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	fetchedAt := time.UnixMilli(now)
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "tiktok_visible",
		Platform:    "tiktok",
		Handle:      "visible",
		DisplayName: "Visible",
		AvatarURL:   "https://example.invalid/avatar.jpg",
		BannerURL:   "igloo:synth-banner:visible",
		FetchedAt:   &fetchedAt,
	}); err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{},
		Channels: map[string]struct{}{
			"tiktok_visible": {},
		},
	})
	if err != nil {
		t.Fatalf("build assets: %v", err)
	}
	found := map[string]bool{}
	for _, asset := range assets {
		if asset.OwnerID != "tiktok_visible" {
			continue
		}
		if asset.RequiredReason != "retention" {
			t.Fatalf("visible channel asset should not be downgraded to bulk profile priority: %+v", asset)
		}
		found[asset.AssetKind] = true
	}
	if !found["avatar"] || !found["banner"] {
		t.Fatalf("expected visible avatar and banner, found=%v assets=%+v", found, assets)
	}
}

func newAndroidSyncTestServer(t *testing.T) *testServer {
	t.Helper()
	tmp, err := os.CreateTemp("", "igloo-sync-test-*.db")
	if err != nil {
		t.Fatalf("temp db: %v", err)
	}
	dbPath := tmp.Name()
	tmp.Close()
	dataDir := t.TempDir()
	d, err := db.Open(dbPath, dataDir)
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
		os.Remove(dbPath)
	})

	cfg := &config.Config{SecretKey: "test-key", DataDir: dataDir}
	s := &Server{
		db:            d,
		cfg:           cfg,
		store:         sessions.NewCookieStore([]byte("test-key")),
		workers:       worker.NewManager(d, cfg),
		profileFlight: newProfileFlight(),
	}
	s.requestAvatar = s.workers.RequestAvatar
	mux := http.NewServeMux()
	s.registerAndroidSyncAPIRoutes(mux)
	mux.HandleFunc("GET /api/media/thumbnail/{videoID}", s.handleThumbnail)
	mux.HandleFunc("GET /api/media/avatar/{channelID}", s.handleChannelAvatar)
	return &testServer{Server: s, mux: mux}
}

func storeAndroidSyncGenerationForWebTest(t *testing.T, d *db.DB, generationID string, createdAtMs int64) {
	t.Helper()
	storeAndroidSyncGenerationForWebTestWithSource(
		t,
		d,
		generationID,
		createdAtMs,
		generationID+"-source",
		map[string]int{"feed_days": 7, "youtube_days": 7, "moments_days": 7, "story_hours": 48},
	)
}

func storeAndroidSyncGenerationForWebTestWithSource(t *testing.T, d *db.DB, generationID string, createdAtMs int64, sourceVersion string, retention map[string]int) {
	t.Helper()
	err := d.StoreAndroidSyncGeneration(
		model.AndroidSyncGeneration{
			GenerationID:    generationID,
			CreatedAtMs:     createdAtMs,
			Status:          "ready",
			SourceVersion:   sourceVersion,
			Retention:       retention,
			ItemCount:       1,
			AssetCount:      1,
			ReadyAssetCount: 1,
			TotalBytes:      1,
			ContentCounts:   map[string]int{"videos": 1},
			AssetCounts:     map[string]int{"video_stream": 1},
		},
		[]model.AndroidSyncItem{{
			GenerationID: generationID,
			Seq:          1,
			ItemKind:     "videos",
			ItemID:       generationID + "-video",
			PayloadJSON:  json.RawMessage(`{}`),
		}},
		[]model.AndroidSyncAsset{{
			GenerationID:       generationID,
			Seq:                1,
			AssetID:            generationID + "-asset",
			AssetKind:          "video_stream",
			OwnerID:            generationID + "-video",
			OwnerKind:          "video",
			Bucket:             "videos",
			ServerURL:          "/asset",
			ContentType:        "video/mp4",
			SizeBytes:          1,
			SHA256:             "sha",
			State:              "ready",
			RequiredReason:     "retention",
			EffectiveRecencyMs: createdAtMs,
		}},
	)
	if err != nil {
		t.Fatalf("StoreAndroidSyncGeneration %s: %v", generationID, err)
	}
}

func countAndroidSyncGenerationsForWebTest(t *testing.T, d *db.DB, likePattern string) int {
	t.Helper()
	var count int
	if err := d.QueryRow(`
		SELECT COUNT(*)
		FROM android_sync_generations
		WHERE generation_id LIKE ?
	`, likePattern).Scan(&count); err != nil {
		t.Fatalf("count generations like %s: %v", likePattern, err)
	}
	return count
}

func mustWriteFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
