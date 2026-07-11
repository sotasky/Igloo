package web

import "net/http"

func (s *Server) registerSyncAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sync/moments-cursor", s.handleMomentsCursor)
}

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
		SortAtMs    int64  `json:"sort_at_ms"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		if requestBodyTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"success": false, "error": requestBodyTooLargeMessage})
			return
		}
		writeJSON(w, 400, map[string]any{"success": false, "error": "video_id required"})
		return
	}
	if body.VideoID == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "video_id required"})
		return
	}

	_, err := s.db.MutateMomentsCursor(body.VideoID, body.PositionMs, body.UpdatedAtMs, body.Scope, body.SortAtMs)
	if writeMutationError(w, "MutateMomentsCursor", err) {
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}
