package db

import "encoding/json"

type AndroidSyncStateKey struct {
	OwnerKind string `json:"owner_kind"`
	OwnerID   string `json:"owner_id"`
}

type AndroidSyncStateProjection struct {
	OwnerKind string
	OwnerID   string
	ChannelID string
	Payload   json.RawMessage
}

func (db *DB) ListAndroidSyncStateKeys() ([]AndroidSyncStateKey, error) {
	rows, err := db.reader().Query(`
		SELECT owner_kind, owner_id
		FROM (
			SELECT 'feed_like' AS owner_kind, tweet_id AS owner_id FROM feed_likes
			UNION ALL SELECT 'bookmark', video_id FROM bookmarks
			UNION ALL SELECT 'bookmark_category', CAST(id AS TEXT) FROM bookmark_categories
			UNION ALL SELECT 'feed_seen', tweet_id FROM feed_seen
			UNION ALL SELECT 'moment_view', video_id FROM moment_views
			UNION ALL SELECT 'watch_history', video_id FROM watch_history
			UNION ALL SELECT 'muted_channel', channel_id FROM muted_channels
			UNION ALL SELECT 'channel_follow', channel_id FROM channel_follows
			UNION ALL SELECT 'channel_star', channel_id FROM channel_stars
			UNION ALL
			SELECT 'channel_setting', channel_id
			FROM channel_settings
			WHERE media_only IS NOT NULL OR include_reposts IS NOT NULL
			   OR media_download_limit IS NOT NULL OR max_videos IS NOT NULL
			   OR download_subtitles IS NOT NULL
			UNION ALL SELECT 'moments_cursor', scope FROM moments_cursors
		)
		ORDER BY owner_kind, owner_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AndroidSyncStateKey
	for rows.Next() {
		var key AndroidSyncStateKey
		if err := rows.Scan(&key.OwnerKind, &key.OwnerID); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (db *DB) ListAndroidSyncStateProjections(keys []AndroidSyncStateKey) ([]AndroidSyncStateProjection, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	wantedJSON, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := db.reader().Query(`
		WITH wanted(owner_kind, owner_id) AS (
			SELECT json_extract(value, '$.owner_kind'), json_extract(value, '$.owner_id')
			FROM json_each(?)
		)
		SELECT owner_kind, owner_id, channel_id, payload
		FROM (
			SELECT 'feed_like' AS owner_kind, s.tweet_id AS owner_id, '' AS channel_id,
			       json_object('tweet_id', s.tweet_id, 'liked_at', s.liked_at) AS payload
			FROM feed_likes s
			JOIN wanted w ON w.owner_kind = 'feed_like' AND w.owner_id = s.tweet_id
			UNION ALL
			SELECT 'bookmark', s.video_id, '',
			       json_object('video_id', s.video_id, 'category_id', COALESCE(s.category_id, 0),
			         'custom_title', s.custom_title, 'account_handles', s.account_handles,
			         'media_indices', s.media_indices, 'bookmarked_at', s.bookmarked_at)
			FROM bookmarks s
			JOIN wanted w ON w.owner_kind = 'bookmark' AND w.owner_id = s.video_id
			UNION ALL
			SELECT 'bookmark_category', CAST(s.id AS TEXT), '',
			       json_object('category_id', s.id, 'name', s.name,
			         'archive_path', s.archive_path, 'created_at', s.created_at)
			FROM bookmark_categories s
			JOIN wanted w ON w.owner_kind = 'bookmark_category' AND w.owner_id = CAST(s.id AS TEXT)
			UNION ALL
			SELECT 'feed_seen', s.tweet_id, '',
			       json_object('tweet_id', s.tweet_id, 'seen_at', s.seen_at)
			FROM feed_seen s
			JOIN wanted w ON w.owner_kind = 'feed_seen' AND w.owner_id = s.tweet_id
			UNION ALL
			SELECT 'moment_view', s.video_id, '',
			       json_object('video_id', s.video_id, 'viewed_at', s.viewed_at)
			FROM moment_views s
			JOIN wanted w ON w.owner_kind = 'moment_view' AND w.owner_id = s.video_id
			UNION ALL
			SELECT 'watch_history', s.video_id, '',
			       json_object('video_id', s.video_id, 'playback_position', s.playback_position,
			         'duration', s.duration, 'updated_at_ms', s.updated_at_ms)
			FROM watch_history s
			JOIN wanted w ON w.owner_kind = 'watch_history' AND w.owner_id = s.video_id
			UNION ALL
			SELECT 'muted_channel', s.channel_id, s.channel_id,
			       json_object('channel_id', s.channel_id, 'muted_at', s.muted_at)
			FROM muted_channels s
			JOIN wanted w ON w.owner_kind = 'muted_channel' AND w.owner_id = s.channel_id
			UNION ALL
			SELECT 'channel_follow', s.channel_id, s.channel_id,
			       json_object('channel_id', s.channel_id, 'followed_at', s.followed_at)
			FROM channel_follows s
			JOIN wanted w ON w.owner_kind = 'channel_follow' AND w.owner_id = s.channel_id
			UNION ALL
			SELECT 'channel_star', s.channel_id, s.channel_id,
			       json_object('channel_id', s.channel_id, 'starred_at', s.starred_at)
			FROM channel_stars s
			JOIN wanted w ON w.owner_kind = 'channel_star' AND w.owner_id = s.channel_id
			UNION ALL
			SELECT 'channel_setting', s.channel_id, s.channel_id,
			       json_object('channel_id', s.channel_id, 'media_only', s.media_only,
			         'include_reposts', s.include_reposts, 'media_download_limit', s.media_download_limit,
			         'max_videos', s.max_videos, 'download_subtitles', s.download_subtitles,
			         'updated_at', s.updated_at)
			FROM channel_settings s
			JOIN wanted w ON w.owner_kind = 'channel_setting' AND w.owner_id = s.channel_id
			WHERE s.media_only IS NOT NULL OR s.include_reposts IS NOT NULL
			   OR s.media_download_limit IS NOT NULL OR s.max_videos IS NOT NULL
			   OR s.download_subtitles IS NOT NULL
			UNION ALL
			SELECT 'moments_cursor', s.scope, '',
			       json_object('scope', s.scope, 'video_id', s.video_id, 'position_ms', s.position_ms,
			         'sort_at_ms', s.sort_at_ms, 'updated_at_ms', s.updated_at_ms)
			FROM moments_cursors s
			JOIN wanted w ON w.owner_kind = 'moments_cursor' AND w.owner_id = s.scope
		)
		ORDER BY owner_kind, owner_id
	`, string(wantedJSON))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AndroidSyncStateProjection
	for rows.Next() {
		var row AndroidSyncStateProjection
		var payload string
		if err := rows.Scan(&row.OwnerKind, &row.OwnerID, &row.ChannelID, &payload); err != nil {
			return nil, err
		}
		row.Payload = json.RawMessage(payload)
		out = append(out, row)
	}
	return out, rows.Err()
}
