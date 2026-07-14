package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
)

func (s *Server) registerChannelAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/channels", s.handleChannelsList)
	mux.HandleFunc("POST /api/channels/{channelID}/star", s.handleChannelStar)
	mux.HandleFunc("POST /api/channels/{channelID}/subscribe", s.handleChannelSubscribe)
	mux.HandleFunc("GET /api/channels/{channelID}/settings", s.handleChannelSettingsGet)
	mux.HandleFunc("POST /api/channels/{channelID}/settings", s.handleChannelSettingsPost)
	mux.HandleFunc("POST /api/channels/{channelID}/refresh", s.handleChannelRefresh)
	mux.HandleFunc("GET /api/channels/{channelID}/feed", s.handleChannelFeed)
	mux.HandleFunc("GET /api/channels/{channelID}/videos", s.handleChannelVideos)
	mux.HandleFunc("POST /api/platform/{platform}/refresh", s.handlePlatformRefresh)
}

type channelListItem struct {
	ChannelID      string `json:"channel_id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Platform       string `json:"platform"`
	IsStarred      bool   `json:"is_starred"`
	IsSubscribed   bool   `json:"is_subscribed"`
	VideoCount     int    `json:"video_count"`
	AvatarURL      string `json:"avatar_url"`
	IncludeReposts *bool  `json:"include_reposts,omitempty"`
}

func (s *Server) handleChannelsList(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	channels := s.enrichedChannels()

	result := make([]channelListItem, 0, len(channels))
	for _, ch := range channels {
		switch platform {
		case "creators":
			if ch.Platform == "youtube" {
				continue
			}
		case "youtube":
			if ch.Platform != "youtube" {
				continue
			}
		case "":
			// no filter
		default:
			if ch.Platform != platform {
				continue
			}
		}

		result = append(result, channelListItem{
			ChannelID:      ch.ChannelID,
			Name:           ch.Name,
			URL:            ch.URL,
			Platform:       ch.Platform,
			IsStarred:      ch.IsStarred,
			IsSubscribed:   ch.IsSubscribed,
			VideoCount:     ch.VideoCount,
			AvatarURL:      ch.AvatarURL,
			IncludeReposts: ch.IncludeReposts,
		})
	}

	writeJSON(w, 200, map[string]any{"channels": result})
}

func (s *Server) handleChannelStar(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")

	isStarred, err := s.db.ToggleChannelStar(channelID)
	if err != nil {
		slog.Error("ToggleChannelStar", "channel", channelID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	_ = s.db.InvalidateFeedWindowByChannelID(channelID)
	s.workers.KickFeedScoring()

	// If HTMX request, return the updated star button HTML
	if r.Header.Get("HX-Request") != "" {
		p := s.pageProps(w, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"starChanged":{"channelId":%q,"starred":%t}}`, channelID, isStarred))
		switch r.URL.Query().Get("ctx") {
		case "feed":
			_ = components.FeedStarButton(p, channelID, isStarred).Render(r.Context(), w)
		case "profile":
			_ = components.ProfileCardStarButton(p, channelID, isStarred).Render(r.Context(), w)
		case "sidebar":
			_ = components.SidebarStarButton(p, channelID, isStarred).Render(r.Context(), w)
		case "player":
			_ = components.PlayerStarButton(p, channelID, isStarred).Render(r.Context(), w)
		default:
			_ = components.ChannelStarButton(p, channelID, isStarred).Render(r.Context(), w)
		}
		return
	}

	writeJSON(w, 200, map[string]any{
		"success":    true,
		"is_starred": isStarred,
	})
}

func (s *Server) handleChannelSubscribe(w http.ResponseWriter, r *http.Request) {
	channelID := canonicalSubscribeChannelID(r.PathValue("channelID"))
	ch, err := s.db.GetChannelByID(channelID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "channel not found"})
		return
	}

	alreadyFollowed := ch.IsSubscribed || s.db.IsChannelFollowed(channelID)
	if !alreadyFollowed {
		if err := s.db.FollowChannel(channelID); err != nil {
			slog.Error("FollowChannel", "channel", channelID, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "db error"})
			return
		}
		if s.workers != nil {
			s.workers.Emit("system", fmt.Sprintf("Subscribed: %s (%s)", ch.Name, ch.Platform), "done")
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"channel_id":      ch.ChannelID,
		"name":            ch.Name,
		"platform":        ch.Platform,
		"subscribed":      true,
		"already_existed": alreadyFollowed,
	})
}

func canonicalSubscribeChannelID(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if strings.HasPrefix(channelID, "UC") {
		return "youtube_" + channelID
	}
	return channelID
}

