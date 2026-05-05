package web

import (
	"net/http"

	"github.com/screwys/igloo/internal/feed"
	"github.com/screwys/igloo/internal/model"
)

func (s *Server) registerThreadAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/thread/{tweet_id}", s.handleGetThread)
}

// handleGetThread returns the conversation chain for a tweet, ordered root → leaf.
//
// GET /api/thread/{tweet_id}
//
// Includes is_ghost rows so the UI can render parent context for tweets the
// user doesn't follow. Bounded at 50 ancestors by GetThreadChain's CTE depth cap.
//
// Response shape:
//   { "success": true,
//     "thread": [ <feed item>, ... ],   // root-first
//     "leaf_id": "<tweet_id>" }
func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	tweetID := r.PathValue("tweet_id")
	if tweetID == "" {
		writeJSONError(w, 400, "missing_tweet_id", "tweet_id required")
		return
	}

	chain, err := s.db.GetThreadChain(tweetID)
	if err != nil {
		writeJSONError(w, 500, "thread_query_failed", err.Error())
		return
	}
	if len(chain) == 0 {
		writeJSON(w, 404, map[string]any{"success": false, "error": "tweet not found"})
		return
	}

	username := ""
	if user := userFromContext(r.Context()); user != nil {
		username = user.Username
	}
	// Use the no-collapse variant so every row in the chain is returned. The
	// regular EnrichFeedItems now drops ancestors that appear as another reply's
	// chain, which is the right behavior on the feed list but wrong here —
	// callers explicitly asked for the entire thread.
	chain = feed.EnrichFeedItemsPreserveRows(s.db, chain, username)

	var allIDs []string
	for _, item := range chain {
		allIDs = append(allIDs, item.TweetID)
	}
	bookmarkInfo, _ := s.db.GetBookmarksForVideoIDsRich(allIDs)

	subscribeURLs := make(map[string]string, len(chain))
	for _, item := range chain {
		if item.ChannelID == "" {
			continue
		}
		if _, ok := subscribeURLs[item.ChannelID]; !ok {
			subscribeURLs[item.ChannelID] = s.db.ResolveSubscribeURL(item.ChannelID)
		}
	}

	jsonItems := make([]map[string]any, 0, len(chain))
	for _, item := range chain {
		m := feedItemToJSON(item, bookmarkInfo, subscribeURLs)
		m["is_reply"] = item.IsReply
		m["is_ghost"] = item.IsGhost
		jsonItems = append(jsonItems, m)
	}

	leafID := chain[len(chain)-1].TweetID
	if _, ok := lookupItem(chain, tweetID); ok {
		// Caller asked for an item in the chain — leaf is the requested ID
		// only when it's actually the bottom of the chain. Otherwise, leaf is
		// the bottom; the caller can compare to position the cursor.
		leafID = chain[len(chain)-1].TweetID
	}

	writeJSON(w, 200, map[string]any{
		"success": true,
		"thread":  jsonItems,
		"leaf_id": leafID,
	})
}

func lookupItem(items []model.FeedItem, id string) (model.FeedItem, bool) {
	for _, it := range items {
		if it.TweetID == id {
			return it, true
		}
	}
	return model.FeedItem{}, false
}
