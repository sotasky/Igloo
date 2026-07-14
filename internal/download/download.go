package download

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/storage"
)

type MediaLane = storage.MediaLane

const (
	MediaLaneState          = storage.MediaLaneState
	MediaLaneBulkForeground = storage.MediaLaneBulkForeground
	MediaLaneBulkRegular    = storage.MediaLaneBulkRegular
	MediaLaneBulkBackground = storage.MediaLaneBulkBackground
)

const (
	maxHTTPVideoDownloadBytes       int64 = 4 << 30
	defaultHTTPVideoDownloadTimeout       = 2 * time.Hour
)

// Opts configures a Download call.
type Opts struct {
	OutputDir          string      // Directory to write files into.
	ID                 string      // Used in output filename when set.
	Cookies            string      // Path to cookies file (for yt-dlp).
	CookiesFromBrowser string      // Browser name for --cookies-from-browser (e.g. "firefox").
	CookieAlternates   []CookieSet // Ordered credential fallbacks.
	Format             string      // yt-dlp -f format string (overrides default FormatSort when set).
	Subtitles          bool        // Download English subtitles as VTT.
	SubtitleDir        string      // State-root directory for subtitle outputs.
}

// Downloader is the unified entry point that routes to the correct backend.
// Two download engines: yt-dlp (videos) and gallery-dl (slideshows, fallback).
// HTTP is a utility for direct CDN URLs (avatars, Twitter media).
type Downloader struct {
	YtDlp     *YtDlpWrapper
	GalleryDL *GalleryDLWrapper
	HTTP      *HTTPDownloader
	sink      OperationSink
	executor  *storage.MediaExecutor
}

// NewDownloader returns a Downloader with default clients.
func NewDownloader(cookiesDir string) *Downloader {
	d := &Downloader{
		YtDlp:     &YtDlpWrapper{CookiesDir: cookiesDir},
		GalleryDL: &GalleryDLWrapper{Runner: CommandRunner{}},
		HTTP:      NewHTTPDownloader(),
		executor:  storage.NewMediaExecutor(),
	}
	return d
}

func (d *Downloader) SetOperationSink(sink OperationSink) {
	d.sink = sink
	if d.YtDlp != nil {
		d.YtDlp.OperationSink = sink
	}
	if d.GalleryDL != nil {
		d.GalleryDL.OperationSink = sink
	}
}

func (d *Downloader) SetMediaExecutor(executor *storage.MediaExecutor) {
	if executor != nil {
		d.executor = executor
	}
}

// Download routes the request to the appropriate backend and returns
// the paths of all files that were written.
//
// Routing:
//  1. TikTok URL or Instagram non-reel URL → gallery-dl first for slideshows
//     and carousels; Instagram reel URL → yt-dlp first
//  2. Direct CDN (pbs.twimg.com, video.twimg.com, photo/image) → HTTP
//  3. Default → yt-dlp
func (d *Downloader) Download(ctx context.Context, lane MediaLane, rawURL string, mediaType string, opts Opts) ([]string, error) {
	completed, err := d.DownloadCompleted(ctx, lane, rawURL, mediaType, opts)
	return completed.MediaPaths, err
}

// DownloadCompleted routes the request while preserving every exact output
// path returned by the selected producer.
func (d *Downloader) DownloadCompleted(ctx context.Context, lane MediaLane, rawURL string, mediaType string, opts Opts) (CompletedDownload, error) {
	var completed CompletedDownload
	err := d.RunMedia(ctx, lane, func() error {
		var err error
		completed, err = d.downloadCompletedAdmitted(ctx, rawURL, mediaType, opts)
		return err
	})
	return completed, err
}

func (d *Downloader) DownloadSubtitles(ctx context.Context, lane MediaLane, rawURL string, opts Opts) ([]string, error) {
	var paths []string
	err := d.RunMedia(ctx, lane, func() error {
		var err error
		attempts := opts.cookieAttempts()
		for index, auth := range attempts {
			usedOpts := opts.withCookieSet(auth)
			if index > 0 {
				usedOpts.ID = fmt.Sprintf("%s-retry-%d", opts.ID, index+1)
			}
			paths, err = d.YtDlp.DownloadSubtitles(ctx, rawURL, usedOpts)
			if err == nil || index+1 >= len(attempts) || !shouldTryNextCookieAttempt(err) {
				return err
			}
		}
		return err
	})
	return paths, err
}

