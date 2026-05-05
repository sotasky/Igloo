package db

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// ListFeedItemsPage returns feed items with cursor-based keyset pagination.
// When username is non-empty and cursor is nil (first page), seen items are
// excluded so the ranking pool contains only unseen content.
func (db *DB) ListFeedItemsPage(limit int, cursor *model.FeedCursor, username string) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 40
	}

	var where []string
	var args []any

	muted, _ := db.GetMutedAccounts()
	if len(muted) > 0 {
		placeholders := strings.Repeat("?,", len(muted))
		placeholders = placeholders[:len(placeholders)-1]
		where = append(where, "author_handle NOT IN ("+placeholders+")")
		for _, h := range muted {
			args = append(args, h)
		}
		where = append(where, "COALESCE(source_handle,'') NOT IN ("+placeholders+")")
		for _, h := range muted {
			args = append(args, h)
		}
	}

	if cursor != nil && cursor.BeforePublishedAtMs > 0 && cursor.BeforeTweetID != "" {
		where = append(where, "(published_at < ? OR (published_at = ? AND tweet_id < ?))")
		args = append(args, cursor.BeforePublishedAtMs, cursor.BeforePublishedAtMs, cursor.BeforeTweetID)
	}

	// Exclude seen items on every page so the scroll tail is genuinely unseen content.
	if username != "" {
		where = append(where, feedUnseenPredicate("feed_items"))
		args = append(args, feedUnseenPredicateArgs(username)...)
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
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name,''), COALESCE(author_avatar_url,''),
		       COALESCE(body_text,''), COALESCE(lang,''),
		       COALESCE(is_retweet,0), COALESCE(retweeted_by_handle,''),
		       COALESCE(retweeted_by_display_name,''),
		       COALESCE(quote_tweet_id,''), COALESCE(quote_author_handle,''),
		       COALESCE(quote_author_display_name,''), COALESCE(quote_author_avatar_url,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_lang,''),
		       COALESCE(quote_media_json,''), COALESCE(media_json,''),
		       COALESCE(canonical_url,''), COALESCE(reply_to_handle,''),
		       COALESCE(reply_to_status,''),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       quote_published_at,
		       COALESCE(views,0), COALESCE(likes,0), COALESCE(retweets,0),
		       published_at, fetched_at,
		       COALESCE(content_hash,''), COALESCE(canonical_tweet_id,'')
		FROM feed_items
		` + whereClause + `
		ORDER BY published_at DESC, tweet_id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeedItems(rows)
}

