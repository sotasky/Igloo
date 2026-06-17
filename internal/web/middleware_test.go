package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
)

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestCSRFRejectsPostWithoutToken(t *testing.T) {
	s := &Server{cfg: &config.Config{SecretKey: "test-key"}}
	handler := s.csrfProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("POST", "/api/test", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCSRFAllowsGet(t *testing.T) {
	s := &Server{cfg: &config.Config{SecretKey: "test-key"}}
	handler := s.csrfProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/channels", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthRefreshBypassesExpiredBearerAndCSRF(t *testing.T) {
	srv := newTestServer(t)
	sessionID, err := srv.db.CreateAuthSession("alice")
	if err != nil {
		t.Fatalf("CreateAuthSession: %v", err)
	}
	tokenID, issuedAtMs, expiresAtMs, err := srv.db.CreateRefreshToken(sessionID, auth.RefreshTokenTTL)
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	refreshToken := auth.SignRefreshToken(srv.cfg.SecretKey, "alice", "admin", nil, sessionID, tokenID, issuedAtMs, expiresAtMs)
	expiredIssuedAtMs := time.Now().Add(-8 * 24 * time.Hour).UnixMilli()
	expiredAccessToken := auth.SignAccessToken(srv.cfg.SecretKey, "alice", "admin", nil, sessionID, expiredIssuedAtMs)

	mux := http.NewServeMux()
	srv.registerAuthAPIRoutes(mux)
	handler := chain(mux, srv.enforceAuth, srv.csrfProtect)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+refreshToken+`"}`))
	req.Header.Set("Authorization", "Bearer "+expiredAccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["access_token"] == "" || body["refresh_token"] == "" {
		t.Fatalf("refresh did not issue a new token pair: %v", body)
	}
}

func TestThemeCSSBypassesAuth(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	srv.registerAdminAPIRoutes(mux)
	handler := chain(mux, srv.enforceAuth, srv.csrfProtect)

	req := httptest.NewRequest(http.MethodGet, "/api/theme.css", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", got)
	}
	if !strings.Contains(rec.Body.String(), "--bg-primary:") {
		t.Fatalf("theme CSS missing bg token: %s", rec.Body.String())
	}
}

func TestThemeJSONRequiresBearerAuth(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	srv.registerAdminAPIRoutes(mux)
	handler := chain(mux, srv.enforceAuth, srv.csrfProtect)

	unauthReq := httptest.NewRequest(http.MethodGet, "/api/theme.json", nil)
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)

	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401, body = %s", unauthRec.Code, unauthRec.Body.String())
	}

	sessionID, err := srv.db.CreateAuthSession("alice")
	if err != nil {
		t.Fatalf("CreateAuthSession: %v", err)
	}
	issuedAtMs := time.Now().UnixMilli()
	token := auth.SignAccessToken(srv.cfg.SecretKey, "alice", "admin", nil, sessionID, issuedAtMs)
	authReq := httptest.NewRequest(http.MethodGet, "/api/theme.json", nil)
	authReq.Header.Set("Authorization", "Bearer "+token)
	authRec := httptest.NewRecorder()

	handler.ServeHTTP(authRec, authReq)

	if authRec.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200, body = %s", authRec.Code, authRec.Body.String())
	}
	if got := authRec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if !strings.Contains(authRec.Body.String(), `"tokens":`) {
		t.Fatalf("theme JSON missing tokens: %s", authRec.Body.String())
	}
}

func TestEnsureCSRFReturnsRandomFailure(t *testing.T) {
	oldReader := csrfRandomReader
	csrfRandomReader = errorReader{err: errors.New("random unavailable")}
	t.Cleanup(func() {
		csrfRandomReader = oldReader
	})

	s := &Server{store: sessions.NewCookieStore([]byte("test-key"))}
	req := httptest.NewRequest(http.MethodGet, "/channels", nil)
	rec := httptest.NewRecorder()
	sess, err := s.store.Get(req, "session")
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	token, err := s.ensureCSRF(sess, rec, req)
	if err == nil {
		t.Fatalf("ensureCSRF succeeded with token %q", token)
	}
	if _, ok := sess.Values["csrf_token"]; ok {
		t.Fatal("csrf token was stored after random source failure")
	}
}
