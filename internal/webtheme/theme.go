// Package webtheme resolves the server-backed web theme settings into CSS.
package webtheme

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	SystemThemeID     = "system"
	DefaultThemeID    = "occult-umbral"
	DefaultAccentHex  = "#e6c27a"
	MaxCustomCSSBytes = 64 * 1024
	defaultSecondary  = "#8b2e2e"
)

// Settings are the persisted web-only theme preferences.
type Settings struct {
	ThemeID   string
	AccentHex string
	CustomCSS string
}

// Theme is one built-in web theme.
type Theme struct {
	ID            string
	Label         string
	Catppuccin    bool
	Dark          bool
	DefaultAccent string
	Secondary     string
	Rosewater     string
	Flamingo      string
	Pink          string
	Mauve         string
	Red           string
	Maroon        string
	Peach         string
	Yellow        string
	Green         string
	Teal          string
	Sky           string
	Sapphire      string
	Blue          string
	Lavender      string
	Text          string
	Subtext1      string
	Subtext0      string
	Overlay2      string
	Overlay1      string
	Overlay0      string
	Surface2      string
	Surface1      string
	Surface0      string
	Base          string
	Mantle        string
	Crust         string
}

// AccentChoice is one Catppuccin accent pill option for the selected flavor.
type AccentChoice struct {
	ID    string
	Label string
	Hex   string
}

// Tokens is the normalized color set a client needs to mirror Igloo's web theme
// outside the Igloo page.
type Tokens struct {
	ThemeID     string `json:"theme_id"`
	ColorScheme string `json:"color_scheme"`
	Dark        bool   `json:"dark"`
	Accent      string `json:"accent"`
	OnAccent    string `json:"on_accent"`
	Rosewater   string `json:"rosewater"`
	Flamingo    string `json:"flamingo"`
	Pink        string `json:"pink"`
	Mauve       string `json:"mauve"`
	Red         string `json:"red"`
	Maroon      string `json:"maroon"`
	Peach       string `json:"peach"`
	Yellow      string `json:"yellow"`
	Green       string `json:"green"`
	Teal        string `json:"teal"`
	Sky         string `json:"sky"`
	Sapphire    string `json:"sapphire"`
	Blue        string `json:"blue"`
	Lavender    string `json:"lavender"`
	Text        string `json:"text"`
	Subtext1    string `json:"subtext1"`
	Subtext0    string `json:"subtext0"`
	Overlay2    string `json:"overlay2"`
	Overlay1    string `json:"overlay1"`
	Overlay0    string `json:"overlay0"`
	Surface2    string `json:"surface2"`
	Surface1    string `json:"surface1"`
	Surface0    string `json:"surface0"`
	Base        string `json:"base"`
	Mantle      string `json:"mantle"`
	Crust       string `json:"crust"`
}

// Snapshot is the JSON form of Igloo's resolved web theme. System themes include
// both light and dark token sets so browser-side integrations can honor the
// user's current color-scheme preference.
type Snapshot struct {
	ThemeID     string  `json:"theme_id"`
	ColorScheme string  `json:"color_scheme"`
	Tokens      Tokens  `json:"tokens"`
	LightTokens *Tokens `json:"light_tokens,omitempty"`
	DarkTokens  *Tokens `json:"dark_tokens,omitempty"`
}

// Map returns Snapshot as a JSON object compatible with the web API envelope.
func (s Snapshot) Map() map[string]any {
	body := map[string]any{
		"theme_id":     s.ThemeID,
		"color_scheme": s.ColorScheme,
		"tokens":       s.Tokens,
	}
	if s.LightTokens != nil {
		body["light_tokens"] = s.LightTokens
	}
	if s.DarkTokens != nil {
		body["dark_tokens"] = s.DarkTokens
	}
	return body
}

