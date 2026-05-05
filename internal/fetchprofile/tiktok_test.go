package fetchprofile

import (
	"os"
	"testing"
)

func TestParseTikTokAvatar(t *testing.T) {
	data, err := os.ReadFile("testdata/tiktok_avatar.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	p, err := parseTikTokAvatar("user_alpha", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Platform != "tiktok" {
		t.Fatalf("platform: %q", p.Platform)
	}
	if p.ChannelID != "tiktok_user_alpha" {
		t.Fatalf("channel_id: %q", p.ChannelID)
	}
	if p.DisplayName == "" {
		t.Fatalf("display_name empty")
	}
	if p.AvatarURL == "" {
		t.Fatalf("avatar_url empty")
	}
	if p.BannerURL != "" {
		t.Fatalf("tiktok has no banner, got: %q", p.BannerURL)
	}
}

func TestParseTikTokEmpty(t *testing.T) {
	if _, err := parseTikTokAvatar("ghost", []byte("[]")); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestParseTikTokMalformed(t *testing.T) {
	if _, err := parseTikTokAvatar("ghost", []byte("not json")); err == nil {
		t.Fatalf("expected parse error")
	}
}
