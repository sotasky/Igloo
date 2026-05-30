package download

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIsDirectMedia(t *testing.T) {
	tests := []struct {
		url       string
		mediaType string
		want      bool
	}{
		{"https://pbs.twimg.com/media/abc.jpg", "photo", true},
		{"https://pbs.twimg.com/media/abc.jpg", "", true},
		{"https://video.twimg.com/ext_tw_video/123/pu/vid/720x1280/v.mp4", "video", true},
		{"https://video.twimg.com/tweet_video/abc.mp4", "gif", true},
		{"https://example.com/image.jpg", "photo", true},
		{"https://example.com/image.jpg", "image", true},
		{"https://www.youtube.com/watch?v=abc", "video", false},
		{"https://www.youtube.com/watch?v=abc", "", false},
		{"https://example.com/pbs.twimg.com/media/abc.jpg", "", false},
		{"file:///tmp/image.jpg", "photo", false},
	}
	for _, tt := range tests {
		got := isDirectMedia(tt.url, tt.mediaType)
		if got != tt.want {
			t.Errorf("isDirectMedia(%q, %q) = %v, want %v", tt.url, tt.mediaType, got, tt.want)
		}
	}
}

func TestMediaExtFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://video.twimg.com/ext_tw_video/123/pu/vid/720x1280/v.mp4", ".mp4"},
		{"https://pbs.twimg.com/media/abc.jpg", ".jpg"},
		{"https://pbs.twimg.com/media/abc.png", ".png"},
		{"https://pbs.twimg.com/media/abc.webp", ".webp"},
		{"https://example.com/noext", ".jpg"},
	}
	for _, tt := range tests {
		got := mediaExtFromURL(tt.url)
		if got != tt.want {
			t.Errorf("mediaExtFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestNextTwimgQuality(t *testing.T) {
	tests := []struct {
		url      string
		wantURL  string
		wantNext bool
	}{
		{
			"https://pbs.twimg.com/media/abc?format=jpg&name=orig",
			"https://pbs.twimg.com/media/abc?format=jpg&name=large",
			true,
		},
		{
			"https://pbs.twimg.com/media/abc?format=jpg&name=large",
			"https://pbs.twimg.com/media/abc?format=jpg&name=medium",
			true,
		},
		{
			"https://pbs.twimg.com/media/abc?format=jpg&name=medium",
			"https://pbs.twimg.com/media/abc?format=jpg&name=small",
			true,
		},
		{
			"https://pbs.twimg.com/media/abc?format=jpg&name=small",
			"",
			false,
		},
		{
			"https://pbs.twimg.com/media/abc?format=jpg",
			"",
			false,
		},
	}
	for _, tt := range tests {
		got, ok := nextTwimgQuality(tt.url)
		if ok != tt.wantNext {
			t.Errorf("nextTwimgQuality(%q) ok=%v, want %v", tt.url, ok, tt.wantNext)
		}
		if ok && got != tt.wantURL {
			t.Errorf("nextTwimgQuality(%q) = %q, want %q", tt.url, got, tt.wantURL)
		}
	}
}

// twimgTestTransport redirects pbs.twimg.com requests to a local test server.
type twimgTestTransport struct {
	srvURL string
}

func (t *twimgTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = t.srvURL
	return http.DefaultTransport.RoundTrip(req2)
}

func TestDownloadTwimgQualityFallback(t *testing.T) {
	// Server returns 404 for orig and large, 200 for medium.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "orig" || name == "large" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("image data"))
	}))
	defer srv.Close()

	client := &http.Client{Transport: &twimgTestTransport{srvURL: srv.Listener.Addr().String()}}
	d := &Downloader{
		HTTP: &HTTPDownloader{Client: client, AllowPrivateHosts: true},
	}
	dir := t.TempDir()
	paths, err := d.Download(context.Background(),
		"https://pbs.twimg.com/media/test?format=jpg&name=orig", "photo",
		Opts{OutputDir: dir, ID: "test_0"})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test_0.jpg"))
	if string(data) != "image data" {
		t.Errorf("unexpected content: %q", data)
	}
}