var themes = []Theme{
	systemTheme(),
	catppuccinMocha(),
	catppuccinMacchiato(),
	catppuccinFrappe(),
	catppuccinLatte(),
	{
		ID: "dracula", Label: "Dracula", Dark: true, DefaultAccent: "#bd93f9", Secondary: "#ff79c6",
		Red: "#ff5555", Maroon: "#ff6e6e", Peach: "#ffb86c", Yellow: "#f1fa8c", Green: "#50fa7b",
		Teal: "#8be9fd", Sky: "#8be9fd", Sapphire: "#8be9fd", Blue: "#6272a4", Mauve: "#bd93f9",
		Pink: "#ff79c6", Lavender: "#bd93f9", Rosewater: "#f8f8f2", Flamingo: "#ffb3d9",
		Text: "#f8f8f2", Subtext1: "#e6e6e6", Subtext0: "#cfcfd7", Overlay2: "#a5adc6",
		Overlay1: "#858ba3", Overlay0: "#6272a4", Surface2: "#6272a4", Surface1: "#44475a",
		Surface0: "#343746", Base: "#282a36", Mantle: "#21222c", Crust: "#191a21",
	},
	{
		ID: "occult-umbral", Label: "Occult Umbral", Dark: true, DefaultAccent: "#8b2e2e", Secondary: "#a83a3a",
		Red: "#c25b5b", Maroon: "#a83a3a", Peach: "#e6c27a", Yellow: "#e6c27a", Green: "#8baa82",
		Teal: "#95b3b0", Sky: "#95b3b0", Sapphire: "#6270a8", Blue: "#6270a8", Mauve: "#9a7398",
		Pink: "#9a7398", Lavender: "#9a7398", Rosewater: "#f2eadf", Flamingo: "#c25b5b",
		Text: "#e4ded2", Subtext1: "#cfc8bb", Subtext0: "#aba397", Overlay2: "#6f6a7d",
		Overlay1: "#5b5b75", Overlay0: "#3a3a4a", Surface2: "#2a2a38", Surface1: "#1c1c28",
		Surface0: "#14141e", Base: "#0a0a12", Mantle: "#0f0f18", Crust: "#040407",
	},
	{
		ID: "ayu-dark", Label: "Ayu Dark", Dark: true, DefaultAccent: "#ffb454", Secondary: "#f07178",
		Red: "#f07178", Maroon: "#ff8f40", Peach: "#ff8f40", Yellow: "#ffee99", Green: "#aad94c",
		Teal: "#95e6cb", Sky: "#59c2ff", Sapphire: "#39bae6", Blue: "#59c2ff", Mauve: "#d2a6ff",
		Pink: "#ff77aa", Lavender: "#b8b4ff", Rosewater: "#e6b673", Flamingo: "#f29e74",
		Text: "#e6e1cf", Subtext1: "#b8cfe6", Subtext0: "#a6accd", Overlay2: "#7f8a99",
		Overlay1: "#6c7680", Overlay0: "#5c6773", Surface2: "#3a4350", Surface1: "#2d3340",
		Surface0: "#1f2430", Base: "#0f1419", Mantle: "#0b1015", Crust: "#090d12",
	},
	{
		ID: "github-dark", Label: "GitHub Dark", Dark: true, DefaultAccent: "#58a6ff", Secondary: "#bc8cff",
		Red: "#f85149", Maroon: "#ff7b72", Peach: "#d29922", Yellow: "#d29922", Green: "#3fb950",
		Teal: "#39c5cf", Sky: "#79c0ff", Sapphire: "#58a6ff", Blue: "#58a6ff", Mauve: "#bc8cff",
		Pink: "#db61a2", Lavender: "#a5d6ff", Rosewater: "#ffa198", Flamingo: "#ffb3ad",
		Text: "#c9d1d9", Subtext1: "#b1bac4", Subtext0: "#8b949e", Overlay2: "#7d8590",
		Overlay1: "#6e7681", Overlay0: "#484f58", Surface2: "#30363d", Surface1: "#21262d",
		Surface0: "#161b22", Base: "#0d1117", Mantle: "#010409", Crust: "#010409",
	},
	{
		ID: "github-light", Label: "GitHub Light", Dark: false, DefaultAccent: "#0969da", Secondary: "#8250df",
		Red: "#cf222e", Maroon: "#a40e26", Peach: "#bc4c00", Yellow: "#9a6700", Green: "#1a7f37",
		Teal: "#3192aa", Sky: "#0969da", Sapphire: "#0969da", Blue: "#0969da", Mauve: "#8250df",
		Pink: "#bf3989", Lavender: "#6639ba", Rosewater: "#953800", Flamingo: "#cf222e",
		Text: "#24292f", Subtext1: "#57606a", Subtext0: "#6e7781", Overlay2: "#8c959f",
		Overlay1: "#afb8c1", Overlay0: "#d0d7de", Surface2: "#d8dee4", Surface1: "#eaeef2",
		Surface0: "#f6f8fa", Base: "#ffffff", Mantle: "#f6f8fa", Crust: "#f0f3f6",
	},
	{
		ID: "green-eyes", Label: "Green Eyes", Dark: true, DefaultAccent: "#a0d57a", Secondary: "#a0cfce",
		Red: "#ffb4ab", Maroon: "#ffdad6", Peach: "#d9e7ca", Yellow: "#bdcbaf", Green: "#a0d57a",
		Teal: "#a0cfce", Sky: "#bbecea", Sapphire: "#a0cfce", Blue: "#a0cfce", Mauve: "#bdcbaf",
		Pink: "#d9e7ca", Lavender: "#bdcbaf", Rosewater: "#d9e7ca", Flamingo: "#ffdad6",
		Text: "#e3e3dc", Subtext1: "#c4c8bb", Subtext0: "#a7ab9f", Overlay2: "#8e9286",
		Overlay1: "#6a6e63", Overlay0: "#44483e", Surface2: "#343531", Surface1: "#292b26",
		Surface0: "#1e201c", Base: "#121410", Mantle: "#1a1c18", Crust: "#0b0d09",
	},
	{
		ID: "nord", Label: "Nord", Dark: true, DefaultAccent: "#88c0d0", Secondary: "#8fbcbb",
		Red: "#bf616a", Maroon: "#d08770", Peach: "#d08770", Yellow: "#ebcb8b", Green: "#a3be8c",
		Teal: "#8fbcbb", Sky: "#88c0d0", Sapphire: "#81a1c1", Blue: "#5e81ac", Mauve: "#b48ead",
		Pink: "#b48ead", Lavender: "#b48ead", Rosewater: "#d8dee9", Flamingo: "#e5e9f0",
		Text: "#eceff4", Subtext1: "#e5e9f0", Subtext0: "#d8dee9", Overlay2: "#a9b5c5",
		Overlay1: "#81a1c1", Overlay0: "#6f7d91", Surface2: "#4c566a", Surface1: "#434c5e",
		Surface0: "#3b4252", Base: "#2e3440", Mantle: "#242933", Crust: "#1f232c",
	},
	{
		ID: "gruvbox-dark", Label: "Gruvbox Dark", Dark: true, DefaultAccent: "#fabd2f", Secondary: "#fe8019",
		Red: "#fb4934", Maroon: "#cc241d", Peach: "#fe8019", Yellow: "#fabd2f", Green: "#b8bb26",
		Teal: "#8ec07c", Sky: "#83a598", Sapphire: "#458588", Blue: "#83a598", Mauve: "#d3869b",
		Pink: "#d3869b", Lavender: "#b16286", Rosewater: "#ebdbb2", Flamingo: "#fbf1c7",
		Text: "#ebdbb2", Subtext1: "#d5c4a1", Subtext0: "#bdae93", Overlay2: "#928374",
		Overlay1: "#7c6f64", Overlay0: "#665c54", Surface2: "#665c54", Surface1: "#504945",
		Surface0: "#3c3836", Base: "#282828", Mantle: "#1d2021", Crust: "#1b1b1b",
	},
	{
		ID: "tokyo-night", Label: "Tokyo Night", Dark: true, DefaultAccent: "#7aa2f7", Secondary: "#bb9af7",
		Red: "#f7768e", Maroon: "#ff9e64", Peach: "#ff9e64", Yellow: "#e0af68", Green: "#9ece6a",
		Teal: "#73daca", Sky: "#7dcfff", Sapphire: "#7aa2f7", Blue: "#7aa2f7", Mauve: "#bb9af7",
		Pink: "#f7768e", Lavender: "#c0caf5", Rosewater: "#cfc9c2", Flamingo: "#ff9e9e",
		Text: "#c0caf5", Subtext1: "#a9b1d6", Subtext0: "#9aa5ce", Overlay2: "#737aa2",
		Overlay1: "#565f89", Overlay0: "#414868", Surface2: "#414868", Surface1: "#30344a",
		Surface0: "#24283b", Base: "#1a1b26", Mantle: "#16161e", Crust: "#11111a",
	},
	{
		ID: "solarized-dark", Label: "Solarized Dark", Dark: true, DefaultAccent: "#268bd2", Secondary: "#6c71c4",
		Red: "#dc322f", Maroon: "#cb4b16", Peach: "#cb4b16", Yellow: "#b58900", Green: "#859900",
		Teal: "#2aa198", Sky: "#268bd2", Sapphire: "#268bd2", Blue: "#268bd2", Mauve: "#6c71c4",
		Pink: "#d33682", Lavender: "#6c71c4", Rosewater: "#eee8d5", Flamingo: "#fdf6e3",
		Text: "#839496", Subtext1: "#93a1a1", Subtext0: "#657b83", Overlay2: "#586e75",
		Overlay1: "#586e75", Overlay0: "#073642", Surface2: "#174652", Surface1: "#0d3a45",
		Surface0: "#073642", Base: "#002b36", Mantle: "#00212b", Crust: "#001a22",
	},
}

