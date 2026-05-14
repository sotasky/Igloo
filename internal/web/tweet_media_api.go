package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/subscribe"
)

func (s *Server) registerTweetMediaAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tweet-media-next-index", s.handleTweetMediaNextIndex)
	mux.HandleFunc("POST /api/tweet-media-save", s.handleTweetMediaSave)
	mux.HandleFunc("POST /api/tweet-media-move", s.handleTweetMediaMove)
	mux.HandleFunc("POST /api/tweet-media-dl", s.handleTweetMediaDl)
}

// handleTweetMediaNextIndex returns the next sequential file number for a
// handle+label+category combo. Used by the Tampermonkey download preview.
func (s *Server) handleTweetMediaNextIndex(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	handle := r.URL.Query().Get("handle")
	label := r.URL.Query().Get("label")
	categoryID := r.URL.Query().Get("category_id")

	archivePath := s.archivePathForCategory(r, categoryID)
	if archivePath == "" {
		writeJSON(w, 200, map[string]any{"next_index": 1})
		return
	}

	safeName := sanitizeArchiveName(handle + " " + label)
	nextIndex := findNextArchiveIndex(archivePath, safeName)
	writeJSON(w, 200, map[string]any{"next_index": nextIndex})
}

// handleTweetMediaSave downloads images from provided URLs and archives them
// to the bookmark category's archive_path.
func (s *Server) handleTweetMediaSave(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body struct {
		URLs       []string `json:"urls"`
		Handle     string   `json:"handle"`
		Label      string   `json:"label"`
		CategoryID int64    `json:"category_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}
	if len(body.URLs) == 0 {
		writeJSON(w, 400, map[string]any{"success": false, "error": "no URLs provided"})
		return
	}

	archivePath := s.archivePathForCategoryID(r, body.CategoryID)
	if archivePath == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "category has no archive_path"})
		return
	}

	safeName := sanitizeArchiveName(body.Handle + " " + body.Label)
	startNum := findNextArchiveIndex(archivePath, safeName) - 1

	dl := s.workers.Downloader()
	ctx := r.Context()
	var saved []string
	for i, mediaURL := range body.URLs {
		if !isAllowedTweetMediaURL(mediaURL) {
			slog.Warn("[TweetMedia] rejected unsupported media URL", "url", mediaURL)
			continue
		}
		fileNum := startNum + i + 1
		destPath, err := archiveTweetMediaURL(ctx, dl, mediaURL, archivePath, safeName, fileNum)
		if err != nil {
			slog.Warn("[TweetMedia] download failed", "url", mediaURL, "err", err)
			continue
		}
		saved = append(saved, filepath.Base(destPath))
	}

	writeJSON(w, 200, map[string]any{
		"success": len(saved) > 0,
		"moved":   saved,
	})
}

// handleTweetMediaMove moves browser-downloaded staging files to the archive path.
// The tampermonkey script uses GM_download to save files to ~/Downloads, then
// calls this endpoint to move them into the correct category archive folder.
func (s *Server) handleTweetMediaMove(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body struct {
		Handle      string `json:"handle"`
		Label       string `json:"label"`
		CategoryID  int64  `json:"category_id"`
		StagedFiles []struct {
			StagingName string `json:"staging_name"`
			Ext         string `json:"ext"`
		} `json:"staged_files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}
	if body.Handle == "" || body.Label == "" || body.CategoryID == 0 || len(body.StagedFiles) == 0 {
		writeJSON(w, 400, map[string]any{"success": false, "error": "handle, label, category_id, and staged_files required"})
		return
	}

	archivePath := s.archivePathForCategoryID(r, body.CategoryID)
	if archivePath == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "category has no archive_path"})
		return
	}

	stagingDir, _ := s.db.GetSetting("x_media_staging_dir", "")
	if stagingDir == "" {
		home, _ := os.UserHomeDir()
		stagingDir = filepath.Join(home, "Downloads")
	}

	safeName := sanitizeArchiveName(body.Handle + " " + body.Label)
	startNum := findNextArchiveIndex(archivePath, safeName) - 1

	os.MkdirAll(archivePath, 0o755)

	var moved, failed []string
	for i, item := range body.StagedFiles {
		name := filepath.Base(item.StagingName) // prevent path traversal
		src := filepath.Join(stagingDir, name)

		// Poll up to 30s for the file to appear (browser download may be in progress)
		for attempt := 0; attempt < 60; attempt++ {
			if _, err := os.Stat(src); err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}

		if _, err := os.Stat(src); err != nil {
			slog.Warn("[TweetMediaMove] staging file not found", "src", src)
			failed = append(failed, name)
			continue
		}

		ext, err := normalizeTweetMediaExt(item.Ext, name)
		if err != nil {
			slog.Warn("[TweetMediaMove] rejected staged extension", "name", name, "ext", item.Ext, "err", err)
			failed = append(failed, name)
			continue
		}

		if err := validateTweetMediaStagingFile(src, ext); err != nil {
			slog.Warn("[TweetMediaMove] rejected staged file", "src", src, "err", err)
			failed = append(failed, name)
			_ = os.Remove(src)
			continue
		}

		fileNum := startNum + i + 1
		destFile := filepath.Join(archivePath, fmt.Sprintf("%s %03d%s", safeName, fileNum, ext))
		if err := moveFile(src, destFile); err != nil {
			slog.Warn("[TweetMediaMove] move failed", "src", src, "dst", destFile, "err", err)
			failed = append(failed, name)
			continue
		}
		moved = append(moved, filepath.Base(destFile))
		slog.Info("[TweetMediaMove] moved", "src", name, "dst", filepath.Base(destFile))
	}

	writeJSON(w, 200, map[string]any{
		"success": len(failed) == 0 && len(moved) > 0,
		"moved":   moved,
		"failed":  failed,
	})
}

