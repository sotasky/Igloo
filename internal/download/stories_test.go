package download

import "testing"

func TestParseTikTokStoryDump(t *testing.T) {
	output := []byte(`[[2,{"id":"7635000000000000001","desc":"native story","author":{"uniqueId":"story_user","nickname":"Story User"},"createTime":"1777600000"}]]` + "\n")

	refs := parseTikTokStoryDump(output, "fallback")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	ref := refs[0]
	if ref.VideoID != "tiktok_story_7635000000000000001" || ref.NativeID != "7635000000000000001" {
		t.Fatalf("unexpected IDs: %+v", ref)
	}
	if ref.ChannelID != "tiktok_story_user" || ref.AuthorHandle != "story_user" || ref.AuthorDisplayName != "Story User" {
		t.Fatalf("unexpected author: %+v", ref)
	}
	if ref.URL != "https://www.tiktok.com/@story_user/video/7635000000000000001" {
		t.Fatalf("URL = %q", ref.URL)
	}
	if ref.PublishedAtMs != 1777600000000 {
		t.Fatalf("PublishedAtMs = %d", ref.PublishedAtMs)
	}
}

func TestParseInstagramStoryDump(t *testing.T) {
	output := []byte(`
[2, {"subcategory":"stories","type":"story","username":"cinema","fullname":"Cinema","post_shortcode":"REEL123","media_id":"987654321","post_url":"https://www.instagram.com/stories/cinema/","description":"story","date":"2026-05-01 10:00:00"}]
`)

	refs := parseInstagramStoryDump(output, "fallback")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	ref := refs[0]
	if ref.VideoID != "instagram_story_987654321" || ref.NativeID != "987654321" {
		t.Fatalf("unexpected IDs: %+v", ref)
	}
	if ref.ChannelID != "instagram_cinema" || ref.AuthorHandle != "cinema" || ref.AuthorDisplayName != "Cinema" {
		t.Fatalf("unexpected author: %+v", ref)
	}
	if ref.URL != "https://www.instagram.com/stories/cinema/987654321/" {
		t.Fatalf("URL = %q", ref.URL)
	}
	if ref.PublishedAtMs == 0 {
		t.Fatal("PublishedAtMs should be parsed")
	}
}

func TestParseInstagramStoryDumpSkipsTrayShortcodeRows(t *testing.T) {
	output := []byte(`
[2, {"subcategory":"stories","type":"story","username":"cinema","fullname":"Cinema","post_shortcode":"TRAY123","post_url":"https://www.instagram.com/stories/cinema/","description":"story tray","date":"2026-05-01 10:00:00","count":3,"user":{"pk":"999999999"}}]
[2, {"subcategory":"stories","type":"story","username":"cinema","fullname":"Cinema","post_shortcode":"TRAY123","media_id":"987654321","post_url":"https://www.instagram.com/stories/cinema/","description":"story frame","date":"2026-05-01 10:00:00"}]
`)

	refs := parseInstagramStoryDump(output, "fallback")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	ref := refs[0]
	if ref.VideoID != "instagram_story_987654321" || ref.NativeID != "987654321" {
		t.Fatalf("unexpected IDs: %+v", ref)
	}
	if ref.URL != "https://www.instagram.com/stories/cinema/987654321/" {
		t.Fatalf("URL = %q", ref.URL)
	}
}

func TestInstagramStoryIDFromURLRejectsNonInstagramHost(t *testing.T) {
	id := instagramStoryIDFromURL("https://evil.example/stories/cinema/987654321/")
	if id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
}

func TestParseInstagramStoryDumpRejectsNonInstagramURLFallbackID(t *testing.T) {
	output := []byte(`
[2, {"subcategory":"stories","type":"story","username":"cinema","fullname":"Cinema","post_url":"https://evil.example/stories/cinema/987654321/","description":"story","date":"2026-05-01 10:00:00"}]
`)

	refs := parseInstagramStoryDump(output, "fallback")
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0: %#v", len(refs), refs)
	}
}

func TestParseTikTokStoryDumpRejectsUnsafeMetadata(t *testing.T) {
	output := []byte(`[[2,{"id":"../escape","author":{"uniqueId":"bad/user"},"url":"http://127.0.0.1/internal"}]]` + "\n")
	refs := parseTikTokStoryDump(output, "fallback")
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0: %#v", len(refs), refs)
	}
}

func TestParseInstagramStoryDumpCanonicalizesURL(t *testing.T) {
	output := []byte(`
[2, {"subcategory":"stories","type":"story","username":"cinema","media_id":"987654321","post_url":"https://attacker.invalid/story/987654321"}]
`)
	refs := parseInstagramStoryDump(output, "fallback")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	if refs[0].URL != "https://www.instagram.com/stories/cinema/987654321/" {
		t.Fatalf("URL = %q", refs[0].URL)
	}
}