var themeByID = func() map[string]Theme {
	out := make(map[string]Theme, len(themes))
	for _, theme := range themes {
		out[theme.ID] = theme
	}
	return out
}()

// AllThemes returns the built-in themes in UI order.
func AllThemes() []Theme {
	out := make([]Theme, len(themes))
	copy(out, themes)
	return out
}

// Lookup returns a theme and whether it exists.
func Lookup(id string) (Theme, bool) {
	theme, ok := themeByID[strings.ToLower(strings.TrimSpace(id))]
	return theme, ok
}

// NormalizeThemeID returns a supported theme ID.
func NormalizeThemeID(id string) string {
	if theme, ok := Lookup(id); ok {
		return theme.ID
	}
	return DefaultThemeID
}

// NormalizeAccentHex returns #rrggbb, falling back to the selected theme default.
func NormalizeAccentHex(themeID, raw string) string {
	if hex, ok := normalizeHex(raw); ok {
		return hex
	}
	if theme, ok := Lookup(themeID); ok {
		return strings.ToLower(theme.DefaultAccent)
	}
	return DefaultAccentHex
}

// NormalizeCustomCSS applies the stored custom CSS size cap.
func NormalizeCustomCSS(raw string) string {
	if len(raw) <= MaxCustomCSSBytes {
		return raw
	}
	return strings.ToValidUTF8(raw[:MaxCustomCSSBytes], "")
}

