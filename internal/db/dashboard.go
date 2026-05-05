package db

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const dataDirUsageCacheTTL = 5 * time.Minute

var dataDirUsageCache struct {
	mu        sync.Mutex
	path      string
	bytes     int64
	updatedAt time.Time
}

// GetDashboardStats returns aggregate statistics for the server status dashboard.
func (db *DB) GetDashboardStats() (map[string]any, error) {
	stats := map[string]any{}

	// Each query is wrapped in a helper that returns 0 on error (table may not exist).
	queryInt := func(query string) int {
		var n int
		db.conn.QueryRow(query).Scan(&n)
		return n
	}
	queryInt64 := func(query string) int64 {
		var n int64
		db.conn.QueryRow(query).Scan(&n)
		return n
	}

	stats["channels_total"] = queryInt("SELECT COUNT(*) FROM channel_follows WHERE user_id = ''")
	stats["videos_total"] = queryInt("SELECT COUNT(*) FROM videos WHERE file_path IS NOT NULL AND file_path <> ''")
	stats["videos_watched"] = queryInt("SELECT COUNT(*) FROM videos WHERE watched=1 AND file_path IS NOT NULL AND file_path <> ''")
	stats["feed_items_count"] = queryInt("SELECT COUNT(*) FROM feed_items")
	stats["bookmarks_count"] = queryInt("SELECT COUNT(*) FROM bookmarks")
	stats["comments_count"] = queryInt("SELECT COUNT(*) FROM video_comments")

	// Media pipeline from feed_media_jobs
	stats["media_pipeline"] = map[string]int{
		"ready":  queryInt("SELECT COUNT(*) FROM feed_media_jobs WHERE status='completed'"),
		"queued": queryInt("SELECT COUNT(*) FROM feed_media_jobs WHERE status='queued'"),
		"failed": queryInt("SELECT COUNT(*) FROM feed_media_jobs WHERE status='failed'"),
	}

	// Download queue
	stats["download_queue"] = map[string]int{
		"pending":    queryInt("SELECT COUNT(*) FROM download_queue WHERE status='pending'"),
		"processing": queryInt("SELECT COUNT(*) FROM download_queue WHERE status='processing'"),
		"completed":  queryInt("SELECT COUNT(*) FROM download_queue WHERE status='completed'"),
		"failed":     queryInt("SELECT COUNT(*) FROM download_queue WHERE status='failed'"),
	}

	// SponsorBlock (tables may not exist if Python never created them)
	stats["sponsorblock"] = map[string]int{
		"checked":  queryInt("SELECT COUNT(*) FROM sponsorblock_checked"),
		"segments": queryInt("SELECT COUNT(*) FROM sponsorblock_segments"),
	}

	// Channels by platform
	channelsByPlatform := map[string]int{}
	platformRows, err := db.conn.Query(`
		SELECT c.platform, COUNT(*) FROM channels c
		INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		GROUP BY c.platform
	`)
	if err == nil {
		defer platformRows.Close()
		for platformRows.Next() {
			var platform string
			var count int
			if platformRows.Scan(&platform, &count) == nil {
				channelsByPlatform[platform] = count
			}
		}
	}
	stats["channels_by_platform"] = channelsByPlatform

	// Source health from ingest_state
	stats["source_health"] = map[string]any{
		"ok":      queryInt("SELECT COUNT(*) FROM ingest_state WHERE fail_count=0 AND last_success_at>0"),
		"cooling": queryInt("SELECT COUNT(*) FROM ingest_state WHERE fail_count BETWEEN 1 AND 3"),
		"failed":  queryInt("SELECT COUNT(*) FROM ingest_state WHERE fail_count>3"),
		"avg_latency_ms": func() int {
			var n int
			db.conn.QueryRow("SELECT COALESCE(CAST(AVG(avg_latency_ms) AS INTEGER),0) FROM ingest_state WHERE avg_latency_ms>0").Scan(&n)
			return n
		}(),
	}

	// Storage totals
	totalVideoBytes := queryInt64("SELECT COALESCE(SUM(file_size),0) FROM videos WHERE file_path IS NOT NULL AND file_path <> ''")
	videosTotal := queryInt("SELECT COUNT(*) FROM videos WHERE file_path IS NOT NULL AND file_path <> ''")
	totalStorageBytes, err := cachedDirUsageBytes(db.dataDir)
	if err != nil {
		slog.Warn("dashboard storage scan failed; falling back to video bytes", "data_dir", db.dataDir, "err", err)
		totalStorageBytes = totalVideoBytes
	}
	stats["storage_total_gb"] = fmt.Sprintf("%.2f", float64(totalStorageBytes)/1073741824)
	stats["video_storage_gb"] = fmt.Sprintf("%.2f", float64(totalVideoBytes)/1073741824)
	if videosTotal > 0 {
		stats["avg_mb_per_video"] = fmt.Sprintf("%.1f", float64(totalVideoBytes)/1048576/float64(videosTotal))
	} else {
		stats["avg_mb_per_video"] = "0"
	}

	// Local feed (completed media)
	stats["local_feed_count"] = queryInt("SELECT COUNT(*) FROM feed_items WHERE media_status='completed'")

	// Preview queue — count sprite.jpg files on disk (Go worker is in-memory, demand-based)
	stats["preview_queue"] = map[string]int{
		"ready":   countPreviewSprites(db.dataDir),
		"pending": 0,
	}

	// DB file sizes (pre-formatted for display)
	dbPath := filepath.Join(db.dataDir, "igloo.db")
	if fi, err := os.Stat(dbPath); err == nil {
		stats["db_size_mb"] = fmt.Sprintf("%.1f", float64(fi.Size())/1048576)
	}
	walPath := dbPath + "-wal"
	if fi, err := os.Stat(walPath); err == nil {
		stats["wal_size_mb"] = fmt.Sprintf("%.1f", float64(fi.Size())/1048576)
	}

	// Table count
	stats["table_count"] = queryInt("SELECT COUNT(*) FROM sqlite_master WHERE type='table'")

	// Video file size total
	stats["total_video_bytes"] = queryInt64("SELECT COALESCE(SUM(file_size),0) FROM videos WHERE file_path IS NOT NULL AND file_path <> ''")

	// Analytics summary for server dashboard
	stats["analytics_summary"] = map[string]int{
		"total":       queryInt("SELECT COUNT(*) FROM analytics_events"),
		"app_starts":  queryInt("SELECT COUNT(*) FROM analytics_events WHERE event_type='app_start'"),
		"video_opens": queryInt("SELECT COUNT(*) FROM analytics_events WHERE event_type='open_video'"),
		"syncs":       queryInt("SELECT COUNT(*) FROM analytics_events WHERE event_type='sync_success'"),
	}

	return stats, nil
}

