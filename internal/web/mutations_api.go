package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/db"
)

// #11 — mutation endpoints. One handler per OutboxKind; all share the
// same dispatcher shape (parse body → apply → respond with envelope
// carrying sync_version + sync_stream per #10).

func (s *Server) registerMutationAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mutations/delta", s.handleMutationDelta)
	mux.HandleFunc("POST /api/mutations/like", s.handleMutationLike)
	mux.HandleFunc("POST /api/mutations/bookmark", s.handleMutationBookmark)
	mux.HandleFunc("POST /api/mutations/follow", s.handleMutationFollow)
	mux.HandleFunc("POST /api/mutations/star", s.handleMutationStar)
	mux.HandleFunc("POST /api/mutations/mute", s.handleMutationMute)
	mux.HandleFunc("POST /api/mutations/seen", s.handleMutationSeen)
	mux.HandleFunc("POST /api/mutations/moment_view", s.handleMutationMomentView)
	mux.HandleFunc("POST /api/mutations/create_category", s.handleMutationCreateCategory)
	mux.HandleFunc("PUT /api/mutations/channel_setting", s.handleMutationChannelSetting)
	mux.HandleFunc("PUT /api/mutations/progress", s.handleMutationProgress)
	mux.HandleFunc("PUT /api/mutations/moments_cursor", s.handleMutationMomentsCursor)
	mux.HandleFunc("PUT /api/mutations/bookmark_alias", s.handleMutationBookmarkAlias)
}

func feedHandleFromChannelID(channelID string) string {
	handle := strings.TrimSpace(channelID)
	if idx := strings.Index(handle, "_"); idx >= 0 {
		handle = handle[idx+1:]
	}
	return handle
}

func (s *Server) kickFeedOrderForTweetIDs(tweetIDs ...string) {
	if len(tweetIDs) == 0 {
		return
	}
	_ = s.db.InvalidateAlgoScore(tweetIDs...)
	s.workers.KickFeedScoring()
}

func (s *Server) kickFeedOrderForHandle(handle string) {
	if strings.TrimSpace(handle) == "" {
		return
	}
	_ = s.db.InvalidateAlgoScoreByHandle(handle)
	s.workers.KickFeedScoring()
}

func (s *Server) kickFeedOrderForChannelID(channelID string) {
	if strings.TrimSpace(channelID) == "" {
		return
	}
	_ = s.db.InvalidateAlgoScoreByHandle(feedHandleFromChannelID(channelID))
	s.workers.KickFeedScoring()
}

func (s *Server) handleMutationDelta(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}

	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	changes, truncated, err := s.db.GetMutationSyncChanges(user.Username, since, limit)
	if err != nil {
		slog.Error("GetMutationSyncChanges", "since", since, "err", err)
		writeJSONError(w, 500, "db_error", "mutation delta read failed")
		return
	}
	currentVersion, _ := s.db.GetCurrentSyncVersion()
	nextCursor := strconv.FormatInt(currentVersion, 10)

	result := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		result = append(result, map[string]any{
			"version":    c.Version,
			"type":       c.Type,
			"item_id":    c.ItemID,
			"value":      c.Value,
			"created_at": c.CreatedAtMs,
		})
	}
	if truncated && len(changes) > 0 {
		nextCursor = strconv.FormatInt(changes[len(changes)-1].Version, 10)
	}

	writeJSON(w, 200, map[string]any{
		"version":     currentVersion,
		"next_cursor": nextCursor,
		"changes":     result,
		"truncated":   truncated,
	})
}

// sync_stream value per kind (track-a intent-pass answer).
func streamForKind(kind string) string {
	switch kind {
	case "like", "seen", "mute":
		return "feed"
	case "bookmark":
		return "feed" // tweet bookmarks land here; video bookmarks bump the video stream too
	case "follow", "star", "channel_setting":
		return "channels"
	case "moment_view":
		return "shorts"
	case "progress":
		return "videos"
	}
	// "moments_cursor", "create_category", "bookmark_alias" omit sync_stream —
	// client doesn't maintain a dedicated cursor for those; the next inbound
	// sync picks up their changes naturally.
	return ""
}

