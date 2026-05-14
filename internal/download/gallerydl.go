package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const galleryDLDefaultTimeout = 2 * time.Hour

// GalleryDLWrapper wraps the gallery-dl CLI for downloading image slideshows.
type GalleryDLWrapper struct {
	Runner        CommandRunner
	OperationSink OperationSink
}

func (g *GalleryDLWrapper) Run(ctx context.Context, operation, platform, subject string, args []string, cookiesFile string, opts CommandOptions, cookiesBrowser ...string) CommandResult {
	result := g.Runner.Run(ctx, "gallery-dl", args, opts)
	browser := ""
	if len(cookiesBrowser) > 0 {
		browser = cookiesBrowser[0]
	}
	status := statusForError(result.Err)
	errorKind := ""
	errorText := ""
	if result.Err != nil {
		errorKind = ClassifyFailure(result.Err, result.CombinedOutput(), 0).Kind
		errorText = errorString(result.Err, result.CombinedOutput())
	}
	recordOperation(ctx, g.OperationSink, model.DownloaderOperation{
		Operation:   operation,
		Platform:    platform,
		Subject:     subjectForURL(subject),
		Tool:        "gallery-dl",
		StartedAtMs: result.StartedAtMs,
		EndedAtMs:   result.EndedAtMs,
		Status:      status,
		ErrorKind:   errorKind,
		Error:       errorText,
		CookieLabel: CookieLabel(cookiesFile, browser),
		ElapsedMs:   result.ElapsedMs,
		SummaryJSON: operationSummaryJSON(map[string]any{
			"args":      result.RedactedArgs,
			"exit_code": result.ExitCode,
		}),
	})
	return result
}

func appendCookieAuthArgs(args []string, cookiesFile, cookiesBrowser string) []string {
	cookiesFile = strings.TrimSpace(cookiesFile)
	cookiesBrowser = strings.TrimSpace(cookiesBrowser)
	if cookiesFile != "" {
		return append(args, "--cookies", cookiesFile)
	}
	if cookiesBrowser != "" {
		return append(args, "--cookies-from-browser", cookiesBrowser)
	}
	return args
}

// Reposts fetches TikTok repost metadata from gallery-dl's /@USER/reposts
// extractor without downloading media.
func (g *GalleryDLWrapper) Reposts(ctx context.Context, handle string, limit int, cookiesFile string) ([]VideoRef, error) {
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if handle == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rawURL := "https://www.tiktok.com/@" + handle + "/reposts"
	args := tiktokRepostArgs(limit, cookiesFile, rawURL)
	result := g.Run(ctx, "tiktok.reposts", "tiktok", rawURL, args, cookiesFile, CommandOptions{Timeout: 90 * time.Second})
	output := result.CombinedOutput()
	err := result.Err
	if err != nil {
		return nil, fmt.Errorf("gallery-dl reposts: %w: %s", err, RedactText(string(output)))
	}
	return parseTikTokRepostDump(output, handle), nil
}

func tiktokRepostArgs(limit int, cookiesFile, rawURL string) []string {
	if limit <= 0 {
		limit = 20
	}
	args := []string{
		"--dump-json",
		"--simulate",
		"-o", "tiktok-range=1-" + strconv.Itoa(limit),
	}
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	}
	args = append(args, rawURL)
	return args
}

