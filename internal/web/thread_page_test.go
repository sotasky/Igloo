package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestHandlePageThreadRendersSelectedReplyBranch(t *testing.T) {
	ts := newTestServer(t)
	rootAt := time.Unix(100, 0).UTC()
	leafAt := time.Unix(110, 0).UTC()
	siblingAt := time.Unix(120, 0).UTC()
	if _, err := ts.db.UpsertFeedItems([]model.FeedItem{
		{TweetID: "sample_root", AuthorHandle: "sample_root_author", BodyText: "root body", PublishedAt: &rootAt, FetchedAt: rootAt, ContentHash: "sample_thread_root"},
		{TweetID: "sample_leaf", AuthorHandle: "sample_author", BodyText: "leaf body", IsReply: true, ReplyToHandle: "sample_root_author", ReplyToStatus: "sample_root", PublishedAt: &leafAt, FetchedAt: leafAt, ContentHash: "sample_thread_leaf"},
		{TweetID: "sample_sibling", AuthorHandle: "sample_author_b", BodyText: "sibling body", IsReply: true, ReplyToHandle: "sample_root_author", ReplyToStatus: "sample_root", PublishedAt: &siblingAt, FetchedAt: siblingAt, ContentHash: "sample_thread_sibling"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/sample_leaf", nil)
	req = attachTestAuth(req, "test_user")
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`root body`, `leaf body`, `data-thread-back-link`, `href="/feed"`, `data-thread-reply`, `data-thread-depth="1"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in body: %s", want, body)
		}
	}
	if strings.Contains(body, `sibling body`) {
		t.Fatalf("thread route should exclude separate reply branches: %s", body)
	}
}

func TestHandlePageThreadRendersPartialThreadRoute(t *testing.T) {
	ts := newTestServer(t)
	now := time.Now().UTC()
	if _, err := ts.db.UpsertFeedItems([]model.FeedItem{
		{TweetID: "sample_root", AuthorHandle: "sample_root_author", BodyText: "root body", PublishedAt: &now, FetchedAt: now, ContentHash: "sample_thread_root"},
		{TweetID: "sample_leaf", AuthorHandle: "sample_author", BodyText: "leaf body", IsReply: true, ReplyToHandle: "sample_root_author", ReplyToStatus: "sample_root", PublishedAt: &now, FetchedAt: now, ContentHash: "sample_thread_leaf"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/sample_leaf?fmt=partial", nil)
	req = attachTestAuth(req, "test_user")
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`root body`, `leaf body`, `data-thread-route`, `id="thread-feed-list"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in body: %s", want, body)
		}
	}
	if strings.Contains(body, `<html`) || strings.Contains(body, `id="feed-list"`) {
		t.Fatalf("partial rendered full page or duplicate feed-list: %s", body)
	}
}

func TestHandlePageThreadReturnsNotFoundForMissingTweet(t *testing.T) {
	ts := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/thread/missing_tweet", nil)
	req = attachTestAuth(req, "test_user")
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
