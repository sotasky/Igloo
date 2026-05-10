package components

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func testStaticV(path string) string {
	return "/static/" + path + "?v=test123"
}

func newTestPageProps() PageProps {
	return PageProps{
		CSRFToken:           "test-csrf-token",
		UserRole:            "admin",
		Username:            "testuser",
		UserPlatforms:       []string{"youtube", "twitter", "tiktok"},
		PageTitle:           "Test Page",
		ActiveNav:           "videos",
		ShortcutConfig:      map[string]string{"feed.like": "l"},
		TranslateTargetLang: "en",
		TranslateSkipLangs:  "zh,ja",
		Language:            "en",
		SupportedLanguages:  []LanguageChoice{{Code: "en", Name: "English"}},
		Sidebar: model.SidebarContext{
			Groups: []model.ChannelGroup{
				{
					Title:   "Starred",
					GroupID: "starred",
					Channels: []model.Channel{
						{
							ChannelID: "youtube_test1",
							Name:      "Test Channel",
							Platform:  "youtube",
							Handle:    "test_handle",
							AvatarURL: "/api/media/avatar/youtube_test1",
							IsStarred: true,
						},
					},
				},
			},
		},
		StaticV: testStaticV,
	}
}

func renderToString(t *testing.T, c func() string) string {
	t.Helper()
	return c()
}

func renderBase(t *testing.T, p PageProps) string {
	t.Helper()
	var buf bytes.Buffer
	err := Base(p).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("Base render failed: %v", err)
	}
	return buf.String()
}

func TestBaseRendersStructure(t *testing.T) {
	p := newTestPageProps()
	html := renderBase(t, p)

	checks := []struct {
		name   string
		substr string
	}{
		{"doctype", "<!doctype html>"},
		{"csrf meta", `name="csrf-token" content="test-csrf-token"`},
		{"user-role meta", `name="user-role" content="admin"`},
		{"user-platforms meta", `name="user-platforms" content="youtube,twitter,tiktok"`},
		{"translate-target meta", `name="translate-target" content="en"`},
		{"translate-skip-langs meta", `name="translate-skip-langs" content="zh,ja"`},
		{"title", "<title>Test Page</title>"},
		{"favicon", `href="/static/favicon.svg?v=test123"`},
		{"fallback theme stylesheet", `href="/static/theme.css?v=test123"`},
		{"stylesheet", `href="/static/style.css?v=test123"`},
		{"generated theme stylesheet id", `id="igloo-theme-css"`},
		{"generated theme stylesheet href", `href="/api/theme.css"`},
		{"empty-actions style", "#page-title-actions:empty"},
		{"sidebar-overlay", `id="sidebar-overlay"`},
		{"sidebar", `class="sidebar"`},
		{"main-content", `id="main-content"`},
		{"sidebar-toggle", `id="sidebar-toggle"`},
		{"floating-header", `class="floating-header"`},
		{"channel-settings-popover", `id="channel-settings-popover"`},
		{"add-sub-modal", `id="add-sub-modal"`},
		{"prefs-modal", `id="prefs-modal"`},
		{"import-config-modal", `id="import-config-modal"`},
		{"logs-modal", `id="logs-modal"`},
		{"confirm-modal", `id="confirm-modal"`},
		{"search-overlay", `id="search-overlay"`},
		{"modal-container", `id="modal-container"`},
		{"i18n config", `window.IglooI18n`},
		{"shortcut config", `window._cfShortcutConfig`},
		{"web_theme.js", `js/web_theme.js?v=test123`},
		{"site_base.js", `js/site_base.js?v=test123`},
		{"sync_poller.js", `js/sync_poller.js?v=test123`},
		{"video_cards.js", `js/video_cards.js?v=test123`},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(strings.ToLower(html), strings.ToLower(c.substr)) {
				t.Errorf("expected %q in output", c.substr)
			}
		})
	}
}

