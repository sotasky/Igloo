package db

import (
	"fmt"
	"os"
	"path/filepath"
)

// GetDashboardStats returns aggregate statistics for the server status dashboard.
func (db *DB) GetDashboardStats() (map[string]any, error) {
	stats := map[string]any{}
	var queryErr error

	// Keep the existing map shape for callers that choose to display a partial
	// dashboard, but report the first failed query so derived caches never
	// publish a zeroed inventory as a complete snapshot.
	queryInt := func(query string) int {
		if queryErr != nil {
			return 0
		}
		var n int
		if err := db.conn.QueryRow(query).Scan(&n); err != nil {
			queryErr = err
		}
		return n
	}
	queryInt64 := func(query string) int64 {
		if queryErr != nil {
			return 0
		}
		var n int64
		if err := db.conn.QueryRow(query).Scan(&n); err != nil {
			queryErr = err
		}
		return n
	}

	stats["channels_total"] = queryInt("SELECT COUNT(*) FROM channel_follows")
	videosTotal := queryInt(`
		SELECT COUNT(*) FROM videos v
		WHERE ` + readyVideoMediaExistsSQL("v") + `
	`)
	stats["videos_total"] = videosTotal
	stats["videos_watched"] = queryInt(`
		SELECT COUNT(*) FROM videos v
		WHERE ` + videoFullyWatchedSQL("v") + `
		  AND ` + readyVideoMediaExistsSQL("v") + `
	`)
	stats["feed_items_count"] = queryInt("SELECT COUNT(*) FROM feed_items")
	stats["bookmarks_count"] = queryInt("SELECT COUNT(*) FROM bookmarks")
	stats["comments_count"] = queryInt("SELECT COUNT(*) FROM video_comments")

	// Canonical asset state is both pipeline state and presentation readiness.
	stats["media_pipeline"] = map[string]int{
		"ready":  queryInt("SELECT COUNT(*) FROM media_objects WHERE published_revision > 0 AND file_path != ''"),
		"queued": queryInt("SELECT COUNT(*) FROM media_objects WHERE job_state IN ('queued','downloading','stale')"),
		"failed": queryInt("SELECT COUNT(*) FROM media_objects WHERE published_revision = 0 AND job_state IN ('failed','server_missing','permanent_missing')"),
	}

	// Download queue
	stats["download_queue"] = map[string]int{
		"pending":    queryInt("SELECT COUNT(*) FROM download_queue WHERE status='pending'"),
		"processing": queryInt("SELECT COUNT(*) FROM download_queue WHERE status='processing'"),
		"completed":  0,
		"failed":     queryInt("SELECT COUNT(*) FROM download_queue WHERE status='blocked'"),
	}

	// SponsorBlock (tables may not exist if Python never created them)
	stats["sponsorblock"] = map[string]int{
		"checked":  queryInt("SELECT COUNT(*) FROM sponsorblock_checked"),
		"segments": queryInt("SELECT COUNT(*) FROM sponsorblock_segments"),
	}

	// Channels by platform
	channelsByPlatform := map[string]int{}
	if queryErr == nil {
		platformRows, err := db.conn.Query(`
			SELECT c.platform, COUNT(*) FROM channels c
			INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id
			GROUP BY c.platform
		`)
		if err != nil {
			queryErr = err
		} else {
			for platformRows.Next() {
				var platform string
				var count int
				if err := platformRows.Scan(&platform, &count); err != nil {
					queryErr = err
					break
				}
				channelsByPlatform[platform] = count
			}
			if err := platformRows.Err(); err != nil && queryErr == nil {
				queryErr = err
			}
			_ = platformRows.Close()
		}
	}
	stats["channels_by_platform"] = channelsByPlatform

	// Source health from ingest_state
	stats["source_health"] = map[string]any{
		"ok":             queryInt("SELECT COUNT(*) FROM ingest_state WHERE fail_count=0 AND last_success_at>0"),
		"cooling":        queryInt("SELECT COUNT(*) FROM ingest_state WHERE fail_count BETWEEN 1 AND 3"),
		"failed":         queryInt("SELECT COUNT(*) FROM ingest_state WHERE fail_count>3"),
		"avg_latency_ms": queryInt("SELECT COALESCE(CAST(AVG(avg_latency_ms) AS INTEGER),0) FROM ingest_state WHERE avg_latency_ms>0"),
	}

	// Storage totals
	totalVideoBytes := queryInt64(`
		WITH video_rows AS (
			SELECT mo.file_path, mo.size_bytes
			FROM assets a
			JOIN media_objects mo ON mo.object_id = a.object_id
			WHERE a.asset_kind = 'video_stream'
			  AND mo.published_revision > 0 AND mo.file_path != ''

			UNION ALL

			SELECT mo.file_path, mo.size_bytes
			FROM media_objects mo
			WHERE mo.published_revision > 0 AND mo.file_path != ''
			  AND mo.content_type LIKE 'video/%'
			  AND EXISTS (SELECT 1 FROM assets a WHERE a.object_id = mo.object_id)
		)
		SELECT COALESCE(SUM(size_bytes), 0)
		FROM (
			SELECT file_path, MAX(size_bytes) AS size_bytes
			FROM video_rows
			GROUP BY file_path
		)
	`)
	totalStorageBytes := queryInt64(`
		SELECT COALESCE(SUM(size_bytes), 0)
		FROM (
			SELECT file_path, MAX(size_bytes) AS size_bytes
			FROM media_objects
			WHERE published_revision > 0 AND file_path != ''
			GROUP BY file_path
		)
	`)
	dbPath := filepath.Join(db.storage.StateRoot(), "igloo.db")
	if fi, statErr := os.Stat(dbPath); statErr == nil {
		totalStorageBytes += fi.Size()
		stats["db_size_mb"] = fmt.Sprintf("%.1f", float64(fi.Size())/1048576)
	}
	if fi, statErr := os.Stat(dbPath + "-wal"); statErr == nil {
		totalStorageBytes += fi.Size()
		stats["wal_size_mb"] = fmt.Sprintf("%.1f", float64(fi.Size())/1048576)
	}
	stats["storage_total_gb"] = fmt.Sprintf("%.2f", float64(totalStorageBytes)/1073741824)
	stats["video_storage_gb"] = fmt.Sprintf("%.2f", float64(totalVideoBytes)/1073741824)
	if videosTotal > 0 {
		stats["avg_mb_per_video"] = fmt.Sprintf("%.1f", float64(totalVideoBytes)/1048576/float64(videosTotal))
	} else {
		stats["avg_mb_per_video"] = "0"
	}

	// Local feed items with canonical ready media, including quoted media.
	stats["local_feed_count"] = queryInt(`
		SELECT COUNT(*)
		FROM feed_items fi
		WHERE EXISTS (
			SELECT 1 FROM assets a JOIN media_objects mo ON mo.object_id = a.object_id
			WHERE a.owner_kind = 'tweet'
			  AND a.owner_id IN (fi.tweet_id, COALESCE(NULLIF(fi.quote_tweet_id, ''), fi.tweet_id))
			  AND a.asset_kind IN ('post_media', 'post_audio')
			  AND mo.published_revision > 0 AND mo.file_path != ''
		)
	`)

	// Preview queue is represented by canonical ready sprite assets.
	stats["preview_queue"] = map[string]int{
		"ready":   queryInt("SELECT COUNT(*) FROM assets a JOIN media_objects mo ON mo.object_id=a.object_id WHERE a.asset_kind='preview_sprite' AND mo.published_revision>0 AND mo.file_path!=''"),
		"pending": queryInt("SELECT COUNT(*) " + pendingVideoPreviewFromSQL),
	}

	// Table count
	stats["table_count"] = queryInt("SELECT COUNT(*) FROM sqlite_master WHERE type='table'")

	// Video file size total
	stats["total_video_bytes"] = totalVideoBytes

	// Analytics summary for server dashboard
	stats["analytics_summary"] = map[string]int{
		"total":       queryInt("SELECT COUNT(*) FROM analytics_events"),
		"app_starts":  queryInt("SELECT COUNT(*) FROM analytics_events WHERE event_type='app_start'"),
		"video_opens": queryInt("SELECT COUNT(*) FROM analytics_events WHERE event_type='open_video'"),
		"syncs":       queryInt("SELECT COUNT(*) FROM analytics_events WHERE event_type='sync_success'"),
	}

	return stats, queryErr
}

// CountFeedItemsWithMedia returns count of feed items that have media.
func (db *DB) CountFeedItemsWithMedia() int {
	var n int
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM feed_items WHERE media_json IS NOT NULL AND media_json <> ''").Scan(&n)
	return n
}

// CountFeedItemsTextOnly returns count of feed items without media.
func (db *DB) CountFeedItemsTextOnly() int {
	var n int
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM feed_items WHERE media_json IS NULL OR media_json = ''").Scan(&n)
	return n
}

// CountSubscribedTwitterChannels returns the count of subscribed Twitter channels.
func (db *DB) CountSubscribedTwitterChannels() int {
	var n int
	_ = db.conn.QueryRow(`
		SELECT COUNT(*) FROM channels c
		INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id
		WHERE c.platform = 'twitter'
	`).Scan(&n)
	return n
}

// IngestCoverageCounts returns aggregate counts for the feed dashboard coverage panel.
// coolingSources = handles in backoff (fail_count 1-3).
func (db *DB) IngestCoverageCounts() (coolingSources int, err error) {
	_ = db.conn.QueryRow("SELECT COUNT(*) FROM ingest_state WHERE fail_count BETWEEN 1 AND 3").Scan(&coolingSources)
	return
}
