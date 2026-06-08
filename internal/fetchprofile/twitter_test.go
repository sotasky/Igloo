package fetchprofile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/fxtwitter"
)

func TestFetchTwitterSuccess(t *testing.T) {
	data, err := os.ReadFile("testdata/twitter_success.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	fx := &fxtwitter.Client{BaseURL: srv.URL, HTTP: http.DefaultClient, Timeout: 2 * time.Second}

	p, err := fetchTwitterWithClient(context.Background(), "user_alpha", fx)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if p.ChannelID != "twitter_user_alpha" {
		t.Fatalf("channel_id: %q", p.ChannelID)
	}
	if p.Platform != "twitter" {
		t.Fatalf("platform: %q", p.Platform)
	}
	if p.DisplayName == "" || p.Handle == "" {
		t.Fatalf("display name or handle empty: %+v", p)
	}
}

func TestFetchTwitterRejectsMismatchedScreenName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"user":{
			"screen_name":"other_user",
			"name":"Other User",
			"verification":{"verified":false}
		}}`))
	}))
	defer srv.Close()
	fx := &fxtwitter.Client{BaseURL: srv.URL, HTTP: http.DefaultClient, Timeout: 2 * time.Second}

	_, err := fetchTwitterWithClient(context.Background(), "sample_user", fx)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("expected ErrIdentityMismatch, got %v", err)
	}
}

func TestFetchTwitterNotFound(t *testing.T) {
	// fxtwitter returns an empty body for missing handles (its documented
	// behavior). httptest server returning empty body + 200 triggers that.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	fx := &fxtwitter.Client{BaseURL: srv.URL, HTTP: http.DefaultClient, Timeout: 2 * time.Second}

	_, err := fetchTwitterWithClient(context.Background(), "ghost", fx)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}
