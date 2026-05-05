package fetchprofile

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestParseYouTubeDump(t *testing.T) {
	data, err := os.ReadFile("testdata/youtube_dump.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rawID := "UCexampleSynthetic"
	p, err := parseYouTubeDump(rawID, data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Platform != "youtube" {
		t.Fatalf("platform: %q", p.Platform)
	}
	if p.ChannelID != "youtube_"+rawID {
		t.Fatalf("channel_id: %q", p.ChannelID)
	}
	if p.DisplayName == "" {
		t.Fatalf("display_name empty")
	}
	if p.AvatarURL == "" {
		t.Fatalf("avatar_url empty — should be populated from thumbnails")
	}
	if strings.Contains(p.AvatarURL, "fcrop64") {
		t.Fatalf("avatar_url picked a wide banner crop: %q", p.AvatarURL)
	}
	if p.BannerURL == "" {
		t.Fatalf("banner_url empty — fixture includes one")
	}
	if !strings.Contains(p.BannerURL, "banner.jpg") {
		t.Fatalf("banner_url should keep the wide channel art, got %q", p.BannerURL)
	}
}

func TestParseYouTubeDumpPrefersSquareAvatarOverWideNumericThumb(t *testing.T) {
	data := []byte(`{"channel":"Example","uploader":"Example","uploader_id":"@example","thumbnails":[{"id":"0","url":"https://img.example/banner.jpg","width":2560,"height":424},{"id":"1","url":"https://img.example/avatar.jpg","width":900,"height":900}]}`)
	p, err := parseYouTubeDump("UCexample", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.AvatarURL != "https://img.example/avatar.jpg" {
		t.Fatalf("avatar_url = %q; want square avatar", p.AvatarURL)
	}
	if p.BannerURL != "https://img.example/banner.jpg" {
		t.Fatalf("banner_url = %q; want wide banner", p.BannerURL)
	}
}

func TestParseYouTubeEmptyOutput(t *testing.T) {
	if _, err := parseYouTubeDump("UCx", []byte("")); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFetchYouTubeContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FetchYouTube(ctx, "UCx"); err == nil {
		t.Fatalf("expected error on canceled context")
	}
}
