package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// seedEnrichmentFixture inserts one followed twitter channel, a feed item
// authored by that channel with a quote from another channel, and a bookmark
// on the primary tweet — the minimum shape needed to exercise every enrichment
// path a feed handler must populate.
func seedEnrichmentFixture(t *testing.T, srv *testServer) {
	t.Helper()

	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "twitter_alice",
		SourceID:     "alice",
		Name:         "Alice",
		URL:          "https://x.com/alice",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel alice: %v", err)
	}

	now := time.Now()
	item := model.FeedItem{
		TweetID:                "tw_enrich_main",
		AuthorHandle:           "alice",
		AuthorDisplayName:      "Alice",
		BodyText:               "hello world",
		CanonicalURL:           "https://x.com/alice/status/tw_enrich_main",
		PublishedAt:            &now,
		QuoteTweetID:           "tw_enrich_quote",
		QuoteAuthorHandle:      "bob",
		QuoteAuthorDisplayName: "Bob",
		QuoteBodyText:          "quoted body",
	}
	if _, err := srv.db.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	if err := srv.db.AddBookmark("tw_enrich_main", 0, "", "", ""); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}
}

// assertEnrichedItem checks every field that the feed enrichment contract
// requires a feed handler to populate. Splitting this out lets every feed
// endpoint share the same contract — if any handler ever skips EnrichFeedItems
// or stops calling ResolveSubscribeURL, every subtest wired to this helper
// will fail loudly with a specific field name.
func assertEnrichedItem(t *testing.T, item map[string]any) {
	t.Helper()

	if got := item["channel_id"]; got != "twitter_alice" {
		t.Errorf("channel_id = %v, want twitter_alice", got)
	}
	if got := item["author_avatar_url"]; got != "/api/media/avatar/twitter_alice" {
		t.Errorf("author_avatar_url = %v, want /api/media/avatar/twitter_alice", got)
	}
	if got := item["avatar_url"]; got != "/api/media/avatar/twitter_alice" {
		t.Errorf("avatar_url = %v, want /api/media/avatar/twitter_alice", got)
	}
	if got := item["channel_is_followed"]; got != true {
		t.Errorf("channel_is_followed = %v, want true", got)
	}
	if got := item["subscribe_url"]; got != "https://x.com/alice" {
		t.Errorf("subscribe_url = %v, want https://x.com/alice", got)
	}
	if got := item["is_bookmarked"]; got != true {
		t.Errorf("is_bookmarked = %v, want true", got)
	}
	if _, ok := item["bookmarked_at"]; !ok {
		t.Errorf("bookmarked_at missing from enriched bookmark")
	}
	if got := item["quote_tweet_id"]; got != "tw_enrich_quote" {
		t.Errorf("quote_tweet_id = %v, want tw_enrich_quote", got)
	}
	if got := item["quote_channel_id"]; got != "twitter_bob" {
		t.Errorf("quote_channel_id = %v, want twitter_bob", got)
	}
	if got := item["quote_author_avatar_url"]; got != "/api/media/avatar/twitter_bob" {
		t.Errorf("quote_author_avatar_url = %v, want /api/media/avatar/twitter_bob", got)
	}
}

func TestFeedXEnrichmentContract(t *testing.T) {
	srv := newTestServer(t)
	seedEnrichmentFixture(t, srv)

	req := httptest.NewRequest("GET", "/api/feed/x", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) == 0 {
		t.Fatalf("no items returned: %s", rr.Body.String())
	}

	var seeded map[string]any
	for _, it := range body.Items {
		if it["tweet_id"] == "tw_enrich_main" {
			seeded = it
			break
		}
	}
	if seeded == nil {
		t.Fatalf("seeded item not in response: %s", rr.Body.String())
	}

	assertEnrichedItem(t, seeded)
}

func TestFeedXExcludesSeenContent(t *testing.T) {
	srv := newTestServer(t)
	user := "alice"
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('twitter_sample_author', 1)`); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		id        string
		hash      string
		published int64
	}{
		{id: "seen_post", hash: "shared_content", published: now},
		{id: "same_content_copy", hash: "shared_content", published: now - 1},
		{id: "fresh_post", hash: "fresh_content", published: now - 2},
	} {
		if err := srv.db.ExecRaw(`INSERT INTO feed_items
			(tweet_id, channel_id, source_channel_id, body_text, content_hash,
			 canonical_tweet_id, published_at, fetched_at, algo_interest, algo_scored_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.id, "twitter_sample_author", "twitter_sample_author", "body "+row.id, row.hash,
			row.id, row.published, now, 20.0, 1); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO feed_seen (tweet_id, seen_at) VALUES (?, ?)`,
		"seen_post", now,
	); err != nil {
		t.Fatalf("insert seen row: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/feed/x?limit=10", nil)
	req = attachTestAuth(req, user)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seenIDs := map[string]bool{}
	for _, item := range body.Items {
		if id, ok := item["tweet_id"].(string); ok {
			seenIDs[id] = true
		}
	}
	if seenIDs["seen_post"] {
		t.Fatalf("seen post returned in /api/feed/x response: %#v", seenIDs)
	}
	if seenIDs["same_content_copy"] {
		t.Fatalf("same-content sibling returned in /api/feed/x response: %#v", seenIDs)
	}
	if !seenIDs["fresh_post"] {
		t.Fatalf("fresh post missing from /api/feed/x response: %#v", seenIDs)
	}
}

func TestChannelFeedEnrichmentContract(t *testing.T) {
	srv := newTestServer(t)
	seedEnrichmentFixture(t, srv)

	req := httptest.NewRequest("GET", "/api/channels/twitter_alice/feed", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) == 0 {
		t.Fatalf("no items returned: %s", rr.Body.String())
	}

	var seeded map[string]any
	for _, it := range body.Items {
		if it["tweet_id"] == "tw_enrich_main" {
			seeded = it
			break
		}
	}
	if seeded == nil {
		t.Fatalf("seeded item not in response: %s", rr.Body.String())
	}

	assertEnrichedItem(t, seeded)
}