// ListFeedItemsSince returns feed items with sync_seq > afterSeq, ordered by sync_seq ASC.
// This is the delta sync query — Android calls it with the last sync_seq it received.
// Applies the same retweet/quote filter the web feed uses so Android never sees
// items hidden by a channel's x_include_retweets=0 setting.
func (db *DB) ListFeedItemsSince(afterSeq int64, limit int) ([]model.FeedItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	query := `
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name,''), COALESCE(author_avatar_url,''),
		       COALESCE(body_text,''), COALESCE(lang,''),
		       COALESCE(is_retweet,0), COALESCE(retweeted_by_handle,''),
		       COALESCE(retweeted_by_display_name,''),
		       COALESCE(quote_tweet_id,''), COALESCE(quote_author_handle,''),
		       COALESCE(quote_author_display_name,''), COALESCE(quote_author_avatar_url,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_lang,''),
		       COALESCE(quote_media_json,''), COALESCE(media_json,''),
		       COALESCE(canonical_url,''), COALESCE(reply_to_handle,''),
		       COALESCE(reply_to_status,''),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       quote_published_at,
		       COALESCE(views,0), COALESCE(likes,0), COALESCE(retweets,0),
		       published_at, fetched_at,
		       COALESCE(content_hash,''), COALESCE(canonical_tweet_id,''),
		       COALESCE(sync_seq,0)
		FROM feed_items
		WHERE sync_seq > ?
		  AND ` + retweetFilterClause("feed_items") + `
		ORDER BY sync_seq ASC
		LIMIT ?
	`
	rows, err := db.conn.Query(query, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeedItemsWithSeq(rows)
}

// scanFeedItemsWithSeq scans feed item rows that include sync_seq as the last column.
func scanFeedItemsWithSeq(rows *sql.Rows) ([]model.FeedItem, error) {
	var items []model.FeedItem
	for rows.Next() {
		var f model.FeedItem
		var quotePubAt, pubAt, fetchedAt sql.NullInt64
		err := rows.Scan(
			&f.TweetID, &f.SourceHandle, &f.AuthorHandle,
			&f.AuthorDisplayName, &f.AuthorAvatarURL,
			&f.BodyText, &f.Lang,
			&f.IsRetweet, &f.RetweetedByHandle,
			&f.RetweetedByDisplayName,
			&f.QuoteTweetID, &f.QuoteAuthorHandle,
			&f.QuoteAuthorDisplayName, &f.QuoteAuthorAvatarURL,
			&f.QuoteBodyText, &f.QuoteLang,
			&f.QuoteMediaJSON, &f.MediaJSON,
			&f.CanonicalURL, &f.ReplyToHandle,
			&f.ReplyToStatus,
			&f.IsReply, &f.IsGhost,
			&quotePubAt,
			&f.Views, &f.Likes, &f.Retweets,
			&pubAt, &fetchedAt,
			&f.ContentHash, &f.CanonicalTweetID,
			&f.SyncSeq,
		)
		if err != nil {
			return nil, err
		}
		f.QuotePublishedAt = millisToTimePtr(quotePubAt)
		f.PublishedAt = millisToTimePtr(pubAt)
		if t := millisToTimePtr(fetchedAt); t != nil {
			f.FetchedAt = *t
		}
		f.ParseMedia()
		items = append(items, f)
	}
	return items, rows.Err()
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

	rows, err := db.conn.Query(`
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name,''), COALESCE(author_avatar_url,''),
		       COALESCE(body_text,''), COALESCE(lang,''),
		       COALESCE(is_retweet,0), COALESCE(retweeted_by_handle,''),
		       COALESCE(retweeted_by_display_name,''),
		       COALESCE(quote_tweet_id,''), COALESCE(quote_author_handle,''),
		       COALESCE(quote_author_display_name,''), COALESCE(quote_author_avatar_url,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_lang,''),
		       COALESCE(quote_media_json,''), COALESCE(media_json,''),
		       COALESCE(canonical_url,''), COALESCE(reply_to_handle,''),
		       COALESCE(reply_to_status,''),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       quote_published_at,
		       COALESCE(views,0), COALESCE(likes,0), COALESCE(retweets,0),
		       published_at, fetched_at,
		       COALESCE(content_hash,''), COALESCE(canonical_tweet_id,'')
		FROM feed_items
		WHERE tweet_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// GetSeenTweetIDs returns which tweet IDs have been seen by username.
func (db *DB) GetSeenTweetIDs(username string, tweetIDs []string) (map[string]bool, error) {
	if len(tweetIDs) == 0 || username == "" {
		return make(map[string]bool), nil
	}
	placeholders := strings.Repeat("?,", len(tweetIDs))
	placeholders = placeholders[:len(placeholders)-1]

	args := []any{username}
	for _, id := range tweetIDs {
		args = append(args, id)
	}

	rows, err := db.conn.Query(
		"SELECT tweet_id FROM feed_seen WHERE username = ? AND tweet_id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// GetFeedLikesForTweetIDs returns which tweet IDs are liked by username.
func (db *DB) GetFeedLikesForTweetIDs(username string, tweetIDs []string) (map[string]bool, error) {
	if len(tweetIDs) == 0 || username == "" {
		return make(map[string]bool), nil
	}
	placeholders := strings.Repeat("?,", len(tweetIDs))
	placeholders = placeholders[:len(placeholders)-1]

	args := []any{username}
	for _, id := range tweetIDs {
		args = append(args, id)
	}

	rows, err := db.conn.Query(
		"SELECT tweet_id FROM feed_likes WHERE username = ? AND tweet_id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	rows, err := db.conn.Query(
		"SELECT DISTINCT video_id FROM bookmarks WHERE video_id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	rows, err := db.conn.Query(
		`SELECT video_id, category_id, custom_title, account_handles, media_indices,
		        COALESCE(bookmarked_at, 0)
		   FROM bookmarks
		  WHERE video_id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// GetMutedAccounts returns all muted account handles.
func (db *DB) GetMutedAccounts() ([]string, error) {
	rows, err := db.conn.Query("SELECT handle FROM muted_accounts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
func (db *DB) GetFeedLikedPage(username string, limit int, cursor *model.FeedCursor) ([]model.FeedLike, error) {
	if limit <= 0 {
		limit = 40
	}

	var where []string
	var args []any

	where = append(where, "username = ?")
	args = append(args, username)

	if cursor != nil && cursor.BeforePublishedAtMs > 0 && cursor.BeforeTweetID != "" {
		where = append(where, "(liked_at < ? OR (liked_at = ? AND tweet_id < ?))")
		args = append(args, cursor.BeforePublishedAtMs, cursor.BeforePublishedAtMs, cursor.BeforeTweetID)
	}

	whereClause := "WHERE " + strings.Join(where, " AND ")

	rows, err := db.conn.Query(`
		SELECT username, tweet_id, liked_at,
		       COALESCE(source_handle,''), COALESCE(author_handle,''),
		       COALESCE(author_display_name,''), COALESCE(link,''),
		       COALESCE(canonical_x_link,''), COALESCE(body_text,''),
		       published_at, COALESCE(media_url,''), COALESCE(avatar_url,''),
		       COALESCE(media_json,''), COALESCE(platform,''),
		       COALESCE(quote_payload_json,'')
		FROM feed_likes
		`+whereClause+`
		ORDER BY liked_at DESC, tweet_id DESC
		LIMIT ?
	`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var likes []model.FeedLike
	for rows.Next() {
		var l model.FeedLike
		var likedAt, publishedAt sql.NullInt64
		err := rows.Scan(
			&l.Username, &l.TweetID, &likedAt,
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

// GetFeedMediaJobs returns media job status for tweet IDs.
func (db *DB) GetFeedMediaJobs(tweetIDs []string) (map[string]model.FeedMediaJob, error) {
	if len(tweetIDs) == 0 {
		return make(map[string]model.FeedMediaJob), nil
	}
	placeholders := strings.Repeat("?,", len(tweetIDs))
	placeholders = placeholders[:len(placeholders)-1]

	var args []any
	for _, id := range tweetIDs {
		args = append(args, id)
	}

	rows, err := db.conn.Query(`
		SELECT tweet_id, COALESCE(status,''), COALESCE(media_kind,'unknown'),
		       COALESCE(slide_count,0)
		FROM feed_media_jobs
		WHERE tweet_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make(map[string]model.FeedMediaJob)
	for rows.Next() {
		var j model.FeedMediaJob
		if err := rows.Scan(&j.TweetID, &j.Status, &j.MediaKind, &j.SlideCount); err != nil {
			return nil, err
		}
		jobs[j.TweetID] = j
	}
	return jobs, rows.Err()
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

	rows, err := db.conn.Query(`
		SELECT content_hash, retweeter_handle, COALESCE(retweeter_display_name,''),
		       tweet_id
		FROM retweet_sources
		WHERE content_hash IN (`+placeholders+`)
		ORDER BY content_hash, published_at DESC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]model.RetweeterInfo)
	for rows.Next() {
		var hash, handle, displayName, tweetID string
		if err := rows.Scan(&hash, &handle, &displayName, &tweetID); err != nil {
			return nil, err
		}
		_ = tweetID
		normHandle := strings.ToLower(strings.TrimPrefix(handle, "@"))
		result[hash] = append(result[hash], model.RetweeterInfo{
			Handle:      handle,
			DisplayName: displayName,
			ChannelID:   "twitter_" + normHandle,
			AvatarURL:   "/api/media/avatar/twitter_" + normHandle,
		})
	}
	return result, rows.Err()
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

	rows, err := db.conn.Query(`
		SELECT video_id, COALESCE(channel_id,''), COALESCE(title,''),
		       COALESCE(file_path,''), COALESCE(thumbnail_path,''),
		       COALESCE(metadata_json,'')
		FROM videos
		WHERE video_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]model.Video)
	for rows.Next() {
		var v model.Video
		if err := rows.Scan(&v.VideoID, &v.ChannelID, &v.Title, &v.FilePath, &v.ThumbnailPath, &v.MetadataJSON); err != nil {
			return nil, err
		}
		result[v.VideoID] = v
	}
	return result, rows.Err()
}

// GetDisplayNamesForHandles returns a map of handle → display name for the given
// handles, using the most recently published feed item that has a non-empty display name.
func (db *DB) GetDisplayNamesForHandles(handles []string) (map[string]string, error) {
	if len(handles) == 0 {
		return make(map[string]string), nil
	}
	ph := strings.Repeat("?,", len(handles))
	ph = ph[:len(ph)-1]

	var args []any
	for _, h := range handles {
		args = append(args, h)
	}

	// Also check quote_author_display_name as a fallback for handles that
	// only appear as quote authors (e.g. retweeted accounts whose original
	// author_display_name is empty in the RSS feed).
	var allArgs []any
	allArgs = append(allArgs, args...)
	allArgs = append(allArgs, args...)
	rows, err := db.conn.Query(`
		SELECT handle, display_name FROM (
			SELECT author_handle AS handle, author_display_name AS display_name
			FROM feed_items
			WHERE author_handle IN (`+ph+`)
			  AND author_display_name IS NOT NULL AND author_display_name != ''
			UNION ALL
			SELECT quote_author_handle AS handle, quote_author_display_name AS display_name
			FROM feed_items
			WHERE quote_author_handle IN (`+ph+`)
			  AND quote_author_display_name IS NOT NULL AND quote_author_display_name != ''
		)
		GROUP BY handle
	`, allArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var handle, name string
		if err := rows.Scan(&handle, &name); err != nil {
			return nil, err
		}
		result[handle] = name
	}
	return result, rows.Err()
}

// GetFeedItemsByAuthor returns feed items by author or source handle, newest first.
func (db *DB) GetFeedItemsByAuthor(handle string, limit int) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 40
	}
	rows, err := db.conn.Query(`
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name,''), COALESCE(author_avatar_url,''),
		       COALESCE(body_text,''), COALESCE(lang,''),
		       COALESCE(is_retweet,0), COALESCE(retweeted_by_handle,''),
		       COALESCE(retweeted_by_display_name,''),
		       COALESCE(quote_tweet_id,''), COALESCE(quote_author_handle,''),
		       COALESCE(quote_author_display_name,''), COALESCE(quote_author_avatar_url,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_lang,''),
		       COALESCE(quote_media_json,''), COALESCE(media_json,''),
		       COALESCE(canonical_url,''), COALESCE(reply_to_handle,''),
		       COALESCE(reply_to_status,''),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       quote_published_at,
		       COALESCE(views,0), COALESCE(likes,0), COALESCE(retweets,0),
		       published_at, fetched_at,
		       COALESCE(content_hash,''), COALESCE(canonical_tweet_id,'')
		FROM feed_items
		WHERE LOWER(author_handle) = LOWER(?) OR LOWER(source_handle) = LOWER(?) OR LOWER(quote_author_handle) = LOWER(?)
		ORDER BY published_at DESC
		LIMIT ?
	`, handle, handle, handle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeedItems(rows)
}

// scanFeedItems scans rows into FeedItem structs.
func scanFeedItems(rows *sql.Rows) ([]model.FeedItem, error) {
	var items []model.FeedItem
	for rows.Next() {
		var f model.FeedItem
		var quotePubAt, pubAt, fetchedAt sql.NullInt64
		err := rows.Scan(
			&f.TweetID, &f.SourceHandle, &f.AuthorHandle,
			&f.AuthorDisplayName, &f.AuthorAvatarURL,
			&f.BodyText, &f.Lang,
			&f.IsRetweet, &f.RetweetedByHandle,
			&f.RetweetedByDisplayName,
			&f.QuoteTweetID, &f.QuoteAuthorHandle,
			&f.QuoteAuthorDisplayName, &f.QuoteAuthorAvatarURL,
			&f.QuoteBodyText, &f.QuoteLang,
			&f.QuoteMediaJSON, &f.MediaJSON,
			&f.CanonicalURL, &f.ReplyToHandle,
			&f.ReplyToStatus,
			&f.IsReply, &f.IsGhost,
			&quotePubAt,
			&f.Views, &f.Likes, &f.Retweets,
			&pubAt, &fetchedAt,
			&f.ContentHash, &f.CanonicalTweetID,
		)
		if err != nil {
			return nil, err
		}
		f.QuotePublishedAt = millisToTimePtr(quotePubAt)
		f.PublishedAt = millisToTimePtr(pubAt)
		if t := millisToTimePtr(fetchedAt); t != nil {
			f.FetchedAt = *t
		}
		f.ParseMedia()
		items = append(items, f)
	}
	return items, rows.Err()
}

// InsertFeedLike creates or updates a feed like record.
// fields map can include: source_handle, author_handle, author_display_name,
// body_text, link, canonical_x_link, published_at, media_url, avatar_url,
// media_json, platform, quote_payload_json.
func (db *DB) InsertFeedLike(username, tweetID string, fields map[string]string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		if err := db.ensureFeedItemStubFromLikeTx(tx, tweetID, fields); err != nil {
			return err
		}
		publishedAtMs := parseTimestampString(fields["published_at"])
		_, err := tx.Exec(`
			INSERT INTO feed_likes (
				username, tweet_id, source_handle, author_handle, author_display_name,
				body_text, link, canonical_x_link, published_at, media_url, avatar_url,
				media_json, platform, quote_payload_json, liked_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(username, tweet_id) DO UPDATE SET
				source_handle = COALESCE(excluded.source_handle, feed_likes.source_handle),
				author_handle = COALESCE(excluded.author_handle, feed_likes.author_handle),
				author_display_name = COALESCE(excluded.author_display_name, feed_likes.author_display_name),
				body_text = COALESCE(excluded.body_text, feed_likes.body_text),
				link = COALESCE(excluded.link, feed_likes.link),
				published_at = CASE WHEN excluded.published_at > 0 THEN excluded.published_at ELSE feed_likes.published_at END,
				media_url = COALESCE(excluded.media_url, feed_likes.media_url),
				avatar_url = COALESCE(excluded.avatar_url, feed_likes.avatar_url),
				media_json = CASE WHEN excluded.media_json IS NULL OR excluded.media_json = '' THEN feed_likes.media_json ELSE excluded.media_json END,
				platform = COALESCE(excluded.platform, feed_likes.platform),
				liked_at = excluded.liked_at,
				updated_at = excluded.updated_at
		`,
			username, tweetID,
			fields["source_handle"], fields["author_handle"], fields["author_display_name"],
			fields["body_text"], fields["link"], fields["canonical_x_link"],
			publishedAtMs, fields["media_url"], fields["avatar_url"],
			fields["media_json"], fields["platform"], fields["quote_payload_json"],
			nowMs, nowMs,
		)
		if err != nil {
			return err
		}
		if err := db.bumpFeedItemSyncSeqTx(tx, tweetID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?)`,
			username, tweetID, nowMs); err != nil {
			return err
		}
		if err := db.recordSyncChangeTx(tx, "like", tweetID, `{"liked":true}`); err != nil {
			return err
		}
		seenValue, _ := json.Marshal(map[string]any{
			"tweet_ids":     []string{tweetID},
			"updated_at_ms": nowMs,
		})
		return db.recordSyncChangeTx(tx, "seen", tweetID, string(seenValue))
	})
}

// DeleteFeedLike removes a feed like record.
func (db *DB) DeleteFeedLike(username, tweetID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM feed_likes WHERE username = ? AND tweet_id = ?", username, tweetID)
		if err != nil {
			return err
		}
		if err := db.bumpFeedItemSyncSeqTx(tx, tweetID); err != nil {
			return err
		}
		return db.recordSyncChangeTx(tx, "like", tweetID, `{"liked":false}`)
	})
}

// MarkSeen marks tweet IDs as seen for a user. Returns count of rows affected.
func (db *DB) MarkSeen(username string, tweetIDs []string) (int, error) {
	if len(tweetIDs) == 0 {
		return 0, nil
	}
	var total int
	err := db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		cleanIDs := make([]string, 0, len(tweetIDs))
		for _, id := range tweetIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			cleanIDs = append(cleanIDs, id)
			res, err := tx.Exec(`
				INSERT INTO feed_seen (username, tweet_id, seen_at)
				VALUES (?, ?, ?)
				ON CONFLICT(username, tweet_id) DO UPDATE SET seen_at = excluded.seen_at
			`, username, id, nowMs)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			total += int(n)
		}
		if len(cleanIDs) == 0 {
			return nil
		}
		valueJSON, _ := json.Marshal(map[string]any{
			"tweet_ids":     cleanIDs,
			"updated_at_ms": nowMs,
		})
		return db.recordSyncChangeTx(tx, "seen", cleanIDs[0], string(valueJSON))
	})
	return total, err
}

// MuteAccount adds a handle to the muted list.
func (db *DB) MuteAccount(handle string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		_, err := tx.Exec("INSERT OR IGNORE INTO muted_accounts (handle, muted_at) VALUES (?, ?)", handle, nowMs)
		if err != nil {
			return err
		}
		valueJSON, _ := json.Marshal(map[string]any{
			"action":        "set",
			"updated_at_ms": nowMs,
		})
		return db.recordSyncChangeTx(tx, "mute", handle, string(valueJSON))
	})
}

