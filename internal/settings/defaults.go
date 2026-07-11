// Package settings holds the canonical default values for rows in the
// `settings` DB table. Both the admin UI handler and backend workers read
// from Defaults so a given key has one authoritative default across the app.
package settings

import (
	"fmt"
)

// DearrowModeValues is the allow-list for the dearrow_mode setting.
var DearrowModeValues = []string{"off", "default", "casual"}

// NormalizeDearrowMode returns one of off|default|casual. Unknown or empty
// values are coerced to "off" (the conservative opt-in default).
func NormalizeDearrowMode(v string) string {
	switch v {
	case "default", "casual":
		return v
	default:
		return "off"
	}
}

const (
	TranslateBackendNone         = "none"
	TranslateBackendKagiCLI      = "kagi_cli"
	TranslateBackendGoogle       = "google"
	TranslateBackendDeepL        = "deepl"
	TranslateBackendOpenAICompat = "openai_compat"

	TranslateAutoOff        = "off"
	TranslateAutoLazy       = "lazy"
	TranslateAutoBackground = "background"
)

// TranslateBackendValues is the allow-list for the translate_backend setting.
var TranslateBackendValues = []string{
	TranslateBackendNone,
	TranslateBackendKagiCLI,
	TranslateBackendGoogle,
	TranslateBackendDeepL,
	TranslateBackendOpenAICompat,
}

// NormalizeTranslateBackend returns a supported translation backend. Unknown
// legacy values are coerced to none so public installs do not assume Kagi.
func NormalizeTranslateBackend(v string) string {
	switch v {
	case TranslateBackendKagiCLI, TranslateBackendGoogle, TranslateBackendDeepL, TranslateBackendOpenAICompat:
		return v
	default:
		return TranslateBackendNone
	}
}

// TranslateAutoModeValues is the allow-list for the translate_auto_mode setting.
var TranslateAutoModeValues = []string{
	TranslateAutoOff,
	TranslateAutoLazy,
	TranslateAutoBackground,
}

// NormalizeTranslateAutoMode returns a supported auto-translate mode.
func NormalizeTranslateAutoMode(v string) string {
	switch v {
	case TranslateAutoOff, TranslateAutoBackground:
		return v
	default:
		return TranslateAutoLazy
	}
}

// ClampTranslateLookahead bounds the lazy auto-translate lookahead. It is a
// card-count hint; the browser maps it to a generous pixel margin.
func ClampTranslateLookahead(n int) int {
	if n < 1 {
		return 1
	}
	if n > 100 {
		return 100
	}
	return n
}

func ClampBackupKeepCount(n int) int {
	if n < 1 {
		return 1
	}
	if n > 5 {
		return 5
	}
	return n
}

// SponsorBlockCategoriesDefault is the canonical default category policy shared by
// server-side settings, rendered pages, and UI fallbacks.
const SponsorBlockCategoriesDefault = "sponsor:silent,selfpromo:silent,interaction:silent,intro:ask,outro:ask,preview:ask,filler:ask,music_offtopic:ask"

// SponsorBlockDefaultAction returns the fallback mode for an individual category.
func SponsorBlockDefaultAction(category string) string {
	switch category {
	case "sponsor", "selfpromo", "interaction":
		return "silent"
	case "intro", "outro", "preview", "filler", "music_offtopic":
		return "ask"
	default:
		return "ask"
	}
}

// Defaults mirrors the client-facing settings defaults. Values may be int,
// bool, or string. DB rows always store strings; callers that need a typed
// default should use the helpers below.
var Defaults = map[string]any{
	"quality":                          "best",
	"web_theme_id":                     "occult-umbral",
	"web_theme_accent":                 "#e6c27a",
	"web_custom_css":                   "",
	"share_embed_friendly_links":       false,
	"youtube_fetch_delay":              120,
	"youtube_max_videos":               12,
	"download_subtitles":               false,
	"tiktok_fetch_delay":               60,
	"shorts_max_videos":                20,
	"instagram_fetch_delay":            60,
	"instagram_max_videos":             20,
	"instagram_include_tagged_default": false,
	"moments_default_tab":              "all",
	"moments_include_reposts_default":  false,
	"stories_window_hours":             48,
	"media_only_default":               false,
	"media_download_limit_default":     20,
	"x_feed_fetch_delay":               10,
	"translate_target_lang":            "en",
	"translate_skip_langs":             "",
	"translate_backend":                TranslateBackendNone,
	"translate_auto_mode":              TranslateAutoLazy,
	"translate_auto_lookahead":         20,
	"translate_api_site":               "",
	"translate_api_key":                "",
	"translate_model":                  "",
	"ui_language":                      "en",
	"youtube_default_playback_speed":   "1",
	"archive_bookmarks":                false,
	"backup_enabled":                   false,
	"backup_dir":                       "",
	"backup_keep_count":                5,
	"sponsorblock_categories":          SponsorBlockCategoriesDefault,
	"starting_page":                    "feed",
	"dearrow_mode":                     "off",
	"algorithmic_feed_enabled":         false,
}

// WebStartingPages is the allow-list of web paths that handleIndex will
// redirect to. Keep in sync with the <option>s in prefsGeneralTab and the
// routes registered in server.go. Values here are the keys stored in the
// `starting_page` setting; handleIndex prepends "/" when redirecting.
var WebStartingPages = map[string]bool{
	"feed":      true,
	"videos":    true,
	"shorts":    true,
	"liked":     true,
	"bookmarks": true,
	"channels":  true,
}

// IntDefault returns the default integer value for key, or 0 if unset.
func IntDefault(key string) int {
	v, ok := Defaults[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case string:
		var i int
		_, _ = fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}
