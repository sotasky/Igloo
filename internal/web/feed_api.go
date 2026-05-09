package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
	"github.com/screwys/igloo/internal/model"
)

// registerFeedAPIRoutes registers feed interaction API routes.
func (s *Server) registerFeedAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/feed/like/{tweetID}", s.handleFeedLike)
	mux.HandleFunc("DELETE /api/feed/like/{tweetID}", s.handleFeedUnlike)
	mux.HandleFunc("GET /api/feed/debug/item/{tweetID}", s.handleFeedDebugItem)
	mux.HandleFunc("POST /api/feed/seen", s.handleFeedSeen)
	mux.HandleFunc("POST /api/feed/mute/{handle}", s.handleFeedMute)
	mux.HandleFunc("DELETE /api/feed/mute/{handle}", s.handleFeedUnmute)
	mux.HandleFunc("GET /api/feed/muted", s.handleFeedMutedList)
	mux.HandleFunc("POST /api/feed/ingest/pause", s.handleFeedIngestPause)
	mux.HandleFunc("POST /api/feed/ingest/trigger", s.handleFeedIngestTrigger)
	mux.HandleFunc("POST /api/feed/media/retry", s.handleFeedMediaRetry)
	mux.HandleFunc("POST /api/feed/interaction", s.handleFeedInteraction)
	mux.HandleFunc("GET /api/feed/rsshub", s.handleFeedRSSHub)
	mux.HandleFunc("GET /api/feed/liked", s.handleFeedLikedList)
	mux.HandleFunc("GET /api/feed/bookmarked", s.handleFeedBookmarkedList)
	mux.HandleFunc("GET /api/feed/shorts", s.handleFeedShorts)
}

func (s *Server) handleFeedLike(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	tweetID := r.PathValue("tweetID")

	var body struct {
		Item map[string]string `json:"item"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	fields := body.Item
	if fields == nil {
		fields = make(map[string]string)
	}

	err := s.db.InsertFeedLike(user.Username, tweetID, fields)
	if err != nil {
		slog.Error("InsertFeedLike", "tweet", tweetID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	_ = s.db.InvalidateAlgoScore(tweetID)
	s.workers.KickFeedScoring()

	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.LikeButton(s.pageProps(w, r), tweetID, true).Render(r.Context(), w)
		return
	}

	syncVersion, _ := s.db.GetCurrentSyncVersion()
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"is_liked":     true,
		"sync_version": syncVersion,
	})
}

func (s *Server) handleFeedUnlike(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	tweetID := r.PathValue("tweetID")

	err := s.db.DeleteFeedLike(user.Username, tweetID)
	if err != nil {
		slog.Error("DeleteFeedLike", "tweet", tweetID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	_ = s.db.InvalidateAlgoScore(tweetID)
	s.workers.KickFeedScoring()

	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.LikeButton(s.pageProps(w, r), tweetID, false).Render(r.Context(), w)
		return
	}

	syncVersion, _ := s.db.GetCurrentSyncVersion()
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"is_liked":     false,
		"removed":      true,
		"sync_version": syncVersion,
	})
}

func (s *Server) handleFeedSeen(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}

	// HTMX per-card POST sends tweet_id via query/form; Android and legacy clients
	// batch multiple IDs via JSON body. Try the single-id path first.
	var tweetIDs []string
	if single := strings.TrimSpace(r.URL.Query().Get("tweet_id")); single != "" {
		tweetIDs = []string{single}
	} else {
		var body struct {
			TweetIDs []string `json:"tweet_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.TweetIDs) == 0 {
			writeJSON(w, 400, map[string]any{"success": false, "error": "tweet_ids required"})
			return
		}
		tweetIDs = body.TweetIDs
	}
	if len(tweetIDs) > 500 {
		tweetIDs = tweetIDs[:500]
	}

	count, err := s.db.MarkSeen(user.Username, tweetIDs)
	if err != nil {
		slog.Error("MarkSeen", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	// Seen state is filtered at read time. Rebuilding the rank snapshot for
	// every visible card churns snapshot cursors while the user is scrolling.
	syncVersion, _ := s.db.GetCurrentSyncVersion()
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"marked":       count,
		"sync_version": syncVersion,
	})
}

