package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/settings"
)

func (s *Server) registerSearchAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/search/suggest", s.handleSearchSuggest)
}

func (s *Server) handleSearchSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 200, map[string]any{"channels": []any{}, "youtube_videos": []any{}})
		return
	}

	channelLimit := 8
	videoLimit := 8
	if cl, err := strconv.Atoi(r.URL.Query().Get("channel_limit")); err == nil && cl > 0 && cl <= 30 {
		channelLimit = cl
	}
	if vl, err := strconv.Atoi(r.URL.Query().Get("video_limit")); err == nil && vl > 0 && vl <= 30 {
		videoLimit = vl
	}

	dearrowMode, _ := s.db.GetSetting("dearrow_mode", "off")
	dearrowMode = settings.NormalizeDearrowMode(dearrowMode)

	channels, _ := s.db.SearchChannelsFast(q, channelLimit)
	videos, _ := s.db.SearchVideosFast(q, videoLimit)

	chResult := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		chResult = append(chResult, map[string]any{
			"channel_id": ch.ChannelID,
			"name":       components.ChannelDisplayName(ch),
			"handle":     components.ChannelDisplayHandle(ch),
			"platform":   ch.Platform,
			"is_starred": ch.IsStarred,
			"avatar_url": ch.AvatarURL,
		})
	}

	// Suggest only shows YouTube videos (X posts are in the full search page)
	vidResult := make([]map[string]any, 0, len(videos))
	for _, v := range videos {
		if v.Platform != "youtube" && v.Platform != "" {
			continue
		}
		vidResult = append(vidResult, map[string]any{
			"video_id":      v.VideoID,
			"title":         ResolveDearrowTitle(dearrowMode, v.Title, v.DearrowTitle, v.DearrowTitleCasual),
			"channel_name":  v.ChannelName,
			"channel_id":    v.ChannelID,
			"platform":      v.Platform,
			"thumbnail_url": ResolveDearrowThumbURL(dearrowMode, v.VideoID, v.DearrowThumbPath),
			"is_temp":       v.IsTemp,
		})
	}

	writeJSON(w, 200, map[string]any{
		"channels":       chResult,
		"youtube_videos": vidResult,
	})
}
