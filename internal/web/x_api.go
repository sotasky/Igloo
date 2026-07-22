package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/xfeed"
)

func (s *Server) registerXAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/x/diagnostics", s.handleXDiagnostics)
	mux.HandleFunc("GET /api/x/probe", s.handleXProbe)
}

func (s *Server) handleXDiagnostics(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleXProbe(w http.ResponseWriter, r *http.Request) {
	handle := xfeed.NormalizeHandle(r.URL.Query().Get("handle"))
	if !xfeed.ValidHandle(handle) {
		writeJSON(w, 400, map[string]any{"success": false, "error": "valid handle required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	cookiesDir := ""
	if s.cfg != nil {
		cookiesDir = s.cfg.CookiesDir
	}
	client := xfeed.NewClient(cookiesDir)
	client.OperationSink = s.db
	items, err := client.FetchTimeline(ctx, handle, 1)
	if err != nil {
		channelID := "twitter_" + handle
		if s.workers == nil || !s.workers.ReportExternalResult(err) {
			_ = s.db.RecordIngestFailure(channelID, err.Error(), 0)
		}
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if s.workers != nil {
		s.workers.ReportExternalResult(nil)
	}
	_ = s.db.RecordIngestSuccess("twitter_"+handle, float64(time.Now().Unix()), 0)

	parentMedia := 0
	quoteMedia := 0
	quotes := 0
	retweets := 0
	replies := 0
	for _, item := range items {
		if item.MediaJSON != "" {
			parentMedia++
		}
		if item.QuoteTweetID != "" {
			quotes++
		}
		if item.QuoteMediaJSON != "" {
			quoteMedia++
		}
		if item.IsRetweet {
			retweets++
		}
		if item.IsReply {
			replies++
		}
	}
	writeJSON(w, 200, map[string]any{
		"success":       true,
		"parsed_items":  len(items),
		"handle":        handle,
		"parent_media":  parentMedia,
		"quotes":        quotes,
		"quote_media":   quoteMedia,
		"retweets":      retweets,
		"replies":       replies,
		"cookie_files":  len(xfeed.DiscoverCookieFiles(cookiesDir)),
		"gallery_limit": 1,
	})
}
