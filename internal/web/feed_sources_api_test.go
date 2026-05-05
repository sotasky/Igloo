package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeedSourceAPIAddListAndListSources(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/feed/sources", strings.NewReader(`{"url":"https://x.com/i/lists/12345"}`))
	req = attachTestAuth(req, "tester")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status = %d body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/feed/sources?platform=twitter", nil)
	req = attachTestAuth(req, "tester")
	rr = httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d body = %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Sources []struct {
			SourceID   string `json:"source_id"`
			SourceType string `json:"source_type"`
			URL        string `json:"url"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sources) != 1 || body.Sources[0].SourceID != "twitter_list_12345" || body.Sources[0].SourceType != "list" {
		t.Fatalf("sources = %#v", body.Sources)
	}
}

func TestFeedSourceAPIDeleteSource(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/feed/sources", strings.NewReader(`{"url":"https://x.com/i/communities/98765"}`))
	req = attachTestAuth(req, "tester")
	srv.mux.ServeHTTP(httptest.NewRecorder(), req)

	req = httptest.NewRequest("DELETE", "/api/feed/sources/twitter_community_98765", nil)
	req = attachTestAuth(req, "tester")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body = %s", rr.Code, rr.Body.String())
	}
}
