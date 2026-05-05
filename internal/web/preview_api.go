package web

import (
	"net/http"
	"path/filepath"

	"github.com/screwys/igloo/internal/worker"
)

func (s *Server) registerPreviewAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/media/preview-sprite/{videoID}", s.handlePreviewSprite)
	mux.HandleFunc("GET /api/media/preview-track-json/{videoID}", s.handlePreviewTrackJSON)
	mux.HandleFunc("POST /api/previews/ensure/{videoID}", s.handlePreviewEnsure)
	mux.HandleFunc("GET /api/previews/status", s.handlePreviewStatus)
}

func (s *Server) handlePreviewSprite(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	path := filepath.Join(s.cfg.DataDir, "thumbnails", "previews", videoID, "sprite.jpg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) handlePreviewTrackJSON(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	path := filepath.Join(s.cfg.DataDir, "thumbnails", "previews", videoID, "track.json")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) handlePreviewEnsure(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	video, err := s.db.GetVideo(videoID)
	if err != nil || video == nil {
		writeJSON(w, 404, map[string]any{"error": "video not found"})
		return
	}
	absPath := video.FilePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(s.cfg.DataDir, absPath)
	}
	s.workers.EnqueuePreview(worker.PreviewRequest{
		VideoID:  videoID,
		FilePath: absPath,
		Duration: float64(video.Duration),
	})
	writeJSON(w, 200, map[string]any{"success": true, "queued": true})
}

func (s *Server) handlePreviewStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"queue_length": len(s.workers.PreviewChan()),
	})
}
