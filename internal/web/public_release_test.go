package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
)

func newFirstInstallTestHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	srv := newTestServer(t)
	authPath := filepath.Join(t.TempDir(), "auth_users.json")
	platforms, err := config.ParseEnabledPlatforms("none")
	if err != nil {
		t.Fatal(err)
	}
	srv.cfg.AuthUsersPath = authPath
	srv.cfg.RuntimeConfigPath = filepath.Join(filepath.Dir(authPath), "config.json")
	srv.cfg.EnabledPlatforms = platforms
	srv.cfg.EnabledPlatformSet = map[string]bool{}
	auth.InitCache(authPath)
	return NewServer(srv.db, srv.cfg, srv.workers, func(path string) string {
		return "/static/" + path
	}), authPath
}

func TestLoginSubmitRedirectsToSetupWhenNoUsersExist(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth_users.json")
	auth.InitCache(authPath)

	s := &Server{
		cfg:   &config.Config{SecretKey: "test-secret", AuthUsersPath: authPath},
		store: sessions.NewCookieStore([]byte("test-secret")),
	}
	req := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	s.handleLoginSubmit(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/setup" {
		t.Fatalf("Location = %q, want /setup", got)
	}
}

func TestLoginSubmitRejectsMissingOrInvalidCSRF(t *testing.T) {
	s := newLoginSubmitTestServer(t)

	tests := []struct {
		name       string
		formToken  string
		sessionTok string
	}{
		{name: "missing"},
		{name: "invalid", formToken: "wrong-token", sessionTok: "valid-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{
				"username": {"admin"},
				"password": {"secret-pass"},
			}
			if tt.formToken != "" {
				form.Set("_csrf_token", tt.formToken)
			}
			req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tt.sessionTok != "" {
				req.AddCookie(loginSessionCookie(t, s, tt.sessionTok))
			}
			rec := httptest.NewRecorder()

			s.handleLoginSubmit(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			assertNoAuthSession(t, s, rec)
		})
	}
}

func TestLoginSubmitAcceptsValidCSRF(t *testing.T) {
	s := newLoginSubmitTestServer(t)
	form := url.Values{
		"_csrf_token": {"valid-token"},
		"username":    {"admin"},
		"password":    {"secret-pass"},
	}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(loginSessionCookie(t, s, "valid-token"))
	rec := httptest.NewRecorder()

	s.handleLoginSubmit(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/" {
		t.Fatalf("Location = %q, want /", got)
	}
	assertAuthSession(t, s, rec, "admin")
}