func (s *Server) handleChannelSettingsGet(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")

	settings, err := s.db.GetChannelSettings(channelID)
	if err != nil {
		slog.Error("GetChannelSettings", "channel", channelID, "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	if settings == nil {
		writeJSON(w, 404, map[string]any{"error": "Channel not found"})
		return
	}

	if r.Header.Get("HX-Request") != "" {
		platform := r.URL.Query().Get("platform")
		cs := components.ChannelSettingsData{
			Quality:            settings.Quality,
			MaxVideos:          settings.MaxVideos,
			DownloadSubtitles:  settings.DownloadSubtitles,
			MediaOnly:          settings.MediaOnly,
			MediaDownloadLimit: settings.MediaDownloadLimit,
		}
		w.Header().Set("Content-Type", "text/html")
		_ = components.ChannelSettingsForm(s.pageProps(w, r), channelID, platform, cs).Render(r.Context(), w)
		return
	}

	writeJSON(w, 200, map[string]any{"settings": settings})
}

func (s *Server) handleChannelSettingsPost(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	isHTMX := r.Header.Get("HX-Request") != ""
	ct := r.Header.Get("Content-Type")

	// Boot-time retweet-mute sync in feed.js POSTs settings for every muted
	// channel in localStorage; stale entries for unsubscribed channels would
	// FK-fail on channel_settings insert. No-op when the channel isn't in
	// the channels table.
	if ch, err := s.db.GetChannel(channelID); err != nil || ch == nil {
		writeJSON(w, 200, map[string]any{"success": true, "noop": true})
		return
	}
	previousSettings, _ := s.db.GetChannelSettings(channelID)

	var body map[string]any

	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		_ =
			// HTMX form submission.
			r.ParseForm()
		body = make(map[string]any)
		if v := r.FormValue("quality"); v != "" {
			body["quality"] = v
		} else {
			body["quality"] = ""
		}
		if v := r.FormValue("max_videos"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				body["max_videos"] = n
			}
		}
		body["download_subtitles"] = r.FormValue("download_subtitles") != ""
		body["media_only"] = r.FormValue("media_only") != ""
		if v := r.FormValue("media_download_limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				body["media_download_limit"] = n
			}
		}
	} else {
		// JSON from Android / legacy JS.
		if err := decodeJSON(w, r, &body); err != nil {
			if requestBodyTooLarge(err) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
				return
			}
			writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
			return
		}
	}

	// If settings_inherit is true, clear all per-channel overrides.
	if inherit, ok := body["settings_inherit"]; ok {
		if b, ok := inherit.(bool); ok && b {
			if err := s.db.ClearChannelSettings(channelID); err != nil {
				slog.Error("ClearChannelSettings", "channel", channelID, "err", err)
				writeJSON(w, 500, map[string]any{"error": "db error"})
				return
			}
		}
		delete(body, "settings_inherit")
	}

	// Convert bool values to 0/1 integers.
	boolToIntFields := map[string]bool{
		"media_only":      true,
		"include_reposts": true,
	}
	for field := range boolToIntFields {
		if val, ok := body[field]; ok {
			if b, ok := val.(bool); ok {
				if b {
					body[field] = 1
				} else {
					body[field] = 0
				}
			}
		}
	}
	// Nullable bool fields: false → NULL (inherit from global), true → 1.
	nullableBoolFields := []string{"download_subtitles"}
	for _, field := range nullableBoolFields {
		if val, ok := body[field]; ok {
			if b, ok := val.(bool); ok {
				if b {
					body[field] = 1
				} else {
					body[field] = nil
				}
			}
		}
	}

	// Convert float64 JSON numbers to int.
	intFields := map[string]bool{
		"max_videos":           true,
		"media_download_limit": true,
	}
	for field := range intFields {
		if val, ok := body[field]; ok {
			if f, ok := val.(float64); ok {
				body[field] = int(f)
			}
		}
	}

	if err := s.db.UpdateChannelSettings(channelID, body); err != nil {
		slog.Error("UpdateChannelSettings", "channel", channelID, "err", err)
		if isHTMX {
			http.Error(w, "db error", 500)
		} else {
			writeJSON(w, 500, map[string]any{"error": "db error"})
		}
		return
	}
	updated := s.applyChannelSettingEffects(channelID, previousSettings)

	if isHTMX {
		// Re-read settings and render updated form.
		platform := r.URL.Query().Get("platform")
		cs := components.ChannelSettingsData{}
		if updated != nil {
			cs = components.ChannelSettingsData{
				Quality:            updated.Quality,
				MaxVideos:          updated.MaxVideos,
				DownloadSubtitles:  updated.DownloadSubtitles,
				MediaOnly:          updated.MediaOnly,
				MediaDownloadLimit: updated.MediaDownloadLimit,
			}
		}
		w.Header().Set("Content-Type", "text/html")
		_ = components.ChannelSettingsForm(s.pageProps(w, r), channelID, platform, cs).Render(r.Context(), w)
	} else {
		writeJSON(w, 200, map[string]any{
			"success": true,
			"message": fmt.Sprintf("Settings updated for %s", channelID),
		})
	}
}

