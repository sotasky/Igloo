package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// AndroidRetentionSettings mirrors the Android cache-retention preferences that
// define the local content boundary. A non-positive value means "do not narrow
// this bucket by age", matching the Android prune semantics.
type AndroidRetentionSettings struct {
	FeedDays    int
	YoutubeDays int
	MomentsDays int
	StoryHours  int
}

// AndroidDashboardExpectations are server-owned denominators for the Android
// logs modal. Android reports actual local presence; the server computes what
// should exist for the active retention window.
type AndroidDashboardExpectations struct {
	FeedItems int
	FeedMedia int
	Videos    int
	Moments   int
	Avatars   int
}

// AndroidSyncHealthReport is the latest Android-reported local asset state for
// a server-owned sync generation. Unlike the older logs/android/cache_health.json
// payload, this comes from /api/android/sync/health and survives server restarts.
type AndroidSyncHealthReport struct {
	ID             int64
	GenerationID   string
	ReportedAtMs   int64
	PayloadJSON    string
	VerifiedAssets int
	PendingAssets  int
	FailedAssets   int
	MissingAssets  int
	TotalAssets    int
	VerifiedBytes  int64
	Retention      AndroidRetentionSettings
	HasRetention   bool
}

// GetLatestAndroidSyncHealthReport returns the newest persisted Android sync
// health report across generations. The report timestamp is Android's emitted
// "sync state observed" timestamp, with server receive time only used by the log
// sink; this table is the durable source for the logs modal's Last Sync card.
func (db *DB) GetLatestAndroidSyncHealthReport() (*AndroidSyncHealthReport, error) {
	var row AndroidSyncHealthReport
	err := db.conn.QueryRow(`
		SELECT id, generation_id, reported_at_ms, payload_json,
		       verified_assets, pending_assets, failed_assets, missing_assets,
		       total_assets, verified_bytes
		FROM android_sync_health_reports
		ORDER BY reported_at_ms DESC, id DESC
		LIMIT 1
	`).Scan(
		&row.ID,
		&row.GenerationID,
		&row.ReportedAtMs,
		&row.PayloadJSON,
		&row.VerifiedAssets,
		&row.PendingAssets,
		&row.FailedAssets,
		&row.MissingAssets,
		&row.TotalAssets,
		&row.VerifiedBytes,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	row.Retention, row.HasRetention = androidSyncHealthRetention(row.PayloadJSON)
	return &row, nil
}

func androidSyncHealthRetention(payload string) (AndroidRetentionSettings, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &raw) != nil {
		return AndroidRetentionSettings{}, false
	}
	retentionRaw, ok := raw["retention"]
	if !ok {
		return AndroidRetentionSettings{}, false
	}
	var ret map[string]int
	if json.Unmarshal(retentionRaw, &ret) != nil {
		return AndroidRetentionSettings{}, false
	}
	storyHours := 48
	if v, ok := ret["story_hours"]; ok {
		storyHours = NormalizeStoriesWindowHours(v)
	}
	return AndroidRetentionSettings{
		FeedDays:    max(0, ret["feed_days"]),
		YoutubeDays: max(0, ret["youtube_days"]),
		MomentsDays: max(0, ret["moments_days"]),
		StoryHours:  storyHours,
	}, true
}

// GetAndroidDashboardExpectations calculates the current server-side expectation
// for Android's retained local content. It intentionally avoids the Android Room
// cache and old client-side totals.
func (db *DB) GetAndroidDashboardExpectations(username string, settings AndroidRetentionSettings, nowMs int64) (AndroidDashboardExpectations, error) {
	var out AndroidDashboardExpectations

	feedCutoff := retentionCutoffMs(nowMs, settings.FeedDays)
	youtubeCutoff := retentionCutoffMs(nowMs, settings.YoutubeDays)
	momentsCutoff := retentionCutoffMs(nowMs, settings.MomentsDays)

	feedItems, err := db.countAndroidFeedItems(username, feedCutoff)
	if err != nil {
		return out, err
	}
	out.FeedItems = feedItems

	feedMedia, err := db.countAndroidFeedMedia(username, feedCutoff)
	if err != nil {
		return out, err
	}
	out.FeedMedia = feedMedia

	videos, err := db.countAndroidVideos(username, youtubeCutoff, "youtube")
	if err != nil {
		return out, err
	}
	out.Videos = videos

	moments, err := db.countAndroidVideos(username, momentsCutoff, "moments")
	if err != nil {
		return out, err
	}
	out.Moments = moments

	avatars, err := db.countAndroidAvatars(username, feedCutoff, youtubeCutoff, momentsCutoff)
	if err != nil {
		return out, err
	}
	out.Avatars = avatars

	return out, nil
}