// UnmuteAccount removes a handle from the muted list.
func (db *DB) UnmuteAccount(handle string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		_, err := tx.Exec("DELETE FROM muted_accounts WHERE handle = ?", handle)
		if err != nil {
			return err
		}
		valueJSON, _ := json.Marshal(map[string]any{
			"action":        "clear",
			"updated_at_ms": nowMs,
		})
		return db.recordSyncChangeTx(tx, "mute", handle, string(valueJSON))
	})
}

// UpsertFeedItems inserts or updates feed_items rows, matching the Python upsert logic.
// Returns the number of items processed.
func (db *DB) UpsertFeedItems(items []model.FeedItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	var total int
	err := db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT INTO feed_items (
				tweet_id, source_handle, author_handle, author_display_name,
				author_avatar_url, body_text, lang, is_retweet,
				retweeted_by_handle, retweeted_by_display_name,
				quote_tweet_id, quote_author_handle, quote_author_display_name,
				quote_author_avatar_url, quote_body_text, quote_lang, quote_media_json,
				media_json, canonical_url, reply_to_handle, reply_to_status,
				is_reply, is_ghost,
				quote_published_at, views, likes, retweets,
				published_at, fetched_at,
				content_hash, canonical_tweet_id,
				sync_seq
			) VALUES (
				?, ?, ?, ?,
				?, ?, ?, ?,
				?, ?,
				?, ?, ?,
				?, ?, ?, ?,
				?, ?, ?, ?,
				?, ?,
				?, ?, ?, ?,
				?, ?,
				?, ?,
				?
			)
			ON CONFLICT(tweet_id) DO UPDATE SET
				source_handle = COALESCE(excluded.source_handle, feed_items.source_handle),
				author_display_name = COALESCE(excluded.author_display_name, feed_items.author_display_name),
				author_avatar_url = CASE
					WHEN excluded.author_avatar_url IS NOT NULL THEN excluded.author_avatar_url
					WHEN LOWER(COALESCE(feed_items.author_avatar_url, '')) LIKE '%/status/undefined%' THEN NULL
					WHEN (
						LOWER(COALESCE(feed_items.author_avatar_url, '')) LIKE 'https://x.com/%/status/%'
						OR LOWER(COALESCE(feed_items.author_avatar_url, '')) LIKE 'http://x.com/%/status/%'
						OR LOWER(COALESCE(feed_items.author_avatar_url, '')) LIKE 'https://twitter.com/%/status/%'
						OR LOWER(COALESCE(feed_items.author_avatar_url, '')) LIKE 'http://twitter.com/%/status/%'
					) THEN NULL
					ELSE feed_items.author_avatar_url
				END,
				body_text = CASE
					WHEN excluded.body_text IS NULL OR excluded.body_text = '' THEN feed_items.body_text
					ELSE excluded.body_text
				END,
				media_json = COALESCE(excluded.media_json, feed_items.media_json),
				quote_tweet_id = CASE
					WHEN feed_items.quote_tweet_id IS NULL THEN excluded.quote_tweet_id
					ELSE feed_items.quote_tweet_id
				END,
				quote_author_handle = CASE
					WHEN feed_items.quote_tweet_id IS NULL THEN COALESCE(excluded.quote_author_handle, feed_items.quote_author_handle)
					ELSE feed_items.quote_author_handle
				END,
				quote_author_display_name = CASE
					WHEN feed_items.quote_tweet_id IS NULL THEN COALESCE(excluded.quote_author_display_name, feed_items.quote_author_display_name)
					ELSE feed_items.quote_author_display_name
				END,
				quote_author_avatar_url = CASE
					WHEN feed_items.quote_tweet_id IS NULL THEN CASE
						WHEN excluded.quote_author_avatar_url IS NOT NULL THEN excluded.quote_author_avatar_url
						WHEN LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE '%/status/undefined%' THEN NULL
						WHEN (
							LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'https://x.com/%/status/%'
							OR LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'http://x.com/%/status/%'
							OR LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'https://twitter.com/%/status/%'
							OR LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'http://twitter.com/%/status/%'
						) THEN NULL
						ELSE feed_items.quote_author_avatar_url
					END
					WHEN LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE '%/status/undefined%' THEN excluded.quote_author_avatar_url
					WHEN (
						LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'https://x.com/%/status/%'
						OR LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'http://x.com/%/status/%'
						OR LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'https://twitter.com/%/status/%'
						OR LOWER(COALESCE(feed_items.quote_author_avatar_url, '')) LIKE 'http://twitter.com/%/status/%'
					) THEN excluded.quote_author_avatar_url
					ELSE feed_items.quote_author_avatar_url
				END,
				quote_body_text = CASE
					WHEN feed_items.quote_tweet_id IS NULL THEN COALESCE(excluded.quote_body_text, feed_items.quote_body_text)
					ELSE feed_items.quote_body_text
				END,
				quote_lang = CASE
					WHEN feed_items.quote_tweet_id IS NULL THEN COALESCE(excluded.quote_lang, feed_items.quote_lang)
					ELSE feed_items.quote_lang
				END,
				quote_media_json = CASE
					WHEN feed_items.quote_tweet_id IS NULL THEN COALESCE(excluded.quote_media_json, feed_items.quote_media_json)
					ELSE feed_items.quote_media_json
				END,
				views = COALESCE(excluded.views, feed_items.views),
				likes = COALESCE(excluded.likes, feed_items.likes),
				retweets = COALESCE(excluded.retweets, feed_items.retweets),
				fetched_at = excluded.fetched_at,
				content_hash = COALESCE(excluded.content_hash, feed_items.content_hash),
				canonical_tweet_id = CASE
					WHEN feed_items.canonical_tweet_id IS NULL THEN excluded.canonical_tweet_id
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
				reply_to_handle = CASE
					WHEN excluded.reply_to_handle IS NOT NULL AND excluded.reply_to_handle != '' THEN excluded.reply_to_handle
					ELSE feed_items.reply_to_handle
				END,
				reply_to_status = CASE
					WHEN excluded.reply_to_status IS NOT NULL AND excluded.reply_to_status != '' THEN excluded.reply_to_status
					ELSE feed_items.reply_to_status
				END,
				sync_seq = excluded.sync_seq
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		nowMs := time.Now().UnixMilli()
		for _, item := range items {
			if item.TweetID == "" {
				continue
			}
			seq := db.NextSyncSeq()
			pubMs := timePtrToMillis(item.PublishedAt)
			quotePubMs := timePtrToMillis(item.QuotePublishedAt)
			_, err := stmt.Exec(
				item.TweetID,
				nilIfEmpty(item.SourceHandle),
				nilIfEmpty(item.AuthorHandle),
				nilIfEmpty(item.AuthorDisplayName),
				nilIfEmpty(model.CleanFeedAvatarURL(item.AuthorAvatarURL)),
				nilIfEmpty(item.BodyText),
				nilIfEmpty(item.Lang),
				boolToInt(item.IsRetweet),
				nilIfEmpty(item.RetweetedByHandle),
				nilIfEmpty(item.RetweetedByDisplayName),
				nilIfEmpty(item.QuoteTweetID),
				nilIfEmpty(item.QuoteAuthorHandle),
				nilIfEmpty(item.QuoteAuthorDisplayName),
				nilIfEmpty(model.CleanFeedAvatarURL(item.QuoteAuthorAvatarURL)),
				nilIfEmpty(item.QuoteBodyText),
				nilIfEmpty(item.QuoteLang),
				nilIfEmpty(item.QuoteMediaJSON),
				nilIfEmpty(item.MediaJSON),
				nilIfEmpty(item.CanonicalURL),
				nilIfEmpty(item.ReplyToHandle),
				nilIfEmpty(item.ReplyToStatus),
				boolToInt(item.IsReply),
				boolToInt(item.IsGhost),
				quotePubMs,
				nilIfZero(item.Views),
				nilIfZero(item.Likes),
				nilIfZero(item.Retweets),
				pubMs,
				nowMs,
				nilIfEmpty(item.ContentHash),
				nilIfEmpty(item.CanonicalTweetID),
				seq,
			)
			if err != nil {
				return err
			}
			total++
		}

		// --- Populate retweet_sources for retweet items ---
		rtStmt, err := tx.Prepare(`
			INSERT OR REPLACE INTO retweet_sources
				(content_hash, retweeter_handle, retweeter_display_name, tweet_id, published_at)
			VALUES (?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer rtStmt.Close()

		hashSet := make(map[string]bool)
		for _, item := range items {
			if item.ContentHash == "" || !item.IsRetweet {
				continue
			}
			hashSet[item.ContentHash] = true
			retweeter := item.SourceHandle
			if item.RetweetedByHandle != "" {
				retweeter = item.RetweetedByHandle
			}
			if retweeter == "" {
				continue
			}
			if _, err := rtStmt.Exec(
				item.ContentHash, retweeter,
				nilIfEmpty(item.RetweetedByDisplayName),
				item.TweetID, timePtrToMillis(item.PublishedAt),
			); err != nil {
				return err
			}
		}

		// --- Set canonical_tweet_id for dedup ---
		// For each content_hash in this batch, pick the canonical tweet
		// (prefer non-retweet, then earliest published_at).
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
				UPDATE feed_items
				SET canonical_tweet_id = (
					SELECT f2.tweet_id FROM feed_items f2
					WHERE f2.content_hash = feed_items.content_hash
					  AND f2.content_hash != ''
					ORDER BY COALESCE(f2.is_retweet,0) ASC, f2.published_at ASC, f2.tweet_id ASC
					LIMIT 1
				)
				WHERE content_hash IN (`+ph+`)
				  AND content_hash != ''
			`, hashArgs...)
			if err != nil {
				return err
			}
		}

		return nil
	})
	return total, err
}

