package components

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// ChannelWithVideos pairs a channel with its latest videos for the channels overview page.
type ChannelWithVideos struct {
	Channel model.Channel
	Videos  []model.Video
}

type PlatformChoice struct {
	Value string
	Label string
}

type LanguageChoice struct {
	Code string
	Name string
}

// IsShortsPlatform returns true for platforms that use vertical (9:16) thumbnails.
func (c ChannelWithVideos) IsShortsPlatform() bool {
	return c.Channel.Platform == "tiktok" || c.Channel.Platform == "instagram" || c.Channel.Platform == "twitter"
}

// HasContent returns true if the section has any videos to display.
func (c ChannelWithVideos) HasContent() bool {
	return len(c.Videos) > 0
}

// PageProps holds common data injected into every page layout.
type PageProps struct {
	CSRFToken               string
	UserRole                string
	Username                string
	UserPlatforms           []string
	PageTitle               string
	ActiveNav               string
	PageBadge               string
	ShortcutConfig          map[string]string
	TranslateTargetLang     string
	TranslateSkipLangs      string
	TranslateBackend        string
	TranslateAutoMode       string
	TranslateLookahead      string
	Language                string
	Text                    map[string]string
	SupportedLanguages      []LanguageChoice
	Sidebar                 model.SidebarContext
	ShareEmbedFriendlyLinks bool
	StaticV                 func(string) string
	PageScripts             []string // JS files to include after base scripts.
	ESBundle                string   // esbuild bundle to load (e.g. "js/dist/feed.js")
	Prefs                   PrefsData
}

func (p PageProps) Lang() string {
	if p.Language == "" {
		return "en"
	}
	return p.Language
}

