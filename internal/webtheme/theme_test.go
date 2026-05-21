package webtheme

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuiltInThemeCatalogAndAccentNormalization(t *testing.T) {
	wantIDs := []string{
		"ayu-dark",
		"catppuccin-frappe",
		"catppuccin-latte",
		"catppuccin-macchiato",
		"catppuccin-mocha",
		"dracula",
		"github-dark",
		"github-light",
		"green-eyes",
		"gruvbox-dark",
		"nord",
		"occult-umbral",
		"solarized-dark",
		"system",
		"tokyo-night",
	}
	if got := SortedThemeIDs(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("SortedThemeIDs() = %#v, want %#v", got, wantIDs)
	}

	if got := NormalizeAccentHex("github-dark", "#58A6FF"); got != "#58a6ff" {
		t.Fatalf("NormalizeAccentHex uppercase = %q, want #58a6ff", got)
	}
	if got := NormalizeAccentHex("github-dark", "not-a-color"); got != "#58a6ff" {
		t.Fatalf("NormalizeAccentHex fallback = %q, want GitHub Dark default", got)
	}
}

func TestDefaultThemeFollowsSystemColorScheme(t *testing.T) {
	got := NormalizeSettings(Settings{})
	if got.ThemeID != SystemThemeID {
		t.Fatalf("ThemeID = %q, want %q", got.ThemeID, SystemThemeID)
	}

	css := CSS(got)
	for _, want := range []string{
		`color-scheme: light dark;`,
		`--web-theme-id: "system";`,
		`--bg-primary: #1e1e2e;`,
		`@media (prefers-color-scheme: light)`,
		`--bg-primary: #eff1f5;`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("system CSS missing %q:\n%s", want, css)
		}
	}
}

func TestThemeSnapshotIncludesSystemLightAndDarkTokens(t *testing.T) {
	snapshot := ThemeSnapshot(Settings{})
	if snapshot.ThemeID != SystemThemeID {
		t.Fatalf("ThemeID = %q, want %q", snapshot.ThemeID, SystemThemeID)
	}
	if snapshot.ColorScheme != "light dark" {
		t.Fatalf("ColorScheme = %q, want light dark", snapshot.ColorScheme)
	}
	if snapshot.LightTokens == nil || snapshot.DarkTokens == nil {
		t.Fatalf("system snapshot should include light and dark tokens: %#v", snapshot)
	}
	if snapshot.LightTokens.Dark {
		t.Fatalf("light tokens marked dark")
	}
	if !snapshot.DarkTokens.Dark {
		t.Fatalf("dark tokens marked light")
	}
	if snapshot.LightTokens.Base != "#eff1f5" {
		t.Fatalf("light base = %q, want Catppuccin Latte base", snapshot.LightTokens.Base)
	}
	if snapshot.DarkTokens.Base != "#1e1e2e" {
		t.Fatalf("dark base = %q, want Catppuccin Mocha base", snapshot.DarkTokens.Base)
	}
	if snapshot.Tokens.Base != snapshot.DarkTokens.Base {
		t.Fatalf("default tokens base = %q, want dark token base %q", snapshot.Tokens.Base, snapshot.DarkTokens.Base)
	}
}

func TestNormalizeSettingsFallsBackToDefaultThemeAccent(t *testing.T) {
	got := NormalizeSettings(Settings{
		ThemeID:   "missing-theme",
		AccentHex: "not-a-color",
		CustomCSS: strings.Repeat("a", MaxCustomCSSBytes+32),
	})

	if got.ThemeID != DefaultThemeID {
		t.Fatalf("ThemeID = %q, want %q", got.ThemeID, DefaultThemeID)
	}
	if got.AccentHex != "#f38ba8" {
		t.Fatalf("AccentHex = %q, want Catppuccin Mocha red", got.AccentHex)
	}
	if len(got.CustomCSS) != MaxCustomCSSBytes {
		t.Fatalf("CustomCSS length = %d, want cap %d", len(got.CustomCSS), MaxCustomCSSBytes)
	}
}

