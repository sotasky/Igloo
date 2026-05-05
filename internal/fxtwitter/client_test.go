package fxtwitter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchUserSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test_user" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"user":{
			"screen_name":"test_user","id":"1001","name":"Test User",
			"description":"bio","followers":1,"following":2,"tweets":3,
			"media_count":4,"likes":5,
			"avatar_url":"https://pbs.twimg.com/profile_images/x_normal.jpg",
			"banner_url":"https://pbs.twimg.com/profile_banners/1001/1",
			"location":"","website":null,
			"joined":"Tue Jun 02 20:12:29 +0000 2009",
			"protected":false,
			"verification":{"verified":true,"type":"individual"}
		}}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 5 * time.Second}
	u, err := c.FetchUser(context.Background(), "test_user")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if u.ScreenName != "test_user" || u.Followers != 1 || !u.Verified {
		t.Fatalf("bad decode: %+v", u)
	}
	if u.Joined.Year() != 2009 {
		t.Fatalf("joined parse failed: %v", u.Joined)
	}
}

func TestFetchUserEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 5 * time.Second}
	_, err := c.FetchUser(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFetchUserNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 5 * time.Second}
	_, err := c.FetchUser(context.Background(), "x")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("expected transient err, got %v", err)
	}
}

func TestFetchUserMalformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 5 * time.Second}
	_, err := c.FetchUser(context.Background(), "x")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("expected decode err, got %v", err)
	}
}

func TestFetchTweetReply(t *testing.T) {
	const fixture = `{
		"code": 200, "message": "OK",
		"tweet": {
			"id": "1000000000000000001",
			"text": "@user_beta reply text here",
			"lang": "en",
			"author": {
				"screen_name": "user_alpha",
				"name": "User Alpha",
				"avatar_url": "https://pbs.twimg.com/profile_images/111/x.jpg"
			},
			"replying_to": "user_beta",
			"replying_to_status": "1000000000000000000",
			"created_at": "Mon Apr 21 10:00:00 +0000 2026"
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user_alpha/status/1000000000000000001" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}
	tw, err := c.FetchTweet(context.Background(), "user_alpha", "1000000000000000001")
	if err != nil {
		t.Fatalf("FetchTweet: %v", err)
	}
	if tw.ID != "1000000000000000001" {
		t.Errorf("ID: got %q", tw.ID)
	}
	if tw.AuthorHandle != "user_alpha" {
		t.Errorf("AuthorHandle: got %q", tw.AuthorHandle)
	}
	if tw.AuthorDisplayName != "User Alpha" {
		t.Errorf("AuthorDisplayName: got %q", tw.AuthorDisplayName)
	}
	if tw.ReplyToHandle != "user_beta" {
		t.Errorf("ReplyToHandle: got %q", tw.ReplyToHandle)
	}
	if tw.ReplyToStatus != "1000000000000000000" {
		t.Errorf("ReplyToStatus: got %q", tw.ReplyToStatus)
	}
	if tw.Lang != "en" {
		t.Errorf("Lang: got %q", tw.Lang)
	}
	if tw.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestFetchTweetNonReply(t *testing.T) {
	const fixture = `{"code": 200, "message": "OK", "tweet": {
		"id": "2000000000000000001",
		"text": "Standalone tweet",
		"author": {"screen_name": "user_x", "name": "User X", "avatar_url": "https://example/x.jpg"}
	}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}
	tw, err := c.FetchTweet(context.Background(), "user_x", "2000000000000000001")
	if err != nil {
		t.Fatalf("FetchTweet: %v", err)
	}
	if tw.ReplyToHandle != "" || tw.ReplyToStatus != "" {
		t.Errorf("non-reply should have empty reply fields, got %q/%q", tw.ReplyToHandle, tw.ReplyToStatus)
	}
}

func TestFetchTweetWithMedia(t *testing.T) {
	const fixture = `{
		"code": 200, "message": "OK",
		"tweet": {
			"id": "3000000000000000001",
			"text": "look at these photos",
			"author": {"screen_name": "user_alpha", "name": "User Alpha", "avatar_url": "https://example/a.jpg"},
			"media": {
				"all": [
					{"type": "photo", "url": "https://pbs.twimg.com/media/AAAA.jpg?name=orig"},
					{"type": "video", "url": "https://video.twimg.com/v/BBBB.mp4"}
				]
			}
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}
	tw, err := c.FetchTweet(context.Background(), "user_alpha", "3000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if tw.MediaJSON == "" {
		t.Fatal("MediaJSON should be populated")
	}
	if !strings.Contains(tw.MediaJSON, "AAAA.jpg") || !strings.Contains(tw.MediaJSON, "BBBB.mp4") {
		t.Errorf("MediaJSON missing entries: %q", tw.MediaJSON)
	}
	if !strings.Contains(tw.MediaJSON, `"type":"photo"`) || !strings.Contains(tw.MediaJSON, `"type":"video"`) {
		t.Errorf("MediaJSON types wrong: %q", tw.MediaJSON)
	}
}

func TestFetchTweetNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Timeout: 2 * time.Second}
	_, err := c.FetchTweet(context.Background(), "user_x", "9999999999999999999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpgradeBannerURL(t *testing.T) {
	in := "https://pbs.twimg.com/profile_banners/1001/1774145451"
	want := in + "/1500x500"
	if got := UpgradeBannerURL(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := UpgradeBannerURL(""); got != "" {
		t.Fatalf("empty in should yield empty out, got %q", got)
	}
}
