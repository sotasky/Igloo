package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/screwys/igloo/internal/db"
)

func TestHandleFeedDebugItemReturnsTimelineAndVisibility(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, body_text, published_at,
			fetched_at, content_hash, algo_interest, algo_scored_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"tw_debug", "alice", "alice", "debug body", int64(1000),
		int64(2000), "same-body", 4.5, int64(2100),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_sources (
			source_id, platform, source_type, external_id, label, url, enabled,
			last_checked, last_ok, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"twitter_user_alice", "twitter", "user", "alice", "@alice", "https://x.com/alice", 1,
		int64(2400), int64(2500), "", int64(1200), int64(2500),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_item_sources (tweet_id, source_id, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?)`,
		"tw_debug", "twitter_user_alice", int64(1800), int64(2000),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO ingest_state (
			handle, fail_count, next_retry_at, last_success_at, last_attempt_at,
			last_error, last_http_status, avg_latency_ms, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"twitter_alice", 0, float64(0), float64(2300), float64(2300),
		"", nil, float64(33), int64(2350),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ReplaceFeedRankSnapshot("alice", []db.SnapshotRow{{
		TweetID:            "tw_debug",
		RankPosition:       7,
		BaseScore:          6,
		DecayFactor:        0.75,
		FreshnessBonus:     1.25,
		Jitter:             0.2,
		DiversityDemotedBy: 0.5,
		FinalScore:         5.45,
	}}); err != nil {
		t.Fatalf("replace snapshot: %v", err)
	}
	if err := srv.db.ExecRaw(
		`UPDATE feed_rank_snapshot SET computed_at = ? WHERE username = ? AND tweet_id = ?`,
		int64(3000), "alice", "tw_debug",
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?)`,
		"alice", "tw_debug", int64(4000),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES (?, ?, ?)`,
		"", "twitter_alice", int64(1500),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES (?, ?, ?)`,
		"", "twitter_alice", int64(1600),
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/feed/debug/item/tw_debug", nil)
	req = attachTestAuth(req, "alice")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["success"] != true {
		t.Fatalf("success = %v body=%#v", body["success"], body)
	}
	item := nestedMap(t, body, "item")
	if item["tweet_id"] != "tw_debug" {
		t.Fatalf("tweet_id = %v", item["tweet_id"])
	}
	if item["published_at_ms"] != float64(1000) {
		t.Fatalf("published_at_ms = %v", item["published_at_ms"])
	}
	if item["fetched_at_ms"] != float64(2000) {
		t.Fatalf("fetched_at_ms = %v", item["fetched_at_ms"])
	}
	if item["algo_scored_at_ms"] != float64(2100) {
		t.Fatalf("algo_scored_at_ms = %v", item["algo_scored_at_ms"])
	}

	sources, ok := body["sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Fatalf("sources = %#v", body["sources"])
	}
	sourceEntry, ok := sources[0].(map[string]any)
	if !ok {
		t.Fatalf("source entry = %#v", sources[0])
	}
	if sourceEntry["source_id"] != "twitter_user_alice" {
		t.Fatalf("source_id = %v", sourceEntry["source_id"])
	}
	if sourceEntry["first_seen_at_sec"] != float64(1800) {
		t.Fatalf("first_seen_at_sec = %v", sourceEntry["first_seen_at_sec"])
	}
	source := nestedMap(t, sourceEntry, "source")
	if source["last_ok_sec"] != float64(2500) {
		t.Fatalf("source.last_ok_sec = %v", source["last_ok_sec"])
	}

	ingest := nestedMap(t, body, "ingest_state")
	if ingest["handle"] != "twitter_alice" {
		t.Fatalf("ingest handle = %v", ingest["handle"])
	}
	if ingest["last_success_at_sec"] != float64(2300) {
		t.Fatalf("ingest last_success_at_sec = %v", ingest["last_success_at_sec"])
	}

	snapshot := nestedMap(t, body, "rank_snapshot")
	if snapshot["in_snapshot"] != true {
		t.Fatalf("in_snapshot = %v", snapshot["in_snapshot"])
	}
	if snapshot["rank_position"] != float64(7) {
		t.Fatalf("rank_position = %v", snapshot["rank_position"])
	}
	if snapshot["computed_at_ms"] != float64(3000) {
		t.Fatalf("computed_at_ms = %v", snapshot["computed_at_ms"])
	}

	viewer := nestedMap(t, body, "viewer_state")
	if viewer["seen_at_ms"] != float64(4000) {
		t.Fatalf("seen_at_ms = %v", viewer["seen_at_ms"])
	}
	if viewer["author_is_followed"] != true {
		t.Fatalf("author_is_followed = %v", viewer["author_is_followed"])
	}
	if viewer["author_is_starred"] != true {
		t.Fatalf("author_is_starred = %v", viewer["author_is_starred"])
	}

	visibility := nestedMap(t, body, "visibility")
	if visibility["visible_now"] != false {
		t.Fatalf("visible_now = %v", visibility["visible_now"])
	}
	if !stringSliceContains(visibility["absent_reasons"], "seen_exact") {
		t.Fatalf("absent_reasons missing seen_exact: %#v", visibility["absent_reasons"])
	}
}

func TestHandleFeedDebugItemRequiresAuth(t *testing.T) {
	srv := newTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/feed/debug/item/tw_debug", nil)
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func nestedMap(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	nested, ok := body[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v", key, body[key])
	}
	return nested
}

func stringSliceContains(raw any, want string) bool {
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
