package i18n

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCatalogLoadsTOMLAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, filepath.Join(dir, "en.toml"), "en", "English", map[string]string{
		"search_global_placeholder": "Search",
		"action_preferences":        "Preferences",
	})
	writeTOML(t, filepath.Join(dir, "tr.toml"), "tr", "Türkçe", map[string]string{
		"search_global_placeholder": "Ara",
	})

	catalog, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := catalog.T("tr", "search_global_placeholder"); got != "Ara" {
		t.Errorf("Turkish search_global_placeholder = %q, want Ara", got)
	}
	if got := catalog.T("tr", "action_preferences"); got != "Preferences" {
		t.Errorf("fallback action_preferences = %q, want Preferences", got)
	}
	if got := catalog.T("de", "search_global_placeholder"); got != "Search" {
		t.Errorf("unsupported language fallback = %q, want Search", got)
	}
}

func TestCatalogLoadsLegacyPO(t *testing.T) {
	dir := t.TempDir()
	writePO(t, filepath.Join(dir, "en.po"), "en", "English", map[string]string{
		"Search": "Search",
	})

	catalog, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := catalog.T("en", "Search"); got != "Search" {
		t.Errorf("legacy PO Search = %q, want Search", got)
	}
}

func TestCatalogLoadsEscapedProperties(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tr.properties")
	data := "# Language: tr\n# Language-Name: Türkçe\n\n" +
		"action_clear = Temizle\n" +
		"label.space\\ key = satır\\u0020bir\\n satır iki\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write properties: %v", err)
	}

	catalog := NewCatalog()
	if err := catalog.LoadPropertiesFile(path); err != nil {
		t.Fatalf("LoadPropertiesFile: %v", err)
	}
	if got := catalog.T("tr", "action_clear"); got != "Temizle" {
		t.Errorf("action_clear = %q, want Temizle", got)
	}
	if got := catalog.T("tr", "label.space key"); got != "satır bir\n satır iki" {
		t.Errorf("escaped value = %q", got)
	}
	if got := catalog.Languages()[1].Name; got != "Türkçe" {
		t.Errorf("language name = %q, want Türkçe", got)
	}
}

func TestCatalogLoadsEscapedTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tr.toml")
	data := "# Language: tr\n# Language-Name: Türkçe\n\n" +
		"action_clear = \"Temizle\"\n" +
		"label_multiline = \"satır bir\\n satır iki\" # comment\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	catalog := NewCatalog()
	if err := catalog.LoadTOMLFile(path); err != nil {
		t.Fatalf("LoadTOMLFile: %v", err)
	}
	if got := catalog.T("tr", "action_clear"); got != "Temizle" {
		t.Errorf("action_clear = %q, want Temizle", got)
	}
	if got := catalog.T("tr", "label_multiline"); got != "satır bir\n satır iki" {
		t.Errorf("escaped value = %q", got)
	}
	if got := catalog.Languages()[1].Name; got != "Türkçe" {
		t.Errorf("language name = %q, want Türkçe", got)
	}
}

func TestMatchAcceptLanguage(t *testing.T) {
	catalog := NewCatalog()
	catalog.messages["tr"] = map[string]string{}

	if got := catalog.MatchAcceptLanguage("de-DE,de;q=0.9,tr;q=0.8,en;q=0.7"); got != "tr" {
		t.Errorf("MatchAcceptLanguage = %q, want tr", got)
	}
	if got := catalog.MatchAcceptLanguage("tr-TR,tr;q=0.9"); got != "tr" {
		t.Errorf("region fallback = %q, want tr", got)
	}
}

func writePO(t *testing.T, path, lang, name string, messages map[string]string) {
	t.Helper()
	data := `msgid ""
msgstr ""
"Language: ` + lang + `\n"
"Language-Name: ` + name + `\n"

`
	for msgid, msgstr := range messages {
		data += `msgid "` + msgid + `"
msgstr "` + msgstr + `"

`
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeProperties(t *testing.T, path, lang, name string, messages map[string]string) {
	t.Helper()
	data := "# Language: " + lang + "\n# Language-Name: " + name + "\n\n"
	for key, msg := range messages {
		data += key + " = " + msg + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeTOML(t *testing.T, path, lang, name string, messages map[string]string) {
	t.Helper()
	data := "# Language: " + lang + "\n# Language-Name: " + name + "\n\n"
	for key, msg := range messages {
		data += key + " = " + strconv.Quote(msg) + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
