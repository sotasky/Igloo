package web

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

var updateContractGoldens = flag.Bool("update-contracts", false, "update web API contract golden files")

func TestAPIContractGoldens(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) any
	}{
		{name: "feed_enriched_item", build: buildFeedEnrichedItemContract},
		{name: "android_sync_page", build: buildAndroidSyncPageContract},
		{name: "mutation_envelopes", build: buildMutationEnvelopeContract},
		{name: "media_serving_states", build: buildMediaServingContract},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGoldenJSON(t, tt.name, tt.build(t))
		})
	}
}

func TestFeedItemEndpointsReturnEnrichedContractShape(t *testing.T) {
	tests := []struct {
		name string
		path string
		seed func(*testing.T, *testServer)
	}{
		{name: "x feed", path: "/api/feed/x?limit=5", seed: seedContractFeedFixture},
		{
			name: "liked feed",
			path: "/api/feed/liked?limit=5",
			seed: func(t *testing.T, srv *testServer) {
				seedContractFeedFixture(t, srv)
				if err := srv.db.InsertFeedLike("sample_tweet_main", map[string]string{}); err != nil {
					t.Fatalf("InsertFeedLike: %v", err)
				}
			},
		},
		{name: "bookmarked feed", path: "/api/feed/bookmarked?limit=5", seed: seedContractFeedFixture},
		{name: "channel feed", path: "/api/channels/twitter_sample_author/feed?limit=5", seed: seedContractFeedFixture},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			tt.seed(t, srv)
			body := requestJSON(t, srv, "GET", tt.path, "alice", nil)
			assertContractEnrichedFeedItem(t, findContractFeedItem(t, body))
		})
	}
}

func buildFeedEnrichedItemContract(t *testing.T) any {
	t.Helper()
	srv := newTestServer(t)
	seedContractFeedFixture(t, srv)
	return map[string]any{
		"items": []any{findContractFeedItem(t, requestJSON(t, srv, "GET", "/api/feed/x?limit=5", "alice", nil))},
	}
}

func seedContractFeedFixture(t *testing.T, srv *testServer) {
	t.Helper()
	published := time.UnixMilli(1_700_000_000_000)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "twitter_sample_author",
		SourceID:     "sample_author",
		Name:         "Contract Author",
		URL:          "https://x.com/sample_author",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel author: %v", err)
	}
	if _, err := srv.db.UpsertFeedItems([]model.FeedItem{{
		TweetID:                "sample_tweet_main",
		SourceHandle:           "sample_source",
		AuthorHandle:           "sample_author",
		AuthorDisplayName:      "Contract Author",
		BodyText:               "contract body",
		CanonicalURL:           "https://x.com/sample_author/status/sample_tweet_main",
		PublishedAt:            &published,
		QuoteTweetID:           "sample_tweet_quote",
		QuoteAuthorHandle:      "sample_quote",
		QuoteAuthorDisplayName: "Contract Quote",
		QuoteBodyText:          "quoted contract body",
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := srv.db.AddBookmark("sample_tweet_main", 0, "Contract Bookmark", "", ""); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}
	if err := srv.db.ExecRaw(
		`UPDATE bookmarks SET bookmarked_at = ? WHERE video_id = 'sample_tweet_main'`,
		int64(1_700_000_000_500),
	); err != nil {
		t.Fatalf("fix bookmark time: %v", err)
	}
}

func findContractFeedItem(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items = %#v, want array", body["items"])
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["tweet_id"] == "sample_tweet_main" {
			return item
		}
	}
	t.Fatalf("sample_tweet_main missing from response: %#v", body)
	return nil
}

func assertContractEnrichedFeedItem(t *testing.T, item map[string]any) {
	t.Helper()
	want := map[string]any{
		"channel_id":              "twitter_sample_author",
		"author_avatar_url":       "/api/media/avatar/twitter_sample_author",
		"avatar_url":              "/api/media/avatar/twitter_sample_author",
		"channel_is_followed":     true,
		"subscribe_url":           "https://x.com/sample_author",
		"is_bookmarked":           true,
		"quote_channel_id":        "twitter_sample_quote",
		"quote_author_avatar_url": "/api/media/avatar/twitter_sample_quote",
	}
	for key, expected := range want {
		if got := item[key]; got != expected {
			t.Fatalf("%s = %#v, want %#v in item %#v", key, got, expected, item)
		}
	}
	if _, ok := item["bookmarked_at"]; !ok {
		t.Fatalf("bookmarked_at missing from enriched item: %#v", item)
	}
}

func buildAndroidSyncPageContract(t *testing.T) any {
	t.Helper()
	srv := newAndroidSyncTestServer(t)
	seedAndroidContractRows(t, srv)
	if err := srv.db.ExecRaw(`
		UPDATE feed_rank_snapshot SET computed_at = 1700000002000;
		INSERT INTO bookmarks (video_id, category_id, bookmarked_at) VALUES
			('sample_tweet_sync', 0, 1700000002500),
			('tiktok_sample_video_sync', 0, 1700000002500);
	`); err != nil {
		t.Fatalf("seed protected state: %v", err)
	}
	body := requestJSON(t, srv, "GET", "/api/android/sync/bootstrap?feed_days=0&youtube_days=0&moments_days=0&story_hours=0", "alice", nil)
	body["next_cursor"] = "<cursor>"
	return body
}

