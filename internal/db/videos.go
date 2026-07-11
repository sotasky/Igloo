package db

import (
	"database/sql"
	"fmt"
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

func hydrateVideoPlatform(video *model.Video) {
	video.Platform = detectPlatform(video.Platform, "")
	if video.Platform != "" {
		return
	}
	video.Platform, _ = videoPlatformForOwnerKind(video.OwnerKind)
}

func videoFullyWatchedSQL(alias string) string {
	return `EXISTS (
		SELECT 1
		FROM watch_history wh_watched
		WHERE wh_watched.video_id = ` + alias + `.video_id
		  AND COALESCE(wh_watched.duration, 0) > 0
		  AND COALESCE(wh_watched.playback_position, 0) >= wh_watched.duration * 0.95
	)`
}

// GetVideo returns a single video by ID with joined channel info.
func (db *DB) GetVideo(videoID string) (*model.Video, error) {
	row := db.conn.QueryRow(`
		SELECT v.id, v.video_id, v.channel_id, v.owner_kind, v.title, COALESCE(v.description,''),
		       COALESCE(v.duration,0), v.published_at, v.downloaded_at,
		       CASE WHEN `+videoFullyWatchedSQL("v")+` THEN 1 ELSE 0 END,
		       COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       COALESCE(v.metadata_json,''),
		       COALESCE(v.media_kind,''), COALESCE(v.slide_count,0), COALESCE(v.source_kind,''),
		       COALESCE(cp.display_name,''),
		       COALESCE(c.platform,''),
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_checked_at
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = v.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
		LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id
		WHERE v.video_id = ?
	`, videoID)

	var v model.Video
	var publishedAt, downloadedAt sql.NullInt64
	err := row.Scan(
		&v.ID, &v.VideoID, &v.ChannelID, &v.OwnerKind, &v.Title, &v.Description,
		&v.Duration, &publishedAt, &downloadedAt,
		&v.Watched, &v.IsTemp, &v.IsPinned,
		&v.MetadataJSON,
		&v.MediaKind, &v.MediaSlideCount, &v.SourceKind,
		&v.ChannelName, &v.Platform,
		&v.IsStarred, &v.IsSubscribed,
		&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowCheckedAtMs,
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
	hydrateVideoPlatform(&v)
	return &v, nil
}

// GetNextVideo returns the video with the closest earlier published_at,
// used for "next in line" in the player sidebar.
func (db *DB) GetNextVideo(videoID string) (*model.Video, error) {
	row := db.conn.QueryRow(`
		SELECT v.id, v.video_id, v.channel_id, v.owner_kind, v.title, COALESCE(v.description,''),
		       COALESCE(v.duration,0), v.published_at, v.downloaded_at,
		       CASE WHEN `+videoFullyWatchedSQL("v")+` THEN 1 ELSE 0 END,
		       COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       COALESCE(v.metadata_json,''),
		       COALESCE(v.media_kind,''), COALESCE(v.slide_count,0),
		       COALESCE(cp.display_name,''),
		       COALESCE(c.platform,''),
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_checked_at
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = v.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
		LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id
		WHERE v.published_at < (SELECT published_at FROM videos WHERE video_id = ?)
		  AND `+readyVideoMediaExistsSQL("v")+`
		  AND c.platform = 'youtube'
		ORDER BY v.published_at DESC
		LIMIT 1
	`, videoID)

	var v model.Video
	var publishedAt, downloadedAt sql.NullInt64
	err := row.Scan(
		&v.ID, &v.VideoID, &v.ChannelID, &v.OwnerKind, &v.Title, &v.Description,
		&v.Duration, &publishedAt, &downloadedAt,
		&v.Watched, &v.IsTemp, &v.IsPinned,
		&v.MetadataJSON,
		&v.MediaKind, &v.MediaSlideCount,
		&v.ChannelName, &v.Platform,
		&v.IsStarred, &v.IsSubscribed,
		&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowCheckedAtMs,
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
	hydrateVideoPlatform(&v)
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
		where = append(where, readyVideoMediaExistsSQL("v"))
	}
	if !opts.IncludeTemp {
		where = append(where, "COALESCE(v.is_temp,0) = 0")
	}
	if opts.UnwatchedOnly {
		where = append(where, "NOT "+videoFullyWatchedSQL("v"))
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
			FROM video_repost_sources_resolved vrs
			INNER JOIN videos owner ON owner.video_id = vrs.video_id
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
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
		       CASE WHEN mr.video_id IS NOT NULL THEN 1 ELSE 0 END AS repost_introduced,
		       CASE WHEN mr.video_id IS NOT NULL
		            THEN COALESCE(NULLIF(mr.reposted_at_ms, 0), NULLIF(mr.first_seen_at_ms, 0), v.published_at, 0)
		            ELSE COALESCE(v.published_at, 0)
		        END AS effective_moment_at_ms`
		orderExpr = "effective_moment_at_ms"
	}

	query := fmt.Sprintf(`
		%s
		SELECT v.id, v.video_id, v.channel_id, v.owner_kind, v.title, COALESCE(v.description,''),
		       COALESCE(v.duration,0), v.published_at, v.downloaded_at,
		       CASE WHEN %s THEN 1 ELSE 0 END,
		       COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       %s,
		       COALESCE(v.media_kind,''), COALESCE(v.slide_count,0), COALESCE(v.source_kind,''),
		       COALESCE(cp.display_name,''),
		       COALESCE(c.platform,''),
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       b.category_id,
		       %s,
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_checked_at
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = v.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
		LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id
		LEFT JOIN bookmarks b ON b.video_id = v.video_id
		%s
		%s
		ORDER BY %s %s, v.video_id %s
		LIMIT ? OFFSET ?
	`, withClause, videoFullyWatchedSQL("v"), metadataCol, repostCols, repostJoin, whereClause, orderExpr, orderDir, orderDir)
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
			&v.ID, &v.VideoID, &v.ChannelID, &v.OwnerKind, &v.Title, &v.Description,
			&v.Duration, &publishedAt, &downloadedAt,
			&v.Watched, &v.IsTemp, &v.IsPinned,
			&v.MetadataJSON,
			&v.MediaKind, &v.MediaSlideCount, &v.SourceKind,
			&v.ChannelName, &v.Platform,
			&v.IsStarred, &v.IsSubscribed,
			&bookmarkCatID,
			&v.ReposterChannelID, &v.ReposterHandle, &v.ReposterDisplayName,
			&v.RepostCount, &repostIntroduced, &v.EffectiveMomentAtMs,
			&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowCheckedAtMs,
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
		hydrateVideoPlatform(&v)
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
			SELECT v.id, v.video_id, v.channel_id, v.owner_kind, v.title, COALESCE(v.description,'') as description,
			       COALESCE(v.duration,0) AS duration, v.published_at, v.downloaded_at,
			       CASE WHEN %s THEN 1 ELSE 0 END as watched,
			       COALESCE(v.is_temp,0) as is_temp, COALESCE(v.is_pinned,0) as is_pinned,
			       '' as metadata_json,
			       COALESCE(v.media_kind,'') as media_kind, COALESCE(v.slide_count,0) as slide_count,
			       COALESCE(cp.display_name,'') as channel_name,
			       COALESCE(c.platform,'') as platform,
			       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END as is_starred,
			       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END as is_subscribed,
			       v.dearrow_title, v.dearrow_title_casual, v.dearrow_checked_at,
			       ROW_NUMBER() OVER (PARTITION BY v.channel_id ORDER BY v.published_at DESC) as rn
			FROM videos v
			LEFT JOIN channels c ON v.channel_id = c.channel_id
			LEFT JOIN channel_profiles cp ON cp.channel_id = v.channel_id
			INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id
			LEFT JOIN channel_stars cs ON cs.channel_id = c.channel_id
			WHERE COALESCE(v.is_temp,0) = 0
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND (
			       COALESCE(c.platform,'youtube') IN ('tiktok','instagram')
			       OR %s
			  )
			  AND COALESCE(c.platform,'youtube') != 'twitter'
			  %s
		)
		SELECT id, video_id, channel_id, owner_kind, title, description,
		       duration, published_at, downloaded_at,
		       watched, is_temp, is_pinned,
		       metadata_json,
		       media_kind, slide_count,
		       channel_name, platform,
		       is_starred, is_subscribed,
		       dearrow_title, dearrow_title_casual, dearrow_checked_at
		FROM ranked WHERE rn <= ?
		ORDER BY channel_id, published_at DESC
	`, videoFullyWatchedSQL("v"), readyVideoMediaExistsSQL("v"), channelFilter)
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
			&v.ID, &v.VideoID, &v.ChannelID, &v.OwnerKind, &v.Title, &v.Description,
			&v.Duration, &publishedAt, &downloadedAt,
			&v.Watched, &v.IsTemp, &v.IsPinned,
			&v.MetadataJSON,
			&v.MediaKind, &v.MediaSlideCount,
			&v.ChannelName, &v.Platform,
			&v.IsStarred, &v.IsSubscribed,
			&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowCheckedAtMs,
		)
		if err != nil {
			return nil, err
		}
		v.PublishedAt = millisToTimePtr(publishedAt)
		if t := millisToTimePtr(downloadedAt); t != nil {
			v.DownloadedAt = *t
		}
		hydrateVideoPlatform(&v)
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
		channelIDs := make([]string, 0, len(handles))
		for _, h := range handles {
			if channelID := model.TwitterChannelIDFromHandle(h); channelID != "" {
				channelIDs = append(channelIDs, channelID)
			}
		}
		if len(channelIDs) == 0 {
			return map[string][]model.FeedItem{}, nil
		}
		ph := strings.Repeat("?,", len(channelIDs))
		handleFilter = "AND channel_id IN (" + ph[:len(ph)-1] + ")"
		for _, channelID := range channelIDs {
			args = append(args, channelID)
		}
	}

	query := fmt.Sprintf(`
		WITH ranked AS (
			SELECT tweet_id,
			       ROW_NUMBER() OVER (PARTITION BY channel_id ORDER BY published_at DESC) AS rn
			FROM feed_items
			WHERE media_json IS NOT NULL AND media_json != '' AND media_json != '[]'
			  AND is_retweet = 0
			  %s
		)
		SELECT `+feedItemSelectSQL("f")+`
		FROM ranked r
		JOIN feed_items_resolved f ON f.tweet_id = r.tweet_id
		WHERE r.rn <= ?
		ORDER BY f.channel_id, f.published_at DESC
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
					INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
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
		where = append(where, readyVideoMediaExistsSQL("v"))
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
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
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
				LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
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
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
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
			       CASE WHEN mr.video_id IS NOT NULL
			            THEN COALESCE(NULLIF(mr.reposted_at_ms, 0), NULLIF(mr.first_seen_at_ms, 0), v.published_at, 0)
			            ELSE COALESCE(v.published_at, 0)
			        END AS effective_moment_at_ms
			FROM videos v
			LEFT JOIN channels c ON v.channel_id = c.channel_id
			LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
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
			LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
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
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
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
			       CASE WHEN mr.video_id IS NOT NULL
			            THEN COALESCE(NULLIF(mr.reposted_at_ms, 0), NULLIF(mr.first_seen_at_ms, 0), v.published_at, 0)
			            ELSE COALESCE(v.published_at, 0)
			        END AS effective_moment_at_ms
			FROM videos v
			LEFT JOIN channels c ON v.channel_id = c.channel_id
			LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
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
		SELECT video_id, comment_id, COALESCE(parent_id,''),
		       COALESCE(author_name,''), COALESCE(author_id,''),
		       COALESCE(text,''), COALESCE(like_count,0), published_at
		FROM video_comments
		WHERE video_id = ?
		ORDER BY like_count DESC, comment_id ASC
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
		var publishedAt sql.NullInt64
		err := rows.Scan(
			&c.VideoID, &c.CommentID, &c.ParentID,
			&c.AuthorName, &c.AuthorID,
			&c.Text, &c.LikeCount, &publishedAt,
		)
		if err != nil {
			return nil, err
		}
		c.PublishedAt = millisToTimePtr(publishedAt)
		c.Platform = "youtube"
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
		return nil
	})
	return deleted, err
}

// AddComments inserts comments for a video. Returns count inserted.
func (db *DB) AddComments(videoID string, comments []CommentInput) (int, error) {
	if len(comments) == 0 {
		return 0, nil
	}
	var inserted int
	err := db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		stmt, err := tx.Prepare(`
			INSERT INTO video_comments (
				video_id, comment_id, parent_id, author_name, author_id,
				text, like_count, published_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
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
				c.Author, c.AuthorID, c.Text, c.LikeCount, publishedAtMs,
			)
			if err != nil {
				continue // skip individual failures
			}
			n, _ := res.RowsAffected()
			inserted += int(n)
			if err := declareYouTubeCommentAvatarTx(tx, c, nowMs); err != nil {
				return err
			}
		}
		return nil
	})
	return inserted, err
}

