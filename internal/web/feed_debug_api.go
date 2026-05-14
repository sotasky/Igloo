package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

type feedDebugItemResponse struct {
	Success      bool                   `json:"success"`
	TweetID      string                 `json:"tweet_id"`
	Item         feedDebugItem          `json:"item"`
	Sources      []feedDebugSourceEntry `json:"sources"`
	IngestState  *feedDebugIngestState  `json:"ingest_state"`
	RankSnapshot feedDebugRankSnapshot  `json:"rank_snapshot"`
	ViewerState  feedDebugViewerState   `json:"viewer_state"`
	Visibility   feedDebugVisibility    `json:"visibility"`
}

type feedDebugItem struct {
	TweetID          string  `json:"tweet_id"`
	SourceHandle     string  `json:"source_handle"`
	AuthorHandle     string  `json:"author_handle"`
	CanonicalTweetID string  `json:"canonical_tweet_id"`
	QuoteTweetID     string  `json:"quote_tweet_id"`
	ContentHash      string  `json:"content_hash"`
	IsRetweet        bool    `json:"is_retweet"`
	IsReply          bool    `json:"is_reply"`
	IsGhost          bool    `json:"is_ghost"`
	PublishedAtMs    int64   `json:"published_at_ms"`
	FetchedAtMs      int64   `json:"fetched_at_ms"`
	AlgoInterest     float64 `json:"algo_interest"`
	AlgoScoredAtMs   int64   `json:"algo_scored_at_ms"`
}

type feedDebugSourceEntry struct {
	SourceID       string           `json:"source_id"`
	FirstSeenAtSec int64            `json:"first_seen_at_sec"`
	LastSeenAtSec  int64            `json:"last_seen_at_sec"`
	Source         *feedDebugSource `json:"source"`
}

type feedDebugSource struct {
	Platform       string `json:"platform"`
	SourceType     string `json:"source_type"`
	ExternalID     string `json:"external_id"`
	Label          string `json:"label"`
	URL            string `json:"url"`
	Enabled        bool   `json:"enabled"`
	LastCheckedSec int64  `json:"last_checked_sec"`
	LastOKSec      int64  `json:"last_ok_sec"`
	LastError      string `json:"last_error"`
	CreatedAtSec   int64  `json:"created_at_sec"`
	UpdatedAtSec   int64  `json:"updated_at_sec"`
}

