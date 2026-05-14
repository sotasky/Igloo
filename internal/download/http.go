package download

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
const maxHTTPDownloadBytes int64 = 512 << 20
const defaultHTTPDownloadTimeout = 30 * time.Second

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
	Client            *http.Client
	AllowPrivateHosts bool
}

// HTTPDownloadOptions tunes the guardrails for one HTTP media download.
type HTTPDownloadOptions struct {
	MaxBytes int64
	Timeout  time.Duration
}

// NewHTTPDownloader returns an HTTPDownloader with a default http.Client.
func NewHTTPDownloader() *HTTPDownloader {
	return &HTTPDownloader{Client: newHTTPDownloadClient()}
}

// DownloadFile downloads url to destDir/filename using a temp file + atomic rename.
// Returns the final file path on success.
func (h *HTTPDownloader) DownloadFile(ctx context.Context, url, destDir, filename string) (string, error) {
	return h.DownloadFileWithOptions(ctx, url, destDir, filename, HTTPDownloadOptions{})
}

// DownloadFileWithOptions downloads url to destDir/filename using per-call
// limits. Zero option values preserve the default image-sized budget.
func (h *HTTPDownloader) DownloadFileWithOptions(ctx context.Context, url, destDir, filename string, opts HTTPDownloadOptions) (string, error) {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = maxHTTPDownloadBytes
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPDownloadTimeout
	}
	if timeout > 0 {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	url = strings.TrimSpace(url)
	if _, _, ok := httpURLParts(url); !ok {
		return "", fmt.Errorf("unsupported URL")
	}
	allowPrivateHosts := h != nil && h.AllowPrivateHosts
	if !allowPrivateHosts {
		if err := validatePublicDownloadURL(ctx, url); err != nil {
			return "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := h.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", &HTTPStatusError{StatusCode: resp.StatusCode, URL: url}
	}
	if resp.ContentLength > maxBytes {
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
			_ = os.Remove(tmpPath)
		}
	}()

	limited := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
	written, err := io.Copy(f, limited)
	if err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write body: %w", err)
	}
	if written > maxBytes {
		_ = f.Close()
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

func (h *HTTPDownloader) httpClient() *http.Client {
	if h != nil && h.Client != nil {
		return h.Client
	}
	return newHTTPDownloadClient()
}

func newHTTPDownloadClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:       http.ProxyFromEnvironment,
			DialContext: publicDownloadDialContext,
		},
	}
}

func publicDownloadDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := publicHostAddrs(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: defaultHTTPDownloadTimeout}
	var lastErr error
	for _, addr := range addrs {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("download host has no public addresses")
}

func validatePublicDownloadURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("unsupported URL")
	}
	_, err = publicHostAddrs(ctx, u.Hostname())
	return err
}

func publicHostAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.Trim(host, "[]")
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !isPublicDownloadAddr(addr) {
			return nil, fmt.Errorf("blocked non-public download host")
		}
		return []netip.Addr{addr}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve download host: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("download host has no addresses")
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		parsed, ok := netip.AddrFromSlice(addr.IP)
		if !ok {
			return nil, fmt.Errorf("blocked invalid download host address")
		}
		parsed = parsed.Unmap()
		if !isPublicDownloadAddr(parsed) {
			return nil, fmt.Errorf("blocked non-public download host")
		}
		out = append(out, parsed)
	}
	return out, nil
}

func isPublicDownloadAddr(addr netip.Addr) bool {
	if !addr.IsGlobalUnicast() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicDownloadAddrPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var nonPublicDownloadAddrPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}
