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

func TestTwitterChannelIDFromHandleEnforcesPlatformLength(t *testing.T) {
	if got := TwitterChannelIDFromHandle("sample_bookmark"); got != "twitter_sample_bookmark" {
		t.Fatalf("15-character handle = %q", got)
	}
	if got := TwitterChannelIDFromHandle("sample_collection"); got != "" {
		t.Fatalf("overlong handle = %q, want empty", got)
	}
}
