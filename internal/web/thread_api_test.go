package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestGetThreadHandler(t *testing.T) {
	ts := newTestServer(t)
	now := time.Now().UTC()

	_, err := ts.db.UpsertFeedItems([]model.FeedItem{
		{TweetID: "t_1", AuthorHandle: "user_alpha", BodyText: "root", PublishedAt: &now, FetchedAt: now, ContentHash: "th_1"},
		{TweetID: "t_2", AuthorHandle: "user_beta", BodyText: "mid", IsReply: true, ReplyToHandle: "user_alpha", ReplyToStatus: "t_1", PublishedAt: &now, FetchedAt: now, ContentHash: "th_2"},
		{TweetID: "t_3", AuthorHandle: "user_alpha", BodyText: "leaf", IsReply: true, ReplyToHandle: "user_beta", ReplyToStatus: "t_2", PublishedAt: &now, FetchedAt: now, ContentHash: "th_3"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/thread/t_3", nil)
	req = attachTestAuth(req, "test_user")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Success bool             `json:"success"`
		Thread  []map[string]any `json:"thread"`
		LeafID  string           `json:"leaf_id"`
	}
	if err := decodeInto(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatal("response not successful")
	}
	if len(resp.Thread) != 3 {
		t.Fatalf("thread length: got %d, want 3, body=%s", len(resp.Thread), rr.Body.String())
	}
	want := []string{"t_1", "t_2", "t_3"}
	for i, item := range resp.Thread {
		if got, _ := item["tweet_id"].(string); got != want[i] {
			t.Errorf("thread[%d].tweet_id: got %q, want %q", i, got, want[i])
		}
	}
	if resp.LeafID != "t_3" {
		t.Errorf("leaf_id: got %q, want t_3", resp.LeafID)
	}
}

func TestGetThreadHandlerNotFound(t *testing.T) {
	ts := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/thread/9999999999999999999", nil)
	req = attachTestAuth(req, "test_user")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing tweet should 404, got %d", rr.Code)
	}
}

func TestGetThreadHandlerSingleTweet(t *testing.T) {
	ts := newTestServer(t)
	now := time.Now().UTC()
	_, _ = ts.db.UpsertFeedItems([]model.FeedItem{
		{TweetID: "lonely_1", AuthorHandle: "user_alpha", BodyText: "no replies", PublishedAt: &now, FetchedAt: now, ContentHash: "th_lonely"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/thread/lonely_1", nil)
	req = attachTestAuth(req, "test_user")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Thread []map[string]any `json:"thread"`
	}
	_ = decodeInto(rr.Body.Bytes(), &resp)
	if len(resp.Thread) != 1 {
		t.Errorf("expected single-item thread, got %d", len(resp.Thread))
	}
}
