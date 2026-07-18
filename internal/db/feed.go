package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func feedItemSelectSQL(alias string) string {
	return fmt.Sprintf(`%[1]s.tweet_id, COALESCE(%[1]s.source_handle,''), COALESCE(%[1]s.author_handle,''),
		COALESCE(%[1]s.author_display_name,''), COALESCE(%[1]s.author_avatar_url,''),
		COALESCE(%[1]s.body_text,''), COALESCE(%[1]s.lang,''),
		COALESCE(%[1]s.is_retweet,0), COALESCE(%[1]s.retweeted_by_handle,''),
		COALESCE(%[1]s.retweeted_by_display_name,''),
		COALESCE(%[1]s.quote_tweet_id,''), COALESCE(%[1]s.quote_author_handle,''),
		COALESCE(%[1]s.quote_author_display_name,''), COALESCE(%[1]s.quote_author_avatar_url,''),
		COALESCE(%[1]s.quote_body_text,''), COALESCE(%[1]s.quote_lang,''),
		COALESCE(%[1]s.quote_media_json,''), COALESCE(%[1]s.media_json,''),
		COALESCE(%[1]s.canonical_url,''), COALESCE(%[1]s.reply_to_handle,''),
		COALESCE(%[1]s.reply_to_status,''), COALESCE(%[1]s.is_reply,0), COALESCE(%[1]s.is_ghost,0),
		%[1]s.quote_published_at,
		COALESCE(%[1]s.views,0), COALESCE(%[1]s.likes,0), COALESCE(%[1]s.retweets,0),
		%[1]s.published_at, %[1]s.fetched_at,
		COALESCE(%[1]s.content_hash,''), COALESCE(%[1]s.canonical_tweet_id,''),
		COALESCE(%[1]s.source_channel_id,''), COALESCE(%[1]s.channel_id,''),
		COALESCE(%[1]s.quote_channel_id,''), COALESCE(%[1]s.reply_channel_id,''),
		COALESCE(%[1]s.reposter_channel_id,'')`, alias)
}

// ListFeedItemsPage returns feed items with cursor-based keyset pagination.
// When excludeSeen is true, the ranking pool contains only unseen content.
func (db *DB) ListFeedItemsPage(limit int, cursor *model.FeedCursor, excludeSeen bool) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 40
	}

	var where []string
	var args []any
	where = append(where, feedPrimaryItemPredicate("feed_items"))
	where = append(where, feedActiveOwnerPredicate("feed_items"))

	muted, _ := db.GetMutedChannelIDs()
	if len(muted) > 0 {
		placeholders := strings.Repeat("?,", len(muted))
		placeholders = placeholders[:len(placeholders)-1]
		where = append(where, "channel_id NOT IN ("+placeholders+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
		where = append(where, "COALESCE(source_channel_id,'') NOT IN ("+placeholders+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
	}

	if cursor != nil && cursor.BeforePublishedAtMs > 0 && cursor.BeforeTweetID != "" {
		where = append(where, "(published_at < ? OR (published_at = ? AND tweet_id < ?))")
		args = append(args, cursor.BeforePublishedAtMs, cursor.BeforePublishedAtMs, cursor.BeforeTweetID)
	}

	// Exclude seen items on every page so the scroll tail is genuinely unseen content.
	if excludeSeen {
		where = append(where, feedUnseenPredicate("feed_items"))
	}

	where = append(where, "(canonical_tweet_id IS NULL OR canonical_tweet_id = '' OR canonical_tweet_id = tweet_id)")

	// Apply the shared retweet/quote filter (handles dedup-awareness and
	// self-pass). The query has no alias on feed_items, so we pass the
	// table name as the "alias" — qualified column references still work.
	where = append(where, retweetFilterClause("feed_items"))

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := `
		SELECT ` + feedItemSelectSQL("feed_items") + `
		FROM feed_items_resolved AS feed_items
		` + whereClause + `
		ORDER BY published_at DESC, tweet_id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanFeedItems(rows)
}

// GetFeedItemsForTweetIDs fetches full feed items by tweet IDs.
func (db *DB) GetFeedItemsForTweetIDs(tweetIDs []string) (map[string]model.FeedItem, error) {
	if len(tweetIDs) == 0 {
		return make(map[string]model.FeedItem), nil
	}
	placeholders := strings.Repeat("?,", len(tweetIDs))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, id := range tweetIDs {
		args = append(args, id)
	}

	rows, err := db.reader().Query(`
		SELECT `+feedItemSelectSQL("feed_items")+`
		FROM feed_items_resolved AS feed_items
		WHERE tweet_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	items, err := scanFeedItems(rows)
	if err != nil {
		return nil, err
	}
	result := make(map[string]model.FeedItem, len(items))
	for _, item := range items {
		result[item.TweetID] = item
	}
	return result, nil
}

// ResolveFeedStateID maps a stored feed row to the status ID that should own
// user state such as likes and bookmarks. Plain videos and unknown feed IDs
// resolve to themselves.
func (db *DB) ResolveFeedStateID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	var canonicalURL string
	err := db.reader().QueryRow(
		`SELECT COALESCE(canonical_url, '') FROM feed_items WHERE tweet_id = ?`,
		id,
	).Scan(&canonicalURL)
	if err == sql.ErrNoRows {
		return id, nil
	}
	if err != nil {
		return "", err
	}
	if stateID := model.TwitterStatusIDFromURL(canonicalURL); stateID != "" {
		return stateID, nil
	}
	return id, nil
}

func resolveFeedStateIDTx(tx *sql.Tx, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	var canonicalURL string
	err := tx.QueryRow(
		`SELECT COALESCE(canonical_url, '') FROM feed_items WHERE tweet_id = ?`,
		id,
	).Scan(&canonicalURL)
	if err == sql.ErrNoRows {
		return id, nil
	}
	if err != nil {
		return "", err
	}
	if stateID := model.TwitterStatusIDFromURL(canonicalURL); stateID != "" {
		return stateID, nil
	}
	return id, nil
}

func (db *DB) resolveFeedStateIDForWriteTx(tx *sql.Tx, id string) (string, error) {
	sourceID := strings.TrimSpace(id)
	stateID, err := resolveFeedStateIDTx(tx, sourceID)
	if err != nil || sourceID == "" || stateID == "" || stateID == sourceID {
		return stateID, err
	}
	if err := db.materializeResolvedFeedStateTx(tx, sourceID, stateID); err != nil {
		return "", err
	}
	return stateID, nil
}

// ResolveFeedStateIDForWrite maps a feed row to its user-state owner and copies
// the stored feed/media shape to that owner. This keeps canonicalized likes and
// bookmarks attached to the media the user actually acted on.
func (db *DB) ResolveFeedStateIDForWrite(id string) (string, error) {
	var stateID string
	err := db.WithWrite(func(tx *sql.Tx) error {
		var err error
		stateID, err = db.resolveFeedStateIDForWriteTx(tx, id)
		return err
	})
	return stateID, err
}

