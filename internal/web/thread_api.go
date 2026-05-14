package web

import (
	"net/http"

	"github.com/screwys/igloo/internal/feed"
)

func (s *Server) registerThreadAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/thread/{tweet_id}", s.handleGetThread)
}

// handleGetThread returns the root tweet and the selected reply branch for a tweet.
//
// GET /api/thread/{tweet_id}
//
// Includes is_ghost rows so the UI can render parent context for tweets the
// user doesn't follow. Sibling reply branches under the same root stay
// separate. Bounded at 50 levels by GetThreadTree's CTE depth cap.
//
// Response shape:
//
//	{ "success": true,
//	  "thread": [ <feed item>, ... ],   // root-first branch pre-order
//	  "root_id": "<root_tweet_id>",
//	  "leaf_id": "<requested_tweet_id>" }
func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	tweetID := r.PathValue("tweet_id")
	if tweetID == "" {
		writeJSONError(w, 400, "missing_tweet_id", "tweet_id required")
		return
	}

	items, err := s.db.GetThreadTree(tweetID)
	if err != nil {
		writeJSONError(w, 500, "thread_query_failed", err.Error())
		return
	}
	if len(items) == 0 {
		writeJSON(w, 404, map[string]any{"success": false, "error": "tweet not found"})
		return
	}

	username := ""
	if user := userFromContext(r.Context()); user != nil {
		username = user.Username
	}
	// Use the no-collapse variant so every row in the reply tree is returned.
	items = feed.EnrichFeedItemsPreserveRows(s.db, items, username)

	var allIDs []string
	for _, item := range items {
		allIDs = append(allIDs, item.TweetID)
	}
	bookmarkInfo, _ := s.db.GetBookmarksForVideoIDsRich(allIDs)

	subscribeURLs := make(map[string]string, len(items))
	for _, item := range items {
		if item.ChannelID == "" {
			continue
		}
		if _, ok := subscribeURLs[item.ChannelID]; !ok {
			subscribeURLs[item.ChannelID] = s.db.ResolveSubscribeURL(item.ChannelID)
		}
	}

	jsonItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m := feedItemToJSON(item, bookmarkInfo, subscribeURLs)
		m["is_reply"] = item.IsReply
		m["is_ghost"] = item.IsGhost
		m["thread_depth"] = item.ThreadDepth
		jsonItems = append(jsonItems, m)
	}

	writeJSON(w, 200, map[string]any{
		"success": true,
		"thread":  jsonItems,
		"root_id": items[0].TweetID,
		"leaf_id": tweetID,
	})
}