func (d *Downloader) downloadCompletedAdmitted(ctx context.Context, rawURL string, mediaType string, opts Opts) (CompletedDownload, error) {
	start := time.Now()
	platform := platformFromURL(rawURL)
	tool := "yt-dlp"
	if IsTikTokURL(rawURL) || IsInstagramURL(rawURL) {
		tool = "gallery-dl/yt-dlp"
	} else if isDirectMedia(rawURL, mediaType) {
		tool = "http"
	}
	var completed CompletedDownload
	var err error
	usedOpts := opts
	defer func() {
		files := len(completed.MediaPaths)
		recordOperation(ctx, d.sink, model.DownloaderOperation{
			Operation:   "media.download",
			Platform:    platform,
			Subject:     subjectForURL(rawURL),
			Tool:        tool,
			StartedAtMs: start.UnixMilli(),
			EndedAtMs:   time.Now().UnixMilli(),
			Status:      statusForError(err),
			ErrorKind:   ClassifyFailure(err, nil, 0).Kind,
			Error:       errorString(err, nil),
			CookieLabel: CookieLabel(usedOpts.Cookies, usedOpts.CookiesFromBrowser),
			FileCount:   files,
			MediaCount:  files,
		})
	}()
	attempts := opts.cookieAttempts()
	for i, auth := range attempts {
		usedOpts = opts.withCookieSet(auth)
		if i > 0 && usedOpts.ID != "" {
			usedOpts.ID = fmt.Sprintf("%s-retry-%d", opts.ID, i+1)
		}
		completed, err = d.downloadCompletedOnce(ctx, rawURL, mediaType, usedOpts)
		if err == nil {
			return completed, nil
		}
		if i+1 >= len(attempts) || !shouldTryNextCookieAttempt(err) {
			return completed, err
		}
		removeCompletedDownloadFiles(completed)
	}
	return completed, err
}

func (d *Downloader) RunMedia(ctx context.Context, lane MediaLane, work func() error) error {
	return d.executor.Run(ctx, lane, work)
}

func (d *Downloader) DownloadFile(ctx context.Context, lane MediaLane, rawURL, dir, filename string) (string, error) {
	return d.DownloadFileWithOptions(ctx, lane, rawURL, dir, filename, HTTPDownloadOptions{})
}

func (d *Downloader) DownloadFileWithOptions(ctx context.Context, lane MediaLane, rawURL, dir, filename string, opts HTTPDownloadOptions) (string, error) {
	var path string
	err := d.RunMedia(ctx, lane, func() error {
		var err error
		path, err = d.HTTP.DownloadFileWithOptions(ctx, rawURL, dir, filename, opts)
		return err
	})
	return path, err
}

func (d *Downloader) downloadCompletedOnce(ctx context.Context, rawURL string, mediaType string, opts Opts) (CompletedDownload, error) {
	if IsTikTokURL(rawURL) {
		return d.downloadTikTok(ctx, rawURL, opts)
	}
	if IsInstagramURL(rawURL) {
		return d.downloadInstagram(ctx, canonicalInstagramURL(rawURL), opts)
	}
	if isDirectMedia(rawURL, mediaType) {
		filename := opts.ID + mediaExtFromURL(rawURL)
		httpOpts := directMediaHTTPOptions(rawURL, mediaType)
		var p string
		var err error
		p, err = d.HTTP.DownloadFileWithOptions(ctx, rawURL, opts.OutputDir, filename, httpOpts)
		if err != nil {
			// Try lower quality variants for twimg photos that 403/404 on orig/large/etc.
			var httpErr *HTTPStatusError
			tryURL := rawURL
			for errors.As(err, &httpErr) && (httpErr.StatusCode == 404 || httpErr.StatusCode == 403) && isTwimgPhoto(tryURL) {
				fallbackURL, ok := nextTwimgQuality(tryURL)
				if !ok {
					break
				}
				log.Printf("[download] twimg 404 on %s, trying %s", tryURL, fallbackURL)
				tryURL = fallbackURL
				p, err = d.HTTP.DownloadFileWithOptions(ctx, fallbackURL, opts.OutputDir, filename, httpOpts)
			}
			if err != nil {
				return CompletedDownload{}, err
			}
		}
		return CompletedDownload{MediaPaths: []string{p}}, nil
	}
	return d.YtDlp.DownloadCompleted(ctx, rawURL, opts)
}

