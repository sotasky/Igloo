package web

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/storage"
)

func TestSettingsFromForm_PersistsDearrowMode(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("dearrow_mode", "casual")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	if got := body["dearrow_mode"]; got != "casual" {
		t.Errorf("dearrow_mode = %q, want casual", got)
	}
}

func TestSettingsFromForm_PersistsShareEmbedFriendlyLinks(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("share_embed_friendly_links", "true")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	if got := body["share_embed_friendly_links"]; got != "true" {
		t.Errorf("share_embed_friendly_links = %q, want true", got)
	}
}

func TestSettingsToAPIFormat_DefaultsShareEmbedFriendlyLinksOff(t *testing.T) {
	got := settingsToAPIFormat(nil)
	if got["share_embed_friendly_links"] != false {
		t.Fatalf("share_embed_friendly_links = %#v, want false", got["share_embed_friendly_links"])
	}
}

func TestSettingsToAPIFormatFiltersRetiredIntervalSettings(t *testing.T) {
	got := settingsToAPIFormat(map[string]string{
		"youtube_fetch_delay":    "12",
		"youtube_check_interval": "6",
		"shorts_check_interval":  "3",
	})
	if got["youtube_fetch_delay"] != "12" {
		t.Fatalf("youtube_fetch_delay = %#v, want 12", got["youtube_fetch_delay"])
	}
	for _, key := range []string{"youtube_check_interval", "shorts_check_interval"} {
		if _, ok := got[key]; ok {
			t.Fatalf("%s should not be exposed: %#v", key, got)
		}
	}
}

func TestSettingsFromForm_PersistsWebThemeSettings(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("web_theme_id", "dracula")
	form.Set("web_theme_accent", "#50fa7b")
	form.Set("web_custom_css", ".feed-card { border-color: hotpink; }")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	normalizeSettingsUpdate(body)
	if got := body["web_theme_id"]; got != "dracula" {
		t.Fatalf("web_theme_id = %q, want dracula", got)
	}
	if got := body["web_theme_accent"]; got != "#50fa7b" {
		t.Fatalf("web_theme_accent = %q, want #50fa7b", got)
	}
	if got := body["web_custom_css"]; got != ".feed-card { border-color: hotpink; }" {
		t.Fatalf("web_custom_css = %q", got)
	}
}

func TestHandleUpdateSettingsStoresNormalizedWebThemeSettings(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("web_theme_id", "github-dark")
	form.Set("web_theme_accent", "#58A6FF")
	form.Set("web_custom_css", ".main { --custom: 1; }")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleUpdateSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, _ := srv.db.GetSetting("web_theme_id", ""); got != "github-dark" {
		t.Fatalf("stored web_theme_id = %q", got)
	}
	if got, _ := srv.db.GetSetting("web_theme_accent", ""); got != "#58a6ff" {
		t.Fatalf("stored web_theme_accent = %q", got)
	}
	if got, _ := srv.db.GetSetting("web_custom_css", ""); got != ".main { --custom: 1; }" {
		t.Fatalf("stored web_custom_css = %q", got)
	}
}

func TestHandleUpdateSettingsRejectsRelativeBackupDir(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("backup_dir", filepath.Join("var", "mnt", "external_drive"))
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleUpdateSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, _ := srv.db.GetSetting("backup_dir", ""); got != "" {
		t.Fatalf("stored backup_dir = %q, want empty", got)
	}
}

func TestHandleUpdateSettingsClampsBackupKeepCount(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("backup_keep_count", "9")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleUpdateSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, _ := srv.db.GetSetting("backup_keep_count", ""); got != "5" {
		t.Fatalf("stored backup_keep_count = %q, want 5", got)
	}
}

