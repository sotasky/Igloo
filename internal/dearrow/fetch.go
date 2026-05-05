package dearrow

import (
	"context"
	"os"
	"path/filepath"
)

// ClientAPI is the narrow interface Fetcher depends on, defined here so
// tests can stub it without a network.
type ClientAPI interface {
	Fetch(ctx context.Context, videoID string) (Result, error)
}

// ExtractFunc matches ExtractFrame's signature — isolated as a type so
// Fetcher can be constructed with a stub in tests.
type ExtractFunc func(ctx context.Context, videoPath string, timestamp float64, outPath string) error

// Fetcher orchestrates a full DeArrow check for one video: API call +
// optional thumbnail frame extraction. It performs no database writes.
// The caller decides whether + how to persist the Processed result.
type Fetcher struct {
	Client   ClientAPI
	Extract  ExtractFunc
	ThumbDir string // absolute path where extracted frames are written
	// FileExists is used after extraction to confirm the output file landed.
	// Leave nil to use os.Stat. Tests may override.
	FileExists func(path string) bool
}

// Processed is the DB-ready outcome of a single DeArrow check.
// Any combination of fields may be nil — caller should treat nil as
// "no override, use original".
type Processed struct {
	Title       *string
	CasualTitle *string
	ThumbPath   *string // absolute path to the extracted frame, if any
}

// FetchAndProcess fetches DeArrow branding for videoID and, when the API
// returns a thumbnail timestamp AND a videoPath is available, extracts
// the frame into ThumbDir/<videoID>.jpg.
//
// Failure modes:
//   - API error -> returns the error, Processed zero-value.
//   - Extraction error when a timestamp was present -> returns the error,
//     but the Processed still carries any title data so the caller can
//     decide to persist the title-only branding.
//   - Missing output file after a successful ffmpeg run is not an error;
//     ThumbPath stays nil.
func (f *Fetcher) FetchAndProcess(ctx context.Context, videoID, videoPath string) (Processed, error) {
	res, err := f.Client.Fetch(ctx, videoID)
	if err != nil {
		return Processed{}, err
	}
	out := Processed{Title: res.Title, CasualTitle: res.CasualTitle}

	if res.ThumbTimestamp == nil || videoPath == "" {
		return out, nil
	}

	if err := os.MkdirAll(f.ThumbDir, 0o755); err != nil {
		return out, err
	}
	dst := filepath.Join(f.ThumbDir, videoID+".jpg")
	if err := f.Extract(ctx, videoPath, *res.ThumbTimestamp, dst); err != nil {
		// Preserve titles we already collected; caller may still persist them.
		return out, err
	}
	exists := f.FileExists
	if exists == nil {
		exists = func(p string) bool { _, e := os.Stat(p); return e == nil }
	}
	if exists(dst) {
		pathCopy := dst
		out.ThumbPath = &pathCopy
	}
	return out, nil
}
