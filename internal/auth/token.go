package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// #16 — paired access + refresh tokens with session tracking.
// Access TTL: 7 days. Refresh TTL: 90 days. Single-use refresh with
// server-side replay detection (see internal/db/auth_sessions.go).

const (
	AccessTokenTTL  = 7 * 24 * time.Hour
	RefreshTokenTTL = 90 * 24 * time.Hour
)

// TokenType enumerates the two signed token shapes. Clients never read
// this field — the typed verify wrappers (VerifyAccessToken,
// VerifyRefreshToken) enforce which token is allowed where.
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// TokenClaims is the signed payload. SessionID + TokenType identify
// which auth_sessions row this token belongs to; the server looks up
// revoked state on every request. ExpiresAtMs is unix-millis per #14.
type TokenClaims struct {
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	Platforms   []string `json:"platforms"`
	SessionID   string   `json:"session_id"`
	TokenType   string   `json:"token_type"`
	TokenID     string   `json:"token_id,omitempty"` // refresh only
	IssuedAtMs  int64    `json:"issued_at_ms"`
	ExpiresAtMs int64    `json:"expires_at_ms"`
}

// SignAccessToken produces a 7-day access token tied to a session.
func SignAccessToken(secret, username, role string, platforms []string, sessionID string, issuedAtMs int64) string {
	claims := TokenClaims{
		Username:    username,
		Role:        role,
		Platforms:   platforms,
		SessionID:   sessionID,
		TokenType:   TokenTypeAccess,
		IssuedAtMs:  issuedAtMs,
		ExpiresAtMs: issuedAtMs + AccessTokenTTL.Milliseconds(),
	}
	return signClaims(secret, claims)
}

// SignRefreshToken produces a 90-day refresh token identified by
// tokenID. The tokenID is what the server looks up in
// auth_refresh_tokens for replay detection.
func SignRefreshToken(secret, username, role string, platforms []string, sessionID, tokenID string, issuedAtMs, expiresAtMs int64) string {
	claims := TokenClaims{
		Username:    username,
		Role:        role,
		Platforms:   platforms,
		SessionID:   sessionID,
		TokenType:   TokenTypeRefresh,
		TokenID:     tokenID,
		IssuedAtMs:  issuedAtMs,
		ExpiresAtMs: expiresAtMs,
	}
	return signClaims(secret, claims)
}

// ErrTokenExpired means the token's ExpiresAtMs has passed.
var ErrTokenExpired = errors.New("token expired")

// ErrTokenMalformed covers every structural failure (bad base64, wrong
// signature, missing fields, wrong token type for the verifier).
var ErrTokenMalformed = errors.New("token malformed")

// ErrTokenWrongType fires when a refresh token is presented to an
// access-token verifier or vice versa. Security-critical — swapping the
// two would let a stolen refresh token authenticate business requests.
var ErrTokenWrongType = errors.New("token wrong type")

// ErrTokenLegacyShape covers the Python-era v1 payloads that signed
// correctly but don't carry session_id / token_type. Callers report this
// to clients as legacy_token_invalid so Android forces a re-login.
var ErrTokenLegacyShape = errors.New("token legacy shape")

// VerifyAccessToken validates signature + expiry + token_type=access.
// Does NOT consult the session-revoked state; the middleware does that
// via db.GetAuthSession — keeps auth package DB-free.
func VerifyAccessToken(secret, token string) (*TokenClaims, error) {
	return verifyTyped(secret, token, TokenTypeAccess)
}

// VerifyRefreshToken validates signature + expiry + token_type=refresh.
// Caller must additionally check replay state via
// db.ConsumeRefreshToken before issuing a new pair.
func VerifyRefreshToken(secret, token string) (*TokenClaims, error) {
	return verifyTyped(secret, token, TokenTypeRefresh)
}

func verifyTyped(secret, token, wantType string) (*TokenClaims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, ErrTokenMalformed
	}
	encoded, sig := parts[0], parts[1]
	expectedSig := signHMAC(secret, encoded)
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil, ErrTokenMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrTokenMalformed
	}
	var claims TokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrTokenMalformed
	}
	// A v1 token signs correctly but carries no TokenType or SessionID.
	// Surface this as a distinct error so the middleware can emit
	// error_code=legacy_token_invalid (plan #18).
	if claims.SessionID == "" || claims.TokenType == "" {
		return nil, ErrTokenLegacyShape
	}
	if claims.TokenType != wantType {
		return nil, ErrTokenWrongType
	}
	if time.Now().UnixMilli() > claims.ExpiresAtMs {
		return nil, ErrTokenExpired
	}
	return &claims, nil
}

func signClaims(secret string, claims TokenClaims) string {
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := signHMAC(secret, encoded)
	return encoded + "." + sig
}

func signHMAC(secret, data string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
