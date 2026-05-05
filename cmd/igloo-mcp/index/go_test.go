package index

import (
	"testing"
)

func TestScanGoHandlers(t *testing.T) {
	src := `package web

func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/feed/rsshub", s.GetRsshub)
	mux.HandleFunc("POST /api/feed/like/{id}", s.LikeFeed)
}

func (s *Server) GetRsshub(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.db.QueryContext(r.Context(), ` + "`" + `SELECT id FROM feed_items WHERE platform = 'rsshub'` + "`" + `)
	s.render(w, r, "feed.html", data)
}
`
	result := ScanGoHandlers(src, "internal/web/feed_handler.go")
	if len(result.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(result.Endpoints))
	}
	if result.Endpoints[0].Method != "GET" || result.Endpoints[0].Path != "/api/feed/rsshub" {
		t.Errorf("unexpected endpoint: %+v", result.Endpoints[0])
	}
	if result.Endpoints[0].Area != "feed" {
		t.Errorf("expected area feed, got %s", result.Endpoints[0].Area)
	}
	if len(result.Templates) == 0 || result.Templates[0] != "feed.html" {
		t.Errorf("expected template feed.html, got %v", result.Templates)
	}
	if len(result.Tables) == 0 || result.Tables[0].Table != "feed_items" {
		t.Errorf("expected table feed_items, got %v", result.Tables)
	}
	if result.Tables[0].Mode != "read" {
		t.Errorf("expected read mode, got %s", result.Tables[0].Mode)
	}
}

func TestExtractGoSymbols(t *testing.T) {
	src := `package db

type FeedStore struct{}

func (f *FeedStore) GetFeedItems(ctx context.Context, limit int) ([]Item, error) {
	return nil, nil
}

func NewFeedStore() *FeedStore {
	return &FeedStore{}
}
`
	syms, refs := ExtractGoSymbols(src, "internal/db/feed.go")
	var methods, funcs, structs int
	for _, s := range syms {
		switch s.Kind {
		case "method":
			methods++
		case "function":
			funcs++
		case "class":
			structs++
		}
	}
	if methods != 1 {
		t.Errorf("expected 1 method, got %d", methods)
	}
	if funcs != 1 {
		t.Errorf("expected 1 function, got %d", funcs)
	}
	if structs != 1 {
		t.Errorf("expected 1 struct, got %d", structs)
	}
	_ = refs // refs may be empty for this sample
}

func TestScanGoPageScripts(t *testing.T) {
	src := `package web

func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /feed", s.FeedPage)
	mux.HandleFunc("GET /player/{id}", s.PlayerPage)
}

func (s *Server) FeedPage(w http.ResponseWriter, r *http.Request) {
	p.PageScripts = []string{"js/infinite_page.js"}
	s.render(w, r, "feed.html", p)
}

func (s *Server) PlayerPage(w http.ResponseWriter, r *http.Request) {
	p.PageScripts = []string{"js/videojs_compat.js", "js/player_page.js"}
	s.render(w, r, "player.html", p)
}
`
	result := ScanGoHandlers(src, "internal/web/pages.go")

	if len(result.PageScripts) != 2 {
		t.Fatalf("expected 2 PageScripts entries, got %d", len(result.PageScripts))
	}

	// First handler: FeedPage
	found := false
	for _, ps := range result.PageScripts {
		if ps.HandlerFunc == "FeedPage" {
			found = true
			if len(ps.Scripts) != 1 || ps.Scripts[0] != "js/infinite_page.js" {
				t.Errorf("FeedPage scripts wrong: %v", ps.Scripts)
			}
		}
	}
	if !found {
		t.Error("expected PageScripts entry for FeedPage")
	}
}
