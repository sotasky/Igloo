package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMutationErrorDoesNotExposeInternalFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	if !writeMutationError(rec, "test mutation", errors.New("private sqlite failure")) {
		t.Fatal("internal failure was not handled")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError || body["error_code"] != "db_error" || body["error_message"] != "database error" {
		t.Fatalf("internal failure response = status %d body %v", rec.Code, body)
	}
}

func TestMutationLikeClockRejectsStaleWork(t *testing.T) {
	srv := newTestServer(t)

	status, _ := mutationRequest(t, srv, "POST", "/api/mutations/like", `{
	  "tweet_id":"sample_like", "action":"set", "updated_at_ms":200
	}`)
	if status != http.StatusOK {
		t.Fatalf("set status = %d", status)
	}
	before := mutationOwnerRevision(t, srv, "feed_like", "sample_like")

	status, _ = mutationRequest(t, srv, "POST", "/api/mutations/like", `{
	  "tweet_id":"sample_like", "action":"set", "updated_at_ms":200
	}`)
	if status != http.StatusOK {
		t.Fatalf("retry status = %d", status)
	}
	if after := mutationOwnerRevision(t, srv, "feed_like", "sample_like"); after != before {
		t.Fatalf("idempotent retry revision = %d, want %d", after, before)
	}

	status, body := mutationRequest(t, srv, "POST", "/api/mutations/like", `{
	  "tweet_id":"sample_like", "action":"clear", "updated_at_ms":100
	}`)
	if status != http.StatusConflict || body["error_code"] != "stale_mutation" {
		t.Fatalf("stale clear = status %d body %v", status, body)
	}
	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE tweet_id = 'sample_like'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stale clear changed like state")
	}

	status, _ = mutationRequest(t, srv, "POST", "/api/mutations/like", `{
	  "tweet_id":"sample_like", "action":"clear", "updated_at_ms":300
	}`)
	if status != http.StatusOK {
		t.Fatalf("clear status = %d", status)
	}
	status, body = mutationRequest(t, srv, "POST", "/api/mutations/like", `{
	  "tweet_id":"sample_like", "action":"set", "updated_at_ms":250
	}`)
	if status != http.StatusConflict || body["error_code"] != "stale_mutation" {
		t.Fatalf("stale set = status %d body %v", status, body)
	}
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE tweet_id = 'sample_like'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale set resurrected cleared like")
	}
}

func TestMutationBookmarkPreservesOmittedMetadata(t *testing.T) {
	srv := newTestServer(t)
	status, _ := mutationRequest(t, srv, "POST", "/api/mutations/bookmark", `{
	  "video_id":"sample_bookmark", "action":"set", "category_id":0,
	  "custom_title":"saved", "account_handles":"[\"sample\"]",
	  "media_indices":"[1]", "updated_at_ms":100
	}`)
	if status != http.StatusOK {
		t.Fatalf("initial set status = %d", status)
	}
	status, _ = mutationRequest(t, srv, "POST", "/api/mutations/bookmark", `{
	  "video_id":"sample_bookmark", "action":"set", "updated_at_ms":200
	}`)
	if status != http.StatusOK {
		t.Fatalf("omitted metadata status = %d", status)
	}
	var title, handles, indices string
	if err := srv.db.QueryRow(`
		SELECT COALESCE(custom_title,''), COALESCE(account_handles,''), COALESCE(media_indices,'')
		FROM bookmarks WHERE video_id = 'sample_bookmark'
	`).Scan(&title, &handles, &indices); err != nil {
		t.Fatal(err)
	}
	if title != "saved" || handles != `["sample"]` || indices != "[1]" {
		t.Fatalf("omitted metadata changed row: %q %q %q", title, handles, indices)
	}

	status, _ = mutationRequest(t, srv, "POST", "/api/mutations/bookmark", `{
	  "video_id":"sample_bookmark", "action":"set", "custom_title":"", "updated_at_ms":300
	}`)
	if status != http.StatusOK {
		t.Fatalf("explicit clear status = %d", status)
	}
	if err := srv.db.QueryRow(`SELECT COALESCE(custom_title,'') FROM bookmarks WHERE video_id = 'sample_bookmark'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "" {
		t.Fatalf("explicit empty title = %q", title)
	}
}

func TestMutationCreateCategoryReceiptIsIdempotent(t *testing.T) {
	srv := newTestServer(t)
	status, first := mutationRequest(t, srv, "POST", "/api/mutations/create_category", `{
	  "name":"Saved", "provisional_id":"-7", "request_id":"request-1", "updated_at_ms":100
	}`)
	if status != http.StatusOK {
		t.Fatalf("create status = %d body %v", status, first)
	}
	categoryID := first["category_id"].(float64)
	before := mutationOwnerRevision(t, srv, "bookmark_category", fmt.Sprintf("%.0f", categoryID))
	status, second := mutationRequest(t, srv, "POST", "/api/mutations/create_category", `{
	  "name":"Different", "provisional_id":"-8", "request_id":"request-1", "updated_at_ms":200
	}`)
	if status != http.StatusOK {
		t.Fatalf("retry status = %d body %v", status, second)
	}
	if second["category_id"] != first["category_id"] || second["provisional_id"] != "-7" {
		t.Fatalf("retry receipt = %v, first = %v", second, first)
	}
	if after := mutationOwnerRevision(t, srv, "bookmark_category", fmt.Sprintf("%.0f", categoryID)); after != before {
		t.Fatalf("receipt retry revision = %d, want %d", after, before)
	}
	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM bookmark_categories`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("category count = %d, want 1", count)
	}
}

