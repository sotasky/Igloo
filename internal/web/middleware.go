package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/auth"
)

type contextKey string

const userContextKey contextKey = "user"
const csrfTokenKey contextKey = "csrf_token"

var csrfRandomReader io.Reader = rand.Reader

type userInfo struct {
	Username  string
	Role      string
	Platforms []string
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !loggableRequestPath(r.URL.Path) {
			return
		}
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"dur", time.Since(start).Round(time.Microsecond),
		)
	})
}

func loggableRequestPath(path string) bool {
	return !androidSyncAssetBodyPath(path)
}

func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					panic(err)
				}
				slog.Error("panic", "err", err, "stack", string(debug.Stack()))
				if apiPath(r.URL.Path) {
					writeJSONError(w, 500, "internal_error", "Internal Server Error")
					return
				}
				http.Error(w, "Internal Server Error", 500)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func openAuthAPIPath(path string) bool {
	switch path {
	case "/api/auth/login", "/api/auth/refresh", "/api/auth/logout":
		return true
	default:
		return false
	}
}

func openThemeAPIPath(path string) bool {
	switch path {
	case "/api/theme.css":
		return true
	default:
		return false
	}
}

// enforceAuth checks session auth. Skips /login, /logout, /static/.
func (s *Server) enforceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/login" || path == "/setup" || path == "/logout" || openAuthAPIPath(path) || openThemeAPIPath(path) || path == "/api/health" || path == "/api/health/live" ||
			strings.HasPrefix(path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		// Bearer token (Android). Access token is signed per session;
		// middleware rejects revoked sessions on every request so a
		// single UPDATE on auth_sessions kills every paired access
		// token immediately (#16).
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			claims, err := auth.VerifyAccessToken(s.cfg.SecretKey, token)
			if err == nil {
				sess, sErr := s.db.GetAuthSession(claims.SessionID)
				if sErr != nil || sess.Revoked {
					if apiPath(r.URL.Path) {
						writeJSONError(w, 401, "session_revoked", "Session revoked")
						return
					}
					// Non-API path — fall through to session-cookie check below.
				} else {
					go func(id string) { _ = s.db.TouchAuthSession(id) }(claims.SessionID)
					ctx := context.WithValue(r.Context(), userContextKey, &userInfo{
						Username:  claims.Username,
						Role:      claims.Role,
						Platforms: s.effectivePlatforms(claims.Platforms),
					})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			} else if apiPath(r.URL.Path) {
				writeJSONError(w, 401, tokenErrorCode(err), err.Error())
				return
			}
		}

		sess, _ := s.store.Get(r, "session")
		username, _ := sess.Values["auth_user"].(string)
		if username == "" {
			if apiPath(r.URL.Path) {
				writeJSONError(w, 401, "unauthenticated", "Authentication required")
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		role, _ := sess.Values["user_role"].(string)
		if role == "" {
			role = "user"
		}
		platforms, _ := sess.Values["user_platforms"].([]string)
		ctx := context.WithValue(r.Context(), userContextKey, &userInfo{
			Username:  username,
			Role:      role,
			Platforms: s.effectivePlatforms(platforms),
		})

		csrfToken, err := s.ensureCSRF(sess, w, r)
		if err != nil {
			s.writeCSRFUnavailable(w, r, err)
			return
		}
		ctx = context.WithValue(ctx, csrfTokenKey, csrfToken)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}
		// Login/setup POSTs use their own CSRF via form field + session.
		// /api/auth/* token endpoints are JSON credential exchanges, not session
		// cookie mutations, so Android can refresh after an access token expires.
		if r.URL.Path == "/login" || r.URL.Path == "/setup" || openAuthAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Bearer-authed requests skip CSRF (Android API). enforceAuth
		// already verified the access token + session-revoked state, so
		// presence of a valid-looking Bearer header here is sufficient.
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if _, err := auth.VerifyAccessToken(s.cfg.SecretKey, token); err == nil {
				next.ServeHTTP(w, r)
				return
			}
		}
		expected := r.Context().Value(csrfTokenKey)
		if expected == nil {
			if apiPath(r.URL.Path) {
				writeJSONError(w, http.StatusForbidden, "csrf_token_missing", "CSRF token missing")
				return
			}
			http.Error(w, "CSRF token missing", http.StatusForbidden)
			return
		}
		provided := r.Header.Get("X-CSRF-Token")
		if provided == "" {
			provided = r.FormValue("_csrf_token")
		}
		if provided != expected.(string) {
			if apiPath(r.URL.Path) {
				writeJSONError(w, http.StatusForbidden, "csrf_token_invalid", "CSRF token invalid")
				return
			}
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ensureCSRF generates or retrieves the CSRF token for the session.
func (s *Server) ensureCSRF(sess *sessions.Session, w http.ResponseWriter, r *http.Request) (string, error) {
	if token, ok := sess.Values["csrf_token"].(string); ok && token != "" {
		return token, nil
	}
	b := make([]byte, 32)
	if _, err := io.ReadFull(csrfRandomReader, b); err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}
	token := hex.EncodeToString(b)
	sess.Values["csrf_token"] = token
	if err := sess.Save(r, w); err != nil {
		delete(sess.Values, "csrf_token")
		return "", fmt.Errorf("save csrf token session: %w", err)
	}
	return token, nil
}

func (s *Server) mustEnsureCSRF(sess *sessions.Session, w http.ResponseWriter, r *http.Request) string {
	token, err := s.ensureCSRF(sess, w, r)
	if err != nil {
		panic(err)
	}
	return token
}

func (s *Server) writeCSRFUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("csrf token unavailable", "err", err)
	if apiPath(r.URL.Path) {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "Internal Server Error")
		return
	}
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func userFromContext(ctx context.Context) *userInfo {
	if ctx == nil {
		return nil
	}
	if u, ok := ctx.Value(userContextKey).(*userInfo); ok {
		return u
	}
	return nil
}
