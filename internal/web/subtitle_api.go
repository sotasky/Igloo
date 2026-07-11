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
	owner, ok := s.videoAssetOwner(videoID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	assets := s.canonicalAssets(owner, "subtitle")
	track := strings.TrimSpace(r.URL.Query().Get("track"))
	for _, file := range assets {
		if track != "" && track != filepath.Base(file.asset.FilePath) {
			continue
		}
		serveVTT(w, file.path)
		return
	}
	http.NotFound(w, r)
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