type feedDebugIngestState struct {
	Handle           string  `json:"handle"`
	FailCount        int     `json:"fail_count"`
	NextRetryAtSec   float64 `json:"next_retry_at_sec"`
	LastSuccessAtSec float64 `json:"last_success_at_sec"`
	LastAttemptAtSec float64 `json:"last_attempt_at_sec"`
	LastError        string  `json:"last_error"`
	LastHTTPStatus   int     `json:"last_http_status"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	UpdatedAtMs      int64   `json:"updated_at_ms"`
}

type feedDebugRankSnapshot struct {
	InSnapshot         bool    `json:"in_snapshot"`
	RankPosition       int     `json:"rank_position,omitempty"`
	BaseScore          float64 `json:"base_score,omitempty"`
	DecayFactor        float64 `json:"decay_factor,omitempty"`
	FreshnessBonus     float64 `json:"freshness_bonus,omitempty"`
	Jitter             float64 `json:"jitter,omitempty"`
	DiversityDemotedBy float64 `json:"diversity_demoted_by,omitempty"`
	FinalScore         float64 `json:"final_score,omitempty"`
	ComputedAtMs       int64   `json:"computed_at_ms,omitempty"`
}

type feedDebugViewerState struct {
	Username         string                 `json:"username"`
	SeenAtMs         *int64                 `json:"seen_at_ms"`
	AuthorChannelID  string                 `json:"author_channel_id"`
	AuthorIsFollowed bool                   `json:"author_is_followed"`
	AuthorFollowedAt *int64                 `json:"author_followed_at"`
	AuthorIsStarred  bool                   `json:"author_is_starred"`
	AuthorStarredAt  *int64                 `json:"author_starred_at"`
	SourceChannelID  string                 `json:"source_channel_id"`
	SourceIsFollowed bool                   `json:"source_is_followed"`
	SourceFollowedAt *int64                 `json:"source_followed_at"`
	SourceIsStarred  bool                   `json:"source_is_starred"`
	SourceStarredAt  *int64                 `json:"source_starred_at"`
	MutedAuthorAt    *int64                 `json:"muted_author_at"`
	MutedSourceAt    *int64                 `json:"muted_source_at"`
	RelatedSeen      []feedDebugRelatedSeen `json:"related_seen"`
}

type feedDebugRelatedSeen struct {
	TweetID  string `json:"tweet_id"`
	SeenAtMs int64  `json:"seen_at_ms"`
}

type feedDebugVisibility struct {
	VisibleNow                bool     `json:"visible_now"`
	AbsentReasons             []string `json:"absent_reasons"`
	RankInputExclusionReasons []string `json:"rank_input_exclusion_reasons"`
}

func (s *Server) handleFeedDebugItem(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	tweetID := strings.TrimSpace(r.PathValue("tweetID"))
	if tweetID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "tweet_id required"})
		return
	}

	resp, err := s.buildFeedDebugItem(user.Username, tweetID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "feed item not found"})
		return
	}
	if err != nil {
		slog.Error("feed debug item", "tweet", tweetID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":       resp.Success,
		"tweet_id":      resp.TweetID,
		"item":          resp.Item,
		"sources":       resp.Sources,
		"ingest_state":  resp.IngestState,
		"rank_snapshot": resp.RankSnapshot,
		"viewer_state":  resp.ViewerState,
		"visibility":    resp.Visibility,
	})
}

func (s *Server) buildFeedDebugItem(username, tweetID string) (feedDebugItemResponse, error) {
	var resp feedDebugItemResponse
	err := s.db.WithRead(func(conn *sql.DB) error {
		item, err := queryFeedDebugItem(conn, tweetID)
		if err != nil {
			return err
		}
		sources, err := queryFeedDebugSources(conn, tweetID)
		if err != nil {
			return err
		}
		rank, err := queryFeedDebugRankSnapshot(conn, username, tweetID)
		if err != nil {
			return err
		}
		viewer, err := queryFeedDebugViewerState(conn, username, item)
		if err != nil {
			return err
		}
		ingest, err := queryFeedDebugIngestState(conn, feedDebugIngestCandidates(item, sources))
		if err != nil {
			return err
		}

		reasons := feedDebugCurrentAbsentReasons(rank, viewer)
		resp = feedDebugItemResponse{
			Success:      true,
			TweetID:      tweetID,
			Item:         item,
			Sources:      sources,
			IngestState:  ingest,
			RankSnapshot: rank,
			ViewerState:  viewer,
			Visibility: feedDebugVisibility{
				VisibleNow:                len(reasons) == 0,
				AbsentReasons:             reasons,
				RankInputExclusionReasons: feedDebugRankInputExclusionReasons(item, viewer),
			},
		}
		return nil
	})
	return resp, err
}

func queryFeedDebugItem(conn *sql.DB, tweetID string) (feedDebugItem, error) {
	var item feedDebugItem
	var isRetweet, isReply, isGhost int
	err := conn.QueryRow(`
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(canonical_tweet_id,''), COALESCE(quote_tweet_id,''),
		       COALESCE(content_hash,''), COALESCE(is_retweet,0),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       published_at, fetched_at,
		       COALESCE(algo_interest,0), COALESCE(algo_scored_at,0)
		FROM feed_items
		WHERE tweet_id = ?
	`, tweetID).Scan(
		&item.TweetID, &item.SourceHandle, &item.AuthorHandle,
		&item.CanonicalTweetID, &item.QuoteTweetID, &item.ContentHash,
		&isRetweet, &isReply, &isGhost,
		&item.PublishedAtMs, &item.FetchedAtMs,
		&item.AlgoInterest, &item.AlgoScoredAtMs,
	)
	if err != nil {
		return feedDebugItem{}, err
	}
	item.IsRetweet = isRetweet != 0
	item.IsReply = isReply != 0
	item.IsGhost = isGhost != 0
	return item, nil
}

func queryFeedDebugSources(conn *sql.DB, tweetID string) ([]feedDebugSourceEntry, error) {
	rows, err := conn.Query(`
		SELECT fis.source_id, fis.first_seen_at, fis.last_seen_at,
		       COALESCE(fs.platform,''), COALESCE(fs.source_type,''),
		       COALESCE(fs.external_id,''), COALESCE(fs.label,''), COALESCE(fs.url,''),
		       COALESCE(fs.enabled,0), COALESCE(fs.last_checked,0),
		       COALESCE(fs.last_ok,0), COALESCE(fs.last_error,''),
		       COALESCE(fs.created_at,0), COALESCE(fs.updated_at,0)
		FROM feed_item_sources fis
		LEFT JOIN feed_sources fs ON fs.source_id = fis.source_id
		WHERE fis.tweet_id = ?
		ORDER BY fis.last_seen_at DESC, fis.source_id
	`, tweetID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []feedDebugSourceEntry
	for rows.Next() {
		entry := feedDebugSourceEntry{Source: &feedDebugSource{}}
		var enabled int
		if err := rows.Scan(
			&entry.SourceID, &entry.FirstSeenAtSec, &entry.LastSeenAtSec,
			&entry.Source.Platform, &entry.Source.SourceType, &entry.Source.ExternalID,
			&entry.Source.Label, &entry.Source.URL, &enabled,
			&entry.Source.LastCheckedSec, &entry.Source.LastOKSec, &entry.Source.LastError,
			&entry.Source.CreatedAtSec, &entry.Source.UpdatedAtSec,
		); err != nil {
			return nil, err
		}
		entry.Source.Enabled = enabled != 0
		out = append(out, entry)
	}
	return out, rows.Err()
}

func queryFeedDebugRankSnapshot(conn *sql.DB, username, tweetID string) (feedDebugRankSnapshot, error) {
	var rank feedDebugRankSnapshot
	err := conn.QueryRow(`
		SELECT rank_position, base_score, decay_factor, freshness_bonus,
		       jitter, diversity_demoted_by, final_score, computed_at
		FROM feed_rank_snapshot
		WHERE username = ? AND tweet_id = ?
	`, username, tweetID).Scan(
		&rank.RankPosition, &rank.BaseScore, &rank.DecayFactor,
		&rank.FreshnessBonus, &rank.Jitter, &rank.DiversityDemotedBy,
		&rank.FinalScore, &rank.ComputedAtMs,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return rank, nil
	}
	if err != nil {
		return rank, err
	}
	rank.InSnapshot = true
	return rank, nil
}

func queryFeedDebugViewerState(conn *sql.DB, username string, item feedDebugItem) (feedDebugViewerState, error) {
	authorChannelID := feedDebugTwitterChannelID(item.AuthorHandle)
	sourceChannelID := feedDebugTwitterChannelID(item.SourceHandle)
	viewer := feedDebugViewerState{
		Username:        username,
		AuthorChannelID: authorChannelID,
		SourceChannelID: sourceChannelID,
	}

	seenAt, err := queryOptionalInt64(conn, `SELECT seen_at FROM feed_seen WHERE username = ? AND tweet_id = ?`, username, item.TweetID)
	if err != nil {
		return viewer, err
	}
	viewer.SeenAtMs = seenAt
	if authorChannelID != "" {
		viewer.AuthorFollowedAt, err = queryOptionalInt64(conn, `SELECT followed_at FROM channel_follows WHERE user_id = '' AND channel_id = ?`, authorChannelID)
		if err != nil {
			return viewer, err
		}
		viewer.AuthorIsFollowed = viewer.AuthorFollowedAt != nil
		viewer.AuthorStarredAt, err = queryOptionalInt64(conn, `SELECT starred_at FROM channel_stars WHERE user_id = '' AND channel_id = ?`, authorChannelID)
		if err != nil {
			return viewer, err
		}
		viewer.AuthorIsStarred = viewer.AuthorStarredAt != nil
		viewer.MutedAuthorAt, err = queryMutedAccountAt(conn, item.AuthorHandle)
		if err != nil {
			return viewer, err
		}
	}
	if sourceChannelID != "" {
		viewer.SourceFollowedAt, err = queryOptionalInt64(conn, `SELECT followed_at FROM channel_follows WHERE user_id = '' AND channel_id = ?`, sourceChannelID)
		if err != nil {
			return viewer, err
		}
		viewer.SourceIsFollowed = viewer.SourceFollowedAt != nil
		viewer.SourceStarredAt, err = queryOptionalInt64(conn, `SELECT starred_at FROM channel_stars WHERE user_id = '' AND channel_id = ?`, sourceChannelID)
		if err != nil {
			return viewer, err
		}
		viewer.SourceIsStarred = viewer.SourceStarredAt != nil
		viewer.MutedSourceAt, err = queryMutedAccountAt(conn, item.SourceHandle)
		if err != nil {
			return viewer, err
		}
	}
	viewer.RelatedSeen, err = queryFeedDebugRelatedSeen(conn, username, item)
	if err != nil {
		return viewer, err
	}
	return viewer, nil
}

func queryFeedDebugIngestState(conn *sql.DB, candidates []string) (*feedDebugIngestState, error) {
	for _, candidate := range candidates {
		var state feedDebugIngestState
		var status sql.NullInt64
		err := conn.QueryRow(`
			SELECT handle, COALESCE(fail_count,0), COALESCE(next_retry_at,0),
			       COALESCE(last_success_at,0), COALESCE(last_attempt_at,0),
			       COALESCE(last_error,''), last_http_status,
			       COALESCE(avg_latency_ms,0), updated_at
			FROM ingest_state
			WHERE handle = ?
		`, candidate).Scan(
			&state.Handle, &state.FailCount, &state.NextRetryAtSec,
			&state.LastSuccessAtSec, &state.LastAttemptAtSec,
			&state.LastError, &status, &state.AvgLatencyMs, &state.UpdatedAtMs,
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if status.Valid {
			state.LastHTTPStatus = int(status.Int64)
		}
		return &state, nil
	}
	return nil, nil
}

func queryFeedDebugRelatedSeen(conn *sql.DB, username string, item feedDebugItem) ([]feedDebugRelatedSeen, error) {
	if strings.TrimSpace(item.ContentHash) == "" {
		return nil, nil
	}
	rows, err := conn.Query(`
		SELECT fs.tweet_id, fs.seen_at
		FROM feed_seen fs
		JOIN feed_items fi ON fi.tweet_id = fs.tweet_id
		WHERE fs.username = ?
		  AND fi.content_hash = ?
		  AND fs.tweet_id != ?
		ORDER BY fs.seen_at DESC, fs.tweet_id
		LIMIT 10
	`, username, item.ContentHash, item.TweetID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []feedDebugRelatedSeen
	for rows.Next() {
		var row feedDebugRelatedSeen
		if err := rows.Scan(&row.TweetID, &row.SeenAtMs); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func queryOptionalInt64(conn *sql.DB, query string, args ...any) (*int64, error) {
	var value int64
	err := conn.QueryRow(query, args...).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func queryMutedAccountAt(conn *sql.DB, handle string) (*int64, error) {
	normalized := feedDebugNormalizedHandle(handle)
	if normalized == "" {
		return nil, nil
	}
	return queryOptionalInt64(conn, `
		SELECT muted_at
		FROM muted_accounts
		WHERE LOWER(LTRIM(TRIM(handle), '@')) = ?
		LIMIT 1
	`, normalized)
}

func feedDebugIngestCandidates(item feedDebugItem, sources []feedDebugSourceEntry) []string {
	var candidates []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}
	add(feedDebugTwitterChannelID(item.AuthorHandle))
	add(feedDebugTwitterChannelID(item.SourceHandle))
	for _, source := range sources {
		if source.Source != nil {
			add(feedDebugTwitterChannelID(source.Source.ExternalID))
			add(feedDebugTwitterChannelID(source.Source.Label))
		}
		add(source.SourceID)
	}
	add(item.AuthorHandle)
	add(item.SourceHandle)
	return candidates
}

func feedDebugCurrentAbsentReasons(rank feedDebugRankSnapshot, viewer feedDebugViewerState) []string {
	var reasons []string
	if !rank.InSnapshot {
		reasons = append(reasons, "not_in_rank_snapshot")
	}
	if viewer.SeenAtMs != nil {
		reasons = append(reasons, "seen_exact")
	}
	if len(viewer.RelatedSeen) > 0 {
		reasons = append(reasons, "seen_content_hash")
	}
	return reasons
}

func feedDebugRankInputExclusionReasons(item feedDebugItem, viewer feedDebugViewerState) []string {
	var reasons []string
	if viewer.MutedAuthorAt != nil {
		reasons = append(reasons, "muted_author")
	}
	if viewer.MutedSourceAt != nil && viewer.SourceChannelID != viewer.AuthorChannelID {
		reasons = append(reasons, "muted_source")
	}
	if item.IsGhost {
		reasons = append(reasons, "ghost_item")
	}
	if item.CanonicalTweetID != "" && item.CanonicalTweetID != item.TweetID {
		reasons = append(reasons, "non_canonical_tweet")
	}
	if item.PublishedAtMs <= 0 {
		reasons = append(reasons, "missing_published_at")
	}
	if item.AlgoScoredAtMs == 0 {
		reasons = append(reasons, "unscored")
	}
	return reasons
}

func feedDebugTwitterChannelID(handle string) string {
	normalized := feedDebugNormalizedHandle(handle)
	if normalized == "" {
		return ""
	}
	return "twitter_" + normalized
}

func feedDebugNormalizedHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	handle = strings.TrimPrefix(handle, "@")
	return strings.ToLower(handle)
}
