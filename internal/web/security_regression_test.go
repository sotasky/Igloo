package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
)

func TestQuickDownloadRejectsNonAdmin(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/quick-download", strings.NewReader(`{"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}`))
	req = req.WithContext(contextWithUser(req, "bob", "user"))
	rec := httptest.NewRecorder()

	srv.handleQuickDownload(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestQuickDownloadAllowsAdminValidationPath(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/quick-download", strings.NewReader(`{"url":""}`))
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleQuickDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestStopResumeRejectNonAdminWithoutMutatingWorkers(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/stop", nil)
	req = req.WithContext(contextWithUser(req, "bob", "user"))
	rec := httptest.NewRecorder()

	srv.handleStop(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("stop status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if srv.workers.IsStopRequested() {
		t.Fatal("non-admin stop request changed stop state")
	}
	if srv.workers.IsIngestPaused() {
		t.Fatal("non-admin stop request paused ingest")
	}

	srv.workers.SetStopRequested(true)
	srv.workers.SetIngestPaused(true)
	req = httptest.NewRequest(http.MethodPost, "/api/resume", nil)
	req = req.WithContext(contextWithUser(req, "bob", "user"))
	rec = httptest.NewRecorder()

	srv.handleResume(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("resume status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if !srv.workers.IsStopRequested() {
		t.Fatal("non-admin resume request cleared stop state")
	}
	if !srv.workers.IsIngestPaused() {
		t.Fatal("non-admin resume request resumed ingest")
	}
}

func TestAdminCanStopAndResumeWorkers(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/stop", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleStop(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !srv.workers.IsStopRequested() || !srv.workers.IsIngestPaused() {
		t.Fatal("admin stop request did not pause worker state")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/resume", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec = httptest.NewRecorder()

	srv.handleResume(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if srv.workers.IsStopRequested() || srv.workers.IsIngestPaused() {
		t.Fatal("admin resume request did not resume worker state")
	}
}

func TestEnforceAuthRequiresSubtitleAuthentication(t *testing.T) {
	s := &Server{
		cfg:   &config.Config{SecretKey: "test-key"},
		store: sessions.NewCookieStore([]byte("test-key")),
	}
	called := false
	handler := s.enforceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/media/subtitle/clip", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("subtitle route reached handler without authentication")
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

func TestHandleSubtitleRejectsTraversalTrack(t *testing.T) {
	srv := newTestServer(t)
	videoDir := filepath.Join(srv.cfg.DataDir, "videos", "youtube")
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		t.Fatalf("mkdir video dir: %v", err)
	}
	videoPath := filepath.Join(videoDir, "clip.mp4")
	if err := os.WriteFile(videoPath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	secretPath := filepath.Join(srv.cfg.DataDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("do-not-leak"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES ('clip', 'youtube_chan', 'Clip', 10, ?, 1)
	`, videoPath); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/media/subtitle/clip?track=../../secret.txt", nil)
	req.SetPathValue("videoID", "clip")
	rec := httptest.NewRecorder()
	srv.handleSubtitle(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "do-not-leak") {
		t.Fatal("subtitle traversal leaked the target file")
	}
}

func TestHandleSubtitleServesValidTrack(t *testing.T) {
	srv := newTestServer(t)
	videoDir := filepath.Join(srv.cfg.DataDir, "videos", "youtube")
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		t.Fatalf("mkdir video dir: %v", err)
	}
	videoPath := filepath.Join(videoDir, "clip.mp4")
	if err := os.WriteFile(videoPath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoDir, "clip.en.vtt"), []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nhello\n"), 0o644); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, duration, file_path, published_at)
		VALUES ('clip', 'youtube_chan', 'Clip', 10, ?, 1)
	`, videoPath); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/media/subtitle/clip?track=clip.en.vtt", nil)
	req.SetPathValue("videoID", "clip")
	rec := httptest.NewRecorder()
	srv.handleSubtitle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("subtitle body missing valid cue: %q", rec.Body.String())
	}
}

func TestNonAdminCannotUpdateUsers(t *testing.T) {
	srv := newTestServer(t)
	authPath := filepath.Join(t.TempDir(), "auth_users.json")
	srv.cfg.AuthUsersPath = authPath
	if err := auth.SaveUsers(authPath, map[string]auth.UserRecord{
		"admin": {Password: auth.HashPassword("old-admin-pass"), Role: "admin"},
		"bob":   {Password: auth.HashPassword("bob-pass"), Role: "user"},
	}); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}
	auth.InitCache(authPath)

	req := httptest.NewRequest(http.MethodPut, "/api/users/admin", strings.NewReader(`{"password":"new-admin-pass"}`))
	req.SetPathValue("username", "admin")
	req = req.WithContext(contextWithUser(req, "bob", "user"))
	rec := httptest.NewRecorder()
	srv.handleUpdateUser(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	users, err := auth.LoadUsers(authPath)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if auth.VerifyPassword("new-admin-pass", users["admin"].Password) {
		t.Fatal("non-admin request changed the admin password")
	}
	if !auth.VerifyPassword("old-admin-pass", users["admin"].Password) {
		t.Fatal("admin password did not remain unchanged")
	}
}

func TestAdminCanUpdateUsers(t *testing.T) {
	srv := newTestServer(t)
	authPath := filepath.Join(t.TempDir(), "auth_users.json")
	srv.cfg.AuthUsersPath = authPath
	if err := auth.SaveUsers(authPath, map[string]auth.UserRecord{
		"admin": {Password: auth.HashPassword("admin-pass"), Role: "admin"},
		"bob":   {Password: auth.HashPassword("old-bob-pass"), Role: "user"},
	}); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}
	auth.InitCache(authPath)

	req := httptest.NewRequest(http.MethodPut, "/api/users/bob", strings.NewReader(`{"password":"new-bob-pass"}`))
	req.SetPathValue("username", "bob")
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()
	srv.handleUpdateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	users, err := auth.LoadUsers(authPath)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if !auth.VerifyPassword("new-bob-pass", users["bob"].Password) {
		t.Fatal("admin request did not update target user password")
	}
}

func TestNonAdminCannotStageRestoreImport(t *testing.T) {
	srv := newTestServer(t)
	body, contentType := multipartBody(t, "file", "restore.tar.gz", []byte{0x1f, 0x8b, 0x00})
	req := httptest.NewRequest(http.MethodPost, "/api/config/import", body)
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(contextWithUser(req, "bob", "user"))
	rec := httptest.NewRecorder()

	srv.handleConfigImport(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "restore-staging")); !os.IsNotExist(err) {
		t.Fatalf("restore staging should not be created for non-admin, stat err = %v", err)
	}
}

func TestAdminCanImportConfigJSON(t *testing.T) {
	srv := newTestServer(t)
	body, contentType := multipartBody(t, "file", "config.json", []byte(`{"version":1,"settings":{"web_theme_id":"dracula"}}`))
	req := httptest.NewRequest(http.MethodPost, "/api/config/import", body)
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handleConfigImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, _ := srv.db.GetSetting("web_theme_id", ""); got != "dracula" {
		t.Fatalf("web_theme_id = %q, want dracula", got)
	}
}

func multipartBody(t *testing.T, field, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart data: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}