// GetLatestFeedItem returns the most recently published feed item, or nil if none.
func (db *DB) GetLatestFeedItem() (*model.FeedItem, error) {
	rows, err := db.conn.Query(`
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name,''), COALESCE(author_avatar_url,''),
		       COALESCE(body_text,''), COALESCE(lang,''),
		       COALESCE(is_retweet,0), COALESCE(retweeted_by_handle,''),
		       COALESCE(retweeted_by_display_name,''),
		       COALESCE(quote_tweet_id,''), COALESCE(quote_author_handle,''),
		       COALESCE(quote_author_display_name,''), COALESCE(quote_author_avatar_url,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_lang,''),
		       COALESCE(quote_media_json,''), COALESCE(media_json,''),
		       COALESCE(canonical_url,''), COALESCE(reply_to_handle,''),
		       COALESCE(reply_to_status,''),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       quote_published_at,
		       COALESCE(views,0), COALESCE(likes,0), COALESCE(retweets,0),
		       published_at, fetched_at,
		       COALESCE(content_hash,''), COALESCE(canonical_tweet_id,'')
		FROM feed_items
		ORDER BY published_at DESC, tweet_id DESC
		LIMIT 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanFeedItems(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

// GetNewPosterAvatars returns up to `limit` unique new posters for the "new
// posts" bar avatar stack. The candidate set is feed_items with
// published_at > knownHeadTweetID.published_at, excluding muted accounts.
//
// Ranking: snapshot winners first (ordered by final_score DESC), then
// recency-based fill (published_at DESC) for authors not yet represented.
// Dedupes by lower(author_handle). Avatar URLs prefer channel_profiles.avatar_url
// (direct twimg URL) and fall back to /api/media/avatar/twitter_<handle_lower>
// so already-cached local avatars still render through the proxy.
//
// Returns an empty slice when knownHeadTweetID is empty or not found.
func (db *DB) GetNewPosterAvatars(username, knownHeadTweetID string, limit int) ([]model.NewPosterAvatar, error) {
	if limit <= 0 || knownHeadTweetID == "" {
		return nil, nil
	}

	var knownPubAt sql.NullInt64
	if err := db.conn.QueryRow(
		"SELECT published_at FROM feed_items WHERE tweet_id = ? LIMIT 1",
		knownHeadTweetID,
	).Scan(&knownPubAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !knownPubAt.Valid {
		return nil, nil
	}

	muted, _ := db.GetMutedAccounts()
	muteClauses, muteArgs := buildMuteClauses(muted)
	muteSQL := ""
	for _, c := range muteClauses {
		muteSQL += " AND " + c
	}

	out := make([]model.NewPosterAvatar, 0, limit)
	seen := make(map[string]bool, limit)

	appendRow := func(handle, display, avatarURL string) {
		if handle == "" {
			return
		}
		key := strings.ToLower(handle)
		if seen[key] {
			return
		}
		if avatarURL == "" {
			avatarURL = "/api/media/avatar/twitter_" + key
		}
		seen[key] = true
		out = append(out, model.NewPosterAvatar{
			AuthorHandle:      handle,
			AuthorDisplayName: display,
			AuthorAvatarURL:   avatarURL,
		})
	}
	const avatarSelect = `COALESCE(
		NULLIF(cp.avatar_url, ''),
		CASE
			WHEN COALESCE(fi.author_avatar_url, '') LIKE 'https://pbs.twimg.com/%' THEN fi.author_avatar_url
			ELSE ''
		END
	)`
	const profileJoin = `LEFT JOIN channel_profiles cp
		ON cp.channel_id = 'twitter_' || lower(fi.author_handle)
		AND cp.tombstone = 0`

	// Primary: ranked by snapshot final_score.
	if username != "" {
		snapQuery := `
			SELECT fi.author_handle,
			       COALESCE(NULLIF(fi.author_display_name,''), COALESCE(cp.display_name,'')),
			       ` + avatarSelect + `
			FROM feed_rank_snapshot s
			JOIN feed_items fi ON fi.tweet_id = s.tweet_id
			` + profileJoin + `
			WHERE s.username = ?
			  AND fi.published_at > ?` + muteSQL + `
			ORDER BY s.final_score DESC
		`
		snapArgs := append([]any{username, knownPubAt.Int64}, muteArgs...)
		rows, err := db.conn.Query(snapQuery, snapArgs...)
		if err == nil {
			for rows.Next() && len(out) < limit {
				var handle, display, avatarURL string
				if err := rows.Scan(&handle, &display, &avatarURL); err != nil {
					break
				}
				appendRow(handle, display, avatarURL)
			}
			rows.Close()
		}
	}

	// Supplement: fill remaining slots from recency.
	if len(out) < limit {
		recentQuery := `
			SELECT fi.author_handle,
			       COALESCE(NULLIF(fi.author_display_name,''), COALESCE(cp.display_name,'')),
			       ` + avatarSelect + `
			FROM feed_items fi
			` + profileJoin + `
			WHERE fi.published_at > ?` + muteSQL + `
			ORDER BY fi.published_at DESC
			LIMIT 50
		`
		recentArgs := append([]any{knownPubAt.Int64}, muteArgs...)
		rows, err := db.conn.Query(recentQuery, recentArgs...)
		if err == nil {
			for rows.Next() && len(out) < limit {
				var handle, display, avatarURL string
				if err := rows.Scan(&handle, &display, &avatarURL); err != nil {
					break
				}
				appendRow(handle, display, avatarURL)
			}
			rows.Close()
		}
	}

	return out, nil
}

// buildMuteClauses returns SQL fragments that filter against muted
// author_handle / source_handle. Each clause references `fi.<col>` so it
// slots into JOINs that alias feed_items as `fi`.
func buildMuteClauses(muted []string) ([]string, []any) {
	if len(muted) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(muted))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(muted)*2)
	for _, h := range muted {
		args = append(args, h)
	}
	for _, h := range muted {
		args = append(args, h)
	}
	return []string{
		"fi.author_handle NOT IN (" + placeholders + ")",
		"COALESCE(fi.source_handle,'') NOT IN (" + placeholders + ")",
	}, args
}

// CountFeedItems returns the total number of rows in feed_items.
func (db *DB) CountFeedItems() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM feed_items").Scan(&count)
	return count, err
}

// ListFeedItemsFiltered returns feed items with cursor pagination and optional handle filter.
func (db *DB) ListFeedItemsFiltered(limit int, cursor *model.FeedCursor, sourceHandle string) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 40
	}

	var where []string
	var args []any

	muted, _ := db.GetMutedAccounts()
	if len(muted) > 0 {
		placeholders := strings.Repeat("?,", len(muted))
		placeholders = placeholders[:len(placeholders)-1]
		where = append(where, "author_handle NOT IN ("+placeholders+")")
		for _, h := range muted {
			args = append(args, h)
		}
		where = append(where, "COALESCE(source_handle,'') NOT IN ("+placeholders+")")
		for _, h := range muted {
			args = append(args, h)
		}
	}

	if sourceHandle != "" {
		where = append(where, "LOWER(source_handle) = LOWER(?)")
		args = append(args, sourceHandle)
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
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name,''), COALESCE(author_avatar_url,''),
		       COALESCE(body_text,''), COALESCE(lang,''),
		       COALESCE(is_retweet,0), COALESCE(retweeted_by_handle,''),
		       COALESCE(retweeted_by_display_name,''),
		       COALESCE(quote_tweet_id,''), COALESCE(quote_author_handle,''),
		       COALESCE(quote_author_display_name,''), COALESCE(quote_author_avatar_url,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_lang,''),
		       COALESCE(quote_media_json,''), COALESCE(media_json,''),
		       COALESCE(canonical_url,''), COALESCE(reply_to_handle,''),
		       COALESCE(reply_to_status,''),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       quote_published_at,
		       COALESCE(views,0), COALESCE(likes,0), COALESCE(retweets,0),
		       published_at, fetched_at,
		       COALESCE(content_hash,''), COALESCE(canonical_tweet_id,'')
		FROM feed_items
		` + whereClause + `
		ORDER BY published_at DESC, tweet_id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
		SELECT f.tweet_id, COALESCE(f.source_handle,''), f.author_handle,
		       COALESCE(f.author_display_name,''), COALESCE(f.author_avatar_url,''),
		       COALESCE(f.body_text,''), COALESCE(f.lang,''),
		       COALESCE(f.is_retweet,0), COALESCE(f.retweeted_by_handle,''),
		       COALESCE(f.retweeted_by_display_name,''),
		       COALESCE(f.quote_tweet_id,''), COALESCE(f.quote_author_handle,''),
		       COALESCE(f.quote_author_display_name,''), COALESCE(f.quote_author_avatar_url,''),
		       COALESCE(f.quote_body_text,''), COALESCE(f.quote_lang,''),
		       COALESCE(f.quote_media_json,''), COALESCE(f.media_json,''),
		       COALESCE(f.canonical_url,''), COALESCE(f.reply_to_handle,''),
		       COALESCE(f.reply_to_status,''),
		       COALESCE(f.is_reply,0), COALESCE(f.is_ghost,0),
		       f.quote_published_at,
		       COALESCE(f.views,0), COALESCE(f.likes,0), COALESCE(f.retweets,0),
		       f.published_at, f.fetched_at,
		       COALESCE(f.content_hash,''), COALESCE(f.canonical_tweet_id,'')
		FROM feed_items f
		INNER JOIN bookmarks b ON b.video_id = f.tweet_id
		` + whereClause + `
		ORDER BY b.bookmarked_at DESC, f.tweet_id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeedItems(rows)
}

// CountBookmarkedFeedItems returns the count of bookmarked feed items.
func (db *DB) CountBookmarkedFeedItems() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM bookmarks b INNER JOIN feed_items f ON f.tweet_id = b.video_id").Scan(&count)
	return count, err
}

// CountFeedLikes returns the count of liked feed items for a user.
func (db *DB) CountFeedLikes(username string) (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM feed_likes WHERE username = ?", username).Scan(&count)
	return count, err
}

// BackfillQuoteData updates the quote fields on a feed item that was previously
// ingested without them (e.g. because RSSHub truncated the description).
func (db *DB) BackfillQuoteData(tweetID string, item model.FeedItem) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE feed_items SET
				quote_tweet_id = ?,
				quote_author_handle = ?,
				quote_author_display_name = ?,
				quote_author_avatar_url = ?,
				quote_body_text = ?,
				quote_lang = ?,
				quote_media_json = ?
			WHERE tweet_id = ? AND quote_tweet_id IS NULL
		`,
			item.QuoteTweetID,
			item.QuoteAuthorHandle,
			item.QuoteAuthorDisplayName,
			item.QuoteAuthorAvatarURL,
			item.QuoteBodyText,
			item.QuoteLang,
			item.QuoteMediaJSON,
			tweetID,
		)
		return err
	})
}

