package web

import "net/http"

// #13 — trivial reachability probe. No auth, no DB, no shared state.
// Android's Reachability state machine distinguishes "Wi-Fi connected" from
// "Igloo server reachable" by calling this.
func (s *Server) registerHealthAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.handleHealth)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{})
}
