package download

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ytdlp "github.com/lrstanley/go-ytdlp"
)

func TestChannelCheckUsesFlatPlaylistAndPreservesPartialOutput(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "yt-dlp"), `#!/bin/sh
case " $* " in
  *" --flat-playlist "*) ;;
  *) exit 2 ;;
esac
printf '{"_type":"url","id":"sample_first","title":"First item","duration":12,"webpage_url":"https://www.youtube.com/watch?v=sample_first","timestamp":null}\n'
printf '{"_type":"url","id":"sample_second","title":"Second item","duration":34,"webpage_url":"https://www.youtube.com/watch?v=sample_second","timestamp":null}\n'
printf 'source stopped before the full window was read\n' >&2
exit 1
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	snapshot, err := (&YtDlpWrapper{}).ChannelCheck(context.Background(), "https://example.invalid/channel", 20)
	if err == nil {
		t.Fatal("ChannelCheck returned nil error after the command failed")
	}
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].Component != SourceComponentDirect {
		t.Fatalf("windows = %#v", snapshot.Windows)
	}
	window := snapshot.Windows[0]
	if window.Complete {
		t.Fatal("failed command returned a complete source window")
	}
	if len(window.Refs) != 2 || window.Refs[0].VideoID != "sample_first" || window.Refs[1].VideoID != "sample_second" {
		t.Fatalf("partial refs = %#v", window.Refs)
	}
	if window.Refs[0].Title != "First item" || window.Refs[0].Duration != 12 || window.Refs[0].URL != "https://www.youtube.com/watch?v=sample_first" {
		t.Fatalf("first ref = %#v", window.Refs[0])
	}
	if window.Refs[0].PublishedAtMs != 0 || window.Refs[1].PublishedAtMs != 0 {
		t.Fatalf("flat rows invented publish times: %#v", window.Refs)
	}
}

func TestChannelCheckTreatsSuccessfulEmptyOutputAsComplete(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "yt-dlp"), `#!/bin/sh
exit 0
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	snapshot, err := (&YtDlpWrapper{}).ChannelCheck(context.Background(), "https://example.invalid/channel", 20)
	if err != nil {
		t.Fatalf("ChannelCheck: %v", err)
	}
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].Component != SourceComponentDirect ||
		!snapshot.Windows[0].Complete || len(snapshot.Windows[0].Refs) != 0 {
		t.Fatalf("empty snapshot = %#v", snapshot)
	}
}

func TestCompletedYtDlpOutputsReturnsOnlyExactProducerSidecars(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"sample.mp4":  "video",
		"sample.jpg":  "exact thumbnail",
		"sample.webp": "stale sibling",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	completed := completedYtDlpOutputs(Opts{OutputDir: dir, ID: "sample"}, []string{filepath.Join(dir, "sample.mp4")}, map[string]any{"id": "sample"})
	if completed.InfoJSONPath != "" {
		t.Fatalf("info path = %q, want no redundant metadata sidecar", completed.InfoJSONPath)
	}
	if completed.ThumbnailPath != filepath.Join(dir, "sample.jpg") {
		t.Fatalf("thumbnail path = %q", completed.ThumbnailPath)
	}
	if len(completed.MediaPaths) != 1 || completed.MediaPaths[0] != filepath.Join(dir, "sample.mp4") {
		t.Fatalf("media paths = %#v", completed.MediaPaths)
	}
	if completed.Metadata["id"] != "sample" {
		t.Fatalf("metadata = %#v", completed.Metadata)
	}
}

