package web

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
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

func TestHandleUpdateSettingsRejectsNonAdmin(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("", "web_theme_id", "dracula"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("", "web_theme_accent", "#50fa7b"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("", "web_custom_css", ".before { color: red; }"); err != nil {
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

func TestHandleThemeCSSServesPersistedThemeAsNoStoreCSS(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("", "web_theme_id", "dracula"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("", "web_theme_accent", "#50fa7b"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetSetting("", "web_custom_css", ".feed-card { border-color: hotpink; }"); err != nil {
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

func TestHandleConfigExportFullIncludesBookmarkedMediaAndAvatars(t *testing.T) {
	srv := newTestServer(t)

	avatarRelPath := filepath.Join("thumbnails", "avatars", "channel_alpha.jpg")
	avatarAbsPath := filepath.Join(srv.cfg.DataDir, avatarRelPath)
	if err := os.MkdirAll(filepath.Dir(avatarAbsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll avatar: %v", err)
	}
	if err := os.WriteFile(avatarAbsPath, []byte("avatar-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile avatar: %v", err)
	}

	mediaRelPath := filepath.Join("media", "youtube", "channel_alpha", "booked_video.mp4")
	mediaAbsPath := filepath.Join(srv.cfg.DataDir, mediaRelPath)
	if err := os.MkdirAll(filepath.Dir(mediaAbsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll media: %v", err)
	}
	if err := os.WriteFile(mediaAbsPath, []byte("bookmarked-video-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}

	feedRelPath := filepath.Join("media", "twitter", "author", "post_1_0.jpg")
	feedAbsPath := filepath.Join(srv.cfg.DataDir, feedRelPath)
	if err := os.MkdirAll(filepath.Dir(feedAbsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll feed media: %v", err)
	}
	if err := os.WriteFile(feedAbsPath, []byte("bookmarked-feed-image"), 0o644); err != nil {
		t.Fatalf("WriteFile feed media: %v", err)
	}

	if err := srv.db.WithWrite(func(tx *sql.Tx) error {
		statements := []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO channels (channel_id, name, url, platform)
				VALUES ('channel_alpha', 'Channel Alpha', 'https://example.com/channel', 'youtube')`, nil},
			{`INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
				VALUES ('booked_video', 'channel_alpha', 'Booked Video', 12, ?, 1000)`, []any{mediaAbsPath}},
			{`INSERT INTO videos (video_id, channel_id, title, duration, published_at)
				VALUES ('post_1', 'twitter_author', 'X post author', 0, 1000)`, nil},
			{`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type)
				VALUES ('feed_media', 'post_1', 0, ?, 'photo')`, []any{feedRelPath}},
			{`INSERT INTO bookmark_categories (id, user_id, name, created_at)
				VALUES (7, 'admin', 'Saved', 1000)`, nil},
			{`INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, bookmarked_at)
				VALUES ('admin', 'booked_video', 7, 'Watch Later', 2000)`, nil},
			{`INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, bookmarked_at)
				VALUES ('admin', 'post_1', 7, 'Reference', 3000)`, nil},
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

	wantMedia := map[string]string{
		"media/avatars/channel_alpha.jpg":      "avatar-bytes",
		"media/bookmarks/booked_video/000.mp4": "bookmarked-video-bytes",
		"media/bookmarks/post_1/000.jpg":       "bookmarked-feed-image",
	}
	for name, want := range wantMedia {
		got, ok := entries[name]
		if !ok {
			t.Fatalf("missing media entry %s; entries=%v", name, mapKeys(entries))
		}
		if string(got) != want {
			t.Fatalf("%s content = %q, want %q", name, string(got), want)
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
		"rsshub.env":                  "RSSHUB_SECRET=example\n",
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
	if _, ok := entries["runtime.json"]; !ok {
		t.Fatalf("runtime.json missing; entries=%v", mapKeys(entries))
	}
	rawJSON := entries["export.json"]
	var payload map[string]any
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		t.Fatalf("export.json invalid: %v", err)
	}
	if got := payload["user_id"]; got != "admin" {
		t.Fatalf("export user_id = %v, want admin", got)
	}
}

func TestHandleConfigExportSavesToBackupDirWhenConfigured(t *testing.T) {
	srv := newTestServer(t)
	backupDir := t.TempDir()
	if err := srv.db.SetSetting("", "backup_dir", backupDir); err != nil {
		t.Fatalf("SetSetting backup_dir: %v", err)
	}
	if err := srv.db.SetSetting("", "export_test_key", "export_test_val"); err != nil {
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

func TestHandleConfigExportFullSavesZipToBackupDirWhenConfigured(t *testing.T) {
	srv := newTestServer(t)
	backupDir := t.TempDir()
	if err := srv.db.SetSetting("", "backup_dir", backupDir); err != nil {
		t.Fatalf("SetSetting backup_dir: %v", err)
	}

	mediaRelPath := filepath.Join("media", "youtube", "channel_alpha", "booked_video.mp4")
	mediaAbsPath := filepath.Join(srv.cfg.DataDir, mediaRelPath)
	if err := os.MkdirAll(filepath.Dir(mediaAbsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll media: %v", err)
	}
	if err := os.WriteFile(mediaAbsPath, []byte("bookmarked-video-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if err := srv.db.WithWrite(func(tx *sql.Tx) error {
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO channels (channel_id, name, url, platform)
				VALUES ('channel_alpha', 'Channel Alpha', 'https://example.com/channel', 'youtube')`, nil},
			{`INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
				VALUES ('booked_video', 'channel_alpha', 'Booked Video', 12, ?, 1000)`, []any{mediaAbsPath}},
			{`INSERT INTO bookmark_categories (id, user_id, name, created_at)
				VALUES (7, 'admin', 'Saved', 1000)`, nil},
			{`INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, bookmarked_at)
				VALUES ('admin', 'booked_video', 7, 'Watch Later', 2000)`, nil},
		} {
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
	if string(entries["media/bookmarks/booked_video/000.mp4"]) != "bookmarked-video-bytes" {
		t.Fatalf("saved full export missing bookmarked media; entries=%v", mapKeys(entries))
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v; body=%s", err, rec.Body.String())
	}
	if body["saved"] != true || body["path"] == "" {
		t.Fatalf("saved response = %#v, want saved path", body)
	}
}

func TestHandleConfigImportFullZipRestoresMetadataBookmarkedMediaAndAvatars(t *testing.T) {
	src := newTestServer(t)
	dst := newTestServer(t)

	mediaRelPath := filepath.Join("media", "youtube", "channel_alpha", "booked_video.mp4")
	mediaAbsPath := filepath.Join(src.cfg.DataDir, mediaRelPath)
	if err := os.MkdirAll(filepath.Dir(mediaAbsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll media: %v", err)
	}
	if err := os.WriteFile(mediaAbsPath, []byte("bookmarked-video-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	avatarPath := filepath.Join(src.cfg.DataDir, "thumbnails", "avatars", "channel_alpha.jpg")
	if err := os.MkdirAll(filepath.Dir(avatarPath), 0o755); err != nil {
		t.Fatalf("MkdirAll avatar: %v", err)
	}
	if err := os.WriteFile(avatarPath, []byte("avatar-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile avatar: %v", err)
	}
	if err := src.db.WithWrite(func(tx *sql.Tx) error {
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO channels (channel_id, name, url, platform)
				VALUES ('channel_alpha', 'Channel Alpha', 'https://example.com/channel', 'youtube')`, nil},
			{`INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
				VALUES ('booked_video', 'channel_alpha', 'Booked Video', 12, ?, 1000)`, []any{mediaAbsPath}},
			{`INSERT INTO bookmark_categories (id, user_id, name, created_at)
				VALUES (7, 'admin', 'Saved', 1000)`, nil},
			{`INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, bookmarked_at)
				VALUES ('admin', 'booked_video', 7, 'Watch Later', 2000)`, nil},
		} {
			if _, err := tx.Exec(stmt.sql, stmt.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed export fixture: %v", err)
	}

	exportReq := httptest.NewRequest("GET", "/api/config/export-full", nil)
	exportReq = exportReq.WithContext(contextWithUser(exportReq, "admin", "admin"))
	exportRec := httptest.NewRecorder()
	src.handleConfigExportFull(exportRec, exportReq)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", exportRec.Code, exportRec.Body.String())
	}

	body, contentType := multipartBody(t, "file", "igloo-full.zip", exportRec.Body.Bytes())
	importReq := httptest.NewRequest(http.MethodPost, "/api/config/import", body)
	importReq.Header.Set("Content-Type", contentType)
	importReq = importReq.WithContext(contextWithUser(importReq, "admin", "admin"))
	importRec := httptest.NewRecorder()

	dst.handleConfigImport(importRec, importReq)

	if importRec.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", importRec.Code, importRec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(importRec.Body.Bytes(), &response); err != nil {
		t.Fatalf("import response JSON: %v; body=%s", err, importRec.Body.String())
	}
	if response["format"] != "full_export_zip" {
		t.Fatalf("format = %v, want full_export_zip; response=%#v", response["format"], response)
	}
	if got := int(response["restored_media"].(float64)); got != 2 {
		t.Fatalf("restored_media = %d, want 2; response=%#v", got, response)
	}

	video, err := dst.db.GetVideo("booked_video")
	if err != nil {
		t.Fatalf("GetVideo imported: %v", err)
	}
	if video == nil || video.FilePath == "" {
		t.Fatalf("imported video file_path missing: %#v", video)
	}
	restored, err := os.ReadFile(video.FilePath)
	if err != nil {
		t.Fatalf("read restored media %q: %v", video.FilePath, err)
	}
	if string(restored) != "bookmarked-video-bytes" {
		t.Fatalf("restored media = %q", string(restored))
	}
	if got, err := dst.db.GetMediaFilePath("feed_media", "booked_video", 0); err != nil || got == "" {
		t.Fatalf("feed_media row missing: path=%q err=%v", got, err)
	}
	restoredAvatar, err := os.ReadFile(filepath.Join(dst.cfg.DataDir, "thumbnails", "avatars", "channel_alpha.jpg"))
	if err != nil {
		t.Fatalf("read restored avatar: %v", err)
	}
	if string(restoredAvatar) != "avatar-bytes" {
		t.Fatalf("restored avatar = %q", string(restoredAvatar))
	}
	labels, err := dst.db.GetBookmarkLabels("admin", "")
	if err != nil {
		t.Fatalf("GetBookmarkLabels: %v", err)
	}
	if len(labels) != 1 || labels[0] != "Watch Later" {
		t.Fatalf("labels = %#v, want Watch Later", labels)
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
		rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", f.Name, err)
		}
		entries[f.Name] = data
	}
	return entries
}
