package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/auth"
)

// Response envelope contract: every /api/* JSON response carries
// {ok, server_time_ms} alongside endpoint fields. sync_version + sync_stream
// land with mutation endpoints.
// HTML pages, HTMX fragments, binary media, and file downloads are exempt.

func nowMs() int64 { return time.Now().UnixMilli() }

func writeJSONEnvelope(w http.ResponseWriter, status int, body map[string]any) {
	if body == nil {
		body = map[string]any{}
	}
	if _, set := body["ok"]; !set {
		body["ok"] = status < 400
	}
	body["server_time_ms"] = nowMs()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("envelope encode", "err", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	writeJSONEnvelope(w, status, map[string]any{
		"ok":            false,
		"error_code":    code,
		"error_message": msg,
	})
}

// tokenErrorCode maps an auth-package verify error onto the 401
// error_code enum from server-side-changes.md #18.
func tokenErrorCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrTokenExpired):
		return "access_token_expired"
	case errors.Is(err, auth.ErrTokenLegacyShape):
		return "legacy_token_invalid"
	case errors.Is(err, auth.ErrTokenWrongType):
		return "access_token_invalid"
	case errors.Is(err, auth.ErrTokenMalformed):
		return "access_token_invalid"
	}
	return "access_token_invalid"
}

// apiPath reports whether a request path should carry the JSON envelope.
// Binary media, subtitles, and file-download endpoints are excluded.
func apiPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	switch {
	// Binary media bucket — every /api/media/<kind>/... endpoint serves a
	// raw file body (image/video/text), not a JSON envelope.
	case strings.HasPrefix(path, "/api/media/"):
		return false
	case strings.HasPrefix(path, "/api/android/sync/assets/"):
		return false
	case strings.HasPrefix(path, "/api/download/video/"):
		return false
	case strings.HasPrefix(path, "/api/config/export"):
		return false
	}
	return true
}
