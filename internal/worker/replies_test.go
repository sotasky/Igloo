package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
)

// fxtMockHandler returns a handler serving canned tweet JSON keyed by
// "/<handle>/status/<id>". Missing entries return 404.
func fxtMockHandler(_ *testing.T, fixtures map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := fixtures[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func tweetFixture(id, author, text, replyToHandle, replyToStatus string) string {
	m := map[string]any{
		"code": 200, "message": "OK",
		"tweet": map[string]any{
			"id":   id,
			"text": text,
			"lang": "en",
			"author": map[string]any{
				"screen_name": author,
				"name":        author,
				"avatar_url":  "https://example/" + author + ".jpg",
			},
			"replying_to":        replyToHandle,
			"replying_to_status": replyToStatus,
			"created_at":         "Mon Apr 21 10:00:00 +0000 2026",
		},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestResolveReplyChainSelfThread(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "1000000000000000001", AuthorHandle: "user_alpha", BodyText: "root", PublishedAt: &now, FetchedAt: now, ContentHash: "h1"},
		{TweetID: "1000000000000000002", AuthorHandle: "user_alpha", BodyText: "self-reply", IsReply: true, ReplyToHandle: "user_alpha", ReplyToStatus: "", PublishedAt: &now, FetchedAt: now, ContentHash: "h2"},
	})

	fixtures := map[string]string{
		"/user_alpha/status/1000000000000000002": tweetFixture("1000000000000000002", "user_alpha", "self-reply", "user_alpha", "1000000000000000001"),
	}
	srv := httptest.NewServer(fxtMockHandler(t, fixtures))
	defer srv.Close()
	fx := &fxtwitter.Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}

	r := NewReplyResolver(d, fx)
	leaf := model.FeedItem{TweetID: "1000000000000000002", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_alpha"}
	if err := r.ResolveCycle(context.Background(), []model.FeedItem{leaf}); err != nil {
		t.Fatalf("ResolveCycle: %v", err)
	}

	got, _ := d.GetFeedItemByTweetID("1000000000000000002")
	if got.ReplyToStatus != "1000000000000000001" {
		t.Errorf("leaf ReplyToStatus: got %q, want 1000000000000000001", got.ReplyToStatus)
	}
	root, _ := d.GetFeedItemByTweetID("1000000000000000001")
	if root.IsGhost {
		t.Error("root should not have been turned into a ghost")
	}
}

func TestResolveReplyChainExternalParent(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "1000000000000000010", AuthorHandle: "user_alpha", BodyText: "external reply", IsReply: true, ReplyToHandle: "user_beta", PublishedAt: &now, FetchedAt: now, ContentHash: "x1"},
	})

	fixtures := map[string]string{
		"/user_alpha/status/1000000000000000010": tweetFixture("1000000000000000010", "user_alpha", "external reply", "user_beta", "1000000000000000000"),
		"/user_beta/status/1000000000000000000":  tweetFixture("1000000000000000000", "user_beta", "user_beta root tweet", "", ""),
	}
	srv := httptest.NewServer(fxtMockHandler(t, fixtures))
	defer srv.Close()
	fx := &fxtwitter.Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}

	r := NewReplyResolver(d, fx)
	leaf := model.FeedItem{TweetID: "1000000000000000010", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_beta"}
	if err := r.ResolveCycle(context.Background(), []model.FeedItem{leaf}); err != nil {
		t.Fatal(err)
	}

	leafRow, _ := d.GetFeedItemByTweetID("1000000000000000010")
	if leafRow.ReplyToStatus != "1000000000000000000" {
		t.Errorf("leaf ReplyToStatus: got %q", leafRow.ReplyToStatus)
	}
	parent, _ := d.GetFeedItemByTweetID("1000000000000000000")
	if parent == nil {
		t.Fatal("parent not stored")
	}
	if !parent.IsGhost {
		t.Error("parent should be is_ghost=1")
	}
	if parent.AuthorHandle != "user_beta" {
		t.Errorf("parent AuthorHandle: got %q", parent.AuthorHandle)
	}
}

func TestResolveReplyChainDeepWalk(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "1000000000000000040", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_xenon", BodyText: "leaf", PublishedAt: &now, FetchedAt: now, ContentHash: "d1"},
	})
	fixtures := map[string]string{
		"/user_alpha/status/1000000000000000040":  tweetFixture("1000000000000000040", "user_alpha", "leaf", "user_xenon", "1000000000000000041"),
		"/user_xenon/status/1000000000000000041":  tweetFixture("1000000000000000041", "user_xenon", "mid1", "user_yankee", "1000000000000000042"),
		"/user_yankee/status/1000000000000000042": tweetFixture("1000000000000000042", "user_yankee", "mid2", "user_zulu", "1000000000000000043"),
		"/user_zulu/status/1000000000000000043":   tweetFixture("1000000000000000043", "user_zulu", "root", "", ""),
	}
	srv := httptest.NewServer(fxtMockHandler(t, fixtures))
	defer srv.Close()
	fx := &fxtwitter.Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}

	r := NewReplyResolver(d, fx)
	leaf := model.FeedItem{TweetID: "1000000000000000040", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_xenon"}
	if err := r.ResolveCycle(context.Background(), []model.FeedItem{leaf}); err != nil {
		t.Fatal(err)
	}

	chain, err := d.GetThreadChain("1000000000000000040")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1000000000000000043", "1000000000000000042", "1000000000000000041", "1000000000000000040"}
	if len(chain) != len(want) {
		t.Fatalf("chain length: got %d, want %d, chain=%+v", len(chain), len(want), chain)
	}
	for i, item := range chain {
		if item.TweetID != want[i] {
			t.Errorf("chain[%d]: got %q, want %q", i, item.TweetID, want[i])
		}
	}
}

func TestResolveReplyChainFxtwitterNotFound(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UTC()
	_, _ = d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "1000000000000000050", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_deleted", BodyText: "orphan", PublishedAt: &now, FetchedAt: now, ContentHash: "n1"},
	})
	fixtures := map[string]string{
		"/user_alpha/status/1000000000000000050": tweetFixture("1000000000000000050", "user_alpha", "orphan", "user_deleted", "1000000000000000099"),
		// Parent fixture missing → 404.
	}
	srv := httptest.NewServer(fxtMockHandler(t, fixtures))
	defer srv.Close()
	fx := &fxtwitter.Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}

	r := NewReplyResolver(d, fx)
	leaf := model.FeedItem{TweetID: "1000000000000000050", AuthorHandle: "user_alpha", IsReply: true, ReplyToHandle: "user_deleted"}
	err := r.ResolveCycle(context.Background(), []model.FeedItem{leaf})
	if err != nil {
		t.Errorf("ResolveCycle should not error on parent 404: %v", err)
	}
	leafRow, _ := d.GetFeedItemByTweetID("1000000000000000050")
	if leafRow.ReplyToStatus != "1000000000000000099" {
		t.Errorf("leaf ReplyToStatus: got %q", leafRow.ReplyToStatus)
	}
	parent, _ := d.GetFeedItemByTweetID("1000000000000000099")
	if parent != nil {
		t.Errorf("404 parent should not be in DB, got %+v", parent)
	}
}