// NormalizeSettings normalizes the persisted web theme settings as a unit.
func NormalizeSettings(settings Settings) Settings {
	themeID := NormalizeThemeID(settings.ThemeID)
	accentHex := NormalizeAccentHex(themeID, settings.AccentHex)
	if _, themeOK := Lookup(settings.ThemeID); !themeOK {
		if _, accentOK := normalizeHex(settings.AccentHex); !accentOK {
			accentHex = DefaultAccentHex
		}
	}
	return Settings{
		ThemeID:   themeID,
		AccentHex: accentHex,
		CustomCSS: NormalizeCustomCSS(settings.CustomCSS),
	}
}

// CatppuccinAccentChoices returns the 14 official accent choices for a
// Catppuccin flavor, or no choices for non-Catppuccin themes.
func CatppuccinAccentChoices(themeID string) []AccentChoice {
	theme, ok := Lookup(themeID)
	if !ok || !theme.Catppuccin {
		return nil
	}
	choices := []AccentChoice{
		{"rosewater", "Rosewater", theme.Rosewater},
		{"flamingo", "Flamingo", theme.Flamingo},
		{"pink", "Pink", theme.Pink},
		{"mauve", "Mauve", theme.Mauve},
		{"red", "Red", theme.Red},
		{"maroon", "Maroon", theme.Maroon},
		{"peach", "Peach", theme.Peach},
		{"yellow", "Yellow", theme.Yellow},
		{"green", "Green", theme.Green},
		{"teal", "Teal", theme.Teal},
		{"sky", "Sky", theme.Sky},
		{"sapphire", "Sapphire", theme.Sapphire},
		{"blue", "Blue", theme.Blue},
		{"lavender", "Lavender", theme.Lavender},
	}
	for i := range choices {
		choices[i].Hex = strings.ToLower(choices[i].Hex)
	}
	return choices
}

// CSS renders the generated web theme CSS.
func CSS(settings Settings) string {
	normalized := NormalizeSettings(settings)
	if normalized.ThemeID == SystemThemeID {
		return systemCSS(normalized)
	}
	theme, ok := Lookup(normalized.ThemeID)
	if !ok {
		theme, _ = Lookup(DefaultThemeID)
	}
	accent := normalized.AccentHex
	secondary := deriveSecondary(theme, accent)
	onAccent := onAccent(accent)
	accentRGB := mustRGB(accent)
	secondaryRGB := mustRGB(secondary)

	var b strings.Builder
	b.WriteString("/* Generated Igloo web theme. */\n")
	b.WriteString(":root {\n")
	writeVar(&b, "color-scheme", colorScheme(theme))
	writeVar(&b, "--web-theme-id", strconv.Quote(theme.ID))
	writeColorVars(&b, theme, accent, secondary, onAccent, accentRGB, secondaryRGB)
	b.WriteString("}\n")
	appendCustomCSS(&b, normalized.CustomCSS)
	return b.String()
}

// ThemeSnapshot resolves persisted theme settings to a client-friendly JSON
// payload. Custom CSS is intentionally omitted; the payload represents the
// normalized theme tokens, not arbitrary page CSS.
func ThemeSnapshot(settings Settings) Snapshot {
	normalized := NormalizeSettings(settings)
	if normalized.ThemeID == SystemThemeID {
		darkTheme := catppuccinMocha()
		lightTheme := catppuccinLatte()
		darkTokens := tokensForTheme(SystemThemeID, darkTheme, normalized.AccentHex, "light dark")
		lightTokens := tokensForTheme(SystemThemeID, lightTheme, normalized.AccentHex, "light dark")
		return Snapshot{
			ThemeID:     SystemThemeID,
			ColorScheme: "light dark",
			Tokens:      darkTokens,
			DarkTokens:  &darkTokens,
			LightTokens: &lightTokens,
		}
	}

	theme, ok := Lookup(normalized.ThemeID)
	if !ok {
		theme, _ = Lookup(DefaultThemeID)
	}
	return Snapshot{
		ThemeID:     theme.ID,
		ColorScheme: colorScheme(theme),
		Tokens:      tokensForTheme(theme.ID, theme, normalized.AccentHex, colorScheme(theme)),
	}
}