func TestSafeLoginNextRejectsExternalTargets(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"/feed?filter=bookmarked", "/feed?filter=bookmarked"},
		{"https://example.com/feed", "/"},
		{"//example.com/feed", "/"},
		{"/login", "/"},
		{"feed", "/"},
		{`/feed\..\admin`, "/"},
		{"/feed%5c..%5cadmin", "/"},
	}
	for _, tt := range tests {
		if got := safeLoginNext(tt.raw); got != tt.want {
			t.Fatalf("safeLoginNext(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestResolveDataPathUnderRejectsEscapes(t *testing.T) {
	dataDir := t.TempDir()
	layout := testWebConfig(t, dataDir).Storage
	if _, ok := resolveDataPathUnder(layout, "../outside.jpg"); ok {
		t.Fatal("relative escape path was accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.jpg")
	if _, ok := resolveDataPathUnder(layout, outside); ok {
		t.Fatal("absolute outside path was accepted")
	}
	if got, ok := resolveDataPathUnder(layout, "feed_media/item.jpg"); !ok || got != filepath.Join(dataDir, "feed_media", "item.jpg") {
		t.Fatalf("valid data path = %q, %v", got, ok)
	}
}

func TestFirstInstallLoginRedirectsToSetup(t *testing.T) {
	handler, _ := newFirstInstallTestHandler(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newLocalRequest("GET", "/login", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/setup" {
		t.Fatalf("Location = %q, want /setup", got)
	}
}

func TestFirstInstallSetupCreatesAdminAndLogsIn(t *testing.T) {
	handler, authPath := newFirstInstallTestHandler(t)

	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, newLocalRequest("GET", "/setup", nil))
	if setupRec.Code != http.StatusOK {
		t.Fatalf("GET /setup status = %d, body = %s", setupRec.Code, setupRec.Body.String())
	}
	csrfToken := setupCSRFToken(t, setupRec.Body.String())

	form := url.Values{
		"_csrf_token":      {csrfToken},
		"username":         {"admin"},
		"password":         {"correct-horse-battery-staple"},
		"password_confirm": {"correct-horse-battery-staple"},
		"platforms":        {"youtube", "twitter"},
	}
	req := newLocalRequest("POST", "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range setupRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, req)

	if createRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	if got := createRec.Header().Get("Location"); got != "/" {
		t.Fatalf("Location = %q, want /", got)
	}

	users, err := auth.LoadUsers(authPath)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	rec, ok := users["admin"]
	if !ok {
		t.Fatalf("admin user not created: %#v", users)
	}
	if rec.Role != "admin" {
		t.Fatalf("role = %q, want admin", rec.Role)
	}
	if got := strings.Join(rec.Platforms, ","); got != "youtube,twitter" {
		t.Fatalf("platforms = %q", got)
	}
	if !auth.VerifyPassword("correct-horse-battery-staple", rec.Password) {
		t.Fatal("stored admin password did not verify")
	}

	loginReq := newLocalRequest("GET", "/login", nil)
	for _, cookie := range createRec.Result().Cookies() {
		loginReq.AddCookie(cookie)
	}
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusSeeOther || loginRec.Header().Get("Location") != "/" {
		t.Fatalf("logged-in /login = %d Location %q", loginRec.Code, loginRec.Header().Get("Location"))
	}
}

func TestFirstInstallSetupPreservesBootstrapImportedUserData(t *testing.T) {
	srv := newTestServer(t)
	authPath := filepath.Join(t.TempDir(), "auth_users.json")
	platforms, err := config.ParseEnabledPlatforms("none")
	if err != nil {
		t.Fatal(err)
	}
	srv.cfg.AuthUsersPath = authPath
	srv.cfg.RuntimeConfigPath = filepath.Join(filepath.Dir(authPath), "config.json")
	srv.cfg.EnabledPlatforms = platforms
	srv.cfg.EnabledPlatformSet = map[string]bool{}
	auth.InitCache(authPath)
	handler := NewServer(srv.db, srv.cfg, srv.workers, func(path string) string {
		return "/static/" + path
	})

	if _, err := srv.db.ImportConfig(db.ConfigExport{
		Version: db.ConfigExportVersion,
		BookmarkCategories: []db.BookmarkCatExport{{
			Name: "Watch Later",
		}},
		Bookmarks: []db.BookmarkExport{{
			VideoID:      "booked_video",
			CategoryName: "Watch Later",
		}},
		LikedPosts: []db.LikedPostExport{{
			TweetID:      "liked_post",
			AuthorHandle: "author",
			BodyText:     "liked text",
		}},
	}, true); err != nil {
		t.Fatalf("seed bootstrap import: %v", err)
	}

	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, newLocalRequest("GET", "/setup", nil))
	csrfToken := setupCSRFToken(t, setupRec.Body.String())

	form := url.Values{
		"_csrf_token":      {csrfToken},
		"username":         {"alice"},
		"password":         {"correct-horse-battery-staple"},
		"password_confirm": {"correct-horse-battery-staple"},
		"platforms":        {"youtube"},
	}
	req := newLocalRequest("POST", "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range setupRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var categories, bookmarks, likes int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM bookmark_categories WHERE name='Watch Later'`).Scan(&categories); err != nil {
		t.Fatalf("category missing: %v", err)
	}
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id='booked_video'`).Scan(&bookmarks); err != nil {
		t.Fatalf("bookmark missing: %v", err)
	}
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE tweet_id='liked_post'`).Scan(&likes); err != nil {
		t.Fatalf("like missing: %v", err)
	}
	if categories != 1 || bookmarks != 1 || likes != 1 {
		t.Fatalf("bootstrap state = categories %d bookmarks %d likes %d, want one each", categories, bookmarks, likes)
	}
}

func TestFirstInstallSetupRendersOptInPlatforms(t *testing.T) {
	handler, _ := newFirstInstallTestHandler(t)

	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, newLocalRequest("GET", "/setup", nil))
	if setupRec.Code != http.StatusOK {
		t.Fatalf("GET /setup status = %d, body = %s", setupRec.Code, setupRec.Body.String())
	}
	body := setupRec.Body.String()
	for _, want := range []string{
		`name="platforms" value="youtube"`,
		`name="platforms" value="twitter"`,
		`name="platforms" value="tiktok"`,
		`name="platforms" value="instagram"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup page missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `name="platforms" value="youtube" checked`) {
		t.Fatalf("setup platforms should not be preselected: %s", body)
	}
}

func TestFirstInstallSetupRejectsNoPlatforms(t *testing.T) {
	handler, _ := newFirstInstallTestHandler(t)

	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, newLocalRequest("GET", "/setup", nil))
	csrfToken := setupCSRFToken(t, setupRec.Body.String())

	form := url.Values{
		"_csrf_token":      {csrfToken},
		"username":         {"admin"},
		"password":         {"correct-horse-battery-staple"},
		"password_confirm": {"correct-horse-battery-staple"},
	}
	req := newLocalRequest("POST", "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range setupRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /setup status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Select at least one platform.") {
		t.Fatalf("expected platform validation error, got %s", rec.Body.String())
	}
}

func TestFirstInstallSetupRejectsMissingCSRF(t *testing.T) {
	handler, authPath := newFirstInstallTestHandler(t)

	form := url.Values{
		"username":         {"admin"},
		"password":         {"secret-pass"},
		"password_confirm": {"secret-pass"},
	}
	req := newLocalRequest("POST", "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	users, err := auth.LoadUsers(authPath)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users created despite missing CSRF: %#v", users)
	}
}

func TestFirstInstallSetupRejectsRemoteRequest(t *testing.T) {
	handler, authPath := newFirstInstallTestHandler(t)
	req := httptest.NewRequest("GET", "/setup", nil)
	req.RemoteAddr = "198.51.100.10:5555"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	users, err := auth.LoadUsers(authPath)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users unexpectedly created: %#v", users)
	}
}

func TestFirstInstallSetupRejectsForwardedRemoteRequest(t *testing.T) {
	handler, authPath := newFirstInstallTestHandler(t)
	req := newLocalRequest("GET", "/setup", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.10")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	users, err := auth.LoadUsers(authPath)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users unexpectedly created: %#v", users)
	}
}

func TestFirstInstallSetupRejectsForwardedRemoteSubmit(t *testing.T) {
	handler, authPath := newFirstInstallTestHandler(t)

	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, newLocalRequest("GET", "/setup", nil))
	csrfToken := setupCSRFToken(t, setupRec.Body.String())

	form := url.Values{
		"_csrf_token":      {csrfToken},
		"username":         {"admin"},
		"password":         {"correct-horse-battery-staple"},
		"password_confirm": {"correct-horse-battery-staple"},
		"platforms":        {"youtube"},
	}
	req := newLocalRequest("POST", "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", "198.51.100.10")
	for _, cookie := range setupRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	users, err := auth.LoadUsers(authPath)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users unexpectedly created: %#v", users)
	}
}

func TestFirstInstallSetupAllowsForwardedLoopbackRequest(t *testing.T) {
	handler, _ := newFirstInstallTestHandler(t)
	req := newLocalRequest("GET", "/setup", nil)
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestFirstInstallSetupUnavailableAfterUserExists(t *testing.T) {
	handler, authPath := newFirstInstallTestHandler(t)
	if err := auth.SaveUsers(authPath, map[string]auth.UserRecord{
		"admin": {
			Password:  auth.HashPassword("secret-pass"),
			Role:      "admin",
			Platforms: []string{"youtube", "twitter", "tiktok", "instagram"},
		},
	}); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}
	auth.InvalidateCache()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newLocalRequest("GET", "/setup", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("Location = %q, want /login", got)
	}
}

func setupCSRFToken(t *testing.T, html string) string {
	t.Helper()
	match := regexp.MustCompile(`name="_csrf_token" value="([^"]+)"`).FindStringSubmatch(html)
	if len(match) != 2 {
		t.Fatalf("setup page did not contain CSRF input: %s", html)
	}
	return match[1]
}

func newLocalRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:5001"
	return req
}

func newLoginSubmitTestServer(t *testing.T) *Server {
	t.Helper()

	authPath := filepath.Join(t.TempDir(), "auth_users.json")
	if err := auth.SaveUsers(authPath, map[string]auth.UserRecord{
		"admin": {
			Password: auth.HashPassword("secret-pass"),
			Role:     "admin",
		},
	}); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}
	auth.InitCache(authPath)

	return &Server{
		cfg:   &config.Config{SecretKey: "test-secret", AuthUsersPath: authPath},
		store: sessions.NewCookieStore([]byte("test-secret")),
	}
}

func loginSessionCookie(t *testing.T, s *Server, token string) *http.Cookie {
	t.Helper()

	req := httptest.NewRequest("GET", "/login", nil)
	rec := httptest.NewRecorder()
	sess, err := s.store.Get(req, "session")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	sess.Values["csrf_token"] = token
	if err := sess.Save(req, rec); err != nil {
		t.Fatalf("save session: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies = %d, want 1", len(cookies))
	}
	return cookies[0]
}

func assertNoAuthSession(t *testing.T, s *Server, rec *httptest.ResponseRecorder) {
	t.Helper()

	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name != "session" {
			continue
		}
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(cookie)
		sess, err := s.store.Get(req, "session")
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		if username, _ := sess.Values["auth_user"].(string); username != "" {
			t.Fatalf("auth_user = %q, want empty", username)
		}
	}
}

