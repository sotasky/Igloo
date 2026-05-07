package download

import (
	"context"
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
		w.Write([]byte("image data"))
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
