package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseInstagramChannelDump(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","username":"cinema","fullname":"Cinema","profile_pic_url":"https://cdn.example/avatar.jpg","post_shortcode":"POST123","post_url":"https://www.instagram.com/p/POST123/","description":"A carousel","date":"2026-04-30 16:26:41"}]
[3, "https://cdn.example/1.webp", {"subcategory":"posts","type":"post","username":"cinema","post_shortcode":"POST123","display_url":"https://cdn.example/1.webp"}]
[2, {"subcategory":"reels","type":"reel","username":"cinema","fullname":"Cinema","post_shortcode":"REEL123","post_url":"https://www.instagram.com/reel/REEL123/","description":"A reel","date":"2026-05-01 10:00:00"}]
[2, {"subcategory":"stories","type":"story","username":"cinema","post_shortcode":"STORY123","post_url":"https://www.instagram.com/stories/cinema/"}]
`)
	refs := ParseInstagramChannelDump(dump)
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want 2: %#v", len(refs), refs)
	}
	if refs[0].VideoID != "instagram_post_POST123" {
		t.Fatalf("first VideoID = %q", refs[0].VideoID)
	}
	if refs[0].ChannelID != "instagram_cinema" || refs[0].AuthorDisplayName != "Cinema" {
		t.Fatalf("first attribution = %#v", refs[0])
	}
	if refs[0].AuthorAvatarURL != "https://cdn.example/avatar.jpg" {
		t.Fatalf("first avatar = %q", refs[0].AuthorAvatarURL)
	}
	if refs[1].VideoID != "instagram_reel_REEL123" {
		t.Fatalf("second VideoID = %q", refs[1].VideoID)
	}
	if refs[1].PublishedAtMs == 0 {
		t.Fatalf("reel PublishedAtMs should be parsed")
	}
}

func TestParseInstagramChannelDumpForHandleUsesCoauthorSource(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","username":"nowness","fullname":"NOWNESS","profile_pic_url":"https://cdn.example/nowness.jpg","coauthors":[{"username":"nowness","full_name":"NOWNESS"},{"username":"sample.creator","full_name":"Sample Creator"}],"user":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":"https://cdn.example/asian.jpg"},"post_shortcode":"POST123","post_url":"https://www.instagram.com/p/POST123/","description":"A coauthored carousel","date":"2026-04-30 16:26:41"}]
`)
	refs := ParseInstagramChannelDumpForHandle(dump, "sample.creator")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	if refs[0].ChannelID != "instagram_sample.creator" || refs[0].AuthorHandle != "sample.creator" {
		t.Fatalf("source attribution = %#v", refs[0])
	}
	if refs[0].AuthorDisplayName != "Sample Creator" {
		t.Fatalf("display name = %q", refs[0].AuthorDisplayName)
	}
	if refs[0].AuthorAvatarURL != "https://cdn.example/asian.jpg" {
		t.Fatalf("avatar = %q", refs[0].AuthorAvatarURL)
	}
}

func TestParseInstagramChannelDumpUsesMatchingAudioUserAvatar(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","username":"sample.creator","fullname":"Sample Creator","audio_user":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":"https://cdn.example/audio-avatar.jpg"},"post_shortcode":"POST123","post_url":"https://www.instagram.com/p/POST123/","description":"A post","date":"2026-04-30 16:26:41"}]
`)
	refs := ParseInstagramChannelDump(dump)
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	if refs[0].AuthorAvatarURL != "https://cdn.example/audio-avatar.jpg" {
		t.Fatalf("avatar = %q", refs[0].AuthorAvatarURL)
	}
}

func TestParseInstagramChannelDumpPrefersFreshNestedAvatar(t *testing.T) {
	expired := instagramAvatarURLForTest("expired", -time.Hour)
	fresh := instagramAvatarURLForTest("fresh", time.Hour)
	dump := []byte(fmt.Sprintf(`