func declareYouTubeCommentAvatarTx(tx *sql.Tx, comment CommentInput, nowMs int64) error {
	ownerID := model.YouTubeCommentAuthorChannelID(comment.AuthorID)
	sourceURL := strings.TrimSpace(comment.AuthorThumbnail)
	if ownerID == "" || !downloadableAssetSource(sourceURL) {
		return nil
	}
	return declareSourceAssetTx(tx, Asset{
		AssetID:        BuildAssetID("youtube", "comment_author", ownerID, "avatar", 0),
		AssetKind:      "avatar",
		OwnerKind:      "comment_author",
		OwnerID:        ownerID,
		SourceURL:      sourceURL,
		ContentType:    "image/jpeg",
		State:          AssetStateQueued,
		RequiredReason: "comment_avatar",
	}, nowMs)
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
func (db *DB) GetPlaybackPosition(videoID string) (float64, error) {
	var pos float64
	err := db.conn.QueryRow(
		"SELECT COALESCE(playback_position,0) FROM watch_history WHERE video_id = ?",
		videoID,
	).Scan(&pos)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return pos, err
}

// GetPlaybackPositions returns saved playback positions for a batch of video IDs.
func (db *DB) GetPlaybackPositions(videoIDs []string) (map[string]float64, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}
	ph := strings.Repeat("?,", len(videoIDs))
	ph = ph[:len(ph)-1]
	args := make([]any, 0, len(videoIDs))
	for _, id := range videoIDs {
		args = append(args, id)
	}
	rows, err := db.conn.Query(
		"SELECT video_id, COALESCE(playback_position,0) FROM watch_history WHERE video_id IN ("+ph+")",
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
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
		LEFT JOIN channel_stars   cs ON cs.channel_id = c.channel_id
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

// UpsertWatchHistoryFullyWatched marks a video as watched with a fully-played sentinel.
func (db *DB) UpsertWatchHistoryFullyWatched(videoID string, updatedAtMs int64) error {
	return db.writeWebProgress(videoID, 1, 1, "set", updatedAtMs)
}

// DeleteWatchHistory removes a watch_history row for a video (unwatched).
func (db *DB) DeleteWatchHistory(videoID string, updatedAtMs int64) error {
	return db.writeWebProgress(videoID, 0, 0, "clear", updatedAtMs)
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
}

// SaveProgress saves watch progress using latest_wins_v2 conflict resolution.
func (db *DB) SaveProgress(videoID string, position, duration float64, updatedAtMs int64) (ProgressResult, error) {
	return db.MutateProgress(videoID, position, duration, updatedAtMs)
}

// SponsorBlockSegment is a cached SponsorBlock segment for a video.
type SponsorBlockSegment struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Category string  `json:"category"`
}

// GetVideoStats returns unwatched count and canonical media size for videos
// with ready assets.
func (db *DB) GetVideoStats() (unwatched int, totalBytes int64, err error) {
	err = db.conn.QueryRow(`
		SELECT
			COUNT(DISTINCT CASE WHEN NOT `+videoFullyWatchedSQL("v")+` THEN v.video_id END),
			COALESCE(SUM(a.size_bytes),0)
		FROM videos v
		JOIN assets a
		  ON a.owner_kind = v.owner_kind
		 AND a.owner_id = v.video_id
		WHERE a.asset_kind IN ('video_stream', 'post_media', 'post_audio')
		  AND a.state = 'ready'
		  AND a.file_path != ''
	`).Scan(&unwatched, &totalBytes)
	return
}

// DeleteVideo deletes a video and only the files owned by its canonical asset rows.
func (db *DB) DeleteVideo(videoID string) error {
	return db.DeleteVideoWithFile(videoID)
}

// InsertVideo inserts or replaces a video record in the videos table.
// publishedAtMs is unix-millis; pass 0 when unknown.
func (db *DB) InsertVideo(videoID, channelID, ownerKind, title, description string,
	duration int, publishedAtMs int64, metadataJSON, mediaKind string, slideCount int, isTemp bool) error {
	return db.InsertVideoWithSourceKind(videoID, channelID, ownerKind, title, description, duration, publishedAtMs, metadataJSON, mediaKind, slideCount, isTemp, "")
}

func (db *DB) InsertVideoWithSourceKind(videoID, channelID, ownerKind, title, description string,
	duration int, publishedAtMs int64, metadataJSON, mediaKind string, slideCount int, isTemp bool, sourceKind string) error {
	if _, ok := videoPlatformForOwnerKind(ownerKind); !ok {
		return fmt.Errorf("video %s has invalid owner kind %q", videoID, ownerKind)
	}
	video := CompletedVideo{
		VideoID: videoID, ChannelID: channelID, OwnerKind: ownerKind, Title: title, Description: description,
		Duration: duration, PublishedAtMs: publishedAtMs, MetadataJSON: metadataJSON, MediaKind: mediaKind,
		SlideCount: slideCount, IsTemp: isTemp, SourceKind: sourceKind,
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		return upsertVideoMetadataTx(tx, video)
	})
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
		return nil
	})
}

// SaveSponsorBlockSegments replaces all segments for a video.
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
		return nil
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