func (s *Server) handleFeedMute(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	handle := r.PathValue("handle")
	if handle == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "handle required"})
		return
	}

	err := s.db.MuteAccount(handle)
	if err != nil {
		slog.Error("MuteAccount", "handle", handle, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	_ = s.db.InvalidateAlgoScoreByHandle(handle)
	s.workers.KickFeedScoring()

	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"accountMuted":{"handle":%q}}`, handle))
		return
	}

	syncVersion, _ := s.db.GetCurrentSyncVersion()
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"muted":        true,
		"handle":       handle,
		"sync_version": syncVersion,
	})
}

func (s *Server) handleFeedUnmute(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	handle := r.PathValue("handle")

	err := s.db.UnmuteAccount(handle)
	if err != nil {
		slog.Error("UnmuteAccount", "handle", handle, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	_ = s.db.InvalidateAlgoScoreByHandle(handle)
	s.workers.KickFeedScoring()

	if r.Header.Get("HX-Request") != "" {
		s.renderMutedAccountsHTML(w, r)
		return
	}

	syncVersion, _ := s.db.GetCurrentSyncVersion()
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"muted":        false,
		"handle":       handle,
		"sync_version": syncVersion,
	})
}

func (s *Server) handleFeedMutedList(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "" {
		s.renderMutedAccountsHTML(w, r)
		return
	}

	muted, err := s.db.GetMutedAccounts()
	if err != nil {
		slog.Error("GetMutedAccounts", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if muted == nil {
		muted = []string{}
	}
	writeJSON(w, 200, map[string]any{"muted": muted})
}

func (s *Server) renderMutedAccountsHTML(w http.ResponseWriter, r *http.Request) {
	muted, err := s.db.GetMutedAccounts()
	if err != nil {
		slog.Error("GetMutedAccounts", "err", err)
		http.Error(w, "Failed to load muted accounts", 500)
		return
	}
	if muted == nil {
		muted = []string{}
	}
	components.MutedAccountsList(s.pageProps(w, r), muted).Render(r.Context(), w)
}

func (s *Server) handleFeedIngestPause(w http.ResponseWriter, r *http.Request) {
	paused := s.workers.IsIngestPaused()
	s.workers.SetIngestPaused(!paused)
	writeJSON(w, 200, map[string]any{"success": true, "paused": !paused})
}

func (s *Server) handleFeedIngestTrigger(w http.ResponseWriter, r *http.Request) {
	s.workers.KickIngest()
	writeJSON(w, 200, map[string]any{"success": true, "triggered": true})
}

func (s *Server) handleFeedMediaRetry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TweetID string `json:"tweet_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TweetID == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "tweet_id required"})
		return
	}
	if err := s.db.UpdateFeedMediaJobStatus(body.TweetID, "queued", "", 0); err != nil {
		slog.Error("UpdateFeedMediaJobStatus", "tweet", body.TweetID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	s.workers.KickFeedMedia()
	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleFeedInteraction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string         `json:"action"`
		TweetID string         `json:"tweet_id"`
		Item    map[string]any `json:"item"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}

	user := userFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}

	switch body.Action {
	case "share":
		writeJSON(w, 200, map[string]any{"success": true, "action": "share"})
	case "mute":
		handle, _ := body.Item["source_handle"].(string)
		if handle != "" {
			if err := s.db.MuteAccount(handle); err != nil {
				slog.Error("MuteAccount", "handle", handle, "err", err)
			}
		}
		writeJSON(w, 200, map[string]any{"success": true, "action": "mute"})
	case "like":
		if body.TweetID != "" {
			fields := make(map[string]string)
			for k, v := range body.Item {
				if s, ok := v.(string); ok {
					fields[k] = s
				}
			}
			if err := s.db.InsertFeedLike(username, body.TweetID, fields); err != nil {
				slog.Error("InsertFeedLike", "tweet", body.TweetID, "err", err)
			}
		}
		writeJSON(w, 200, map[string]any{"success": true, "action": "like"})
	default:
		writeJSON(w, 400, map[string]any{"success": false, "error": "unknown action"})
	}
}

// feedItemToJSON converts a FeedItem to a JSON-serializable map matching Android's FeedItemDto.
func feedItemToJSON(item model.FeedItem, bookmarkInfo map[string]db.BookmarkInfo, subscribeURLs map[string]string) map[string]any {
	canonicalURL := canonicalFeedItemURL(item)
	m := map[string]any{
		"tweet_id":                  item.TweetID,
		"title":                     item.BodyText,
		"link":                      canonicalURL,
		"canonical_x_link":          canonicalURL,
		"canonical_url":             canonicalURL,
		"source_handle":             item.SourceHandle,
		"author_handle":             item.AuthorHandle,
		"author_display_name":       item.AuthorDisplayName,
		"author_avatar_url":         item.AuthorAvatarURL,
		"avatar_url":                item.AuthorAvatarURL,
		"body_text":                 item.BodyText,
		"lang":                      item.Lang,
		"is_retweet":                item.IsRetweet,
		"retweeted_by_handle":       item.RetweetedByHandle,
		"retweeted_by_display_name": item.RetweetedByDisplayName,
		"reposted_by":               item.RetweetedByHandle,
		"reposted_by_display_name":  item.RetweetedByDisplayName,
		"reposter_channel_id":       item.ReposterChannelID,
		"reply_to_handle":           item.ReplyToHandle,
		"reply_to_status":           item.ReplyToStatus,
		"is_reply":                  item.IsReply,
		"is_ghost":                  item.IsGhost,
		"is_liked":                  item.IsLiked,
		"is_bookmarked":             item.IsBookmarked,
		"is_seen":                   item.IsSeen,
		"algo_interest_score":       item.AlgoInterestScore,
		"channel_id":                item.ChannelID,
		"channel_is_followed":       item.ChannelIsFollowed,
		"channel_is_starred":        item.ChannelIsStarred,
		"subscribe_url":             subscribeURLs[item.ChannelID],
		"platform":                  "x",
		"media_status":              item.MediaStatus,
		"media_kind":                item.MediaKind,
		"media_slide_count":         item.MediaSlideCount,
		"media_stream_url":          item.MediaStreamURL,
		"media_preview_url":         item.MediaPreviewURL,
		"has_media":                 len(item.Media) > 0,
	}

	if item.PublishedAt != nil {
		m["published_at"] = item.PublishedAt.UnixMilli()
	}

	// Bookmark enrichment
	if bi, ok := bookmarkInfo[item.TweetID]; ok {
		m["is_bookmarked"] = true
		m["bookmarked_at"] = bi.BookmarkedAtMs
		if bi.CategoryID != nil {
			m["bookmark_category_id"] = *bi.CategoryID
		}
	}

	// Media slide URLs
	if len(item.MediaSlideURLs) > 0 {
		m["media_slide_urls"] = item.MediaSlideURLs
	}

	// Parent media fields (only when parent has its own media)
	if len(item.Media) > 0 {
		m["media_url"] = item.Media[0].URL
		if item.Media[0].Type == "video" || item.Media[0].Type == "gif" {
			m["media_cdn_video_url"] = item.Media[0].URL
		}
		var types []string
		for _, media := range item.Media {
			types = append(types, media.Type)
		}
		m["media_types"] = types
	}

	// Quote tweet fields (flat, not nested)
	if item.QuoteTweetID != "" {
		if quoteCanonicalURL := canonicalFeedItemQuoteURL(item); quoteCanonicalURL != "" {
			m["quote_canonical_url"] = quoteCanonicalURL
		}
		m["quote_tweet_id"] = item.QuoteTweetID
		m["quote_author_handle"] = item.QuoteAuthorHandle
		m["quote_author_display_name"] = item.QuoteAuthorDisplayName
		m["quote_author_avatar_url"] = item.QuoteAuthorAvatarURL
		m["quote_body_text"] = item.QuoteBodyText
		m["quote_channel_id"] = item.QuoteChannelID
		m["quote_channel_is_followed"] = item.QuoteChannelFollowed
		m["quote_media_stream_url"] = item.QuoteMediaStreamURL
		if item.QuotePublishedAt != nil {
			m["quote_published_at"] = item.QuotePublishedAt.UnixMilli()
		}
		m["quote_lang"] = item.QuoteLang
		// Quote media array
		if len(item.QuoteMedia) > 0 {
			var qm []map[string]any
			for _, media := range item.QuoteMedia {
				qm = append(qm, map[string]any{
					"type":      media.Type,
					"url":       media.URL,
					"thumbnail": media.ThumbnailURL,
				})
			}
			m["quote_media"] = qm
		}
	}

	// Translation fields
	if item.BodyTranslation != "" {
		m["body_translation"] = item.BodyTranslation
		m["body_source_lang"] = item.BodySourceLang
	}
	if item.QuoteTranslation != "" {
		m["quote_translation"] = item.QuoteTranslation
		m["quote_source_lang"] = item.QuoteSourceLang
	}

	// Tagged accounts
	if len(item.TaggedAccounts) > 0 {
		var ta []map[string]any
		for _, acct := range item.TaggedAccounts {
			ta = append(ta, map[string]any{
				"retweeter_handle":       acct.Handle,
				"retweeter_display_name": acct.DisplayName,
				"channel_id":             acct.ChannelID,
				"avatar_url":             acct.AvatarURL,
			})
		}
		m["tagged_accounts"] = ta
	}

	if len(item.Retweeters) > 0 {
		rt := make([]map[string]any, 0, len(item.Retweeters))
		for _, r := range item.Retweeters {
			rt = append(rt, map[string]any{
				"handle":       r.Handle,
				"display_name": r.DisplayName,
				"channel_id":   r.ChannelID,
				"avatar_url":   r.AvatarURL,
			})
		}
		m["retweeters"] = rt
	}

	return m
}

func canonicalFeedItemURL(item model.FeedItem) string {
	if raw := strings.TrimSpace(item.CanonicalURL); strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	return canonicalXStatusURL(item.AuthorHandle, item.SourceHandle, item.CanonicalTweetID, item.TweetID)
}

func canonicalFeedItemQuoteURL(item model.FeedItem) string {
	if raw := strings.TrimSpace(item.QuoteCanonicalURL); strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	return canonicalXStatusURL(item.QuoteAuthorHandle, "", item.QuoteTweetID, item.QuoteTweetID)
}

func canonicalXStatusURL(handle string, fallbackHandle string, id string, fallbackID string) string {
	h := strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if h == "" {
		h = strings.TrimPrefix(strings.TrimSpace(fallbackHandle), "@")
	}
	statusID := strings.TrimSpace(id)
	if statusID == "" {
		statusID = strings.TrimSpace(fallbackID)
	}
	if h == "" || statusID == "" {
		return ""
	}
	return "https://x.com/" + h + "/status/" + statusID
}

// writeFeedResponse builds and writes the standard Android FeedResponse JSON.
// cursorOverride, if non-empty, is used instead of computing from the last item's PublishedAt.
func (s *Server) writeFeedResponse(w http.ResponseWriter, items []model.FeedItem, hasMore bool, total int, username string, cursorOverride string) {
	// Collect bookmark info for all items
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

	var jsonItems []map[string]any
	for i, item := range items {
		m := feedItemToJSON(item, bookmarkInfo, subscribeURLs)
		m["rank_position"] = i
		jsonItems = append(jsonItems, m)
	}
	if jsonItems == nil {
		jsonItems = []map[string]any{}
	}

	var nextCursor any
	if hasMore && len(items) > 0 {
		if cursorOverride != "" {
			nextCursor = cursorOverride
		} else {
			last := items[len(items)-1]
			if last.PublishedAt != nil {
				nextCursor = fmt.Sprintf("%d|%s", last.PublishedAt.UnixMilli(), last.TweetID)
			}
		}
	}

	writeJSON(w, 200, map[string]any{
		"items":       jsonItems,
		"next_cursor": nextCursor,
		"has_more":    hasMore,
		"total":       total,
		"scored_at":   time.Now().UnixMilli(),
	})
}

func (s *Server) handleFeedRSSHub(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}

	limit := 40
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	cursorToken := r.URL.Query().Get("cursor")
	var cursor *model.FeedCursor
	if cursorToken != "" {
		c := model.ParseFeedCursor(cursorToken)
		cursor = &c
	}

	sourceHandle := r.URL.Query().Get("source_handle")

	items, err := s.db.ListFeedItemsFiltered(limit+1, cursor, sourceHandle)
	if err != nil {
		slog.Error("ListFeedItemsFiltered", "err", err)
		items = nil
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	items = feed.EnrichFeedItems(s.db, items, username)
	if feed.AlgorithmicFeedEnabled(s.db) {
		items = feed.RankFeedItems(items)
	} else {
		items = feed.SortFeedItemsChronological(items)
	}

	total, _ := s.db.CountFeedItems()

	s.writeFeedResponse(w, items, hasMore, total, username, "")
}

func (s *Server) handleFeedLikedList(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}

	limit := 40
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	cursorToken := r.URL.Query().Get("cursor")
	var cursor *model.FeedCursor
	if cursorToken != "" {
		c := model.ParseFeedCursor(cursorToken)
		cursor = &c
	}

	likes, err := s.db.GetFeedLikedPage(user.Username, limit+1, cursor)
	if err != nil {
		slog.Error("GetFeedLikedPage", "err", err)
		likes = nil
	}

	hasMore := len(likes) > limit
	if hasMore {
		likes = likes[:limit]
	}

	// Convert likes to feed items for enrichment
	var tweetIDs []string
	for _, l := range likes {
		tweetIDs = append(tweetIDs, l.TweetID)
	}
	feedItemMap, _ := s.db.GetFeedItemsForTweetIDs(tweetIDs)

	var items []model.FeedItem
	for _, l := range likes {
		if fi, ok := feedItemMap[l.TweetID]; ok {
			fi.IsLiked = true
			items = append(items, fi)
		} else {
			items = append(items, model.FeedItem{
				TweetID:           l.TweetID,
				AuthorHandle:      l.AuthorHandle,
				AuthorDisplayName: l.AuthorDisplayName,
				SourceHandle:      l.SourceHandle,
				BodyText:          l.BodyText,
				CanonicalURL:      l.Link,
				PublishedAt:       l.PublishedAt,
				MediaJSON:         l.MediaJSON,
				AuthorAvatarURL:   l.AvatarURL,
				IsLiked:           true,
			})
		}
	}

	items = feed.EnrichFeedItemsPreserveRows(s.db, items, user.Username)

	total, _ := s.db.CountFeedLikes(user.Username)

	// Cursor for liked feed uses liked_at (not published_at).
	// Format: "<unix_millis>|<tweet_id>".
	var likedCursor string
	if hasMore && len(likes) > 0 {
		last := likes[len(likes)-1]
		likedCursor = fmt.Sprintf("%d|%s", last.LikedAt.UnixMilli(), last.TweetID)
	}
	s.writeFeedResponse(w, items, hasMore, total, user.Username, likedCursor)
}

func (s *Server) handleFeedBookmarkedList(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	username := user.Username

	limit := 40
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	cursorToken := r.URL.Query().Get("cursor")
	var cursor *model.FeedCursor
	if cursorToken != "" {
		c := model.ParseFeedCursor(cursorToken)
		cursor = &c
	}

	items, err := s.db.GetBookmarkedFeedItems(limit+1, cursor)
	if err != nil {
		slog.Error("GetBookmarkedFeedItems", "err", err)
		items = nil
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	items = feed.EnrichFeedItems(s.db, items, username)

	total, _ := s.db.CountBookmarkedFeedItems()

	// Cursor for bookmarked feed uses bookmarked_at (not published_at).
	// Format: "<unix_millis>|<tweet_id>".
	var bmCursor string
	if hasMore && len(items) > 0 {
		lastID := items[len(items)-1].TweetID
		if bi, _ := s.db.GetBookmarksForVideoIDsRich([]string{lastID}); len(bi) > 0 {
			if info, ok := bi[lastID]; ok && info.BookmarkedAtMs > 0 {
				bmCursor = fmt.Sprintf("%d|%s", info.BookmarkedAtMs, lastID)
			}
		}
	}
	s.writeFeedResponse(w, items, hasMore, total, username, bmCursor)
}

func (s *Server) handleFeedShorts(w http.ResponseWriter, r *http.Request) {
	// Shorts are TikTok/Instagram feed items
	user := userFromContext(r.Context())
	_ = user

	limit := 10000
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50000 {
			limit = n
		}
	}

	opts := db.GetVideosOpts{
		Platform: "shorts",
		Limit:    limit,
		OrderAsc: true,
	}
	videos, _ := s.db.GetVideos(opts)

	var jsonItems []map[string]any
	for _, v := range videos {
		v.EnrichForCard()
		item := map[string]any{
			"title":               v.Title,
			"body_text":           v.Description,
			"channel_id":          v.ChannelID,
			"author_handle":       v.ChannelName,
			"author_display_name": v.ChannelName,
			"avatar_url":          v.AvatarURL,
			"media_kind":          v.MediaKind,
			"media_slide_count":   v.MediaSlideCount,
			"media_stream_url":    "/api/media/stream/" + v.VideoID,
			"media_preview_url":   v.ThumbnailURL,
			"platform":            v.Platform,
			"has_media":           true,
		}
		// Platform-typed ID — no generic catch-all field. When a new platform
		// joins shorts, add a case here or the Android side throws.
		switch v.Platform {
		case "tiktok":
			item["tiktok_id"] = v.VideoID
		case "instagram":
			item["instagram_id"] = v.VideoID
		case "youtube":
			item["youtube_id"] = v.VideoID
		}
		if v.PublishedAt != nil {
			item["published_at"] = v.PublishedAt.UnixMilli()
		}
		if v.MediaKind == "slideshow" {
			item["media_audio_url"] = "/api/media/audio/" + v.VideoID
		}
		jsonItems = append(jsonItems, item)
	}

	if jsonItems == nil {
		jsonItems = []map[string]any{}
	}

	writeJSON(w, 200, map[string]any{
		"items":       jsonItems,
		"next_cursor": nil,
		"has_more":    false,
		"total":       len(jsonItems),
		"scored_at":   time.Now().UnixMilli(),
	})
}
