package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// #16 — auth session + refresh token storage.
// One auth_sessions row per login. Zero-or-more auth_refresh_tokens rows
// (only the most recently issued one is unconsumed at any time). Replay
// detection: a second consume of an already-consumed refresh token ID
// marks the whole session revoked, killing every paired access token
// on the next probe.

var ErrSessionRevoked = errors.New("session revoked")
var ErrRefreshTokenExpired = errors.New("refresh token expired")
var ErrRefreshTokenConsumed = errors.New("refresh token already consumed")
var ErrRefreshTokenUnknown = errors.New("refresh token unknown")

// AuthSession is the row shape returned from lookup calls.
type AuthSession struct {
	SessionID      string
	Username       string
	CreatedAtMs    int64
	LastActiveAtMs int64
	Revoked        bool
	RevokeReason   string
}

// NewRandomID returns a 128-bit hex-encoded random identifier for use as
// a session_id or refresh token_id.
func NewRandomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("rand.Read: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// CreateAuthSession opens a new session for username. Returns the
// session_id. Caller pairs this with CreateRefreshToken and signs the
// access + refresh token pair.
func (db *DB) CreateAuthSession(username string) (string, error) {
	sessionID := NewRandomID()
	now := time.Now().UnixMilli()
	err := db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO auth_sessions
			  (session_id, username, created_at_ms, last_active_at_ms, revoked)
			VALUES (?, ?, ?, ?, 0)`,
			sessionID, username, now, now,
		)
		return err
	})
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

// GetAuthSession returns the session row, or sql.ErrNoRows if missing.
func (db *DB) GetAuthSession(sessionID string) (*AuthSession, error) {
	var s AuthSession
	var reason sql.NullString
	var revoked int
	err := db.conn.QueryRow(`
		SELECT session_id, username, created_at_ms, last_active_at_ms, revoked, revoke_reason
		FROM auth_sessions WHERE session_id = ?`, sessionID,
	).Scan(&s.SessionID, &s.Username, &s.CreatedAtMs, &s.LastActiveAtMs, &revoked, &reason)
	if err != nil {
		return nil, err
	}
	s.Revoked = revoked != 0
	if reason.Valid {
		s.RevokeReason = reason.String
	}
	return &s, nil
}

// TouchAuthSession bumps last_active_at_ms. Best-effort — errors logged
// by caller, never fatal.
func (db *DB) TouchAuthSession(sessionID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE auth_sessions SET last_active_at_ms = ? WHERE session_id = ?`,
			time.Now().UnixMilli(), sessionID,
		)
		return err
	})
}

// RevokeAuthSession marks a session revoked. Idempotent.
func (db *DB) RevokeAuthSession(sessionID, reason string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE auth_sessions SET revoked = 1, revoke_reason = ? WHERE session_id = ?`,
			reason, sessionID,
		)
		return err
	})
}

// RevokeAuthSessionsForUser revokes every session belonging to a user.
// Used by account-delete.
func (db *DB) RevokeAuthSessionsForUser(username, reason string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE auth_sessions SET revoked = 1, revoke_reason = ? WHERE username = ? AND revoked = 0`,
			reason, username,
		)
		return err
	})
}

// CreateRefreshToken issues a fresh refresh token for a session. Called
// at login and after every successful rotation.
func (db *DB) CreateRefreshToken(sessionID string, ttl time.Duration) (tokenID string, issuedAtMs int64, expiresAtMs int64, err error) {
	tokenID = NewRandomID()
	issuedAtMs = time.Now().UnixMilli()
	expiresAtMs = issuedAtMs + ttl.Milliseconds()
	err = db.WithWrite(func(tx *sql.Tx) error {
		_, e := tx.Exec(`
			INSERT INTO auth_refresh_tokens
			  (token_id, session_id, issued_at_ms, expires_at_ms, consumed_at_ms)
			VALUES (?, ?, ?, ?, NULL)`,
			tokenID, sessionID, issuedAtMs, expiresAtMs,
		)
		return e
	})
	return
}

// ConsumeRefreshToken atomically checks-and-marks a refresh token as used.
// Returns (session_id, nil) on successful first-use.
// Returns ErrRefreshTokenUnknown if the token ID has no row.
// Returns ErrRefreshTokenExpired when expires_at_ms has passed.
// Returns ErrRefreshTokenConsumed (AND revokes the session) on replay.
// Returns ErrSessionRevoked if the session was already revoked.
func (db *DB) ConsumeRefreshToken(tokenID string) (sessionID string, err error) {
	now := time.Now().UnixMilli()
	var outcome error
	txErr := db.WithWrite(func(tx *sql.Tx) error {
		var sid string
		var expiresAt int64
		var consumedAt sql.NullInt64
		var sessionRevoked int
		qErr := tx.QueryRow(`
			SELECT rt.session_id, rt.expires_at_ms, rt.consumed_at_ms, s.revoked
			FROM auth_refresh_tokens rt
			JOIN auth_sessions s ON s.session_id = rt.session_id
			WHERE rt.token_id = ?`, tokenID,
		).Scan(&sid, &expiresAt, &consumedAt, &sessionRevoked)
		if qErr == sql.ErrNoRows {
			outcome = ErrRefreshTokenUnknown
			return nil
		}
		if qErr != nil {
			return qErr
		}
		if sessionRevoked != 0 {
			outcome = ErrSessionRevoked
			return nil
		}
		if consumedAt.Valid {
			// Replay: revoke the whole session within this tx so the
			// revoke commits alongside the outcome classification.
			if _, e := tx.Exec(
				`UPDATE auth_sessions SET revoked = 1, revoke_reason = ? WHERE session_id = ?`,
				"refresh_replay", sid,
			); e != nil {
				return e
			}
			outcome = ErrRefreshTokenConsumed
			return nil
		}
		if now > expiresAt {
			outcome = ErrRefreshTokenExpired
			return nil
		}
		if _, e := tx.Exec(
			`UPDATE auth_refresh_tokens SET consumed_at_ms = ? WHERE token_id = ?`,
			now, tokenID,
		); e != nil {
			return e
		}
		sessionID = sid
		return nil
	})
	if txErr != nil {
		return "", txErr
	}
	return sessionID, outcome
}
