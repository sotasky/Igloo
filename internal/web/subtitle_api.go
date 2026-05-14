package web

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func (s *Server) registerSubtitleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/media/subtitle/{videoID}", s.handleSubtitle)
}

func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	video, err := s.db.GetVideo(videoID)
	if err != nil || video == nil || video.FilePath == "" {
		http.NotFound(w, r)
		return
	}

	absFilePath := resolveDataPath(s.cfg.DataDir, video.FilePath)
	videoBase := strings.TrimSuffix(absFilePath, filepath.Ext(absFilePath))

	// Try specific track if requested
	track := r.URL.Query().Get("track")
	if track != "" {
		trackPath, ok := subtitleTrackPath(filepath.Dir(absFilePath), filepath.Base(videoBase), track)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(trackPath); err == nil {
			serveVTT(w, trackPath)
			return
		}
		http.NotFound(w, r)
		return
	}

	// Default: try .en.vtt, then .vtt
	for _, suffix := range []string{".en.vtt", ".vtt"} {
		path := videoBase + suffix
		if _, err := os.Stat(path); err == nil {
			serveVTT(w, path)
			return
		}
	}

	// Try any .vtt in the directory matching video stem
	dir := filepath.Dir(absFilePath)
	stem := filepath.Base(videoBase)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stem) && strings.HasSuffix(e.Name(), ".vtt") {
			serveVTT(w, filepath.Join(dir, e.Name()))
			return
		}
	}

	http.NotFound(w, r)
}

func subtitleTrackPath(dir, stem, track string) (string, bool) {
	name := strings.TrimSpace(track)
	if name == "" || filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return "", false
	}
	if !strings.HasPrefix(name, stem) || !strings.HasSuffix(strings.ToLower(name), ".vtt") {
		return "", false
	}
	path := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return path, true
}

// Matches WebVTT cue timing lines and strips positioning directives
// (align:, position:, line:, size:, vertical:) that YouTube auto-captions
// emit (e.g. `align:start position:0%`). Stripping them lets auto subs
// use the same default centered placement as manual ones, so shared CSS
// (including hover-lift above the seekbar) applies uniformly.
var vttCueSettingRe = regexp.MustCompile(`\s+(?:align|position|line|size|vertical):\S+`)

func serveVTT(w http.ResponseWriter, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/vtt")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(sanitizeVTT(data))
}

func sanitizeVTT(data []byte) []byte {
	s := string(data)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.Contains(line, "-->") {
			lines[i] = vttCueSettingRe.ReplaceAllString(line, "")
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
