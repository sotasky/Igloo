package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/screwys/igloo/internal/db"
)

func (s *Server) registerMutationAPIRoutes(mux *http.ServeMux) {
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

// ── handlers ─────────────────────────────────────────────────────────

func writeMutationError(w http.ResponseWriter, operation string, err error) bool {
	if err == nil {
		return false
	}
	if db.IsStaleMutation(err) {
		writeJSONError(w, http.StatusConflict, "stale_mutation", err.Error())
		return true
	}
	if db.IsInvalidMutation(err) {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return true
	}
	slog.Error(operation, "err", err)
	writeJSONError(w, http.StatusInternalServerError, "db_error", "database error")
	return true
}

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
	result, err := s.db.MutateLike(db.LikeMutation{
		TweetID: body.TweetID, Action: body.Action, UpdatedAtMs: body.UpdatedAtMs,
	})
	if writeMutationError(w, "MutateLike", err) {
		return
	}
	if result.Applied {
		if body.Action == "set" {
			s.requestXStatusRecovery(result.CanonicalID, false)
		}
		s.kickFeedOrderForTweetIDs(result.CanonicalID)
	}
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) handleMutationBookmark(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		VideoID        string  `json:"video_id"`
		Action         string  `json:"action"`
		CategoryID     *int64  `json:"category_id,omitempty"`
		CustomTitle    *string `json:"custom_title,omitempty"`
		AccountHandles *string `json:"account_handles,omitempty"`
		MediaIndices   *string `json:"media_indices,omitempty"`
		UpdatedAtMs    int64   `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	if body.Action == "set" && body.CategoryID != nil {
		category, ok, err := s.resolveBookmarkCategory(*body.CategoryID)
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
	}
	result, err := s.db.MutateBookmark(db.BookmarkMutation{
		VideoID:        body.VideoID,
		Action:         body.Action,
		CategoryID:     body.CategoryID,
		CustomTitle:    body.CustomTitle,
		AccountHandles: body.AccountHandles,
		MediaIndices:   body.MediaIndices,
		UpdatedAtMs:    body.UpdatedAtMs,
	})
	if writeMutationError(w, "MutateBookmark", err) {
		return
	}
	if result.Applied {
		if body.Action == "set" {
			s.requestXStatusRecovery(result.CanonicalID, true)
			if result.Affected > 0 {
				s.startMutationBookmarkArchive(user, result.CanonicalID)
			}
		}
		s.kickFeedOrderForTweetIDs(result.CanonicalID)
	}
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) startMutationBookmarkArchive(user *userInfo, videoID string) {
	var categoryID int64
	var customTitle, accountHandles, mediaIndices string
	err := s.db.QueryRow(`
		SELECT category_id, COALESCE(custom_title, ''),
		       COALESCE(account_handles, ''), COALESCE(media_indices, '')
		FROM bookmarks WHERE video_id = ?
	`, videoID).Scan(&categoryID, &customTitle, &accountHandles, &mediaIndices)
	if err != nil {
		slog.Warn("bookmark mutation archive state read failed", "video", videoID, "err", err)
		return
	}
	category, ok, err := s.resolveBookmarkCategory(categoryID)
	if err != nil || !ok {
		return
	}
	archivePath := ""
	if bookmarkArchivePathsAllowed(user) {
		archivePath = category.ArchivePath
	}
	s.startBookmarkArchive(videoID, archivePath, customTitle, accountHandles, parseBookmarkMediaIndices(mediaIndices))
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
	result, err := s.db.MutateFollow(channelID, action, ts)
	if writeMutationError(w, "MutateFollow", err) {
		return
	}
	if result.Applied {
		if action == "clear" {
			s.removeCanonicalAssetFiles(result.DeletedFileKeys)
		}
		s.kickFeedOrderForChannelID(result.CanonicalID)
	}
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) handleMutationStar(w http.ResponseWriter, r *http.Request) {
	s.applyToggleMutation(w, r, s.db.MutateStar, "channel_id", s.kickFeedOrderForChannelID)
}

func (s *Server) handleMutationMute(w http.ResponseWriter, r *http.Request) {
	s.applyToggleMutation(w, r, s.db.MutateMute, "channel_id", s.kickFeedOrderForChannelID)
}

// applyToggleMutation shares the body-shape for the three simple
// toggle kinds: {<id_field>, action, updated_at_ms}.
func (s *Server) applyToggleMutation(
	w http.ResponseWriter,
	r *http.Request,
	apply func(id, action string, ts int64) (db.MutationResult, error),
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
	result, err := apply(id, action, ts)
	if writeMutationError(w, "toggle mutation", err) {
		return
	}
	if result.Applied && after != nil {
		after(result.CanonicalID)
	}
	writeJSON(w, 200, map[string]any{})
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
	_, err := s.db.MutateSeen(body.TweetIDs, body.UpdatedAtMs)
	if writeMutationError(w, "MutateSeen", err) {
		return
	}
	writeJSON(w, 200, map[string]any{})
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
	_, err := s.db.MutateMomentView(body.VideoID, body.UpdatedAtMs)
	if writeMutationError(w, "MutateMomentView", err) {
		return
	}
	writeJSON(w, 200, map[string]any{})
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
		RequestID     string `json:"request_id"`
		UpdatedAtMs   int64  `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	cat, err := s.db.ApplyCreateCategoryMutation(body.Name, body.ProvisionalID, body.RequestID, body.UpdatedAtMs)
	if writeMutationError(w, "ApplyCreateCategoryMutation", err) {
		return
	}
	writeJSON(w, 200, map[string]any{
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
	result, err := s.db.MutateChannelSetting(body.ChannelID, body.Field, body.Value, body.UpdatedAtMs)
	if writeMutationError(w, "MutateChannelSetting", err) {
		return
	}
	if result.Applied {
		s.kickFeedOrderForChannelID(result.CanonicalID)
	}
	writeJSON(w, 200, map[string]any{})
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
		UpdatedAtMs int64   `json:"updated_at_ms"`
	}
	if err := decodeMutation(r, &body); err != nil {
		writeJSONError(w, 400, "invalid_body", err.Error())
		return
	}
	_, err := s.db.MutateProgress(body.VideoID, body.Position, body.Duration, body.UpdatedAtMs)
	if writeMutationError(w, "MutateProgress", err) {
		return
	}
	writeJSON(w, 200, map[string]any{})
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
	_, err := s.db.MutateMomentsCursor(body.VideoID, body.PositionMs, body.UpdatedAtMs, body.Scope, body.SortAtMs)
	if writeMutationError(w, "MutateMomentsCursor", err) {
		return
	}
	writeJSON(w, 200, map[string]any{})
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