func TestBaseEmbedsSharePreferenceConfig(t *testing.T) {
	p := newTestPageProps()
	html := renderBase(t, p)

	if !strings.Contains(html, `"shareEmbedFriendlyLinks":false`) {
		t.Fatalf("base config should default shareEmbedFriendlyLinks off:\n%s", html)
	}

	p.ShareEmbedFriendlyLinks = true
	html = renderBase(t, p)
	if !strings.Contains(html, `"shareEmbedFriendlyLinks":true`) {
		t.Fatalf("base config should expose enabled shareEmbedFriendlyLinks:\n%s", html)
	}
}

func TestPrefsBodyRendersAppearanceThemeControls(t *testing.T) {
	p := newTestPageProps()
	prefs := PrefsData{Settings: map[string]any{
		"web_theme_id":     "catppuccin-mocha",
		"web_theme_accent": "#f38ba8",
		"web_custom_css":   ".feed-card { border-color: hotpink; }",
	}}
	var buf bytes.Buffer
	if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	checks := []string{
		`data-prefs-tab="appearance"`,
		`name="web_theme_id"`,
		`value="catppuccin-mocha" selected`,
		`name="web_theme_accent"`,
		`type="color"`,
		`data-catppuccin-accent="mauve"`,
		`data-accent-hex="#cba6f7"`,
		`name="web_custom_css"`,
		`.feed-card { border-color: hotpink; }`,
	}
	for _, want := range checks {
		if !strings.Contains(html, want) {
			t.Fatalf("preferences body missing %q:\n%s", want, html)
		}
	}
}

func TestPrefsBodyHidesCatppuccinPillsForNonCatppuccinTheme(t *testing.T) {
	p := newTestPageProps()
	prefs := PrefsData{Settings: map[string]any{
		"web_theme_id":     "dracula",
		"web_theme_accent": "#bd93f9",
	}}
	var buf bytes.Buffer
	if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	checks := []string{
		`value="dracula" selected`,
		`data-catppuccin-accent-row style="display:none;"`,
	}
	for _, want := range checks {
		if !strings.Contains(html, want) {
			t.Fatalf("preferences body missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `data-catppuccin-accent="mauve"`) {
		t.Fatalf("non-Catppuccin theme should not render Catppuccin accent pills:\n%s", html)
	}
}

func TestPrefsBodyRendersInstagramTaggedToggleInRightColumn(t *testing.T) {
	p := newTestPageProps()
	prefs := PrefsData{Settings: map[string]any{
		"instagram_include_tagged_default": true,
	}}
	var buf bytes.Buffer
	if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, `name="instagram_include_tagged_default" value="true" checked`) {
		t.Fatalf("preferences body should render checked Instagram tagged toggle:\n%s", html)
	}
	translationIdx := strings.Index(html, `name="translate_auto_mode"`)
	instagramIdx := strings.Index(html, `name="instagram_fetch_delay"`)
	if translationIdx < 0 || instagramIdx < 0 {
		t.Fatalf("preferences body missing translation or Instagram controls")
	}
	if instagramIdx < translationIdx {
		t.Fatalf("Instagram section should render in the right column after translation controls")
	}
}

func TestPrefsBodyGeneralTabGroupsEmbedsAndMovesBackupsLeft(t *testing.T) {
	p := newTestPageProps()
	prefs := PrefsData{Settings: map[string]any{
		"share_embed_friendly_links": true,
		"backup_enabled":             true,
	}}
	var buf bytes.Buffer
	if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	embedsIdx := strings.Index(html, `Embeds`)
	shareIdx := strings.Index(html, `Use embed-friendly sites for sharing links`)
	if embedsIdx < 0 || shareIdx < 0 {
		t.Fatalf("preferences body should render the Embeds section with plural copy:\n%s", html)
	}
	if shareIdx < embedsIdx {
		t.Fatalf("share embed toggle should render under the Embeds header")
	}
	if strings.Contains(html, `Use embed-friendly site for sharing links`) {
		t.Fatalf("preferences body should not render the old singular embed copy")
	}

	backupIdx := strings.Index(html, `name="backup_enabled"`)
	archiveIdx := strings.Index(html, `name="archive_bookmarks"`)
	if backupIdx < 0 || archiveIdx < 0 {
		t.Fatalf("preferences body missing backup or bookmark controls")
	}
	if archiveIdx < backupIdx {
		t.Fatalf("backup controls should render in the left column before bookmark controls")
	}
}

func TestBookmarkCategoryPathsPanelUsesTallerScrollArea(t *testing.T) {
	p := newTestPageProps()
	var buf bytes.Buffer
	if err := BookmarkCategoryPathsPanel(p, nil).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, `max-height: 420px`) {
		t.Fatalf("bookmark category list should allow 1.5x more height before scrolling:\n%s", html)
	}
}