func (d *Downloader) downloadInstagram(ctx context.Context, rawURL string, opts Opts) (CompletedDownload, error) {
	if !isInstagramReelURL(rawURL) {
		return d.downloadGalleryDLFirst(ctx, rawURL, opts)
	}
	ytResult, ytErr := d.YtDlp.DownloadCompleted(ctx, rawURL, opts)
	if ytErr == nil && len(ytResult.MediaPaths) > 0 {
		return ytResult, nil
	}
	if ytErr == nil {
		ytErr = errors.New("yt-dlp returned no files")
	}
	gdlResult, gdlErr := d.GalleryDL.DownloadCompleted(ctx, rawURL, opts.OutputDir, opts.ID, opts.Cookies, opts.CookiesFromBrowser)
	if gdlErr == nil && len(gdlResult.MediaPaths) > 0 {
		return gdlResult, nil
	}
	return ytResult, fallbackDownloadError(gdlErr, ytErr)
}

func (opts Opts) cookieAttempts() []CookieSet {
	if len(opts.CookieAlternates) > 0 {
		sets := make([]CookieSet, 0, len(opts.CookieAlternates))
		seen := map[string]struct{}{}
		for _, set := range opts.CookieAlternates {
			set = normalizeCookieSet(set)
			key := set.File + "\x00" + set.Browser
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			sets = append(sets, set)
		}
		if len(sets) > 0 {
			return sets
		}
	}
	return []CookieSet{normalizeCookieSet(CookieSet{File: opts.Cookies, Browser: opts.CookiesFromBrowser})}
}

func (opts Opts) withCookieSet(set CookieSet) Opts {
	set = normalizeCookieSet(set)
	opts.Cookies = set.File
	opts.CookiesFromBrowser = set.Browser
	opts.CookieAlternates = nil
	return opts
}

func normalizeCookieSet(set CookieSet) CookieSet {
	set.File = strings.TrimSpace(set.File)
	set.Browser = strings.TrimSpace(set.Browser)
	if set.File != "" {
		set.Browser = ""
	}
	return set
}

func shouldTryNextCookieAttempt(err error) bool {
	if err == nil {
		return false
	}
	if ClassifyFailure(err, nil, 0).Kind == ErrorKindAuth {
		return true
	}
	text := strings.ToLower(err.Error())
	if containsInstagramAccessThrottleSignal(text) {
		return true
	}
	return containsAny(text, "login required", "not logged in", "use --cookies", "--cookies-from-browser")
}

func directMediaHTTPOptions(rawURL, mediaType string) HTTPDownloadOptions {
	if !isDirectVideoMedia(rawURL, mediaType) {
		return HTTPDownloadOptions{}
	}
	return HTTPDownloadOptions{
		MaxBytes: maxHTTPVideoDownloadBytes,
		Timeout:  defaultHTTPVideoDownloadTimeout,
	}
}

// downloadTikTok handles TikTok URLs with slideshow detection.
// gallery-dl is tried first — it handles slideshows natively (images + audio)
// with clean 1-based numbering. For regular videos, falls back to yt-dlp.
func (d *Downloader) downloadTikTok(ctx context.Context, rawURL string, opts Opts) (CompletedDownload, error) {
	// gallery-dl handles TikTok slideshows natively (images + audio).
	// It fails fast on regular videos, so there's no significant overhead.
	gdlResult, gdlErr := d.GalleryDL.DownloadCompleted(ctx, rawURL, opts.OutputDir, opts.ID, opts.Cookies, opts.CookiesFromBrowser)
	if gdlErr == nil && len(gdlResult.MediaPaths) > 0 {
		return gdlResult, nil
	}

	var httpErr *HTTPStatusError
	if ClassifyError(gdlErr, nil) == ErrorKindNotFound ||
		(errors.As(gdlErr, &httpErr) && httpErr.StatusCode == 403) {
		return CompletedDownload{}, gdlErr
	}

	// gallery-dl failed or returned nothing — it's a regular video. Use yt-dlp.
	ytResult, ytErr := d.YtDlp.DownloadCompleted(ctx, rawURL, opts)
	if ytErr == nil && len(ytResult.MediaPaths) > 0 {
		return ytResult, nil
	}
	if ytErr == nil {
		ytErr = errors.New("yt-dlp returned no files")
	}
	return ytResult, fallbackDownloadError(gdlErr, ytErr)
}

