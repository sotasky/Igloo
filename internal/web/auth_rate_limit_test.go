package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthRateLimitBlocksRepeatedAPIAttemptsByIP(t *testing.T) {
	now := time.Unix(100, 0)
	s := &Server{authLimiter: newAuthAttemptLimiter(func() time.Time { return now })}
	handler := s.authRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	}))

	for i := 0; i < authRateLimitMaxFailures; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authRateLimitRequest("/api/auth/login", "198.51.100.10:1234", `{"username":"alice","password":"bad"}`))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRateLimitRequest("/api/auth/login", "198.51.100.10:1234", `{"username":"bob","password":"bad"}`))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing")
	}
}

func TestAuthRateLimitBlocksRepeatedAPIAttemptsByUsername(t *testing.T) {
	now := time.Unix(100, 0)
	s := &Server{authLimiter: newAuthAttemptLimiter(func() time.Time { return now })}
	handler := s.authRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	}))

	for i := 0; i < authRateLimitMaxFailures; i++ {
		rec := httptest.NewRecorder()
		remote := strings.Replace("198.51.100.X:1234", "X", string(rune('1'+i)), 1)
		handler.ServeHTTP(rec, authRateLimitRequest("/api/auth/login", remote, `{"username":"alice","password":"bad"}`))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRateLimitRequest("/api/auth/login", "198.51.100.99:1234", `{"username":"ALICE","password":"bad"}`))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRateLimitSuccessClearsFailures(t *testing.T) {
	now := time.Unix(100, 0)
	s := &Server{authLimiter: newAuthAttemptLimiter(func() time.Time { return now })}
	failHandler := s.authRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	}))
	successHandler := s.authRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))

	for i := 0; i < authRateLimitMaxFailures-1; i++ {
		failHandler.ServeHTTP(httptest.NewRecorder(), authRateLimitRequest("/api/auth/login", "198.51.100.20:1234", `{"username":"alice"}`))
	}
	successRec := httptest.NewRecorder()
	successHandler.ServeHTTP(successRec, authRateLimitRequest("/api/auth/login", "198.51.100.20:1234", `{"username":"alice"}`))
	if successRec.Code != http.StatusOK {
		t.Fatalf("success status = %d", successRec.Code)
	}

	for i := 0; i < authRateLimitMaxFailures-1; i++ {
		rec := httptest.NewRecorder()
		failHandler.ServeHTTP(rec, authRateLimitRequest("/api/auth/login", "198.51.100.20:1234", `{"username":"alice"}`))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("post-success attempt %d status = %d", i+1, rec.Code)
		}
	}
}

func authRateLimitRequest(path, remoteAddr, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")
	return req
}