func TestMutationSeenUsesConversationOwner(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items(tweet_id, reply_to_status) VALUES ('parent', ''), ('child', 'parent')
	`); err != nil {
		t.Fatal(err)
	}
	status, _ := mutationRequest(t, srv, "POST", "/api/mutations/seen", `{
	  "tweet_ids":["child"], "updated_at_ms":100
	}`)
	if status != http.StatusOK {
		t.Fatalf("seen status = %d", status)
	}
	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_seen WHERE tweet_id IN ('parent','child')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("seen conversation rows = %d, want 2", count)
	}
}

func TestMutationFollowChangesStateWithoutDeletingStoredContent(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform)
		VALUES ('twitter_sample_author', 'sample_author', 'Sample Author', 'twitter');
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, body_text, published_at)
		VALUES ('sample_tweet', 'twitter_sample_author', 'twitter_sample_author', 'body', 1);
	`); err != nil {
		t.Fatal(err)
	}

	status, _ := mutationRequest(t, srv, "POST", "/api/mutations/follow", `{
	  "channel_id":"twitter_sample_author", "action":"set", "updated_at_ms":100
	}`)
	if status != http.StatusOK || !srv.db.IsChannelFollowed("twitter_sample_author") {
		t.Fatalf("follow status = %d followed = %t", status, srv.db.IsChannelFollowed("twitter_sample_author"))
	}
	status, _ = mutationRequest(t, srv, "POST", "/api/mutations/follow", `{
	  "channel_id":"twitter_sample_author", "action":"clear", "updated_at_ms":200
	}`)
	if status != http.StatusOK || srv.db.IsChannelFollowed("twitter_sample_author") {
		t.Fatalf("unfollow status = %d followed = %t", status, srv.db.IsChannelFollowed("twitter_sample_author"))
	}
	var stored int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id = 'sample_tweet'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("stored content after unfollow = %d", stored)
	}
}

func TestMutationRejectsInvalidActionAndSettingField(t *testing.T) {
	srv := newTestServer(t)
	status, body := mutationRequest(t, srv, "POST", "/api/mutations/like", `{
	  "tweet_id":"sample", "action":"toggle", "updated_at_ms":1
	}`)
	if status != http.StatusBadRequest || body["error_code"] != "invalid_body" {
		t.Fatalf("invalid action = status %d body %v", status, body)
	}
	status, body = mutationRequest(t, srv, "PUT", "/api/mutations/channel_setting", `{
	  "channel_id":"youtube_sample", "field":"unknown", "value":1, "updated_at_ms":1
	}`)
	if status != http.StatusBadRequest || body["error_code"] != "invalid_body" {
		t.Fatalf("invalid setting = status %d body %v", status, body)
	}
}

func mutationOwnerRevision(t *testing.T, srv *testServer, ownerKind, ownerID string) int64 {
	t.Helper()
	var revision int64
	if err := srv.db.QueryRow(`
		SELECT revision FROM android_sync_heads WHERE owner_kind = ? AND owner_id = ?
	`, ownerKind, ownerID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func mutationRequest(t *testing.T, srv *testServer, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "sample")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	parsed := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("decode mutation response: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, parsed
}