func TestRunVideoDownloadExtractsFilenameAfterLogOutput(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "yt-dlp"), `#!/bin/sh
printf 'downloader warning\n'
printf '{"filename":"%s"}\n' "$1"
`)

	output := filepath.Join(t.TempDir(), "sample.mp4")
	paths, metadata, err := runVideoDownload(context.Background(), ytdlp.New().SetExecutable(filepath.Join(bin, "yt-dlp")), output)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != output {
		t.Fatalf("paths = %#v, want %q", paths, output)
	}
	if metadata["filename"] != output {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestDownloadSubtitlesUsesExplicitStateDirectory(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "yt-dlp"), `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--output)
      shift
      out="$1"
      ;;
  esac
  shift
done
target=$(printf '%s' "$out" | sed 's/%(ext)s/en.vtt/')
mkdir -p "$(dirname "$target")"
printf 'WEBVTT\n\nstate subtitle' > "$target"
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	mediaDir := filepath.Join(t.TempDir(), "media")
	subtitleDir := filepath.Join(t.TempDir(), "state", "subtitles", "youtube")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := (&YtDlpWrapper{}).DownloadSubtitles(context.Background(), "https://www.youtube.com/watch?v=sample", Opts{
		OutputDir: mediaDir, SubtitleDir: subtitleDir, ID: "sample-attempt-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(subtitleDir, "sample-attempt-1.en.vtt")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("subtitle paths = %#v, want %q", paths, want)
	}
	if entries, err := os.ReadDir(mediaDir); err != nil || len(entries) != 0 {
		t.Fatalf("subtitle wrote into media directory: entries=%v err=%v", entries, err)
	}
}

func TestCanonicalizeYouTubeChannelIDPrefersCanonicalURL(t *testing.T) {
	got := CanonicalizeYouTubeChannelID(
		"example_creator",
		"https://www.youtube.com/channel/UCEXAMPLE000000000000001",
		"https://www.youtube.com/@example_creator",
	)
	want := "youtube_UCEXAMPLE000000000000001"
	if got != want {
		t.Fatalf("CanonicalizeYouTubeChannelID(...) = %q; want %q", got, want)
	}
}

func TestCanonicalizeYouTubeChannelIDKeepsCanonicalInput(t *testing.T) {
	got := CanonicalizeYouTubeChannelID("youtube_UCEXAMPLE000000000000002")
	want := "youtube_UCEXAMPLE000000000000002"
	if got != want {
		t.Fatalf("CanonicalizeYouTubeChannelID(...) = %q; want %q", got, want)
	}
}

func TestExtractYouTubeChannelIDFromURLSupportsQueryParam(t *testing.T) {
	got := extractYouTubeChannelIDFromURL("https://www.youtube.com/feeds/videos.xml?channel_id=UCEXAMPLE000000000000002")
	want := "UCEXAMPLE000000000000002"
	if got != want {
		t.Fatalf("extractYouTubeChannelIDFromURL(...) = %q; want %q", got, want)
	}
}

func TestFetchInfoCommandUsesCookieFile(t *testing.T) {
	cmd := fetchInfoCommand(Opts{Cookies: "/config/cookies/youtube_cookies.txt"}).
		BuildCommand(context.Background(), "https://www.youtube.com/watch?v=test")
	args := cmd.Args

	if !slices.Contains(args, "--cookies") {
		t.Fatalf("fetch info args missing --cookies: %#v", args)
	}
	if !slices.Contains(args, "/config/cookies/youtube_cookies.txt") {
		t.Fatalf("fetch info args missing cookie path: %#v", args)
	}
	if slices.Contains(args, "--cookies-from-browser") {
		t.Fatalf("cookie file should take precedence over browser cookies: %#v", args)
	}
}

func TestFetchInfoCommandUsesBrowserCookiesWhenNoFile(t *testing.T) {
	cmd := fetchInfoCommand(Opts{CookiesFromBrowser: "firefox"}).
		BuildCommand(context.Background(), "https://www.youtube.com/watch?v=test")
	args := cmd.Args

	if !slices.Contains(args, "--cookies-from-browser") {
		t.Fatalf("fetch info args missing --cookies-from-browser: %#v", args)
	}
	if !slices.Contains(args, "firefox") {
		t.Fatalf("fetch info args missing browser name: %#v", args)
	}
	if slices.Contains(args, "--cookies") {
		t.Fatalf("browser cookies should not also set --cookies: %#v", args)
	}
}

func TestFetchCommentsCommandUsesExpandedThreadCap(t *testing.T) {
	cmd := fetchCommentsCommand(500, Opts{CookiesFromBrowser: "firefox"}).
		BuildCommand(context.Background(), "https://www.youtube.com/watch?v=test")
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "youtube:max_comments=500,500,500,100") {
		t.Fatalf("comment args missing expanded cap: %#v", cmd.Args)
	}
}

func TestParseCommentsJSONPreservesRepliesAndLikes(t *testing.T) {
	raw := []byte(`{"comments":[
		{"id":"top","author":"Creator","author_id":"UCcreator","author_thumbnail":"https://example.test/a.jpg","text":"hello","like_count":42,"timestamp":1710000000},
		{"id":"reply","parent":"top","author":"Viewer","author_id":"UCviewer","text":"reply","like_count":7,"timestamp":1710000001}
	]}`)

	comments, err := ParseCommentsDumpJSON(raw)
	if err != nil {
		t.Fatalf("ParseCommentsDumpJSON: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("comments len = %d, want 2", len(comments))
	}
	if comments[0].LikeCount != 42 {
		t.Fatalf("root like count = %d, want 42", comments[0].LikeCount)
	}
	if comments[1].ParentID != "top" {
		t.Fatalf("reply parent = %q, want top", comments[1].ParentID)
	}
	if comments[1].LikeCount != 7 {
		t.Fatalf("reply like count = %d, want 7", comments[1].LikeCount)
	}
}
