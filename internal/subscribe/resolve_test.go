package subscribe

import (
	"context"
	"testing"
)

func TestDetectPlatform(t *testing.T) {
	tests := []struct {
		name             string
		rawURL           string
		explicitPlatform string
		want             string
	}{
		// Explicit override always wins.
		{"explicit override youtube", "https://x.com/user_a", "youtube", "youtube"},
		{"explicit override twitter", "https://youtube.com/channel/UC123", "twitter", "twitter"},

		// YouTube detection.
		{"youtube.com URL", "https://www.youtube.com/channel/UC123", "", "youtube"},
		{"youtube.com schemeless URL", "youtube.com/@channel_a", "", "youtube"},
		{"youtu.be URL", "https://youtu.be/abc123", "", "youtube"},
		{"youtube channel/@handle", "https://youtube.com/@channel_a", "", "youtube"},

		// TikTok detection.
		{"tiktok URL", "https://www.tiktok.com/@user_b", "", "tiktok"},
		{"tnktok URL", "https://tnktok.com/@user_b", "", "tiktok"},

		// Twitter/X detection.
		{"x.com URL", "https://x.com/twitter_jane", "", "twitter"},
		{"twitter.com URL", "https://twitter.com/twitter_jane", "", "twitter"},

		// Instagram detection.
		{"instagram profile URL", "https://www.instagram.com/example.page/", "", "instagram"},
		{"instagram stories URL", "https://www.instagram.com/stories/example.page/", "", "instagram"},

		// Bare handles default to twitter.
		{"@handle", "@user_a", "", "twitter"},
		{"bare handle", "user_a", "", "twitter"},
		{"unsupported host is rejected", "https://example.com/?next=youtube.com", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectPlatform(tc.rawURL, tc.explicitPlatform)
			if got != tc.want {
				t.Errorf("DetectPlatform(%q, %q) = %q; want %q", tc.rawURL, tc.explicitPlatform, got, tc.want)
			}
		})
	}
}

func TestParseTwitterHandle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// x.com URLs.
		{"x.com plain", "https://x.com/user_a", "user_a"},
		{"x.com with status path", "https://x.com/user_a/status/12345", "user_a"},
		{"x.com with query", "https://x.com/user_a?ref=nav", "user_a"},

		// twitter.com URLs.
		{"twitter.com plain", "https://twitter.com/twitter_jane", "twitter_jane"},
		{"twitter.com with path", "https://twitter.com/twitter_jane/followers", "twitter_jane"},

		// @-prefixed.
		{"@handle", "@user_b", "user_b"},
		{"@handle uppercase", "@User_B", "user_b"},

		// Bare handles.
		{"bare handle", "user_c", "user_c"},
		{"bare handle uppercase", "User_C", "user_c"},

		// Empty.
		{"empty string", "", ""},
		{"reject path in bare handle", "user_c/status/123", ""},
		{"reject query in bare handle", "user_c?x=1", ""},
		{"reject spoofed twitter host", "https://example.com/twitter.com/user_c", ""},
		{"fxtwitter URL", "https://fxtwitter.com/user_d/status/12345", "user_d"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseTwitterHandle(tc.input)
			if got != tc.want {
				t.Errorf("ParseTwitterHandle(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateInput(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		platform string
		wantErr  bool
	}{
		{"youtube host", "https://www.youtube.com/watch?v=abc", "youtube", false},
		{"youtube spoofed path", "https://example.com/?u=youtube.com/watch?v=abc", "youtube", true},
		{"tiktok host", "https://www.tiktok.com/@user_b/video/123", "tiktok", false},
		{"tiktok spoofed path", "https://example.com/tiktok.com/@user_b", "tiktok", true},
		{"twitter handle", "@user_c", "twitter", false},
		{"twitter URL", "https://x.com/user_c/status/123", "twitter", false},
		{"twitter non-url junk", "user_c/status/123", "twitter", true},
		{"instagram profile URL", "https://www.instagram.com/example.page/", "instagram", false},
		{"instagram story URL", "https://www.instagram.com/stories/example.page/", "instagram", false},
		{"instagram spoofed path", "https://example.com/instagram.com/example.page", "instagram", true},
		{"unsupported scheme", "file:///etc/passwd", "youtube", true},
		{"leading dash", "--help", "youtube", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInput(tc.rawURL, tc.platform)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateInput(%q, %q) err=%v, wantErr=%v", tc.rawURL, tc.platform, err, tc.wantErr)
			}
		})
	}
}

func TestParseInstagramHandle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://www.instagram.com/Example.Page/", "example.page"},
		{"https://instagram.com/stories/Example.Page/123/", "example.page"},
		{"https://www.instagram.com/reel/ABC123/", ""},
		{"https://example.com/Example.Page/", ""},
	}
	for _, tt := range tests {
		if got := ParseInstagramHandle(tt.input); got != tt.want {
			t.Fatalf("ParseInstagramHandle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveChannelTwitterSetsSourceID(t *testing.T) {
	got, err := ResolveChannel(context.Background(), "https://x.com/User_A/status/123", "twitter", nil)
	if err != nil {
		t.Fatalf("ResolveChannel: %v", err)
	}
	if got.ChannelID != "twitter_user_a" {
		t.Fatalf("ChannelID = %q; want twitter_user_a", got.ChannelID)
	}
	if got.SourceID != "user_a" {
		t.Fatalf("SourceID = %q; want user_a", got.SourceID)
	}
}

func TestResolveChannelTikTokSetsSourceID(t *testing.T) {
	got, err := ResolveChannel(context.Background(), "https://www.tiktok.com/@User_B", "tiktok", nil)
	if err != nil {
		t.Fatalf("ResolveChannel: %v", err)
	}
	if got.ChannelID != "tiktok_user_b" {
		t.Fatalf("ChannelID = %q; want tiktok_user_b", got.ChannelID)
	}
	if got.SourceID != "user_b" {
		t.Fatalf("SourceID = %q; want user_b", got.SourceID)
	}
}

func TestResolveChannelInstagramSetsSourceID(t *testing.T) {
	got, err := ResolveChannel(context.Background(), "https://www.instagram.com/User.Example/", "instagram", nil)
	if err != nil {
		t.Fatalf("ResolveChannel: %v", err)
	}
	if got.ChannelID != "instagram_user.example" {
		t.Fatalf("ChannelID = %q; want instagram_user.example", got.ChannelID)
	}
	if got.SourceID != "user.example" {
		t.Fatalf("SourceID = %q; want user.example", got.SourceID)
	}
	if got.URL != "https://www.instagram.com/user.example/" {
		t.Fatalf("URL = %q", got.URL)
	}
}

func TestResolveChannelYouTubeLocalChannelURL(t *testing.T) {
	got, err := ResolveChannel(context.Background(), "https://www.youtube.com/channel/UCabc123456789", "youtube", nil)
	if err != nil {
		t.Fatalf("ResolveChannel: %v", err)
	}
	if got.ChannelID != "youtube_UCabc123456789" {
		t.Fatalf("ChannelID = %q", got.ChannelID)
	}
	if got.SourceID != "UCabc123456789" {
		t.Fatalf("SourceID = %q", got.SourceID)
	}
	if got.URL != "https://www.youtube.com/channel/UCabc123456789" {
		t.Fatalf("URL = %q", got.URL)
	}
	if got.Platform != "youtube" || !got.IsSubscribed {
		t.Fatalf("unexpected channel: %+v", got)
	}
}
