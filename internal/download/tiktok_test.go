package download

import (
	"testing"
)

func TestIsTikTokURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.tiktok.com/@user_a/video/123456789", true},
		{"https://vm.tiktok.com/ZMeABCDEF/", true},
		{"https://tnktok.com/@user_b/video/987654321", true},
		{"https://www.youtube.com/watch?v=abc123", false},
		{"https://x.com/user_c/status/111222333", false},
		{"https://example.com/tiktok.com/not-a-real-one", false},
	}

	for _, tc := range cases {
		got := IsTikTokURL(tc.url)
		if got != tc.want {
			t.Errorf("IsTikTokURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestExtractTikTokID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.tiktok.com/@user_a/video/9000000000000000001", "9000000000000000001"},
		{"https://www.tiktok.com/@user_b/photo/9000000000000000002", "9000000000000000002"},
		{"https://www.tiktok.com/video/9000000000000000003", "9000000000000000003"},
		{"https://www.youtube.com/watch?v=abc123", ""},
		{"https://vm.tiktok.com/ZMeABCDEF/", ""},
	}

	for _, tc := range cases {
		got := extractTikTokID(tc.url)
		if got != tc.want {
			t.Errorf("extractTikTokID(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestMediaExtFromURL_TikTok(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://pbs.twimg.com/media/AbCdEfG.jpg", ".jpg"},
		{"https://pbs.twimg.com/media/AbCdEfG.png", ".png"},
		{"https://pbs.twimg.com/media/AbCdEfG.webp", ".webp"},
		{"https://example.com/image", ".jpg"}, // no ext → default
		{"https://example.com/image?format=jpg&name=large", ".jpg"},
		{"https://example.com/photo.PNG", ".png"}, // case-insensitive
	}

	for _, tc := range cases {
		got := mediaExtFromURL(tc.url)
		if got != tc.want {
			t.Errorf("mediaExtFromURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestIsDirectMedia_TikTok(t *testing.T) {
	cases := []struct {
		url       string
		mediaType string
		want      bool
	}{
		{"https://pbs.twimg.com/media/abc.jpg", "video", true},
		{"https://example.com/video.mp4", "photo", true},
		{"https://example.com/video.mp4", "image", true},
		{"https://example.com/video.mp4", "video", false},
		{"https://example.com/video.mp4", "", false},
	}

	for _, tc := range cases {
		got := isDirectMedia(tc.url, tc.mediaType)
		if got != tc.want {
			t.Errorf("isDirectMedia(%q, %q) = %v, want %v", tc.url, tc.mediaType, got, tc.want)
		}
	}
}