[2, {"subcategory":"posts","type":"post","username":"sample.creator","fullname":"Sample Creator","profile_pic_url":%q,"user":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":%q},"owner":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":%q},"post_shortcode":"sample_post","post_url":"https://www.instagram.com/p/sample_post/","description":"A post","date":"2026-04-30 16:26:41"}]
`, expired, expired, fresh))
	refs := ParseInstagramChannelDump(dump)
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	if refs[0].AuthorAvatarURL != fresh {
		t.Fatalf("avatar = %q, want %q", refs[0].AuthorAvatarURL, fresh)
	}
}

func TestParseInstagramTaggedDumpForHandleKeepsOriginalOwner(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"tagged","type":"post","username":"owner.one","fullname":"Owner One","profile_pic_url":"https://cdn.example/owner.jpg","post_shortcode":"TAG123","post_url":"https://www.instagram.com/p/TAG123/","description":"Tagged post","date":1714494401,"tagged_username":"followed.one","tagged_users":[{"username":"followed.one","full_name":"Followed One","profile_pic_url":"https://cdn.example/followed.jpg"},{"username":"other.two","full_name":"Other Two"}]}]
`)
	refs := ParseInstagramTaggedDumpForHandle(dump, "followed.one")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	ref := refs[0]
	if ref.VideoID != "instagram_post_TAG123" {
		t.Fatalf("VideoID = %q", ref.VideoID)
	}
	if ref.ChannelID != "instagram_owner.one" || ref.AuthorHandle != "owner.one" || ref.AuthorDisplayName != "Owner One" {
		t.Fatalf("tagged route should keep original owner attribution: %#v", ref)
	}
	if !ref.IsRepost || ref.ReposterChannelID != "instagram_followed.one" || ref.ReposterHandle != "followed.one" {
		t.Fatalf("tagged route should mark followed account as introducer: %#v", ref)
	}
	if ref.ReposterDisplayName != "Followed One" || ref.ReposterAvatarURL != "https://cdn.example/followed.jpg" {
		t.Fatalf("tagged route should keep introducer profile media: %#v", ref)
	}
	if ref.PublishedAtMs == 0 {
		t.Fatalf("PublishedAtMs should be parsed from post date: %#v", ref)
	}
	if ref.RepostedAtMs != 0 {
		t.Fatalf("RepostedAtMs = %d, want 0 when dump has no explicit tagged/repost timestamp", ref.RepostedAtMs)
	}
	if ref.AuthorAvatarURL != "https://cdn.example/owner.jpg" {
		t.Fatalf("author avatar = %q", ref.AuthorAvatarURL)
	}
}

func TestParseInstagramTaggedDumpUsesExplicitTaggedTimestamp(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"tagged","type":"post","username":"owner.one","fullname":"Owner One","post_shortcode":"TAG123","post_url":"https://www.instagram.com/p/TAG123/","description":"Tagged post","date":1714494401,"tagged_at":1714498001,"tagged_username":"followed.one","tagged_users":[{"username":"followed.one","full_name":"Followed One"}]}]
`)
	refs := ParseInstagramTaggedDumpForHandle(dump, "followed.one")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1: %#v", len(refs), refs)
	}
	if refs[0].RepostedAtMs == 0 || refs[0].RepostedAtMs == refs[0].PublishedAtMs {
		t.Fatalf("RepostedAtMs = %d, PublishedAtMs = %d, want explicit tagged timestamp", refs[0].RepostedAtMs, refs[0].PublishedAtMs)
	}
}

func TestParseInstagramTaggedDumpRejectsInvalidOwnerHandle(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"tagged","type":"post","username":"../../tmp/pwn","fullname":"Owner One","post_shortcode":"TAG123","post_url":"https://www.instagram.com/p/TAG123/","description":"Tagged post","date":1714494401,"tagged_username":"followed.one","tagged_users":[{"username":"followed.one","full_name":"Followed One"}]}]
`)
	refs := ParseInstagramTaggedDumpForHandle(dump, "followed.one")
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0 for invalid owner handle: %#v", len(refs), refs)
	}
}

func TestInstagramTaggedArgsUseConfiguredLimit(t *testing.T) {
	args := instagramTaggedArgs(30, "/tmp/cookies.txt", "https://www.instagram.com/followed.one/tagged/")
	if !containsString(args, "--range") || !containsString(args, "1-30") {
		t.Fatalf("args should use configured generic range: %#v", args)
	}
	if !containsString(args, "--cookies") || !containsString(args, "/tmp/cookies.txt") {
		t.Fatalf("args should preserve cookies: %#v", args)
	}
}

func TestInstagramTaggedArgsUseBrowserCookies(t *testing.T) {
	args := instagramTaggedArgs(30, "", "https://www.instagram.com/sample_followed/tagged/", "firefox")
	if !containsString(args, "--cookies-from-browser") || !containsString(args, "firefox") {
		t.Fatalf("args should preserve browser cookies: %#v", args)
	}
	if containsString(args, "--cookies") {
		t.Fatalf("args should not request a cookies file when only browser cookies are configured: %#v", args)
	}
}

func TestInstagramProfileCookieAttemptsPreferConfiguredCookies(t *testing.T) {
	got := instagramProfileCookieAttempts("/tmp/instagram-cookies.txt", "firefox")
	want := []CookieSet{{File: "/tmp/instagram-cookies.txt"}, {Browser: "firefox"}}
	if len(got) != len(want) {
		t.Fatalf("cookie attempts = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cookie attempts = %#v, want %#v", got, want)
		}
	}
}

