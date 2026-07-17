package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/settings"
	"github.com/screwys/igloo/internal/webtheme"
)

func (s *Server) registerAdminAPIRoutes(mux *http.ServeMux) {
	// Settings
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("GET /api/settings/form", s.handleSettingsForm)
	mux.HandleFunc("POST /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/theme.css", s.handleThemeCSS)
	mux.HandleFunc("GET /api/theme.json", s.handleThemeJSON)

	// Cookies
	mux.HandleFunc("GET /api/cookies", s.handleGetCookies)
	mux.HandleFunc("POST /api/cookies/{platform}", s.handleUploadCookie)
	mux.HandleFunc("POST /api/cookies/{platform}/toggle", s.handleToggleCookie)
	mux.HandleFunc("POST /api/cookies/{platform}/browser", s.handleSetCookieBrowser)
	mux.HandleFunc("DELETE /api/cookies/{platform}", s.handleDeleteCookie)

	// Users
	mux.HandleFunc("GET /api/users", s.handleListUsers)
	mux.HandleFunc("POST /api/users", s.handleCreateUser)
	mux.HandleFunc("PUT /api/users/{username}", s.handleUpdateUser)
	mux.HandleFunc("DELETE /api/users/{username}", s.handleDeleteUser)

	// Subscribe / Unsubscribe
	mux.HandleFunc("POST /api/subscribe", s.handleSubscribe)
	mux.HandleFunc("DELETE /api/unsubscribe/{channelID}", s.handleUnsubscribe)

	// Auth
	mux.HandleFunc("POST /api/auth/change-credentials", s.handleChangeCredentials)

	// Config export / import
	mux.HandleFunc("GET /api/config/export", s.handleConfigExport)
	mux.HandleFunc("GET /api/config/export-subscriptions", s.handleConfigExportSubscriptions)
	mux.HandleFunc("GET /api/config/export-full", s.handleConfigExportFull)
	mux.HandleFunc("POST /api/config/import", s.handleConfigImport)
}