// CountFeedItemsWithMedia returns count of feed items that have media.
func (db *DB) CountFeedItemsWithMedia() int {
	var n int
	db.conn.QueryRow("SELECT COUNT(*) FROM feed_items WHERE media_json IS NOT NULL AND media_json <> ''").Scan(&n)
	return n
}

// CountFeedItemsTextOnly returns count of feed items without media.
func (db *DB) CountFeedItemsTextOnly() int {
	var n int
	db.conn.QueryRow("SELECT COUNT(*) FROM feed_items WHERE media_json IS NULL OR media_json = ''").Scan(&n)
	return n
}

// CountSubscribedTwitterChannels returns the count of subscribed Twitter channels.
func (db *DB) CountSubscribedTwitterChannels() int {
	var n int
	db.conn.QueryRow(`
		SELECT COUNT(*) FROM channels c
		INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		WHERE c.platform = 'twitter'
	`).Scan(&n)
	return n
}

// countPreviewSprites counts video IDs that have a generated sprite.jpg on disk.
func countPreviewSprites(dataDir string) int {
	dir := filepath.Join(dataDir, "thumbnails", "previews")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "sprite.jpg")); err == nil {
				count++
			}
		}
	}
	return count
}

func cachedDirUsageBytes(root string) (int64, error) {
	dataDirUsageCache.mu.Lock()
	if dataDirUsageCache.path == root && time.Since(dataDirUsageCache.updatedAt) < dataDirUsageCacheTTL {
		bytes := dataDirUsageCache.bytes
		dataDirUsageCache.mu.Unlock()
		return bytes, nil
	}
	dataDirUsageCache.mu.Unlock()

	bytes, err := dirUsageBytes(root)
	if err != nil {
		return 0, err
	}

	dataDirUsageCache.mu.Lock()
	dataDirUsageCache.path = root
	dataDirUsageCache.bytes = bytes
	dataDirUsageCache.updatedAt = time.Now()
	dataDirUsageCache.mu.Unlock()
	return bytes, nil
}

func dirUsageBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			if os.IsNotExist(infoErr) {
				return nil
			}
			return infoErr
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// IngestCoverageCounts returns aggregate counts for the feed dashboard coverage panel.
// coolingSources = handles in backoff (fail_count 1-3).
func (db *DB) IngestCoverageCounts() (coolingSources int, err error) {
	db.conn.QueryRow("SELECT COUNT(*) FROM ingest_state WHERE fail_count BETWEEN 1 AND 3").Scan(&coolingSources)
	return
}
