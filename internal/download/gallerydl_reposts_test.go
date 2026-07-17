package download

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTikTokRepostArgsUseExtractorRange(t *testing.T) {
	args := tiktokRepostArgs(20, "/tmp/cookies.txt", "https://www.tiktok.com/@reposter_one/reposts")
	if !slices.Contains(args, "tiktok-range=1-20") {
		t.Fatalf("args should use TikTok extractor range: %#v", args)
	}
	if slices.Contains(args, "--post-range") || slices.Contains(args, "--range") {
		t.Fatalf("args should not use generic range flags for TikTok reposts: %#v", args)
	}
	if !slices.Contains(args, "--cookies") || !slices.Contains(args, "/tmp/cookies.txt") {
		t.Fatalf("args should preserve cookies: %#v", args)
	}
	if !slices.Contains(args, "--write-pages") {
		t.Fatalf("args should capture the raw repost response: %#v", args)
	}
}

func TestTikTokRepostsCaptureEventTimeFromRawList(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), `#!/bin/sh
case "$PWD" in
  */igloo-tiktok-reposts-*) ;;
  *) exit 41 ;;
esac
case " $* " in
  *" --write-pages "*) ;;
  *) exit 42 ;;
esac
echo '{"itemList":[{"id":9000000000000000000,"repost_time":1710003600}]}' > "$PWD/01_https___www.tiktok.com_api_repost_item_list_.txt"
echo '[[2,{"id":"9000000000000000000","url":"https://www.tiktok.com/@sample_author/video/9000000000000000000","author":{"uniqueId":"sample_author"},"createTime":"1710000000"}]]'
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	refs, err := (&GalleryDLWrapper{Runner: CommandRunner{}}).Reposts(
		context.Background(), "sample_reposter", 1, "",
	)
	if err != nil {
		t.Fatalf("Reposts: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].RepostedAtMs != 1710003600000 {
		t.Fatalf("RepostedAtMs = %d, want raw repost event time", refs[0].RepostedAtMs)
	}
}

func TestParseTikTokRepostDumpSeparatesPublishAndRepostTimes(t *testing.T) {
	output := []byte(`[[2,{"id":"7350123456789012345","url":"https://www.tiktok.com/@author_one/video/7350123456789012345","description":"clip","author":{"uniqueId":"author_one","nickname":"Author One"},"createTime":"1710000000","date":"2024-03-09 16:00:00","reposted_at":1710003600}]]` + "\n")

	refs := parseTikTokRepostDump(output, "reposter_one")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	ref := refs[0]
	if ref.VideoID != "7350123456789012345" {
		t.Fatalf("VideoID = %q", ref.VideoID)
	}
	if !ref.IsRepost || ref.ReposterChannelID != "tiktok_reposter_one" || ref.ReposterHandle != "reposter_one" {
		t.Fatalf("unexpected reposter metadata: %+v", ref)
	}
	if ref.ChannelID != "tiktok_author_one" || ref.AuthorHandle != "author_one" || ref.AuthorDisplayName != "Author One" {
		t.Fatalf("unexpected author metadata: %+v", ref)
	}
	if ref.PublishedAtMs != 1710000000000 {
		t.Fatalf("PublishedAtMs = %d", ref.PublishedAtMs)
	}
	if ref.RepostedAtMs != 1710003600000 {
		t.Fatalf("RepostedAtMs = %d", ref.RepostedAtMs)
	}
}

func TestParseTikTokRepostDumpGalleryDLTuple(t *testing.T) {
	output := []byte(`[[2,{"id":"7633530281529494792","desc":"clip","author":{"uniqueId":".wayru.fx","nickname":"Wayru"},"createTime":"1777319779"}]]` + "\n")

	refs := parseTikTokRepostDump(output, "reposter_one")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	ref := refs[0]
	if ref.VideoID != "7633530281529494792" || ref.Title != "clip" {
		t.Fatalf("unexpected video metadata: %+v", ref)
	}
	if ref.ChannelID != "tiktok_.wayru.fx" || ref.AuthorHandle != ".wayru.fx" || ref.AuthorDisplayName != "Wayru" {
		t.Fatalf("unexpected author metadata: %+v", ref)
	}
	if ref.PublishedAtMs != 1777319779000 || ref.RepostedAtMs != 0 {
		t.Fatalf("publish/repost timestamps = %d/%d", ref.PublishedAtMs, ref.RepostedAtMs)
	}
}

func TestParseTikTokRepostDumpPrettyGalleryDLOutput(t *testing.T) {
	output := []byte(`[tiktok][info] https://www.tiktok.com/@reposter_one: retrieving repost/item_list page 1 (0 items)
[
  [
    2,
    {
      "id": "7633530281529494792",
      "desc": "pretty clip",
      "author": {
        "uniqueId": ".wayru.fx",
        "nickname": "Wayru"
      },
      "createTime": "1777319779"
    }
  ]
]
`)

	refs := parseTikTokRepostDump(output, "reposter_one")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	ref := refs[0]
	if ref.VideoID != "7633530281529494792" || ref.Title != "pretty clip" {
		t.Fatalf("unexpected video metadata: %+v", ref)
	}
	if ref.ChannelID != "tiktok_.wayru.fx" || ref.AuthorHandle != ".wayru.fx" || ref.AuthorDisplayName != "Wayru" {
		t.Fatalf("unexpected author metadata: %+v", ref)
	}
}

func TestParseTikTokRepostDumpSkipsInternalAuthorID(t *testing.T) {
	output := []byte(`{"id":"7000000000000000002","desc":"clip","author":"7000000000000000001","uploader_id":"sample_creator_42","nickname":"Sample Creator","webpage_url":"https://www.tiktok.com/@7000000000000000001/video/7000000000000000002"}` + "\n")

	refs := parseTikTokRepostDump(output, "reposter_one")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	ref := refs[0]
	if ref.ChannelID != "tiktok_sample_creator_42" || ref.AuthorHandle != "sample_creator_42" {
		t.Fatalf("unexpected author identity: %+v", ref)
	}
}

func TestParseTikTokRepostDumpDropsUnresolvedInternalAuthorID(t *testing.T) {
	output := []byte(`{"id":"7000000000000000002","desc":"clip","author":"7000000000000000001","webpage_url":"https://www.tiktok.com/@7000000000000000001/video/7000000000000000002"}` + "\n")

	refs := parseTikTokRepostDump(output, "reposter_one")
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0: %+v", len(refs), refs)
	}
}

func TestSanitizeDownloadIDPreservesSafeNonNumericIDs(t *testing.T) {
	for _, id := range []string{"BaW_jenozKc", "instagram_reel_ABC123", "tweet-123_ABC"} {
		if got := sanitizeDownloadID(id); got != id {
			t.Fatalf("sanitizeDownloadID(%q) = %q", id, got)
		}
	}
}

func TestSanitizeDownloadIDRejectsPathLikeIDs(t *testing.T) {
	for _, id := range []string{"", "../escape", "nested/path", `nested\path`, "bad..id"} {
		if got := sanitizeDownloadID(id); got != "unknown" {
			t.Fatalf("sanitizeDownloadID(%q) = %q, want unknown", id, got)
		}
	}
}
