package dearrow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -- stub types --

type stubClient struct {
	res Result
	err error
}

func (s *stubClient) Fetch(_ context.Context, _ string) (Result, error) {
	return s.res, s.err
}

type stubExtractor struct {
	called     bool
	gotPath    string
	gotTs      float64
	gotOut     string
	err        error
	writeBytes []byte
}

func (e *stubExtractor) Extract(_ context.Context, videoPath string, ts float64, outPath string) error {
	e.called = true
	e.gotPath = videoPath
	e.gotTs = ts
	e.gotOut = outPath
	if e.err != nil {
		return e.err
	}
	if e.writeBytes != nil {
		return os.WriteFile(outPath, e.writeBytes, 0o644)
	}
	return nil
}

// -- helpers --

func ptr[T any](v T) *T { return &v }

func newFetcher(t *testing.T, client ClientAPI, ext *stubExtractor, thumbDir string) *Fetcher {
	t.Helper()
	return &Fetcher{
		Client:   client,
		Extract:  ext.Extract,
		ThumbDir: thumbDir,
	}
}

// -- tests --

func TestFetchAndProcess_WritesThumbFromTimestamp(t *testing.T) {
	dir := t.TempDir()
	ext := &stubExtractor{writeBytes: []byte("fake-jpeg")}
	client := &stubClient{res: Result{
		Title:          ptr("Better Title"),
		CasualTitle:    ptr("Casual Title"),
		ThumbTimestamp: ptr(12.5),
	}}
	f := newFetcher(t, client, ext, dir)

	got, err := f.FetchAndProcess(context.Background(), "vid1", "/videos/vid1.mp4")
	if err != nil {
		t.Fatalf("FetchAndProcess: %v", err)
	}
	if got.Title == nil || *got.Title != "Better Title" {
		t.Errorf("Title = %v, want 'Better Title'", got.Title)
	}
	if got.CasualTitle == nil || *got.CasualTitle != "Casual Title" {
		t.Errorf("CasualTitle = %v, want 'Casual Title'", got.CasualTitle)
	}
	wantThumb := filepath.Join(dir, "vid1.jpg")
	if got.ThumbPath == nil || *got.ThumbPath != wantThumb {
		t.Errorf("ThumbPath = %v, want %q", got.ThumbPath, wantThumb)
	}
	if !ext.called {
		t.Fatal("extractor was not called")
	}
	if ext.gotTs != 12.5 {
		t.Errorf("extractor timestamp = %v, want 12.5", ext.gotTs)
	}
	if !strings.HasSuffix(ext.gotOut, "vid1.jpg") {
		t.Errorf("extractor outPath = %q, want suffix 'vid1.jpg'", ext.gotOut)
	}
}

func TestFetchAndProcess_NoThumbTimestamp_NoExtraction(t *testing.T) {
	dir := t.TempDir()
	ext := &stubExtractor{}
	client := &stubClient{res: Result{
		Title: ptr("Community Title"),
	}}
	f := newFetcher(t, client, ext, dir)

	got, err := f.FetchAndProcess(context.Background(), "vid2", "/videos/vid2.mp4")
	if err != nil {
		t.Fatalf("FetchAndProcess: %v", err)
	}
	if ext.called {
		t.Error("extractor should not be called when ThumbTimestamp is nil")
	}
	if got.ThumbPath != nil {
		t.Errorf("ThumbPath = %v, want nil", got.ThumbPath)
	}
	if got.Title == nil || *got.Title != "Community Title" {
		t.Errorf("Title = %v, want 'Community Title'", got.Title)
	}
}

func TestFetchAndProcess_NoVideoPath_NoExtraction(t *testing.T) {
	dir := t.TempDir()
	ext := &stubExtractor{}
	client := &stubClient{res: Result{
		Title:          ptr("Some Title"),
		ThumbTimestamp: ptr(5.0),
	}}
	f := newFetcher(t, client, ext, dir)

	got, err := f.FetchAndProcess(context.Background(), "vid3", "")
	if err != nil {
		t.Fatalf("FetchAndProcess: %v", err)
	}
	if ext.called {
		t.Error("extractor should not be called when videoPath is empty")
	}
	if got.ThumbPath != nil {
		t.Errorf("ThumbPath = %v, want nil", got.ThumbPath)
	}
	if got.Title == nil || *got.Title != "Some Title" {
		t.Errorf("Title = %v, want 'Some Title'", got.Title)
	}
}

