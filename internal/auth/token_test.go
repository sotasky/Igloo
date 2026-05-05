package auth

import (
	"errors"
	"testing"
	"time"
)

const testSecret = "test-secret-key-at-least-32-bytes-long"

func TestSignAndVerifyAccessToken(t *testing.T) {
	issued := time.Now().UnixMilli()
	token := SignAccessToken(testSecret, "user_a", "admin", []string{"youtube", "twitter"}, "sess_abc", issued)
	if token == "" {
		t.Fatal("empty token")
	}

	claims, err := VerifyAccessToken(testSecret, token)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if claims.Username != "user_a" {
		t.Errorf("username = %q", claims.Username)
	}
	if claims.SessionID != "sess_abc" {
		t.Errorf("session_id = %q", claims.SessionID)
	}
	if claims.TokenType != TokenTypeAccess {
		t.Errorf("token_type = %q", claims.TokenType)
	}
}

func TestSignAndVerifyRefreshToken(t *testing.T) {
	issued := time.Now().UnixMilli()
	expires := issued + RefreshTokenTTL.Milliseconds()
	token := SignRefreshToken(testSecret, "user_a", "admin", nil, "sess_x", "tok_1", issued, expires)

	claims, err := VerifyRefreshToken(testSecret, token)
	if err != nil {
		t.Fatalf("VerifyRefreshToken: %v", err)
	}
	if claims.TokenID != "tok_1" {
		t.Errorf("token_id = %q", claims.TokenID)
	}
	if claims.TokenType != TokenTypeRefresh {
		t.Errorf("token_type = %q", claims.TokenType)
	}
}

func TestExpiredAccessToken(t *testing.T) {
	past := time.Now().Add(-25 * time.Hour).UnixMilli()
	token := SignAccessToken(testSecret, "user_a", "admin", nil, "sess_x", past)
	_, err := VerifyAccessToken(testSecret, token)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestWrongTokenType(t *testing.T) {
	// Sign a refresh token then try to verify it as access — must fail.
	issued := time.Now().UnixMilli()
	expires := issued + RefreshTokenTTL.Milliseconds()
	refreshTok := SignRefreshToken(testSecret, "u", "admin", nil, "sess", "tok", issued, expires)

	_, err := VerifyAccessToken(testSecret, refreshTok)
	if !errors.Is(err, ErrTokenWrongType) {
		t.Errorf("expected ErrTokenWrongType, got %v", err)
	}
}

func TestInvalidSignature(t *testing.T) {
	issued := time.Now().UnixMilli()
	token := SignAccessToken("correct-secret", "u", "admin", nil, "sess", issued)
	_, err := VerifyAccessToken("wrong-secret", token)
	if !errors.Is(err, ErrTokenMalformed) {
		t.Errorf("expected ErrTokenMalformed, got %v", err)
	}
}

func TestLegacyTokenFormatRejected(t *testing.T) {
	// A token without session_id + token_type simulates a v1 token — the
	// verifier must reject it so clients fall back to re-login.
	legacyClaims := TokenClaims{
		Username:    "u",
		Role:        "admin",
		ExpiresAtMs: time.Now().Add(time.Hour).UnixMilli(),
		// SessionID and TokenType deliberately empty.
	}
	token := signClaims(testSecret, legacyClaims)
	_, err := VerifyAccessToken(testSecret, token)
	if !errors.Is(err, ErrTokenLegacyShape) {
		t.Errorf("expected ErrTokenLegacyShape for v1-shape token, got %v", err)
	}
}
