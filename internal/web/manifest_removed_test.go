package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyAndroidOnlyRoutesAreNotRegistered(t *testing.T) {
	srv := newTestServer(t)

	for _, path := range []string{
		"/api/media/manifest",
		"/api/media/manifest/health",
		"/api/feed/sync",
		"/api/sync/deleted",
	} {
		req := httptest.NewRequest("GET", path, nil)
		req = attachTestAuth(req, "alice")
		rec := httptest.NewRecorder()

		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}
}
