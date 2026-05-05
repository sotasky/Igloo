package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPDownloadFile(t *testing.T) {
	content := []byte("hello world")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := &HTTPDownloader{Client: srv.Client()}

	got, err := dl.DownloadFile(context.Background(), srv.URL+"/test.jpg", dir, "test.jpg")
	if err != nil {
		t.Fatalf("DownloadFile error: %v", err)
	}

	want := filepath.Join(dir, "test.jpg")
	if got != want {
		t.Errorf("path: got %q, want %q", got, want)
	}

	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content: got %q, want %q", data, content)
	}

	// tmp file must be cleaned up.
	if _, err := os.Stat(want + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file was not removed")
	}
}

func TestHTTPDownloadFileContextCancel(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done // block until test signals
	}))
	defer func() {
		close(done)
		srv.Close()
	}()

	dir := t.TempDir()
	dl := &HTTPDownloader{Client: srv.Client()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := dl.DownloadFile(ctx, srv.URL+"/img.jpg", dir, "img.jpg")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}

	// tmp file must be cleaned up on error.
	if _, err := os.Stat(filepath.Join(dir, "img.jpg.tmp")); !os.IsNotExist(err) {
		t.Errorf("tmp file was not removed after cancel")
	}
}

func TestHTTPDownloadFileBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := &HTTPDownloader{Client: srv.Client()}

	_, err := dl.DownloadFile(context.Background(), srv.URL+"/missing.jpg", dir, "missing.jpg")
	if err == nil {
		t.Error("expected error for 404 status, got nil")
	}
}

func TestHTTPStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	h := NewHTTPDownloader()
	dir := t.TempDir()
	_, err := h.DownloadFile(context.Background(), ts.URL+"/test.jpg", dir, "test.jpg")
	if err == nil {
		t.Fatal("expected error for 429")
	}

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != 429 {
		t.Errorf("expected status 429, got %d", statusErr.StatusCode)
	}
	if !statusErr.IsRateLimit() {
		t.Error("expected IsRateLimit() to return true")
	}
}

func TestHTTPDownloadFileRejectsUnsafeFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := &HTTPDownloader{Client: srv.Client()}

	for _, filename := range []string{"../escape.jpg", "subdir/file.jpg", "", "..\\escape.jpg"} {
		if _, err := dl.DownloadFile(context.Background(), srv.URL+"/test.jpg", dir, filename); err == nil {
			t.Fatalf("expected unsafe filename %q to fail", filename)
		}
	}
}
