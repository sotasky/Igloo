package download

import (
	"context"
	"slices"
	"strings"
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

func TestFetchCommentsCommandUsesExpandedThreadCap(t *testing.T) {
	cmd := fetchCommentsCommand(500, Opts{CookiesFromBrowser: "firefox"}).
		BuildCommand(context.Background(), "https://www.youtube.com/watch?v=test")
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "youtube:max_comments=500,500,500,100") {
		t.Fatalf("comment args missing expanded cap: %#v", cmd.Args)
	}
}

func TestParseCommentsJSONPreservesRepliesAndLikes(t *testing.T) {
	raw := []byte(`{"comments":[
		{"id":"top","author":"Creator","author_id":"UCcreator","author_thumbnail":"https://example.test/a.jpg","text":"hello","like_count":42,"timestamp":1710000000},
		{"id":"reply","parent":"top","author":"Viewer","author_id":"UCviewer","text":"reply","like_count":7,"timestamp":1710000001}
	]}`)

	comments, err := ParseCommentsDumpJSON(raw)
	if err != nil {
		t.Fatalf("ParseCommentsDumpJSON: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("comments len = %d, want 2", len(comments))
	}
	if comments[0].LikeCount != 42 {
		t.Fatalf("root like count = %d, want 42", comments[0].LikeCount)
	}
	if comments[1].ParentID != "top" {
		t.Fatalf("reply parent = %q, want top", comments[1].ParentID)
	}
	if comments[1].LikeCount != 7 {
		t.Fatalf("reply like count = %d, want 7", comments[1].LikeCount)
	}
}
