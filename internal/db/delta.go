package db

import (
	"database/sql"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

// #6 bundle-delta queries. One per content-stream table. Each returns
// (rows, maxSyncSeq, error). maxSyncSeq is the largest sync_seq in the
// returned batch — the caller serializes it as the opaque next_marker.

// ListFeedItemsForDelta reads Twitter feed_items rows with sync_seq > since.
// Ordered by sync_seq ASC; capped at `limit`.
//
// When cutoffMs > 0, the delta is restricted to Android's current retention-window
// working set: rows whose server-computed effective recency is inside the cutoff,
// plus rows protected by likes/bookmarks. This narrows the response surface without
// changing what the server stores.
//
// The Android rebuild stores the full retention-window working set locally and
// applies seen/muted filtering at read time. Delta therefore must not hide rows
// just because they are currently seen or muted on the server; it only joins
// that state onto the primary row as booleans for side-table ingest.
func (db *DB) ListFeedItemsForDelta(username string, since int64, limit int, cutoffMs int64) ([]model.FeedItem, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	baseSelect := `
		SELECT fi.tweet_id, COALESCE(fi.source_handle,''), fi.author_handle,
		       COALESCE(fi.author_display_name,''), COALESCE(fi.author_avatar_url,''),
		       COALESCE(fi.body_text,''), COALESCE(fi.lang,''),
		       COALESCE(fi.is_retweet,0), COALESCE(fi.retweeted_by_handle,''),
		       COALESCE(fi.retweeted_by_display_name,''),
		       COALESCE(fi.quote_tweet_id,''), COALESCE(fi.quote_author_handle,''),
		       COALESCE(fi.quote_author_display_name,''), COALESCE(fi.quote_author_avatar_url,''),
		       COALESCE(fi.quote_body_text,''), COALESCE(fi.quote_lang,''),
		       COALESCE(fi.quote_media_json,''), COALESCE(fi.media_json,''),
		       COALESCE(fi.canonical_url,''), COALESCE(fi.reply_to_handle,''),
		       COALESCE(fi.reply_to_status,''),
		       COALESCE(fi.is_reply,0), COALESCE(fi.is_ghost,0),
		       fi.quote_published_at,
		       COALESCE(fi.views,0), COALESCE(fi.likes,0), COALESCE(fi.retweets,0),
		       fi.published_at, fi.fetched_at,
		       COALESCE(fi.content_hash,''), COALESCE(fi.canonical_tweet_id,''),
		       COALESCE(fi.sync_seq,0)
	`
	args := make([]any, 0, 11)
	query := ""
	if cutoffMs > 0 {
		query = `
			WITH
			recent_hashes AS (
				SELECT DISTINCT content_hash
				FROM feed_items
				WHERE content_hash IS NOT NULL AND content_hash != ''
				  AND published_at >= ?

				UNION

				SELECT DISTINCT content_hash
				FROM retweet_sources
				WHERE content_hash IS NOT NULL AND content_hash != ''
				  AND published_at >= ?

				UNION

				SELECT DISTINCT q.content_hash
				FROM feed_items parent
				JOIN feed_items q ON q.tweet_id = parent.quote_tweet_id
				WHERE parent.published_at >= ?
				  AND q.content_hash IS NOT NULL
				  AND q.content_hash != ''
			),
			protected_hashes AS (
				SELECT DISTINCT fi.content_hash
				FROM feed_items fi
				JOIN feed_likes fl ON fl.tweet_id = fi.tweet_id
				WHERE fl.username = ?
				  AND fi.content_hash IS NOT NULL
				  AND fi.content_hash != ''

				UNION

				SELECT DISTINCT fi.content_hash
				FROM feed_items fi
				JOIN bookmarks b ON b.video_id = fi.tweet_id
				WHERE (b.user_id = '' OR b.user_id = ?)
				  AND fi.content_hash IS NOT NULL
				  AND fi.content_hash != ''
			),
			eligible_tweet_ids AS (
				SELECT tweet_id
				FROM feed_items
				WHERE published_at >= ?

				UNION

				SELECT fi.tweet_id
				FROM feed_items fi
				JOIN recent_hashes rh ON rh.content_hash = fi.content_hash

				UNION

				SELECT quote_tweet_id AS tweet_id
				FROM feed_items
				WHERE quote_tweet_id IS NOT NULL
				  AND quote_tweet_id != ''
				  AND published_at >= ?

				UNION

				SELECT fl.tweet_id
				FROM feed_likes fl
				WHERE fl.username = ?

				UNION

				SELECT b.video_id
				FROM bookmarks b
				WHERE (b.user_id = '' OR b.user_id = ?)

				UNION

				SELECT fi.tweet_id
				FROM feed_items fi
				JOIN protected_hashes ph ON ph.content_hash = fi.content_hash
			)
	` + baseSelect + `
			FROM eligible_tweet_ids e
			JOIN feed_items fi ON fi.tweet_id = e.tweet_id
			WHERE fi.sync_seq > ?
			  AND (
			      ` + retweetFilterClause("fi") + `
			      OR EXISTS (
			          SELECT 1
			            FROM protected_hashes ph_filter
			           WHERE ph_filter.content_hash = fi.content_hash
			      )
			  )
			ORDER BY fi.sync_seq ASC
			LIMIT ?
		`
		args = append(args,
			cutoffMs, cutoffMs, cutoffMs,
			username, username,
			cutoffMs, cutoffMs,
			username, username,
			since, limit,
		)
	} else {
		query = baseSelect + `
			FROM feed_items fi
			WHERE fi.sync_seq > ?
			  AND ` + retweetFilterClause("fi") + `
			ORDER BY fi.sync_seq ASC
			LIMIT ?
		`
		args = append(args, since, limit)
	}
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err := scanFeedItemsWithSeq(rows)
	if err != nil {
		return nil, 0, err
	}
	var maxSeq int64
	for _, it := range items {
		if it.SyncSeq > maxSeq {
			maxSeq = it.SyncSeq
		}
	}
	return items, maxSeq, nil
}

// ListVideosForDelta reads videos rows (filtered to the given
// platforms) with sync_seq > since, ordered by sync_seq ASC, capped at
// `limit`. No seen/muted exclusion for videos — those filters are
// Twitter-specific.
func (db *DB) ListVideosForDelta(platforms []string, since int64, limit int) ([]model.Video, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	if len(platforms) == 0 {
		return nil, 0, nil
	}
	placeholders := make([]string, len(platforms))
	args := make([]any, 0, len(platforms)+2)
	args = append(args, since)
	for i, p := range platforms {
		placeholders[i] = "?"
		args = append(args, p)
	}
	args = append(args, limit)

	query := `
		SELECT v.video_id, COALESCE(v.channel_id,''), COALESCE(v.title,''),
		       COALESCE(v.description,''), COALESCE(v.duration,0),
		       COALESCE(v.thumbnail_path,''), COALESCE(v.file_path,''),
		       COALESCE(v.file_size,0), COALESCE(v.published_at,0),
		       COALESCE(v.downloaded_at,0), COALESCE(v.watched,0),
		       COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       COALESCE(v.metadata_json,''), COALESCE(v.media_kind,''),
		       COALESCE(v.slide_count,0), COALESCE(v.source_kind,''), COALESCE(v.sync_seq,0),
		       COALESCE(c.name,''), COALESCE(c.platform,''),
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_followed,
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred,
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_thumb_path, v.dearrow_checked_at
		FROM videos v
		LEFT JOIN channels c ON c.channel_id = v.channel_id
		LEFT JOIN channel_follows cf
		  ON cf.channel_id = v.channel_id AND cf.user_id = ''
		LEFT JOIN channel_stars cs
		  ON cs.channel_id = v.channel_id AND cs.user_id = ''
		WHERE v.sync_seq > ?
		  AND COALESCE(c.platform,'') IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY v.sync_seq ASC
		LIMIT ?
	`
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var videos []model.Video
	var maxSeq int64
	for rows.Next() {
		var v model.Video
		var pubAt, dlAt sql.NullInt64
		var watched, isTemp, isPinned, isFollowed, isStarred int
		if err := rows.Scan(
			&v.VideoID, &v.ChannelID, &v.Title, &v.Description, &v.Duration,
			&v.ThumbnailPath, &v.FilePath, &v.FileSize, &pubAt, &dlAt,
			&watched, &isTemp, &isPinned, &v.MetadataJSON, &v.MediaKind,
			&v.MediaSlideCount, &v.SourceKind, &v.SyncSeq, &v.ChannelName, &v.Platform,
			&isFollowed, &isStarred,
			&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowThumbPath, &v.DearrowCheckedAtMs,
		); err != nil {
			return nil, 0, err
		}
		v.PublishedAt = millisToTimePtr(pubAt)
		if dlAt.Valid && dlAt.Int64 > 0 {
			if t := millisToTimePtr(dlAt); t != nil {
				v.DownloadedAt = *t
			}
		}
		v.Watched = watched != 0
		v.IsTemp = isTemp != 0
		v.IsPinned = isPinned != 0
		v.IsSubscribed = isFollowed != 0
		v.IsStarred = isStarred != 0
		v.EnrichForCard()
		videos = append(videos, v)
		if v.SyncSeq > maxSeq {
			maxSeq = v.SyncSeq
		}
	}
	return videos, maxSeq, rows.Err()
}

// ListChannelsForDelta reads channels rows with sync_seq > since,
// ordered by sync_seq ASC. is_subscribed + is_starred are joined at
// read time from the side tables (channel_follows, channel_stars).
func (db *DB) ListChannelsForDelta(since int64, limit int) ([]model.Channel, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	// Avatars are served via the /api/media/avatar proxy; Android resolves the
	// URL from channel_id locally and doesn't need one in the delta row.
	query := `
		SELECT c.channel_id, COALESCE(c.source_id,''), COALESCE(c.name,''), COALESCE(c.url,''),
		       COALESCE(c.platform,''), '' AS avatar_url, COALESCE(c.quality,''),
		       c.check_interval, c.last_checked, c.created_at, COALESCE(c.sync_seq,0),
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_followed,
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred
		FROM channels c
		LEFT JOIN channel_follows cf
		  ON cf.channel_id = c.channel_id AND cf.user_id = ''
		LEFT JOIN channel_stars cs
		  ON cs.channel_id = c.channel_id AND cs.user_id = ''
		WHERE COALESCE(c.sync_seq, 0) > ?
		ORDER BY c.sync_seq ASC
		LIMIT ?
	`
	rows, err := db.conn.Query(query, since, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []model.Channel
	var maxSeq int64
	for rows.Next() {
		var c model.Channel
		var checkInterval, lastChecked, createdAt sql.NullInt64
		var isFollowed, isStarred int
		if err := rows.Scan(
			&c.ChannelID, &c.SourceID, &c.Name, &c.URL, &c.Platform, &c.AvatarURL,
			&c.Quality, &checkInterval, &lastChecked, &createdAt, &c.SyncSeq, &isFollowed, &isStarred,
		); err != nil {
			return nil, 0, err
		}
		if checkInterval.Valid {
			value := int(checkInterval.Int64)
			c.CheckInterval = &value
		}
		c.LastChecked = millisToTimePtr(lastChecked)
		if createdAt.Valid && createdAt.Int64 > 0 {
			if t := millisToTimePtr(createdAt); t != nil {
				c.CreatedAt = *t
			}
		}
		c.IsSubscribed = isFollowed != 0
		c.IsStarred = isStarred != 0
		out = append(out, c)
		if c.SyncSeq > maxSeq {
			maxSeq = c.SyncSeq
		}
	}
	return out, maxSeq, rows.Err()
}
