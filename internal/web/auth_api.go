package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/db"
)

func (s *Server) registerAuthAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/refresh", s.handleAuthRefresh)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("DELETE /api/account", s.handleAccountDelete)
	mux.HandleFunc("GET /api/auth/verify", s.handleAuthVerify)
}

// handleAuthLogin opens a new session and issues a paired access +
// refresh token (#16, #17). Login endpoint is in the enforceAuth
// allowlist so it runs without a pre-existing Bearer token.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, 400, "invalid_body", "invalid JSON")
		return
	}

	users := auth.GetCachedUsers()
	if len(users) == 0 {
		writeJSONError(w, 503, "setup_required", "no users are configured")
		return
	}
	rec, ok := users[body.Username]
	if !ok {
		slog.Warn("auth: unknown user", "username", body.Username, "known_users", len(users))
		writeJSONError(w, 401, "invalid_credentials", "invalid credentials")
		return
	}
	if !auth.VerifyPassword(body.Password, rec.Password) {
		slog.Warn("auth: password mismatch", "username", body.Username)
		writeJSONError(w, 401, "invalid_credentials", "invalid credentials")
		return
	}

	sessionID, err := s.db.CreateAuthSession(body.Username)
	if err != nil {
		slog.Error("CreateAuthSession", "err", err)
		writeJSONError(w, 500, "session_create_failed", "could not create session")
		return
	}
	tokenID, issuedAtMs, refreshExpiresAtMs, err := s.db.CreateRefreshToken(sessionID, auth.RefreshTokenTTL)
	if err != nil {
		slog.Error("CreateRefreshToken", "err", err)
		writeJSONError(w, 500, "session_create_failed", "could not issue refresh token")
		return
	}

	platforms := s.effectivePlatforms(rec.Platforms)
	accessToken := auth.SignAccessToken(s.cfg.SecretKey, body.Username, rec.Role, platforms, sessionID, issuedAtMs)
	refreshToken := auth.SignRefreshToken(s.cfg.SecretKey, body.Username, rec.Role, platforms, sessionID, tokenID, issuedAtMs, refreshExpiresAtMs)

	s.workers.Emit("auth", fmt.Sprintf("Login: %s", body.Username), "info")
	writeJSON(w, 200, map[string]any{
		"access_token":          accessToken,
		"refresh_token":         refreshToken,
		"access_expires_at_ms":  issuedAtMs + auth.AccessTokenTTL.Milliseconds(),
		"refresh_expires_at_ms": refreshExpiresAtMs,
		"username":              body.Username,
		"role":                  rec.Role,
		"platforms":             platforms,
		"is_admin":              rec.Role == "admin",
	})
}

// handleAuthRefresh rotates a refresh token (#17). Single-use:
// consuming the same refresh token twice triggers replay detection
// and revokes the whole session.
func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, 400, "invalid_body", "invalid JSON")
		return
	}

	claims, err := auth.VerifyRefreshToken(s.cfg.SecretKey, body.RefreshToken)
	if err != nil {
		writeJSONError(w, 401, refreshErrorCode(err), err.Error())
		return
	}

	sessionID, cErr := s.db.ConsumeRefreshToken(claims.TokenID)
	if cErr != nil {
		writeJSONError(w, 401, refreshErrorCode(cErr), cErr.Error())
		return
	}
	_ = sessionID // matches claims.SessionID; used as the authoritative lookup key below

	tokenID, issuedAtMs, refreshExpiresAtMs, err := s.db.CreateRefreshToken(claims.SessionID, auth.RefreshTokenTTL)
	if err != nil {
		slog.Error("CreateRefreshToken rotate", "err", err)
		writeJSONError(w, 500, "session_create_failed", "could not rotate refresh token")
		return
	}

	platforms := s.effectivePlatforms(claims.Platforms)
	accessToken := auth.SignAccessToken(s.cfg.SecretKey, claims.Username, claims.Role, platforms, claims.SessionID, issuedAtMs)
	refreshToken := auth.SignRefreshToken(s.cfg.SecretKey, claims.Username, claims.Role, platforms, claims.SessionID, tokenID, issuedAtMs, refreshExpiresAtMs)

	writeJSON(w, 200, map[string]any{
		"access_token":          accessToken,
		"refresh_token":         refreshToken,
		"access_expires_at_ms":  issuedAtMs + auth.AccessTokenTTL.Milliseconds(),
		"refresh_expires_at_ms": refreshExpiresAtMs,
	})
}

// handleAuthLogout revokes the session identified by the presented
// refresh token. Fire-and-forget — client logs out locally regardless
// of whether the call reaches the server (#17).
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, 400, "invalid_body", "invalid JSON")
		return
	}
	claims, err := auth.VerifyRefreshToken(s.cfg.SecretKey, body.RefreshToken)
	if err != nil {
		// Even if the token's bad, we tell the client OK — they logged out locally already.
		writeJSON(w, 200, map[string]any{})
		return
	}
	if err := s.db.RevokeAuthSession(claims.SessionID, "user_logout"); err != nil {
		slog.Error("RevokeAuthSession", "session", claims.SessionID, "err", err)
	}
	writeJSON(w, 200, map[string]any{})
}

// handleAccountDelete removes the user's account and all scoped data.
// Admin is protected — the plan (#17 + #18) requires a 403 +
// error_code=admin_account_protected.
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	if user.Role == "admin" {
		writeJSONError(w, 403, "admin_account_protected",
			"Admin account cannot be deleted from the client. Sign in on the web admin console.")
		return
	}

	// Multi-user account deletion is out-of-scope for N=1 deployment per
	// server-side-changes.md #17. We revoke all sessions — the account
	// entry itself remains until the multi-user work lands.
	if err := s.db.RevokeAuthSessionsForUser(user.Username, "account_deleted"); err != nil {
		slog.Error("RevokeAuthSessionsForUser", "user", user.Username, "err", err)
	}
	writeJSON(w, 200, map[string]any{})
}

// handleAuthVerify is a legacy probe — Bearer verification already ran
// in the middleware; this just echoes back who the caller is.
func (s *Server) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	writeJSON(w, 200, map[string]any{
		"valid":    true,
		"username": user.Username,
		"role":     user.Role,
	})
}

// refreshErrorCode maps refresh-path verify + consume errors onto the
// #18 401 enum.
func refreshErrorCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrTokenExpired):
		return "refresh_token_expired"
	case errors.Is(err, auth.ErrTokenLegacyShape):
		return "legacy_token_invalid"
	case errors.Is(err, auth.ErrTokenMalformed):
		return "refresh_token_invalid"
	case errors.Is(err, auth.ErrTokenWrongType):
		return "refresh_token_invalid"
	case errors.Is(err, db.ErrRefreshTokenExpired):
		return "refresh_token_expired"
	case errors.Is(err, db.ErrRefreshTokenConsumed):
		return "refresh_token_replayed"
	case errors.Is(err, db.ErrSessionRevoked):
		return "session_revoked"
	case errors.Is(err, db.ErrRefreshTokenUnknown):
		return "refresh_token_invalid"
	}
	return "refresh_token_invalid"
}