func (db *DB) materializeResolvedFeedStateTx(tx *sql.Tx, sourceID, stateID string) error {
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(stateID) == "" || sourceID == stateID {
		return nil
	}
	if _, err := tx.Exec(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id,
			body_text, media_json, canonical_url,
			published_at, fetched_at,
			content_hash, canonical_tweet_id
		)
		SELECT
			?,
			source_channel_id,
			channel_id,
			body_text,
			media_json,
			canonical_url,
			published_at,
			fetched_at,
			content_hash, ?
		FROM feed_items
		WHERE tweet_id = ?
		ON CONFLICT(tweet_id) DO UPDATE SET
			source_channel_id = CASE WHEN COALESCE(feed_items.source_channel_id, '') = '' THEN excluded.source_channel_id ELSE feed_items.source_channel_id END,
			channel_id = CASE WHEN COALESCE(feed_items.channel_id, '') = '' THEN excluded.channel_id ELSE feed_items.channel_id END,
			body_text = CASE WHEN COALESCE(feed_items.body_text, '') = '' THEN excluded.body_text ELSE feed_items.body_text END,
			media_json = CASE WHEN COALESCE(feed_items.media_json, '') IN ('', '[]') THEN excluded.media_json ELSE feed_items.media_json END,
			canonical_url = CASE WHEN COALESCE(feed_items.canonical_url, '') = '' THEN excluded.canonical_url ELSE feed_items.canonical_url END,
			published_at = CASE WHEN COALESCE(feed_items.published_at, 0) = 0 THEN excluded.published_at ELSE feed_items.published_at END,
			fetched_at = CASE WHEN COALESCE(feed_items.fetched_at, 0) = 0 THEN excluded.fetched_at ELSE feed_items.fetched_at END,
			content_hash = CASE WHEN COALESCE(feed_items.content_hash, '') = '' THEN excluded.content_hash ELSE feed_items.content_hash END,
			canonical_tweet_id = CASE WHEN COALESCE(feed_items.canonical_tweet_id, '') = '' THEN excluded.canonical_tweet_id ELSE feed_items.canonical_tweet_id END
	`, stateID, stateID, sourceID); err != nil {
		return err
	}
	nowMs := time.Now().UnixMilli()
	if _, err := tx.Exec(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			object_id, desired_object_id, is_auto, audio_language, required_reason,
			created_at_ms, updated_at_ms
		)
		SELECT
			'twitter_tweet_' || ? || '_' || asset_kind ||
				CASE WHEN media_index > 0 THEN '_' || CAST(media_index AS TEXT) ELSE '' END,
			asset_kind, owner_kind, ?, media_index,
			object_id, desired_object_id, is_auto, audio_language, required_reason, ?, ?
		FROM assets
		WHERE owner_kind = 'tweet'
		  AND owner_id = ?
		  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		ON CONFLICT(asset_kind, owner_kind, owner_id, media_index) DO UPDATE SET
			asset_id = excluded.asset_id,
			object_id = excluded.object_id,
			desired_object_id = excluded.desired_object_id,
			is_auto = excluded.is_auto,
			audio_language = excluded.audio_language,
			required_reason = excluded.required_reason,
			revision = assets.revision + 1,
			updated_at_ms = excluded.updated_at_ms
	`, stateID, stateID, nowMs, nowMs, sourceID); err != nil {
		return err
	}
	return nil
}

// GetSeenTweetIDs returns which tweet IDs have been seen.
func (db *DB) GetSeenTweetIDs(tweetIDs []string) (map[string]bool, error) {
	if len(tweetIDs) == 0 {
		return make(map[string]bool), nil
	}
	placeholders := strings.Repeat("?,", len(tweetIDs))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, id := range tweetIDs {
		args = append(args, id)
	}

	rows, err := db.reader().Query(
		"SELECT tweet_id FROM feed_seen WHERE tweet_id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	seen := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		seen[id] = true
	}
	return seen, rows.Err()
}

// GetFeedLikesForTweetIDs returns which tweet IDs are liked.
func (db *DB) GetFeedLikesForTweetIDs(tweetIDs []string) (map[string]bool, error) {
	if len(tweetIDs) == 0 {
		return make(map[string]bool), nil
	}
	placeholders := strings.Repeat("?,", len(tweetIDs))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, id := range tweetIDs {
		args = append(args, id)
	}

	rows, err := db.reader().Query(
		"SELECT tweet_id FROM feed_likes WHERE tweet_id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	liked := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		liked[id] = true
	}
	return liked, rows.Err()
}

// GetBookmarksForVideoIDs returns which video IDs are bookmarked.
func (db *DB) GetBookmarksForVideoIDs(videoIDs []string) (map[string]bool, error) {
	if len(videoIDs) == 0 {
		return make(map[string]bool), nil
	}
	placeholders := strings.Repeat("?,", len(videoIDs))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, id := range videoIDs {
		args = append(args, id)
	}

	rows, err := db.reader().Query(
		"SELECT DISTINCT video_id FROM bookmarks WHERE video_id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	bookmarked := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		bookmarked[id] = true
	}
	return bookmarked, rows.Err()
}

// BookmarkInfo holds bookmark metadata for enrichment.
type BookmarkInfo struct {
	CategoryID     *int64
	CustomTitle    *string
	AccountHandles *string
	MediaIndices   *string
	BookmarkedAtMs int64
}

// GetBookmarksForVideoIDsRich returns bookmark info (not just existence) per video ID.
func (db *DB) GetBookmarksForVideoIDsRich(videoIDs []string) (map[string]BookmarkInfo, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(videoIDs))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, id := range videoIDs {
		args = append(args, id)
	}
	rows, err := db.reader().Query(
		`SELECT video_id, category_id, custom_title, account_handles, media_indices,
		        COALESCE(bookmarked_at, 0)
		   FROM bookmarks
		  WHERE video_id IN (`+placeholders+`)
		  ORDER BY video_id`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	result := make(map[string]BookmarkInfo)
	for rows.Next() {
		var id string
		var categoryID sql.NullInt64
		var customTitle, accountHandles, mediaIndices sql.NullString
		var bookmarkedAtMs int64
		if err := rows.Scan(
			&id,
			&categoryID,
			&customTitle,
			&accountHandles,
			&mediaIndices,
			&bookmarkedAtMs,
		); err != nil {
			continue
		}
		if _, exists := result[id]; exists {
			continue
		}
		info := BookmarkInfo{BookmarkedAtMs: bookmarkedAtMs}
		if categoryID.Valid {
			value := categoryID.Int64
			info.CategoryID = &value
		}
		if customTitle.Valid {
			value := customTitle.String
			info.CustomTitle = &value
		}
		if accountHandles.Valid {
			value := accountHandles.String
			info.AccountHandles = &value
		}
		if mediaIndices.Valid {
			value := mediaIndices.String
			info.MediaIndices = &value
		}
		result[id] = info
	}
	return result, rows.Err()
}

func (db *DB) GetMutedChannelIDs() ([]string, error) {
	rows, err := db.reader().Query("SELECT channel_id FROM muted_channels ORDER BY channel_id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var channelIDs []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		channelIDs = append(channelIDs, channelID)
	}
	return channelIDs, rows.Err()
}