func TestCookieRowsPanelRendersDisableActionAndCompactBrowserSelect(t *testing.T) {
	p := newTestPageProps()
	rows := []CookieRowData{{
		Platform: "twitter",
		Name:     "X / Twitter",
		Exists:   true,
		Enabled:  true,
	}}
	var buf bytes.Buffer
	if err := CookieRowsPanel(p, rows).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	for _, want := range []string{
		`class="input cookie-browser-select"`,
		`hx-post="/api/cookies/twitter/toggle"`,
		`>Disable<`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("cookie rows panel missing %q:\n%s", want, html)
		}
	}
}

func TestCookieBrowserSelectSizingOverridesGlobalInputWidth(t *testing.T) {
	css, err := os.ReadFile("../../static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	text := string(css)
	globalInputIdx := strings.Index(text, "\n.input {")
	cookieSelectIdx := strings.LastIndex(text, "select.input.cookie-browser-select")
	if globalInputIdx < 0 || cookieSelectIdx < 0 {
		t.Fatalf("missing global input or cookie select sizing rule")
	}
	if cookieSelectIdx < globalInputIdx {
		t.Fatalf("cookie select sizing rule should come after the global .input width rule")
	}
	rule := text[cookieSelectIdx:]
	if end := strings.Index(rule, "}"); end >= 0 {
		rule = rule[:end]
	}
	if !strings.Contains(rule, "width: auto;") || !strings.Contains(rule, "height: 34px;") {
		t.Fatalf("cookie select sizing rule should keep dropdown button-sized, got:\n%s", rule)
	}
	themedIdx := strings.LastIndex(text, ".cookie-row-actions .themed-select {")
	if themedIdx < 0 {
		t.Fatalf("missing cookie row themed select sizing rule")
	}
	themedRule := text[themedIdx:]
	if end := strings.Index(themedRule, "}"); end >= 0 {
		themedRule = themedRule[:end]
	}
	if !strings.Contains(themedRule, "width: max-content;") {
		t.Fatalf("visible themed select should only use needed width, got:\n%s", themedRule)
	}
}

func TestSidebarNavPlatforms(t *testing.T) {
	t.Run("all platforms", func(t *testing.T) {
		p := newTestPageProps()
		p.UserPlatforms = []string{"youtube", "twitter", "tiktok"}
		html := renderBase(t, p)

		navItems := []string{
			`href="/videos"`,
			`href="/feed"`,
			`href="/shorts"`,
			`href="/channels"`,
			`href="/bookmarks"`,
			`href="/liked"`,
		}
		for _, item := range navItems {
			if !strings.Contains(html, item) {
				t.Errorf("expected nav item %q with all platforms", item)
			}
		}
	})

	t.Run("youtube only", func(t *testing.T) {
		p := newTestPageProps()
		p.UserPlatforms = []string{"youtube"}
		html := renderBase(t, p)

		// Should have videos, channels, bookmarks
		for _, item := range []string{`href="/videos"`, `href="/channels"`, `href="/bookmarks"`} {
			if !strings.Contains(html, item) {
				t.Errorf("expected nav item %q with youtube-only", item)
			}
		}

		// Should NOT have feed, shorts, liked
		for _, item := range []string{`href="/feed"`, `href="/shorts"`, `href="/liked"`} {
			if strings.Contains(html, item) {
				t.Errorf("unexpected nav item %q with youtube-only", item)
			}
		}
	})
}

func TestSidebarChannelGroups(t *testing.T) {
	p := newTestPageProps()
	html := renderBase(t, p)

	if !strings.Contains(html, `id="group-starred"`) {
		t.Error("expected starred group")
	}
	if !strings.Contains(html, `data-channel-id="youtube_test1"`) {
		t.Error("expected channel item with youtube_test1")
	}
	if !strings.Contains(html, "Test Channel") {
		t.Error("expected channel name")
	}
	if !strings.Contains(html, "@test_handle") {
		t.Error("expected channel handle next to channel name")
	}
}

func TestSidebarGroupTitleUsesCatalog(t *testing.T) {
	p := newTestPageProps()
	p.Language = "tr"
	p.Text = map[string]string{
		"drawer_starred": "Yıldızlı",
	}
	p.Sidebar.Groups[0].Title = "Favourites"
	p.Sidebar.Groups[0].GroupID = "favourites"
	html := renderBase(t, p)

	if !strings.Contains(html, ">Yıldızlı<") {
		t.Fatalf("expected localized favourites group title:\n%s", html)
	}
	if strings.Contains(html, ">Favourites<") {
		t.Fatalf("unexpected raw favourites group title:\n%s", html)
	}
	if !strings.Contains(html, `data-i18n="drawer_starred"`) {
		t.Fatalf("expected live i18n marker for sidebar group title:\n%s", html)
	}
}

func TestSidebarPinnedVideos(t *testing.T) {
	p := newTestPageProps()
	p.Sidebar.PinnedVideos = []model.Video{
		{VideoID: "pin1", Title: "Pinned Test Video", Duration: 600, PlaybackPosition: 150},
	}
	html := renderBase(t, p)

	if !strings.Contains(html, "Pinned Videos") {
		t.Error("expected pinned section title")
	}
	if !strings.Contains(html, "/player/pin1") {
		t.Error("expected pinned video link")
	}
	if !strings.Contains(html, "Pinned Test Video") {
		t.Error("expected pinned video title")
	}
	if !strings.Contains(html, "width:25%") {
		t.Error("expected 25% progress fill for 150s of 600s")
	}
}

func TestSidebarCurrentlyWatching(t *testing.T) {
	p := newTestPageProps()
	p.Sidebar.CurrentlyWatching = []model.Video{
		{VideoID: "w1", Title: "In Progress", Duration: 1000, PlaybackPosition: 200},
	}
	html := renderBase(t, p)

	if !strings.Contains(html, "Continue Watching") {
		t.Error("expected Continue Watching section title")
	}
	if !strings.Contains(html, "width:20%") {
		t.Error("expected 20% progress fill for 200s of 1000s")
	}
}

func TestSidebarCurrentlyAvailable(t *testing.T) {
	p := newTestPageProps()
	p.Sidebar.CurrentlyAvailable = []model.Video{
		{VideoID: "a1", Title: "Temp Unpinned"},
	}
	html := renderBase(t, p)

	if !strings.Contains(html, "Currently Available") {
		t.Error("expected Currently Available section title")
	}
	if !strings.Contains(html, "Temp Unpinned") {
		t.Error("expected unpinned temp title")
	}
}

func TestPrefsModalAdminTabs(t *testing.T) {
	t.Run("admin sees users tab", func(t *testing.T) {
		// Prefs body is now HTMX lazy-loaded. Test the PrefsBody component directly.
		p := newTestPageProps()
		p.UserRole = "admin"
		prefs := PrefsData{Settings: map[string]any{"quality": "best"}}
		var buf bytes.Buffer
		if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		if !strings.Contains(html, `data-prefs-tab="users"`) {
			t.Error("expected users tab for admin")
		}
		if !strings.Contains(html, `/api/config/export`) {
			t.Error("expected export link for admin")
		}
	})

	t.Run("non-admin no users tab", func(t *testing.T) {
		p := newTestPageProps()
		p.UserRole = "user"
		prefs := PrefsData{Settings: map[string]any{"quality": "best"}}
		var buf bytes.Buffer
		if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		if strings.Contains(html, `data-prefs-tab="users"`) {
			t.Error("unexpected users tab for non-admin")
		}
		if strings.Contains(html, `/api/config/export`) {
			t.Error("unexpected export link for non-admin")
		}
	})
}

func TestPrefsPlatformSettingsTabOwnsPlatformDefaults(t *testing.T) {
	p := newTestPageProps()
	prefs := PrefsData{Settings: map[string]any{"quality": "best"}}
	var buf bytes.Buffer
	if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, `data-prefs-tab="feed" type="button">Platform Settings</button>`) {
		t.Fatalf("expected settings modal feed tab to be labeled Platform Settings:\n%s", html)
	}
	if !strings.Contains(html, `data-shortcuts-sub="feed-shorts" type="button">Feed</button>`) {
		t.Fatalf("shortcuts Feed subtab should stay labeled Feed:\n%s", html)
	}

	platformPanel := strings.Index(html, `data-prefs-panel="feed"`)
	sponsorPanel := strings.Index(html, `data-prefs-panel="sponsorblock"`)
	youtubeSetting := strings.Index(html, `name="youtube_fetch_delay"`)
	if platformPanel < 0 || sponsorPanel < 0 || youtubeSetting < 0 {
		t.Fatalf("missing expected platform panel or YouTube setting:\n%s", html)
	}
	if youtubeSetting < platformPanel || youtubeSetting > sponsorPanel {
		t.Fatalf("youtube settings should render inside Platform Settings panel:\n%s", html)
	}
}

