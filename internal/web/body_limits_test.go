package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigImportRejectsOversizedBody(t *testing.T) {
	srv := newTestServer(t)
	body, contentType := multipartBody(t, "file", "config.json", []byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPost, "/api/config/import", body)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = configImportMaxBodyBytes + 1
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigImport(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCookieUploadRejectsOversizedBody(t *testing.T) {
	srv := newTestServer(t)
	body, contentType := multipartBody(t, "file", "cookies.txt", []byte("cookie"))
	req := httptest.NewRequest(http.MethodPost, "/api/cookies/twitter", body)
	req.SetPathValue("platform", "twitter")
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = cookieUploadMaxBodyBytes + 1
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleUploadCookie(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAnalyticsEventsRejectsOversizedChunkedBody(t *testing.T) {
	srv := newTestServer(t)
	body := `{"events":[{"event_type":"` + strings.Repeat("x", int(analyticsEventsMaxBodyBytes)+1) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/analytics/events", strings.NewReader(body))
	req.ContentLength = -1
	rec := httptest.NewRecorder()

	srv.handleAnalyticsEvents(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %s", rec.Code, rec.Body.String())
	}
}
