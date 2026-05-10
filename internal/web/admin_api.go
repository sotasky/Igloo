package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fullimport"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/restore"
	"github.com/screwys/igloo/internal/settings"
	"github.com/screwys/igloo/internal/subscribe"
	"github.com/screwys/igloo/internal/webtheme"
)

func (s *Server) registerAdminAPIRoutes(mux *http.ServeMux) {
	// Settings
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("GET /api/settings/form", s.handleSettingsForm)
	mux.HandleFunc("POST /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/theme.css", s.handleThemeCSS)

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
	mux.HandleFunc("GET /api/config/export-full", s.handleConfigExportFull)
	mux.HandleFunc("POST /api/config/import", s.handleConfigImport)
}

// ── Settings ─────────────────────────────────────────────────────────────────

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
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
	w.Write(buf.Bytes())
}

var shortcutDefaults = map[string]string{
	"feed.like": "l", "feed.bookmark": "b", "feed.share": "s", "feed.translate": "t", "feed.media": "f",
	"shorts.autoplay": "a", "shorts.bookmark": "b", "shorts.share": "s", "shorts.grid": "c",
	"player.fullscreen": "f", "player.bookmark": "b", "player.share": "s", "player.autoplay": "a",
	"global.logs": "l",
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
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
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
	if err := s.db.UpdateSettings(body); err != nil {
		slog.Error("UpdateSettings", "err", err)
		if isHTMX {
			http.Error(w, "db error", 500)
		} else {
			writeJSON(w, 500, map[string]any{"error": "db error"})
		}
		return
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
		"backup_dir", "starting_page",
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

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user := userFromContext(r.Context())
	if user != nil && user.Role == "admin" {
		return true
	}
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<span class="status-message error">Admin access required.</span>`)
		return false
	}
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required")
		return false
	}
	writeJSONError(w, http.StatusForbidden, "admin_required", "Admin access required")
	return false
}

// ── Cookies ───────────────────────────────────────────────────────────────────

func (s *Server) handleGetCookies(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if r.Header.Get("HX-Request") != "" {
		s.renderCookieRowsHTML(w, r)
		return
	}

	platforms := s.enabledPlatforms()
	result := make(map[string]any, len(platforms))
	for _, platform := range platforms {
		path := filepath.Join(s.cfg.CookiesDir, platform+"_cookies.txt")
		info, err := os.Stat(path)
		exists := err == nil
		var size int64
		if exists {
			size = info.Size()
		}
		enabledVal, _ := s.db.GetSetting("cookies_"+platform+"_enabled", "1")
		browserVal, _ := s.db.GetSetting("cookies_"+platform+"_browser", "")
		result[platform] = map[string]any{
			"filename": platform + "_cookies.txt",
			"exists":   exists,
			"size":     size,
			"enabled":  enabledVal == "1",
			"browser":  browserVal,
		}
	}
	writeJSON(w, 200, result)
}

var cookiePlatformNames = []struct{ ID, Name string }{
	{"twitter", "X / Twitter"},
	{"youtube", "YouTube"},
	{"tiktok", "TikTok"},
	{"instagram", "Instagram"},
}

func (s *Server) renderCookieRowsHTML(w http.ResponseWriter, r *http.Request) {
	var rows []components.CookieRowData
	for _, p := range cookiePlatformNames {
		if !s.platformEnabled(p.ID) {
			continue
		}
		path := filepath.Join(s.cfg.CookiesDir, p.ID+"_cookies.txt")
		_, err := os.Stat(path)
		exists := err == nil
		enabledVal, _ := s.db.GetSetting("cookies_"+p.ID+"_enabled", "1")
		browserVal, _ := s.db.GetSetting("cookies_"+p.ID+"_browser", "")
		rows = append(rows, components.CookieRowData{
			Platform: p.ID,
			Name:     p.Name,
			Exists:   exists,
			Enabled:  enabledVal == "1",
			Browser:  browserVal,
		})
	}
	components.CookieRowsPanel(s.pageProps(w, r), rows).Render(r.Context(), w)
}

func (s *Server) handleUploadCookie(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, 400, map[string]any{"error": "multipart parse error"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "missing file"})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "read error"})
		return
	}

	if err := os.MkdirAll(s.cfg.CookiesDir, 0o700); err != nil {
		slog.Error("MkdirAll cookies", "err", err)
		writeJSON(w, 500, map[string]any{"error": "mkdir error"})
		return
	}
	dest := filepath.Join(s.cfg.CookiesDir, platform+"_cookies.txt")
	if err := atomicWrite(dest, data, 0o600); err != nil {
		slog.Error("write cookie", "platform", platform, "err", err)
		writeJSON(w, 500, map[string]any{"error": "write error"})
		return
	}
	if r.Header.Get("HX-Request") != "" {
		s.renderCookieRowsHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "platform": platform})
}

func (s *Server) handleToggleCookie(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}

	key := "cookies_" + platform + "_enabled"
	current, _ := s.db.GetSetting(key, "1")
	newVal := "0"
	if current == "0" {
		newVal = "1"
	}
	if err := s.db.SetSetting("", key, newVal); err != nil {
		slog.Error("SetSetting cookie toggle", "platform", platform, "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	if r.Header.Get("HX-Request") != "" {
		s.renderCookieRowsHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "enabled": newVal == "1"})
}

var validBrowsers = map[string]bool{
	"firefox": true, "chrome": true, "chromium": true, "brave": true, "edge": true, "": true,
}

func (s *Server) handleSetCookieBrowser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	isHTMX := r.Header.Get("HX-Request") != ""
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}

	var browser string
	if isHTMX {
		browser = r.FormValue("browser")
	} else {
		var body struct {
			Browser string `json:"browser"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad request"})
			return
		}
		browser = body.Browser
	}

	if !validBrowsers[browser] {
		writeJSON(w, 400, map[string]any{"error": "unsupported browser"})
		return
	}
	key := "cookies_" + platform + "_browser"
	if err := s.db.SetSetting("", key, browser); err != nil {
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	if isHTMX {
		s.renderCookieRowsHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "browser": browser})
}

