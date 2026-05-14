package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// GetVideosOpts holds filtering/pagination options for video queries.
type GetVideosOpts struct {
	ChannelID        string
	Limit            int
	Offset           int
	Search           string
	Platform         string
	SourceKind       string
	ExcludeMetadata  bool
	UnwatchedOnly    bool
	IncludeTemp      bool
	UserID           string
	OrderAsc         bool // true = oldest first, false = newest first (default)
	MomentsMode      string
	PublishedAfterMs int64
}

func includeUndownloadedVideoRows(opts GetVideosOpts) bool {
	platform := strings.ToLower(strings.TrimSpace(opts.Platform))
	switch platform {
	case "shorts", "tiktok", "instagram":
		return true
	}

	channelID := strings.ToLower(strings.TrimSpace(opts.ChannelID))
	return strings.HasPrefix(channelID, "tiktok_") || strings.HasPrefix(channelID, "instagram_")
}

// GetVideo returns a single video by ID with joined channel info.
func (db *DB) GetVideo(videoID string) (*model.Video, error) {
	row := db.conn.QueryRow(`
		SELECT v.id, v.video_id, v.channel_id, v.title, COALESCE(v.description,''),
		       v.duration, COALESCE(v.thumbnail_path,''), COALESCE(v.file_path,''),
		       COALESCE(v.file_size,0), v.published_at, v.downloaded_at,
		       COALESCE(v.watched,0), COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       COALESCE(v.metadata_json,''),
		       COALESCE(v.media_kind,''), COALESCE(v.slide_count,0), COALESCE(v.source_kind,''),
		       COALESCE(c.name,''), COALESCE(c.platform,'youtube'),
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_thumb_path, v.dearrow_checked_at
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id AND cs.user_id = ''
		WHERE v.video_id = ?
	`, videoID)

	var v model.Video
	var publishedAt, downloadedAt sql.NullInt64
	err := row.Scan(
		&v.ID, &v.VideoID, &v.ChannelID, &v.Title, &v.Description,
		&v.Duration, &v.ThumbnailPath, &v.FilePath,
		&v.FileSize, &publishedAt, &downloadedAt,
		&v.Watched, &v.IsTemp, &v.IsPinned,
		&v.MetadataJSON,
		&v.MediaKind, &v.MediaSlideCount, &v.SourceKind,
		&v.ChannelName, &v.Platform,
		&v.IsStarred, &v.IsSubscribed,
		&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowThumbPath, &v.DearrowCheckedAtMs,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.PublishedAt = millisToTimePtr(publishedAt)
	if t := millisToTimePtr(downloadedAt); t != nil {
		v.DownloadedAt = *t
	}
	v.Platform = detectPlatform(v.Platform, "")
	db.resolveVideoPaths(&v)
	return &v, nil
}

// GetNextVideo returns the video with the closest earlier published_at,
// used for "next in line" in the player sidebar.
func (db *DB) GetNextVideo(videoID string) (*model.Video, error) {
	row := db.conn.QueryRow(`
		SELECT v.id, v.video_id, v.channel_id, v.title, COALESCE(v.description,''),
		       v.duration, COALESCE(v.thumbnail_path,''), COALESCE(v.file_path,''),
		       COALESCE(v.file_size,0), v.published_at, v.downloaded_at,
		       COALESCE(v.watched,0), COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       COALESCE(v.metadata_json,''),
		       COALESCE(v.media_kind,''), COALESCE(v.slide_count,0),
		       COALESCE(c.name,''), COALESCE(c.platform,'youtube'),
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_thumb_path, v.dearrow_checked_at
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id AND cs.user_id = ''
		WHERE v.published_at < (SELECT published_at FROM videos WHERE video_id = ?)
		  AND v.file_path != ''
		  AND c.platform = 'youtube'
		ORDER BY v.published_at DESC
		LIMIT 1
	`, videoID)

	var v model.Video
	var publishedAt, downloadedAt sql.NullInt64
	err := row.Scan(
		&v.ID, &v.VideoID, &v.ChannelID, &v.Title, &v.Description,
		&v.Duration, &v.ThumbnailPath, &v.FilePath,
		&v.FileSize, &publishedAt, &downloadedAt,
		&v.Watched, &v.IsTemp, &v.IsPinned,
		&v.MetadataJSON,
		&v.MediaKind, &v.MediaSlideCount,
		&v.ChannelName, &v.Platform,
		&v.IsStarred, &v.IsSubscribed,
		&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowThumbPath, &v.DearrowCheckedAtMs,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.PublishedAt = millisToTimePtr(publishedAt)
	if t := millisToTimePtr(downloadedAt); t != nil {
		v.DownloadedAt = *t
	}
	v.Platform = detectPlatform(v.Platform, "")
	db.resolveVideoPaths(&v)
	return &v, nil
}

// GetVideos returns a paginated list of videos with optional filtering.
func (db *DB) GetVideos(opts GetVideosOpts) ([]model.Video, error) {
	var where []string
	var args []any
	momentsMode := NormalizeMomentsTab(opts.MomentsMode)
	isMomentsQuery := opts.Platform == "shorts" && opts.MomentsMode != ""
	includeMomentReposts := isMomentsQuery && momentsMode == "all" && db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := isMomentsQuery && momentsMode == "all" && db.InstagramIncludeTaggedEnabled()
	includeSourceWindows := includeMomentReposts || includeInstagramTagged

	if opts.ChannelID != "" {
		where = append(where, "v.channel_id = ?")
		args = append(args, opts.ChannelID)
	}
	if opts.SourceKind != "" {
		where = append(where, "COALESCE(v.source_kind, '') = ?")
		args = append(args, opts.SourceKind)
		if opts.SourceKind == "story" {
			where = append(where, validStoryVideoSQL("v", "c"))
		}
	} else {
		where = append(where, "COALESCE(v.source_kind, '') != 'story'")
	}
	if opts.Platform == "shorts" {
		where = append(where, "COALESCE(c.platform,'') IN ('tiktok','instagram')")
		if isMomentsQuery {
			if includeSourceWindows {
				where = append(where, "(cf.channel_id IS NOT NULL OR mr.video_id IS NOT NULL)")
			} else {
				where = append(where, "cf.channel_id IS NOT NULL")
			}
		}
	} else if opts.Platform != "" {
		where = append(where, "v.channel_id IN (SELECT channel_id FROM channels WHERE platform = ?)")
		args = append(args, opts.Platform)
	}
	if opts.Search != "" {
		where = append(where, "(v.title LIKE ? OR v.description LIKE ?)")
		like := "%" + opts.Search + "%"
		args = append(args, like, like)
	}
	if opts.PublishedAfterMs > 0 {
		where = append(where, "COALESCE(v.published_at,0) >= ?")
		args = append(args, opts.PublishedAfterMs)
	}
	if !includeUndownloadedVideoRows(opts) {
		where = append(where, "v.file_path IS NOT NULL AND v.file_path <> ''")
	}
	if !opts.IncludeTemp {
		where = append(where, "COALESCE(v.is_temp,0) = 0")
	}
	if opts.UnwatchedOnly {
		where = append(where, "COALESCE(v.watched,0) = 0")
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	metadataCol := "COALESCE(v.metadata_json,'')"
	if opts.ExcludeMetadata {
		metadataCol = "''"
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10000
	}

	orderDir := "DESC"
	if opts.OrderAsc {
		orderDir = "ASC"
	}
	withClause := ""
	repostJoin := ""
	repostCols := `
		       '' AS reposter_channel_id,
		       '' AS reposter_handle,
		       '' AS reposter_display_name,
		       0 AS repost_count,
		       0 AS repost_introduced,
		       COALESCE(v.published_at, 0) AS effective_moment_at_ms`
	orderExpr := "v.published_at"
	if includeSourceWindows {
		withClause = `
		WITH allowed_moment_reposts AS (
			SELECT vrs.*,
			       COUNT(*) OVER (PARTITION BY vrs.video_id) AS repost_count,
			       ROW_NUMBER() OVER (
			           PARTITION BY vrs.video_id
			           ORDER BY COALESCE(NULLIF(vrs.reposted_at_ms, 0), vrs.first_seen_at_ms) DESC,
			                    vrs.reposter_channel_id ASC
			       ) AS rn
			FROM video_repost_sources vrs
			INNER JOIN videos owner ON owner.video_id = vrs.video_id
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id AND rcf.user_id = ''
			LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
			WHERE COALESCE(rcs.include_reposts, 1) != 0
			  AND ` + sourceWindowPlatformEnabledClause("owner", includeMomentReposts, includeInstagramTagged) + `
		),
		moments_repost_heads AS (
			SELECT *
			FROM allowed_moment_reposts
			WHERE rn = 1
		)`
		repostJoin = "LEFT JOIN moments_repost_heads mr ON mr.video_id = v.video_id"
		repostCols = `
		       COALESCE(mr.reposter_channel_id, '') AS reposter_channel_id,
		       COALESCE(mr.reposter_handle, '') AS reposter_handle,
		       COALESCE(mr.reposter_display_name, '') AS reposter_display_name,
		       COALESCE(mr.repost_count, 0) AS repost_count,
		       CASE WHEN cf.channel_id IS NULL AND mr.video_id IS NOT NULL THEN 1 ELSE 0 END AS repost_introduced,
		       CASE WHEN cf.channel_id IS NULL AND mr.video_id IS NOT NULL
		            THEN COALESCE(NULLIF(mr.reposted_at_ms, 0), NULLIF(mr.first_seen_at_ms, 0), v.published_at, 0)
		            ELSE COALESCE(v.published_at, 0)
		        END AS effective_moment_at_ms`
		orderExpr = "effective_moment_at_ms"
	}

	query := fmt.Sprintf(`
		%s
		SELECT v.id, v.video_id, v.channel_id, v.title, COALESCE(v.description,''),
		       v.duration, COALESCE(v.thumbnail_path,''), COALESCE(v.file_path,''),
		       COALESCE(v.file_size,0), v.published_at, v.downloaded_at,
		       COALESCE(v.watched,0), COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       %s,
		       COALESCE(v.media_kind,''), COALESCE(v.slide_count,0), COALESCE(v.source_kind,''),
		       COALESCE(c.name,''), COALESCE(c.platform,'youtube'),
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       b.category_id,
		       %s,
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_thumb_path, v.dearrow_checked_at
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id AND cs.user_id = ''
		LEFT JOIN bookmarks b ON b.video_id = v.video_id
		%s
		%s
		ORDER BY %s %s, v.video_id %s
		LIMIT ? OFFSET ?
	`, withClause, metadataCol, repostCols, repostJoin, whereClause, orderExpr, orderDir, orderDir)
	args = append(args, limit, opts.Offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var videos []model.Video
	for rows.Next() {
		var v model.Video
		var publishedAt, downloadedAt sql.NullInt64
		var bookmarkCatID sql.NullInt64
		var repostIntroduced int
		err := rows.Scan(
			&v.ID, &v.VideoID, &v.ChannelID, &v.Title, &v.Description,
			&v.Duration, &v.ThumbnailPath, &v.FilePath,
			&v.FileSize, &publishedAt, &downloadedAt,
			&v.Watched, &v.IsTemp, &v.IsPinned,
			&v.MetadataJSON,
			&v.MediaKind, &v.MediaSlideCount, &v.SourceKind,
			&v.ChannelName, &v.Platform,
			&v.IsStarred, &v.IsSubscribed,
			&bookmarkCatID,
			&v.ReposterChannelID, &v.ReposterHandle, &v.ReposterDisplayName,
			&v.RepostCount, &repostIntroduced, &v.EffectiveMomentAtMs,
			&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowThumbPath, &v.DearrowCheckedAtMs,
		)
		if err != nil {
			return nil, err
		}
		v.PublishedAt = millisToTimePtr(publishedAt)
		if t := millisToTimePtr(downloadedAt); t != nil {
			v.DownloadedAt = *t
		}
		if bookmarkCatID.Valid {
			catID := bookmarkCatID.Int64
			v.BookmarkCategoryID = &catID
		}
		v.RepostIntroduced = repostIntroduced != 0
		v.Platform = detectPlatform(v.Platform, "")
		db.resolveVideoPaths(&v)
		v.EnrichForCard()
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

// GetLatestVideosPerChannel returns up to perChannel recent videos for subscribed non-Twitter channels.
// If channelIDs is non-empty, only those channels are included.
// Returns a map from channelID to slice of videos.
func (db *DB) GetLatestVideosPerChannel(perChannel int, channelIDs ...string) (map[string][]model.Video, error) {
	var channelFilter string
	var args []any
	if len(channelIDs) > 0 {
		ph := strings.Repeat("?,", len(channelIDs))
		channelFilter = "AND v.channel_id IN (" + ph[:len(ph)-1] + ")"
		for _, id := range channelIDs {
			args = append(args, id)
		}
	}

	query := fmt.Sprintf(`
		WITH ranked AS (
			SELECT v.id, v.video_id, v.channel_id, v.title, COALESCE(v.description,'') as description,
			       v.duration, COALESCE(v.thumbnail_path,'') as thumbnail_path, COALESCE(v.file_path,'') as file_path,
			       COALESCE(v.file_size,0) as file_size, v.published_at, v.downloaded_at,
			       COALESCE(v.watched,0) as watched, COALESCE(v.is_temp,0) as is_temp, COALESCE(v.is_pinned,0) as is_pinned,
			       '' as metadata_json,
			       COALESCE(v.media_kind,'') as media_kind, COALESCE(v.slide_count,0) as slide_count,
			       COALESCE(c.name,'') as channel_name, COALESCE(c.platform,'youtube') as platform,
			       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END as is_starred,
			       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END as is_subscribed,
			       v.dearrow_title, v.dearrow_title_casual, v.dearrow_thumb_path, v.dearrow_checked_at,
			       ROW_NUMBER() OVER (PARTITION BY v.channel_id ORDER BY v.published_at DESC) as rn
			FROM videos v
			LEFT JOIN channels c ON v.channel_id = c.channel_id
			INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
			LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id AND cs.user_id = ''
			WHERE COALESCE(v.is_temp,0) = 0
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND (
			       COALESCE(c.platform,'youtube') IN ('tiktok','instagram')
			       OR (v.file_path IS NOT NULL AND v.file_path <> '')
			  )
			  AND COALESCE(c.platform,'youtube') != 'twitter'
			  %s
		)
		SELECT id, video_id, channel_id, title, description,
		       duration, thumbnail_path, file_path,
		       file_size, published_at, downloaded_at,
		       watched, is_temp, is_pinned,
		       metadata_json,
		       media_kind, slide_count,
		       channel_name, platform,
		       is_starred, is_subscribed,
		       dearrow_title, dearrow_title_casual, dearrow_thumb_path, dearrow_checked_at
		FROM ranked WHERE rn <= ?
		ORDER BY channel_id, published_at DESC
	`, channelFilter)
	args = append(args, perChannel)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	result := make(map[string][]model.Video)
	for rows.Next() {
		var v model.Video
		var publishedAt, downloadedAt sql.NullInt64
		err := rows.Scan(
			&v.ID, &v.VideoID, &v.ChannelID, &v.Title, &v.Description,
			&v.Duration, &v.ThumbnailPath, &v.FilePath,
			&v.FileSize, &publishedAt, &downloadedAt,
			&v.Watched, &v.IsTemp, &v.IsPinned,
			&v.MetadataJSON,
			&v.MediaKind, &v.MediaSlideCount,
			&v.ChannelName, &v.Platform,
			&v.IsStarred, &v.IsSubscribed,
			&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowThumbPath, &v.DearrowCheckedAtMs,
		)
		if err != nil {
			return nil, err
		}
		v.PublishedAt = millisToTimePtr(publishedAt)
		if t := millisToTimePtr(downloadedAt); t != nil {
			v.DownloadedAt = *t
		}
		v.Platform = detectPlatform(v.Platform, "")
		db.resolveVideoPaths(&v)
		v.EnrichForCard()
		result[v.ChannelID] = append(result[v.ChannelID], v)
	}
	return result, rows.Err()
}

// GetLatestFeedMediaPerAuthor returns up to perAuthor recent feed items with media,
// grouped by author_handle. If handles is non-empty, only those authors are included.
// Returns a map from lowercase handle to items.
func (db *DB) GetLatestFeedMediaPerAuthor(perAuthor int, handles ...string) (map[string][]model.FeedItem, error) {
	var handleFilter string
	var args []any
	if len(handles) > 0 {
		ph := strings.Repeat("?,", len(handles))
		handleFilter = "AND author_handle COLLATE NOCASE IN (" + ph[:len(ph)-1] + ")"
		for _, h := range handles {
			args = append(args, h)
		}
	}

	query := fmt.Sprintf(`
		WITH ranked AS (
			SELECT tweet_id, COALESCE(source_handle,'') as source_handle, author_handle,
			       COALESCE(author_display_name,'') as author_display_name, COALESCE(author_avatar_url,'') as author_avatar_url,
			       COALESCE(body_text,'') as body_text, COALESCE(lang,'') as lang,
			       COALESCE(is_retweet,0) as is_retweet, COALESCE(retweeted_by_handle,'') as retweeted_by_handle,
			       COALESCE(retweeted_by_display_name,'') as retweeted_by_display_name,
			       COALESCE(quote_tweet_id,'') as quote_tweet_id, COALESCE(quote_author_handle,'') as quote_author_handle,
			       COALESCE(quote_author_display_name,'') as quote_author_display_name, COALESCE(quote_author_avatar_url,'') as quote_author_avatar_url,
			       COALESCE(quote_body_text,'') as quote_body_text, COALESCE(quote_lang,'') as quote_lang,
			       COALESCE(quote_media_json,'') as quote_media_json, COALESCE(media_json,'') as media_json,
			       COALESCE(canonical_url,'') as canonical_url, COALESCE(reply_to_handle,'') as reply_to_handle,
			       COALESCE(reply_to_status,'') as reply_to_status,
			       COALESCE(is_reply,0) as is_reply, COALESCE(is_ghost,0) as is_ghost,
			       quote_published_at,
			       COALESCE(views,0) as views, COALESCE(likes,0) as likes, COALESCE(retweets,0) as retweets,
			       published_at, fetched_at,
			       COALESCE(content_hash,'') as content_hash, COALESCE(canonical_tweet_id,'') as canonical_tweet_id,
			       ROW_NUMBER() OVER (PARTITION BY author_handle COLLATE NOCASE ORDER BY published_at DESC) as rn
			FROM feed_items
			WHERE media_json IS NOT NULL AND media_json != '' AND media_json != '[]'
			  AND is_retweet = 0
			  %s
		)
		SELECT tweet_id, source_handle, author_handle,
		       author_display_name, author_avatar_url,
		       body_text, lang,
		       is_retweet, retweeted_by_handle,
		       retweeted_by_display_name,
		       quote_tweet_id, quote_author_handle,
		       quote_author_display_name, quote_author_avatar_url,
		       quote_body_text, quote_lang,
		       quote_media_json, media_json,
		       canonical_url, reply_to_handle,
		       reply_to_status,
		       is_reply, is_ghost,
		       quote_published_at,
		       views, likes, retweets,
		       published_at, fetched_at,
		       content_hash, canonical_tweet_id
		FROM ranked WHERE rn <= ?
		ORDER BY author_handle COLLATE NOCASE, published_at DESC
	`, handleFilter)
	args = append(args, perAuthor)

	rows, err := db.conn.Query(query, args...)
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

	result := make(map[string][]model.FeedItem)
	for i := range items {
		handle := strings.ToLower(items[i].AuthorHandle)
		result[handle] = append(result[handle], items[i])
	}
	return result, nil
}

// GetVideoCount returns the total count of videos matching the filter.
func (db *DB) GetVideoCount(opts GetVideosOpts) (int, error) {
	var where []string
	var args []any
	momentsMode := NormalizeMomentsTab(opts.MomentsMode)
	isMomentsQuery := opts.Platform == "shorts" && opts.MomentsMode != ""
	includeMomentReposts := isMomentsQuery && momentsMode == "all" && db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := isMomentsQuery && momentsMode == "all" && db.InstagramIncludeTaggedEnabled()
	includeSourceWindows := includeMomentReposts || includeInstagramTagged

	if opts.ChannelID != "" {
		where = append(where, "v.channel_id = ?")
		args = append(args, opts.ChannelID)
	}
	if opts.SourceKind != "" {
		where = append(where, "COALESCE(v.source_kind, '') = ?")
		args = append(args, opts.SourceKind)
	} else {
		where = append(where, "COALESCE(v.source_kind, '') != 'story'")
	}
	if opts.Platform == "shorts" {
		where = append(where, "COALESCE(c.platform,'') IN ('tiktok','instagram')")
		if isMomentsQuery {
			if includeSourceWindows {
				where = append(where, `(cf.channel_id IS NOT NULL OR EXISTS (
					SELECT 1
					FROM video_repost_sources vrs
					INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id AND rcf.user_id = ''
					LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
					WHERE vrs.video_id = v.video_id
					  AND COALESCE(rcs.include_reposts, 1) != 0
					  AND `+sourceWindowPlatformEnabledClause("v", includeMomentReposts, includeInstagramTagged)+`
				))`)
			} else {
				where = append(where, "cf.channel_id IS NOT NULL")
			}
		}
	} else if opts.Platform != "" {
		where = append(where, "v.channel_id IN (SELECT channel_id FROM channels WHERE platform = ?)")
		args = append(args, opts.Platform)
	}
	if opts.Search != "" {
		where = append(where, "(v.title LIKE ? OR v.description LIKE ?)")
		like := "%" + opts.Search + "%"
		args = append(args, like, like)
	}
	if opts.PublishedAfterMs > 0 {
		where = append(where, "COALESCE(v.published_at,0) >= ?")
		args = append(args, opts.PublishedAfterMs)
	}
	if !includeUndownloadedVideoRows(opts) {
		where = append(where, "v.file_path IS NOT NULL AND v.file_path <> ''")
	}
	if !opts.IncludeTemp {
		where = append(where, "COALESCE(v.is_temp,0) = 0")
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		%s
	`, whereClause)

	var count int
	err := db.conn.QueryRow(query, args...).Scan(&count)
	return count, err
}

// GetShortsOrdinal returns the 1-based position of a short in the same order
// used by the web Moments page for the requested tab.
//
// Moments is intentionally oldest -> newest so scrolling forward moves through
// time and new rows append at the end. If the page/card query order changes,
// this comparison must change with it or resume page hints will land wrong.
func (db *DB) GetShortsOrdinal(videoID, momentsMode string) (int, bool, error) {
	momentsMode = NormalizeMomentsTab(momentsMode)
	includeMomentReposts := momentsMode == "all" && db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := momentsMode == "all" && db.InstagramIncludeTaggedEnabled()
	includeSourceWindows := includeMomentReposts || includeInstagramTagged
	visibility := "cf.channel_id IS NOT NULL"

	var ordinal int
	if !includeSourceWindows {
		err := db.conn.QueryRow(`
			WITH visible AS (
				SELECT v.video_id, COALESCE(v.published_at, 0) AS effective_moment_at_ms
				FROM videos v
				LEFT JOIN channels c ON v.channel_id = c.channel_id
				LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
				WHERE COALESCE(c.platform, '') IN ('tiktok','instagram')
				  AND COALESCE(v.source_kind, '') != 'story'
				  AND COALESCE(v.is_temp,0) = 0
				  AND `+visibility+`
			),
			target AS (
				SELECT effective_moment_at_ms, video_id
				FROM visible
				WHERE video_id = ?
				LIMIT 1
			)
			SELECT COUNT(*)
			FROM visible v
			CROSS JOIN target t
			WHERE v.effective_moment_at_ms < t.effective_moment_at_ms
			   OR (v.effective_moment_at_ms = t.effective_moment_at_ms AND v.video_id <= t.video_id)
		`, videoID).Scan(&ordinal)
		if err != nil {
			return 0, false, err
		}
		return ordinal, ordinal > 0, nil
	}
	visibility = "(cf.channel_id IS NOT NULL OR mr.video_id IS NOT NULL)"

	err := db.conn.QueryRow(`
		WITH allowed_moment_reposts AS (
			SELECT vrs.*,
			       COUNT(*) OVER (PARTITION BY vrs.video_id) AS repost_count,
			       ROW_NUMBER() OVER (
			           PARTITION BY vrs.video_id
			           ORDER BY COALESCE(NULLIF(vrs.reposted_at_ms, 0), vrs.first_seen_at_ms) DESC,
			                    vrs.reposter_channel_id ASC
			       ) AS rn
			FROM video_repost_sources vrs
			INNER JOIN videos owner ON owner.video_id = vrs.video_id
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id AND rcf.user_id = ''
			LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
			WHERE COALESCE(rcs.include_reposts, 1) != 0
			  AND `+sourceWindowPlatformEnabledClause("owner", includeMomentReposts, includeInstagramTagged)+`
		),
		moments_repost_heads AS (
			SELECT *
			FROM allowed_moment_reposts
			WHERE rn = 1
		),
		visible AS (
			SELECT v.video_id,
			       CASE WHEN cf.channel_id IS NULL AND mr.video_id IS NOT NULL
			            THEN COALESCE(NULLIF(mr.reposted_at_ms, 0), NULLIF(mr.first_seen_at_ms, 0), v.published_at, 0)
			            ELSE COALESCE(v.published_at, 0)
			        END AS effective_moment_at_ms
			FROM videos v
			LEFT JOIN channels c ON v.channel_id = c.channel_id
			LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
			LEFT JOIN moments_repost_heads mr ON mr.video_id = v.video_id
			WHERE COALESCE(c.platform, '') IN ('tiktok','instagram')
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND COALESCE(v.is_temp,0) = 0
			  AND `+visibility+`
		),
		target AS (
			SELECT effective_moment_at_ms, video_id
			FROM visible
			WHERE video_id = ?
			LIMIT 1
		)
		SELECT COUNT(*)
		FROM visible v
		CROSS JOIN target t
		WHERE v.effective_moment_at_ms < t.effective_moment_at_ms
		   OR (v.effective_moment_at_ms = t.effective_moment_at_ms AND v.video_id <= t.video_id)
	`, videoID).Scan(&ordinal)
	if err != nil {
		return 0, false, err
	}
	return ordinal, ordinal > 0, nil
}

// GetNearestShortsCursorTarget returns the visible Moments row closest to the
// hidden cursor target's timeline position. Forward progress is preferred: the
// first visible item at or after the old cursor wins, otherwise the previous
// visible item is used. This keeps stale cursors from falling back to the oldest
// row when an account is unfollowed or a source-window row disappears.
func (db *DB) GetNearestShortsCursorTarget(videoID, momentsMode string, sortAtHint int64) (string, int, bool, error) {
	sortAt := sortAtHint
	if sortAt <= 0 {
		var ok bool
		var err error
		sortAt, ok, err = db.GetShortsCursorSortAt(videoID, momentsMode)
		if err != nil || !ok {
			return "", 0, false, err
		}
	}
	return db.GetNearestShortsOrdinal(sortAt, momentsMode)
}

func (db *DB) GetShortsVisibleSortAt(videoID, momentsMode string) (int64, bool, error) {
	momentsMode = NormalizeMomentsTab(momentsMode)
	query := db.shortsVisibleCTE(momentsMode) + `
		SELECT effective_moment_at_ms
		FROM visible
		WHERE video_id = ?
		LIMIT 1`
	var sortAt int64
	err := db.conn.QueryRow(query, videoID).Scan(&sortAt)
	if err == nil {
		return sortAt, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	return 0, false, nil
}

func (db *DB) GetShortsCursorSortAt(videoID, momentsMode string) (int64, bool, error) {
	sortAt, ok, err := db.GetShortsVisibleSortAt(videoID, momentsMode)
	if err != nil || ok {
		return sortAt, ok, err
	}
	err = db.conn.QueryRow(`
		SELECT COALESCE(v.published_at, 0)
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		WHERE v.video_id = ?
		  AND COALESCE(c.platform, '') IN ('tiktok','instagram')
		  AND COALESCE(v.source_kind, '') != 'story'
		  AND COALESCE(v.is_temp,0) = 0
		LIMIT 1
	`, videoID).Scan(&sortAt)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return sortAt, true, nil
}

func (db *DB) GetNearestShortsOrdinal(sortAt int64, momentsMode string) (string, int, bool, error) {
	query := db.shortsVisibleCTE(momentsMode) + `
		, candidate AS (
			SELECT video_id, effective_moment_at_ms
			FROM visible
			ORDER BY CASE WHEN effective_moment_at_ms >= ? THEN 0 ELSE 1 END,
			         CASE WHEN effective_moment_at_ms >= ? THEN effective_moment_at_ms END ASC,
			         CASE WHEN effective_moment_at_ms < ? THEN effective_moment_at_ms END DESC,
			         CASE WHEN effective_moment_at_ms >= ? THEN video_id END ASC,
			         CASE WHEN effective_moment_at_ms < ? THEN video_id END DESC
			LIMIT 1
		)
		SELECT c.video_id, COUNT(*)
		FROM candidate c
		JOIN visible v ON v.effective_moment_at_ms < c.effective_moment_at_ms
		              OR (v.effective_moment_at_ms = c.effective_moment_at_ms AND v.video_id <= c.video_id)
		GROUP BY c.video_id`
	var videoID string
	var ordinal int
	err := db.conn.QueryRow(query, sortAt, sortAt, sortAt, sortAt, sortAt).Scan(&videoID, &ordinal)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return videoID, ordinal, ordinal > 0, nil
}

func (db *DB) shortsVisibleCTE(momentsMode string) string {
	momentsMode = NormalizeMomentsTab(momentsMode)
	includeMomentReposts := momentsMode == "all" && db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := momentsMode == "all" && db.InstagramIncludeTaggedEnabled()
	includeSourceWindows := includeMomentReposts || includeInstagramTagged
	if !includeSourceWindows {
		return `
		WITH visible AS (
			SELECT v.video_id, COALESCE(v.published_at, 0) AS effective_moment_at_ms
			FROM videos v
			LEFT JOIN channels c ON v.channel_id = c.channel_id
			LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
			WHERE COALESCE(c.platform, '') IN ('tiktok','instagram')
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND COALESCE(v.is_temp,0) = 0
			  AND cf.channel_id IS NOT NULL
		)`
	}
	return `
		WITH allowed_moment_reposts AS (
			SELECT vrs.*,
			       COUNT(*) OVER (PARTITION BY vrs.video_id) AS repost_count,
			       ROW_NUMBER() OVER (
			           PARTITION BY vrs.video_id
			           ORDER BY COALESCE(NULLIF(vrs.reposted_at_ms, 0), vrs.first_seen_at_ms) DESC,
			                    vrs.reposter_channel_id ASC
			       ) AS rn
			FROM video_repost_sources vrs
			INNER JOIN videos owner ON owner.video_id = vrs.video_id
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id AND rcf.user_id = ''
			LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
			WHERE COALESCE(rcs.include_reposts, 1) != 0
			  AND ` + sourceWindowPlatformEnabledClause("owner", includeMomentReposts, includeInstagramTagged) + `
		),
		moments_repost_heads AS (
			SELECT *
			FROM allowed_moment_reposts
			WHERE rn = 1
		),
		visible AS (
			SELECT v.video_id,
			       CASE WHEN cf.channel_id IS NULL AND mr.video_id IS NOT NULL
			            THEN COALESCE(NULLIF(mr.reposted_at_ms, 0), NULLIF(mr.first_seen_at_ms, 0), v.published_at, 0)
			            ELSE COALESCE(v.published_at, 0)
			        END AS effective_moment_at_ms
			FROM videos v
			LEFT JOIN channels c ON v.channel_id = c.channel_id
			LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
			LEFT JOIN moments_repost_heads mr ON mr.video_id = v.video_id
			WHERE COALESCE(c.platform, '') IN ('tiktok','instagram')
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND COALESCE(v.is_temp,0) = 0
			  AND (cf.channel_id IS NOT NULL OR mr.video_id IS NOT NULL)
		)`
}

// GetComments returns comments for a video, sorted by like count.
func (db *DB) GetComments(videoID string, limit int) ([]model.Comment, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.conn.Query(`
		SELECT id, video_id, comment_id, COALESCE(parent_id,''),
		       COALESCE(author_name,''), COALESCE(author_id,''),
		       COALESCE(author_thumbnail,''), COALESCE(text,''),
		       COALESCE(like_count,0),
		       published_at, COALESCE(platform,'youtube'), fetched_at
		FROM video_comments
		WHERE video_id = ?
		ORDER BY like_count DESC, id ASC
		LIMIT ?
	`, videoID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var comments []model.Comment
	for rows.Next() {
		var c model.Comment
		var publishedAt, fetchedAt sql.NullInt64
		err := rows.Scan(
			&c.ID, &c.VideoID, &c.CommentID, &c.ParentID,
			&c.AuthorName, &c.AuthorID,
			&c.AuthorThumbnail, &c.Text,
			&c.LikeCount,
			&publishedAt, &c.Platform, &fetchedAt,
		)
		if err != nil {
			return nil, err
		}
		c.PublishedAt = millisToTimePtr(publishedAt)
		if t := millisToTimePtr(fetchedAt); t != nil {
			c.FetchedAt = *t
		}
		c.SetPublishedAtMs()
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// DeleteComments removes all comments for a video. Returns count deleted.
func (db *DB) DeleteComments(videoID string) (int, error) {
	var deleted int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec("DELETE FROM video_comments WHERE video_id = ?", videoID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		deleted = int(n)
		if deleted > 0 {
			if _, err := tx.Exec(`UPDATE videos SET sync_seq = ? WHERE video_id = ?`, db.NextSyncSeq(), videoID); err != nil {
				return err
			}
		}
		return nil
	})
	return deleted, err
}

// AddComments inserts comments for a video. Returns count inserted.
func (db *DB) AddComments(videoID string, comments []CommentInput, platform string) (int, error) {
	if len(comments) == 0 {
		return 0, nil
	}
	var inserted int
	err := db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		stmt, err := tx.Prepare(`
			INSERT INTO video_comments (
				video_id, comment_id, parent_id, author_name, author_id,
				author_thumbnail, text, like_count, published_at, platform, fetched_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(video_id, comment_id) DO NOTHING
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = stmt.Close()
		}()
		for _, c := range comments {
			var publishedAtMs int64
			if c.Timestamp > 0 {
				publishedAtMs = c.Timestamp * 1000
			}
			res, err := stmt.Exec(
				videoID, c.CommentID, nilIfEmpty(c.ParentID),
				c.Author, c.AuthorID, c.AuthorThumbnail,
				c.Text, c.LikeCount, publishedAtMs,
				platform, nowMs,
			)
			if err != nil {
				continue // skip individual failures
			}
			n, _ := res.RowsAffected()
			inserted += int(n)
		}
		if inserted > 0 {
			if _, err := tx.Exec(`UPDATE videos SET sync_seq = ? WHERE video_id = ?`, db.NextSyncSeq(), videoID); err != nil {
				return err
			}
		}
		return nil
	})
	return inserted, err
}

// CommentInput holds raw comment data from yt-dlp for insertion.
type CommentInput struct {
	CommentID       string
	ParentID        string
	Author          string
	AuthorID        string
	AuthorThumbnail string
	Text            string
	LikeCount       int
	Timestamp       int64
}

// GetPlaybackPosition returns the saved playback position for a video.
func (db *DB) GetPlaybackPosition(videoID, userID string) (float64, error) {
	var pos float64
	err := db.conn.QueryRow(
		"SELECT COALESCE(playback_position,0) FROM watch_history WHERE video_id = ? AND user_id = ?",
		videoID, userID,
	).Scan(&pos)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return pos, err
}

// GetPlaybackPositions returns saved playback positions for a batch of video IDs.
func (db *DB) GetPlaybackPositions(videoIDs []string, userID string) (map[string]float64, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}
	ph := strings.Repeat("?,", len(videoIDs))
	ph = ph[:len(ph)-1]
	args := make([]any, 0, len(videoIDs)+1)
	args = append(args, userID)
	for _, id := range videoIDs {
		args = append(args, id)
	}
	rows, err := db.conn.Query(
		"SELECT video_id, COALESCE(playback_position,0) FROM watch_history WHERE user_id = ? AND video_id IN ("+ph+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	m := make(map[string]float64, len(videoIDs))
	for rows.Next() {
		var id string
		var pos float64
		if err := rows.Scan(&id, &pos); err != nil {
			return nil, err
		}
		if pos > 0 {
			m[id] = pos
		}
	}
	return m, rows.Err()
}

// GetChannel returns a single channel by channel_id. Per-channel settings
// (max_videos, download_subtitles, media_*, include_reposts) live in
// channel_settings and are fetched separately via GetChannelSettings.
func (db *DB) GetChannel(channelID string) (*model.Channel, error) {
	row := db.conn.QueryRow(`
		SELECT c.id, c.channel_id, COALESCE(c.source_id,''), c.name,
		       COALESCE(c.url,''), COALESCE(c.platform,'youtube'),
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       COALESCE(c.quality,''),
		       c.last_checked, c.created_at
		FROM channels c
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		LEFT JOIN channel_stars   cs ON cs.channel_id = c.channel_id AND cs.user_id = ''
		WHERE c.channel_id = ?
	`, channelID)

	var ch model.Channel
	var lastChecked, createdAt sql.NullInt64
	var isSubscribed, isStarred int
	err := row.Scan(
		&ch.ID, &ch.ChannelID, &ch.SourceID, &ch.Name,
		&ch.URL, &ch.Platform,
		&isSubscribed, &isStarred,
		&ch.Quality,
		&lastChecked, &createdAt,
	)
	ch.IsSubscribed = isSubscribed != 0
	ch.IsStarred = isStarred != 0
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ch.LastChecked = millisToTimePtr(lastChecked)
	if t := millisToTimePtr(createdAt); t != nil {
		ch.CreatedAt = *t
	}
	ch.Platform = detectPlatform(ch.Platform, ch.URL)
	return &ch, nil
}

// ThumbnailRow is a minimal struct for cache warming.
type ThumbnailRow struct {
	VideoID       string
	FilePath      string
	ThumbnailPath string
}

// QueryVideoThumbnails returns all video IDs with their file paths for thumbnail resolution.
func (db *DB) QueryVideoThumbnails() ([]ThumbnailRow, error) {
	rows, err := db.conn.Query(
		"SELECT video_id, COALESCE(file_path,''), COALESCE(thumbnail_path,'') FROM videos",
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var result []ThumbnailRow
	for rows.Next() {
		var r ThumbnailRow
		if err := rows.Scan(&r.VideoID, &r.FilePath, &r.ThumbnailPath); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) UpdateVideoThumbnailPath(videoID, thumbnailPath string) error {
	videoID = strings.TrimSpace(videoID)
	thumbnailPath = strings.TrimSpace(thumbnailPath)
	if videoID == "" || thumbnailPath == "" {
		return nil
	}
	if err := db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE videos
			SET thumbnail_path = ?,
			    sync_seq = ?
			WHERE video_id = ?
			  AND COALESCE(thumbnail_path, '') = ''
		`, thumbnailPath, db.NextSyncSeq(), videoID)
		return err
	}); err != nil {
		return err
	}
	return db.MaintainVideoAssets(videoID, time.Now().UnixMilli())
}

// resolveVideoPaths converts relative DB paths to absolute runtime paths.
func (db *DB) resolveVideoPaths(v *model.Video) {
	if v.FilePath != "" && !filepath.IsAbs(v.FilePath) {
		v.FilePath = filepath.Join(db.dataDir, v.FilePath)
	}
	if v.ThumbnailPath != "" && !filepath.IsAbs(v.ThumbnailPath) {
		v.ThumbnailPath = filepath.Join(db.dataDir, v.ThumbnailPath)
	}
}

// MarkWatched sets the watched flag for a video.
func (db *DB) MarkWatched(videoID string, watched bool) error {
	val := 0
	if watched {
		val = 1
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE videos SET watched = ? WHERE video_id = ?", val, videoID)
		return err
	})
}

// UpsertWatchHistoryFullyWatched marks a video as watched for a user by setting
// playback_position equal to duration (fully-played sentinel). Uses
// latest_wins_v2 — accepts the update only if updatedAtMs >= the stored value.
func (db *DB) UpsertWatchHistoryFullyWatched(userID, videoID string, updatedAtMs int64) error {
	var duration float64
	_ = db.conn.QueryRow(`SELECT COALESCE(duration, 0) FROM videos WHERE video_id = ?`, videoID).Scan(&duration)

	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO watch_history (user_id, video_id, playback_position, duration, progress_updated_at_ms, progress_source, last_watched)
			 VALUES (?, ?, ?, ?, ?, 'server', CAST(strftime('%s','now') AS INTEGER) * 1000)
			 ON CONFLICT(user_id, video_id) DO UPDATE SET
			   playback_position = excluded.playback_position,
			   duration          = excluded.duration,
			   progress_updated_at_ms = excluded.progress_updated_at_ms,
			   progress_source   = excluded.progress_source,
			   last_watched      = CAST(strftime('%s','now') AS INTEGER) * 1000
			 WHERE excluded.progress_updated_at_ms >= watch_history.progress_updated_at_ms`,
			userID, videoID, duration, duration, updatedAtMs,
		)
		return err
	})
}

// DeleteWatchHistory removes a user's watch_history row for a video (unwatched).
func (db *DB) DeleteWatchHistory(userID, videoID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM watch_history WHERE user_id = ? AND video_id = ?`, userID, videoID)
		return err
	})
}

// SetPinned sets the is_pinned flag for a video.
func (db *DB) SetPinned(videoID string, pinned bool) error {
	val := 0
	if pinned {
		val = 1
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE videos SET is_pinned = ? WHERE video_id = ?", val, videoID)
		return err
	})
}

// TogglePinned flips is_pinned for a video and returns the new state.
func (db *DB) TogglePinned(videoID string) (bool, error) {
	var next bool
	err := db.WithWrite(func(tx *sql.Tx) error {
		var cur int
		if err := tx.QueryRow(
			"SELECT COALESCE(is_pinned, 0) FROM videos WHERE video_id = ?",
			videoID,
		).Scan(&cur); err != nil {
			return err
		}
		nextVal := 1 - cur
		next = nextVal == 1
		_, err := tx.Exec("UPDATE videos SET is_pinned = ? WHERE video_id = ?", nextVal, videoID)
		return err
	})
	return next, err
}

// ProgressResult holds the outcome of a SaveProgress call.
type ProgressResult struct {
	Accepted            bool
	ResolvedPosition    float64
	ResolvedUpdatedAtMs int64
	SyncVersion         int64
}

// SaveProgress saves watch progress using latest_wins_v2 conflict resolution.
func (db *DB) SaveProgress(userID, videoID string, position, duration float64, updatedAtMs int64, source string) (ProgressResult, error) {
	if updatedAtMs == 0 {
		updatedAtMs = time.Now().UnixMilli()
	}
	if position < 0 {
		position = 0
	}
	if duration < 0 {
		duration = 0
	}

	var result ProgressResult

	err := db.WithWrite(func(tx *sql.Tx) error {
		var existingPos, existingDur float64
		var existingTs int64
		err := tx.QueryRow(
			"SELECT playback_position, duration, COALESCE(progress_updated_at_ms, 0) FROM watch_history WHERE user_id = ? AND video_id = ?",
			userID, videoID,
		).Scan(&existingPos, &existingDur, &existingTs)

		if err == sql.ErrNoRows {
			// No existing record — insert new
			effectiveDur := duration
			_, err = tx.Exec(`
				INSERT INTO watch_history (user_id, video_id, playback_position, duration, progress_updated_at_ms, progress_source, last_watched)
				VALUES (?, ?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
			`, userID, videoID, position, effectiveDur, updatedAtMs, source)
			if err != nil {
				return err
			}
			result.Accepted = true
			result.ResolvedPosition = position
			result.ResolvedUpdatedAtMs = updatedAtMs
			valueJSON := fmt.Sprintf(`{"position":%f,"duration":%f,"ts":%d,"source":%q}`, position, effectiveDur, updatedAtMs, source)
			return db.recordSyncChangeTx(tx, "watch_progress", videoID, valueJSON)
		}
		if err != nil {
			return err
		}

		if updatedAtMs >= existingTs {
			// Accept: incoming is newer or same time
			effectiveDur := duration
			if effectiveDur <= 0 {
				effectiveDur = existingDur
			}
			_, err = tx.Exec(`
				UPDATE watch_history SET playback_position = ?, duration = ?, progress_updated_at_ms = ?, progress_source = ?, last_watched = CAST(strftime('%s','now') AS INTEGER) * 1000
				WHERE user_id = ? AND video_id = ?
			`, position, effectiveDur, updatedAtMs, source, userID, videoID)
			if err != nil {
				return err
			}
			result.Accepted = true
			result.ResolvedPosition = position
			result.ResolvedUpdatedAtMs = updatedAtMs
			valueJSON := fmt.Sprintf(`{"position":%f,"duration":%f,"ts":%d,"source":%q}`, position, effectiveDur, updatedAtMs, source)
			return db.recordSyncChangeTx(tx, "watch_progress", videoID, valueJSON)
		}

		// Reject: incoming is older
		result.Accepted = false
		result.ResolvedPosition = existingPos
		result.ResolvedUpdatedAtMs = existingTs
		return nil
	})
	if err != nil {
		return result, err
	}
	result.SyncVersion, _ = db.GetCurrentSyncVersion()
	return result, nil
}

// SponsorBlockSegment is a cached SponsorBlock segment for a video.
type SponsorBlockSegment struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Category string  `json:"category"`
}

// GetVideoStats returns unwatched count and total file size for downloaded videos.
func (db *DB) GetVideoStats() (unwatched int, totalBytes int64, err error) {
	err = db.conn.QueryRow(`
		SELECT
			COUNT(CASE WHEN COALESCE(watched,0)=0 THEN 1 END),
			COALESCE(SUM(COALESCE(file_size,0)),0)
		FROM videos
		WHERE file_path IS NOT NULL AND file_path <> ''
	`).Scan(&unwatched, &totalBytes)
	return
}

// DeleteVideo deletes a video record by video_id. Returns error if not found.
func (db *DB) DeleteVideo(videoID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec("DELETE FROM videos WHERE video_id = ?", videoID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("video not found: %s", videoID)
		}
		// Also remove any media_files entries owned by this video
		_, err = tx.Exec("DELETE FROM media_files WHERE owner_id = ?", videoID)
		return err
	})
}

// InsertVideo inserts or replaces a video record in the videos table.
// publishedAtMs is unix-millis; pass 0 when unknown. sync_seq bumps on
// every write so the videos/delta + shorts/delta endpoints pick it up
// on the next cursor advance (#6).
func (db *DB) InsertVideo(videoID, channelID, title, description string,
	duration int, thumbnailPath, filePath string, fileSize int64,
	publishedAtMs int64, metadataJSON, mediaKind string, slideCount int, isTemp bool) error {
	return db.InsertVideoWithSourceKind(videoID, channelID, title, description, duration, thumbnailPath, filePath, fileSize, publishedAtMs, metadataJSON, mediaKind, slideCount, isTemp, "")
}

func (db *DB) InsertVideoWithSourceKind(videoID, channelID, title, description string,
	duration int, thumbnailPath, filePath string, fileSize int64,
	publishedAtMs int64, metadataJSON, mediaKind string, slideCount int, isTemp bool, sourceKind string) error {
	seq := db.NextSyncSeq()
	if err := db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO videos
				(video_id, channel_id, title, description, duration,
				 thumbnail_path, file_path, file_size, published_at,
				 metadata_json, media_kind, slide_count, source_kind, is_temp, downloaded_at, sync_seq)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000, ?)
			ON CONFLICT(video_id) DO UPDATE SET
				channel_id = CASE
					WHEN excluded.channel_id != '' THEN excluded.channel_id
					ELSE videos.channel_id
				END,
				title = CASE
					WHEN excluded.title != '' THEN excluded.title
					ELSE videos.title
				END,
				description = CASE
					WHEN excluded.description != '' THEN excluded.description
					ELSE videos.description
				END,
				duration = CASE
					WHEN excluded.duration > 0 THEN excluded.duration
					ELSE videos.duration
				END,
				thumbnail_path = CASE
					WHEN excluded.thumbnail_path != '' THEN excluded.thumbnail_path
					ELSE videos.thumbnail_path
				END,
				file_path = CASE
					WHEN excluded.file_path != '' THEN excluded.file_path
					ELSE videos.file_path
				END,
				file_size = CASE
					WHEN excluded.file_size > 0 THEN excluded.file_size
					ELSE videos.file_size
				END,
				published_at = CASE
					WHEN excluded.published_at > 0 THEN excluded.published_at
					ELSE videos.published_at
				END,
				metadata_json = CASE
					WHEN excluded.metadata_json != '' THEN excluded.metadata_json
					ELSE videos.metadata_json
				END,
				media_kind = CASE
					WHEN excluded.media_kind != '' THEN excluded.media_kind
					ELSE videos.media_kind
				END,
				slide_count = CASE
					WHEN excluded.slide_count > 0 THEN excluded.slide_count
					WHEN excluded.media_kind = 'slideshow' THEN videos.slide_count
					ELSE excluded.slide_count
				END,
				source_kind = CASE
					WHEN excluded.source_kind != '' THEN excluded.source_kind
					ELSE COALESCE(videos.source_kind, '')
				END,
				is_temp = excluded.is_temp,
				downloaded_at = excluded.downloaded_at,
				sync_seq = excluded.sync_seq
		`, videoID, channelID, title, description, duration,
			thumbnailPath, filePath, fileSize, publishedAtMs,
			metadataJSON, mediaKind, slideCount, sourceKind, isTemp, seq)
		return err
	}); err != nil {
		return err
	}
	return db.MaintainVideoAssets(videoID, time.Now().UnixMilli())
}

// SBCheckedRow represents a row from sponsorblock_checked.
type SBCheckedRow struct {
	VideoID         string
	CheckedAtMs     int64
	VideoAgeAtCheck string
}

// GetSponsorBlockChecked returns the check record for a video, or nil if unchecked.
func (db *DB) GetSponsorBlockChecked(videoID string) (*SBCheckedRow, error) {
	var row SBCheckedRow
	err := db.conn.QueryRow(
		"SELECT video_id, checked_at, COALESCE(video_age_at_check,'') FROM sponsorblock_checked WHERE video_id = ?",
		videoID,
	).Scan(&row.VideoID, &row.CheckedAtMs, &row.VideoAgeAtCheck)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// MarkSponsorBlockChecked records that a video was checked for SponsorBlock segments.
// Bumps videos.sync_seq so Android's videos delta re-emits the row with the
// updated sponsorblock_checked attachment.
func (db *DB) MarkSponsorBlockChecked(videoID, ageLabel string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO sponsorblock_checked (video_id, checked_at, video_age_at_check)
			VALUES (?, CAST(strftime('%s','now') AS INTEGER) * 1000, ?)
			ON CONFLICT(video_id) DO UPDATE SET
				checked_at = CAST(strftime('%s','now') AS INTEGER) * 1000,
				video_age_at_check = excluded.video_age_at_check
		`, videoID, ageLabel); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE videos SET sync_seq = ? WHERE video_id = ?`, db.NextSyncSeq(), videoID)
		return err
	})
}

