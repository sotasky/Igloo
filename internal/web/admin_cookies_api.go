package web

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/download"
)

// ── Cookies ───────────────────────────────────────────────────────────────────

const (
	cookieUploadMaxBodyBytes   int64 = 16 << 20
	cookieUploadMaxMemoryBytes int64 = 1 << 20
)

func (s *Server) handleGetCookies(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if r.Header.Get("HX-Request") != "" {
		s.renderCookieRowsHTML(w, r)
		return
	}

	platforms := s.enabledPlatforms()
	result := make(map[string]any, len(platforms))
	for _, platform := range platforms {
		candidates := download.DiscoverCookieFiles(s.cfg.CookiesDir, platform)
		var size int64
		filenames := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			info, err := os.Stat(candidate.Path)
			if err != nil {
				continue
			}
			size += info.Size()
			filenames = append(filenames, filepath.Base(candidate.Path))
		}
		filename := platform + "_cookies.txt"
		if len(filenames) > 0 {
			filename = filenames[0]
		}
		enabledVal, _ := s.db.GetSetting("cookies_"+platform+"_enabled", "1")
		browserVal, _ := s.db.GetSetting("cookies_"+platform+"_browser", "")
		result[platform] = map[string]any{
			"filename":   filename,
			"filenames":  filenames,
			"file_count": len(filenames),
			"exists":     len(filenames) > 0,
			"size":       size,
			"enabled":    enabledVal == "1",
			"browser":    browserVal,
		}
	}
	writeJSON(w, 200, result)
}

var cookiePlatformNames = []struct{ ID, Name string }{
	{"twitter", "X / Twitter"},
	{"youtube", "YouTube"},
	{"tiktok", "TikTok"},
	{"instagram", "Instagram"},
}

func (s *Server) renderCookieRowsHTML(w http.ResponseWriter, r *http.Request) {
	var rows []components.CookieRowData
	for _, p := range cookiePlatformNames {
		if !s.platformEnabled(p.ID) {
			continue
		}
		candidates := download.DiscoverCookieFiles(s.cfg.CookiesDir, p.ID)
		enabledVal, _ := s.db.GetSetting("cookies_"+p.ID+"_enabled", "1")
		browserVal, _ := s.db.GetSetting("cookies_"+p.ID+"_browser", "")
		rows = append(rows, components.CookieRowData{
			Platform:  p.ID,
			Name:      p.Name,
			Exists:    len(candidates) > 0,
			Enabled:   enabledVal == "1",
			Browser:   browserVal,
			FileCount: len(candidates),
		})
	}
	_ = components.CookieRowsPanel(s.pageProps(w, r), rows).Render(r.Context(), w)
}

func (s *Server) handleUploadCookie(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}

	if requestContentLengthTooLarge(r, cookieUploadMaxBodyBytes) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
		return
	}
	limitRequestBody(w, r, cookieUploadMaxBodyBytes)
	if err := r.ParseMultipartForm(cookieUploadMaxMemoryBytes); err != nil {
		if requestBodyTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
			return
		}
		writeJSON(w, 400, map[string]any{"error": "multipart parse error"})
		return
	}
	if r.MultipartForm == nil {
		writeJSON(w, 400, map[string]any{"error": "missing file"})
		return
	}
	defer func() {
		_ = r.MultipartForm.RemoveAll()
	}()
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeJSON(w, 400, map[string]any{"error": "missing file"})
		return
	}

	if err := os.MkdirAll(s.cfg.CookiesDir, 0o700); err != nil {
		slog.Error("MkdirAll cookies", "err", err)
		writeJSON(w, 500, map[string]any{"error": "mkdir error"})
		return
	}
	written := make([]string, 0, len(files))
	used := make(map[string]struct{}, len(files))
	multiple := len(files) > 1
	for i, header := range files {
		file, err := header.Open()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "open error"})
			return
		}
		data, readErr := readLimitedBody(file, cookieUploadMaxBodyBytes)
		closeErr := file.Close()
		if readErr != nil {
			if requestBodyTooLarge(readErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
				return
			}
			writeJSON(w, 500, map[string]any{"error": "read error"})
			return
		}
		if closeErr != nil {
			writeJSON(w, 500, map[string]any{"error": "close error"})
			return
		}
		dest := cookieUploadPath(s.cfg.CookiesDir, platform, header.Filename, i, multiple, used)
		if err := atomicWrite(dest, data, 0o600); err != nil {
			slog.Error("write cookie", "platform", platform, "err", err)
			writeJSON(w, 500, map[string]any{"error": "write error"})
			return
		}
		written = append(written, filepath.Base(dest))
	}
	if r.Header.Get("HX-Request") != "" {
		s.resetDownloadAuthFailuresAfterCookieChange(platform)
		s.renderCookieRowsHTML(w, r)
		return
	}
	s.resetDownloadAuthFailuresAfterCookieChange(platform)
	writeJSON(w, 200, map[string]any{"success": true, "platform": platform, "files": written})
}