// BackfillQuoteText updates just the quote_body_text (and lang) for a feed item
// whose quote text was truncated by RSSHub (~280 char limit). Returns the number
// of rows actually updated (0 when the stored text is already at least as long
// as fullText).
func (db *DB) BackfillQuoteText(tweetID, fullText, lang string) (int64, error) {
	var affected int64
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE feed_items SET quote_body_text = ?, quote_lang = ?
			WHERE tweet_id = ? AND quote_body_text != '' AND length(quote_body_text) < length(?)
		`, fullText, lang, tweetID, fullText)
		if err != nil {
			return err
		}
		affected, _ = res.RowsAffected()
		return nil
	})
	return affected, err
}

// TruncatedQuoteCandidate identifies a feed item whose stored quote_body_text is
// near the RSSHub ~280-char truncation boundary and may still need a full-text
// refetch from fxtwitter.
type TruncatedQuoteCandidate struct {
	TweetID      string
	QuoteTweetID string
	QuoteAuthor  string
}

// FindTruncatedQuoteCandidates returns feed items whose quote_body_text length
// is in [minLen, maxLen] — the window where RSSHub may have truncated the body
// at its ~280-char limit. Used by the periodic sweep that retries fxtwitter for
// items the per-cycle backfill failed to enrich.
func (db *DB) FindTruncatedQuoteCandidates(minLen, maxLen int) ([]TruncatedQuoteCandidate, error) {
	rows, err := db.conn.Query(`
		SELECT tweet_id, quote_tweet_id, quote_author_handle
		FROM feed_items
		WHERE quote_body_text != ''
		  AND length(quote_body_text) BETWEEN ? AND ?
		  AND quote_tweet_id IS NOT NULL AND quote_tweet_id != ''
		  AND quote_author_handle IS NOT NULL AND quote_author_handle != ''
	`, minLen, maxLen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TruncatedQuoteCandidate
	for rows.Next() {
		var c TruncatedQuoteCandidate
		if err := rows.Scan(&c.TweetID, &c.QuoteTweetID, &c.QuoteAuthor); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