func seedAndroidContractRows(t *testing.T, srv *testServer) {
	t.Helper()
	now := int64(1_700_000_001_000)
	fetchedAt := time.UnixMilli(now)
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform)
		VALUES
			('twitter_sample_channel', 'sample_channel', 'Contract Channel', 'https://x.com/sample_channel', 'twitter'),
			('tiktok_sample_channel', 'sample_channel', 'Contract TikTok Channel', 'https://www.tiktok.com/@sample_channel', 'tiktok');
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('twitter_sample_channel', ?), ('tiktok_sample_channel', ?)
	`, now, now); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	for _, profile := range []model.ChannelProfile{
		{ChannelID: "twitter_sample_channel", Platform: "twitter", Handle: "sample_channel", DisplayName: "Contract Channel Profile", Followers: 1200, FetchedAt: &fetchedAt},
		{ChannelID: "youtube_sample_profile", Platform: "youtube", Handle: "sample_profile", DisplayName: "Contract Profile Only", Followers: 42, FetchedAt: &fetchedAt},
		{ChannelID: "tiktok_sample_channel", Platform: "tiktok", Handle: "sample_channel", DisplayName: "Contract TikTok Profile", FetchedAt: &fetchedAt},
	} {
		if err := srv.db.UpsertChannelProfile(profile); err != nil {
			t.Fatalf("upsert channel profile: %v", err)
		}
	}
	storeReadyProfileAsset(t, srv, "twitter_sample_channel", "avatar", []byte("contract-avatar"), "image/jpeg")

	published := time.UnixMilli(now)
	if _, err := srv.db.UpsertFeedItems([]model.FeedItem{{
		TweetID: "sample_tweet_sync", SourceHandle: "sample_source", AuthorHandle: "sample_channel",
		AuthorDisplayName: "Inline Stale Name", BodyText: "android sync contract body",
		CanonicalURL: "https://x.com/sample_channel/status/sample_tweet_sync", PublishedAt: &published,
	}}); err != nil {
		t.Fatalf("upsert feed item: %v", err)
	}
	if err := srv.db.ReplaceFeedRankSnapshot([]db.SnapshotRow{{
		TweetID: "sample_tweet_sync", RankPosition: 1, FinalScore: 10,
	}}); err != nil {
		t.Fatalf("replace feed rank snapshot: %v", err)
	}
	if err := srv.db.InsertVideo(
		"tiktok_sample_video_sync", "tiktok_sample_channel", "tiktok_video",
		"Contract Video", "video contract body", 90, now, "", "video", 0, false,
	); err != nil {
		t.Fatalf("insert contract video: %v", err)
	}
}

func buildMutationEnvelopeContract(t *testing.T) any {
	t.Helper()
	srv := newTestServer(t)
	success := requestJSON(t, srv, "POST", "/api/mutations/like", "alice", map[string]any{
		"tweet_id": "sample_tweet_like", "action": "set", "updated_at_ms": 1_700_000_003_000,
	})
	stale := requestJSON(t, srv, "POST", "/api/mutations/like", "alice", map[string]any{
		"tweet_id": "sample_tweet_like", "action": "clear", "updated_at_ms": 1_700_000_002_000,
	})
	invalid := requestJSON(t, srv, "POST", "/api/mutations/like", "alice", map[string]any{
		"tweet_id": "sample_tweet_like", "action": "toggle", "updated_at_ms": 1_700_000_003_001,
	})
	return map[string]any{"success": success, "stale": stale, "invalid": invalid}
}

func buildMediaServingContract(t *testing.T) any {
	t.Helper()
	srv := newTestServer(t)
	storeReadyProfileAsset(t, srv, "twitter_sample_media", "avatar", []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43}, "image/jpeg")
	return map[string]any{
		"ready":   requestSummary(t, srv, "GET", "/api/media/avatar/twitter_sample_media", ""),
		"missing": requestSummary(t, srv, "GET", "/api/media/avatar/twitter_sample_missing", ""),
	}
}

func requestJSON(t *testing.T, srv *testServer, method, path, user string, body any) map[string]any {
	t.Helper()
	rec := requestRecorder(t, srv, method, path, user, body)
	if rec.Code < 200 || rec.Code >= 500 {
		t.Fatalf("%s %s status = %d body=%s", method, path, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s: %v body=%s", method, path, err, rec.Body.String())
	}
	delete(out, "server_time_ms")
	return out
}

func requestSummary(t *testing.T, srv *testServer, method, path, user string) map[string]any {
	t.Helper()
	rec := requestRecorder(t, srv, method, path, user, nil)
	return map[string]any{
		"status": rec.Code, "content_type": rec.Header().Get("Content-Type"),
		"cache_control": rec.Header().Get("Cache-Control"), "body_bytes": rec.Body.Len(),
	}
}

func requestRecorder(t *testing.T, srv *testServer, method, path, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req = attachTestAuth(req, user)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	return rec
}

func assertGoldenJSON(t *testing.T, name string, got any) {
	t.Helper()
	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden %s: %v", name, err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("testdata", "contracts", name+".golden.json")
	if *updateContractGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create contract golden dir: %v", err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("write contract golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract golden %s: %v", name, err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatalf("contract golden %s drifted\nRun: go test ./internal/web -run TestAPIContractGoldens/%s -update-contracts -count=1\n\nGot:\n%s", name, name, raw)
	}
}