func (s *Server) handleDeleteCookie(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}
	path := filepath.Join(s.cfg.CookiesDir, platform+"_cookies.txt")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Error("delete cookie", "platform", platform, "err", err)
		writeJSON(w, 500, map[string]any{"error": "delete error"})
		return
	}
	if r.Header.Get("HX-Request") != "" {
		s.renderCookieRowsHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

// ── Users ─────────────────────────────────────────────────────────────────────

type userResponse struct {
	Username  string   `json:"username"`
	Role      string   `json:"role"`
	Platforms []string `json:"platforms"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if r.Header.Get("HX-Request") != "" {
		// Form request: return create or edit form
		if form := r.URL.Query().Get("form"); form != "" {
			if form == "edit" {
				username := r.URL.Query().Get("user")
				users := auth.GetCachedUsers()
				if rec, ok := users[username]; ok {
					u := components.UserDisplay{Username: username, Role: rec.Role, Platforms: s.effectivePlatforms(rec.Platforms)}
					components.UserForm(s.pageProps(w, r), "edit", &u, s.platformChoices()).Render(r.Context(), w)
					return
				}
			}
			components.UserForm(s.pageProps(w, r), "create", nil, s.platformChoices()).Render(r.Context(), w)
			return
		}
		// Default: return full users panel
		s.renderUsersPanelHTML(w, r)
		return
	}

	users := auth.GetCachedUsers()
	result := make([]userResponse, 0, len(users))
	for username, rec := range users {
		result = append(result, userResponse{
			Username:  username,
			Role:      rec.Role,
			Platforms: s.effectivePlatforms(rec.Platforms),
		})
	}
	writeJSON(w, 200, map[string]any{"users": result})
}

func (s *Server) renderUsersPanelHTML(w http.ResponseWriter, r *http.Request) {
	users := auth.GetCachedUsers()
	display := make([]components.UserDisplay, 0, len(users))
	for username, rec := range users {
		display = append(display, components.UserDisplay{
			Username:  username,
			Role:      rec.Role,
			Platforms: s.effectivePlatforms(rec.Platforms),
		})
	}
	components.UsersPanel(s.pageProps(w, r), display).Render(r.Context(), w)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	isHTMX := r.Header.Get("HX-Request") != ""

	var username, password string
	var platforms []string

	// HTMX form: dispatch to create or update based on _method hidden field
	if isHTMX && r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		r.ParseForm()
		method := r.FormValue("_method")
		if method == "PUT" {
			editUser := r.FormValue("_edit_user")
			if editUser != "" {
				s.doUpdateUser(w, r, editUser, r.FormValue("password"), r.Form["platforms"], isHTMX)
				return
			}
		}
		username = strings.TrimSpace(r.FormValue("username"))
		password = strings.TrimSpace(r.FormValue("password"))
		platforms = r.Form["platforms"]
	} else {
		var body struct {
			Username  string   `json:"username"`
			Password  string   `json:"password"`
			Platforms []string `json:"platforms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
			return
		}
		username = body.Username
		password = body.Password
		platforms = body.Platforms
	}

	if username == "" || password == "" {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(422)
			fmt.Fprint(w, `<span class="status-message error">Username and password required.</span>`)
			return
		}
		writeJSON(w, 400, map[string]any{"error": "username and password required"})
		return
	}
	platforms, platformErr := s.normalizeRequestedPlatforms(platforms)
	if platformErr != nil {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(422)
			fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(platformErr.Error()))
			return
		}
		writeJSON(w, 422, map[string]any{"error": platformErr.Error()})
		return
	}

	auth.LockUsers()
	defer auth.UnlockUsers()

	users, err := auth.LoadUsers(s.cfg.AuthUsersPath)
	if err != nil {
		slog.Error("LoadUsers", "err", err)
		writeJSON(w, 500, map[string]any{"error": "load error"})
		return
	}
	if _, exists := users[username]; exists {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(409)
			fmt.Fprint(w, `<span class="status-message error">User already exists.</span>`)
			return
		}
		writeJSON(w, 409, map[string]any{"error": "user already exists"})
		return
	}

	users[username] = auth.UserRecord{
		Password:  auth.HashPassword(password),
		Role:      "user",
		Platforms: platforms,
	}
	if err := auth.SaveUsers(s.cfg.AuthUsersPath, users); err != nil {
		slog.Error("SaveUsers", "err", err)
		writeJSON(w, 500, map[string]any{"error": "save error"})
		return
	}
	auth.InvalidateCache()

	if isHTMX {
		s.renderUsersPanelHTML(w, r)
		return
	}
	writeJSON(w, 201, map[string]any{"success": true, "username": username})
}