// ── Settings ─────────────────────────────────────────────────────────────────

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	settings, err := s.db.GetAllSettings()
	if err != nil {
		slog.Error("GetAllSettings", "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	writeJSON(w, 200, settingsToAPIFormat(settings))
}

// handleSettingsForm renders the preferences form body (HTMX fragment).
func (s *Server) handleSettingsForm(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	dbSettings, err := s.db.GetAllSettings()
	if err != nil {
		slog.Error("GetAllSettings", "err", err)
		http.Error(w, "db error", 500)
		return
	}
	prefsSettings := settingsToAPIFormat(dbSettings)
	if previewLang := strings.TrimSpace(r.URL.Query().Get("lang")); previewLang != "" {
		persisted, _ := prefsSettings["ui_language"].(string)
		prefsSettings["_persisted_ui_language"] = persisted
		if previewLang == "auto" {
			prefsSettings["ui_language"] = "auto"
		} else {
			prefsSettings["ui_language"] = s.catalog().ResolveLanguage(previewLang)
		}
	}
	prefs := components.PrefsData{Settings: prefsSettings}
	p := s.pageProps(w, r)
	var buf bytes.Buffer
	if err := components.PrefsBody(p, prefs).Render(r.Context(), &buf); err != nil {
		slog.Error("render PrefsBody", "err", err)
		http.Error(w, "render error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write(buf.Bytes())
}

var shortcutDefaults = map[string]string{
	"feed.like": "l", "feed.bookmark": "b", "feed.share": "s", "feed.translate": "t", "feed.media": "f",
	"shorts.autoplay": "a", "shorts.bookmark": "b", "shorts.share": "s", "shorts.grid": "c",
	"player.fullscreen": "f", "player.cinema": "c", "player.bookmark": "b", "player.share": "s", "player.autoplay": "a",
	"global.sidebar": "z",
	"global.logs":    "l",
}

var settingsInternalKeys = map[string]bool{
	"search_fts5_available": true, "search_ready": true, "search_version": true,
	"search_building": true, "search_last_built_at": true, "search_last_error": true,
	"shorts_cursor_video_id": true, "shorts_cursor_updated_at_ms": true,
	"deleted_channels": true, "x_media_staging_dir": true, "android_last_known_server_url": true,
	"cookies_enabled_twitter": true, "cookies_enabled_youtube": true, "cookies_enabled_tiktok": true,
	"include_reposts_default":  true,
	"youtube_check_interval":   true,
	"shorts_check_interval":    true,
	"instagram_check_interval": true,
}

// settingsToAPIFormat transforms raw DB settings into the format the JS frontend expects.
func settingsToAPIFormat(dbSettings map[string]string) map[string]any {
	result := make(map[string]any)

	// Start with defaults.
	for k, v := range settings.Defaults {
		result[k] = v
	}
	result["shortcuts"] = shortcutDefaults

	// Override with DB values.
	for k, v := range dbSettings {
		if settingsInternalKeys[k] {
			continue
		}
		if k == "sponsorblock" || k == "sponsorblock_categories" {
			result["sponsorblock_categories"] = v
			continue
		}
		if k == "shortcuts" {
			// Parse JSON string into object.
			var parsed map[string]string
			if json.Unmarshal([]byte(v), &parsed) == nil {
				result["shortcuts"] = parsed
			}
			continue
		}
		result[k] = v
	}
	themeSettings := webtheme.NormalizeSettings(webtheme.Settings{
		ThemeID:   fmt.Sprintf("%v", result["web_theme_id"]),
		AccentHex: fmt.Sprintf("%v", result["web_theme_accent"]),
		CustomCSS: fmt.Sprintf("%v", result["web_custom_css"]),
	})
	result["web_theme_id"] = themeSettings.ThemeID
	result["web_theme_accent"] = themeSettings.AccentHex
	result["web_custom_css"] = themeSettings.CustomCSS

	return result
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	isHTMX := r.Header.Get("HX-Request") != ""
	ct := r.Header.Get("Content-Type")

	var body map[string]string
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		// HTMX form submission.
		if err := r.ParseForm(); err != nil {
			if isHTMX {
				http.Error(w, "invalid form", 400)
			} else {
				writeJSON(w, 400, map[string]any{"error": "invalid form"})
			}
			return
		}
		body = s.settingsFromForm(r)
	} else {
		// JSON from Android / legacy JS.
		var raw map[string]any
		if err := decodeJSON(w, r, &raw); err != nil {
			if requestBodyTooLarge(err) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
				return
			}
			writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
			return
		}
		body = make(map[string]string, len(raw))
		for k, v := range raw {
			switch val := v.(type) {
			case string:
				body[k] = val
			case bool:
				if val {
					body[k] = "true"
				} else {
					body[k] = "false"
				}
			case float64:
				if val == float64(int(val)) {
					body[k] = fmt.Sprintf("%d", int(val))
				} else {
					body[k] = fmt.Sprintf("%g", val)
				}
			default:
				// Objects (e.g. shortcuts) — serialize back to JSON string for DB storage.
				if b, err := json.Marshal(val); err == nil {
					body[k] = string(b)
				}
			}
		}
	}

	normalizeSettingsUpdate(body)
	if err := validateSettingsUpdate(body); err != nil {
		if isHTMX {
			http.Error(w, err.Error(), 400)
		} else {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
		}
		return
	}
	previousLimits := map[string]int{
		"youtube":   s.db.IntSetting("youtube_max_videos"),
		"tiktok":    s.db.IntSetting("shorts_max_videos"),
		"instagram": s.db.IntSetting("instagram_max_videos"),
	}
	previousXMediaLimit := s.db.IntSetting("media_download_limit_default")
	previousTiktokReposts := s.db.MomentsIncludeRepostsEnabled()
	previousInstagramTagged := s.db.InstagramIncludeTaggedEnabled()
	if err := s.db.UpdateSettings(body); err != nil {
		slog.Error("UpdateSettings", "err", err)
		if isHTMX {
			http.Error(w, "db error", 500)
		} else {
			writeJSON(w, 500, map[string]any{"error": "db error"})
		}
		return
	}
	if s.workers != nil {
		changes := []struct {
			platform, settingKey string
			extraChanged         bool
		}{
			{"youtube", "youtube_max_videos", false},
			{"tiktok", "shorts_max_videos", previousTiktokReposts != s.db.MomentsIncludeRepostsEnabled()},
			{"instagram", "instagram_max_videos", previousInstagramTagged != s.db.InstagramIncludeTaggedEnabled()},
		}
		var refresh []string
		for _, change := range changes {
			currentLimit := s.db.IntSetting(change.settingKey)
			if currentLimit < previousLimits[change.platform] {
				if err := s.workers.EnforceVideoRetentionForPlatform(change.platform); err != nil {
					slog.Error("EnforceVideoRetentionForPlatform", "platform", change.platform, "err", err)
				}
			}
			if currentLimit != previousLimits[change.platform] || change.extraChanged {
				refresh = append(refresh, change.platform)
			}
		}
		for _, platform := range refresh {
			s.workers.TriggerPlatformRefresh(platform)
		}
		currentXMediaLimit := s.db.IntSetting("media_download_limit_default")
		if currentXMediaLimit < previousXMediaLimit {
			if err := s.workers.EnforceXMediaRetention(); err != nil {
				slog.Error("EnforceXMediaRetention", "err", err)
			}
		} else if currentXMediaLimit > previousXMediaLimit {
			if err := s.workers.ExpandXMediaRetention(); err != nil {
				slog.Error("ExpandXMediaRetention", "err", err)
			}
		}
	}

	if isHTMX {
		w.WriteHeader(200)
	} else {
		writeJSON(w, 200, map[string]any{"success": true})
	}
}

func normalizeSettingsUpdate(body map[string]string) {
	if _, ok := body["web_theme_id"]; ok {
		body["web_theme_id"] = webtheme.NormalizeThemeID(body["web_theme_id"])
	}
	if _, ok := body["web_theme_accent"]; ok {
		themeID := body["web_theme_id"]
		if themeID == "" {
			themeID = webtheme.DefaultThemeID
		}
		body["web_theme_accent"] = webtheme.NormalizeAccentHex(themeID, body["web_theme_accent"])
	}
	if _, ok := body["web_custom_css"]; ok {
		body["web_custom_css"] = webtheme.NormalizeCustomCSS(body["web_custom_css"])
	}
	if v, ok := body["translate_backend"]; ok {
		body["translate_backend"] = settings.NormalizeTranslateBackend(v)
	}
	if v, ok := body["translate_auto_mode"]; ok {
		body["translate_auto_mode"] = settings.NormalizeTranslateAutoMode(v)
	}
	if v, ok := body["translate_auto_lookahead"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			n = settings.IntDefault("translate_auto_lookahead")
		}
		body["translate_auto_lookahead"] = strconv.Itoa(settings.ClampTranslateLookahead(n))
	}
	if v, ok := body["backup_keep_count"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			n = settings.IntDefault("backup_keep_count")
		}
		body["backup_keep_count"] = strconv.Itoa(settings.ClampBackupKeepCount(n))
	}
	if v, ok := body["moments_default_tab"]; ok {
		body["moments_default_tab"] = db.NormalizeMomentsTab(v)
	}
	if v, ok := body["stories_window_hours"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			n = settings.IntDefault("stories_window_hours")
		}
		body["stories_window_hours"] = strconv.Itoa(db.NormalizeStoriesWindowHours(n))
	}
}