func retentionCutoffMs(nowMs int64, days int) int64 {
	if days <= 0 {
		return 0
	}
	return nowMs - int64(days)*86_400_000
}

func (db *DB) countAndroidFeedItems(username string, cutoffMs int64) (int, error) {
	query, args := androidEligibleFeedCountQuery(username, cutoffMs)
	var n int
	if err := db.conn.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count android feed items: %w", err)
	}
	return n, nil
}

func (db *DB) countAndroidFeedMedia(username string, cutoffMs int64) (int, error) {
	cte, args := androidEligibleFeedCTE(username, cutoffMs)
	query := cte + `
		, scope_quote_media(quote_tweet_id) AS (
			SELECT DISTINCT fi.quote_tweet_id
			FROM feed_items fi
			JOIN eligible_tweet_ids e ON e.tweet_id = fi.tweet_id
			JOIN feed_media_jobs fmj ON fmj.tweet_id = fi.tweet_id
			WHERE fmj.status = 'completed'
			  AND fi.quote_tweet_id IS NOT NULL
			  AND fi.quote_tweet_id != ''
		),
		expected(asset_id) AS (
			SELECT 'feed:' || mf.id
			FROM media_files mf
			WHERE mf.owner_type = 'feed_media'
			  AND mf.owner_id IN (SELECT tweet_id FROM eligible_tweet_ids)
			  AND EXISTS (SELECT 1 FROM feed_media_jobs fmj WHERE fmj.tweet_id = mf.owner_id AND fmj.status = 'completed')
			  AND NOT ` + manifestSkipSQL("mf.file_path") + `

			UNION

			SELECT 'quote:' || mf.id
			FROM media_files mf
			WHERE mf.owner_type = 'quote_media'
			  AND mf.owner_id IN (SELECT quote_tweet_id FROM scope_quote_media)
			  AND NOT ` + manifestSkipSQL("mf.file_path") + `

			UNION

			SELECT 'feed_thumb:' || mf.owner_id
			FROM media_files mf
			WHERE mf.owner_type = 'feed_media'
			  AND (mf.media_type = 'video' OR mf.media_type = 'gif')
			  AND mf.media_index = 0
			  AND mf.owner_id IN (SELECT tweet_id FROM eligible_tweet_ids)
			  AND EXISTS (SELECT 1 FROM feed_media_jobs fmj WHERE fmj.tweet_id = mf.owner_id AND fmj.status = 'completed')

			UNION

			SELECT 'quote_thumb:' || mf.owner_id
			FROM media_files mf
			WHERE mf.owner_type = 'quote_media'
			  AND (mf.media_type = 'video' OR mf.media_type = 'gif')
			  AND mf.media_index = 0
			  AND mf.owner_id IN (SELECT quote_tweet_id FROM scope_quote_media)
		)
		SELECT COUNT(*) FROM expected
	`
	var n int
	if err := db.conn.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count android feed media: %w", err)
	}
	return n, nil
}

func (db *DB) countAndroidVideos(username string, cutoffMs int64, kind string) (int, error) {
	filter := "v.channel_id LIKE 'youtube_%'"
	if kind == "moments" {
		filter = "(v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')"
	}
	query := `
		SELECT COUNT(DISTINCT v.video_id)
		FROM videos v
		WHERE ` + filter + `
		  AND COALESCE(v.file_path, '') != ''
		  AND (
		    ? = 0
		    OR COALESCE(v.published_at, 0) >= ?
		    OR EXISTS (SELECT 1 FROM bookmarks b WHERE (b.user_id = '' OR b.user_id = ?) AND b.video_id = v.video_id)
		    OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.username = ? AND fl.tweet_id = v.video_id)
		    OR EXISTS (SELECT 1 FROM moment_views mv WHERE mv.username = ? AND mv.video_id = v.video_id)
		  )
	`
	var n int
	if err := db.conn.QueryRow(query, cutoffMs, cutoffMs, username, username, username).Scan(&n); err != nil {
		return 0, fmt.Errorf("count android %s videos: %w", kind, err)
	}
	return n, nil
}