// handleTweetMediaDl downloads a tweet's video via yt-dlp to the archive path.
func (s *Server) handleTweetMediaDl(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body struct {
		TweetURL   string `json:"tweet_url"`
		MediaURL   string `json:"media_url"`
		MediaID    string `json:"media_id"`
		MediaIndex *int   `json:"media_index"`
		Handle     string `json:"handle"`
		Label      string `json:"label"`
		CategoryID int64  `json:"category_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TweetURL == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "tweet_url required"})
		return
	}
	if err := subscribe.ValidateInput(body.TweetURL, "twitter"); err != nil {
		writeJSON(w, 422, map[string]any{"success": false, "error": "supported X URL required"})
		return
	}

	archivePath := s.archivePathForCategoryID(r, body.CategoryID)
	if archivePath == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "category has no archive_path"})
		return
	}

	safeName := sanitizeArchiveName(body.Handle + " " + body.Label)
	startNum := findNextArchiveIndex(archivePath, safeName)

	dl := s.workers.Downloader()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	if strings.TrimSpace(body.MediaURL) != "" {
		destPath, err := archiveTweetMediaURL(ctx, dl, body.MediaURL, archivePath, safeName, startNum)
		if err != nil {
			slog.Warn("[TweetMedia] direct media download failed", "url", body.MediaURL, "media_id", body.MediaID, "err", err)
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success": true,
			"moved":   []string{filepath.Base(destPath)},
		})
		return
	}

	tmpDir, err := os.MkdirTemp("", "igloo-tweetdl-*")
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": "cannot create temp dir"})
		return
	}
	defer os.RemoveAll(tmpDir)

	paths, dlErr := dl.YtDlp.Download(ctx, body.TweetURL, download.Opts{
		OutputDir: tmpDir,
		Cookies:   filepath.Join(s.cfg.CookiesDir, "x.com_cookies.txt"),
	})
	if dlErr != nil || len(paths) == 0 {
		errMsg := "yt-dlp failed"
		if dlErr != nil {
			full := dlErr.Error()
			if strings.Contains(full, "No video could be found") {
				errMsg = "no video in this tweet"
			} else {
				errMsg = full
			}
		}
		slog.Warn("[TweetMedia] yt-dlp failed", "url", body.TweetURL, "err", dlErr)
		writeJSON(w, 200, map[string]any{"success": false, "error": errMsg})
		return
	}
	if len(paths) > 1 {
		slog.Warn("[TweetMedia] yt-dlp returned multiple files for one selected video", "url", body.TweetURL, "media_id", body.MediaID, "media_index", body.MediaIndex, "count", len(paths))
		writeJSON(w, 200, map[string]any{"success": false, "error": "multiple videos returned; refresh X and try again"})
		return
	}

	var saved []string
	for i, p := range paths {
		ext, err := normalizeTweetMediaExt(filepath.Ext(p), p)
		if err != nil {
			slog.Warn("[TweetMedia] rejected downloaded extension", "path", p, "err", err)
			continue
		}
		fileNum := startNum + i
		destFile := filepath.Join(archivePath, fmt.Sprintf("%s %03d%s", safeName, fileNum, ext))
		if err := moveFile(p, destFile); err != nil {
			slog.Warn("[TweetMedia] move failed", "src", p, "dst", destFile, "err", err)
			continue
		}
		saved = append(saved, filepath.Base(destFile))
	}

	writeJSON(w, 200, map[string]any{
		"success": len(saved) > 0,
		"moved":   saved,
	})
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (s *Server) archivePathForCategory(r *http.Request, categoryIDStr string) string {
	catID, err := strconv.ParseInt(categoryIDStr, 10, 64)
	if err != nil || catID <= 0 {
		return ""
	}
	return s.archivePathForCategoryID(r, catID)
}

func (s *Server) archivePathForCategoryID(r *http.Request, catID int64) string {
	user := userFromContext(r.Context())
	if !bookmarkArchivePathsAllowed(user) {
		return ""
	}
	uid := user.Username
	cats, _ := s.db.GetBookmarkCategories(uid)
	for _, c := range cats {
		if c.ID == catID {
			archivePath, err := normalizeArchivePath(c.ArchivePath)
			if err != nil {
				slog.Warn("[TweetMedia] invalid archive path", "category_id", catID, "err", err)
				return ""
			}
			return archivePath
		}
	}
	return ""
}

func findNextArchiveIndex(archivePath, safeName string) int {
	maxNum := 0
	entries, _ := os.ReadDir(archivePath)
	namePrefix := safeName + " "
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, namePrefix) {
			numPart := strings.TrimPrefix(n, namePrefix)
			numPart = strings.TrimSuffix(numPart, filepath.Ext(numPart))
			if num, err := strconv.Atoi(strings.TrimSpace(numPart)); err == nil && num > maxNum {
				maxNum = num
			}
		}
	}
	return maxNum + 1
}

func tweetMediaExtFromURL(rawURL string) string {
	path := strings.SplitN(rawURL, "?", 2)[0]
	ext := filepath.Ext(path)
	if ext != "" {
		if safeExt, err := normalizeTweetMediaExt(ext, ""); err == nil {
			return safeExt
		}
	}
	if strings.Contains(rawURL, "format=png") {
		return ".png"
	}
	if strings.Contains(rawURL, "format=webp") {
		return ".webp"
	}
	return ".jpg"
}

func normalizeTweetMediaExt(raw, fallbackName string) (string, error) {
	ext := strings.TrimSpace(raw)
	if ext == "" {
		ext = filepath.Ext(fallbackName)
	}
	if ext == "" || ext == "." {
		return ".bin", nil
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	ext = strings.ToLower(ext)
	if strings.ContainsAny(ext, `/\`) || strings.Contains(ext, "..") || filepath.Base(ext) != ext {
		return "", fmt.Errorf("unsafe media extension %q", raw)
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".mp4", ".mov", ".bin":
		return ext, nil
	default:
		return "", fmt.Errorf("unsupported media extension %q", ext)
	}
}

func archiveTweetMediaURL(ctx context.Context, dl *download.Downloader, mediaURL, archivePath, safeName string, fileNum int) (string, error) {
	if dl == nil || dl.HTTP == nil {
		return "", fmt.Errorf("media downloader unavailable")
	}
	if !isAllowedTweetMediaURL(mediaURL) {
		return "", fmt.Errorf("unsupported media URL")
	}
	ext := tweetMediaExtFromURL(mediaURL)
	filename := fmt.Sprintf("%s %03d%s", safeName, fileNum, ext)
	if strings.EqualFold(ext, ".mp4") {
		destPath, err := dl.HTTP.DownloadFileWithOptions(ctx, mediaURL, archivePath, filename, download.HTTPDownloadOptions{
			MaxBytes: 4 << 30,
			Timeout:  2 * time.Hour,
		})
		if err != nil {
			return "", err
		}
		if err := validateTweetMediaStagingFile(destPath, ext); err != nil {
			_ = os.Remove(destPath)
			return "", err
		}
		return destPath, nil
	}
	return dl.HTTP.DownloadFile(ctx, mediaURL, archivePath, filename)
}

func validateTweetMediaStagingFile(path, ext string) error {
	if strings.ToLower(ext) != ".mp4" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}
	if n >= 12 && string(buf[4:8]) == "ftyp" {
		return nil
	}

	prefix := strings.ToLower(strings.TrimSpace(string(buf[:n])))
	if strings.HasPrefix(prefix, "<!doctype html") ||
		strings.HasPrefix(prefix, "<html") ||
		strings.Contains(prefix, "<html") {
		return fmt.Errorf("staged mp4 is HTML")
	}
	return fmt.Errorf("staged mp4 does not have an MP4 ftyp header")
}

func isAllowedTweetMediaURL(rawURL string) bool {
	u, err := parseHTTPURL(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "pbs.twimg.com":
		return strings.HasPrefix(u.EscapedPath(), "/media/")
	case "video.twimg.com":
		return true
	default:
		return false
	}
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	success = true
	_ = os.Remove(src)
	return nil
}