// writeMutation returns the standard mutation envelope.
func writeMutation(w http.ResponseWriter, status int, kind string, version int64, extra map[string]any) {
	body := map[string]any{}
	if version > 0 {
		body["sync_version"] = version
		if stream := streamForKind(kind); stream != "" {
			body["sync_stream"] = stream
		}
	}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// ── handlers ─────────────────────────────────────────────────────────

func (s *Server) handleMutationLike(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		TweetID     string `json:"tweet_id"`
		Action      string `json:"action"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	var tweetID string
	var err error
	if body.Action == "set" {
		tweetID, err = s.db.ResolveFeedStateIDForWrite(body.TweetID)
	} else {
		tweetID, err = s.db.ResolveFeedStateID(body.TweetID)
	}
	if err != nil {
		slog.Error("ResolveFeedStateID", "tweet", body.TweetID, "err", err)
		writeJSONError(w, 500, "db_error", "database error")
		return
	}
	body.TweetID = tweetID
	res, err := s.db.ApplyLikeMutation(user.Username, body.TweetID, body.Action, body.UpdatedAtMs)
	if err != nil {
		slog.Error("ApplyLikeMutation", "err", err)
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	if body.Action == "set" {
		s.requestXStatusRecovery(body.TweetID, false)
	}
	s.kickFeedOrderForTweetIDs(body.TweetID)
	writeMutation(w, 200, "like", res.SyncVersion, nil)
}

func (s *Server) handleMutationBookmark(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		VideoID        string `json:"video_id"`
		Action         string `json:"action"`
		CategoryID     *int64 `json:"category_id,omitempty"`
		CustomTitle    string `json:"custom_title,omitempty"`
		AccountHandles string `json:"account_handles,omitempty"`
		MediaIndices   string `json:"media_indices,omitempty"`
		UpdatedAtMs    int64  `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	var videoID string
	var err error
	if body.Action == "set" {
		videoID, err = s.db.ResolveFeedStateIDForWrite(body.VideoID)
	} else {
		videoID, err = s.db.ResolveFeedStateID(body.VideoID)
	}
	if err != nil {
		slog.Error("ResolveFeedStateID", "video", body.VideoID, "err", err)
		writeJSONError(w, 500, "db_error", "database error")
		return
	}
	body.VideoID = videoID
	var archivePath string
	if body.Action == "set" {
		requestedCategoryID := int64(0)
		if body.CategoryID != nil {
			requestedCategoryID = *body.CategoryID
		}
		category, ok, err := s.resolveOwnedBookmarkCategory(user.Username, requestedCategoryID)
		if err != nil {
			slog.Error("GetBookmarkCategories", "err", err)
			writeJSONError(w, 500, "db_error", "database error")
			return
		}
		if !ok {
			writeJSONError(w, 404, "not_found", "bookmark category not found")
			return
		}
		categoryID := category.ID
		body.CategoryID = &categoryID
		if bookmarkArchivePathsAllowed(user) {
			archivePath = category.ArchivePath
		}
	}
	alreadyCurrent := false
	if body.Action == "set" && body.CategoryID != nil {
		current, err := s.bookmarkPayloadIsCurrent(
			user.Username,
			body.VideoID,
			*body.CategoryID,
			body.CustomTitle,
			body.AccountHandles,
			body.MediaIndices,
		)
		if err != nil {
			slog.Warn("bookmark mutation current-state check failed", "video", body.VideoID, "err", err)
		}
		alreadyCurrent = current
	}
	res, err := s.db.ApplyBookmarkMutation(user.Username, db.BookmarkMutation{
		VideoID:        body.VideoID,
		Action:         body.Action,
		CategoryID:     body.CategoryID,
		CustomTitle:    body.CustomTitle,
		AccountHandles: body.AccountHandles,
		MediaIndices:   body.MediaIndices,
		UpdatedAtMs:    body.UpdatedAtMs,
	})
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	if body.Action == "set" {
		s.requestXStatusRecovery(body.VideoID, true)
	}
	s.kickFeedOrderForTweetIDs(body.VideoID)
	if body.Action == "set" && body.CategoryID != nil && !alreadyCurrent {
		s.startBookmarkArchive(
			body.VideoID,
			archivePath,
			body.CustomTitle,
			body.AccountHandles,
			parseBookmarkMediaIndices(body.MediaIndices),
		)
	}
	writeMutation(w, 200, "bookmark", res.SyncVersion, nil)
}

func (s *Server) handleMutationFollow(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	raw, err := readMutationBody(r)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	channelID, _ := raw["channel_id"].(string)
	action, _ := raw["action"].(string)
	ts := asInt64(raw["updated_at_ms"])
	if channelID == "" {
		writeJSONError(w, 400, "invalid_body", "channel_id required")
		return
	}
	res, err := s.db.ApplyFollowMutation(channelID, action, ts)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	if action == "clear" {
		deletedVideos, err := s.db.PurgeUnfollowedChannelContent(channelID, user.Username)
		if err != nil {
			slog.Error("PurgeUnfollowedChannelContent", "channel", channelID, "err", err)
			writeJSONError(w, 500, "db_error", "channel purge failed")
			return
		}
		for _, v := range deletedVideos {
			s.deleteVideoFiles(v)
		}
	}
	s.kickFeedOrderForChannelID(channelID)
	writeMutation(w, 200, "follow", res.SyncVersion, nil)
}

func (s *Server) handleMutationStar(w http.ResponseWriter, r *http.Request) {
	s.applyToggleMutation(w, r, "star", func(_, id, action string, ts int64) (int64, error) {
		res, err := s.db.ApplyStarMutation(id, action, ts)
		return res.SyncVersion, err
	}, "channel_id", s.kickFeedOrderForChannelID)
}

func (s *Server) handleMutationMute(w http.ResponseWriter, r *http.Request) {
	s.applyToggleMutation(w, r, "mute", func(_, id, action string, ts int64) (int64, error) {
		res, err := s.db.ApplyMuteMutation(id, action, ts)
		return res.SyncVersion, err
	}, "handle", s.kickFeedOrderForHandle)
}

// applyToggleMutation shares the body-shape for the three simple
// toggle kinds: {<id_field>, action, updated_at_ms}.
func (s *Server) applyToggleMutation(
	w http.ResponseWriter,
	r *http.Request,
	kind string,
	apply func(user, id, action string, ts int64) (int64, error),
	idField string,
	after func(id string),
) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	raw, err := readMutationBody(r)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	id, _ := raw[idField].(string)
	action, _ := raw["action"].(string)
	ts := asInt64(raw["updated_at_ms"])
	if id == "" {
		writeJSONError(w, 400, "invalid_body", idField+" required")
		return
	}
	version, err := apply(user.Username, id, action, ts)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	if after != nil {
		after(id)
	}
	writeMutation(w, 200, kind, version, nil)
}