func TestInstagramProfileCookieAttemptsUseBrowserWhenFileDisabled(t *testing.T) {
	got := instagramProfileCookieAttempts("", "firefox")
	want := []CookieSet{{Browser: "firefox"}}
	if len(got) != len(want) {
		t.Fatalf("cookie attempts = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cookie attempts = %#v, want %#v", got, want)
		}
	}
}

func TestInstagramCookieAuthAttemptsUseOnlyBrowserWhenFileDisabled(t *testing.T) {
	got := instagramCookieAuthAttempts("", "firefox")
	want := []CookieSet{{Browser: "firefox"}}
	if len(got) != len(want) {
		t.Fatalf("cookie attempts = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cookie attempts = %#v, want %#v", got, want)
		}
	}
}

func TestInstagramCookieAuthAttemptsAllowAnonymousOnlyWithoutConfiguredCookies(t *testing.T) {
	got := instagramCookieAuthAttempts("", "")
	want := []CookieSet{{}}
	if len(got) != len(want) {
		t.Fatalf("cookie attempts = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cookie attempts = %#v, want %#v", got, want)
		}
	}
}

func TestParseInstagramProfileDump(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","user":{"username":"cinema","full_name":"Cinema Page","profile_pic_url_hd":"https://cdn.example/avatar-hd.jpg","edge_followed_by":{"count":42},"is_verified":true},"post_shortcode":"POST123"}]
`)
	profile := ParseInstagramProfileDump(dump, "cinema")
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.Handle != "cinema" || profile.DisplayName != "Cinema Page" {
		t.Fatalf("profile identity = %#v", profile)
	}
	if profile.AvatarURL != "https://cdn.example/avatar-hd.jpg" {
		t.Fatalf("avatar = %q", profile.AvatarURL)
	}
	if profile.Followers != 42 || !profile.Verified {
		t.Fatalf("counts/verified = %#v", profile)
	}
}

func TestParseInstagramProfileDumpUsesMatchingAudioUserAvatar(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","username":"sample.creator","fullname":"Sample Creator","audio_user":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":"https://cdn.example/audio-avatar.jpg"},"post_shortcode":"POST123"}]
`)
	profile := ParseInstagramProfileDump(dump, "sample.creator")
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.Handle != "sample.creator" || profile.DisplayName != "Sample Creator" {
		t.Fatalf("profile identity = %#v", profile)
	}
	if profile.AvatarURL != "https://cdn.example/audio-avatar.jpg" {
		t.Fatalf("avatar = %q", profile.AvatarURL)
	}
}

func TestParseInstagramProfileDumpPrefersFreshNestedAvatar(t *testing.T) {
	expired := instagramAvatarURLForTest("expired", -time.Hour)
	fresh := instagramAvatarURLForTest("fresh", time.Hour)
	dump := []byte(fmt.Sprintf(`
[2, {"subcategory":"reels","type":"reel","username":"sample.creator","fullname":"Sample Creator","user":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":%q},"owner":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":%q},"post_shortcode":"sample_post"}]
`, expired, fresh))
	profile := ParseInstagramProfileDump(dump, "sample.creator")
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.AvatarURL != fresh {
		t.Fatalf("avatar = %q, want %q", profile.AvatarURL, fresh)
	}
}

func TestInstagramProfileContinuesPastFallbackDumpForAvatar(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  */reels/)
    printf '[2, {"subcategory":"reels","type":"reel","username":"sample.creator","fullname":"Sample Creator","post_shortcode":"sample_reel"}]\n'
    ;;
  */posts/)
    printf '[2, {"subcategory":"posts","type":"post","username":"sample.creator","fullname":"Sample Creator","audio_user":{"username":"sample.creator","full_name":"Sample Creator","profile_pic_url":"https://cdn.example/audio-avatar.jpg"},"post_shortcode":"sample_post"}]\n'
    ;;
esac
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	profile, err := (&GalleryDLWrapper{Runner: CommandRunner{}}).InstagramProfile(context.Background(), "sample.creator", "")
	if err != nil {
		t.Fatalf("InstagramProfile: %v", err)
	}
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.AvatarURL != "https://cdn.example/audio-avatar.jpg" {
		t.Fatalf("avatar = %q", profile.AvatarURL)
	}
}

func TestInstagramProfileContinuesPastExpiredAvatar(t *testing.T) {
	expired := instagramAvatarURLForTest("expired", -time.Hour)
	fresh := instagramAvatarURLForTest("fresh", time.Hour)
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), fmt.Sprintf(`#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  */reels/)
    printf '[2, {"subcategory":"reels","type":"reel","username":"sample.creator","fullname":"Sample Creator","profile_pic_url":%q,"post_shortcode":"sample_reel"}]\n'
    ;;
  */posts/)
    printf '[2, {"subcategory":"posts","type":"post","username":"sample.creator","fullname":"Sample Creator","profile_pic_url":%q,"post_shortcode":"sample_post"}]\n'
    ;;
esac
`, expired, fresh))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	profile, err := (&GalleryDLWrapper{Runner: CommandRunner{}}).InstagramProfile(context.Background(), "sample.creator", "")
	if err != nil {
		t.Fatalf("InstagramProfile: %v", err)
	}
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.AvatarURL != fresh {
		t.Fatalf("avatar = %q, want %q", profile.AvatarURL, fresh)
	}
}

