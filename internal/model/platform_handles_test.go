package model

import "testing"

func TestNormalizeInstagramHandle(t *testing.T) {
	if got := NormalizeInstagramHandle(" @User.Name_42 "); got != "user.name_42" {
		t.Fatalf("NormalizeInstagramHandle valid = %q", got)
	}
	if got := NormalizeInstagramHandle("../../../tmp/pwn"); got != "" {
		t.Fatalf("NormalizeInstagramHandle traversal = %q, want empty", got)
	}
}

func TestInstagramChannelIDFromHandleRejectsInvalid(t *testing.T) {
	if got := InstagramChannelIDFromHandle("owner.name"); got != "instagram_owner.name" {
		t.Fatalf("InstagramChannelIDFromHandle valid = %q", got)
	}
	if got := InstagramChannelIDFromHandle("owner/name"); got != "" {
		t.Fatalf("InstagramChannelIDFromHandle invalid = %q, want empty", got)
	}
}

func TestTikTokChannelIDFromHandleRejectsInvalid(t *testing.T) {
	if got := TikTokChannelIDFromHandle("creator.name_42"); got != "tiktok_creator.name_42" {
		t.Fatalf("TikTokChannelIDFromHandle valid = %q", got)
	}
	if got := TikTokChannelIDFromHandle("creator/name"); got != "" {
		t.Fatalf("TikTokChannelIDFromHandle slash = %q, want empty", got)
	}
	if got := TikTokChannelIDFromHandle("7000000000000000001"); got != "" {
		t.Fatalf("TikTokChannelIDFromHandle internal id = %q, want empty", got)
	}
}