func TestHandleUpdateSettingsRejectsNonAdmin(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("web_theme_id", "dracula"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("web_theme_accent", "#50fa7b"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("web_custom_css", ".before { color: red; }"); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("web_theme_id", "github-dark")
	form.Set("web_theme_accent", "#58A6FF")
	form.Set("web_custom_css", ".after { color: blue; }")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(contextWithUser(req, "user", "user"))
	rec := httptest.NewRecorder()

	srv.handleUpdateSettings(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, _ := srv.db.GetSetting("web_theme_id", ""); got != "dracula" {
		t.Fatalf("stored web_theme_id = %q", got)
	}
	if got, _ := srv.db.GetSetting("web_theme_accent", ""); got != "#50fa7b" {
		t.Fatalf("stored web_theme_accent = %q", got)
	}
	if got, _ := srv.db.GetSetting("web_custom_css", ""); got != ".before { color: red; }" {
		t.Fatalf("stored web_custom_css = %q", got)
	}
}

func TestHandleGetSettingsRequiresAdmin(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("translate_api_key", "sample-secret-key"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/settings", nil)
	req = req.WithContext(contextWithUser(req, "user", "user"))
	rec := httptest.NewRecorder()

	srv.handleGetSettings(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sample-secret-key") {
		t.Fatal("non-admin settings response leaked translate_api_key")
	}

	adminReq := httptest.NewRequest("GET", "/api/settings", nil)
	adminReq = adminReq.WithContext(contextWithUser(adminReq, "admin", "admin"))
	adminRec := httptest.NewRecorder()

	srv.handleGetSettings(adminRec, adminReq)

	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body = %s", adminRec.Code, adminRec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(adminRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin settings: %v", err)
	}
	if got := body["translate_api_key"]; got != "sample-secret-key" {
		t.Fatalf("admin translate_api_key = %#v", got)
	}
}

func TestHandleSettingsFormRequiresAdmin(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("translate_api_key", "sample-secret-key"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/settings/form", nil)
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(contextWithUser(req, "user", "user"))
	rec := httptest.NewRecorder()

	srv.handleSettingsForm(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sample-secret-key") {
		t.Fatal("non-admin settings form leaked translate_api_key")
	}
}

func TestHandleThemeCSSServesPersistedThemeAsNoStoreCSS(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("web_theme_id", "dracula"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("web_theme_accent", "#50fa7b"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("web_custom_css", ".feed-card { border-color: hotpink; }"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/theme.css", nil)
	rec := httptest.NewRecorder()
	srv.handleThemeCSS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`--bg-primary: #282a36;`,
		`--accent-primary: #50fa7b;`,
		`.feed-card { border-color: hotpink; }`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("theme CSS missing %q:\n%s", want, body)
		}
	}
}

func TestHandleThemeJSONServesPersistedThemeTokens(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("web_theme_id", "dracula"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("web_theme_accent", "#50fa7b"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("web_custom_css", ".feed-card { border-color: hotpink; }"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/theme.json", nil)
	rec := httptest.NewRecorder()
	srv.handleThemeJSON(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		ThemeID string `json:"theme_id"`
		Tokens  struct {
			Accent string `json:"accent"`
			Base   string `json:"base"`
			Text   string `json:"text"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode theme json: %v\n%s", err, rec.Body.String())
	}
	if body.ThemeID != "dracula" {
		t.Fatalf("theme_id = %q, want dracula", body.ThemeID)
	}
	if body.Tokens.Accent != "#50fa7b" {
		t.Fatalf("accent = %q, want #50fa7b", body.Tokens.Accent)
	}
	if body.Tokens.Base != "#282a36" {
		t.Fatalf("base = %q, want #282a36", body.Tokens.Base)
	}
	if body.Tokens.Text != "#f8f8f2" {
		t.Fatalf("text = %q, want #f8f8f2", body.Tokens.Text)
	}
	if strings.Contains(rec.Body.String(), "hotpink") {
		t.Fatal("theme JSON should not expose custom CSS")
	}
}

func TestHandleConfigExportFullIncludesDatabaseStateWithoutMediaPayload(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.WithWrite(func(tx *sql.Tx) error {
		statements := []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO channels (channel_id, name, url, platform)
				VALUES ('test_channel_alpha', 'Channel Alpha', 'https://example.com/channel', 'youtube')`, nil},
			{`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
				VALUES ('test_bookmarked_video', 'test_channel_alpha', 'youtube_video', 'Booked Video', 12, 1000)`, nil},
			{`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
				VALUES ('test_post_1', 'twitter_sample_author', 'tweet', 'X post author', 0, 1000)`, nil},
			{`INSERT INTO feed_items (tweet_id, channel_id, published_at, fetched_at)
				VALUES ('test_post_1', 'twitter_sample_author', 1000, 1000)`, nil},
			{`INSERT INTO bookmark_categories (id, name, created_at)
				VALUES (7, 'Saved', 1000)`, nil},
			{`INSERT INTO bookmarks (video_id, category_id, custom_title, bookmarked_at)
				VALUES ('test_bookmarked_video', 7, 'Watch Later', 2000)`, nil},
			{`INSERT INTO bookmarks (video_id, category_id, custom_title, bookmarked_at)
				VALUES ('test_post_1', 7, 'Reference', 3000)`, nil},
			{`INSERT INTO muted_channels (channel_id, muted_at)
				VALUES ('twitter_sample_muted', 4000)`, nil},
			{`INSERT INTO watch_history (video_id, playback_position, duration, updated_at_ms)
				VALUES ('test_bookmarked_video', 12, 12, 5000)`, nil},
			{`INSERT INTO moment_views (video_id, viewed_at)
				VALUES ('test_bookmarked_video', 6000)`, nil},
		}
		for _, stmt := range statements {
			if _, err := tx.Exec(stmt.sql, stmt.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed export fixture: %v", err)
	}
	req := httptest.NewRequest("GET", "/api/config/export-full", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigExportFull(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", got)
	}

	entries := readZipEntries(t, rec.Body.Bytes())

	rawJSON, ok := entries["export.json"]
	if !ok {
		t.Fatalf("export.json missing; entries=%v", mapKeys(entries))
	}
	var payload map[string]any
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		t.Fatalf("export.json invalid: %v", err)
	}
	if got := payload["bookmarks"].([]any)[0].(map[string]any)["custom_title"]; got != "Watch Later" {
		t.Fatalf("first bookmark custom_title = %v, want Watch Later", got)
	}
	dbBytes, ok := entries[config.DatabaseFilename]
	if !ok {
		t.Fatalf("full export missing %s; entries=%v", config.DatabaseFilename, mapKeys(entries))
	}
	snapshotPath := filepath.Join(t.TempDir(), config.DatabaseFilename)
	if err := os.WriteFile(snapshotPath, dbBytes, 0o644); err != nil {
		t.Fatalf("write db snapshot: %v", err)
	}
	snapshotDB, err := sql.Open("sqlite", "file:"+snapshotPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open db snapshot: %v", err)
	}
	defer func() {
		_ = snapshotDB.Close()
	}()
	for _, table := range []string{"muted_channels", "watch_history", "moment_views"} {
		var count int
		if err := snapshotDB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("snapshot count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("snapshot %s count = %d, want 1", table, count)
		}
	}

	for name := range entries {
		if strings.HasPrefix(name, "assets/") {
			t.Fatalf("full export included media payload %s", name)
		}
	}
}

func TestHandleConfigExportFullIncludesRuntimeConfigFiles(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.ConfDir = t.TempDir()
	srv.cfg.CookiesDir = filepath.Join(srv.cfg.ConfDir, "cookies")
	srv.cfg.StaticDir = filepath.Join(t.TempDir(), "static")

	files := map[string]string{
		"nginx.conf":                  "pid /old/state/nginx.pid;\nssl_certificate /old/config/server.crt;\n",
		"custom.env":                  "CUSTOM_SECRET=example\n",
		"auth_users.json":             `{"admin":{"role":"admin"}}` + "\n",
		"auth_secret":                 "secret-key",
		"cookies/twitter_cookies.txt": "cookie-data",
	}
	for rel, content := range files {
		path := filepath.Join(srv.cfg.ConfDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}
	sidecarPath := filepath.Join(srv.cfg.ConfDir, "subscriptions_youtube.json")
	if err := os.WriteFile(sidecarPath, []byte(`[{"id":"stale_sidecar"}]`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile stale subscription sidecar: %v", err)
	}
	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "youtube_sample_export",
		Name:         "Runtime Subs",
		Platform:     "youtube",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel runtime subscription: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/config/export-full", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigExportFull(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	entries := readZipEntries(t, rec.Body.Bytes())
	for rel, want := range files {
		name := filepath.ToSlash(filepath.Join("config", rel))
		got, ok := entries[name]
		if !ok {
			t.Fatalf("missing config entry %s; entries=%v", name, mapKeys(entries))
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, string(got), want)
		}
	}
	if _, ok := entries["config/subscriptions_youtube.json"]; ok {
		t.Fatalf("full export included stale subscription sidecar; entries=%v", mapKeys(entries))
	}
	rawSubs, ok := entries["subscriptions.json"]
	if !ok {
		t.Fatalf("subscriptions.json missing; entries=%v", mapKeys(entries))
	}
	if bytes.Contains(rawSubs, []byte("stale_sidecar")) {
		t.Fatalf("subscriptions.json came from stale sidecar: %s", string(rawSubs))
	}
	var subsPayload db.ConfigExport
	if err := json.Unmarshal(rawSubs, &subsPayload); err != nil {
		t.Fatalf("subscriptions.json invalid: %v", err)
	}
	if len(subsPayload.Subscriptions) != 1 || subsPayload.Subscriptions[0].ChannelID != "youtube_sample_export" {
		t.Fatalf("subscriptions.json subscriptions = %#v", subsPayload.Subscriptions)
	}
	if subsPayload.Scope != "subscriptions" {
		t.Fatalf("subscriptions.json scope = %q, want subscriptions", subsPayload.Scope)
	}
	if _, ok := entries["runtime.json"]; !ok {
		t.Fatalf("runtime.json missing; entries=%v", mapKeys(entries))
	}
	rawJSON := entries["export.json"]
	var payload map[string]any
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		t.Fatalf("export.json invalid: %v", err)
	}
	if _, ok := payload["user_id"]; ok {
		t.Fatalf("single-user export carried retired user_id: %v", payload["user_id"])
	}
}

func TestWriteFullExportFailsWhenSelectedRuntimeConfigCannotBeRead(t *testing.T) {
	var output bytes.Buffer
	err := writeFullExportZip(
		&output,
		db.ConfigExport{Version: db.ConfigExportVersion},
		"",
		[]fullExportRuntimeFile{{SourcePath: filepath.Join(t.TempDir(), "missing"), ArchivePath: "config/auth_secret"}},
		fullExportRuntimeManifest{Version: 2},
	)
	if err == nil {
		t.Fatal("writeFullExportZip accepted a missing selected runtime config file")
	}
}

func TestCollectFullExportRuntimeConfigRejectsInvalidRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: &config.Config{ConfDir: root}}
	if _, err := server.collectFullExportRuntimeConfigFiles(); err == nil {
		t.Fatal("collectFullExportRuntimeConfigFiles accepted a non-directory config root")
	}
}

func TestCollectFullExportRuntimeConfigRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "auth_secret")); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: &config.Config{ConfDir: root}}
	if _, err := server.collectFullExportRuntimeConfigFiles(); err == nil {
		t.Fatal("collectFullExportRuntimeConfigFiles accepted a config symlink")
	}
}

func TestHandleConfigExportSubscriptionsDownloadsDBSubscriptions(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "youtube_sample_followed",
		Name:         "Export Subscription",
		Platform:     "youtube",
		IsSubscribed: true,
		IsStarred:    true,
	}); err != nil {
		t.Fatalf("AddChannel subscribed: %v", err)
	}
	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "youtube_sample_unfollowed",
		Name:         "Not Exported",
		Platform:     "youtube",
		IsSubscribed: false,
	}); err != nil {
		t.Fatalf("AddChannel unsubscribed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/config/export-subscriptions", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigExportSubscriptions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "igloo-subscriptions-") {
		t.Fatalf("Content-Disposition = %q, want subscriptions attachment", got)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response invalid JSON: %v", err)
	}
	if _, ok := raw["settings"]; ok {
		t.Fatalf("subscription export should not carry settings: %s", rec.Body.String())
	}
	var payload db.ConfigExport
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response invalid config payload: %v", err)
	}
	if payload.Scope != "subscriptions" {
		t.Fatalf("scope = %q, want subscriptions", payload.Scope)
	}
	if len(payload.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %#v, want one DB follow", payload.Subscriptions)
	}
	got := payload.Subscriptions[0]
	if got.ChannelID != "youtube_sample_followed" || !got.IsStarred {
		t.Fatalf("subscription payload = %#v", got)
	}
}

