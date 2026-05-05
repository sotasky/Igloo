package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/rsshub"
)

func (s *Server) registerRSSHubAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rsshub/diagnostics", s.handleRSSHubDiagnostics)
	mux.HandleFunc("GET /api/rsshub/probe", s.handleRSSHubProbe)
}

func (s *Server) handleRSSHubDiagnostics(w http.ResponseWriter, r *http.Request) {
	channels, err := s.db.GetSubscribedChannels()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}

	// Collect X/Twitter handles.
	var handles []string
	handleSet := make(map[string]bool)
	for _, ch := range channels {
		handle := twitterSourceHandle(ch)
		if handle != "" && !handleSet[handle] {
			handles = append(handles, handle)
			handleSet[handle] = true
		}
	}

	// Get ingest states.
	states, err := s.db.GetAllIngestStates()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "ingest state error"})
		return
	}
	stateMap := make(map[string]map[string]any, len(states))
	for _, st := range states {
		status := "unknown"
		switch {
		case st.FailCount == 0 && st.LastSuccessAt > 0:
			status = "ok"
		case st.FailCount > 0 && st.FailCount <= 3:
			status = "cooling"
		case st.FailCount > 3:
			status = "failing"
		case st.LastSuccessAt > 0:
			status = "degraded"
		}
		// ingest_state.handle uses full channel_id (e.g. "twitter_username");
		// diagnostics are keyed by ch.SourceID (bare username) — strip prefix.
		key := strings.TrimPrefix(st.Handle, "twitter_")
		stateMap[key] = map[string]any{
			"status":           status,
			"fail_count":       st.FailCount,
			"last_success_at":  st.LastSuccessAt,
			"last_attempt_at":  st.LastAttemptAt,
			"last_error":       st.LastError,
			"last_http_status": st.LastHTTPStatus,
			"avg_latency_ms":   st.AvgLatencyMs,
		}
	}

	// Get item counts per source.
	itemCounts, _ := s.db.CountFeedItemsBySource()

	// Build per-handle diagnostics.
	var diagnostics []map[string]any
	for _, handle := range handles {
		diag := map[string]any{
			"handle":     handle,
			"status":     "unknown",
			"fail_count": 0,
			"item_count": 0,
		}
		if st, ok := stateMap[handle]; ok {
			for k, v := range st {
				diag[k] = v
			}
		}
		if c, ok := itemCounts[handle]; ok {
			diag["item_count"] = c
		}
		diagnostics = append(diagnostics, diag)
	}

	now := float64(time.Now().Unix())
	writeJSON(w, 200, map[string]any{
		"success":    true,
		"handles":    diagnostics,
		"total":      len(handles),
		"checked_at": now,
	})
}

func (s *Server) handleRSSHubProbe(w http.ResponseWriter, r *http.Request) {
	handle := r.URL.Query().Get("handle")
	if handle == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "handle required"})
		return
	}

	if s.cfg.RSSHubBase == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "RSSHUB_BASE not configured"})
		return
	}

	url := rsshub.BuildEnrichedURL(s.cfg.RSSHubBase, handle)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	req.Header.Set("User-Agent", "Igloo/1.0")
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		writeJSON(w, 200, map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d", resp.StatusCode)})
		return
	}

	feed, err := rsshub.Parse(resp.Body)
	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}

	items := rsshub.ToFeedItems(feed, handle)
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"parsed_items": len(items),
		"handle":       handle,
	})
}