func (s *Server) handleMutationSeen(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		TweetIDs    []string `json:"tweet_ids"`
		UpdatedAtMs int64    `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	if len(body.TweetIDs) > 500 {
		body.TweetIDs = body.TweetIDs[:500]
	}
	res, err := s.db.ApplySeenMutation(user.Username, body.TweetIDs, body.UpdatedAtMs)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	writeMutation(w, 200, "seen", res.SyncVersion, map[string]any{"marked": len(body.TweetIDs)})
}

func (s *Server) handleMutationMomentView(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		VideoID     string `json:"video_id"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	res, err := s.db.ApplyMomentViewMutation(user.Username, body.VideoID, body.UpdatedAtMs)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	writeMutation(w, 200, "moment_view", res.SyncVersion, nil)
}

func (s *Server) handleMutationCreateCategory(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		Name          string `json:"name"`
		ProvisionalID string `json:"provisional_id"`
		UpdatedAtMs   int64  `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	cat, err := s.db.ApplyCreateCategoryMutation(user.Username, body.Name, body.ProvisionalID, body.UpdatedAtMs)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	writeMutation(w, 200, "create_category", cat.SyncVersion, map[string]any{
		"category_id":    cat.CategoryID,
		"provisional_id": cat.ProvisionalID,
	})
}

func (s *Server) handleMutationChannelSetting(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		ChannelID   string `json:"channel_id"`
		Field       string `json:"field"`
		Value       any    `json:"value"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	res, err := s.db.ApplyChannelSettingMutation(body.ChannelID, body.Field, body.Value, body.UpdatedAtMs)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	s.kickFeedOrderForChannelID(body.ChannelID)
	writeMutation(w, 200, "channel_setting", res.SyncVersion, nil)
}

func (s *Server) handleMutationProgress(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		VideoID     string  `json:"video_id"`
		Position    float64 `json:"position"`
		Duration    float64 `json:"duration"`
		Source      string  `json:"source"`
		UpdatedAtMs int64   `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	res, err := s.db.ApplyProgressMutation(user.Username, body.VideoID, body.Position, body.Duration, body.Source, body.UpdatedAtMs)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	writeMutation(w, 200, "progress", res.SyncVersion, nil)
}

func (s *Server) handleMutationMomentsCursor(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		VideoID     string `json:"video_id"`
		PositionMs  int64  `json:"position_ms"`
		UpdatedAtMs int64  `json:"updated_at_ms"`
		Scope       string `json:"scope"`
		SortAtMs    int64  `json:"sort_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	res, err := s.db.ApplyMomentsCursorMutationWithSortAt(user.Username, body.VideoID, body.PositionMs, body.UpdatedAtMs, body.Scope, body.SortAtMs)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	writeMutation(w, 200, "moments_cursor", res.SyncVersion, nil)
}

func (s *Server) handleMutationBookmarkAlias(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		OriginalHandle string `json:"original_handle"`
		DisplayAlias   string `json:"display_alias"`
		UpdatedAtMs    int64  `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	res, err := s.db.ApplyBookmarkAliasMutation(body.OriginalHandle, body.DisplayAlias, body.UpdatedAtMs)
	if err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	writeMutation(w, 200, "bookmark_alias", res.SyncVersion, nil)
}

// ── body helpers ─────────────────────────────────────────────────────

const mutationMaxBody = 64 * 1024

func decodeMutation(r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, mutationMaxBody)
	return json.NewDecoder(r.Body).Decode(into)
}

func readMutationBody(r *http.Request) (map[string]any, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, mutationMaxBody)
	var raw map[string]any
	err := json.NewDecoder(r.Body).Decode(&raw)
	return raw, err
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}