func tokensForTheme(themeID string, theme Theme, accent, scheme string) Tokens {
	return Tokens{
		ThemeID:     themeID,
		ColorScheme: scheme,
		Dark:        theme.Dark,
		Accent:      strings.ToLower(accent),
		OnAccent:    onAccent(accent),
		Rosewater:   strings.ToLower(theme.Rosewater),
		Flamingo:    strings.ToLower(theme.Flamingo),
		Pink:        strings.ToLower(theme.Pink),
		Mauve:       strings.ToLower(theme.Mauve),
		Red:         strings.ToLower(theme.Red),
		Maroon:      strings.ToLower(theme.Maroon),
		Peach:       strings.ToLower(theme.Peach),
		Yellow:      strings.ToLower(theme.Yellow),
		Green:       strings.ToLower(theme.Green),
		Teal:        strings.ToLower(theme.Teal),
		Sky:         strings.ToLower(theme.Sky),
		Sapphire:    strings.ToLower(theme.Sapphire),
		Blue:        strings.ToLower(theme.Blue),
		Lavender:    strings.ToLower(theme.Lavender),
		Text:        strings.ToLower(theme.Text),
		Subtext1:    strings.ToLower(theme.Subtext1),
		Subtext0:    strings.ToLower(theme.Subtext0),
		Overlay2:    strings.ToLower(theme.Overlay2),
		Overlay1:    strings.ToLower(theme.Overlay1),
		Overlay0:    strings.ToLower(theme.Overlay0),
		Surface2:    strings.ToLower(theme.Surface2),
		Surface1:    strings.ToLower(theme.Surface1),
		Surface0:    strings.ToLower(theme.Surface0),
		Base:        strings.ToLower(theme.Base),
		Mantle:      strings.ToLower(theme.Mantle),
		Crust:       strings.ToLower(theme.Crust),
	}
}

func systemCSS(settings Settings) string {
	darkTheme := catppuccinMocha()
	lightTheme := catppuccinLatte()
	accent := settings.AccentHex
	darkSecondary := deriveSecondary(darkTheme, accent)
	lightSecondary := deriveSecondary(lightTheme, accent)

	var b strings.Builder
	b.WriteString("/* Generated Igloo web theme. */\n")
	b.WriteString(":root {\n")
	writeVar(&b, "color-scheme", "light dark")
	writeVar(&b, "--web-theme-id", strconv.Quote(SystemThemeID))
	writeColorVars(&b, darkTheme, accent, darkSecondary, onAccent(accent), mustRGB(accent), mustRGB(darkSecondary))
	b.WriteString("}\n\n")
	b.WriteString("@media (prefers-color-scheme: light) {\n")
	b.WriteString(":root {\n")
	writeColorVars(&b, lightTheme, accent, lightSecondary, onAccent(accent), mustRGB(accent), mustRGB(lightSecondary))
	b.WriteString("}\n")
	b.WriteString("}\n")
	appendCustomCSS(&b, settings.CustomCSS)
	return b.String()
}

func appendCustomCSS(b *strings.Builder, customCSS string) {
	if strings.TrimSpace(customCSS) == "" {
		return
	}
	b.WriteString("\n/* Custom web theme CSS. */\n")
	b.WriteString(customCSS)
	if !strings.HasSuffix(customCSS, "\n") {
		b.WriteByte('\n')
	}
}