func parseTikTokRepostDump(output []byte, reposterHandle string) []VideoRef {
	seen := map[string]struct{}{}
	var refs []VideoRef
	for _, payload := range galleryDLJSONPayloads(output) {
		for _, item := range flattenJSONObjects(payload) {
			ref := videoRefFromGalleryDLObject(item, reposterHandle)
			if ref.VideoID == "" {
				continue
			}
			if ref.IsRepost && ref.ChannelID == "" {
				continue
			}
			if _, ok := seen[ref.VideoID]; ok {
				continue
			}
			seen[ref.VideoID] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

func galleryDLJSONPayloads(output []byte) []any {
	return JSONPayloads(output)
}

func flattenJSONObjects(value any) []map[string]any {
	return FlattenJSONObjects(value)
}

var tiktokPostPathRe = regexp.MustCompile(`/@([^/]+)/video/([0-9]+)`)

func videoRefFromGalleryDLObject(item map[string]any, reposterHandle string) VideoRef {
	ref := VideoRef{
		IsRepost:          true,
		ReposterHandle:    strings.TrimPrefix(strings.TrimSpace(reposterHandle), "@"),
		ReposterChannelID: "tiktok_" + strings.ToLower(strings.TrimPrefix(strings.TrimSpace(reposterHandle), "@")),
	}
	ref.VideoID = firstString(item, "video_id", "aweme_id", "id", "post_id")
	ref.Title = firstString(item, "title", "description", "desc", "caption")
	ref.URL = firstString(item, "webpage_url", "post_url", "url", "permalink")
	ref.AuthorHandle = tikTokAuthorHandleFromGalleryDLObject(item, ref.URL)
	ref.AuthorDisplayName = firstString(item, "author_display_name", "nickname", "uploader")
	ref.RepostedAtMs = firstMillis(item, "reposted_at", "repost_time", "date", "timestamp", "created_at")
	if ref.VideoID == "" {
		if _, id := parseTikTokPostURL(ref.URL); id != "" {
			ref.VideoID = id
		}
	}
	if !model.IsValidTikTokVideoID(ref.VideoID) {
		ref.VideoID = ""
	}
	if !model.IsValidTikTokHandle(ref.AuthorHandle) {
		ref.AuthorHandle = ""
	}
	if ref.AuthorHandle != "" {
		ref.ChannelID = "tiktok_" + ref.AuthorHandle
	}
	return ref
}

func tikTokAuthorHandleFromGalleryDLObject(item map[string]any, rawURL string) string {
	var candidates []string
	for _, nestedKey := range []string{"author", "user", "owner"} {
		if nested, ok := item[nestedKey].(map[string]any); ok {
			for _, key := range []string{"uniqueId", "unique_id", "userUniqueId", "username"} {
				if s := stringFromAny(nested[key]); s != "" {
					candidates = append(candidates, s)
				}
			}
		}
	}
	for _, key := range []string{"author_username", "username", "uploader_id"} {
		if s := firstString(item, key); s != "" {
			candidates = append(candidates, s)
		}
	}
	if handle, _ := parseTikTokPostURL(rawURL); handle != "" {
		candidates = append(candidates, handle)
	}
	for _, candidate := range candidates {
		handle := model.NormalizeTikTokHandle(candidate)
		if handle != "" && !model.IsTikTokInternalID(handle) {
			return handle
		}
	}
	return ""
}

func parseTikTokPostURL(raw string) (string, string) {
	m := tiktokPostPathRe.FindStringSubmatch(raw)
	if len(m) != 3 {
		return "", ""
	}
	return strings.ToLower(m[1]), m[2]
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			if s := stringFromAny(v); s != "" {
				return s
			}
		}
	}
	for _, nestedKey := range []string{"author", "user", "owner"} {
		if nested, ok := item[nestedKey].(map[string]any); ok {
			for _, key := range keys {
				if v, ok := nested[key]; ok {
					if s := stringFromAny(v); s != "" {
						return s
					}
				}
			}
			for _, key := range []string{"uniqueId", "unique_id", "nickname", "name", "username"} {
				if v, ok := nested[key]; ok {
					if s := stringFromAny(v); s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v == 0 {
			return ""
		}
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	case int:
		if v == 0 {
			return ""
		}
		return strconv.Itoa(v)
	case int64:
		if v == 0 {
			return ""
		}
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

func firstMillis(item map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			if ms := millisFromAny(v); ms > 0 {
				return ms
			}
		}
	}
	return 0
}

func millisFromAny(value any) int64 {
	switch v := value.(type) {
	case float64:
		n := int64(v)
		if n > 0 && n < 100000000000 {
			return n * 1000
		}
		return n
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			return 0
		}
		if n < 100000000000 {
			return n * 1000
		}
		return n
	case json.Number:
		n, err := strconv.ParseInt(v.String(), 10, 64)
		if err != nil || n <= 0 {
			return 0
		}
		if n < 100000000000 {
			return n * 1000
		}
		return n
	default:
		return 0
	}
}

// Download fetches media from a URL using gallery-dl.
// Videos are renamed to {id}.{ext}; image slides are renamed to
// {id}_{1-based-index}.{ext}. Thumbnails next to videos are copied as
// {id}.{ext} so existing thumbnail discovery can use them.
// If cookiesFile is non-empty, it is passed via --cookies. Otherwise, an
// optional cookiesBrowser is passed via --cookies-from-browser.
func (g *GalleryDLWrapper) Download(ctx context.Context, rawURL, destDir, id, cookiesFile string, cookiesBrowser ...string) ([]string, error) {
	tmpDir, err := os.MkdirTemp("", "gallerydl-*")
	if err != nil {
		return nil, fmt.Errorf("gallery-dl tmpdir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()
	browser := ""
	if len(cookiesBrowser) > 0 {
		browser = strings.TrimSpace(cookiesBrowser[0])
	}

	args := []string{
		"--no-mtime",
		"--write-info-json",
		"-D", tmpDir,
	}
	args = appendCookieAuthArgs(args, cookiesFile, browser)
	args = append(args, rawURL)
	result := g.Run(ctx, "media.gallerydl", platformFromURL(rawURL), rawURL, args, cookiesFile, CommandOptions{Timeout: galleryDLDefaultTimeout}, browser)
	output := result.CombinedOutput()
	err = result.Err
	// TikTok posts that are deleted, private, or geo-restricted surface as
	// "Requested post not available" (exit 0, empty tmpdir). Without this
	// check we'd fall through to yt-dlp, which reports a misleading "IP
	// blocked" error and never prunes the job.
	if strings.Contains(string(output), "Requested post not available") {
		return nil, &HTTPStatusError{StatusCode: 404, URL: rawURL}
	}
	if err != nil {
		return nil, fmt.Errorf("gallery-dl: %w: %s", err, RedactText(string(output)))
	}

	// Collect downloaded files, sort for deterministic ordering
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("gallery-dl read dir: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("gallery-dl: no files downloaded")
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("gallery-dl mkdir: %w", err)
	}

	// Copy gallery-dl metadata → {id}.info.json so extractPublishedAt can read it.
	// gallery-dl creates per-file .json sidecars (not a single info.json), so
	// look for info.json first, then fall back to the first .json file found.
	var infoData []byte
	srcInfoJSON := filepath.Join(tmpDir, "info.json")
	if data, err := os.ReadFile(srcInfoJSON); err == nil {
		infoData = data
	} else {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
				if data, err := os.ReadFile(filepath.Join(tmpDir, e.Name())); err == nil {
					infoData = data
					break
				}
			}
		}
	}
	safeID := sanitizeDownloadID(id)
	if infoData != nil {
		_ = os.WriteFile(filepath.Join(destDir, safeID+".info.json"), infoData, 0o644)
	}

	// Separate media. Videos win over images for reel-style posts; image-only
	// posts keep 1-based slide numbering.
	imageExts := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	videoExts := map[string]bool{".mp4": true, ".mov": true, ".m4v": true, ".webm": true}
	audioExts := map[string]bool{".mp3": true, ".m4a": true, ".ogg": true, ".aac": true}

	var imageEntries, videoEntries, audioEntries []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if imageExts[ext] {
			imageEntries = append(imageEntries, e)
		} else if videoExts[ext] {
			videoEntries = append(videoEntries, e)
		} else if audioExts[ext] {
			audioEntries = append(audioEntries, e)
		}
	}

	var paths []string
	if len(videoEntries) > 0 {
		for idx, e := range videoEntries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			destName := safeID + ext
			if idx > 0 {
				destName = fmt.Sprintf("%s_%d%s", safeID, idx+1, ext)
			}
			destPath := filepath.Join(destDir, destName)
			srcPath := filepath.Join(tmpDir, e.Name())
			if err := copyFileStreaming(srcPath, destPath); err != nil {
				continue
			}
			paths = append(paths, destPath)
		}
		if len(imageEntries) > 0 {
			e := imageEntries[0]
			ext := strings.ToLower(filepath.Ext(e.Name()))
			destPath := filepath.Join(destDir, safeID+ext)
			if data, err := os.ReadFile(filepath.Join(tmpDir, e.Name())); err == nil {
				_ = os.WriteFile(destPath, data, 0o644)
			}
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("gallery-dl: no files after rename")
		}
		return paths, nil
	}

	// Images: {id}_{1-based}.{ext}
	for idx, e := range imageEntries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		destName := fmt.Sprintf("%s_%d%s", safeID, idx+1, ext)
		destPath := filepath.Join(destDir, destName)
		srcPath := filepath.Join(tmpDir, e.Name())
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			continue
		}
		paths = append(paths, destPath)
	}

	// Audio: {id}.{ext} (no index — separate from slide numbering)
	for _, e := range audioEntries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		destName := safeID + ext
		destPath := filepath.Join(destDir, destName)
		srcPath := filepath.Join(tmpDir, e.Name())
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			continue
		}
		// Audio path included so caller knows it exists, but listed after images
		paths = append(paths, destPath)
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("gallery-dl: no files after rename")
	}
	return paths, nil
}

func sanitizeDownloadID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.Contains(id, "..") {
		return "unknown"
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return "unknown"
	}
	return id
}

func copyFileStreaming(srcPath, destPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = src.Close()
	}()

	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	return copyStreamAndClose(src, dest)
}

func copyStreamAndClose(src io.Reader, dest io.WriteCloser) error {
	_, copyErr := io.Copy(dest, src)
	closeErr := dest.Close()
	return errors.Join(copyErr, closeErr)
}