func TestFetchAndProcess_ClientErrorReturnsError(t *testing.T) {
	dir := t.TempDir()
	ext := &stubExtractor{}
	client := &stubClient{err: errors.New("network failure")}
	f := newFetcher(t, client, ext, dir)

	got, err := f.FetchAndProcess(context.Background(), "vid4", "/videos/vid4.mp4")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got.Title != nil {
		t.Errorf("Title = %v, want nil on client error", got.Title)
	}
	if got.CasualTitle != nil {
		t.Errorf("CasualTitle = %v, want nil on client error", got.CasualTitle)
	}
	if got.ThumbPath != nil {
		t.Errorf("ThumbPath = %v, want nil on client error", got.ThumbPath)
	}
	if ext.called {
		t.Error("extractor should not be called on client error")
	}
}

func TestFetchAndProcess_ExtractorErrorPreservesTitles(t *testing.T) {
	dir := t.TempDir()
	ext := &stubExtractor{err: errors.New("ffmpeg exploded")}
	client := &stubClient{res: Result{
		Title:          ptr("Partial Title"),
		ThumbTimestamp: ptr(30.0),
	}}
	f := newFetcher(t, client, ext, dir)

	got, err := f.FetchAndProcess(context.Background(), "vid5", "/videos/vid5.mp4")
	if err == nil {
		t.Fatal("expected error from extractor, got nil")
	}
	// Title must still be populated so the caller can persist title-only branding.
	if got.Title == nil || *got.Title != "Partial Title" {
		t.Errorf("Title = %v, want 'Partial Title' even on extractor error", got.Title)
	}
	if got.ThumbPath != nil {
		t.Errorf("ThumbPath = %v, want nil when extraction failed", got.ThumbPath)
	}
}

func TestFetchAndProcess_MissingOutputFileDoesNotSetThumbPath(t *testing.T) {
	dir := t.TempDir()
	// Extractor succeeds but writes no bytes — file won't exist.
	ext := &stubExtractor{}
	client := &stubClient{res: Result{
		Title:          ptr("Good Title"),
		ThumbTimestamp: ptr(7.0),
	}}
	f := &Fetcher{
		Client:     client,
		Extract:    ext.Extract,
		ThumbDir:   dir,
		FileExists: func(_ string) bool { return false },
	}

	got, err := f.FetchAndProcess(context.Background(), "vid6", "/videos/vid6.mp4")
	if err != nil {
		t.Fatalf("FetchAndProcess: %v", err)
	}
	if got.ThumbPath != nil {
		t.Errorf("ThumbPath = %v, want nil when output file is missing", got.ThumbPath)
	}
	if got.Title == nil || *got.Title != "Good Title" {
		t.Errorf("Title = %v, want 'Good Title'", got.Title)
	}
}

func TestFetchAndProcess_CreatesThumbDirIfMissing(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "nested", "dearrow")
	ext := &stubExtractor{writeBytes: []byte("fake-jpeg")}
	client := &stubClient{res: Result{
		Title:          ptr("Dir Test"),
		ThumbTimestamp: ptr(3.0),
	}}
	f := newFetcher(t, client, ext, nested)

	got, err := f.FetchAndProcess(context.Background(), "vid7", "/videos/vid7.mp4")
	if err != nil {
		t.Fatalf("FetchAndProcess: %v", err)
	}
	wantThumb := filepath.Join(nested, "vid7.jpg")
	if got.ThumbPath == nil || *got.ThumbPath != wantThumb {
		t.Errorf("ThumbPath = %v, want %q", got.ThumbPath, wantThumb)
	}
	if _, statErr := os.Stat(nested); statErr != nil {
		t.Errorf("ThumbDir was not created: %v", statErr)
	}
}