func validateSettingsUpdate(body map[string]string) error {
	if v, ok := body["backup_dir"]; ok {
		dir := strings.TrimSpace(v)
		body["backup_dir"] = dir
		if dir != "" && !filepath.IsAbs(dir) {
			return fmt.Errorf("backup_dir must be an absolute path")
		}
	}
	return nil
}

// normalizeSkipLangs trims, lowercases, de-duplicates, and drops empties
// from a comma-separated language code list.
func normalizeSkipLangs(raw string) string {
	seen := make(map[string]bool)
	var out []string
	for _, v := range strings.Split(raw, ",") {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return strings.Join(out, ",")
}

// settingsFromForm converts HTMX form values to the settings map format.
func (s *Server) settingsFromForm(r *http.Request) map[string]string {
	body := make(map[string]string)

	// Simple text/number fields.
	simpleFields := []string{
		"web_theme_id", "web_theme_accent",
		"quality", "youtube_fetch_delay", "youtube_max_videos",
		"youtube_default_playback_speed",
		"tiktok_fetch_delay", "shorts_max_videos",
		"instagram_fetch_delay", "instagram_max_videos",
		"moments_default_tab", "stories_window_hours",
		"media_download_limit_default", "x_feed_fetch_delay",
		"translate_target_lang", "translate_backend",
		"translate_auto_mode", "translate_auto_lookahead",
		"backup_dir", "backup_keep_count", "starting_page",
		"dearrow_mode", "ui_language",
	}
	for _, key := range simpleFields {
		if v := r.FormValue(key); v != "" {
			body[key] = v
		}
	}
	clearableFields := []string{"translate_api_site", "translate_api_key", "translate_model", "web_custom_css"}
	for _, key := range clearableFields {
		if _, ok := r.Form[key]; ok {
			body[key] = r.FormValue(key)
		}
	}

	// Checkboxes: present=true, absent=false.
	checkboxFields := []string{
		"download_subtitles", "media_only_default",
		"archive_bookmarks", "backup_enabled",
		"algorithmic_feed_enabled", "moments_include_reposts_default",
		"instagram_include_tagged_default", "share_embed_friendly_links",
	}
	for _, key := range checkboxFields {
		if r.FormValue(key) != "" {
			body[key] = "true"
		} else {
			body[key] = "false"
		}
	}

	// Skip-translate languages are submitted as a single CSV hidden input.
	body["translate_skip_langs"] = normalizeSkipLangs(r.FormValue("translate_skip_langs"))

	// SponsorBlock categories → "sponsor:silent,selfpromo:ask,..."
	sbCategories := []string{"sponsor", "selfpromo", "interaction", "intro", "outro", "preview", "filler", "music_offtopic"}
	var sbParts []string
	for _, cat := range sbCategories {
		val := r.FormValue("sb_" + cat)
		if val == "" {
			val = "off"
		}
		sbParts = append(sbParts, cat+":"+val)
	}
	body["sponsorblock_categories"] = strings.Join(sbParts, ",")

	// Shortcuts stay JS-managed; preserve from DB so we don't clear them.
	if shortcuts, _ := s.db.GetSetting("shortcuts", ""); shortcuts != "" {
		body["shortcuts"] = shortcuts
	}

	return body
}

func (s *Server) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	themeID, _ := s.db.GetSetting("web_theme_id", webtheme.DefaultThemeID)
	accent, _ := s.db.GetSetting("web_theme_accent", webtheme.DefaultAccentHex)
	customCSS, _ := s.db.GetSetting("web_custom_css", "")
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(webtheme.CSS(webtheme.Settings{
		ThemeID:   themeID,
		AccentHex: accent,
		CustomCSS: customCSS,
	})))
}

func (s *Server) handleThemeJSON(w http.ResponseWriter, r *http.Request) {
	themeID, _ := s.db.GetSetting("web_theme_id", webtheme.DefaultThemeID)
	accent, _ := s.db.GetSetting("web_theme_accent", webtheme.DefaultAccentHex)
	w.Header().Set("Cache-Control", "no-store")
	snapshot := webtheme.ThemeSnapshot(webtheme.Settings{
		ThemeID:   themeID,
		AccentHex: accent,
	})
	writeJSON(w, http.StatusOK, snapshot.Map())
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user := userFromContext(r.Context())
	if user != nil && user.Role == "admin" {
		return true
	}
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `<span class="status-message error">Admin access required.</span>`)
		return false
	}
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required")
		return false
	}
	writeJSONError(w, http.StatusForbidden, "admin_required", "Admin access required")
	return false
}
