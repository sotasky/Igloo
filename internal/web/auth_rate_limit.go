package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authRateLimitBodyMaxBytes = 4096
	authRateLimitMaxFailures  = 5
	authRateLimitBaseBackoff  = time.Second
	authRateLimitMaxBackoff   = 5 * time.Minute
	authRateLimitEntryTTL     = 30 * time.Minute
)

type authAttemptLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*authAttemptState
}

type authAttemptState struct {
	failures     int
	blockedUntil time.Time
	lastSeen     time.Time
}

func newAuthAttemptLimiter(now func() time.Time) *authAttemptLimiter {
	if now == nil {
		now = time.Now
	}
	return &authAttemptLimiter{
		now:     now,
		entries: make(map[string]*authAttemptState),
	}
}

func (l *authAttemptLimiter) retryAfter(keys []string) time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.pruneLocked(now)
	var retryAfter time.Duration
	for _, key := range keys {
		entry := l.entries[key]
		if entry == nil {
			continue
		}
		entry.lastSeen = now
		if remaining := entry.blockedUntil.Sub(now); remaining > retryAfter {
			retryAfter = remaining
		}
	}
	return retryAfter
}

func (l *authAttemptLimiter) record(keys []string, success bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if success {
		for _, key := range keys {
			delete(l.entries, key)
		}
		return
	}
	for _, key := range keys {
		entry := l.entries[key]
		if entry == nil {
			entry = &authAttemptState{}
			l.entries[key] = entry
		}
		entry.failures++
		entry.lastSeen = now
		if entry.failures >= authRateLimitMaxFailures {
			entry.blockedUntil = now.Add(authRateLimitBackoff(entry.failures))
		}
	}
}

func (l *authAttemptLimiter) pruneLocked(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.lastSeen) > authRateLimitEntryTTL {
			delete(l.entries, key)
		}
	}
}

func authRateLimitBackoff(failures int) time.Duration {
	if failures < authRateLimitMaxFailures {
		return 0
	}
	exp := failures - authRateLimitMaxFailures
	if exp > 8 {
		exp = 8
	}
	delay := authRateLimitBaseBackoff * time.Duration(1<<exp)
	if delay > authRateLimitMaxBackoff {
		return authRateLimitMaxBackoff
	}
	return delay
}

func (s *Server) authRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authRateLimitedPath(r.Method, r.URL.Path) || s.authLimiter == nil {
			next.ServeHTTP(w, r)
			return
		}

		username, ok := authRateLimitUsername(w, r)
		if !ok {
			return
		}
		keys := authRateLimitKeys(r, username)
		if retryAfter := s.authLimiter.retryAfter(keys); retryAfter > 0 {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			slog.Warn("auth: rate limited",
				"path", r.URL.Path,
				"client", authRateLimitClientIP(r),
				"username_present", username != "",
				"retry_after_seconds", seconds,
			)
			if apiPath(r.URL.Path) {
				writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many authentication attempts")
				return
			}
			http.Error(w, "Too many authentication attempts. Try again later.", http.StatusTooManyRequests)
			return
		}

		rec := &authRateLimitResponseWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		s.authLimiter.record(keys, authRateLimitSuccess(r.URL.Path, rec.statusCode()))
	})
}

func authRateLimitedPath(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/login", "/setup", "/api/auth/login":
		return true
	default:
		return false
	}
}

func authRateLimitSuccess(path string, status int) bool {
	if path == "/api/auth/login" {
		return status >= 200 && status < 300
	}
	return status == http.StatusSeeOther
}

func authRateLimitKeys(r *http.Request, username string) []string {
	keys := []string{"ip:" + authRateLimitClientIP(r)}
	username = strings.ToLower(strings.TrimSpace(username))
	if username != "" {
		keys = append(keys, "user:"+username)
	}
	return keys
}

func authRateLimitClientIP(r *http.Request) string {
	if isLoopbackAddr(r.RemoteAddr) && hasForwardedClientHeaders(r) {
		if ips, ok := forwardedClientIPs(r); ok && len(ips) > 0 {
			return ips[0].String()
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if host == "" {
		return "unknown"
	}
	return host
}

func authRateLimitUsername(w http.ResponseWriter, r *http.Request) (string, bool) {
	body, ok := readAndRestoreAuthRateLimitBody(w, r)
	if !ok {
		return "", false
	}
	switch r.URL.Path {
	case "/api/auth/login":
		var parsed struct {
			Username string `json:"username"`
		}
		_ = json.Unmarshal(body, &parsed)
		return parsed.Username, true
	default:
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return "", true
		}
		return values.Get("username"), true
	}
}

func readAndRestoreAuthRateLimitBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, authRateLimitBodyMaxBytes+1))
	_ = r.Body.Close()
	if err != nil {
		http.Error(w, "could not read request body", http.StatusBadRequest)
		return nil, false
	}
	if len(body) > authRateLimitBodyMaxBytes {
		if apiPath(r.URL.Path) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body too large")
		} else {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		}
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}

type authRateLimitResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *authRateLimitResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *authRateLimitResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (w *authRateLimitResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
