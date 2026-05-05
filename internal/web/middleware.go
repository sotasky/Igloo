package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

type userInfo struct {
	Username  string
	Role      string
	Platforms []string
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"dur", time.Since(start).Round(time.Microsecond),
		)
	})
}

func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
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

// enforceAuth checks session auth. Skips /login, /logout, /static/.
func (s *Server) enforceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/login" || path == "/setup" || path == "/logout" || path == "/api/auth/login" || path == "/api/health" ||
			strings.HasPrefix(path, "/static/") ||
			strings.HasPrefix(path, "/api/logs/android/room-query") {
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

		csrfToken := s.ensureCSRF(sess, w, r)
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
		// /api/auth/login is the Android token-auth endpoint (JSON, no session).
		if r.URL.Path == "/login" || r.URL.Path == "/setup" || r.URL.Path == "/api/auth/login" ||
			strings.HasPrefix(r.URL.Path, "/api/logs/android/room-query") {
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
				writeJSONError(w, 403, "csrf_token_missing", "CSRF token missing")
				return
			}
			http.Error(w, "CSRF token missing", 403)
			return
		}
		provided := r.Header.Get("X-CSRF-Token")
		if provided == "" {
			provided = r.FormValue("_csrf_token")
		}
		if provided != expected.(string) {
			if apiPath(r.URL.Path) {
				writeJSONError(w, 403, "csrf_token_invalid", "CSRF token invalid")
				return
			}
			http.Error(w, "CSRF token invalid", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ensureCSRF generates or retrieves the CSRF token for the session.
func (s *Server) ensureCSRF(sess *sessions.Session, w http.ResponseWriter, r *http.Request) string {
	if token, ok := sess.Values["csrf_token"].(string); ok && token != "" {
		return token
	}
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	sess.Values["csrf_token"] = token
	sess.Save(r, w)
	return token
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