func TestCSSUsesThemeSurfacesCustomAccentAndReadableOnAccent(t *testing.T) {
	css := CSS(Settings{
		ThemeID:   "dracula",
		AccentHex: "#50fa7b",
	})

	checks := []string{
		`--bg-primary: #282a36;`,
		`--accent-primary: #50fa7b;`,
		`--accent-primary-rgb: 80, 250, 123;`,
		`--on-accent: #11111b;`,
		`--media-range-bar-color: rgba(var(--accent-primary-rgb), 0.5);`,
	}
	for _, want := range checks {
		if !strings.Contains(css, want) {
			t.Fatalf("theme CSS missing %q:\n%s", want, css)
		}
	}
}

func TestOccultUmbralUsesNoctaliaPalette(t *testing.T) {
	css := CSS(Settings{ThemeID: "occult-umbral"})

	for _, want := range []string{
		`--web-theme-id: "occult-umbral";`,
		`--bg-primary: #0a0a12;`,
		`--bg-secondary: #14141e;`,
		`--text-primary: #e4ded2;`,
		`--accent-primary: #8b2e2e;`,
		`--accent-secondary: #a83a3a;`,
		`--success: #8baa82;`,
		`--error: #c25b5b;`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("Occult Umbral CSS missing %q:\n%s", want, css)
		}
	}
}

func TestCSSExposesReadableHandleToken(t *testing.T) {
	for _, tc := range []struct {
		themeID string
		want    string
	}{
		{"solarized-dark", `--text-handle: #93a1a1;`},
		{"github-light", `--text-handle: #57606a;`},
	} {
		t.Run(tc.themeID, func(t *testing.T) {
			css := CSS(Settings{ThemeID: tc.themeID})
			if !strings.Contains(css, tc.want) {
				t.Fatalf("theme CSS missing readable handle token %q:\n%s", tc.want, css)
			}
		})
	}
}

func TestCSSExposesContrastSafeMutedAndHandleTokens(t *testing.T) {
	for _, theme := range AllThemes() {
		if theme.ID == SystemThemeID {
			continue
		}
		t.Run(theme.ID, func(t *testing.T) {
			css := CSS(Settings{ThemeID: theme.ID})
			backgrounds := []struct {
				name string
				hex  string
			}{
				{"base", theme.Base},
				{"mantle", theme.Mantle},
				{"surface0", theme.Surface0},
			}

			for _, token := range []string{"--text-muted", "--status-muted"} {
				value := extractCSSVar(t, css, token)
				for _, bg := range backgrounds {
					if got := contrastRatio(mustRGB(value), mustRGB(bg.hex)); got < 3.0 {
						t.Fatalf("%s = %s contrast on %s %s = %.2f, want >= 3.0", token, value, bg.name, bg.hex, got)
					}
				}
			}

			handle := extractCSSVar(t, css, "--text-handle")
			for _, bg := range backgrounds {
				if got := contrastRatio(mustRGB(handle), mustRGB(bg.hex)); got < 4.5 {
					t.Fatalf("--text-handle = %s contrast on %s %s = %.2f, want >= 4.5", handle, bg.name, bg.hex, got)
				}
			}
		})
	}
}

func TestCSSAppendsRawCustomCSSAfterVariables(t *testing.T) {
	custom := ".feed-card { border-color: hotpink; }"
	css := CSS(Settings{
		ThemeID:   "github-dark",
		AccentHex: "#58a6ff",
		CustomCSS: custom,
	})

	accentIndex := strings.Index(css, `--accent-primary: #58a6ff;`)
	customIndex := strings.Index(css, custom)
	if accentIndex < 0 || customIndex < 0 {
		t.Fatalf("expected generated variables and custom CSS:\n%s", css)
	}
	if customIndex < accentIndex {
		t.Fatalf("custom CSS should be appended after generated variables:\n%s", css)
	}
}