func (d *Downloader) downloadGalleryDLFirst(ctx context.Context, rawURL string, opts Opts) (CompletedDownload, error) {
	gdlResult, gdlErr := d.GalleryDL.DownloadCompleted(ctx, rawURL, opts.OutputDir, opts.ID, opts.Cookies, opts.CookiesFromBrowser)
	if gdlErr == nil && len(gdlResult.MediaPaths) > 0 {
		return gdlResult, nil
	}
	var httpErr *HTTPStatusError
	if ClassifyError(gdlErr, nil) == ErrorKindNotFound ||
		(errors.As(gdlErr, &httpErr) && httpErr.StatusCode == 403) {
		return CompletedDownload{}, gdlErr
	}
	ytResult, ytErr := d.YtDlp.DownloadCompleted(ctx, rawURL, opts)
	if ytErr == nil && len(ytResult.MediaPaths) > 0 {
		return ytResult, nil
	}
	if ytErr == nil {
		ytErr = errors.New("yt-dlp returned no files")
	}
	return ytResult, fallbackDownloadError(gdlErr, ytErr)
}

func fallbackDownloadError(primaryErr, fallbackErr error) error {
	if fallbackErr == nil {
		return fallbackErr
	}
	if primaryErr == nil {
		return fallbackErr
	}
	primaryKind := ClassifyError(primaryErr, nil)
	fallbackKind := ClassifyError(fallbackErr, nil)
	switch primaryKind {
	case ErrorKindAuth, ErrorKindRateLimit:
		if fallbackKind == ErrorKindUnknown || fallbackKind == ErrorKindEmptyResult {
			return primaryErr
		}
	}
	return fallbackErr
}

// isDirectMedia reports whether the URL or mediaType indicates media that
// should be fetched directly via HTTP rather than via yt-dlp.
// This covers Twitter CDN photos (pbs.twimg.com) and videos (video.twimg.com),
// as well as any URL with mediaType "photo" or "image".
func isDirectMedia(rawURL, mediaType string) bool {
	mt := strings.ToLower(mediaType)
	host, path, ok := httpURLParts(rawURL)
	if mt == "photo" || mt == "image" {
		return ok
	}
	if hostMatches(host, "pbs.twimg.com") && strings.HasPrefix(path, "/media/") {
		return true
	}
	if hostMatches(host, "video.twimg.com") {
		return true
	}
	return false
}

func isDirectVideoMedia(rawURL, mediaType string) bool {
	mt := strings.ToLower(mediaType)
	if mt == "video" || mt == "gif" || mt == "animated_gif" {
		return true
	}
	host, _, ok := httpURLParts(rawURL)
	return ok && hostMatches(host, "video.twimg.com")
}

// mediaExtFromURL returns the file extension for the given media URL.
// Detects .mp4, .png, and .webp from the URL path; defaults to .jpg.
func mediaExtFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ".jpg"
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".mp4":
		return ".mp4"
	case ".png":
		return ".png"
	case ".webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

// twimgQualities is the fallback chain for pbs.twimg.com photo sizes.
var twimgQualities = []string{"orig", "large", "medium", "small"}

// isTwimgPhoto reports whether the URL is a pbs.twimg.com photo.
func isTwimgPhoto(rawURL string) bool {
	host, path, ok := httpURLParts(rawURL)
	return ok && hostMatches(host, "pbs.twimg.com") && strings.HasPrefix(path, "/media/")
}

// nextTwimgQuality returns the URL with the next lower quality level.
// E.g. name=orig → name=large, name=large → name=medium.
func nextTwimgQuality(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	q := u.Query()
	current := q.Get("name")
	for i, qual := range twimgQualities {
		if qual == current && i+1 < len(twimgQualities) {
			q.Set("name", twimgQualities[i+1])
			u.RawQuery = q.Encode()
			return u.String(), true
		}
	}
	return "", false
}
