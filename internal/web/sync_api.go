package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

// registerSyncAPIRoutes registers web interaction sync routes. Android uses
// Android sync plus mutation deltas instead of this web poller surface.
func (s *Server) registerSyncAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sync/changes", s.handleSyncChanges)
	mux.HandleFunc("POST /api/sync/moments-cursor", s.handleMomentsCursor)
}

// handleSyncChanges backs the web SyncPoller for cross-tab and cross-page
// interaction updates.
func (s *Server) handleSyncChanges(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}

	sinceStr := r.URL.Query().Get("since")
	since, _ := strconv.ParseInt(sinceStr, 10, 64)

	currentVersion, _ := s.db.GetCurrentSyncVersion()

	// Baseline request: client just loaded, return current version only.
	if since <= 0 {
		writeJSON(w, 200, map[string]any{
			"version":   currentVersion,
			"changes":   []any{},
			"truncated": false,
		})
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	changes, truncated, err := s.db.GetSyncChanges(since, limit)
	if err != nil {
		slog.Error("GetSyncChanges", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	currentVersion, _ = s.db.GetCurrentSyncVersion()

	result := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		result = append(result, map[string]any{
			"version":    c.Version,
			"type":       c.Type,
			"item_id":    c.ItemID,
			"value":      c.Value,
			"created_at": c.CreatedAtMs,
		})
	}

	writeJSON(w, 200, map[string]any{
		"version":   currentVersion,
		"changes":   result,
		"truncated": truncated,
	})
}

// handleMomentsCursor persists the web shorts resume point and records a sync
// change so other open pages can observe it through SyncPoller.
func (s *Server) handleMomentsCursor(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}

	var body struct {
		VideoID     string `json:"video_id"`
		PositionMs  int64  `json:"position_ms"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
		Scope       string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.VideoID == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "video_id required"})
		return
	}

	res, err := s.db.ApplyMomentsCursorMutation(user.Username, body.VideoID, body.PositionMs, body.UpdatedAtMs, body.Scope)
	if err != nil {
		slog.Error("ApplyMomentsCursorMutation", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"sync_version": res.SyncVersion,
	})
}