func TestCatppuccinAccentChoicesOnlyForCatppuccinThemes(t *testing.T) {
	choices := CatppuccinAccentChoices("catppuccin-macchiato")
	if len(choices) != 14 {
		t.Fatalf("choice count = %d, want 14", len(choices))
	}

	var mauve string
	for _, choice := range choices {
		if choice.ID == "mauve" {
			mauve = choice.Hex
			break
		}
	}
	if mauve != "#c6a0f6" {
		t.Fatalf("Macchiato mauve = %q, want #c6a0f6", mauve)
	}

	if got := CatppuccinAccentChoices("dracula"); len(got) != 0 {
		t.Fatalf("non-Catppuccin theme returned %d Catppuccin accents", len(got))
	}
}

func TestThemeableHandleAssetsUseSemanticTokens(t *testing.T) {
	styleCSS := readRepoFile(t, "static", "style.css")
	profileCSS := readRepoFile(t, "static", "css", "profile-card.css")

	for _, selector := range []string{".channel-handle", ".feed-author-handle", ".story-channel-handle"} {
		assertRuleContains(t, styleCSS, selector, "var(--text-handle)")
	}
	assertRuleContains(t, styleCSS, ".retweeter-handle", "var(--text-handle)")
	assertRuleContains(t, styleCSS, "#logs-modal .logs-tab", "var(--text-secondary)")
	assertRuleContains(t, styleCSS, "#logs-modal .log-line .log-ts", "var(--text-muted)")
	assertRuleContains(t, styleCSS, "#logs-modal .feed-source-table td", "var(--text-secondary)")
	assertRuleContains(t, styleCSS, "#logs-modal .feed-handle-link", "var(--text-handle)")
	assertRuleContains(t, profileCSS, ".profile-card", "--profile-card-surface: rgb(var(--surface-soft-rgb))")
	assertRuleContains(t, profileCSS, ".profile-card", "background: var(--profile-card-surface)")
	assertRuleContains(t, profileCSS, ".profile-card-avatar", "var(--profile-card-surface)")
	assertRuleContains(t, profileCSS, ".profile-card-handle", "var(--text-handle")
	assertRuleContains(t, profileCSS, ".profile-card-website a", "overflow-wrap: anywhere")
	assertRuleContains(t, profileCSS, ".profile-card-bio .feed-inline-link", "overflow-wrap: anywhere")

	for _, forbidden := range []string{"--surface-1", "--text-1", "--text-2"} {
		if strings.Contains(profileCSS, forbidden) {
			t.Fatalf("profile-card.css should not depend on legacy token %s", forbidden)
		}
	}
}

func TestProfileHoverStacksAboveFeedMediaOverlay(t *testing.T) {
	styleCSS := readRepoFile(t, "static", "style.css")
	profileCSS := readRepoFile(t, "static", "css", "profile-card.css")

	assertRuleContains(t, styleCSS, ".feed-media-overlay", "z-index: 40000")
	assertRuleContains(t, profileCSS, ".profile-card--hover", "z-index: 45000")
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

func extractCSSVar(t *testing.T, css, name string) string {
	t.Helper()
	prefix := name + ":"
	for _, line := range strings.Split(css, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, prefix), ";"))
		if value == "" {
			t.Fatalf("CSS variable %s was empty in:\n%s", name, css)
		}
		return value
	}
	t.Fatalf("CSS variable %s not found in:\n%s", name, css)
	return ""
}

func assertRuleContains(t *testing.T, css, selector, want string) {
	t.Helper()
	offset := 0
	for {
		idx := strings.Index(css[offset:], selector)
		if idx < 0 {
			break
		}
		idx += offset
		end := strings.Index(css[idx:], "}")
		if end < 0 {
			t.Fatalf("CSS selector %s has no closing brace", selector)
		}
		rule := css[idx : idx+end]
		if strings.Contains(rule, want) {
			return
		}
		offset = idx + len(selector)
	}
	t.Fatalf("CSS selector %s missing %q", selector, want)
}
