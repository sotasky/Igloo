package web

import (
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) serveDataFileViaXAccel(w http.ResponseWriter, r *http.Request, path, contentType, cacheControl string) bool {
	if !requestFromReverseProxy(r) {
		return false
	}
	redirect, ok := s.dataFileXAccelRedirect(path)
	if !ok {
		return false
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	w.Header().Set("X-Accel-Redirect", redirect)
	w.WriteHeader(http.StatusOK)
	return true
}

func requestFromReverseProxy(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-Proto") != "" || r.Header.Get("X-Real-IP") != ""
}

func (s *Server) dataFileXAccelRedirect(path string) (string, bool) {
	key, err := s.cfg.Storage.Key(path)
	if err != nil {
		return "", false
	}
	prefix := "/x-accel/igloo-state/"
	if strings.HasPrefix(key, "media/") {
		prefix = "/x-accel/igloo-media/"
		key = strings.TrimPrefix(key, "media/")
	}
	return prefix + escapeXAccelPath(key), true
}

func escapeXAccelPath(rel string) string {
	parts := strings.Split(rel, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