func (s *Server) doUpdateUser(w http.ResponseWriter, r *http.Request, username, password string, platforms []string, isHTMX bool) {
	auth.LockUsers()
	defer auth.UnlockUsers()

	users, err := auth.LoadUsers(s.cfg.AuthUsersPath)
	if err != nil {
		slog.Error("LoadUsers", "err", err)
		writeJSON(w, 500, map[string]any{"error": "load error"})
		return
	}
	rec, exists := users[username]
	if !exists {
		writeJSON(w, 404, map[string]any{"error": "user not found"})
		return
	}

	password = strings.TrimSpace(password)
	if password != "" {
		rec.Password = auth.HashPassword(password)
	}
	if platforms != nil {
		normalized, platformErr := s.normalizeRequestedPlatforms(platforms)
		if platformErr != nil {
			if isHTMX {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(422)
				fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(platformErr.Error()))
				return
			}
			writeJSON(w, 422, map[string]any{"error": platformErr.Error()})
			return
		}
		platforms = normalized
		rec.Platforms = platforms
	}
	users[username] = rec

	if err := auth.SaveUsers(s.cfg.AuthUsersPath, users); err != nil {
		slog.Error("SaveUsers", "err", err)
		writeJSON(w, 500, map[string]any{"error": "save error"})
		return
	}
	auth.InvalidateCache()

	if isHTMX {
		s.renderUsersPanelHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	username := r.PathValue("username")

	var body struct {
		Password  string   `json:"password"`
		Platforms []string `json:"platforms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}

	s.doUpdateUser(w, r, username, body.Password, body.Platforms, r.Header.Get("HX-Request") != "")
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	username := r.PathValue("username")
	if username == "admin" {
		writeJSON(w, 403, map[string]any{"error": "cannot delete admin user"})
		return
	}

	auth.LockUsers()
	defer auth.UnlockUsers()

	users, err := auth.LoadUsers(s.cfg.AuthUsersPath)
	if err != nil {
		slog.Error("LoadUsers", "err", err)
		writeJSON(w, 500, map[string]any{"error": "load error"})
		return
	}
	if _, exists := users[username]; !exists {
		writeJSON(w, 404, map[string]any{"error": "user not found"})
		return
	}

	delete(users, username)
	if err := auth.SaveUsers(s.cfg.AuthUsersPath, users); err != nil {
		slog.Error("SaveUsers", "err", err)
		writeJSON(w, 500, map[string]any{"error": "save error"})
		return
	}
	auth.InvalidateCache()

	if r.Header.Get("HX-Request") != "" {
		s.renderUsersPanelHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
			return
		}
		rawURL = body.URL
		rawPlatform = body.Platform
	}

	if rawURL == "" {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<span class="status-message error">Paste a URL first.</span>`)
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
			fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(msg))
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
			fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(err.Error()))
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
						fmt.Fprint(w, `<span class="status-message error">Database error.</span>`)
						return
					}
					writeJSON(w, 500, map[string]any{"error": "db error"})
					return
				}
				created = false
			} else {
				if isHTMX {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					fmt.Fprint(w, `<span class="status-message error">Already subscribed.</span>`)
					return
				}
				writeJSON(w, 409, map[string]any{"error": "already subscribed", "channel_id": ch.ChannelID})
				return
			}
		} else {
			slog.Error("AddChannel", "channel", ch.ChannelID, "err", err)
			if isHTMX {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<span class="status-message error">Database error.</span>`)
				return
			}
			writeJSON(w, 500, map[string]any{"error": "db error"})
			return
		}
	}

	s.workers.RequestAvatar(ch.ChannelID)
	s.workers.Emit("system", fmt.Sprintf("Subscribed: %s (%s)", ch.Name, ch.Platform), "done")

	valueJSON := fmt.Sprintf(`{"channel_id":%q,"platform":%q}`, ch.ChannelID, ch.Platform)
	if err := s.db.RecordSyncChange("subscribe", ch.ChannelID, valueJSON); err != nil {
		slog.Warn("RecordSyncChange subscribe", "channel", ch.ChannelID, "err", err)
	}

	if isHTMX {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"followChanged":{"channelId":%q}}`, ch.ChannelID))
		// Modal form: show success + close modal after brief delay
		if r.URL.Query().Get("handle") == "" {
			msg := template.HTMLEscapeString(ch.Name) + " added."
			fmt.Fprintf(w, `<span class="status-message success">%s</span><script>setTimeout(function(){var m=document.getElementById('add-sub-modal');if(m){m.classList.add('hidden');m.setAttribute('aria-hidden','true');document.body.style.overflow=''}},900)</script>`, msg)
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
	userID := ""
	if user := userFromContext(r.Context()); user != nil {
		userID = user.Username
	}
	videos, err := s.db.PurgeUnfollowedChannelContent(channelID, userID)
	if err != nil {
		slog.Error("PurgeUnfollowedChannelContent", "channel", channelID, "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	for _, v := range videos {
		deleteVideoFiles(s.cfg.DataDir, v)
	}

	s.workers.Emit("system", fmt.Sprintf("Unsubscribed: %s", channelID), "info")

	valueJSON := fmt.Sprintf(`{"channel_id":%q}`, channelID)
	if err := s.db.RecordSyncChange("unsubscribe", channelID, valueJSON); err != nil {
		slog.Warn("RecordSyncChange unsubscribe", "channel", channelID, "err", err)
	}

	if r.Header.Get("HX-Request") != "" {
		// Return empty — hx-swap="outerHTML" removes the channel item
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "deleted_files": len(videos)})
}

