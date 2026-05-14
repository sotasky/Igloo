package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/i18n"
)

func TestI18NCatalogEndpoint(t *testing.T) {
	srv := newTestServer(t)
	srv.i18n = i18n.NewCatalog()
	_ = srv.i18n.LoadTOMLFile(testTOMLFile(t, "tr", map[string]string{"search_global_placeholder": "Ara"}))

	req := httptest.NewRequest("GET", "/api/i18n/catalog?lang=tr", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Language string            `json:"language"`
		Messages map[string]string `json:"messages"`
	}
	if err := decodeInto(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Language != "tr" {
		t.Errorf("language = %q, want tr", body.Language)
	}
	if got := body.Messages["search_global_placeholder"]; got != "Ara" {
		t.Errorf("search_global_placeholder = %q, want Ara", got)
	}
}

func TestRequestLanguageAutoUsesAcceptLanguage(t *testing.T) {
	srv := newTestServer(t)
	srv.i18n = i18n.NewCatalog()
	_ = srv.i18n.LoadTOMLFile(testTOMLFile(t, "tr", map[string]string{"search_global_placeholder": "Ara"}))
	if err := srv.db.SetSetting("", "ui_language", "auto"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	req := httptest.NewRequest("GET", "/feed", nil)
	req.Header.Set("Accept-Language", "de-DE,de;q=0.9,tr-TR;q=0.8,en;q=0.7")

	if got := srv.requestLanguage(req); got != "tr" {
		t.Errorf("requestLanguage = %q, want tr", got)
	}
}

func TestRequestLanguageQueryAutoUsesAcceptLanguage(t *testing.T) {
	srv := newTestServer(t)
	srv.i18n = i18n.NewCatalog()
	_ = srv.i18n.LoadTOMLFile(testTOMLFile(t, "tr", map[string]string{"search_global_placeholder": "Ara"}))

	req := httptest.NewRequest("GET", "/feed?lang=auto", nil)
	req.Header.Set("Accept-Language", "tr-TR,tr;q=0.9,en;q=0.7")

	if got := srv.requestLanguage(req); got != "tr" {
		t.Errorf("requestLanguage = %q, want tr", got)
	}
}

func TestSettingsFormPreviewLanguageUsesCatalogWithoutChangingPersistedSelection(t *testing.T) {
	srv := newTestServer(t)
	srv.i18n = i18n.NewCatalog()
	_ = srv.i18n.LoadTOMLFile(testTOMLFile(t, "tr", map[string]string{
		"action_save_preferences": "Tercihleri kaydet",
		"settings_tab_general":    "Genel",
	}))
	if err := srv.db.SetSetting("", "ui_language", "en"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/settings/form?lang=tr", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()
	srv.handleSettingsForm(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		">Genel<",
		">Tercihleri kaydet<",
		`data-persisted-ui-language="en"`,
		`value="tr" selected`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings form missing %q in:\n%s", want, body)
		}
	}
}

func TestSupportedLanguageChoicesIncludesAutomatic(t *testing.T) {
	srv := newTestServer(t)
	choices := srv.supportedLanguageChoices("en")
	if len(choices) == 0 {
		t.Fatal("expected language choices")
	}
	if choices[0].Code != "auto" {
		t.Fatalf("first language choice = %q, want auto", choices[0].Code)
	}
	if choices[0].Name != "Automatic (browser)" {
		t.Fatalf("automatic language label = %q", choices[0].Name)
	}
}

func testTOMLFile(t *testing.T, lang string, messages map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), lang+".toml")
	data := "# Language: " + lang + "\n# Language-Name: " + lang + "\n\n"
	for key, value := range messages {
		data += key + " = " + strconv.Quote(value) + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	return path
}
