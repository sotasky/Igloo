package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
const maxHTTPDownloadBytes = 512 << 20

// HTTPStatusError is returned when an HTTP request receives a non-200 status code.
type HTTPStatusError struct {
	StatusCode int
	URL        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d for %s", e.StatusCode, e.URL)
}

// IsRateLimit returns true if the status code is 429.
func (e *HTTPStatusError) IsRateLimit() bool {
	return e.StatusCode == 429
}

// HTTPDownloader downloads files directly over HTTP.
type HTTPDownloader struct {
	Client *http.Client
}

// NewHTTPDownloader returns an HTTPDownloader with a default http.Client.
func NewHTTPDownloader() *HTTPDownloader {
	return &HTTPDownloader{Client: &http.Client{}}
}

// DownloadFile downloads url to destDir/filename using a temp file + atomic rename.
// Returns the final file path on success.
func (h *HTTPDownloader) DownloadFile(ctx context.Context, url, destDir, filename string) (string, error) {
	if _, _, ok := httpURLParts(url); !ok {
		return "", fmt.Errorf("unsupported URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := h.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &HTTPStatusError{StatusCode: resp.StatusCode, URL: url}
	}
	if resp.ContentLength > maxHTTPDownloadBytes {
		return "", fmt.Errorf("response too large: %d bytes", resp.ContentLength)
	}

	if filename == "" || filepath.Base(filename) != filename || strings.Contains(filename, "..") {
		return "", fmt.Errorf("unsafe filename")
	}
	dest := filepath.Join(destDir, filename)
	tmpPath := dest + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("create tmp file: %w", err)
	}

	// Clean up tmp file on any error path.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	limited := &io.LimitedReader{R: resp.Body, N: maxHTTPDownloadBytes + 1}
	written, err := io.Copy(f, limited)
	if err != nil {
		f.Close()
		return "", fmt.Errorf("write body: %w", err)
	}
	if written > maxHTTPDownloadBytes {
		f.Close()
		return "", fmt.Errorf("response too large")
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close tmp file: %w", err)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return "", fmt.Errorf("rename to dest: %w", err)
	}

	success = true
	return dest, nil
}