func cookieUploadPath(cookiesDir, platform, filename string, index int, multiple bool, used map[string]struct{}) string {
	name := platform + "_cookies.txt"
	if multiple && index > 0 {
		base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		suffix := sanitizeCookieFilenamePart(base)
		if suffix == "" || suffix == "cookies" || suffix == platform || suffix == platform+"_cookies" {
			suffix = strconv.Itoa(index + 1)
		}
		name = platform + "_cookies_" + suffix + ".txt"
	}
	return uniqueCookieUploadPath(cookiesDir, name, used)
}

func sanitizeCookieFilenamePart(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func uniqueCookieUploadPath(cookiesDir, name string, used map[string]struct{}) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	ext := filepath.Ext(name)
	if ext == "" {
		ext = ".txt"
	}
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = base + "_" + strconv.Itoa(i+1) + ext
		}
		if _, ok := used[candidate]; ok {
			continue
		}
		used[candidate] = struct{}{}
		return filepath.Join(cookiesDir, candidate)
	}
}

func (s *Server) handleToggleCookie(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}

	key := "cookies_" + platform + "_enabled"
	current, _ := s.db.GetSetting(key, "1")
	newVal := "0"
	if current == "0" {
		newVal = "1"
	}
	if err := s.db.SetSetting(key, newVal); err != nil {
		slog.Error("SetSetting cookie toggle", "platform", platform, "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	s.resetDownloadAuthFailuresAfterCookieChange(platform)
	if r.Header.Get("HX-Request") != "" {
		s.renderCookieRowsHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "enabled": newVal == "1"})
}

var validBrowsers = map[string]bool{
	"firefox": true, "chrome": true, "chromium": true, "brave": true, "edge": true, "": true,
}

func (s *Server) handleSetCookieBrowser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	isHTMX := r.Header.Get("HX-Request") != ""
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}

	var browser string
	if isHTMX {
		browser = r.FormValue("browser")
	} else {
		var body struct {
			Browser string `json:"browser"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			if requestBodyTooLarge(err) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": requestBodyTooLargeMessage})
				return
			}
			writeJSON(w, 400, map[string]any{"error": "bad request"})
			return
		}
		browser = body.Browser
	}

	if !validBrowsers[browser] {
		writeJSON(w, 400, map[string]any{"error": "unsupported browser"})
		return
	}
	key := "cookies_" + platform + "_browser"
	if err := s.db.SetSetting(key, browser); err != nil {
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	s.resetDownloadAuthFailuresAfterCookieChange(platform)
	if isHTMX {
		s.renderCookieRowsHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "browser": browser})
}

func (s *Server) handleDeleteCookie(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	platform := r.PathValue("platform")
	if !s.platformEnabled(platform) {
		writeJSON(w, 400, map[string]any{"error": "unknown platform"})
		return
	}
	candidates := download.DiscoverCookieFiles(s.cfg.CookiesDir, platform)
	if len(candidates) == 0 {
		candidates = []download.CookieCandidate{{Path: filepath.Join(s.cfg.CookiesDir, platform+"_cookies.txt")}}
	}
	for _, candidate := range candidates {
		if err := os.Remove(candidate.Path); err != nil && !os.IsNotExist(err) {
			slog.Error("delete cookie", "platform", platform, "err", err)
			writeJSON(w, 500, map[string]any{"error": "delete error"})
			return
		}
	}
	s.resetDownloadAuthFailuresAfterCookieChange(platform)
	if r.Header.Get("HX-Request") != "" {
		s.renderCookieRowsHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) resetDownloadAuthFailuresAfterCookieChange(platform string) {
	if s == nil || s.db == nil || !s.platformEnabled(platform) {
		return
	}
	fileEnabled, _ := s.db.GetSetting("cookies_"+platform+"_enabled", "1")
	browser, _ := s.db.GetSetting("cookies_"+platform+"_browser", "")
	cookiesDir := ""
	if s.cfg != nil {
		cookiesDir = s.cfg.CookiesDir
	}
	if len(download.ResolveCookieSets(cookiesDir, platform, fileEnabled != "0", browser)) == 0 {
		return
	}
	n, err := s.db.ResetDownloadAuthFailuresForPlatform(platform)
	if err != nil {
		slog.Error("reset auth-failed downloads after cookie change", "platform", platform, "err", err)
		return
	}
	if n > 0 {
		slog.Info("reset auth-failed downloads after cookie change", "platform", platform, "count", n)
	}
}
