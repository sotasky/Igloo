package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// handleChannelAvatar serves cached avatars from disk only.
//
// NEVER add on-demand network fetching here. Missing avatars are queued
// via requestAvatarRecovery; the dedicated background worker
// (internal/worker/profile.go runProfileRefreshLoop) drains the queue and
// downloads them. The request path stays disk-only so a slow/failed source
// can't stall a page render or saturate the request goroutine pool.
func (s *Server) handleChannelAvatar(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if channelID == "" {
		http.NotFound(w, r)
		return
	}

	avatarPath := s.resolveAvatarPath(channelID)
	if avatarPath == "" {
		s.requestAvatarRecovery(channelID)
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
	w.Header().Set("Content-Type", detectImageContentType(avatarPath))
	http.ServeFile(w, r, avatarPath)
}

func (s *Server) requestAvatarRecovery(channelID string) {
	if s.requestAvatar == nil {
		return
	}
	channelID = normalizeAvatarRecoveryChannelID(channelID)
	if channelID == "" {
		return
	}
	s.requestAvatar(channelID)
}

func normalizeAvatarRecoveryChannelID(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	lower := strings.ToLower(channelID)
	switch {
	case strings.HasPrefix(lower, "twitter_"),
		strings.HasPrefix(lower, "tiktok_"),
		strings.HasPrefix(lower, "instagram_"):
		return lower
	default:
		// YouTube channel IDs are case-sensitive, so preserve the server-owned ID.
		return channelID
	}
}

// resolveAvatarPath scans the conventional avatar directory for a file
// matching channelID. The profile worker writes files here; no DB index.
func (s *Server) resolveAvatarPath(channelID string) string {
	return diskScan(filepath.Join(s.cfg.DataDir, "thumbnails", "avatars"), channelID)
}

func (s *Server) handleChannelBanner(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if channelID == "" {
		http.NotFound(w, r)
		return
	}
	bannerPath := s.resolveBannerPath(channelID)
	if bannerPath == "" {
		http.NotFound(w, r) // CSS renders a gradient fallback
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
	w.Header().Set("Content-Type", detectImageContentType(bannerPath))
	http.ServeFile(w, r, bannerPath)
}

// resolveBannerPath scans the conventional banner directory for a file
// matching channelID. TikTok channels have no banner — the caller returns
// 404 and the client CSS renders a gradient.
func (s *Server) resolveBannerPath(channelID string) string {
	return diskScan(filepath.Join(s.cfg.DataDir, "thumbnails", "banners"), channelID)
}

func diskScan(dir, channelID string) string {
	for _, ext := range []string{".jpg", ".png", ".webp", ".jpeg", ".gif"} {
		candidate := filepath.Join(dir, channelID+ext)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func detectImageContentType(path string) string {
	fallback := "image/jpeg"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		fallback = "image/png"
	case ".webp":
		fallback = "image/webp"
	case ".gif":
		fallback = "image/gif"
	}

	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return fallback
	}
	if n == 0 {
		return fallback
	}
	if detected := http.DetectContentType(buf[:n]); strings.HasPrefix(detected, "image/") {
		return detected
	}
	return fallback
}

// --- Thumbnail serving with cache ---

type thumbCacheEntry struct {
	path string
	at   time.Time
}

var (
	thumbCache   = make(map[string]thumbCacheEntry)
	thumbCacheMu sync.RWMutex
	thumbNegTTL  = 5 * time.Minute // negative results expire quickly so new media gets picked up
)

func rememberResolvedThumb(videoID, path string) {
	if videoID == "" || path == "" {
		return
	}
	thumbCacheMu.Lock()
	thumbCache[videoID] = thumbCacheEntry{path: path, at: time.Now()}
	thumbCacheMu.Unlock()
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	if videoID == "" {
		http.NotFound(w, r)
		return
	}

	// DeArrow branch: when the client asks for the DeArrow variant and we have
	// one on disk, serve it. On any miss (no path in DB, file gone, stat error),
	// fall through to the original resolver below so the <img> never renders as
	// broken. We deliberately don't touch thumbCache here — DeArrow and original
	// resolutions must not share cache slots.
	if r.URL.Query().Get("da") == "1" {
		if path := s.resolveDearrowThumb(videoID); path != "" {
			s.serveThumbFile(w, r, path)
			return
		}
		// Fall through to original resolution below.
	}

	// Fast path: check cache
	thumbCacheMu.RLock()
	cached, ok := thumbCache[videoID]
	thumbCacheMu.RUnlock()

	if ok {
		// Negative entries expire after thumbNegTTL so newly-downloaded media gets picked up.
		if cached.path == "" && time.Since(cached.at) > thumbNegTTL {
			ok = false
		}
	}

	if ok {
		if cached.path == "" {
			http.NotFound(w, r)
			return
		}
		s.serveThumbFile(w, r, cached.path)
		return
	}

	// Slow path: resolve thumbnail
	path := s.resolveThumb(videoID)

	thumbCacheMu.Lock()
	thumbCache[videoID] = thumbCacheEntry{path: path, at: time.Now()}
	thumbCacheMu.Unlock()

	if path == "" {
		http.NotFound(w, r)
		return
	}
	s.serveThumbFile(w, r, path)
}

func (s *Server) resolveThumb(videoID string) string {
	video, err := s.db.GetVideo(videoID)
	if err != nil || video == nil {
		return s.resolveThumbFromMedia(videoID)
	}
	if video.ThumbnailPath != "" {
		thumbPath := resolveDataPath(s.cfg.DataDir, video.ThumbnailPath)
		if _, err := os.Stat(thumbPath); err == nil {
			return thumbPath
		}
	}
	if video.FilePath == "" {
		return s.resolveThumbFromMedia(videoID)
	}

	filePath := resolveDataPath(s.cfg.DataDir, video.FilePath)

	// Thumbnail is next to the video file with an image extension (yt-dlp convention)
	base := strings.TrimSuffix(filePath, filepath.Ext(filePath))
	for _, ext := range []string{".webp", ".jpg", ".jpeg", ".png", ".image"} {
		candidate := base + ext
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Slideshow: file is {id}_0.mp3, first slide is {id}_1.jpg
	dir := filepath.Dir(filePath)
	if strings.HasSuffix(base, "_0") {
		slideBase := base[:len(base)-2] + "_1"
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			candidate := slideBase + ext
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}

	// Try {videoID}_1.jpg in the same directory
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		candidate := filepath.Join(dir, videoID+"_1"+ext)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// The file itself might be an image (TikTok/Instagram slideshows)
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".webp" || ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".image" {
		if _, err := os.Stat(filePath); err == nil {
			return filePath
		}
	}

	// Generated thumbnails
	genPath := filepath.Join(s.cfg.DataDir, "thumbnails", "generated", videoID+".jpg")
	if _, err := os.Stat(genPath); err == nil {
		return genPath
	}

	// Last resort: extract first frame from the video file itself
	videoExt := strings.ToLower(filepath.Ext(filePath))
	if videoExt == ".mp4" || videoExt == ".webm" || videoExt == ".mkv" {
		if _, err := os.Stat(filePath); err == nil {
			if err := os.MkdirAll(filepath.Dir(genPath), 0o755); err == nil {
				if generateJPEGThumbnail(filePath, genPath) {
					rememberResolvedThumb(videoID, genPath)
					return genPath
				}
			}
		}
	}

	// Fall back to media_files table (may have entries even when video.file_path is set)
	return s.resolveThumbFromMedia(videoID)
}

// resolveDearrowThumb returns the absolute path to the DeArrow-voted thumbnail
// for videoID, or "" when none is available. Handles both relative
// (data-dir-rooted) and absolute stored paths. Does no caching — caller calls
// at most once per request, and caching would mask a newly-extracted DeArrow
// frame for up to thumbNegTTL.
func (s *Server) resolveDearrowThumb(videoID string) string {
	v, err := s.db.GetVideo(videoID)
	if err != nil || v == nil || v.DearrowThumbPath == nil || *v.DearrowThumbPath == "" {
		return ""
	}
	p := *v.DearrowThumbPath
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.cfg.DataDir, p)
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// resolveThumbFromMedia finds a thumbnail from feed_media files (for bookmark stubs without file_path).
func (s *Server) resolveThumbFromMedia(videoID string) string {
	// Use the first slide (index 0) as thumbnail.
	path := s.findFeedMediaFile(videoID, 0)
	if path == "" {
		return ""
	}

	// If the media file is an image, use it directly.
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".image":
		return path
	}

	// Video file — extract first frame with ffmpeg.
	genDir := filepath.Join(s.cfg.DataDir, "thumbnails", "generated")
	genPath := filepath.Join(genDir, videoID+".jpg")
	if _, err := os.Stat(genPath); err == nil {
		return genPath // already generated
	}
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		return ""
	}
	if generateJPEGThumbnail(path, genPath) {
		rememberResolvedThumb(videoID, genPath)
		return genPath
	}
	return ""
}

func generateJPEGThumbnail(inputPath, outputPath string) bool {
	tmpPath := outputPath + ".tmp.jpg"
	_ = os.Remove(tmpPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-i", inputPath, "-frames:v", "1", "-q:v", "2", "-f", "image2", "-y", tmpPath)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		return false
	}

	info, err := os.Stat(tmpPath)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(tmpPath)
		return false
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		_ = os.Remove(tmpPath)
		return false
	}
	return true
}

func (s *Server) serveThumbFile(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	contentType := "image/jpeg"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		contentType = "image/png"
	case ".webp":
		contentType = "image/webp"
	case ".gif":
		contentType = "image/gif"
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, path)
}

// --- Video file serving ---

func (s *Server) handleDownloadVideo(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	video, err := s.db.GetVideo(videoID)
	if err != nil || video == nil {
		http.NotFound(w, r)
		return
	}
	if video.FilePath == "" {
		http.NotFound(w, r)
		return
	}
	absPath := resolveDataPath(s.cfg.DataDir, video.FilePath)
	if _, err := os.Stat(absPath); err != nil {
		http.NotFound(w, r)
		return
	}

	filename := sanitizeFilename(video.Title) + filepath.Ext(absPath)
	if filename == filepath.Ext(absPath) {
		filename = "video_" + videoID + filepath.Ext(absPath)
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeFile(w, r, absPath)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	s := replacer.Replace(strings.TrimSpace(name))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
