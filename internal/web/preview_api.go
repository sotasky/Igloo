package web

import (
	"net/http"
)

func (s *Server) registerPreviewAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/media/preview-sprite/{videoID}", s.handlePreviewSprite)
	mux.HandleFunc("GET /api/media/preview-track-json/{videoID}", s.handlePreviewTrackJSON)
	mux.HandleFunc("GET /api/previews/status", s.handlePreviewStatus)
}

func (s *Server) handlePreviewSprite(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	owner, ok := s.videoAssetOwner(videoID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file := s.canonicalAsset(owner, "preview_sprite", 0)
	if file == nil {
		http.NotFound(w, r)
		return
	}
	cacheControl := "public, max-age=86400"
	contentType := file.asset.ContentType
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", contentType)
	if s.serveDataFileViaXAccel(w, r, file.path, contentType, cacheControl) {
		return
	}
	http.ServeFile(w, r, file.path)
}

func (s *Server) handlePreviewTrackJSON(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	owner, ok := s.videoAssetOwner(videoID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file := s.canonicalAsset(owner, "preview_track_json", 0)
	if file == nil {
		http.NotFound(w, r)
		return
	}
	contentType := file.asset.ContentType
	cacheControl := "public, max-age=86400"
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", cacheControl)
	if s.serveDataFileViaXAccel(w, r, file.path, contentType, cacheControl) {
		return
	}
	http.ServeFile(w, r, file.path)
}

func (s *Server) handlePreviewStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"queue_length": len(s.workers.PreviewChan()),
	})
}
