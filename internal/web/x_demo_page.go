package web

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/feed"
)

const xDemoSourceID = "x_demo_scweet"

func (s *Server) handlePageXDemo(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}

	items, err := s.db.ListFeedItemsBySourceID(xDemoSourceID, 1000)
	if err != nil {
		slog.Error("ListFeedItemsBySourceID", "source", xDemoSourceID, "err", err)
		http.Error(w, "Failed to load X demo feed", http.StatusInternalServerError)
		return
	}
	items = feed.EnrichFeedItems(s.db, items, username)
	items = feed.SortFeedItemsChronological(items)

	p := s.pageProps(w, r)
	p.PageTitle = "X ingest demo"
	p.PageBadge = fmt.Sprintf("%d posts", len(items))
	p.ESBundle = "js/dist/feed.js"
	p.Sidebar = s.mustBuildSidebar(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.FeedPage(p, items, false, "", false, false, nil, "").Render(r.Context(), w)
}