// ── Config export / import ────────────────────────────────────────────────────

func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	userID := ""
	if user := userFromContext(r.Context()); user != nil {
		userID = user.Username
	}
	cfg, err := s.db.ExportConfig(userID)
	if err != nil {
		slog.Error("ExportConfig", "err", err)
		writeJSON(w, 500, map[string]any{"error": "export error"})
		return
	}

	if dir := s.configuredExportDir(); dir != "" {
		path, err := writeExportFile(dir, "igloo-config", ".json", func(dst io.Writer) error {
			return writeConfigExportJSON(dst, cfg)
		})
		if err != nil {
			slog.Error("ExportConfig save", "dir", dir, "err", err)
			writeJSON(w, 500, map[string]any{"error": "export save error"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success": true,
			"saved":   true,
			"path":    path,
		})
		return
	}

	// Config export is a downloadable JSON document, not an API response —
	// envelope fields would pollute the archived file. apiPath() excludes it.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="igloo-config-%s.json"`,
			time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(200)
	if err := writeConfigExportJSON(w, cfg); err != nil {
		slog.Error("ExportConfig write", "err", err)
	}
}

func (s *Server) handleConfigExportFull(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	userID := ""
	if user := userFromContext(r.Context()); user != nil {
		userID = user.Username
	}
	cfg, err := s.db.ExportFullData(userID)
	if err != nil {
		slog.Error("ExportFullData", "err", err)
		writeJSON(w, 500, map[string]any{"error": "export error"})
		return
	}

	mediaFiles := s.collectFullExportBookmarkMedia(cfg.Bookmarks)
	mediaFiles = append(mediaFiles, s.collectFullExportAvatarMedia()...)
	runtimeFiles := s.collectFullExportRuntimeConfigFiles()
	runtimeManifest := s.fullExportRuntimeManifest()

	if dir := s.configuredExportDir(); dir != "" {
		path, err := writeExportFile(dir, "igloo-full", ".zip", func(dst io.Writer) error {
			return writeFullExportZip(dst, cfg, mediaFiles, runtimeFiles, runtimeManifest)
		})
		if err != nil {
			slog.Error("ExportFullData save", "dir", dir, "err", err)
			writeJSON(w, 500, map[string]any{"error": "export save error"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success": true,
			"saved":   true,
			"path":    path,
		})
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="igloo-full-%s.zip"`,
			time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(200)
	if err := writeFullExportZip(w, cfg, mediaFiles, runtimeFiles, runtimeManifest); err != nil {
		slog.Error("ExportFullData zip write", "err", err)
	}
}

func writeConfigExportJSON(w io.Writer, cfg db.ConfigExport) error {
	return json.NewEncoder(w).Encode(cfg)
}

func writeFullExportZip(w io.Writer, cfg db.ConfigExport, mediaFiles []fullExportMediaFile, runtimeFiles []fullExportRuntimeFile, runtimeManifest fullimport.RuntimeManifest) error {
	zw := zip.NewWriter(w)
	if err := writeFullExportJSON(zw, cfg); err != nil {
		zw.Close()
		return err
	}
	if err := writeFullExportRuntimeManifest(zw, runtimeManifest); err != nil {
		zw.Close()
		return err
	}
	for _, file := range runtimeFiles {
		if err := writeFullExportRuntimeConfigFile(zw, file); err != nil {
			slog.Warn("ExportFullData runtime config file skipped", "path", file.SourcePath, "err", err)
		}
	}
	for _, file := range mediaFiles {
		if err := writeFullExportMediaFile(zw, file); err != nil {
			slog.Warn("ExportFullData media file skipped", "path", file.SourcePath, "err", err)
		}
	}
	return zw.Close()
}

func (s *Server) configuredExportDir() string {
	dir, _ := s.db.GetSetting("backup_dir", "")
	return strings.TrimSpace(dir)
}

func writeExportFile(dir, prefix, ext string, write func(io.Writer) error) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create export dir: %w", err)
	}
	stamp := time.Now().UTC().Format("2006-01-02-150405")
	name := fmt.Sprintf("%s-%s%s", prefix, stamp, ext)
	tmp, err := os.CreateTemp(dir, "."+prefix+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp export: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			tmp.Close()
		}
		os.Remove(tmpPath)
	}()
	if err := write(tmp); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return "", err
	}
	closed = true
	finalPath := filepath.Join(dir, name)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("rename export: %w", err)
	}
	return finalPath, nil
}

type fullExportMediaFile struct {
	SourcePath  string
	ArchivePath string
}

type fullExportRuntimeFile struct {
	SourcePath  string
	ArchivePath string
}

func writeFullExportJSON(zw *zip.Writer, cfg db.ConfigExport) error {
	f, err := zw.Create("export.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func writeFullExportRuntimeManifest(zw *zip.Writer, manifest fullimport.RuntimeManifest) error {
	f, err := zw.Create("runtime.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
}

func writeFullExportRuntimeConfigFile(zw *zip.Writer, file fullExportRuntimeFile) error {
	src, err := os.Open(file.SourcePath)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("runtime config path is not a regular file")
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = file.ArchivePath
	hdr.Method = zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

func writeFullExportMediaFile(zw *zip.Writer, file fullExportMediaFile) error {
	src, err := os.Open(file.SourcePath)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("media path is directory")
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = file.ArchivePath
	hdr.Method = zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

func (s *Server) fullExportRuntimeManifest() fullimport.RuntimeManifest {
	if s == nil || s.cfg == nil {
		return fullimport.RuntimeManifest{Version: 1}
	}
	return fullimport.RuntimeManifest{
		Version:   1,
		DataDir:   s.cfg.DataDir,
		ConfigDir: s.cfg.ConfDir,
		RepoDir:   s.repoDirForRuntimeExport(),
	}
}

func (s *Server) repoDirForRuntimeExport() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	if strings.TrimSpace(s.cfg.RepoDir) != "" {
		return s.cfg.RepoDir
	}
	if strings.TrimSpace(s.cfg.StaticDir) != "" {
		return filepath.Dir(s.cfg.StaticDir)
	}
	return ""
}

func (s *Server) collectFullExportRuntimeConfigFiles() []fullExportRuntimeFile {
	if s == nil || s.cfg == nil || strings.TrimSpace(s.cfg.ConfDir) == "" {
		return nil
	}
	root := filepath.Clean(s.cfg.ConfDir)
	var files []fullExportRuntimeFile
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			slog.Warn("ExportFullData runtime config path skipped", "path", path, "err", err)
			return nil
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			slog.Warn("ExportFullData runtime config rel path skipped", "path", path, "err", err)
			return nil
		}
		if skipFullExportRuntimeConfigPath(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			slog.Warn("ExportFullData runtime config stat skipped", "path", path, "err", err)
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, fullExportRuntimeFile{
			SourcePath:  path,
			ArchivePath: filepath.ToSlash(filepath.Join("config", rel)),
		})
		return nil
	}); err != nil {
		slog.Warn("ExportFullData runtime config walk failed", "dir", root, "err", err)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ArchivePath < files[j].ArchivePath
	})
	return files
}

func skipFullExportRuntimeConfigPath(rel string) bool {
	rel = filepath.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	base := filepath.Base(rel)
	if base == "" || strings.HasPrefix(base, ".") {
		return true
	}
	for _, prefix := range []string{".auth_users_", ".config_", ".upload_", ".import-media-", ".import-config-"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

func (s *Server) collectFullExportBookmarkMedia(bookmarks []db.BookmarkExport) []fullExportMediaFile {
	var files []fullExportMediaFile
	seenPath := make(map[string]bool)
	countByBookmark := make(map[string]int)
	add := func(bookmarkID, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.cfg.DataDir, path)
		}
		cleanPath := filepath.Clean(path)
		info, err := os.Stat(cleanPath)
		if err != nil || info.IsDir() || seenPath[cleanPath] {
			return
		}
		seenPath[cleanPath] = true
		safeID := sanitizeFilename(bookmarkID)
		if safeID == "" {
			safeID = "item"
		}
		idx := countByBookmark[safeID]
		countByBookmark[safeID] = idx + 1
		ext := strings.ToLower(filepath.Ext(cleanPath))
		if ext == "" {
			ext = ".bin"
		}
		files = append(files, fullExportMediaFile{
			SourcePath:  cleanPath,
			ArchivePath: filepath.ToSlash(filepath.Join("media", "bookmarks", safeID, fmt.Sprintf("%03d%s", idx, ext))),
		})
	}

	for _, bm := range bookmarks {
		for _, path := range s.collectSlides(bm.VideoID) {
			add(bm.VideoID, path)
		}
		if path := s.androidSyncAudioPath(bm.VideoID); path != "" {
			add(bm.VideoID, path)
		}
		items, err := s.db.GetFeedItemsForTweetIDs([]string{bm.VideoID})
		if err == nil {
			if item, ok := items[bm.VideoID]; ok && item.QuoteTweetID != "" {
				for _, path := range s.collectSlides(item.QuoteTweetID) {
					add(bm.VideoID, path)
				}
				if path := s.androidSyncAudioPath(item.QuoteTweetID); path != "" {
					add(bm.VideoID, path)
				}
			}
		}
	}
	return files
}

func (s *Server) collectFullExportAvatarMedia() []fullExportMediaFile {
	if s == nil || s.cfg == nil || strings.TrimSpace(s.cfg.DataDir) == "" {
		return nil
	}
	root := filepath.Join(s.cfg.DataDir, "thumbnails", "avatars")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	files := make([]fullExportMediaFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !fullExportAvatarMediaName(name) {
			continue
		}
		sourcePath := filepath.Join(root, name)
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, fullExportMediaFile{
			SourcePath:  sourcePath,
			ArchivePath: filepath.ToSlash(filepath.Join("media", "avatars", name)),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ArchivePath < files[j].ArchivePath
	})
	return files
}

func fullExportAvatarMediaName(name string) bool {
	if strings.TrimSpace(name) == "" || name != filepath.Base(name) {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return true
	default:
		return false
	}
}

func (s *Server) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	isHTMX := r.Header.Get("HX-Request") != ""
	if !requireAdmin(w, r) {
		return
	}

	importErr := func(code int, msg string) {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(code)
			fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(msg))
			return
		}
		writeJSON(w, code, map[string]any{"error": msg})
	}
	importOK := func(msg string) {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<span class="status-message success">%s</span><script>setTimeout(function(){window.location.reload()},2000)</script>`, template.HTMLEscapeString(msg))
			return
		}
	}

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		importErr(400, "multipart parse error")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		importErr(400, "missing file")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		importErr(500, "read error")
		return
	}

	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		if err := restore.StageTarball(bytes.NewReader(data), s.cfg.DataDir); err != nil {
			slog.Error("StageTarball", "err", err)
			importErr(400, "tarball error: "+err.Error())
			return
		}
		slog.Info("restore: staged, exiting for systemd restart")
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<span class="status-message success">Restore staged. Igloo is restarting…</span><script>setTimeout(function(){window.location.reload()},12000)</script>`)
		} else {
			writeJSON(w, 200, map[string]any{"success": true, "format": "tarball", "restart": true})
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() {
			time.Sleep(500 * time.Millisecond)
			os.Exit(1)
		}()
		return
	}

	if fullimport.IsZipPayload(data) {
		userID := ""
		if user := userFromContext(r.Context()); user != nil {
			userID = user.Username
		}
		replace := r.FormValue("mode") == "replace"
		result, restoredMedia, restoredConfig, err := fullimport.ImportFullExportZip(s.db, s.cfg.DataDir, s.cfg.ConfDir, s.repoDirForRuntimeExport(), data, userID, replace)
		if err != nil {
			slog.Error("ImportFullExportZip", "err", err)
			importErr(400, "zip import error: "+err.Error())
			return
		}
		if restoredConfig > 0 {
			auth.InvalidateCache()
		}
		var parts []string
		if result.AddedChannels > 0 {
			parts = append(parts, fmt.Sprintf("%d subscriptions", result.AddedChannels))
		}
		if result.AddedBookmarks > 0 {
			parts = append(parts, fmt.Sprintf("%d bookmarks", result.AddedBookmarks))
		}
		if result.AddedCategories > 0 {
			parts = append(parts, fmt.Sprintf("%d categories", result.AddedCategories))
		}
		if restoredMedia > 0 {
			parts = append(parts, fmt.Sprintf("%d media files", restoredMedia))
		}
		if restoredConfig > 0 {
			parts = append(parts, fmt.Sprintf("%d config files", restoredConfig))
		}
		summary := "Import complete"
		if len(parts) > 0 {
			summary = "Imported: " + strings.Join(parts, ", ")
		}
		importOK(summary)
		if !isHTMX {
			writeJSON(w, 200, map[string]any{
				"success": true, "format": "full_export_zip",
				"added_channels": result.AddedChannels, "added_bookmarks": result.AddedBookmarks,
				"added_categories": result.AddedCategories, "updated_settings": result.UpdatedSettings,
				"restored_media": restoredMedia, "restored_config_files": restoredConfig, "skipped": result.Skipped,
			})
		}
		return
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		importErr(400, "empty file")
		return
	}

	userID := ""
	if user := userFromContext(r.Context()); user != nil {
		userID = user.Username
	}
	replace := r.FormValue("mode") == "replace"

	switch trimmed[0] {
	case '{':
		var cfgExport db.ConfigExport
		if err := json.Unmarshal(trimmed, &cfgExport); err != nil {
			importErr(400, "invalid config JSON")
			return
		}
		result, err := s.db.ImportConfig(cfgExport, userID, replace)
		if err != nil {
			slog.Error("ImportConfig", "err", err)
			importErr(500, "import error")
			return
		}
		var parts []string
		if result.AddedChannels > 0 {
			parts = append(parts, fmt.Sprintf("%d subscriptions", result.AddedChannels))
		}
		if result.AddedBookmarks > 0 {
			parts = append(parts, fmt.Sprintf("%d bookmarks", result.AddedBookmarks))
		}
		if result.AddedCategories > 0 {
			parts = append(parts, fmt.Sprintf("%d categories", result.AddedCategories))
		}
		if result.UpdatedSettings > 0 {
			parts = append(parts, fmt.Sprintf("%d settings", result.UpdatedSettings))
		}
		summary := "Import complete"
		if len(parts) > 0 {
			summary = "Imported: " + strings.Join(parts, ", ")
		}
		importOK(summary)
		if !isHTMX {
			writeJSON(w, 200, map[string]any{
				"success": true, "format": "full_config",
				"added_channels": result.AddedChannels, "added_bookmarks": result.AddedBookmarks,
				"added_categories": result.AddedCategories, "updated_settings": result.UpdatedSettings,
				"skipped": result.Skipped,
			})
		}

	case '[':
		var urls []string
		if err := json.Unmarshal(trimmed, &urls); err != nil {
			importErr(400, "invalid subscription array JSON")
			return
		}
		added, skipped := s.importSubscriptionList(r.Context(), urls)
		importOK(fmt.Sprintf("Imported %d channels (%d skipped)", added, skipped))
		if !isHTMX {
			writeJSON(w, 200, map[string]any{"success": true, "format": "subscription_list", "added_channels": added, "skipped": skipped})
		}

	case '<':
		channels := parseOPML(trimmed)
		added, skipped := s.importChannelList(r.Context(), channels)
		importOK(fmt.Sprintf("Imported %d channels (%d skipped)", added, skipped))
		if !isHTMX {
			writeJSON(w, 200, map[string]any{"success": true, "format": "opml", "added_channels": added, "skipped": skipped})
		}

	default:
		importErr(400, "unrecognized format")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// atomicWrite writes data to dest using a temp-file + rename pattern.
func atomicWrite(dest string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".upload_*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dest)
}

// deleteVideoFiles removes video file and sibling thumbnails from disk.
func deleteVideoFiles(dataDir string, v model.Video) {
	removePath := func(p string) {
		if p == "" {
			return
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dataDir, p)
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			slog.Warn("delete video file", "path", p, "err", err)
		}
	}

	removePath(v.FilePath)
	removePath(v.ThumbnailPath)

	// Remove sibling thumbnail files (same base name, any extension)
	if v.FilePath != "" {
		absPath := v.FilePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(dataDir, absPath)
		}
		base := strings.TrimSuffix(absPath, filepath.Ext(absPath))
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".avif"} {
			sibling := base + ext
			if sibling != absPath {
				if err := os.Remove(sibling); err != nil && !os.IsNotExist(err) {
					slog.Warn("delete sibling thumbnail", "path", sibling, "err", err)
				}
			}
		}
	}
}

// importSubscriptionList resolves and adds a list of URLs/handles.
// Returns (added, skipped) counts.
func (s *Server) importSubscriptionList(ctx context.Context, urls []string) (int, int) {
	added, skipped := 0, 0
	for _, rawURL := range urls {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			skipped++
			continue
		}
		platform := subscribe.DetectPlatform(rawURL, "")
		if !s.platformEnabled(platform) {
			slog.Warn("importSubscriptionList disabled platform", "url", rawURL, "platform", platform)
			skipped++
			continue
		}
		subCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ch, err := subscribe.ResolveChannel(subCtx, rawURL, platform, s.workers.Downloader())
		cancel()
		if err != nil {
			slog.Warn("importSubscriptionList resolve", "url", rawURL, "err", err)
			skipped++
			continue
		}
		if err := s.db.AddChannel(ch); err != nil {
			skipped++
			continue
		}
		s.workers.RequestAvatar(ch.ChannelID)
		valueJSON := fmt.Sprintf(`{"channel_id":%q,"platform":%q}`, ch.ChannelID, ch.Platform)
		_ = s.db.RecordSyncChange("subscribe", ch.ChannelID, valueJSON)
		added++
	}
	return added, skipped
}

// importChannelList adds pre-resolved channels.
// Returns (added, skipped) counts.
func (s *Server) importChannelList(ctx context.Context, channels []model.Channel) (int, int) {
	added, skipped := 0, 0
	for _, ch := range channels {
		if !s.platformEnabled(ch.Platform) {
			skipped++
			continue
		}
		if err := s.db.AddChannel(ch); err != nil {
			skipped++
			continue
		}
		s.workers.RequestAvatar(ch.ChannelID)
		valueJSON := fmt.Sprintf(`{"channel_id":%q,"platform":%q}`, ch.ChannelID, ch.Platform)
		_ = s.db.RecordSyncChange("subscribe", ch.ChannelID, valueJSON)
		added++
	}
	return added, skipped
}

// parseOPML extracts YouTube channels from an OPML byte slice.
// Handles both attribute orderings: xmlUrl before text, and text before xmlUrl.
func parseOPML(data []byte) []model.Channel {
	// Match xmlUrl="...youtube..." anywhere before or after text="..."
	reA := regexp.MustCompile(`xmlUrl="([^"]*youtube[^"]*)"[^>]*text="([^"]*)"`)
	reB := regexp.MustCompile(`text="([^"]*)"[^>]*xmlUrl="([^"]*youtube[^"]*)"`)
	reChannelID := regexp.MustCompile(`channel_id=([A-Za-z0-9_-]+)`)

	seen := make(map[string]bool)
	var channels []model.Channel

	addChannel := func(feedURL, name string) {
		m := reChannelID.FindStringSubmatch(feedURL)
		if len(m) < 2 {
			return
		}
		rawID := m[1]
		channelID := "youtube_" + strings.TrimPrefix(rawID, "youtube_")
		if seen[channelID] {
			return
		}
		seen[channelID] = true
		channels = append(channels, model.Channel{
			ChannelID:    channelID,
			SourceID:     strings.TrimPrefix(channelID, "youtube_"),
			Name:         name,
			URL:          "https://www.youtube.com/channel/" + strings.TrimPrefix(channelID, "youtube_"),
			Platform:     "youtube",
			IsSubscribed: true,
		})
	}

	for _, m := range reA.FindAllSubmatch(data, -1) {
		addChannel(string(m[1]), string(m[2]))
	}
	for _, m := range reB.FindAllSubmatch(data, -1) {
		addChannel(string(m[2]), string(m[1]))
	}

	return channels
}

