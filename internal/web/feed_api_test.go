package web

import (
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

func TestAndroidSyncFeedItemBuildsCanonicalURLs(t *testing.T) {
	item := androidSyncFeedItemFromModel(model.FeedItem{
		TweetID:           "tw_1",
		SourceHandle:      "source_user",
		AuthorHandle:      "@alice",
		QuoteTweetID:      "quote_1",
		QuoteAuthorHandle: "@bob",
	})

	if item.CanonicalURL != "https://x.com/i/status/tw_1" {
		t.Fatalf("canonical_url = %q", item.CanonicalURL)
	}
	if item.QuoteCanonicalURL != "https://x.com/i/status/quote_1" {
		t.Fatalf("quote_canonical_url = %q", item.QuoteCanonicalURL)
	}
}

func TestAndroidSyncFeedItemPreservesStoredCanonicalURLs(t *testing.T) {
	item := androidSyncFeedItemFromModel(model.FeedItem{
		TweetID:           "tw_1",
		AuthorHandle:      "alice",
		CanonicalURL:      "https://example.invalid/canonical",
		QuoteTweetID:      "quote_1",
		QuoteAuthorHandle: "bob",
		QuoteCanonicalURL: "https://example.invalid/quote",
	})

	if item.CanonicalURL != "https://example.invalid/canonical" {
		t.Fatalf("canonical_url = %q", item.CanonicalURL)
	}
	if item.QuoteCanonicalURL != "https://example.invalid/quote" {
		t.Fatalf("quote_canonical_url = %q", item.QuoteCanonicalURL)
	}
}

func TestAndroidSyncFeedItemBuildsHandleIndependentCanonicalURL(t *testing.T) {
	tweetID := "sample_tweet"
	sourceHandle := "sample_source"
	placeholderAuthor := "unknown"
	item := androidSyncFeedItemFromModel(model.FeedItem{
		TweetID:      tweetID,
		SourceHandle: sourceHandle,
		AuthorHandle: placeholderAuthor,
	})

	want := "https://x.com/i/status/" + tweetID
	if item.CanonicalURL != want {
		t.Fatalf("canonical_url = %q", item.CanonicalURL)
	}
}

func TestAndroidSyncFeedItemIncludesTranslationSourceLabels(t *testing.T) {
	item := androidSyncFeedItemFromModel(model.FeedItem{
		TweetID:          "tw_translated",
		AuthorHandle:     "alice",
		BodyTranslation:  "hello",
		BodySourceLang:   "Korean",
		QuoteTweetID:     "quote_1",
		QuoteTranslation: "quoted hello",
		QuoteSourceLang:  "Japanese",
	})

	if item.BodySourceLang != "Korean" {
		t.Fatalf("body_source_lang = %q, want Korean", item.BodySourceLang)
	}
	if item.QuoteSourceLang != "Japanese" {
		t.Fatalf("quote_source_lang = %q, want Japanese", item.QuoteSourceLang)
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

func TestHandleFeedLikePublishesItsStateOwners(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/feed/like/sample_like_once", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	_ = mutationOwnerRevision(t, srv, "feed_like", "sample_like_once")
	_ = mutationOwnerRevision(t, srv, "feed_seen", "sample_like_once")
	var liked, seen int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE tweet_id = 'sample_like_once'`).Scan(&liked); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_seen WHERE tweet_id = 'sample_like_once'`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if liked != 1 || seen != 1 {
		t.Fatalf("like state = liked %d seen %d", liked, seen)
	}
}

func TestHandleFeedSeenDoesNotInvalidateFeedRanking(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.ExecRaw(`INSERT INTO feed_items
		(tweet_id, source_channel_id, channel_id, body_text, published_at, algo_interest, algo_scored_at)
		VALUES ('tw_seen_ranked', 'twitter_sample_seen', 'twitter_sample_seen', 'body', 1000, 1.0, 12345)`); err != nil {
		t.Fatal(err)
	}

	seenReq := httptest.NewRequest("POST", "/api/feed/seen?tweet_id=tw_seen_ranked", nil)
	seenReq = attachTestAuth(seenReq, "alice")
	seenRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(seenRec, seenReq)
	if seenRec.Code != http.StatusOK {
		t.Fatalf("seen status: got %d - %s", seenRec.Code, seenRec.Body.String())
	}

	var scoredAt int64
	if err := srv.db.QueryRow(`SELECT algo_scored_at FROM feed_items WHERE tweet_id='tw_seen_ranked'`).Scan(&scoredAt); err != nil {
		t.Fatal(err)
	}
	if scoredAt != 12345 {
		t.Fatalf("algo_scored_at after seen = %d, want unchanged 12345", scoredAt)
	}
}
