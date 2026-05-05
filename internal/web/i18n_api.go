package web

import (
	"net/http"
)

func (s *Server) registerI18NAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/i18n/catalog", s.handleI18NCatalog)
}

func (s *Server) handleI18NCatalog(w http.ResponseWriter, r *http.Request) {
	lang := s.requestLanguage(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"language":            lang,
		"default_language":    s.catalog().DefaultLanguage(),
		"supported_languages": s.catalog().Languages(),
		"messages":            s.catalog().Messages(lang),
	})
}
