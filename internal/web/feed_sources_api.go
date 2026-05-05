package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/screwys/igloo/internal/xsource"
)

func (s *Server) registerFeedSourceAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/feed/sources", s.handleFeedSourcesList)
	mux.HandleFunc("POST /api/feed/sources", s.handleFeedSourcesCreate)
	mux.HandleFunc("DELETE /api/feed/sources/{sourceID}", s.handleFeedSourcesDelete)
	mux.HandleFunc("POST /api/feed/sources/{sourceID}/refresh", s.handleFeedSourcesRefresh)
}

func (s *Server) handleFeedSourcesList(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	platform := r.URL.Query().Get("platform")
	sources, err := s.db.ListFeedSources(platform)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "sources": sources})
}

func (s *Server) handleFeedSourcesCreate(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	var body struct {
		URL   string `json:"url"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid json"})
		return
	}
	source, err := xsource.ParseURL(body.URL)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if body.Label != "" {
		source.Label = body.Label
	}
	now := time.Now()
	source.CreatedAt = now
	source.UpdatedAt = now
	if err := s.db.UpsertFeedSource(source); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "source": source})
}

func (s *Server) handleFeedSourcesDelete(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	sourceID := r.PathValue("sourceID")
	if sourceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "source_id required"})
		return
	}
	if err := s.db.DeleteFeedSource(sourceID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "removed": true})
}

func (s *Server) handleFeedSourcesRefresh(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	sourceID := r.PathValue("sourceID")
	if sourceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "source_id required"})
		return
	}
	if s.workers == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "workers unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	count, err := s.workers.FetchOneFeedSource(ctx, sourceID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		writeJSON(w, status, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "items": count})
}