func (s *Server) applyChannelSettingEffects(channelID string, previous *db.ChannelSettings) *db.ChannelSettings {
	updated, _ := s.db.GetChannelSettings(channelID)
	if previous == nil || updated == nil || s.workers == nil {
		return updated
	}
	if updated.MaxVideos < previous.MaxVideos {
		if err := s.workers.EnforceVideoRetentionForChannel(channelID); err != nil {
			slog.Error("EnforceVideoRetentionForChannel", "channel", channelID, "err", err)
		}
	}
	if updated.MediaDownloadLimit < previous.MediaDownloadLimit {
		if err := s.workers.EnforceXMediaRetentionForChannel(channelID); err != nil {
			slog.Error("EnforceXMediaRetentionForChannel", "channel", channelID, "err", err)
		}
	}
	if updated.MediaDownloadLimit > previous.MediaDownloadLimit {
		if err := s.workers.ExpandXMediaRetentionForChannel(channelID); err != nil {
			slog.Error("ExpandXMediaRetentionForChannel", "channel", channelID, "err", err)
		}
	} else if previous.MaxVideos != updated.MaxVideos || previous.IncludeReposts != updated.IncludeReposts {
		s.workers.TriggerChannelCheck(channelID)
	}
	return updated
}

func (s *Server) handleChannelRefresh(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	profileErr := s.db.RequestProfileJob(channelID, time.Now().UnixMilli())
	ch, err := s.db.GetChannel(channelID)
	if err != nil || ch == nil {
		if profileErr == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"success":    true,
				"channel_id": channelID,
			})
			return
		}
		writeJSON(w, 404, map[string]any{"error": "channel not found"})
		return
	}
	if profileErr != nil {
		slog.Warn("RequestProfileJob", "channel", channelID, "err", profileErr)
	}
	if ch.Platform == "twitter" {
		// Reset ingest state and fetch this channel immediately so a completed
		// refresh response means the channel feed has been re-queried upstream.
		_ = s.db.ResetIngestHandle(channelID)
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		n, err := s.workers.FetchOneChannel(ctx, channelID)
		if err != nil {
			slog.Warn("FetchOneChannel", "channel", channelID, "err", err)
			writeJSON(w, 502, map[string]any{"success": false, "channel_id": channelID, "error": err.Error()})
			return
		}
		slog.Info("FetchOneChannel", "channel", channelID, "upserted", n)
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"channelRefreshComplete":{"channelId":%q,"fetched":%d}}`, channelID, n))
		writeJSON(w, 200, map[string]any{
			"success":    true,
			"channel_id": ch.ChannelID,
			"fetched":    n,
			"message":    fmt.Sprintf("Fetched %d posts", n),
		})
		return
	} else {
		// Queue the channel directly so refresh follows the same discovery path as background checks.
		s.workers.TriggerChannelCheck(channelID)
	}
	writeJSON(w, 200, map[string]any{"success": true, "channel_id": channelID, "message": "Refresh triggered"})
}

func (s *Server) handleChannelFeed(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	ch, err := s.db.GetChannel(channelID)
	if err != nil || ch == nil {
		writeJSON(w, 404, map[string]any{"error": "channel not found"})
		return
	}
	// Use source_id (e.g. "@handle") to query feed items
	handle := ch.SourceID
	if handle == "" && ch.Platform == "twitter" {
		handle = twitterSourceHandle(*ch)
	}
	if handle == "" {
		handle = ch.Name
	}

	limit := 40
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	items, _ := s.db.GetFeedItemsByAuthor(handle, limit)
	items = feed.EnrichFeedItems(s.db, items)
	if feed.AlgorithmicFeedEnabled(s.db) {
		items = feed.RankFeedItems(items)
	} else {
		items = feed.SortFeedItemsChronological(items)
	}

	s.writeFeedResponse(w, items, false, len(items), "")
}

func (s *Server) handleChannelVideos(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}
	videos, _ := s.db.GetVideos(db.GetVideosOpts{
		ChannelID: channelID,
		Limit:     limit,
		Offset:    offset,
	})
	total, _ := s.db.GetVideoCount(db.GetVideosOpts{ChannelID: channelID})

	var jsonVideos []map[string]any
	for _, v := range videos {
		jsonVideos = append(jsonVideos, videoToJSON(v))
	}
	if jsonVideos == nil {
		jsonVideos = []map[string]any{}
	}
	writeJSON(w, 200, map[string]any{"videos": jsonVideos, "total": total})
}

func (s *Server) handlePlatformRefresh(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 422, map[string]any{"success": false, "error": "platform is not enabled", "platform": platform})
		return
	}
	s.workers.TriggerPlatformRefresh(platform)
	writeJSON(w, 200, map[string]any{"success": true, "platform": platform})
}