// ── Change Credentials ──────────────────────────────────────────────────────

func (s *Server) handleChangeCredentials(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	isHTMX := r.Header.Get("HX-Request") != ""

	if user == nil {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<span class="status-msg error">Not authenticated</span>`)
		} else {
			writeJSON(w, 401, map[string]any{"error": "not authenticated"})
		}
		return
	}

	var currentPassword, newUsername, newPassword, confirmPassword string
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		r.ParseForm()
		currentPassword = r.FormValue("current_password")
		newUsername = r.FormValue("new_username")
		newPassword = r.FormValue("new_password")
		confirmPassword = r.FormValue("new_password_confirm")
	} else {
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewUsername     string `json:"new_username"`
			NewPassword     string `json:"new_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid request"})
			return
		}
		currentPassword = body.CurrentPassword
		newUsername = body.NewUsername
		newPassword = body.NewPassword
	}

	credErr := func(msg string) {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<span class="status-msg error">%s</span>`, template.HTMLEscapeString(msg))
		} else {
			writeJSON(w, 400, map[string]any{"error": msg})
		}
	}

	if currentPassword == "" {
		credErr("Current password is required")
		return
	}
	if newPassword != "" && confirmPassword != "" && newPassword != confirmPassword {
		credErr("Passwords do not match")
		return
	}

	auth.LockUsers()
	defer auth.UnlockUsers()

	users, err := auth.LoadUsers(s.cfg.AuthUsersPath)
	if err != nil {
		credErr("Could not load users")
		return
	}

	rec, exists := users[user.Username]
	if !exists {
		credErr("User not found")
		return
	}

	if !auth.VerifyPassword(currentPassword, rec.Password) {
		credErr("Current password incorrect")
		return
	}

	usernameChanged := false
	finalUsername := user.Username

	if newUsername != "" && newUsername != user.Username {
		if len(newUsername) < 3 {
			credErr("Username must be at least 3 characters")
			return
		}
		if _, taken := users[newUsername]; taken {
			credErr("Username already taken")
			return
		}
		delete(users, user.Username)
		users[newUsername] = rec
		finalUsername = newUsername
		usernameChanged = true
	}

	if newPassword != "" {
		if len(newPassword) < 6 {
			credErr("Password must be at least 6 characters")
			return
		}
		updated := users[finalUsername]
		updated.Password = auth.HashPassword(newPassword)
		users[finalUsername] = updated
	}

	if err := auth.SaveUsers(s.cfg.AuthUsersPath, users); err != nil {
		credErr("Could not save users")
		return
	}
	auth.InvalidateCache()

	if isHTMX {
		w.Header().Set("Content-Type", "text/html")
		msg := "Credentials updated!"
		if usernameChanged {
			msg += " Reloading..."
			w.Header().Set("HX-Refresh", "true")
		}
		fmt.Fprintf(w, `<span class="status-msg success">%s</span>`, template.HTMLEscapeString(msg))
	} else {
		writeJSON(w, 200, map[string]any{
			"success":          true,
			"message":          "Credentials updated successfully",
			"username_changed": usernameChanged,
			"new_username":     finalUsername,
		})
	}
}
