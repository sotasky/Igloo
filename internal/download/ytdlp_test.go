package download

import (
	"context"
	"slices"
	"testing"
)

func TestCanonicalizeYouTubeChannelIDPrefersCanonicalURL(t *testing.T) {
	got := CanonicalizeYouTubeChannelID(
		"example_creator",
		"https://www.youtube.com/channel/UCEXAMPLE000000000000001",
		"https://www.youtube.com/@example_creator",
	)
	want := "youtube_UCEXAMPLE000000000000001"
	if got != want {
		t.Fatalf("CanonicalizeYouTubeChannelID(...) = %q; want %q", got, want)
	}
}

func TestCanonicalizeYouTubeChannelIDKeepsCanonicalInput(t *testing.T) {
	got := CanonicalizeYouTubeChannelID("youtube_UCEXAMPLE000000000000002")
	want := "youtube_UCEXAMPLE000000000000002"
	if got != want {
		t.Fatalf("CanonicalizeYouTubeChannelID(...) = %q; want %q", got, want)
	}
}

func TestExtractYouTubeChannelIDFromURLSupportsQueryParam(t *testing.T) {
	got := extractYouTubeChannelIDFromURL("https://www.youtube.com/feeds/videos.xml?channel_id=UCEXAMPLE000000000000002")
	want := "UCEXAMPLE000000000000002"
	if got != want {
		t.Fatalf("extractYouTubeChannelIDFromURL(...) = %q; want %q", got, want)
	}
}

func TestFetchInfoCommandUsesCookieFile(t *testing.T) {
	cmd := fetchInfoCommand(Opts{Cookies: "/config/cookies/youtube_cookies.txt"}).
		BuildCommand(context.Background(), "https://www.youtube.com/watch?v=test")
	args := cmd.Args

	if !slices.Contains(args, "--cookies") {
		t.Fatalf("fetch info args missing --cookies: %#v", args)
	}
	if !slices.Contains(args, "/config/cookies/youtube_cookies.txt") {
		t.Fatalf("fetch info args missing cookie path: %#v", args)
	}
	if slices.Contains(args, "--cookies-from-browser") {
		t.Fatalf("cookie file should take precedence over browser cookies: %#v", args)
	}
}

func TestFetchInfoCommandUsesBrowserCookiesWhenNoFile(t *testing.T) {
	cmd := fetchInfoCommand(Opts{CookiesFromBrowser: "firefox"}).
		BuildCommand(context.Background(), "https://www.youtube.com/watch?v=test")
	args := cmd.Args

	if !slices.Contains(args, "--cookies-from-browser") {
		t.Fatalf("fetch info args missing --cookies-from-browser: %#v", args)
	}
	if !slices.Contains(args, "firefox") {
		t.Fatalf("fetch info args missing browser name: %#v", args)
	}
	if slices.Contains(args, "--cookies") {
		t.Fatalf("browser cookies should not also set --cookies: %#v", args)
	}
}
