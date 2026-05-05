package components

import (
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestPlatformsContain(t *testing.T) {
	p := PageProps{UserPlatforms: []string{"youtube", "twitter"}}
	if !p.PlatformsContain("twitter") {
		t.Error("expected twitter to be found")
	}
	if p.PlatformsContain("tiktok") {
		t.Error("expected tiktok not found")
	}
}

func TestShortcutConfigJSON(t *testing.T) {
	p := PageProps{ShortcutConfig: map[string]string{"feed.like": "l"}}
	js := p.ShortcutConfigJSON()
	if !strings.Contains(js, `"feed.like":"l"`) {
		t.Errorf("unexpected JSON: %s", js)
	}
	if !strings.HasPrefix(js, "window._cfShortcutConfig = ") {
		t.Errorf("missing window assignment: %s", js)
	}
}

func TestI18nConfigJSON(t *testing.T) {
	p := PageProps{
		Language: "tr",
		Text:     map[string]string{"search_global_placeholder": "Ara", "unsafe": "</script>"},
	}
	js := p.I18nConfigJSON()
	if !strings.HasPrefix(js, "window.IglooI18n = ") {
		t.Errorf("missing window assignment: %s", js)
	}
	if !strings.Contains(js, `"language":"tr"`) {
		t.Errorf("missing language: %s", js)
	}
	if !strings.Contains(js, `"search_global_placeholder":"Ara"`) {
		t.Errorf("missing message: %s", js)
	}
	if strings.Contains(js, "</script>") {
		t.Errorf("script-breaking text was not escaped: %s", js)
	}
}

func TestLFFormatsIndexedStringAndNumberPlaceholders(t *testing.T) {
	p := PageProps{
		Text: map[string]string{
			"confirm": "Unfollow \"%1$s\" after %2$d failed checks?",
		},
	}

	got := LF(p, "confirm", "", "@sample", 4)
	want := "Unfollow \"@sample\" after 4 failed checks?"
	if got != want {
		t.Fatalf("LF() = %q, want %q", got, want)
	}

	got = LF(PageProps{}, "fallback", "Refresh %1$s in %2$d seconds", "feed", 10)
	want = "Refresh feed in 10 seconds"
	if got != want {
		t.Fatalf("LF() fallback = %q, want %q", got, want)
	}
}

func TestFirstChar(t *testing.T) {
	if FirstChar("Hello") != "H" {
		t.Error("expected H")
	}
	if FirstChar("") != "" {
		t.Error("expected empty string")
	}
}

func TestChannelDisplayHandleShowsKnownHandleEvenWhenNameMatches(t *testing.T) {
	ch := model.Channel{
		ChannelID:   "twitter_creator",
		Platform:    "twitter",
		Name:        "creator",
		DisplayName: "creator",
		Handle:      "creator",
	}
	if got := ChannelDisplayHandle(ch); got != "creator" {
		t.Fatalf("ChannelDisplayHandle() = %q, want %q", got, "creator")
	}
	if got := channelNameTitle(ch); got != "creator (@creator)" {
		t.Fatalf("channelNameTitle() = %q, want %q", got, "creator (@creator)")
	}
}

func TestChannelDisplayNamePrefersDistinctChannelNameOverHandleLikeProfileName(t *testing.T) {
	ch := model.Channel{
		Platform:    "twitter",
		Name:        "Readable Creator",
		DisplayName: "@creator",
		Handle:      "creator",
	}
	if got := ChannelDisplayName(ch); got != "Readable Creator" {
		t.Fatalf("ChannelDisplayName() = %q, want %q", got, "Readable Creator")
	}
}

func TestChannelDisplayHandleRejectsTikTokInternalIDs(t *testing.T) {
	ch := model.Channel{
		ChannelID: "tiktok_MS4wLjABAAAANimIR6uNi69rFPkPOrdPgNIMp2fyxEqejtZTXpYL1cYb3DxzB-qGjWBE6XJGvA5J",
		SourceID:  "MS4wLjABAAAANimIR6uNi69rFPkPOrdPgNIMp2fyxEqejtZTXpYL1cYb3DxzB-qGjWBE6XJGvA5J",
		Name:      "Readable Creator",
		Platform:  "tiktok",
	}

	if got := ChannelDisplayHandle(ch); got != "" {
		t.Fatalf("ChannelDisplayHandle() = %q, want empty", got)
	}
	if got := channelNameTitle(ch); got != "Readable Creator" {
		t.Fatalf("channelNameTitle() = %q, want %q", got, "Readable Creator")
	}
}

func TestChannelDisplayHandleDoesNotGuessTikTokHandleFromName(t *testing.T) {
	ch := model.Channel{
		ChannelID: "tiktok_7000000000000000001",
		SourceID:  "7000000000000000001",
		Name:      "creator_one",
		Platform:  "tiktok",
	}

	if got := ChannelDisplayHandle(ch); got != "" {
		t.Fatalf("ChannelDisplayHandle() = %q, want empty", got)
	}
	if got := channelNameTitle(ch); got != "creator_one" {
		t.Fatalf("channelNameTitle() = %q, want %q", got, "creator_one")
	}
}

func TestChannelDisplayHandleUsesFixedTikTokSourceID(t *testing.T) {
	ch := model.Channel{
		ChannelID: "tiktok_sample_handle_99",
		SourceID:  "sample_handle_99",
		Name:      "sample_handle_99",
		Platform:  "tiktok",
	}

	if got := ChannelDisplayHandle(ch); got != "sample_handle_99" {
		t.Fatalf("ChannelDisplayHandle() = %q, want %q", got, "sample_handle_99")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, ""},
		{65, "1:05"},
		{3661, "1:01:01"},
	}
	for _, tt := range tests {
		if got := FormatDuration(tt.in); got != tt.want {
			t.Errorf("FormatDuration(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	fiveMin := now.Add(-5 * time.Minute)
	if got := RelativeTime(&fiveMin); got != "5m" {
		t.Errorf("expected 5m, got %s", got)
	}
	if got := RelativeTime(nil); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestLinkify(t *testing.T) {
	got := Linkify("Check https://example.com and @user_a")
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Error("URL not linked")
	}
	if !strings.Contains(got, `href="/channels/twitter_user_a"`) {
		t.Error("mention not linked")
	}
}

func TestCsrfInputHTML(t *testing.T) {
	got := CsrfInputHTML("abc123")
	if !strings.Contains(got, `value="abc123"`) {
		t.Error("token not in output")
	}
	if !strings.Contains(got, `name="_csrf_token"`) {
		t.Error("field name not in output")
	}
}