func writeColorVars(b *strings.Builder, theme Theme, accent, secondary, onAccent string, accentRGB, secondaryRGB rgb) {
	// Compatibility aliases for existing theme consumers.
	writeHex(b, "--ctp-rosewater", theme.Rosewater)
	writeHex(b, "--ctp-flamingo", theme.Flamingo)
	writeHex(b, "--ctp-pink", theme.Pink)
	writeHex(b, "--ctp-mauve", theme.Mauve)
	writeHex(b, "--ctp-red", theme.Red)
	writeHex(b, "--ctp-maroon", theme.Maroon)
	writeHex(b, "--ctp-peach", theme.Peach)
	writeHex(b, "--ctp-yellow", theme.Yellow)
	writeHex(b, "--ctp-green", theme.Green)
	writeHex(b, "--ctp-teal", theme.Teal)
	writeHex(b, "--ctp-sky", theme.Sky)
	writeHex(b, "--ctp-sapphire", theme.Sapphire)
	writeHex(b, "--ctp-blue", theme.Blue)
	writeHex(b, "--ctp-lavender", theme.Lavender)
	writeHex(b, "--ctp-text", theme.Text)
	writeHex(b, "--ctp-subtext1", theme.Subtext1)
	writeHex(b, "--ctp-subtext0", theme.Subtext0)
	writeHex(b, "--ctp-overlay2", theme.Overlay2)
	writeHex(b, "--ctp-overlay1", theme.Overlay1)
	writeHex(b, "--ctp-overlay0", theme.Overlay0)
	writeHex(b, "--ctp-surface2", theme.Surface2)
	writeHex(b, "--ctp-surface1", theme.Surface1)
	writeHex(b, "--ctp-surface0", theme.Surface0)
	writeHex(b, "--ctp-base", theme.Base)
	writeHex(b, "--ctp-mantle", theme.Mantle)
	writeHex(b, "--ctp-crust", theme.Crust)

	writeRGB(b, "--ctp-red-rgb", mustRGB(theme.Red))
	writeRGB(b, "--ctp-maroon-rgb", mustRGB(theme.Maroon))
	writeRGB(b, "--ctp-peach-rgb", mustRGB(theme.Peach))
	writeRGB(b, "--ctp-yellow-rgb", mustRGB(theme.Yellow))
	writeRGB(b, "--ctp-green-rgb", mustRGB(theme.Green))
	writeRGB(b, "--ctp-blue-rgb", mustRGB(theme.Blue))
	writeRGB(b, "--ctp-lavender-rgb", mustRGB(theme.Lavender))
	writeRGB(b, "--ctp-mauve-rgb", mustRGB(theme.Mauve))
	writeRGB(b, "--ctp-text-rgb", mustRGB(theme.Text))
	writeRGB(b, "--ctp-subtext1-rgb", mustRGB(theme.Subtext1))
	writeRGB(b, "--ctp-subtext0-rgb", mustRGB(theme.Subtext0))
	writeRGB(b, "--ctp-overlay2-rgb", mustRGB(theme.Overlay2))
	writeRGB(b, "--ctp-overlay1-rgb", mustRGB(theme.Overlay1))
	writeRGB(b, "--ctp-base-rgb", mustRGB(theme.Base))
	writeRGB(b, "--ctp-mantle-rgb", mustRGB(theme.Mantle))
	writeRGB(b, "--ctp-crust-rgb", mustRGB(theme.Crust))
	writeRGB(b, "--ctp-surface0-rgb", mustRGB(theme.Surface0))
	writeRGB(b, "--ctp-surface1-rgb", mustRGB(theme.Surface1))
	writeRGB(b, "--ctp-overlay0-rgb", mustRGB(theme.Overlay0))

	writeHex(b, "--bg-primary", theme.Base)
	writeHex(b, "--bg-secondary", theme.Surface0)
	writeHex(b, "--bg-tertiary", theme.Crust)
	writeVar(b, "--bg-hover", "rgba(var(--text-primary-rgb), 0.06)")
	writeVar(b, "--surface-hover", "var(--bg-hover)")
	writeVar(b, "--bg-glass", "rgba(var(--bg-primary-rgb), 0.92)")
	writeRGB(b, "--bg-primary-rgb", mustRGB(theme.Base))
	writeRGB(b, "--bg-secondary-rgb", mustRGB(theme.Surface0))
	writeRGB(b, "--bg-tertiary-rgb", mustRGB(theme.Crust))

	writeHex(b, "--text-primary", theme.Text)
	writeHex(b, "--text-secondary", theme.Subtext1)
	mutedText := readableMutedText(theme)
	writeHex(b, "--text-muted", mutedText)
	handleText := readableHandleText(theme)
	writeHex(b, "--text-handle", handleText)
	writeRGB(b, "--text-primary-rgb", mustRGB(theme.Text))
	writeRGB(b, "--text-secondary-rgb", mustRGB(theme.Subtext1))
	writeRGB(b, "--text-muted-rgb", mustRGB(mutedText))
	writeRGB(b, "--text-handle-rgb", mustRGB(handleText))

	writeHex(b, "--accent-primary", accent)
	writeHex(b, "--accent-secondary", secondary)
	writeRGB(b, "--accent-primary-rgb", accentRGB)
	writeRGB(b, "--accent-secondary-rgb", secondaryRGB)
	writeVar(b, "--accent-gradient", fmt.Sprintf("linear-gradient(135deg, %s 0%%, %s 100%%)", accent, secondary))
	writeHex(b, "--accent-yellow", theme.Yellow)
	writeHex(b, "--accent-green", theme.Green)

	writeHex(b, "--success", theme.Green)
	writeHex(b, "--warning", theme.Yellow)
	writeHex(b, "--error", theme.Red)
	writeRGB(b, "--status-success-rgb", mustRGB(theme.Green))
	writeRGB(b, "--status-warning-rgb", mustRGB(theme.Yellow))
	writeRGB(b, "--status-error-rgb", mustRGB(theme.Red))

	writeVar(b, "--border-color", "rgba(var(--text-primary-rgb), 0.06)")
	writeVar(b, "--shadow", shadow(theme))
	writeHex(b, "--surface-soft", theme.Base)
	writeHex(b, "--surface-strong", theme.Crust)
	writeRGB(b, "--surface-soft-rgb", mustRGB(theme.Mantle))
	writeRGB(b, "--surface-strong-rgb", mustRGB(theme.Crust))
	writeRGB(b, "--surface-elevated-rgb", mustRGB(theme.Surface1))

	writeHex(b, "--status-info", theme.Blue)
	writeHex(b, "--status-success", theme.Green)
	writeHex(b, "--status-warning", theme.Yellow)
	writeHex(b, "--status-error", theme.Red)
	writeHex(b, "--status-purple", theme.Lavender)
	writeHex(b, "--status-source", theme.Mauve)
	writeHex(b, "--status-tag", theme.Sky)
	writeHex(b, "--status-muted", mutedText)

	writeHex(b, "--media-primary-color", theme.Text)
	writeVar(b, "--media-range-bar-color", "rgba(var(--accent-primary-rgb), 0.5)")
	writeHex(b, "--media-icon-color", theme.Text)
	writeHex(b, "--on-accent", onAccent)
}

