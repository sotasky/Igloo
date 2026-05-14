package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
)

// registerShortsAPIRoutes registers shorts watch-history API routes.
func (s *Server) registerShortsAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/shorts/watched/{videoID}", s.handleShortsWatched)
	mux.HandleFunc("GET /api/shorts/watched", s.handleShortsWatchedList)
	mux.HandleFunc("GET /api/shorts/cards", s.handleShortsCards)
	mux.HandleFunc("GET /api/stories/{channelID}/cards", s.handleStoryCards)
}

// GET /api/shorts/cards?tab=all|following&offset=N&limit=N
// Returns hydrated card HTML for the lightweight skeletons on the Moments grid.
func (s *Server) handleShortsCards(w http.ResponseWriter, r *http.Request) {
	tab := db.NormalizeMomentsTab(r.URL.Query().Get("tab"))
	if strings.TrimSpace(r.URL.Query().Get("tab")) == "" {
		tab = s.db.MomentsDefaultTab()
	}
	if tab == "stories" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			offset = n
		}
	}
	limit := shortsHydrateBatchSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 240 {
				n = 240
			}
			limit = n
		}
	}

	videos, err := s.db.GetVideos(db.GetVideosOpts{
		Platform: "shorts",
		Limit:    limit,
		Offset:   offset,
		// Hydrated batches must match the initial Moments page order:
		// oldest -> newest, with newly discovered items appended at the end.
		OrderAsc:    true,
		MomentsMode: tab,
	})
	if err != nil {
		slog.Error("GetVideos shorts cards", "err", err, "tab", tab, "offset", offset, "limit", limit)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	userID := ""
	if user := userFromContext(r.Context()); user != nil {
		userID = user.Username
	}
	if err := s.db.AttachStoryStatusToVideos(userID, videos, time.Now().UnixMilli()); err != nil {
		slog.Error("AttachStoryStatusToVideos shorts cards", "err", err, "tab", tab)
	}

	p := s.pageProps(w, r)
	p.ActiveNav = "shorts"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.ShortsCardsPartial(p, videos).Render(r.Context(), w)
}

func (s *Server) handleStoryCards(w http.ResponseWriter, r *http.Request) {
	channelID := strings.TrimSpace(r.PathValue("channelID"))
	if channelID == "" {
		http.Error(w, "channelID required", http.StatusBadRequest)
		return
	}
	userID := ""
	if user := userFromContext(r.Context()); user != nil {
		userID = user.Username
	}
	nowMs := time.Now().UnixMilli()
	statuses, err := s.db.GetStoryStatusForChannelIDs(userID, []string{channelID}, nowMs)
	if err != nil {
		slog.Error("GetStoryStatusForChannelIDs", "channel", channelID, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if statuses[channelID].Count <= 0 {
		p := s.pageProps(w, r)
		p.ActiveNav = "shorts"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.ShortsCardsPartial(p, nil).Render(r.Context(), w)
		return
	}
	videos, err := s.db.GetStoryVideos(userID, channelID, nowMs)
	if err != nil {
		slog.Error("GetStoryVideos", "channel", channelID, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	p := s.pageProps(w, r)
	p.ActiveNav = "shorts"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.ShortsCardsPartial(p, videos).Render(r.Context(), w)
}

// POST /api/shorts/watched/{videoID}
// Records that the authenticated user watched the shorts video (TikTok/Instagram).
// Idempotent — repeated calls refresh viewed_at.
func (s *Server) handleShortsWatched(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	videoID := r.PathValue("videoID")
	if videoID == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "videoID required"})
		return
	}

	if _, err := s.db.UpsertMomentView(user.Username, videoID); err != nil {
		slog.Error("UpsertMomentView", "user", user.Username, "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if err := s.db.RecordSyncChange("moment_view", videoID, `{"viewed":true}`); err != nil {
		slog.Error("RecordSyncChange moment_view", "video", videoID, "err", err)
	}
	syncVersion, _ := s.db.GetCurrentSyncVersion()

	writeJSON(w, 200, map[string]any{
		"success":      true,
		"sync_version": syncVersion,
	})
}

// GET /api/shorts/watched?since=<rfc3339>&limit=N
// Returns this user's moment views, newest first.
func (s *Server) handleShortsWatchedList(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}

	var since time.Time
	if raw := r.URL.Query().Get("since"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
		}
	}
	limit := 1000
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}

	rows, err := s.db.ListMomentViews(user.Username, since, limit)
	if err != nil {
		slog.Error("ListMomentViews", "user", user.Username, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, mv := range rows {
		out = append(out, map[string]any{
			"video_id":  mv.VideoID,
			"viewed_at": mv.ViewedAt.UnixMilli(),
		})
	}
	writeJSON(w, 200, map[string]any{"views": out})
}