func TestHandleConfigExportSavesToBackupDirWhenConfigured(t *testing.T) {
	srv := newTestServer(t)
	backupDir := t.TempDir()
	if err := srv.db.SetSetting("backup_dir", backupDir); err != nil {
		t.Fatalf("SetSetting backup_dir: %v", err)
	}
	if err := srv.db.SetSetting("backup_enabled", "true"); err != nil {
		t.Fatalf("SetSetting backup_enabled: %v", err)
	}
	if err := srv.db.SetSetting("export_test_key", "export_test_val"); err != nil {
		t.Fatalf("SetSetting export key: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/config/export", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want no download attachment", got)
	}
	matches, err := filepath.Glob(filepath.Join(backupDir, "igloo-config-*.json"))
	if err != nil {
		t.Fatalf("glob config exports: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("config export files = %v, want one", matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read saved config export: %v", err)
	}
	if !strings.Contains(string(raw), `"export_test_key":"export_test_val"`) {
		t.Fatalf("saved config export missing setting: %s", string(raw))
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v; body=%s", err, rec.Body.String())
	}
	if body["saved"] != true || body["path"] == "" {
		t.Fatalf("saved response = %#v, want saved path", body)
	}
}

func TestHandleConfigExportDownloadsWhenBackupDirConfiguredButDisabled(t *testing.T) {
	srv := newTestServer(t)
	backupDir := t.TempDir()
	if err := srv.db.SetSetting("backup_dir", backupDir); err != nil {
		t.Fatalf("SetSetting backup_dir: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/config/export", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("Content-Disposition = %q, want download attachment", got)
	}
	matches, err := filepath.Glob(filepath.Join(backupDir, "igloo-config-*.json"))
	if err != nil {
		t.Fatalf("glob config exports: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("config export files = %v, want none", matches)
	}
}

func TestWriteExportFileRejectsRelativeDir(t *testing.T) {
	_, err := writeExportFile(context.Background(), storage.NewMediaExecutor(), filepath.Join("var", "mnt", "external_drive"), "igloo-config", ".json", func(dst io.Writer) error {
		_, err := dst.Write([]byte("{}"))
		return err
	})
	if err == nil {
		t.Fatal("writeExportFile accepted a relative dir")
	}
	if _, err := os.Stat(filepath.Join("var", "mnt", "external_drive")); !os.IsNotExist(err) {
		t.Fatalf("relative export dir was created or stat failed: %v", err)
	}
}

func TestHandleConfigExportFullSavesZipToBackupDirWhenConfigured(t *testing.T) {
	srv := newTestServer(t)
	backupDir := t.TempDir()
	if err := srv.db.SetSetting("backup_dir", backupDir); err != nil {
		t.Fatalf("SetSetting backup_dir: %v", err)
	}
	if err := srv.db.SetSetting("backup_enabled", "true"); err != nil {
		t.Fatalf("SetSetting backup_enabled: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/config/export-full", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigExportFull(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want no download attachment", got)
	}
	matches, err := filepath.Glob(filepath.Join(backupDir, "igloo-full-*.zip"))
	if err != nil {
		t.Fatalf("glob full exports: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("full export files = %v, want one", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read saved full export: %v", err)
	}
	entries := readZipEntries(t, data)
	if _, ok := entries[config.DatabaseFilename]; !ok {
		t.Fatalf("saved full export missing %s; entries=%v", config.DatabaseFilename, mapKeys(entries))
	}
	for name := range entries {
		if strings.HasPrefix(name, "assets/") {
			t.Fatalf("saved full export included media payload %s", name)
		}
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v; body=%s", err, rec.Body.String())
	}
	if body["saved"] != true || body["path"] == "" {
		t.Fatalf("saved response = %#v, want saved path", body)
	}
}

func TestHandleConfigImportRequiresReplaceForRestoreArchive(t *testing.T) {
	srv := newTestServer(t)
	body, contentType := multipartBody(t, "file", "backup.zip", []byte{0x50, 0x4b, 0x03, 0x04})
	req := httptest.NewRequest(http.MethodPost, "/api/config/import", body)
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigImport(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "replace mode required") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSubscribeExistingUnfollowedChannelAddsFollow(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: "tiktok_existing",
		SourceID:  "existing",
		Name:      "Existing",
		URL:       "https://www.tiktok.com/@existing",
		Platform:  "tiktok",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/subscribe", strings.NewReader(`{"url":"https://www.tiktok.com/@existing"}`))
	rec := httptest.NewRecorder()

	srv.handleSubscribe(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !srv.db.IsChannelFollowed("tiktok_existing") {
		t.Fatal("expected existing channel to gain a follow row")
	}
	if !strings.Contains(rec.Body.String(), `"already_existed":true`) {
		t.Fatalf("expected already_existed response, got %s", rec.Body.String())
	}
}

func TestSubscribeYouTubeKnownProfileHandleDoesNotRequireYtDlp(t *testing.T) {
	for _, rawURL := range []string{
		"https://www.youtube.com/@known.handle/videos",
		"https://www.youtube.com/known.handle",
	} {
		t.Run(rawURL, func(t *testing.T) {
			srv := newTestServer(t)
			if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
				ChannelID:   "youtube_UCknownprofile",
				Platform:    "youtube",
				Handle:      "@Known.Handle",
				DisplayName: "Known Profile",
			}); err != nil {
				t.Fatalf("UpsertChannelProfile: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest("POST", "/api/subscribe", strings.NewReader(fmt.Sprintf(`{"url":%q}`, rawURL))).WithContext(ctx)
			rec := httptest.NewRecorder()

			srv.handleSubscribe(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !srv.db.IsChannelFollowed("youtube_UCknownprofile") {
				t.Fatal("expected known profile channel to gain a follow row")
			}
			ch, err := srv.db.GetChannel("youtube_UCknownprofile")
			if err != nil {
				t.Fatalf("GetChannel: %v", err)
			}
			if ch == nil {
				t.Fatal("expected channel row to be restored from profile")
			}
			if ch.SourceID != "UCknownprofile" || ch.URL != "https://www.youtube.com/channel/UCknownprofile" {
				t.Fatalf("unexpected restored channel: %+v", ch)
			}
			if !strings.Contains(rec.Body.String(), `"channel_id":"youtube_UCknownprofile"`) {
				t.Fatalf("expected response channel id, got %s", rec.Body.String())
			}
		})
	}
}

func TestParseOPMLCanonicalizesYouTubeChannelIDs(t *testing.T) {
	channels := parseOPML([]byte(`
		<opml><body>
			<outline text="Example" xmlUrl="https://www.youtube.com/feeds/videos.xml?channel_id=UCexample12345"/>
		</body></opml>
	`))
	if len(channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(channels))
	}
	ch := channels[0]
	if ch.ChannelID != "youtube_UCexample12345" {
		t.Fatalf("ChannelID = %q, want youtube_UCexample12345", ch.ChannelID)
	}
	if ch.SourceID != "UCexample12345" {
		t.Fatalf("SourceID = %q, want UCexample12345", ch.SourceID)
	}
	if ch.URL != "https://www.youtube.com/channel/UCexample12345" {
		t.Fatalf("URL = %q", ch.URL)
	}
}

func TestSettingsFromForm_OmitsDearrowModeWhenEmpty(t *testing.T) {
	// When the form has no dearrow_mode field the field should be absent
	// from the body — simpleFields handling uses `if v != ""`.
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	if _, exists := body["dearrow_mode"]; exists {
		t.Error("dearrow_mode should be absent when form value is empty")
	}
}

func TestSettingsFromForm_PersistsTranslateAutomationFields(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("translate_backend", "deepl")
	form.Set("translate_auto_mode", "background")
	form.Set("translate_auto_lookahead", "25")
	form.Set("translate_model", "qwen2.5:7b")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	normalizeSettingsUpdate(body)
	if got := body["translate_backend"]; got != "deepl" {
		t.Errorf("translate_backend = %q, want deepl", got)
	}
	if got := body["translate_auto_mode"]; got != "background" {
		t.Errorf("translate_auto_mode = %q, want background", got)
	}
	if got := body["translate_auto_lookahead"]; got != "25" {
		t.Errorf("translate_auto_lookahead = %q, want 25", got)
	}
	if got := body["translate_model"]; got != "qwen2.5:7b" {
		t.Errorf("translate_model = %q, want qwen2.5:7b", got)
	}
}

func TestSettingsFromForm_AllowsClearingTranslateAPIFields(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("translate_api_site", "")
	form.Set("translate_api_key", "")
	form.Set("translate_model", "")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	for _, key := range []string{"translate_api_site", "translate_api_key", "translate_model"} {
		got, exists := body[key]
		if !exists {
			t.Fatalf("%s should be present when submitted blank", key)
		}
		if got != "" {
			t.Fatalf("%s = %q, want empty string", key, got)
		}
	}
}

func TestSettingsFromForm_OmitsTranslateAPIFieldsWhenAbsent(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	for _, key := range []string{"translate_api_site", "translate_api_key"} {
		if _, exists := body[key]; exists {
			t.Fatalf("%s should be absent when not submitted", key)
		}
	}
}

func TestSettingsFromForm_PersistsUILanguage(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("ui_language", "tr")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	if got := body["ui_language"]; got != "tr" {
		t.Errorf("ui_language = %q, want tr", got)
	}
}

func TestSettingsFromForm_PersistsInstagramGlobals(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{}
	form.Set("instagram_fetch_delay", "5")
	form.Set("instagram_max_videos", "37")
	form.Set("instagram_include_tagged_default", "true")
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	body := srv.settingsFromForm(req)
	if got := body["instagram_fetch_delay"]; got != "5" {
		t.Errorf("instagram_fetch_delay = %q, want 5", got)
	}
	if got := body["instagram_max_videos"]; got != "37" {
		t.Errorf("instagram_max_videos = %q, want 37", got)
	}
	if got := body["instagram_include_tagged_default"]; got != "true" {
		t.Errorf("instagram_include_tagged_default = %q, want true", got)
	}
}

func TestCookieRowsIncludeInstagram(t *testing.T) {
	for _, p := range cookiePlatformNames {
		if p.ID == "instagram" && p.Name == "Instagram" {
			return
		}
	}
	t.Fatal("cookie platform rows should include Instagram")
}

func TestHandleToggleCookieHTMXRerendersRowsAndKeepsFile(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.CookiesDir = t.TempDir()
	cookiePath := filepath.Join(srv.cfg.CookiesDir, "twitter_cookies.txt")
	if err := os.WriteFile(cookiePath, []byte("cookies"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/cookies/twitter/toggle", nil)
	req.SetPathValue("platform", "twitter")
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleToggleCookie(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(cookiePath); err != nil {
		t.Fatalf("cookie file should remain after disable: %v", err)
	}
	if got, _ := srv.db.GetSetting("cookies_twitter_enabled", "1"); got != "0" {
		t.Fatalf("cookies_twitter_enabled = %q, want 0", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`File (disabled)`, `hx-post="/api/cookies/twitter/toggle"`, `>Enable<`} {
		if !strings.Contains(body, want) {
			t.Fatalf("toggle response missing %q:\n%s", want, body)
		}
	}
}

func TestHandleSetCookieBrowserRequeuesAuthFailedDownloads(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.CookiesDir = t.TempDir()
	now := int64(1700000000000)
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES
			('instagram_sample', 'Instagram Sample', 'instagram')
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := srv.db.FollowChannel("instagram_sample"); err != nil {
		t.Fatalf("follow channel: %v", err)
	}
	if _, err := srv.db.ReconcileVideoDesires(db.VideoDesireSnapshot{
		SourceChannelID: "instagram_sample",
		Component:       "posts",
		Items: []db.VideoDesire{{
			VideoID: "sample_instagram_auth_failed", OwnerChannelID: "instagram_sample",
			Title: "Auth Failed", Lane: db.DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatalf("reconcile desire: %v", err)
	}
	claimed, ok, err := srv.db.ClaimDownloadWork("test", db.DownloadLaneCurrent, "instagram", now, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim download work = %v, %v, %v", claimed, ok, err)
	}
	if err := srv.db.RetryDownloadWork(claimed.VideoID, claimed.LeaseOwner, "auth", "login required", time.Hour, now); err != nil {
		t.Fatalf("retry auth work: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/cookies/instagram/browser", strings.NewReader(`{"browser":"firefox"}`))
	req.SetPathValue("platform", "instagram")
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleSetCookieBrowser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var status, kind, msg string
	var retries int
	var nextAttempt int64
	if err := srv.db.QueryRow(`
		SELECT status, retry_count, COALESCE(last_error_kind,''),
		       COALESCE(last_error,''), next_attempt_at_ms
		  FROM download_queue
		 WHERE video_id='sample_instagram_auth_failed'
	`).Scan(&status, &retries, &kind, &msg, &nextAttempt); err != nil {
		t.Fatalf("query queue row: %v", err)
	}
	if status != "pending" || retries != 0 || kind != "" || msg != "" || nextAttempt != 0 {
		t.Fatalf("row = status=%q retries=%d kind=%q msg=%q next=%d, want reset pending row",
			status, retries, kind, msg, nextAttempt)
	}
}

func TestCookieChangeRequeuesAuthFailedXContent(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.CookiesDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(srv.cfg.CookiesDir, "twitter_cookies.txt"), []byte("cookies"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := db.Asset{
		AssetID:   db.BuildAssetID("twitter", "tweet", "sample_post", "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post",
		SourceURL: "https://example.test/auth.jpg", State: db.AssetStateQueued,
	}
	if err := srv.db.DeclareAsset(asset, 1000); err != nil {
		t.Fatal(err)
	}
	claimed, err := srv.db.ClaimContentAssetDownloadBatch(db.LeaseOptions{
		Owner: "sample-worker", NowMs: 1001, LeaseMs: time.Minute.Milliseconds(), Limit: 1,
	}, true, db.DownloadLaneBackfill)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, err = %v", claimed, err)
	}
	if err := srv.db.MarkContentAssetFailed(
		asset.AssetID, asset.AssetKind, "sample-worker", download.ErrorKindAuth, "login required", 1002,
	); err != nil {
		t.Fatal(err)
	}

	srv.resetDownloadAuthFailuresAfterCookieChange("twitter")

	recovered, err := srv.db.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.State != db.AssetStateQueued || recovered.Attempts != 0 || recovered.NextAttemptAtMs != 0 || recovered.LastErrorKind != "" {
		t.Fatalf("recovered content asset = %+v", recovered)
	}
}

func TestHandleUploadCookieAcceptsMultipleFiles(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.CookiesDir = t.TempDir()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, item := range []struct {
		name string
		data string
	}{
		{name: "first.txt", data: "cookie-one"},
		{name: "second account.txt", data: "cookie-two"},
	} {
		part, err := writer.CreateFormFile("file", item.name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write([]byte(item.data)); err != nil {
			t.Fatalf("Write part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/cookies/twitter", &body)
	req.SetPathValue("platform", "twitter")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleUploadCookie(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	candidates := download.DiscoverCookieFiles(srv.cfg.CookiesDir, "twitter")
	if len(candidates) != 2 {
		t.Fatalf("cookie candidates = %#v, want 2", candidates)
	}
	var names []string
	for _, candidate := range candidates {
		names = append(names, filepath.Base(candidate.Path))
	}
	sort.Strings(names)
	wantNames := []string{"twitter_cookies.txt", "twitter_cookies_second_account.txt"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("cookie filenames = %#v, want %#v", names, wantNames)
	}
	if !strings.Contains(rec.Body.String(), ">2 files active<") {
		t.Fatalf("upload response should show active file count:\n%s", rec.Body.String())
	}
}

func TestHandleDeleteCookieRemovesAllDiscoveredFiles(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.CookiesDir = t.TempDir()
	for _, name := range []string{"twitter_cookies.txt", "twitter_cookies_extra.txt"} {
		if err := os.WriteFile(filepath.Join(srv.cfg.CookiesDir, name), []byte("cookies"), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	req := httptest.NewRequest("DELETE", "/api/cookies/twitter", nil)
	req.SetPathValue("platform", "twitter")
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleDeleteCookie(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := download.DiscoverCookieFiles(srv.cfg.CookiesDir, "twitter"); len(got) != 0 {
		t.Fatalf("cookie candidates after delete = %#v, want none", got)
	}
	if !strings.Contains(rec.Body.String(), `>Not set<`) {
		t.Fatalf("delete response should show unset row:\n%s", rec.Body.String())
	}
}

func TestNormalizeSettingsUpdate_ClampsTranslateLookaheadAndBackend(t *testing.T) {
	body := map[string]string{
		"translate_backend":        "api",
		"translate_auto_mode":      "weird",
		"translate_auto_lookahead": "500",
	}
	normalizeSettingsUpdate(body)
	if got := body["translate_backend"]; got != "none" {
		t.Errorf("translate_backend = %q, want none", got)
	}
	if got := body["translate_auto_mode"]; got != "lazy" {
		t.Errorf("translate_auto_mode = %q, want lazy", got)
	}
	if got := body["translate_auto_lookahead"]; got != "100" {
		t.Errorf("translate_auto_lookahead = %q, want 100", got)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readZipEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open export zip: %v", err)
	}
	entries := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", f.Name, err)
		}
		entries[f.Name] = data
	}
	return entries
}