func assertAuthSession(t *testing.T, s *Server, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name != "session" {
			continue
		}
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(cookie)
		sess, err := s.store.Get(req, "session")
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		username, _ := sess.Values["auth_user"].(string)
		if username == want {
			return
		}
		t.Fatalf("auth_user = %q, want %q", username, want)
	}
	t.Fatal("login did not set a session cookie")
}

func TestSubscribeRejectsDisabledPlatform(t *testing.T) {
	platforms, err := config.ParseEnabledPlatforms("youtube")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg: &config.Config{
			EnabledPlatforms:   platforms,
			EnabledPlatformSet: map[string]bool{"youtube": true},
		},
	}
	req := httptest.NewRequest("POST", "/api/subscribe", strings.NewReader(`{"url":"https://x.com/alice","platform":"twitter"}`))
	rec := httptest.NewRecorder()

	s.handleSubscribe(rec, req)

	if rec.Code != 422 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not enabled") {
		t.Fatalf("expected disabled-platform error, got %s", rec.Body.String())
	}
}

func TestPlatformChoicesFollowEnabledPlatforms(t *testing.T) {
	platforms, err := config.ParseEnabledPlatforms("youtube,tiktok")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg: &config.Config{
			EnabledPlatforms:   platforms,
			EnabledPlatformSet: map[string]bool{"youtube": true, "tiktok": true},
		},
	}

	choices := s.platformChoices()
	var values []string
	for _, choice := range choices {
		values = append(values, choice.Value)
	}
	got := strings.Join(values, ",")
	if got != "youtube,tiktok" {
		t.Fatalf("choices = %q", got)
	}
}