// SaveSponsorBlockSegments replaces all segments for a video. Bumps
// videos.sync_seq so Android's videos delta re-emits the row with the new
// sponsorblock_segments attachment.
func (db *DB) SaveSponsorBlockSegments(videoID string, segments []SponsorBlockSegment) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM sponsorblock_segments WHERE video_id = ?", videoID); err != nil {
			return err
		}
		if len(segments) > 0 {
			stmt, err := tx.Prepare(
				"INSERT INTO sponsorblock_segments (video_id, start_time, end_time, category) VALUES (?, ?, ?, ?)",
			)
			if err != nil {
				return err
			}
			defer func() {
				_ = stmt.Close()
			}()
			for _, s := range segments {
				if _, err := stmt.Exec(videoID, s.Start, s.End, s.Category); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(`UPDATE videos SET sync_seq = ? WHERE video_id = ?`, db.NextSyncSeq(), videoID)
		return err
	})
}

// GetSponsorBlockSegments returns cached SponsorBlock segments for a video.
func (db *DB) GetSponsorBlockSegments(videoID string) ([]SponsorBlockSegment, error) {
	rows, err := db.conn.Query(
		"SELECT start_time, end_time, category FROM sponsorblock_segments WHERE video_id = ?",
		videoID,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	segments := []SponsorBlockSegment{}
	for rows.Next() {
		var s SponsorBlockSegment
		if err := rows.Scan(&s.Start, &s.End, &s.Category); err != nil {
			return nil, err
		}
		segments = append(segments, s)
	}
	return segments, rows.Err()
}

// PreviewCandidate is a minimal video record for preview backfill.
type PreviewCandidate struct {
	VideoID  string
	FilePath string
	Duration float64
}

// LatestVideoFileForChannel returns the newest downloaded video's ID and
// file_path for the given channel, or ("", "", nil) if nothing is cached.
// Used by the profile worker to synthesize a banner from a video cover on
// short-form platforms that have no native profile banner.
func (db *DB) LatestVideoFileForChannel(channelID string) (videoID, filePath string, err error) {
	row := db.conn.QueryRow(`
		SELECT video_id, COALESCE(file_path, '')
		FROM videos
		WHERE channel_id = ?
		  AND COALESCE(file_path, '') != ''
		  AND COALESCE(is_temp, 0) = 0
		ORDER BY published_at DESC, id DESC
		LIMIT 1
	`, channelID)
	if err := row.Scan(&videoID, &filePath); err != nil {
		if err == sql.ErrNoRows {
			return "", "", nil
		}
		return "", "", err
	}
	return videoID, filePath, nil
}

// GetPreviewCandidates returns downloaded videos with duration > 0 for preview backfill.
func (db *DB) GetPreviewCandidates() ([]PreviewCandidate, error) {
	rows, err := db.conn.Query(`
		SELECT v.video_id, v.file_path, CAST(v.duration AS REAL)
		FROM videos v
		WHERE v.file_path IS NOT NULL AND v.file_path <> ''
		  AND v.duration > 0
		  AND v.is_temp = 0
		ORDER BY v.downloaded_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var candidates []PreviewCandidate
	for rows.Next() {
		var c PreviewCandidate
		if err := rows.Scan(&c.VideoID, &c.FilePath, &c.Duration); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(c.FilePath) {
			c.FilePath = filepath.Join(db.dataDir, c.FilePath)
		}
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}
