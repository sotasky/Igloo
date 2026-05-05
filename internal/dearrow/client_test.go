package dearrow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_Fetch_PicksHighestVoteNonOriginalTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"titles": [
				{"title": "Original Title", "original": true, "votes": 100},
				{"title": "Community Best", "original": false, "votes": 20},
				{"title": "Community OK", "original": false, "votes": 5}
			],
			"thumbnails": [
				{"timestamp": 42.5, "original": false, "votes": 10},
				{"timestamp": null, "original": true, "votes": 50},
				{"timestamp": 10.0, "original": false, "votes": 3}
			],
			"casualVotes": [
				{"id": "funny", "count": 7, "title": "Ha ha title"}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	got, err := c.Fetch(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Title == nil || *got.Title != "Community Best" {
		t.Errorf("Title = %v, want 'Community Best'", got.Title)
	}
	if got.ThumbTimestamp == nil || *got.ThumbTimestamp != 42.5 {
		t.Errorf("ThumbTimestamp = %v, want 42.5", got.ThumbTimestamp)
	}
	if got.CasualTitle == nil || *got.CasualTitle != "Ha ha title" {
		t.Errorf("CasualTitle = %v, want 'Ha ha title'", got.CasualTitle)
	}
}

func TestClient_Fetch_NoNonOriginalCandidatesMeansNoOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"titles": [
				{"title": "Original Title", "original": true, "votes": 100}
			],
			"thumbnails": [
				{"timestamp": null, "original": true, "votes": 50}
			],
			"casualVotes": []
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	got, err := c.Fetch(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Title != nil {
		t.Errorf("Title = %v, want nil", got.Title)
	}
	if got.ThumbTimestamp != nil {
		t.Errorf("ThumbTimestamp = %v, want nil", got.ThumbTimestamp)
	}
	if got.CasualTitle != nil {
		t.Errorf("CasualTitle = %v, want nil", got.CasualTitle)
	}
}

func TestClient_Fetch_404IsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	got, err := c.Fetch(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("expected nil error for 404, got %v", err)
	}
	if got.Title != nil || got.ThumbTimestamp != nil || got.CasualTitle != nil {
		t.Errorf("expected zero Result for 404, got %+v", got)
	}
}

func TestClient_Fetch_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	_, err := c.Fetch(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "dearrow: http 500") {
		t.Errorf("error = %q, want to contain 'dearrow: http 500'", err.Error())
	}
}

func TestClient_Fetch_CasualEmptyTitleIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"titles": [],
			"thumbnails": [],
			"casualVotes": [
				{"id": "funny", "count": 5, "title": null}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	got, err := c.Fetch(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.CasualTitle != nil {
		t.Errorf("CasualTitle = %v, want nil (null title should be ignored)", got.CasualTitle)
	}
}

func TestClient_Fetch_CasualNegativeCountIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"titles": [],
			"thumbnails": [],
			"casualVotes": [
				{"id": "bad", "count": -3, "title": "Should be ignored"}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	got, err := c.Fetch(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.CasualTitle != nil {
		t.Errorf("CasualTitle = %v, want nil (negative count should be ignored)", got.CasualTitle)
	}
}

func TestClient_Fetch_SendsCorrectVideoID(t *testing.T) {
	var receivedID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedID = r.URL.Query().Get("videoID")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"titles":[],"thumbnails":[],"casualVotes":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	_, err := c.Fetch(context.Background(), "dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if receivedID != "dQw4w9WgXcQ" {
		t.Errorf("received videoID = %q, want %q", receivedID, "dQw4w9WgXcQ")
	}
}

func TestClient_Fetch_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never respond — block until the client disconnects.
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Fetch(ctx, "abc123")
	if err == nil {
		t.Fatal("expected error from context cancellation, got nil")
	}
}

// TestClient_Fetch_NonOriginalWinsRegardlessOfOriginalVoteCount proves that the
// filter is categorical on the original flag — a community candidate wins even
// when the original entry has far more votes. There is no vote competition
// between the two tracks.
func TestClient_Fetch_NonOriginalWinsRegardlessOfOriginalVoteCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"titles": [
				{"title": "Original Better", "original": true,  "votes": 100, "locked": false, "UUID": "a"},
				{"title": "Challenger",       "original": false, "votes": 5,   "locked": false, "UUID": "b"}
			],
			"thumbnails": [
				{"timestamp": null, "original": true,  "votes": 100, "locked": false, "UUID": "t1"},
				{"timestamp": 10.0, "original": false, "votes": 3,   "locked": false, "UUID": "t2"}
			],
			"casualVotes": []
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.HTTP = srv.Client()
	got, err := c.Fetch(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Title == nil || *got.Title != "Challenger" {
		t.Errorf("Title = %v, want 'Challenger'", got.Title)
	}
	if got.ThumbTimestamp == nil || *got.ThumbTimestamp != 10.0 {
		t.Errorf("ThumbTimestamp = %v, want 10.0", got.ThumbTimestamp)
	}
}

// TestClient_Fetch_TrailingSlashBaseURLNormalized guards against double-slash
// URLs when a caller stores BaseURL with a trailing slash.
func TestClient_Fetch_TrailingSlashBaseURLNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"titles":[],"thumbnails":[],"casualVotes":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL + "/") // trailing slash
	c.HTTP = srv.Client()
	_, err := c.Fetch(context.Background(), "x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if receivedPath != "/api/branding" {
		t.Errorf("request path = %q, want %q", receivedPath, "/api/branding")
	}
}
