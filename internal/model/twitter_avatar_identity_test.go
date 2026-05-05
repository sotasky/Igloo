package model

import "testing"

func TestCleanFeedAvatarURLDropsTwitterStatusURLs(t *testing.T) {
	bad := []string{
		"https://x.com/alice/status/undefined",
		"https://twitter.com/alice/status/123",
		" http://x.com/alice/status/123 ",
	}
	for _, raw := range bad {
		if got := CleanFeedAvatarURL(raw); got != "" {
			t.Fatalf("CleanFeedAvatarURL(%q) = %q, want empty", raw, got)
		}
	}
}

func TestCleanFeedAvatarURLPreservesProfileImages(t *testing.T) {
	raw := " https://pbs.twimg.com/profile_images/123/avatar_normal.jpg "
	if got := CleanFeedAvatarURL(raw); got != "https://pbs.twimg.com/profile_images/123/avatar_normal.jpg" {
		t.Fatalf("CleanFeedAvatarURL() = %q", got)
	}
}
