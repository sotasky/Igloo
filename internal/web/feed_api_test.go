package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestHandleFeedMute_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)

	for _, method := range []string{"POST", "DELETE"} {
		req := httptest.NewRequest(method, "/api/feed/mute/alice", nil)
		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", method, rr.Code)
		}
	}
}

func TestHandleFeedMute_AuthedSucceeds(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/feed/mute/alice", nil)
	req = attachTestAuth(req, "bob")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("post authed: got %d — %s", rr.Code, rr.Body.String())
	}
}

func TestFeedRankedEndpointRemovedForAndroidSyncContract(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/feed/ranked", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET /api/feed/ranked status = %d, want 404", rr.Code)
	}
}

func TestFeedItemPrimaryBuildsCanonicalURLs(t *testing.T) {
	primary := feedItemToBundlePrimary(model.FeedItem{
		TweetID:           "tw_1",
		SourceHandle:      "source_user",
		AuthorHandle:      "@alice",
		QuoteTweetID:      "quote_1",
		QuoteAuthorHandle: "@bob",
	}, nil, nil, nil)

	if got := primary["canonical_url"]; got != "https://x.com/alice/status/tw_1" {
		t.Fatalf("canonical_url = %#v", got)
	}
	if got := primary["link"]; got != "https://x.com/alice/status/tw_1" {
		t.Fatalf("link = %#v", got)
	}
	if got := primary["canonical_x_link"]; got != "https://x.com/alice/status/tw_1" {
		t.Fatalf("canonical_x_link = %#v", got)
	}
	if got := primary["quote_canonical_url"]; got != "https://x.com/bob/status/quote_1" {
		t.Fatalf("quote_canonical_url = %#v", got)
	}
}

func TestFeedItemPrimaryPreservesStoredCanonicalURLs(t *testing.T) {
	primary := feedItemToBundlePrimary(model.FeedItem{
		TweetID:           "tw_1",
		AuthorHandle:      "alice",
		CanonicalURL:      "https://example.invalid/canonical",
		QuoteTweetID:      "quote_1",
		QuoteAuthorHandle: "bob",
		QuoteCanonicalURL: "https://example.invalid/quote",
	}, nil, nil, nil)

	if got := primary["canonical_url"]; got != "https://example.invalid/canonical" {
		t.Fatalf("canonical_url = %#v", got)
	}
	if got := primary["quote_canonical_url"]; got != "https://example.invalid/quote" {
		t.Fatalf("quote_canonical_url = %#v", got)
	}
}

func TestHandleFeedBookmarked_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/feed/bookmarked", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rr.Code)
	}
}

func TestHandleFeedInteraction_BookmarkActionRejected(t *testing.T) {
	srv := newTestServer(t)

	body := strings.NewReader(`{"action":"bookmark","tweet_id":"x"}`)
	req := httptest.NewRequest("POST", "/api/feed/interaction", body)
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bookmark via interaction: got %d, want 400", rr.Code)
	}
}

func TestHandleFeedLike_EmitsLikeAndSeenSyncChanges(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/feed/like/tweet_like_once", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	var likeRows, seenRows, seenState int
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM sync_changes WHERE type = 'like' AND item_id = ?`,
		"tweet_like_once",
	).Scan(&likeRows); err != nil {
		t.Fatal(err)
	}
	if likeRows != 1 {
		t.Errorf("like sync_changes rows after like: got %d, want 1", likeRows)
	}
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM sync_changes WHERE type = 'seen' AND item_id = ?`,
		"tweet_like_once",
	).Scan(&seenRows); err != nil {
		t.Fatal(err)
	}
	if seenRows != 1 {
		t.Errorf("seen sync_changes rows after like: got %d, want 1", seenRows)
	}
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM feed_seen WHERE username = 'alice' AND tweet_id = ?`,
		"tweet_like_once",
	).Scan(&seenState); err != nil {
		t.Fatal(err)
	}
	if seenState != 1 {
		t.Errorf("feed_seen rows after like: got %d, want 1", seenState)
	}
}

func TestHandleFeedSeenAndMuteReturnSyncVersion(t *testing.T) {
	srv := newTestServer(t)

	seenReq := httptest.NewRequest("POST", "/api/feed/seen", strings.NewReader(`{"tweet_ids":["tw_seen_web"]}`))
	seenReq.Header.Set("Content-Type", "application/json")
	seenReq = attachTestAuth(seenReq, "alice")
	seenRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(seenRec, seenReq)
	if seenRec.Code != http.StatusOK {
		t.Fatalf("seen status: got %d - %s", seenRec.Code, seenRec.Body.String())
	}
	var seenBody map[string]any
	if err := json.Unmarshal(seenRec.Body.Bytes(), &seenBody); err != nil {
		t.Fatal(err)
	}
	if _, ok := seenBody["sync_version"].(float64); !ok {
		t.Fatalf("seen response missing sync_version: %v", seenBody)
	}

	muteReq := httptest.NewRequest("POST", "/api/feed/mute/handle_web", nil)
	muteReq = attachTestAuth(muteReq, "alice")
	muteRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(muteRec, muteReq)
	if muteRec.Code != http.StatusOK {
		t.Fatalf("mute status: got %d - %s", muteRec.Code, muteRec.Body.String())
	}
	var muteBody map[string]any
	if err := json.Unmarshal(muteRec.Body.Bytes(), &muteBody); err != nil {
		t.Fatal(err)
	}
	if _, ok := muteBody["sync_version"].(float64); !ok {
		t.Fatalf("mute response missing sync_version: %v", muteBody)
	}
}