func (db *DB) countAndroidAvatars(username string, feedCutoffMs, youtubeCutoffMs, momentsCutoffMs int64) (int, error) {
	cte, args := androidEligibleFeedCTE(username, feedCutoffMs)
	query := cte + `
		, retained_videos(channel_id) AS (
			SELECT DISTINCT v.channel_id
			FROM videos v
			WHERE COALESCE(v.channel_id, '') != ''
			  AND (
			    (v.channel_id LIKE 'youtube_%' AND (? = 0 OR COALESCE(v.published_at, 0) >= ?))
			    OR ((v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%') AND (? = 0 OR COALESCE(v.published_at, 0) >= ?))
			    OR EXISTS (SELECT 1 FROM bookmarks b WHERE (b.user_id = '' OR b.user_id = ?) AND b.video_id = v.video_id)
			    OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.username = ? AND fl.tweet_id = v.video_id)
			    OR EXISTS (SELECT 1 FROM moment_views mv WHERE mv.username = ? AND mv.video_id = v.video_id)
			  )
		),
		avatar_channels(channel_id) AS (
			SELECT cf.channel_id
			FROM channel_follows cf
			WHERE cf.user_id = ''

			UNION

			SELECT DISTINCT 'twitter_' || LOWER(fi.author_handle)
			FROM feed_items fi
			JOIN eligible_tweet_ids e ON e.tweet_id = fi.tweet_id
			WHERE fi.author_handle != ''

			UNION

			SELECT DISTINCT 'twitter_' || LOWER(fi.quote_author_handle)
			FROM feed_items fi
			JOIN eligible_tweet_ids e ON e.tweet_id = fi.tweet_id
			WHERE fi.quote_author_handle != ''

			UNION

			SELECT DISTINCT 'twitter_' || LOWER(rs.retweeter_handle)
			FROM retweet_sources rs
			JOIN feed_items fi ON fi.content_hash = rs.content_hash
			JOIN eligible_tweet_ids e ON e.tweet_id = fi.tweet_id
			WHERE rs.retweeter_handle != ''

			UNION

			SELECT channel_id FROM retained_videos
		)
		SELECT DISTINCT cp.channel_id
		FROM channel_profiles cp
		JOIN avatar_channels ac ON ac.channel_id = cp.channel_id
		WHERE cp.tombstone = 0
	`
	args = append(args,
		youtubeCutoffMs, youtubeCutoffMs,
		momentsCutoffMs, momentsCutoffMs,
		username, username, username,
	)
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return 0, fmt.Errorf("count android avatars: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	n := 0
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return 0, err
		}
		if db.findAvatarRelativePath(channelID) != "" {
			n++
		}
	}
	return n, rows.Err()
}

func androidEligibleFeedCountQuery(username string, cutoffMs int64) (string, []any) {
	cte, args := androidEligibleFeedCTE(username, cutoffMs)
	return cte + `
		SELECT COUNT(DISTINCT fi.tweet_id)
		FROM eligible_tweet_ids e
		JOIN feed_items fi ON fi.tweet_id = e.tweet_id
		WHERE ` + retweetFilterClause("fi"), args
}

func androidEligibleFeedCTE(username string, cutoffMs int64) (string, []any) {
	if cutoffMs <= 0 {
		return `
			WITH RECURSIVE eligible_tweet_ids(tweet_id) AS (
				SELECT fi.tweet_id FROM feed_items fi
			)
		`, nil
	}
	return `
		WITH RECURSIVE
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
	`, []any{
			cutoffMs, cutoffMs, cutoffMs,
			username, username,
			cutoffMs, cutoffMs,
			username, username,
		}
}

func manifestSkipSQL(column string) string {
	c := strings.TrimSpace(column)
	if c == "" {
		c = "file_path"
	}
	return `(LOWER(` + c + `) LIKE '%.mp3'
		OR LOWER(` + c + `) LIKE '%.m4a'
		OR LOWER(` + c + `) LIKE '%.aac'
		OR LOWER(` + c + `) LIKE '%.ogg')`
}
