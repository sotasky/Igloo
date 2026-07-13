package download

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsInstagramURLRecognizesNativeHostsOnly(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://www.instagram.com/reel/sample/", true},
		{"https://instagram.com/p/sample/", true},
		{"https://vxinstagram.com/reel/sample/", false},
		{"https://www.vxinstagram.com/p/sample/", false},
		{"https://kkinstagram.com/reel/sample/", false},
		{"https://cdn.kkinstagram.com/p/sample/", false},
		{"https://example.com/instagram.com/reel/sample/", false},
		{"file:///tmp/instagram.mp4", false},
	}

	for _, tt := range tests {
		if got := IsInstagramURL(tt.raw); got != tt.want {
			t.Fatalf("IsInstagramURL(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestCanonicalInstagramURLLeavesMirrorHostsAlone(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{
			raw:  "https://vxinstagram.com/reel/sample/?utm_source=x",
			want: "https://vxinstagram.com/reel/sample/?utm_source=x",
		},
		{
			raw:  "http://kkinstagram.com/p/sample/",
			want: "http://kkinstagram.com/p/sample/",
		},
		{
			raw:  "https://example.com/p/sample/",
			want: "https://example.com/p/sample/",
		},
	}

	for _, tt := range tests {
		if got := canonicalInstagramURL(tt.raw); got != tt.want {
			t.Fatalf("canonicalInstagramURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestIsInstagramReelURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://www.instagram.com/reel/sample/", true},
		{"https://www.instagram.com/p/sample/", false},
		{"https://vxinstagram.com/reel/sample/", false},
	}
	for _, tt := range tests {
		if got := isInstagramReelURL(tt.raw); got != tt.want {
			t.Fatalf("isInstagramReelURL(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestInstagramReelUsesYtDlpBeforeGalleryDL(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "gallery-called")
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), `#!/bin/sh
printf called > "$GALLERY_MARKER"
echo 'gallery-dl should not be called for a reel when yt-dlp succeeds' >&2
exit 1
`)
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
file="${out%.*}.mp4"
mkdir -p "$(dirname "$file")"
printf 'video data' > "$file"
printf '{"_type":"video","id":"source","filename":"%s"}\n' "$file"
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GALLERY_MARKER", marker)

	outDir := t.TempDir()
	paths, err := NewDownloader("").Download(context.Background(), MediaLaneBulkForeground, "https://www.instagram.com/reel/sample/", "video", Opts{
		OutputDir: outDir,
		ID:        "sample",
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(outDir, "sample.mp4") {
		t.Fatalf("paths = %#v, want sample.mp4", paths)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("gallery-dl was called for a reel; marker stat err=%v", err)
	}
}