func systemTheme() Theme {
	theme := catppuccinMocha()
	theme.ID = SystemThemeID
	theme.Label = "System"
	theme.Catppuccin = false
	return theme
}

func catppuccinMocha() Theme {
	return Theme{
		ID: "catppuccin-mocha", Label: "Catppuccin Mocha", Catppuccin: true, Dark: true,
		DefaultAccent: "#f38ba8", Secondary: "#eba0ac",
		Rosewater: "#f5e0dc", Flamingo: "#f2cdcd", Pink: "#f5c2e7", Mauve: "#cba6f7",
		Red: "#f38ba8", Maroon: "#eba0ac", Peach: "#fab387", Yellow: "#f9e2af",
		Green: "#a6e3a1", Teal: "#94e2d5", Sky: "#89dceb", Sapphire: "#74c7ec",
		Blue: "#89b4fa", Lavender: "#b4befe",
		Text: "#cdd6f4", Subtext1: "#bac2de", Subtext0: "#a6adc8", Overlay2: "#9399b2",
		Overlay1: "#7f849c", Overlay0: "#6c7086", Surface2: "#585b70", Surface1: "#45475a",
		Surface0: "#313244", Base: "#1e1e2e", Mantle: "#181825", Crust: "#11111b",
	}
}

func catppuccinMacchiato() Theme {
	return Theme{
		ID: "catppuccin-macchiato", Label: "Catppuccin Macchiato", Catppuccin: true, Dark: true,
		DefaultAccent: "#ed8796", Secondary: "#ee99a0",
		Rosewater: "#f4dbd6", Flamingo: "#f0c6c6", Pink: "#f5bde6", Mauve: "#c6a0f6",
		Red: "#ed8796", Maroon: "#ee99a0", Peach: "#f5a97f", Yellow: "#eed49f",
		Green: "#a6da95", Teal: "#8bd5ca", Sky: "#91d7e3", Sapphire: "#7dc4e4",
		Blue: "#8aadf4", Lavender: "#b7bdf8",
		Text: "#cad3f5", Subtext1: "#b8c0e0", Subtext0: "#a5adcb", Overlay2: "#939ab7",
		Overlay1: "#8087a2", Overlay0: "#6e738d", Surface2: "#5b6078", Surface1: "#494d64",
		Surface0: "#363a4f", Base: "#24273a", Mantle: "#1e2030", Crust: "#181926",
	}
}

func catppuccinFrappe() Theme {
	return Theme{
		ID: "catppuccin-frappe", Label: "Catppuccin Frappé", Catppuccin: true, Dark: true,
		DefaultAccent: "#e78284", Secondary: "#ea999c",
		Rosewater: "#f2d5cf", Flamingo: "#eebebe", Pink: "#f4b8e4", Mauve: "#ca9ee6",
		Red: "#e78284", Maroon: "#ea999c", Peach: "#ef9f76", Yellow: "#e5c890",
		Green: "#a6d189", Teal: "#81c8be", Sky: "#99d1db", Sapphire: "#85c1dc",
		Blue: "#8caaee", Lavender: "#babbf1",
		Text: "#c6d0f5", Subtext1: "#b5bfe2", Subtext0: "#a5adce", Overlay2: "#949cbb",
		Overlay1: "#838ba7", Overlay0: "#737994", Surface2: "#626880", Surface1: "#51576d",
		Surface0: "#414559", Base: "#303446", Mantle: "#292c3c", Crust: "#232634",
	}
}

func catppuccinLatte() Theme {
	return Theme{
		ID: "catppuccin-latte", Label: "Catppuccin Latte", Catppuccin: true, Dark: false,
		DefaultAccent: "#d20f39", Secondary: "#e64553",
		Rosewater: "#dc8a78", Flamingo: "#dd7878", Pink: "#ea76cb", Mauve: "#8839ef",
		Red: "#d20f39", Maroon: "#e64553", Peach: "#fe640b", Yellow: "#df8e1d",
		Green: "#40a02b", Teal: "#179299", Sky: "#04a5e5", Sapphire: "#209fb5",
		Blue: "#1e66f5", Lavender: "#7287fd",
		Text: "#4c4f69", Subtext1: "#5c5f77", Subtext0: "#6c6f85", Overlay2: "#7c7f93",
		Overlay1: "#8c8fa1", Overlay0: "#9ca0b0", Surface2: "#acb0be", Surface1: "#bcc0cc",
		Surface0: "#ccd0da", Base: "#eff1f5", Mantle: "#e6e9ef", Crust: "#dce0e8",
	}
}