func TestParseInstagramProfileDumpDoesNotUsePostCaptionAsBio(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","username":"sample_cinema","fullname":"Sample Cinema Page","profile_pic_url":"https://cdn.example/avatar.jpg","post_shortcode":"sample_post","post_url":"https://www.instagram.com/p/sample_post/","description":"This is a post caption, not a profile bio.","url":"https://www.instagram.com/p/sample_post/"}]
`)
	profile := ParseInstagramProfileDump(dump, "sample_cinema")
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.Handle != "sample_cinema" || profile.DisplayName != "Sample Cinema Page" {
		t.Fatalf("profile identity = %#v", profile)
	}
	if profile.Bio != "" {
		t.Fatalf("Bio = %q, want empty because media descriptions are captions", profile.Bio)
	}
	if profile.Website != "" {
		t.Fatalf("Website = %q, want empty because media URLs are not profile websites", profile.Website)
	}
	if profile.AvatarURL != "https://cdn.example/avatar.jpg" {
		t.Fatalf("AvatarURL = %q, want matching owner avatar", profile.AvatarURL)
	}
}

func TestParseInstagramProfileDumpSkipsMismatchedPostOwner(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","username":"reposter","fullname":"Reposter","profile_pic_url":"https://cdn.example/reposter.jpg","post_shortcode":"POST123","description":"A repost caption"}]
`)
	profile := ParseInstagramProfileDump(dump, "cinema")
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.Handle != "cinema" || profile.DisplayName != "cinema" {
		t.Fatalf("profile should fall back to the requested handle, got %#v", profile)
	}
	if profile.AvatarURL != "" || profile.Bio != "" {
		t.Fatalf("mismatched post owner leaked into profile: %#v", profile)
	}
}

func TestParseInstagramProfileDumpSkipsMismatchedNestedOwner(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","user":{"username":"reposter","full_name":"Reposter","profile_pic_url":"https://cdn.example/reposter.jpg"},"post_shortcode":"POST123","description":"A repost caption"}]
`)
	profile := ParseInstagramProfileDump(dump, "cinema")
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.Handle != "cinema" || profile.DisplayName != "cinema" {
		t.Fatalf("profile should fall back to the requested handle, got %#v", profile)
	}
	if profile.AvatarURL != "" || profile.Bio != "" {
		t.Fatalf("mismatched nested owner leaked into profile: %#v", profile)
	}
}

func TestParseInstagramProfileDumpForCoauthorUsesMatchingNestedAvatar(t *testing.T) {
	dump := []byte(`
[2, {"subcategory":"posts","type":"post","username":"primary","fullname":"Primary","profile_pic_url":"https://cdn.example/primary.jpg","coauthors":[{"username":"coauthor","full_name":"Co Author"}],"user":{"username":"coauthor","full_name":"Co Author","profile_pic_url":"https://cdn.example/coauthor.jpg"},"post_shortcode":"POST123"}]
`)
	profile := ParseInstagramProfileDump(dump, "coauthor")
	if profile == nil {
		t.Fatal("profile missing")
	}
	if profile.Handle != "coauthor" || profile.DisplayName != "Co Author" {
		t.Fatalf("profile identity = %#v", profile)
	}
	if profile.AvatarURL != "https://cdn.example/coauthor.jpg" {
		t.Fatalf("avatar = %q", profile.AvatarURL)
	}
}

func TestParseInstagramProfileDumpEmptyOutputDoesNotFallback(t *testing.T) {
	if profile := ParseInstagramProfileDump(nil, "fallback"); profile != nil {
		t.Fatalf("profile = %#v, want nil", profile)
	}
}

func TestMergeInstagramRefsSortsAndDedupes(t *testing.T) {
	refs := mergeInstagramRefs([]VideoRef{
		{VideoID: "instagram_post_old", PublishedAtMs: 1000},
		{VideoID: "instagram_reel_new", PublishedAtMs: 3000},
		{VideoID: "instagram_post_old", PublishedAtMs: 2000},
	}, 2)
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d", len(refs))
	}
	if refs[0].VideoID != "instagram_reel_new" || refs[1].VideoID != "instagram_post_old" {
		t.Fatalf("unexpected order: %#v", refs)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func instagramAvatarURLForTest(name string, delta time.Duration) string {
	return fmt.Sprintf("https://cdn.example/%s.jpg?oe=%X", name, time.Now().Add(delta).Unix())
}