func (p PageProps) T(key string, fallback ...string) string {
	if p.Text != nil {
		if msg := p.Text[key]; msg != "" {
			return msg
		}
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return key
}

func L(p PageProps, key string, fallback ...string) string {
	return p.T(key, fallback...)
}

func LF(p PageProps, key, fallback string, args ...any) string {
	format := L(p, key, fallback)
	for i, arg := range args {
		value := fmt.Sprint(arg)
		for _, verb := range []string{"d", "s"} {
			placeholder := fmt.Sprintf("%%%d$%s", i+1, verb)
			format = strings.ReplaceAll(format, placeholder, value)
		}
	}
	return format
}

func N(key string, fallback ...string) string {
	return key
}

// PlatformsContain checks if the user has access to a given platform.
func (p PageProps) PlatformsContain(platform string) bool {
	for _, plat := range p.UserPlatforms {
		if plat == platform {
			return true
		}
	}
	return false
}

// ShortcutConfigJSON returns the shortcut config as a JS assignment string.
func (p PageProps) ShortcutConfigJSON() string {
	b, _ := json.Marshal(p.ShortcutConfig)
	return "window._cfShortcutConfig = " + string(b) + ";"
}

// PreferencesConfigJSON returns client-side preferences needed by static JS.
func (p PageProps) PreferencesConfigJSON() string {
	b, _ := json.Marshal(map[string]any{
		"shareEmbedFriendlyLinks": p.ShareEmbedFriendlyLinks,
	})
	return "window.IglooPreferences = " + string(b) + ";"
}

// I18nConfigJSON returns the current page language and translation catalog for
// static JavaScript. encoding/json keeps strings safe inside a script tag by
// escaping HTML-sensitive bytes.
func (p PageProps) I18nConfigJSON() string {
	b, _ := json.Marshal(map[string]any{
		"language": p.Lang(),
		"messages": p.Text,
	})
	return "window.IglooI18n = " + string(b) + ";"
}

// FirstChar returns the first rune of a string.
func FirstChar(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

// PlatformLabel returns the short, localized platform name shown as a hover
// label next to each sidebar channel.
func PlatformLabel(p PageProps, platform string) string {
	switch platform {
	case "youtube":
		return L(p, "platform_youtube", "YouTube")
	case "tiktok":
		return L(p, "platform_tiktok", "TikTok")
	case "instagram":
		return L(p, "platform_instagram", "Instagram")
	case "twitter":
		return L(p, "platform_x", "X")
	default:
		return ""
	}
}

// ChannelDisplayName returns the pretty, human-facing name to show in the
// sidebar. Prefers `channel_profiles.display_name` when non-empty because
// Twitter ingest stores the handle in `channels.name` for ~82% of rows —
// without this fallback the sidebar would render "sample_handle_ja" instead of
// "Example Display Name". Other platforms usually have the pretty name in
// `channels.name`, so DisplayName stays empty and we fall back.
func ChannelDisplayName(ch model.Channel) string {
	if ch.DisplayName != "" {
		displayHandle := model.NormalizeTwitterHandle(ch.DisplayName)
		channelHandle := model.NormalizeTwitterHandle(ch.Handle)
		if displayHandle != "" && displayHandle == channelHandle && strings.TrimSpace(ch.Name) != "" {
			if model.NormalizeTwitterHandle(ch.Name) != displayHandle {
				return ch.Name
			}
		}
		return ch.DisplayName
	}
	return ch.Name
}

// ChannelDisplayHandle returns the @-handle that should be shown next to a
// sidebar display name. Profile handles are preferred. For handle-first
// platforms, source_id/channel_id can fill old rows that predate profile
// coverage. YouTube source IDs are stable channel IDs, not user-facing handles.
// A known handle is still useful when it equals the display label because the
// @ prefix makes the account identity explicit.
func ChannelDisplayHandle(ch model.Channel) string {
	handle := strings.TrimSpace(strings.TrimPrefix(ch.Handle, "@"))
	if ch.Platform == "tiktok" {
		handle = model.NormalizeTikTokHandle(handle)
		if model.IsTikTokInternalID(handle) {
			handle = ""
		}
	}
	if handle == "" {
		handle = fallbackChannelHandle(ch)
	}
	return handle
}

func fallbackChannelHandle(ch model.Channel) string {
	switch ch.Platform {
	case "twitter", "tiktok", "instagram":
	default:
		return ""
	}
	if ch.Platform == "tiktok" {
		if sourceID := model.NormalizeTikTokHandle(ch.SourceID); sourceID != "" && !model.IsTikTokInternalID(sourceID) {
			return sourceID
		}
		if handle := model.TikTokHandleFromChannelID(ch.ChannelID); handle != "" {
			return handle
		}
		return ""
	}
	if sourceID := strings.TrimSpace(strings.TrimPrefix(ch.SourceID, "@")); sourceID != "" {
		return sourceID
	}
	prefix := ch.Platform + "_"
	if ch.Platform == "twitter" {
		if handle, ok := strings.CutPrefix(ch.ChannelID, "x_"); ok {
			return strings.TrimSpace(strings.TrimPrefix(handle, "@"))
		}
	}
	if handle, ok := strings.CutPrefix(ch.ChannelID, prefix); ok {
		return strings.TrimSpace(strings.TrimPrefix(handle, "@"))
	}
	return ""
}

// channelNameTitle builds the native-tooltip text for a sidebar channel row:
// "Name" when no handle is known, otherwise "Name (@handle)".
func channelNameTitle(ch model.Channel) string {
	name := ChannelDisplayName(ch)
	handle := ChannelDisplayHandle(ch)
	if handle == "" {
		return name
	}
	return fmt.Sprintf("%s (@%s)", name, handle)
}

// FormatDuration formats seconds as H:MM:SS or M:SS.
func FormatDuration(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// RelativeTime formats a time as a relative string.
func RelativeTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	d := time.Since(*t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}

func RelativeTimeText(p PageProps, t *time.Time) string {
	if t == nil {
		return ""
	}
	sec := int64(time.Since(*t).Seconds())
	abs := sec
	if abs < 0 {
		abs = -abs
	}
	future := sec < 0
	switch {
	case abs < 60:
		return L(p, "time_just_now", "Just now")
	case abs < 3600:
		n := (abs + 30) / 60
		if future {
			return LF(p, "time_minutes_from_now", "%1$dm from now", n)
		}
		return LF(p, "time_minutes_ago", "%1$dm ago", n)
	case abs < 86400:
		n := (abs + 1800) / 3600
		if future {
			return LF(p, "time_hours_from_now", "%1$dh from now", n)
		}
		return LF(p, "time_hours_ago", "%1$dh ago", n)
	case abs < 30*86400:
		n := (abs + 43200) / 86400
		if future {
			return LF(p, "time_days_from_now", "%1$dd from now", n)
		}
		return LF(p, "time_days_ago", "%1$dd ago", n)
	case abs < 365*86400:
		n := (abs + 15*86400) / (30 * 86400)
		if future {
			return LF(p, "time_months_from_now", "%1$dmo from now", n)
		}
		return LF(p, "time_months_ago", "%1$dmo ago", n)
	default:
		n := (abs + 182*86400) / (365 * 86400)
		if future {
			return LF(p, "time_years_from_now", "%1$dy from now", n)
		}
		return LF(p, "time_years_ago", "%1$dy ago", n)
	}
}

var (
	urlRe              = regexp.MustCompile(`(https?://[^\s<>"']+)`)
	anchorRe           = regexp.MustCompile(`(?s)<a\b[^>]*>.*?</a>`)
	shortFormMentionRe = regexp.MustCompile(`@[A-Za-z0-9_](?:[A-Za-z0-9_.]{0,30}[A-Za-z0-9_])?`)
	emailTLD           = regexp.MustCompile(`^\.[A-Za-z]{2,12}\b`)
)

// Linkify escapes text and converts URLs and @mentions to HTML links.
// Skips @mentions that look like email addresses (e.g., `user@outlook.com`)
// by checking surrounding characters.
func Linkify(s string) string {
	return LinkifyForPlatform(s, "twitter")
}

// LinkifyForPlatform escapes text and converts URLs plus @mentions to links for
// the account namespace that owns the text.
func LinkifyForPlatform(s, platform string) string {
	escaped := html.EscapeString(s)
	escaped = urlRe.ReplaceAllString(escaped, `<a href="$1" class="feed-inline-link" target="_blank" rel="noopener">$1</a>`)
	escaped = linkifyMentionsForPlatform(escaped, platform)
	escaped = strings.ReplaceAll(escaped, "\n", "<br>\n")
	return escaped
}

// linkifyMentions replaces @handles with profile links, skipping matches that
// look like parts of an email address.
func linkifyMentionsForPlatform(s, platform string) string {
	if normalized := strings.ToLower(strings.TrimSpace(platform)); normalized != "tiktok" && normalized != "instagram" {
		return linkifyTwitterMentions(s)
	}
	prefix, re := mentionLinkPrefixAndPattern(platform)
	matches := re.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	anchors := anchorRe.FindAllStringIndex(s, -1)
	var b strings.Builder
	last := 0
	anchorIdx := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		b.WriteString(s[last:start])

		skip := false
		for anchorIdx < len(anchors) && anchors[anchorIdx][1] <= start {
			anchorIdx++
		}
		if anchorIdx < len(anchors) && start >= anchors[anchorIdx][0] && start < anchors[anchorIdx][1] {
			skip = true
		}
		// Preceded by a word char → looks like `foo@bar` (email).
		if !skip && start > 0 && isMentionWordByte(s[start-1]) {
			skip = true
		}
		// Followed by another `@` → looks like `@foo@bar.com`.
		if !skip && end < len(s) && s[end] == '@' {
			skip = true
		}
		// Followed by `.tld` → looks like an email domain part.
		if !skip && end < len(s) && emailTLD.MatchString(s[end:]) {
			skip = true
		}

		if skip {
			b.WriteString(s[start:end])
		} else {
			handle := strings.ToLower(s[start+1 : end])
			b.WriteString(`<a href="/channels/`)
			b.WriteString(prefix)
			b.WriteString(handle)
			b.WriteString(`" class="feed-inline-link">@`)
			b.WriteString(s[start+1 : end])
			b.WriteString(`</a>`)
		}
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func linkifyTwitterMentions(s string) string {
	matches := model.LinkableTwitterMentions(s)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, match := range matches {
		b.WriteString(s[last:match.Start])
		b.WriteString(`<a href="/channels/twitter_`)
		b.WriteString(match.Handle)
		b.WriteString(`" class="feed-inline-link">`)
		b.WriteString(s[match.Start:match.End])
		b.WriteString(`</a>`)
		last = match.End
	}
	b.WriteString(s[last:])
	return b.String()
}

func mentionLinkPrefixAndPattern(platform string) (string, *regexp.Regexp) {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "tiktok":
		return "tiktok_", shortFormMentionRe
	default:
		return "instagram_", shortFormMentionRe
	}
}

func isMentionWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// Nl2br escapes text and converts newlines to <br>.
func Nl2br(s string) string {
	escaped := html.EscapeString(s)
	return strings.ReplaceAll(escaped, "\n", "<br>\n")
}

// CsrfInputHTML returns a hidden input element for CSRF protection.
func CsrfInputHTML(token string) string {
	return fmt.Sprintf(`<input type="hidden" name="_csrf_token" value="%s">`, html.EscapeString(token))
}
