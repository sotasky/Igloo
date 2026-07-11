package web

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/subscribe"
)

// ── Subscribe / Unsubscribe ───────────────────────────────────────────────────

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	isHTMX := r.Header.Get("HX-Request") != ""

	var rawURL, rawPlatform string
	if handle := r.URL.Query().Get("handle"); handle != "" {
		// HTMX follow buttons on profile cards
		rawURL = "https://x.com/" + handle
		rawPlatform = "twitter"
	} else if isHTMX && r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		// HTMX form submission (add-channel modal)
		rawURL = strings.TrimSpace(r.FormValue("url"))
		rawPlatform = r.FormValue("platform")
	} else {
		var body struct {
			URL      string `json:"url"`
			Platform string `json:"platform"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			if requestBodyTooLarge(err) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
				return
			}
			writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
			return
		}
		rawURL = body.URL
		rawPlatform = body.Platform
	}

	if rawURL == "" {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<span class="status-message error">Paste a URL first.</span>`)
			return
		}
		writeJSON(w, 400, map[string]any{"error": "url required"})
		return
	}

	platform := subscribe.DetectPlatform(rawURL, rawPlatform)
	if !s.platformEnabled(platform) {
		msg := fmt.Sprintf("%s is not enabled on this Igloo server", platformChoiceLabel(platform))
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(422)
			_, _ = fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(msg))
			return
		}
		writeJSON(w, 422, map[string]any{"error": msg, "platform": platform})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	ch, localResolved, err := s.resolveLocalYouTubeSubscribeChannel(rawURL, platform)
	if err == nil && !localResolved {
		ch, err = subscribe.ResolveChannel(ctx, rawURL, platform, s.workers.Downloader())
	}
	if err != nil {
		slog.Error("ResolveChannel", "url", rawURL, "platform", platform, "err", err)
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(err.Error()))
			return
		}
		writeJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}

	created := true
	if err := s.db.AddChannel(ch); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			if !s.db.IsChannelFollowed(ch.ChannelID) {
				if followErr := s.db.FollowChannel(ch.ChannelID); followErr != nil {
					slog.Error("FollowChannel existing", "channel", ch.ChannelID, "err", followErr)
					if isHTMX {
						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						_, _ = fmt.Fprint(w, `<span class="status-message error">Database error.</span>`)
						return
					}
					writeJSON(w, 500, map[string]any{"error": "db error"})
					return
				}
				created = false
			} else {
				if isHTMX {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = fmt.Fprint(w, `<span class="status-message error">Already subscribed.</span>`)
					return
				}
				writeJSON(w, 409, map[string]any{"error": "already subscribed", "channel_id": ch.ChannelID})
				return
			}
		} else {
			slog.Error("AddChannel", "channel", ch.ChannelID, "err", err)
			if isHTMX {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = fmt.Fprint(w, `<span class="status-message error">Database error.</span>`)
				return
			}
			writeJSON(w, 500, map[string]any{"error": "db error"})
			return
		}
	}

	s.workers.Emit("system", fmt.Sprintf("Subscribed: %s (%s)", ch.Name, ch.Platform), "done")

	if isHTMX {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"followChanged":{"channelId":%q}}`, ch.ChannelID))
		// Modal form: show success + close modal after brief delay
		if r.URL.Query().Get("handle") == "" {
			msg := template.HTMLEscapeString(ch.Name) + " added."
			_, _ = fmt.Fprintf(w, `<span class="status-message success">%s</span><script>setTimeout(function(){var m=document.getElementById('add-sub-modal');if(m){m.classList.add('hidden');m.setAttribute('aria-hidden','true');document.body.style.overflow=''}},900)</script>`, msg)
		}
		return
	}

	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"success":         true,
		"channel_id":      ch.ChannelID,
		"name":            ch.Name,
		"platform":        ch.Platform,
		"url":             ch.URL,
		"already_existed": !created,
	})
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	keys, err := s.db.PurgeUnfollowedChannelContent(channelID)
	if err != nil {
		slog.Error("PurgeUnfollowedChannelContent", "channel", channelID, "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	s.removeCanonicalAssetFiles(keys)

	s.workers.Emit("system", fmt.Sprintf("Unsubscribed: %s", channelID), "info")

	if r.Header.Get("HX-Request") != "" {
		// Return empty — hx-swap="outerHTML" removes the channel item
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "deleted_files": len(keys)})
}