// GetMutedAccounts returns presentation handles for the settings UI.
func (db *DB) GetMutedAccounts() ([]string, error) {
	rows, err := db.reader().Query(`
		SELECT coalesce(nullif(profile.handle, ''), muted.channel_id)
		FROM muted_channels muted
		LEFT JOIN channel_profiles profile ON profile.channel_id = muted.channel_id
		ORDER BY lower(coalesce(nullif(profile.handle, ''), muted.channel_id))
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var handles []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		handles = append(handles, h)
	}
	return handles, rows.Err()
}

// GetFeedLikedPage returns liked items with cursor-based pagination.
func (db *DB) GetFeedLikedPage(limit int, cursor *model.FeedCursor) ([]model.FeedLike, error) {
	if limit <= 0 {
		limit = 40
	}

	var where []string
	var args []any

	if cursor != nil && cursor.BeforePublishedAtMs > 0 && cursor.BeforeTweetID != "" {
		where = append(where, "(fl.liked_at < ? OR (fl.liked_at = ? AND fl.tweet_id < ?))")
		args = append(args, cursor.BeforePublishedAtMs, cursor.BeforePublishedAtMs, cursor.BeforeTweetID)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	rows, err := db.reader().Query(`
		SELECT fl.tweet_id, fl.liked_at,
		       COALESCE(fi.source_handle,''), COALESCE(fi.author_handle,''),
		       COALESCE(fi.author_display_name,''), COALESCE(fi.canonical_url,''),
		       COALESCE(fi.canonical_url,''), COALESCE(fi.body_text,''),
		       fi.published_at, '', COALESCE(fi.author_avatar_url,''),
		       COALESCE(fi.media_json,''), 'twitter', ''
		FROM feed_likes fl
		JOIN feed_items_resolved fi ON fi.tweet_id = fl.tweet_id
		`+whereClause+`
		ORDER BY fl.liked_at DESC, fl.tweet_id DESC
		LIMIT ?
	`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var likes []model.FeedLike
	for rows.Next() {
		var l model.FeedLike
		var likedAt, publishedAt sql.NullInt64
		err := rows.Scan(
			&l.TweetID, &likedAt,
			&l.SourceHandle, &l.AuthorHandle,
			&l.AuthorDisplayName, &l.Link,
			&l.CanonicalXLink, &l.BodyText,
			&publishedAt, &l.MediaURL, &l.AvatarURL,
			&l.MediaJSON, &l.Platform,
			&l.QuotePayloadJSON,
		)
		if err != nil {
			return nil, err
		}
		if t := millisToTimePtr(likedAt); t != nil {
			l.LikedAt = *t
		}
		l.PublishedAt = millisToTimePtr(publishedAt)
		likes = append(likes, l)
	}
	return likes, rows.Err()
}

// GetRetweetSources returns retweeters grouped by content hash.
func (db *DB) GetRetweetSources(contentHashes []string) (map[string][]model.RetweeterInfo, error) {
	if len(contentHashes) == 0 {
		return make(map[string][]model.RetweeterInfo), nil
	}
	placeholders := strings.Repeat("?,", len(contentHashes))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, h := range contentHashes {
		args = append(args, h)
	}

	rows, err := db.reader().Query(`
		SELECT content_hash, retweeter_channel_id, retweeter_handle,
		       COALESCE(retweeter_display_name,''), tweet_id
		FROM retweet_sources_resolved
		WHERE content_hash IN (`+placeholders+`)
		ORDER BY content_hash, published_at DESC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	result := make(map[string][]model.RetweeterInfo)
	for rows.Next() {
		var hash, channelID, handle, displayName, tweetID string
		if err := rows.Scan(&hash, &channelID, &handle, &displayName, &tweetID); err != nil {
			return nil, err
		}
		_ = tweetID
		result[hash] = append(result[hash], model.RetweeterInfo{
			Handle:      handle,
			DisplayName: displayName,
			ChannelID:   channelID,
			AvatarURL:   "/api/media/avatar/" + channelID,
		})
	}
	return result, rows.Err()
}

// RetweetSourceRow is the Android sync contract row for retweet_sources.
// It mirrors the server table columns and matches Android's RetweetSourceEntity.
type RetweetSourceRow struct {
	ContentHash          string `json:"content_hash"`
	RetweeterChannelID   string `json:"retweeter_channel_id"`
	RetweeterHandle      string `json:"retweeter_handle"`
	RetweeterDisplayName string `json:"retweeter_display_name"`
	TweetID              string `json:"tweet_id"`
	PublishedAt          int64  `json:"published_at"`
}

// GetRetweetSourceRows returns raw retweet_sources rows grouped by content_hash.
// Callers should treat the returned rows as server-owned schema, not UI hints.
func (db *DB) GetRetweetSourceRows(contentHashes []string) (map[string][]RetweetSourceRow, error) {
	if len(contentHashes) == 0 {
		return make(map[string][]RetweetSourceRow), nil
	}

	placeholders := strings.Repeat("?,", len(contentHashes))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(contentHashes))
	for _, h := range contentHashes {
		args = append(args, h)
	}

	rows, err := db.reader().Query(`
		SELECT content_hash,
		       retweeter_channel_id,
		       tweet_id,
		       COALESCE(published_at, 0)
		FROM retweet_sources
		WHERE content_hash IN (`+placeholders+`)
		ORDER BY content_hash, published_at DESC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	out := make(map[string][]RetweetSourceRow)
	for rows.Next() {
		var row RetweetSourceRow
		if err := rows.Scan(
			&row.ContentHash,
			&row.RetweeterChannelID,
			&row.TweetID,
			&row.PublishedAt,
		); err != nil {
			return nil, err
		}
		out[row.ContentHash] = append(out[row.ContentHash], row)
	}
	return out, rows.Err()
}

// GetVideosByIDs returns videos keyed by video_id.
func (db *DB) GetVideosByIDs(videoIDs []string) (map[string]model.Video, error) {
	if len(videoIDs) == 0 {
		return make(map[string]model.Video), nil
	}
	placeholders := strings.Repeat("?,", len(videoIDs))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, id := range videoIDs {
		args = append(args, id)
	}

	rows, err := db.reader().Query(`
		SELECT video_id, COALESCE(channel_id,''), COALESCE(title,''),
		       COALESCE(metadata_json,'')
		FROM videos
		WHERE video_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	result := make(map[string]model.Video)
	for rows.Next() {
		var v model.Video
		if err := rows.Scan(&v.VideoID, &v.ChannelID, &v.Title, &v.MetadataJSON); err != nil {
			return nil, err
		}
		result[v.VideoID] = v
	}
	return result, rows.Err()
}

// GetFeedItemsByAuthor returns feed items by author or source handle, newest first.
func (db *DB) GetFeedItemsByAuthor(handle string, limit int) ([]model.FeedItem, error) {
	return db.GetFeedItemsByAuthorPage(handle, limit, 0)
}

// CountFeedItemsByAuthor returns the total locally stored feed rows for a handle.
func (db *DB) CountFeedItemsByAuthor(handle string) (int, error) {
	channelID := model.TwitterChannelIDFromHandle(handle)
	if channelID == "" {
		return 0, nil
	}
	var count int
	err := db.reader().QueryRow(`
		SELECT COUNT(*)
		FROM feed_items
		WHERE channel_id = ? OR source_channel_id = ? OR quote_channel_id = ?
	`, channelID, channelID, channelID).Scan(&count)
	return count, err
}

// GetFeedItemsByAuthorPage returns a bounded page of feed items by author or
// source handle, newest first. The caller owns pagination; this method is not a
// retention policy.
func (db *DB) GetFeedItemsByAuthorPage(handle string, limit int, offset int) ([]model.FeedItem, error) {
	channelID := model.TwitterChannelIDFromHandle(handle)
	if channelID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 40
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := db.reader().Query(`
		SELECT `+feedItemSelectSQL("feed_items")+`
		FROM feed_items_resolved AS feed_items
		WHERE channel_id = ? OR source_channel_id = ? OR quote_channel_id = ?
		ORDER BY published_at DESC
		LIMIT ? OFFSET ?
	`, channelID, channelID, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanFeedItems(rows)
}

// GetFeedThreadItemsByAuthorPage returns one representative row per conversation
// root for an X profile. Replies in the same stored thread collapse to the
// newest matching row so profile infinite scroll cannot render the same thread
// again on a later page.
func (db *DB) GetFeedThreadItemsByAuthorPage(handle string, limit int, offset int) ([]model.FeedItem, error) {
	channelID := model.TwitterChannelIDFromHandle(handle)
	if channelID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 40
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := db.reader().Query(`
		WITH RECURSIVE
		matched(tweet_id) AS (
			SELECT tweet_id
			FROM feed_items
			WHERE (channel_id = ? OR source_channel_id = ? OR quote_channel_id = ?)
			  AND `+feedPrimaryItemPredicate("feed_items")+`
		),
		chain(seed_id, tweet_id, reply_to_status, depth) AS (
			SELECT m.tweet_id, f.tweet_id, COALESCE(f.reply_to_status, ''), 0
			FROM matched m
			JOIN feed_items f ON f.tweet_id = m.tweet_id
			UNION ALL
			SELECT chain.seed_id, parent.tweet_id, COALESCE(parent.reply_to_status, ''), chain.depth + 1
			FROM chain
			JOIN feed_items parent ON parent.tweet_id = chain.reply_to_status
			WHERE chain.reply_to_status != ''
			  AND chain.depth < 50
		),
		roots AS (
			SELECT c.seed_id, c.tweet_id AS root_id
			FROM chain c
			JOIN (
				SELECT seed_id, MAX(depth) AS max_depth
				FROM chain
				GROUP BY seed_id
			) deepest ON deepest.seed_id = c.seed_id AND deepest.max_depth = c.depth
		),
		ranked AS (
			SELECT f.tweet_id,
			       ROW_NUMBER() OVER (
				       PARTITION BY COALESCE(r.root_id, f.tweet_id)
				       ORDER BY f.published_at DESC, f.tweet_id DESC
			       ) AS rn
			FROM feed_items f
			JOIN matched m ON m.tweet_id = f.tweet_id
			LEFT JOIN roots r ON r.seed_id = f.tweet_id
		)
		SELECT `+feedItemSelectSQL("f")+`
		FROM ranked
		JOIN feed_items_resolved f ON f.tweet_id = ranked.tweet_id
		WHERE ranked.rn = 1
		ORDER BY f.published_at DESC, f.tweet_id DESC
		LIMIT ? OFFSET ?
	`, channelID, channelID, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanFeedItems(rows)
}

// scanFeedItems scans rows into FeedItem structs.
func scanFeedItems(rows *sql.Rows) ([]model.FeedItem, error) {
	var items []model.FeedItem
	for rows.Next() {
		f, err := scanFeedItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, rows.Err()
}

type feedItemScanner interface {
	Scan(dest ...any) error
}

func scanFeedItem(row feedItemScanner) (model.FeedItem, error) {
	var f model.FeedItem
	var quotePubAt, pubAt, fetchedAt sql.NullInt64
	err := row.Scan(
		&f.TweetID, &f.SourceHandle, &f.AuthorHandle,
		&f.AuthorDisplayName, &f.AuthorAvatarURL,
		&f.BodyText, &f.Lang,
		&f.IsRetweet, &f.RetweetedByHandle, &f.RetweetedByDisplayName,
		&f.QuoteTweetID, &f.QuoteAuthorHandle,
		&f.QuoteAuthorDisplayName, &f.QuoteAuthorAvatarURL,
		&f.QuoteBodyText, &f.QuoteLang,
		&f.QuoteMediaJSON, &f.MediaJSON,
		&f.CanonicalURL, &f.ReplyToHandle, &f.ReplyToStatus,
		&f.IsReply, &f.IsGhost, &quotePubAt,
		&f.Views, &f.Likes, &f.Retweets,
		&pubAt, &fetchedAt,
		&f.ContentHash, &f.CanonicalTweetID,
		&f.SourceChannelID, &f.ChannelID, &f.QuoteChannelID,
		&f.ReplyChannelID, &f.ReposterChannelID,
	)
	if err != nil {
		return model.FeedItem{}, err
	}
	f.QuotePublishedAt = millisToTimePtr(quotePubAt)
	f.PublishedAt = millisToTimePtr(pubAt)
	if t := millisToTimePtr(fetchedAt); t != nil {
		f.FetchedAt = *t
	}
	f.ParseMedia()
	return f, nil
}

// InsertFeedLike creates or updates a feed like record.
// fields map can include: source_handle, author_handle, author_display_name,
// body_text, link, canonical_x_link, published_at, media_url, avatar_url,
// media_json, platform, quote_payload_json.
func (db *DB) InsertFeedLike(tweetID string, fields map[string]string) error {
	_, err := db.MutateLike(LikeMutation{TweetID: tweetID, Action: "set", Fields: fields})
	return err
}

// DeleteFeedLike removes a feed like record.
func (db *DB) DeleteFeedLike(tweetID string) error {
	_, err := db.MutateLike(LikeMutation{TweetID: tweetID, Action: "clear"})
	return err
}

// MarkSeen marks tweet IDs as seen. Returns count of rows affected.
func (db *DB) MarkSeen(tweetIDs []string) (int, error) {
	result, err := db.MutateSeen(tweetIDs, 0)
	return result.Affected, err
}

func expandSeenConversationIDsTx(tx *sql.Tx, tweetIDs []string) ([]string, error) {
	if len(tweetIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("(?),", len(tweetIDs)), ",")
	args := make([]any, 0, len(tweetIDs))
	for _, id := range tweetIDs {
		args = append(args, id)
	}
	rows, err := tx.Query(`
		WITH RECURSIVE
		seed(tweet_id) AS (VALUES `+placeholders+`),
		resolved_seed(tweet_id) AS (
			SELECT tweet_id FROM seed
			UNION
			SELECT fi.canonical_tweet_id
			FROM seed
			CROSS JOIN feed_items fi ON fi.tweet_id = seed.tweet_id
			WHERE COALESCE(fi.is_retweet, 0) = 1
			  AND COALESCE(fi.quote_tweet_id, '') = ''
			  AND COALESCE(fi.canonical_tweet_id, '') != ''
		),
		up(seed_id, tweet_id, reply_to_status, depth) AS (
			SELECT fi.tweet_id, fi.tweet_id, COALESCE(fi.reply_to_status, ''), 0
			FROM resolved_seed
			CROSS JOIN feed_items fi ON fi.tweet_id = resolved_seed.tweet_id
			UNION
			SELECT up.seed_id, parent.tweet_id, COALESCE(parent.reply_to_status, ''), up.depth + 1
			FROM up
			JOIN feed_items parent ON parent.tweet_id = up.reply_to_status
			WHERE up.reply_to_status != ''
			  AND up.depth < 50
		),
		root_depth AS (
			SELECT seed_id, MAX(depth) AS max_depth
			FROM up
			GROUP BY seed_id
		),
		roots(root_id) AS (
			SELECT DISTINCT up.tweet_id
			FROM up
			JOIN root_depth
			  ON root_depth.seed_id = up.seed_id
			 AND root_depth.max_depth = up.depth
		),
		down(tweet_id, depth) AS (
			SELECT root_id, 0 FROM roots
			UNION
			SELECT child.tweet_id, down.depth + 1
			FROM down
			JOIN feed_items child ON child.reply_to_status = down.tweet_id
			WHERE child.reply_to_status IS NOT NULL
			  AND child.reply_to_status != ''
			  AND down.depth < 50
		)
		SELECT tweet_id FROM seed
		UNION
		SELECT tweet_id FROM down
		UNION
		SELECT repost.tweet_id
		FROM down
		CROSS JOIN feed_items repost INDEXED BY idx_feed_items_canonical_tweet
		  ON repost.canonical_tweet_id = down.tweet_id
		WHERE COALESCE(repost.is_retweet, 0) = 1
		  AND COALESCE(repost.quote_tweet_id, '') = ''
		  AND repost.canonical_tweet_id IS NOT NULL
		  AND repost.canonical_tweet_id != ''
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	out := make([]string, 0, len(tweetIDs))
	seen := make(map[string]bool, len(tweetIDs))
	for _, id := range tweetIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if strings.TrimSpace(id) == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MuteAccount resolves a presentation handle to its persisted channel identity.
func (db *DB) MuteAccount(handle string) error {
	channelID := model.TwitterChannelIDFromHandle(handle)
	if channelID == "" {
		return fmt.Errorf("invalid handle")
	}
	_, err := db.MutateMute(channelID, "set", 0)
	return err
}

// UnmuteAccount removes a handle from the muted list.
func (db *DB) UnmuteAccount(handle string) error {
	channelID := model.TwitterChannelIDFromHandle(handle)
	if channelID == "" {
		return fmt.Errorf("invalid handle")
	}
	_, err := db.MutateMute(channelID, "clear", 0)
	return err
}

type FeedUpsertResult struct {
	Processed              int
	XMediaRetentionChanges map[string][]string
}

// UpsertFeedItems inserts or updates feed_items rows, matching the Python upsert logic.
// Returns the number of items processed.
func (db *DB) UpsertFeedItems(items []model.FeedItem) (int, error) {
	result, err := db.UpsertFeedItemsDetailed(items)
	return result.Processed, err
}

func (db *DB) UpsertFeedItemsDetailed(items []model.FeedItem) (FeedUpsertResult, error) {
	if len(items) == 0 {
		return FeedUpsertResult{}, nil
	}
	normalizedItems := make([]model.FeedItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.TweetID) == "" {
			continue
		}
		item.ParseMedia()
		item = normalizeFeedItemIdentity(item)
		assignFeedRoleIdentities(&item)
		normalizedItems = append(normalizedItems, item)
	}
	if len(normalizedItems) == 0 {
		return FeedUpsertResult{}, nil
	}

	result := FeedUpsertResult{}
	changedByChannel := make(map[string]map[string]struct{})
	err := db.WithWrite(func(tx *sql.Tx) error {
		var translationTargetLang string
		if err := tx.QueryRow(`
			SELECT COALESCE((
				SELECT NULLIF(value, '')
				FROM settings
				WHERE key = 'translate_target_lang'
			), 'en')
		`).Scan(&translationTargetLang); err != nil {
			return err
		}
		translationTargetLang = strings.ToLower(strings.TrimSpace(translationTargetLang))
		if translationTargetLang == "" {
			translationTargetLang = "en"
		}

		invalidateTranslationStmt, err := tx.Prepare(`
			DELETE FROM translations
			WHERE tweet_id = ? AND field = ? AND target_lang = ?
			  AND EXISTS (
				SELECT 1
				FROM translation_jobs
				WHERE translation_jobs.tweet_id = translations.tweet_id
				  AND translation_jobs.field = translations.field
				  AND translation_jobs.target_lang = translations.target_lang
				  AND translation_jobs.source_hash != ?
			  )
		`)
		if err != nil {
			return err
		}
		defer func() { _ = invalidateTranslationStmt.Close() }()

		translationJobStmt, err := tx.Prepare(`
			INSERT INTO translation_jobs (
				tweet_id, field, target_lang, source_hash, status, priority,
				attempts, next_attempt_at, last_error_kind, last_error,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 'queued', 1, 0, 0, '', '', ?, ?)
			ON CONFLICT(tweet_id, field, target_lang) DO UPDATE SET
				source_hash = excluded.source_hash,
				status = 'queued',
				priority = 1,
				attempts = 0,
				next_attempt_at = 0,
				last_error_kind = '',
				last_error = '',
				updated_at = excluded.updated_at
			WHERE translation_jobs.source_hash != excluded.source_hash
		`)
		if err != nil {
			return err
		}
		defer func() { _ = translationJobStmt.Close() }()

		stmt, err := tx.Prepare(`
			INSERT INTO feed_items (
				tweet_id, source_channel_id, channel_id,
				body_text, lang, is_retweet,
				quote_tweet_id, quote_channel_id,
				quote_body_text, quote_lang, quote_media_json,
				media_json, canonical_url, reply_channel_id, reply_to_status,
				is_reply, is_ghost, reposter_channel_id,
				quote_published_at, views, likes, retweets,
				published_at, fetched_at,
				content_hash, canonical_tweet_id
			) VALUES (
				?, ?, ?,
				?, ?, ?,
				?, ?,
				?, ?, ?,
				?, ?, ?, ?,
				?, ?, ?,
				?, ?, ?, ?,
				?, ?,
				?, ?
			)
			ON CONFLICT(tweet_id) DO UPDATE SET
				source_channel_id = COALESCE(excluded.source_channel_id, feed_items.source_channel_id),
				channel_id = CASE WHEN COALESCE(feed_items.channel_id, '') = '' THEN excluded.channel_id ELSE feed_items.channel_id END,
				body_text = CASE
					WHEN excluded.body_text IS NULL OR excluded.body_text = '' THEN feed_items.body_text
					ELSE excluded.body_text
				END,
				lang = CASE
					WHEN excluded.lang IS NOT NULL
						AND excluded.lang != ''
						AND (
							feed_items.lang IS NULL
							OR feed_items.lang = ''
							OR LOWER(feed_items.lang) IN ('und','unknown','qam','qct','qht','qme','qst','zxx')
							OR (LOWER(feed_items.lang) GLOB 'q??' AND length(feed_items.lang) = 3)
						) THEN excluded.lang
					ELSE feed_items.lang
				END,
				media_json = COALESCE(excluded.media_json, feed_items.media_json),
				canonical_url = CASE
					WHEN excluded.canonical_url IS NOT NULL
					 AND excluded.canonical_url != ''
					 AND (
						COALESCE(feed_items.canonical_url, '') = ''
						OR LOWER(feed_items.canonical_url) LIKE '%/unknown/status/%'
						OR LOWER(feed_items.canonical_url) LIKE '%/undefined/status/%'
					 )
					THEN excluded.canonical_url
					ELSE feed_items.canonical_url
				END,
				quote_tweet_id = CASE
					WHEN COALESCE(feed_items.quote_tweet_id, '') = '' THEN excluded.quote_tweet_id
					ELSE feed_items.quote_tweet_id
				END,
				quote_channel_id = CASE WHEN COALESCE(feed_items.quote_channel_id, '') = '' THEN excluded.quote_channel_id ELSE feed_items.quote_channel_id END,
				quote_body_text = CASE
					WHEN COALESCE(feed_items.quote_body_text, '') = '' THEN COALESCE(excluded.quote_body_text, feed_items.quote_body_text)
					ELSE feed_items.quote_body_text
				END,
				quote_lang = CASE
					WHEN COALESCE(feed_items.quote_lang, '') = '' THEN COALESCE(excluded.quote_lang, feed_items.quote_lang)
					WHEN excluded.quote_lang IS NOT NULL
						AND excluded.quote_lang != ''
						AND (
							feed_items.quote_lang IS NULL
							OR feed_items.quote_lang = ''
							OR LOWER(feed_items.quote_lang) IN ('und','unknown','qam','qct','qht','qme','qst','zxx')
							OR (LOWER(feed_items.quote_lang) GLOB 'q??' AND length(feed_items.quote_lang) = 3)
						) THEN excluded.quote_lang
					ELSE feed_items.quote_lang
				END,
				quote_media_json = CASE
					WHEN COALESCE(feed_items.quote_media_json, '') IN ('', '[]') THEN COALESCE(excluded.quote_media_json, feed_items.quote_media_json)
					ELSE feed_items.quote_media_json
				END,
				views = COALESCE(excluded.views, feed_items.views),
				likes = COALESCE(excluded.likes, feed_items.likes),
				retweets = COALESCE(excluded.retweets, feed_items.retweets),
				fetched_at = CASE
					WHEN COALESCE(feed_items.fetched_at, 0) > 0 THEN feed_items.fetched_at
					ELSE excluded.fetched_at
				END,
				content_hash = COALESCE(excluded.content_hash, feed_items.content_hash),
				canonical_tweet_id = CASE
					WHEN COALESCE(excluded.is_retweet, 0) = 1
						AND COALESCE(excluded.quote_tweet_id, '') = ''
						AND COALESCE(excluded.canonical_tweet_id, '') != ''
						THEN excluded.canonical_tweet_id
					WHEN COALESCE(feed_items.canonical_tweet_id, '') = '' THEN excluded.canonical_tweet_id
					ELSE feed_items.canonical_tweet_id
				END,
				is_reply = CASE
					WHEN excluded.is_reply > 0 THEN excluded.is_reply
					ELSE feed_items.is_reply
				END,
				is_ghost = CASE
					WHEN COALESCE(feed_items.is_ghost,0) > 0 AND COALESCE(excluded.is_ghost,0) = 0 THEN 0
					ELSE feed_items.is_ghost
				END,
				reply_channel_id = CASE
					WHEN excluded.reply_channel_id IS NOT NULL AND excluded.reply_channel_id != '' THEN excluded.reply_channel_id
					ELSE feed_items.reply_channel_id
				END,
				reply_to_status = CASE
					WHEN excluded.reply_to_status IS NOT NULL AND excluded.reply_to_status != '' THEN excluded.reply_to_status
					ELSE feed_items.reply_to_status
				END,
				reposter_channel_id = CASE
					WHEN COALESCE(feed_items.reposter_channel_id, '') = '' THEN excluded.reposter_channel_id
					ELSE feed_items.reposter_channel_id
				END
			WHERE (excluded.source_channel_id IS NOT NULL
			       AND feed_items.source_channel_id IS NOT excluded.source_channel_id)
			   OR (COALESCE(feed_items.channel_id, '') = ''
			       AND feed_items.channel_id IS NOT excluded.channel_id)
			   OR (excluded.body_text IS NOT NULL AND excluded.body_text != ''
			       AND feed_items.body_text IS NOT excluded.body_text)
			   OR (excluded.lang IS NOT NULL AND excluded.lang != ''
			       AND (feed_items.lang IS NULL OR feed_items.lang = ''
			            OR LOWER(feed_items.lang) IN ('und','unknown','qam','qct','qht','qme','qst','zxx')
			            OR (LOWER(feed_items.lang) GLOB 'q??' AND length(feed_items.lang) = 3))
			       AND feed_items.lang IS NOT excluded.lang)
			   OR (excluded.media_json IS NOT NULL
			       AND feed_items.media_json IS NOT excluded.media_json)
			   OR (excluded.canonical_url IS NOT NULL AND excluded.canonical_url != ''
			       AND (COALESCE(feed_items.canonical_url, '') = ''
			            OR LOWER(feed_items.canonical_url) LIKE '%/unknown/status/%'
			            OR LOWER(feed_items.canonical_url) LIKE '%/undefined/status/%')
			       AND feed_items.canonical_url IS NOT excluded.canonical_url)
			   OR (COALESCE(feed_items.quote_tweet_id, '') = ''
			       AND feed_items.quote_tweet_id IS NOT excluded.quote_tweet_id)
			   OR (COALESCE(feed_items.quote_channel_id, '') = ''
			       AND feed_items.quote_channel_id IS NOT excluded.quote_channel_id)
			   OR (COALESCE(feed_items.quote_body_text, '') = ''
			       AND excluded.quote_body_text IS NOT NULL
			       AND feed_items.quote_body_text IS NOT excluded.quote_body_text)
			   OR (excluded.quote_lang IS NOT NULL AND excluded.quote_lang != ''
			       AND (feed_items.quote_lang IS NULL OR feed_items.quote_lang = ''
			            OR LOWER(feed_items.quote_lang) IN ('und','unknown','qam','qct','qht','qme','qst','zxx')
			            OR (LOWER(feed_items.quote_lang) GLOB 'q??' AND length(feed_items.quote_lang) = 3))
			       AND feed_items.quote_lang IS NOT excluded.quote_lang)
			   OR (COALESCE(feed_items.quote_media_json, '') IN ('', '[]')
			       AND excluded.quote_media_json IS NOT NULL
			       AND feed_items.quote_media_json IS NOT excluded.quote_media_json)
			   OR (excluded.views IS NOT NULL AND feed_items.views IS NOT excluded.views)
			   OR (excluded.likes IS NOT NULL AND feed_items.likes IS NOT excluded.likes)
			   OR (excluded.retweets IS NOT NULL AND feed_items.retweets IS NOT excluded.retweets)
			   OR (COALESCE(feed_items.fetched_at, 0) <= 0
			       AND feed_items.fetched_at IS NOT excluded.fetched_at)
			   OR (excluded.content_hash IS NOT NULL
			       AND feed_items.content_hash IS NOT excluded.content_hash)
			   OR (feed_items.canonical_tweet_id IS NOT excluded.canonical_tweet_id
			       AND ((COALESCE(excluded.is_retweet, 0) = 1
			             AND COALESCE(excluded.quote_tweet_id, '') = ''
			             AND COALESCE(excluded.canonical_tweet_id, '') != '')
			            OR COALESCE(feed_items.canonical_tweet_id, '') = ''))
			   OR (excluded.is_reply > 0 AND feed_items.is_reply IS NOT excluded.is_reply)
			   OR (COALESCE(feed_items.is_ghost, 0) > 0
			       AND COALESCE(excluded.is_ghost, 0) = 0)
			   OR (excluded.reply_channel_id IS NOT NULL AND excluded.reply_channel_id != ''
			       AND feed_items.reply_channel_id IS NOT excluded.reply_channel_id)
			   OR (excluded.reply_to_status IS NOT NULL AND excluded.reply_to_status != ''
			       AND feed_items.reply_to_status IS NOT excluded.reply_to_status)
			   OR (COALESCE(feed_items.reposter_channel_id, '') = ''
			       AND feed_items.reposter_channel_id IS NOT excluded.reposter_channel_id)
			RETURNING tweet_id, COALESCE(body_text, ''), COALESCE(quote_body_text, '')
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = stmt.Close()
		}()

		nowMs := time.Now().UnixMilli()
		enqueueTranslation := func(tweetID, field, sourceText string) error {
			if strings.TrimSpace(sourceText) == "" {
				return nil
			}
			sourceHash := translationSourceHash(sourceText)
			if _, err := invalidateTranslationStmt.Exec(
				tweetID, field, translationTargetLang, sourceHash,
			); err != nil {
				return err
			}
			_, err := translationJobStmt.Exec(
				tweetID, field, translationTargetLang, sourceHash, nowMs, nowMs,
			)
			return err
		}
		for _, item := range normalizedItems {
			pubMs := timePtrToMillis(item.PublishedAt)
			quotePubMs := timePtrToMillis(item.QuotePublishedAt)
			row := stmt.QueryRow(
				item.TweetID,
				nilIfEmpty(item.SourceChannelID),
				nilIfEmpty(item.ChannelID),
				nilIfEmpty(item.BodyText),
				nilIfEmpty(item.Lang),
				boolToInt(item.IsRetweet),
				nilIfEmpty(item.QuoteTweetID),
				nilIfEmpty(item.QuoteChannelID),
				nilIfEmpty(item.QuoteBodyText),
				nilIfEmpty(item.QuoteLang),
				nilIfEmpty(item.QuoteMediaJSON),
				nilIfEmpty(item.MediaJSON),
				nilIfEmpty(item.CanonicalURL),
				nilIfEmpty(item.ReplyChannelID),
				nilIfEmpty(item.ReplyToStatus),
				boolToInt(item.IsReply),
				boolToInt(item.IsGhost),
				nilIfEmpty(item.ReposterChannelID),
				quotePubMs,
				nilIfZero(item.Views),
				nilIfZero(item.Likes),
				nilIfZero(item.Retweets),
				pubMs,
				nowMs,
				nilIfEmpty(item.ContentHash),
				nilIfEmpty(item.CanonicalTweetID),
			)
			var storedTweetID, finalBodyText, finalQuoteBodyText string
			err := row.Scan(&storedTweetID, &finalBodyText, &finalQuoteBodyText)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			result.Processed++
			if err == sql.ErrNoRows {
				continue
			}
			if err := enqueueTranslation(storedTweetID, "body", finalBodyText); err != nil {
				return err
			}
			if err := enqueueTranslation(storedTweetID, "quote", finalQuoteBodyText); err != nil {
				return err
			}
		}

		for _, observation := range collectFeedProfileObservations(normalizedItems, nowMs) {
			if err := observeProfileTx(tx, observation); err != nil {
				return err
			}
		}
		for _, item := range normalizedItems {
			changed, err := declareXContentAssetsTx(tx, item, nowMs)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			channelID := xMediaRetentionChannelID(item)
			if channelID == "" {
				continue
			}
			if changedByChannel[channelID] == nil {
				changedByChannel[channelID] = make(map[string]struct{})
			}
			changedByChannel[channelID][item.TweetID] = struct{}{}
		}

		// --- Populate retweet_sources for retweet items ---
		rtStmt, err := tx.Prepare(`
			INSERT INTO retweet_sources
				(content_hash, retweeter_channel_id, tweet_id, published_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(content_hash, retweeter_channel_id) DO UPDATE SET
				tweet_id = excluded.tweet_id,
				published_at = excluded.published_at
			WHERE retweet_sources.tweet_id IS NOT excluded.tweet_id
			   OR retweet_sources.published_at IS NOT excluded.published_at
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = rtStmt.Close()
		}()

		hashSet := make(map[string]bool)
		for _, item := range normalizedItems {
			if item.ContentHash == "" || !item.IsRetweet {
				continue
			}
			hashSet[item.ContentHash] = true
			if item.ReposterChannelID == "" {
				continue
			}
			if _, err := rtStmt.Exec(
				item.ContentHash, item.ReposterChannelID,
				item.TweetID, timePtrToMillis(item.PublishedAt),
			); err != nil {
				return err
			}
		}

		// --- Set canonical_tweet_id for dedup ---
		// For each content_hash in this batch, use the earliest original when
		// one is stored. Otherwise a pure repost keeps its parsed target ID.
		if len(hashSet) > 0 {
			hashes := make([]string, 0, len(hashSet))
			for h := range hashSet {
				hashes = append(hashes, h)
			}
			ph := strings.Repeat("?,", len(hashes))
			ph = ph[:len(ph)-1]
			var hashArgs []any
			for _, h := range hashes {
				hashArgs = append(hashArgs, h)
			}
			_, err := tx.Exec(`
				WITH desired AS MATERIALIZED (
					SELECT item.tweet_id,
					       item.canonical_tweet_id AS stored_canonical,
					       COALESCE((
						SELECT f2.tweet_id FROM feed_items f2
						WHERE f2.content_hash = item.content_hash
						  AND f2.content_hash != ''
						  AND COALESCE(f2.is_retweet, 0) = 0
						ORDER BY f2.published_at ASC, f2.tweet_id ASC
						LIMIT 1
					       ), NULLIF(item.canonical_tweet_id, ''), item.tweet_id) AS desired_canonical
					FROM feed_items item
					WHERE item.content_hash IN (`+ph+`)
					  AND item.content_hash != ''
				)
				UPDATE feed_items
				SET canonical_tweet_id = (
					SELECT desired_canonical FROM desired
					WHERE desired.tweet_id = feed_items.tweet_id
				)
				WHERE tweet_id IN (
					SELECT tweet_id FROM desired
					WHERE stored_canonical IS NOT desired_canonical
				)
			`, hashArgs...)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return FeedUpsertResult{}, err
	}
	if len(changedByChannel) > 0 {
		result.XMediaRetentionChanges = make(map[string][]string, len(changedByChannel))
		for channelID, tweetIDs := range changedByChannel {
			result.XMediaRetentionChanges[channelID] = sortedKeys(tweetIDs)
		}
	}
	return result, nil
}

func xMediaRetentionChannelID(item model.FeedItem) string {
	if item.IsRetweet && item.ReposterChannelID != "" {
		return item.ReposterChannelID
	}
	if item.SourceChannelID != "" {
		return item.SourceChannelID
	}
	return item.ChannelID
}

func normalizeFeedItemIdentity(item model.FeedItem) model.FeedItem {
	author := model.EffectiveTwitterAuthorHandle(item.AuthorHandle, item.SourceHandle, item.IsRetweet)
	if author == item.AuthorHandle {
		return item
	}
	item.AuthorHandle = author
	if shouldRewritePlaceholderXStatusURL(item.CanonicalURL) {
		statusID := strings.TrimSpace(item.CanonicalTweetID)
		if statusID == "" {
			statusID = strings.TrimSpace(item.TweetID)
		}
		if statusID != "" {
			item.CanonicalURL = "https://x.com/" + strings.TrimPrefix(author, "@") + "/status/" + statusID
		}
	}
	return item
}

func assignFeedRoleIdentities(item *model.FeedItem) {
	if item == nil {
		return
	}
	item.SourceChannelID = model.TwitterChannelIDFromHandle(item.SourceHandle)
	item.ChannelID = model.TwitterChannelIDFromHandle(item.AuthorHandle)
	item.QuoteChannelID = model.TwitterChannelIDFromHandle(item.QuoteAuthorHandle)
	item.ReplyChannelID = model.TwitterChannelIDFromHandle(item.ReplyToHandle)
	item.ReposterChannelID = ""
	if !item.IsRetweet {
		return
	}
	reposterHandle := item.RetweetedByHandle
	if model.IsPlaceholderTwitterHandle(reposterHandle) {
		reposterHandle = item.SourceHandle
	}
	item.ReposterChannelID = model.TwitterChannelIDFromHandle(reposterHandle)
}

func collectFeedProfileObservations(items []model.FeedItem, observedAt int64) []profileObservation {
	byChannel := make(map[string]profileObservation)
	add := func(channelID, handle, displayName, avatarURL string, seenAt int64) {
		if channelID == "" {
			return
		}
		if seenAt <= 0 {
			seenAt = observedAt
		}
		observation := byChannel[channelID]
		if observation.observedAt > seenAt {
			return
		}
		observation.channelID = channelID
		observation.platform = "twitter"
		observation.handle = handle
		if displayName = strings.TrimSpace(displayName); displayName != "" {
			observation.displayName = displayName
		}
		if model.IsRawTwitterProfileAvatar(avatarURL) {
			observation.avatarURL = strings.TrimSpace(avatarURL)
		}
		observation.observedAt = seenAt
		byChannel[channelID] = observation
	}
	for _, item := range items {
		seenAt := feedIdentityObservedAtMs(item)
		add(item.SourceChannelID, item.SourceHandle, "", "", seenAt)
		add(item.ChannelID, item.AuthorHandle, item.AuthorDisplayName, item.AuthorAvatarURL, seenAt)
		add(item.QuoteChannelID, item.QuoteAuthorHandle, item.QuoteAuthorDisplayName, item.QuoteAuthorAvatarURL, seenAt)
		add(item.ReplyChannelID, item.ReplyToHandle, "", "", seenAt)
		if item.ReposterChannelID != "" {
			handle := item.RetweetedByHandle
			if model.IsPlaceholderTwitterHandle(handle) {
				handle = item.SourceHandle
			}
			add(item.ReposterChannelID, handle, item.RetweetedByDisplayName, "", seenAt)
		}
		for _, body := range []string{item.BodyText, item.QuoteBodyText} {
			for _, mention := range model.LinkableTwitterMentions(body) {
				add(model.TwitterChannelIDFromHandle(mention.Handle), mention.Handle, "", "", seenAt)
			}
		}
	}
	observations := make([]profileObservation, 0, len(byChannel))
	for _, observation := range byChannel {
		observations = append(observations, observation)
	}
	return observations
}

func feedIdentityObservedAtMs(item model.FeedItem) int64 {
	var observedAt int64
	if item.PublishedAt != nil {
		observedAt = item.PublishedAt.UnixMilli()
	}
	if !item.FetchedAt.IsZero() && item.FetchedAt.UnixMilli() > observedAt {
		observedAt = item.FetchedAt.UnixMilli()
	}
	return observedAt
}

func shouldRewritePlaceholderXStatusURL(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	return normalized == "" ||
		strings.Contains(normalized, "://x.com/unknown/status/") ||
		strings.Contains(normalized, "://twitter.com/unknown/status/") ||
		strings.Contains(normalized, "://fxtwitter.com/unknown/status/") ||
		strings.Contains(normalized, "://x.com/undefined/status/") ||
		strings.Contains(normalized, "://twitter.com/undefined/status/") ||
		strings.Contains(normalized, "://fxtwitter.com/undefined/status/")
}

// GetLatestFeedItem returns the most recently published feed item, or nil if none.
func (db *DB) GetLatestFeedItem() (*model.FeedItem, error) {
	rows, err := db.reader().Query(`
		SELECT ` + feedItemSelectSQL("feed_items") + `
		FROM feed_items_resolved AS feed_items
		WHERE ` + feedPrimaryItemPredicate("feed_items") + `
		  AND ` + feedActiveOwnerPredicate("feed_items") + `
		ORDER BY published_at DESC, tweet_id DESC
		LIMIT 1
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	items, err := scanFeedItems(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

// GetLatestFetchedFeedItem returns the most recently fetched feed item, or nil if none.
func (db *DB) GetLatestFetchedFeedItem() (*model.FeedItem, error) {
	rows, err := db.reader().Query(`
		SELECT ` + feedItemSelectSQL("feed_items") + `
		FROM feed_items_resolved AS feed_items
		WHERE ` + feedPrimaryItemPredicate("feed_items") + `
		  AND ` + feedActiveOwnerPredicate("feed_items") + `
		  AND (canonical_tweet_id IS NULL OR canonical_tweet_id = '' OR canonical_tweet_id = tweet_id)
		  AND ` + retweetFilterClause("feed_items") + `
		ORDER BY fetched_at DESC, tweet_id DESC
		LIMIT 1
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	items, err := scanFeedItems(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func (db *DB) GetLatestFetchedFeedItemID() (string, error) {
	var tweetID string
	err := db.reader().QueryRow(`
		SELECT tweet_id
		FROM feed_items
		WHERE ` + feedPrimaryItemPredicate("feed_items") + `
		  AND ` + feedActiveOwnerPredicate("feed_items") + `
		  AND (canonical_tweet_id IS NULL OR canonical_tweet_id = '' OR canonical_tweet_id = tweet_id)
		  AND ` + retweetFilterClause("feed_items") + `
		ORDER BY fetched_at DESC, tweet_id DESC
		LIMIT 1
	`).Scan(&tweetID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tweetID, err
}

// GetNewPosterAvatars returns up to `limit` unique new posters for the "new
// posts" bar avatar stack. The candidate set is feed_items with
// fetched_at > knownHeadTweetID.fetched_at, excluding muted accounts.
//
// Ranking: snapshot winners first (ordered by final_score DESC), then
// newly fetched fill (fetched_at DESC) for authors not yet represented.
// Dedupes by lower(author_handle). Avatar URLs use the canonical local profile
// asset endpoint.
//
// Returns an empty slice when knownHeadTweetID is empty or not found.
func (db *DB) GetNewPosterAvatars(knownHeadTweetID string, limit int) ([]model.NewPosterAvatar, error) {
	if limit <= 0 || knownHeadTweetID == "" {
		return nil, nil
	}

	var knownFetchedAt sql.NullInt64
	if err := db.reader().QueryRow(
		"SELECT fetched_at FROM feed_items WHERE tweet_id = ? LIMIT 1",
		knownHeadTweetID,
	).Scan(&knownFetchedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !knownFetchedAt.Valid {
		return nil, nil
	}

	muted, _ := db.GetMutedChannelIDs()
	muteClauses, muteArgs := buildMuteClauses(muted)
	muteSQL := ""
	for _, c := range muteClauses {
		muteSQL += " AND " + c
	}

	out := make([]model.NewPosterAvatar, 0, limit)
	seen := make(map[string]bool, limit)

	appendRow := func(channelID, handle, display string) {
		if channelID == "" || handle == "" {
			return
		}
		if seen[channelID] {
			return
		}
		seen[channelID] = true
		out = append(out, model.NewPosterAvatar{
			AuthorHandle:      handle,
			AuthorDisplayName: display,
			AuthorAvatarURL:   "/api/media/avatar/" + channelID,
		})
	}
	const profileJoin = `LEFT JOIN channel_profiles cp
		ON cp.channel_id = fi.channel_id
		AND cp.tombstone = 0`

	// Primary: ranked by snapshot final_score.
	snapQuery := `
			SELECT fi.channel_id, COALESCE(cp.handle,''), COALESCE(cp.display_name,'')
			FROM feed_rank_snapshot s
			JOIN feed_items fi ON fi.tweet_id = s.tweet_id
			` + profileJoin + `
			WHERE fi.fetched_at > ?
			  AND ` + feedPrimaryItemPredicate("fi") + `
			  AND ` + feedActiveOwnerPredicate("fi") + muteSQL + `
			ORDER BY s.final_score DESC
		`
	snapArgs := append([]any{knownFetchedAt.Int64}, muteArgs...)
	rows, err := db.reader().Query(snapQuery, snapArgs...)
	if err == nil {
		for rows.Next() && len(out) < limit {
			var channelID, handle, display string
			if err := rows.Scan(&channelID, &handle, &display); err != nil {
				break
			}
			appendRow(channelID, handle, display)
		}
		_ = rows.Close()
	}

	// Supplement: fill remaining slots from recency.
	if len(out) < limit {
		recentQuery := `
			SELECT fi.channel_id, COALESCE(cp.handle,''), COALESCE(cp.display_name,'')
			FROM feed_items fi
			` + profileJoin + `
			WHERE fi.fetched_at > ?
			  AND ` + feedPrimaryItemPredicate("fi") + `
			  AND ` + feedActiveOwnerPredicate("fi") + muteSQL + `
			ORDER BY fi.fetched_at DESC, fi.tweet_id DESC
			LIMIT 50
		`
		recentArgs := append([]any{knownFetchedAt.Int64}, muteArgs...)
		rows, err := db.reader().Query(recentQuery, recentArgs...)
		if err == nil {
			for rows.Next() && len(out) < limit {
				var channelID, handle, display string
				if err := rows.Scan(&channelID, &handle, &display); err != nil {
					break
				}
				appendRow(channelID, handle, display)
			}
			_ = rows.Close()
		}
	}

	return out, nil
}

// buildMuteClauses returns SQL fragments that filter persisted channel identities.
func buildMuteClauses(muted []string) ([]string, []any) {
	if len(muted) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(muted))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(muted)*2)
	for _, channelID := range muted {
		args = append(args, channelID)
	}
	for _, channelID := range muted {
		args = append(args, channelID)
	}
	return []string{
		"fi.channel_id NOT IN (" + placeholders + ")",
		"COALESCE(fi.source_channel_id,'') NOT IN (" + placeholders + ")",
	}, args
}

// CountFeedItems returns the total number of rows in feed_items.
func (db *DB) CountFeedItems() (int, error) {
	var count int
	err := db.reader().QueryRow("SELECT COUNT(*) FROM feed_items").Scan(&count)
	return count, err
}

// ListFeedItemsFiltered returns feed items with cursor pagination, optional
// handle filter, and optional seen suppression.
func (db *DB) ListFeedItemsFiltered(limit int, cursor *model.FeedCursor, sourceHandle string, excludeSeen bool) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 40
	}

	var where []string
	var args []any
	where = append(where, feedPrimaryItemPredicate("feed_items"))
	where = append(where, feedActiveOwnerPredicate("feed_items"))

	muted, _ := db.GetMutedChannelIDs()
	if len(muted) > 0 {
		placeholders := strings.Repeat("?,", len(muted))
		placeholders = placeholders[:len(placeholders)-1]
		where = append(where, "channel_id NOT IN ("+placeholders+")")
		for _, h := range muted {
			args = append(args, h)
		}
		where = append(where, "COALESCE(source_channel_id,'') NOT IN ("+placeholders+")")
		for _, h := range muted {
			args = append(args, h)
		}
	}

	if sourceHandle != "" {
		sourceChannelID := model.TwitterChannelIDFromHandle(sourceHandle)
		if sourceChannelID == "" {
			return nil, nil
		}
		where = append(where, "source_channel_id = ?")
		args = append(args, sourceChannelID)
	}

	if excludeSeen {
		where = append(where, feedUnseenPredicate("feed_items"))
	}

	if cursor != nil && cursor.BeforePublishedAtMs > 0 && cursor.BeforeTweetID != "" {
		where = append(where, "(published_at < ? OR (published_at = ? AND tweet_id < ?))")
		args = append(args, cursor.BeforePublishedAtMs, cursor.BeforePublishedAtMs, cursor.BeforeTweetID)
	}

	where = append(where, "(canonical_tweet_id IS NULL OR canonical_tweet_id = '' OR canonical_tweet_id = tweet_id)")

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := `
		SELECT ` + feedItemSelectSQL("feed_items") + `
		FROM feed_items_resolved AS feed_items
		` + whereClause + `
		ORDER BY published_at DESC, tweet_id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanFeedItems(rows)
}

// ListFeedItemsBySourceID returns feed items tagged through feed_item_sources.
// It is used by source-scoped views where source_handle cannot represent the
// full set, such as X list/community feeds or local ingest demos.
func (db *DB) ListFeedItemsBySourceID(sourceID string, limit int) ([]model.FeedItem, error) {
	if sourceID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 40
	}

	rows, err := db.reader().Query(`
		SELECT `+feedItemSelectSQL("f")+`
		FROM feed_item_sources fis
		JOIN feed_items_resolved f ON f.tweet_id = fis.tweet_id
		WHERE fis.source_id = ?
		  AND `+feedPrimaryItemPredicate("f")+`
		ORDER BY f.published_at DESC, f.tweet_id DESC
		LIMIT ?
	`, sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanFeedItems(rows)
}

// GetBookmarkedFeedItems returns feed items that are bookmarked, with cursor pagination.
func (db *DB) GetBookmarkedFeedItems(limit int, cursor *model.FeedCursor) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 40
	}

	var where []string
	var args []any

	where = append(where, "b.video_id IS NOT NULL")

	if cursor != nil && cursor.BeforePublishedAtMs > 0 && cursor.BeforeTweetID != "" {
		where = append(where, "(b.bookmarked_at < ? OR (b.bookmarked_at = ? AND f.tweet_id < ?))")
		args = append(args, cursor.BeforePublishedAtMs, cursor.BeforePublishedAtMs, cursor.BeforeTweetID)
	}

	whereClause := "WHERE " + strings.Join(where, " AND ")

	query := `
		SELECT ` + feedItemSelectSQL("f") + `
		FROM feed_items_resolved f
		INNER JOIN bookmarks b ON b.video_id = f.tweet_id
		` + whereClause + `
		ORDER BY b.bookmarked_at DESC, f.tweet_id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanFeedItems(rows)
}

// CountBookmarkedFeedItems returns the count of bookmarked feed items.
func (db *DB) CountBookmarkedFeedItems() (int, error) {
	var count int
	err := db.reader().QueryRow("SELECT COUNT(*) FROM bookmarks b INNER JOIN feed_items f ON f.tweet_id = b.video_id").Scan(&count)
	return count, err
}

// CountFeedLikes returns the count of liked feed items for a user.
func (db *DB) CountFeedLikes() (int, error) {
	var count int
	err := db.reader().QueryRow("SELECT COUNT(*) FROM feed_likes").Scan(&count)
	return count, err
}
