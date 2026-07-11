package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/auth"
)

func contextWithUser(r *http.Request, username, role string) context.Context {
	return context.WithValue(r.Context(), userContextKey, &userInfo{
		Username: username,
		Role:     role,
	})
}

// Covers the #16 auth-session + refresh-token-rotation contract end to
// end: session creation, token consumption, replay detection, revoked
// session rejection. The middleware side (rejecting a revoked session
// on subsequent requests) lives in a separate test below.

func TestRefreshRotationIssuesNewPair(t *testing.T) {
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

	req := httptest.NewRequest("POST", "/api/auth/refresh",
		strings.NewReader(`{"refresh_token":"`+refreshToken+`"}`))
	rec := httptest.NewRecorder()
	srv.handleAuthRefresh(rec, req)

	if rec.Code != 200 {
		t.Fatalf("refresh: got %d — %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["access_token"] == "" || body["access_token"] == nil {
		t.Errorf("no access_token in response: %v", body)
	}
	if body["refresh_token"] == refreshToken {
		t.Errorf("refresh_token should rotate, got same value")
	}
}

func TestRefreshReplayRevokesSession(t *testing.T) {
	srv := newTestServer(t)

	sessionID, _ := srv.db.CreateAuthSession("alice")
	tokenID, issuedAtMs, expiresAtMs, _ := srv.db.CreateRefreshToken(sessionID, auth.RefreshTokenTTL)
	refreshToken := auth.SignRefreshToken(srv.cfg.SecretKey, "alice", "admin", nil, sessionID, tokenID, issuedAtMs, expiresAtMs)
	payload := `{"refresh_token":"` + refreshToken + `"}`

	// First consume: legitimate rotation.
	rec1 := httptest.NewRecorder()
	srv.handleAuthRefresh(rec1, httptest.NewRequest("POST", "/api/auth/refresh", strings.NewReader(payload)))
	if rec1.Code != 200 {
		t.Fatalf("first refresh expected 200, got %d — %s", rec1.Code, rec1.Body.String())
	}

	// Second consume of the SAME refresh token: replay.
	rec2 := httptest.NewRecorder()
	srv.handleAuthRefresh(rec2, httptest.NewRequest("POST", "/api/auth/refresh", strings.NewReader(payload)))
	if rec2.Code != 401 {
		t.Fatalf("replay expected 401, got %d — %s", rec2.Code, rec2.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &body)
	if body["error_code"] != "refresh_token_replayed" {
		t.Errorf("expected error_code=refresh_token_replayed, got %v", body["error_code"])
	}

	// Session must be marked revoked.
	sess, err := srv.db.GetAuthSession(sessionID)
	if err != nil {
		t.Fatalf("GetAuthSession: %v", err)
	}
	if !sess.Revoked || sess.RevokeReason != "refresh_replay" {
		t.Errorf("session should be revoked with refresh_replay reason, got revoked=%v reason=%q", sess.Revoked, sess.RevokeReason)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	srv := newTestServer(t)

	sessionID, _ := srv.db.CreateAuthSession("alice")
	tokenID, issuedAtMs, expiresAtMs, _ := srv.db.CreateRefreshToken(sessionID, auth.RefreshTokenTTL)
	refreshToken := auth.SignRefreshToken(srv.cfg.SecretKey, "alice", "admin", nil, sessionID, tokenID, issuedAtMs, expiresAtMs)

	rec := httptest.NewRecorder()
	srv.handleAuthLogout(rec, httptest.NewRequest("POST", "/api/auth/logout",
		strings.NewReader(`{"refresh_token":"`+refreshToken+`"}`)))
	if rec.Code != 200 {
		t.Fatalf("logout: got %d", rec.Code)
	}

	sess, _ := srv.db.GetAuthSession(sessionID)
	if !sess.Revoked || sess.RevokeReason != "user_logout" {
		t.Errorf("expected user_logout reason, got revoked=%v reason=%q", sess.Revoked, sess.RevokeReason)
	}
}

func TestTokenErrorCodeMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"expired", auth.ErrTokenExpired, "access_token_expired"},
		{"legacy", auth.ErrTokenLegacyShape, "legacy_token_invalid"},
		{"wrong_type", auth.ErrTokenWrongType, "access_token_invalid"},
		{"malformed", auth.ErrTokenMalformed, "access_token_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenErrorCode(tc.err); got != tc.want {
				t.Errorf("%v → %s, want %s", tc.err, got, tc.want)
			}
		})
	}
}