func TestPrefsBodyAllowsThreeSecondFetchDelay(t *testing.T) {
	p := newTestPageProps()
	prefs := PrefsData{Settings: map[string]any{"x_feed_fetch_delay": "3"}}
	var buf bytes.Buffer
	if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, `id="global-setting-x-feed-fetch-delay" name="x_feed_fetch_delay" class="input" min="3"`) {
		t.Fatalf("fetch delay input should allow 3 seconds:\n%s", html)
	}
	if !strings.Contains(html, `name="x_feed_fetch_delay" class="input" min="3" max="300" value="3"`) {
		t.Fatalf("fetch delay input should preserve a 3 second value:\n%s", html)
	}
}

func TestServerDashboardLoadsRawServerLogUnderActivity(t *testing.T) {
	p := newTestPageProps()
	data := ServerDashboardData{
		UptimeText:     "1m",
		UptimeStarted:  "2026-05-02 01:00:00",
		MemoryHistory:  []float64{1, 2, 3},
		ChannelsByPlat: map[string]int{},
		Activity: []ServerActivityEntry{{
			Time:    "01:00:00",
			Status:  "info",
			Source:  "worker",
			Message: "activity stays concise",
		}},
	}
	var buf bytes.Buffer
	if err := ServerDashboard(p, data).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	activityIdx := strings.Index(html, `id="sv-log-console"`)
	rawIdx := strings.Index(html, `id="sv-raw-log-section"`)
	if activityIdx < 0 || rawIdx < 0 {
		t.Fatalf("server dashboard should render activity and raw log sections:\n%s", html)
	}
	if rawIdx < activityIdx {
		t.Fatalf("raw server log should render under the activity log:\n%s", html)
	}
	if !strings.Contains(html, `activity stays concise`) {
		t.Fatalf("activity log should keep rendering existing activity entries:\n%s", html)
	}
	if !strings.Contains(html, `hx-get="/api/logs/server/read?type=server&lines=80&filter_noise=1&fmt=html"`) {
		t.Fatalf("raw server log section should load verbose server log endpoint:\n%s", html)
	}
	if !strings.Contains(html, `hx-trigger="load, logs-poll"`) {
		t.Fatalf("raw server log section should self-load after the server panel swaps in:\n%s", html)
	}
}

