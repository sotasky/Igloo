package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/subscribe"
)

func (s *Server) registerDownloadAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/quick-download", s.handleQuickDownload)
	mux.HandleFunc("POST /api/cancel-download", s.handleCancelDownload)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("POST /api/resume", s.handleResume)
	mux.HandleFunc("GET /api/stop-play-btn", s.handleStopPlayBtn)
}

func (s *Server) handleQuickDownload(w http.ResponseWriter, r *http.Request) {
	isHTMX := r.Header.Get("HX-Request") != ""
	user := userFromContext(r.Context())
	if user == nil || user.Role != "admin" {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "Quick download is restricted to admins")
			return
		}
		writeJSONError(w, http.StatusForbidden, "forbidden", "Quick download is restricted to admins")
		return
	}

	var rawURL string
	if isHTMX && r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		rawURL = strings.TrimSpace(r.FormValue("url"))
	} else {
		var body struct {
			URL         string `json:"url"`
			SaveChannel bool   `json:"save_channel"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]any{"error": "url required"})
			return
		}
		rawURL = body.URL
	}

	platform := subscribe.DetectPlatform(rawURL, "")
	if rawURL == "" || subscribe.ValidateInput(rawURL, platform) != nil {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(422)
			fmt.Fprint(w, `Enter a supported YouTube, TikTok, or X URL`)
			return
		}
		writeJSON(w, 400, map[string]any{"error": "supported YouTube, TikTok, or X URL required"})
		return
	}
	if !s.platformEnabled(platform) {
		msg := fmt.Sprintf("%s is not enabled on this Igloo server", platformChoiceLabel(platform))
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(422)
			fmt.Fprint(w, template.HTMLEscapeString(msg))
			return
		}
		writeJSON(w, 422, map[string]any{"error": msg, "platform": platform})
		return
	}

	result := s.workers.DownloadTemp(r.Context(), rawURL, false)

	if isHTMX {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if result.Success && result.VideoID != "" {
			fmt.Fprintf(w, `Downloaded. <a href="/player/%s?autoplay=1" style="text-decoration:underline">Watch</a>`,
				template.HTMLEscapeString(template.URLQueryEscaper(result.VideoID)))
		} else if result.Success {
			fmt.Fprint(w, `Download complete`)
		} else {
			msg := result.Message
			if msg == "" {
				msg = "Download failed"
			}
			w.WriteHeader(422)
			fmt.Fprint(w, template.HTMLEscapeString(msg))
		}
		return
	}

	writeJSON(w, 200, map[string]any{
		"success":     result.Success,
		"message":     result.Message,
		"video_id":    result.VideoID,
		"playlist_id": result.PlaylistID,
	})
}

func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		VideoID string `json:"video_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	// Stub: full cancellation requires tracking active download contexts
	writeJSON(w, 200, map[string]any{"success": true, "video_id": body.VideoID})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.workers.SetStopRequested(true)
	s.workers.SetIngestPaused(true)
	if r.Header.Get("HX-Request") != "" {
		components.StopPlayButton(s.pageProps(w, r), true).Render(r.Context(), w)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "stopped": true})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.workers.SetStopRequested(false)
	s.workers.SetIngestPaused(false)
	s.workers.KickDownloadPool()
	s.workers.KickFeedMedia()
	s.workers.KickIngest()
	if r.Header.Get("HX-Request") != "" {
		components.StopPlayButton(s.pageProps(w, r), false).Render(r.Context(), w)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "resumed": true})
}

func (s *Server) handleStopPlayBtn(w http.ResponseWriter, r *http.Request) {
	components.StopPlayButton(s.pageProps(w, r), s.workers.IsStopRequested()).Render(r.Context(), w)
}