func deriveSecondary(theme Theme, accent string) string {
	if theme.ID == DefaultThemeID && strings.EqualFold(accent, DefaultAccentHex) {
		return defaultSecondary
	}
	if strings.EqualFold(accent, theme.DefaultAccent) {
		return strings.ToLower(theme.Secondary)
	}
	accentRGB := mustRGB(accent)
	target := mustRGB(theme.Text)
	if luminance(accentRGB) > 0.45 {
		target = mustRGB(theme.Crust)
	}
	return blend(accentRGB, target, 0.22).hex()
}

func colorScheme(theme Theme) string {
	if theme.Dark {
		return "dark"
	}
	return "light"
}

func shadow(theme Theme) string {
	if theme.Dark {
		return "0 14px 38px rgba(0, 0, 0, 0.5)"
	}
	return "0 14px 38px rgba(31, 35, 40, 0.16)"
}

func readableHandleText(theme Theme) string {
	return firstReadableColorAcross(themeBackgrounds(theme), []string{theme.Overlay0, theme.Subtext0, theme.Subtext1, theme.Text}, 4.5)
}

func readableMutedText(theme Theme) string {
	return firstReadableColorAcross(themeBackgrounds(theme), []string{theme.Overlay0, theme.Overlay1, theme.Overlay2, theme.Subtext0, theme.Subtext1}, 3.0)
}

func themeBackgrounds(theme Theme) []string {
	return []string{theme.Base, theme.Mantle, theme.Surface0}
}

func firstReadableColorAcross(backgrounds []string, candidates []string, minimumContrast float64) string {
	best := candidates[len(candidates)-1]
	bestContrast := 0.0
	for _, candidate := range candidates {
		ratio := minimumContrastAcross(backgrounds, candidate)
		if ratio > bestContrast {
			best = candidate
			bestContrast = ratio
		}
		if ratio >= minimumContrast {
			return strings.ToLower(candidate)
		}
	}
	return strings.ToLower(best)
}

func firstReadableColor(background string, candidates []string, minimumContrast float64) string {
	bg := mustRGB(background)
	best := candidates[len(candidates)-1]
	bestContrast := 0.0
	for _, candidate := range candidates {
		ratio := contrastRatio(bg, mustRGB(candidate))
		if ratio > bestContrast {
			best = candidate
			bestContrast = ratio
		}
		if ratio >= minimumContrast {
			return strings.ToLower(candidate)
		}
	}
	return strings.ToLower(best)
}

func minimumContrastAcross(backgrounds []string, foreground string) float64 {
	minimum := math.Inf(1)
	fg := mustRGB(foreground)
	for _, background := range backgrounds {
		ratio := contrastRatio(mustRGB(background), fg)
		if ratio < minimum {
			minimum = ratio
		}
	}
	return minimum
}

func contrastRatio(a, b rgb) float64 {
	la := luminance(a)
	lb := luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func normalizeHex(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if len(raw) != 7 || raw[0] != '#' {
		return "", false
	}
	for _, ch := range raw[1:] {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return "", false
		}
	}
	return raw, true
}

type rgb struct{ r, g, b int }

func mustRGB(hex string) rgb {
	normalized, ok := normalizeHex(hex)
	if !ok {
		normalized = defaultSecondary
	}
	r, _ := strconv.ParseInt(normalized[1:3], 16, 0)
	g, _ := strconv.ParseInt(normalized[3:5], 16, 0)
	b, _ := strconv.ParseInt(normalized[5:7], 16, 0)
	return rgb{int(r), int(g), int(b)}
}

func (c rgb) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", clamp(c.r), clamp(c.g), clamp(c.b))
}

func blend(a, b rgb, amount float64) rgb {
	return rgb{
		r: int(math.Round(float64(a.r)*(1-amount) + float64(b.r)*amount)),
		g: int(math.Round(float64(a.g)*(1-amount) + float64(b.g)*amount)),
		b: int(math.Round(float64(a.b)*(1-amount) + float64(b.b)*amount)),
	}
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func luminance(c rgb) float64 {
	linear := func(v int) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.r) + 0.7152*linear(c.g) + 0.0722*linear(c.b)
}

func onAccent(hex string) string {
	if luminance(mustRGB(hex)) > 0.35 {
		return "#11111b"
	}
	return "#ffffff"
}

func writeHex(b *strings.Builder, name, value string) {
	writeVar(b, name, strings.ToLower(value))
}

func writeRGB(b *strings.Builder, name string, value rgb) {
	writeVar(b, name, fmt.Sprintf("%d, %d, %d", value.r, value.g, value.b))
}

func writeVar(b *strings.Builder, name, value string) {
	fmt.Fprintf(b, "    %s: %s;\n", name, value)
}

// SortedThemeIDs returns stable IDs for tests and diagnostics.
func SortedThemeIDs() []string {
	ids := make([]string, 0, len(themeByID))
	for id := range themeByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