func TestPrefsUILanguagePreviewAndSaveDoesNotReloadPage(t *testing.T) {
	p := newTestPageProps()
	p.SupportedLanguages = []LanguageChoice{
		{Code: "auto", Name: "Automatic"},
		{Code: "en", Name: "English"},
		{Code: "tr", Name: "Turkish"},
	}
	prefs := PrefsData{Settings: map[string]any{
		"quality":                "best",
		"ui_language":            "tr",
		"_persisted_ui_language": "en",
	}}
	var buf bytes.Buffer
	if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "window.location.reload") {
		t.Fatalf("language preferences should not force a page reload:\n%s", html)
	}
	if !strings.Contains(html, "handlePrefsAfterRequest.call(this,event") {
		t.Fatalf("save status handler should route through the shared preferences handler:\n%s", html)
	}
	if !strings.Contains(html, "previewLanguage(this.value)") {
		t.Fatalf("language select should preview the selected catalog before save:\n%s", html)
	}
	if strings.Contains(html, "/api/settings/form?lang=") {
		t.Fatalf("language preview should not reload the preferences form:\n%s", html)
	}
	for _, want := range []string{
		`data-i18n-scope="prefs"`,
		`id="prefs-unsaved-reminder"`,
		`data-i18n="status_save_preferences_to_apply"`,
		`data-i18n="action_save_preferences"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("preferences form missing %q:\n%s", want, html)
		}
	}
}

func TestPrefsFeedTranslateLookaheadVisibility(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		wantHidden bool
	}{
		{name: "lazy shows lookahead", mode: "lazy", wantHidden: false},
		{name: "manual hides lookahead", mode: "off", wantHidden: true},
		{name: "background hides lookahead", mode: "background", wantHidden: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPageProps()
			prefs := PrefsData{Settings: map[string]any{
				"quality":             "best",
				"translate_auto_mode": tc.mode,
			}}
			var buf bytes.Buffer
			if err := PrefsBody(p, prefs).Render(context.Background(), &buf); err != nil {
				t.Fatal(err)
			}
			html := buf.String()
			hiddenMarker := `id="translate-lookahead-config" style="display:none;"`
			if tc.wantHidden && !strings.Contains(html, hiddenMarker) {
				t.Fatalf("expected lookahead row to be hidden for mode %q:\n%s", tc.mode, html)
			}
			if !tc.wantHidden && strings.Contains(html, hiddenMarker) {
				t.Fatalf("expected lookahead row to be visible for mode %q:\n%s", tc.mode, html)
			}
		})
	}
}

func TestHeaderElements(t *testing.T) {
	p := newTestPageProps()
	html := renderBase(t, p)

	checks := []string{
		`id="global-search-input"`,
		`id="global-search-clear"`,
		`id="search-dropdown"`,
		`id="stop-play-container"`,
		`id="prefs-btn"`,
	}
	for _, c := range checks {
		if !strings.Contains(html, c) {
			t.Errorf("expected header element %q", c)
		}
	}
}

func TestLogsModalTabs(t *testing.T) {
	p := newTestPageProps()
	html := renderBase(t, p)

	tabs := []string{
		`data-logs-tab="server"`,
		`data-logs-tab="android"`,
		`data-logs-tab="downloads"`,
		`data-logs-tab="twitter"`,
		`data-logs-panel="server"`,
		`data-logs-panel="android"`,
		`data-logs-panel="downloads"`,
		`data-logs-panel="twitter"`,
	}
	for _, tab := range tabs {
		if !strings.Contains(html, tab) {
			t.Errorf("expected logs modal element %q", tab)
		}
	}
}

func TestTranslateTargetDefault(t *testing.T) {
	p := newTestPageProps()
	p.TranslateTargetLang = ""
	html := renderBase(t, p)

	if !strings.Contains(html, `name="translate-target" content="en"`) {
		t.Error("expected default translate-target of 'en'")
	}
}