func TestDownloadTwimgAllQualitiesFail(t *testing.T) {
	// Server returns 404 for all qualities.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := &http.Client{Transport: &twimgTestTransport{srvURL: srv.Listener.Addr().String()}}
	d := &Downloader{
		HTTP: &HTTPDownloader{Client: client, AllowPrivateHosts: true},
	}
	dir := t.TempDir()
	_, err := d.Download(context.Background(),
		"https://pbs.twimg.com/media/test?format=jpg&name=orig", "photo",
		Opts{OutputDir: dir, ID: "test_0"})
	if err == nil {
		t.Fatal("expected error when all qualities fail")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDownloadDirectTwitterVideoUsesLargeHTTPBudget(t *testing.T) {
	const body = "video data"
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        make(http.Header),
			ContentLength: maxHTTPDownloadBytes + 1,
			Body:          io.NopCloser(bytes.NewBufferString(body)),
			Request:       req,
		}, nil
	})}
	d := &Downloader{
		HTTP: &HTTPDownloader{Client: client, AllowPrivateHosts: true},
	}
	dir := t.TempDir()

	paths, err := d.Download(context.Background(),
		"https://video.twimg.com/amplify_video/123/vid/avc1/3840x2160/video.mp4?tag=27", "video",
		Opts{OutputDir: dir, ID: "tweet_0"})
	if err != nil {
		t.Fatalf("expected direct video download to allow large response, got: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	data, err := os.ReadFile(filepath.Join(dir, "tweet_0.mp4"))
	if err != nil {
		t.Fatalf("read video: %v", err)
	}
	if string(data) != body {
		t.Errorf("unexpected content: %q", data)
	}
}

func TestDownloadRetriesCookieAlternatesForInstagramAuthFailure(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), `#!/bin/sh
out=""
cookie=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D)
      shift
      out="$1"
      ;;
    --cookies)
      shift
      cookie="$1"
      ;;
    --cookies-from-browser)
      shift
      cookie="browser:$1"
      ;;
  esac
  shift
done
if [ "$cookie" = "browser:firefox" ]; then
  mkdir -p "$out"
  printf 'video data' > "$out/source.mp4"
  printf '{"id":"source"}' > "$out/source.json"
  exit 0
fi
echo 'ERROR: login required; cookies missing' >&2
exit 1
`)
	writeExecutable(t, filepath.Join(bin, "yt-dlp"), `#!/bin/sh
echo 'ERROR: login required; cookies missing' >&2
exit 1
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cookieDir := t.TempDir()
	badCookie := filepath.Join(cookieDir, "instagram_cookies.txt")
	if err := os.WriteFile(badCookie, []byte("# stale\n"), 0o600); err != nil {
		t.Fatalf("write bad cookie: %v", err)
	}

	d := NewDownloader("")
	outDir := t.TempDir()
	paths, err := d.Download(context.Background(), "https://www.instagram.com/p/sample/", "video", Opts{
		OutputDir: outDir,
		ID:        "sample",
		CookieAlternates: []CookieSet{
			{File: badCookie},
			{Browser: "firefox"},
		},
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(outDir, "sample.mp4") {
		t.Fatalf("paths = %#v, want sample.mp4 in output dir", paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "video data" {
		t.Fatalf("output = %q, want video data", data)
	}
}

func TestDownloadRetriesCookieAlternateWhenYtDlpMasksInstagramGalleryDLAuth(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), `#!/bin/sh
out=""
cookie=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D)
      shift
      out="$1"
      ;;
    --cookies)
      shift
      cookie="$1"
      ;;
  esac
  shift
done
case "$cookie" in
  *good.txt)
    mkdir -p "$out"
    printf 'video data' > "$out/source.mp4"
    printf '{"id":"source"}' > "$out/source.json"
    exit 0
    ;;
esac
echo '[instagram][error] HTTP redirect to login page (https://www.instagram.com/accounts/login/)' >&2
exit 4
`)
	writeExecutable(t, filepath.Join(bin, "yt-dlp"), `#!/bin/sh
echo 'ERROR: [Instagram] sample: No video formats found!' >&2
exit 1
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cookieDir := t.TempDir()
	badCookie := filepath.Join(cookieDir, "bad.txt")
	goodCookie := filepath.Join(cookieDir, "good.txt")
	if err := os.WriteFile(badCookie, []byte("# stale\n"), 0o600); err != nil {
		t.Fatalf("write bad cookie: %v", err)
	}
	if err := os.WriteFile(goodCookie, []byte("# fresh\n"), 0o600); err != nil {
		t.Fatalf("write good cookie: %v", err)
	}

	d := NewDownloader("")
	outDir := t.TempDir()
	paths, err := d.Download(context.Background(), "https://www.instagram.com/p/sample/", "video", Opts{
		OutputDir: outDir,
		ID:        "sample",
		CookieAlternates: []CookieSet{
			{File: badCookie},
			{File: goodCookie},
		},
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(outDir, "sample.mp4") {
		t.Fatalf("paths = %#v, want sample.mp4 in output dir", paths)
	}
}

func TestDownloadRetriesCookieAlternateForInstagramAPIAccessDenial(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), `#!/bin/sh
out=""
cookie=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D)
      shift
      out="$1"
      ;;
    --cookies)
      shift
      cookie="$1"
      ;;
    --cookies-from-browser)
      shift
      cookie="browser:$1"
      ;;
  esac
  shift
done
if [ "$cookie" = "browser:firefox" ]; then
  mkdir -p "$out"
  printf 'video data' > "$out/source.mp4"
  printf '{"id":"source"}' > "$out/source.json"
  exit 0
fi
echo "[instagram][error] HttpError: '400 Bad Request' for 'https://instagram.invalid/api/v1/media/1234567890/info/'" >&2
exit 4
`)
	writeExecutable(t, filepath.Join(bin, "yt-dlp"), `#!/bin/sh
echo 'ERROR: [Instagram] sample: No video formats found!' >&2
exit 1
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cookieDir := t.TempDir()
	badCookie := filepath.Join(cookieDir, "instagram_cookies.txt")
	if err := os.WriteFile(badCookie, []byte("# stale\n"), 0o600); err != nil {
		t.Fatalf("write bad cookie: %v", err)
	}

	d := NewDownloader("")
	outDir := t.TempDir()
	paths, err := d.Download(context.Background(), "https://www.instagram.com/p/sample/", "video", Opts{
		OutputDir: outDir,
		ID:        "sample",
		CookieAlternates: []CookieSet{
			{File: badCookie},
			{Browser: "firefox"},
		},
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(outDir, "sample.mp4") {
		t.Fatalf("paths = %#v, want sample.mp4 in output dir", paths)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
