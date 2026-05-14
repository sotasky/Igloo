package db

import (
	"database/sql"
	"log"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

func runFeedMediaLegacyFixes(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "python_feed_media_legacy_fixes", func(tx *sql.Tx) error {
		pythonMigrations := []string{
			// Fix 1-based media_index → 0-based for Python-era feed media.
			// Python stored indices as 1,2,3; Go uses 0,1,2. Only touch entries whose
			// parent job is still 'ready' (Python era) and whose minimum index > 0.
			`UPDATE media_files SET media_index = media_index - 1
		 WHERE owner_type = 'feed_media'
		 AND owner_id IN (
			SELECT tweet_id FROM feed_media_jobs WHERE status = 'ready'
		 )
		 AND owner_id NOT IN (
			SELECT owner_id FROM media_files WHERE owner_type = 'feed_media' AND media_index = 0
		 )`,

			// Incomplete slideshows: fewer media_files than expected slides → re-queue
			`UPDATE feed_media_jobs SET status='queued', updated_at=CAST(strftime('%s','now') AS INTEGER) * 1000
		 WHERE status='ready' AND slide_count > 0
		 AND (SELECT COUNT(*) FROM media_files WHERE owner_type='feed_media' AND owner_id=feed_media_jobs.tweet_id) < slide_count`,
		}
		for _, m := range pythonMigrations {
			if _, err := tx.Exec(m); err != nil {
				return err
			}
		}

		// Re-queue 'ready' video jobs without a downloaded file.
		// Tolerant of missing 'videos' table (only exists in production DBs from Python era).
		if _, err := tx.Exec(`UPDATE feed_media_jobs SET status='queued', updated_at=CAST(strftime('%s','now') AS INTEGER) * 1000
			WHERE status='ready' AND media_kind='video' AND slide_count = 0
			AND NOT EXISTS (SELECT 1 FROM videos WHERE video_id=feed_media_jobs.tweet_id AND file_path != '')`); err != nil {
			return err
		}

		// Finalize: remaining 'ready' (complete) → 'completed', stuck 'downloading' → re-queue
		finalMigrations := []string{
			`UPDATE feed_media_jobs SET status='completed' WHERE status='ready'`,
			`UPDATE feed_media_jobs SET status='queued', updated_at=CAST(strftime('%s','now') AS INTEGER) * 1000 WHERE status='downloading'`,
		}
		for _, m := range finalMigrations {
			if _, err := tx.Exec(m); err != nil {
				return err
			}
		}

		// Create jobs for tweets with quote media but no parent media (and no existing job).
		// These were missed because classifyMediaKind only checked parent MediaJSON.
		if _, err := tx.Exec(`INSERT OR IGNORE INTO feed_media_jobs (tweet_id, tweet_url, source_handle, status, media_kind, slide_count)
			SELECT fi.tweet_id, fi.canonical_url, fi.source_handle, 'queued',
				CASE WHEN fi.quote_media_json LIKE '%"video"%' OR fi.quote_media_json LIKE '%"gif"%' THEN 'video' ELSE 'image' END,
				0
			FROM feed_items fi
			WHERE (fi.media_json IS NULL OR fi.media_json = '' OR fi.media_json = '[]')
			AND fi.quote_media_json IS NOT NULL AND fi.quote_media_json != '' AND fi.quote_media_json != '[]'
			AND NOT EXISTS (SELECT 1 FROM feed_media_jobs fmj WHERE fmj.tweet_id = fi.tweet_id)`); err != nil {
			return err
		}

		// Fix slide_count=0 for completed jobs that actually have multiple media_files.
		// This was caused by older ingest code not calling ParseMedia() before using len(item.Media).
		_, err := tx.Exec(`UPDATE feed_media_jobs
			SET slide_count = (
				SELECT COUNT(*) FROM media_files
				WHERE owner_type='feed_media' AND owner_id=feed_media_jobs.tweet_id
			),
			media_kind = CASE
				WHEN (SELECT COUNT(*) FROM media_files WHERE owner_type='feed_media' AND owner_id=feed_media_jobs.tweet_id) > 1
				THEN 'slideshow'
				ELSE media_kind
			END
			WHERE status='completed' AND slide_count=0
			AND (SELECT COUNT(*) FROM media_files WHERE owner_type='feed_media' AND owner_id=feed_media_jobs.tweet_id) > 0`)
		return err
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfRows(conn, "python_feed_media_legacy_fixes", `
			SELECT COUNT(*)
			FROM feed_media_jobs
			WHERE status IN ('ready', 'downloading')
		`)
	}

	return nil
}

func cleanupLegacyAndroidSyncGenerations(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "legacy_android_v3_generation_cleanup", func(tx *sql.Tx) error {
		for _, stmt := range []string{
			"DELETE FROM android_sync_health_reports WHERE generation_id LIKE 'android-v3-%'",
			"DELETE FROM android_sync_assets WHERE generation_id LIKE 'android-v3-%'",
			"DELETE FROM android_sync_items WHERE generation_id LIKE 'android-v3-%'",
			"DELETE FROM android_sync_generations WHERE generation_id LIKE 'android-v3-%'",
		} {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfRows(conn, "legacy_android_v3_generation_cleanup", `
			SELECT COUNT(*)
			FROM android_sync_generations
			WHERE generation_id LIKE 'android-v3-%'
		`)
	}
	return nil
}

func cleanupYouTubeCommentAuthorProfilesOnce(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "remove_youtube_comment_author_profiles", func(tx *sql.Tx) error {
		// These rows were previously derived from video_comments. yt-dlp already
		// stores commenter thumbnails on the comment rows, and commenters are not
		// clickable Igloo profiles, so profile/avatar workers should ignore them.
		_, err := tx.Exec(`
			DELETE FROM channel_profiles
			WHERE platform = 'youtube'
			  AND NOT EXISTS (SELECT 1 FROM channels c WHERE c.channel_id = channel_profiles.channel_id)
			  AND NOT EXISTS (SELECT 1 FROM videos v WHERE v.channel_id = channel_profiles.channel_id)
			  AND NOT EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id = channel_profiles.channel_id)
			  AND NOT EXISTS (SELECT 1 FROM channel_stars cs WHERE cs.channel_id = channel_profiles.channel_id)
		`)
		return err
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfRows(conn, "remove_youtube_comment_author_profiles", `
			SELECT COUNT(*)
			FROM channel_profiles
			WHERE platform = 'youtube'
			  AND NOT EXISTS (SELECT 1 FROM channels c WHERE c.channel_id = channel_profiles.channel_id)
			  AND NOT EXISTS (SELECT 1 FROM videos v WHERE v.channel_id = channel_profiles.channel_id)
			  AND NOT EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id = channel_profiles.channel_id)
			  AND NOT EXISTS (SELECT 1 FROM channel_stars cs WHERE cs.channel_id = channel_profiles.channel_id)
		`)
	}
	return nil
}

func (db *DB) repairTwitterPlaceholderAuthorsOnce() error {
	ran, err := runSchemaMigrationOnce(db.conn, "twitter_placeholder_author_repair", func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT tweet_id, COALESCE(source_handle, ''), COALESCE(canonical_tweet_id, ''),
			       COALESCE(canonical_url, '')
			FROM feed_items
			WHERE COALESCE(is_retweet, 0) = 0
			  AND LOWER(COALESCE(author_handle, '')) IN ('unknown', 'undefined')
			  AND LOWER(COALESCE(source_handle, '')) NOT IN ('', 'unknown', 'undefined')
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = rows.Close()
		}()

		type repairRow struct {
			tweetID          string
			sourceHandle     string
			canonicalTweetID string
			canonicalURL     string
		}
		var repairs []repairRow
		for rows.Next() {
			var row repairRow
			if err := rows.Scan(&row.tweetID, &row.sourceHandle, &row.canonicalTweetID, &row.canonicalURL); err != nil {
				return err
			}
			repairs = append(repairs, row)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, row := range repairs {
			author := strings.TrimPrefix(strings.TrimSpace(row.sourceHandle), "@")
			if model.IsPlaceholderTwitterHandle(author) {
				continue
			}
			statusID := strings.TrimSpace(row.canonicalTweetID)
			if statusID == "" {
				statusID = strings.TrimSpace(row.tweetID)
			}
			canonicalURL := row.canonicalURL
			if shouldRewritePlaceholderXStatusURL(canonicalURL) && statusID != "" {
				canonicalURL = "https://x.com/" + author + "/status/" + statusID
			}
			if _, err := tx.Exec(`
				UPDATE feed_items
				SET author_handle = ?,
				    canonical_url = ?,
				    sync_seq = ?
				WHERE tweet_id = ?
			`, author, nilIfEmpty(canonicalURL), db.NextSyncSeq(), row.tweetID); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				UPDATE videos
				SET channel_id = ?,
				    sync_seq = ?
				WHERE video_id = ?
				  AND LOWER(COALESCE(channel_id, '')) IN ('twitter_', 'twitter_unknown', 'twitter_undefined')
			`, "twitter_"+strings.ToLower(author), db.NextSyncSeq(), row.tweetID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfRows(db.conn, "twitter_placeholder_author_repair", `
			SELECT COUNT(*)
			FROM feed_items
			WHERE COALESCE(is_retweet, 0) = 0
			  AND LOWER(COALESCE(author_handle, '')) IN ('unknown', 'undefined')
			  AND LOWER(COALESCE(source_handle, '')) NOT IN ('', 'unknown', 'undefined')
		`)
	}
	return nil
}

func runLegacyTableRepairs(conn *sql.DB) error {
	if err := dropLegacyChannelAvatarsOnce(conn); err != nil {
		return err
	}
	if err := importLegacyTwitterProfilesOnce(conn); err != nil {
		return err
	}
	return cleanupLegacyAvatarBannerMediaOnce(conn)
}

func dropChannelCheckIntervalOnce(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "drop_channel_check_interval", func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			DELETE FROM settings
			WHERE key IN ('youtube_check_interval', 'shorts_check_interval', 'instagram_check_interval')
		`); err != nil {
			return err
		}
		exists, err := columnExistsTx(tx, "channels", "check_interval")
		if err != nil || !exists {
			return err
		}
		_, err = tx.Exec(`ALTER TABLE channels DROP COLUMN check_interval`)
		return err
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfColumnExists(conn, "drop_channel_check_interval", "channels", "check_interval")
	}
	return nil
}

func dropLegacyChannelAvatarsOnce(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "drop_legacy_channel_avatars", func(tx *sql.Tx) error {
		_, err := tx.Exec("DROP TABLE IF EXISTS channel_avatars")
		return err
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfTableExists(conn, "drop_legacy_channel_avatars", "channel_avatars")
	}
	return nil
}

func importLegacyTwitterProfilesOnce(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "legacy_twitter_profiles_import", func(tx *sql.Tx) error {
		var hasLegacyTwitterProfiles int
		if err := tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='twitter_profiles'").Scan(&hasLegacyTwitterProfiles); err != nil {
			return err
		}
		if hasLegacyTwitterProfiles == 0 {
			return nil
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO channel_profiles (
				channel_id, platform, handle, display_name, bio, website,
				followers, following, verified, verified_type, protected,
				banner_url, fetched_at, fail_count, next_retry_at, tombstone
			)
			SELECT 'twitter_' || handle, 'twitter', handle, display_name, bio, website,
			       followers, following, verified, verified_type, protected,
			       banner_url, fetched_at, fail_count, next_retry_at, tombstone
			FROM twitter_profiles
		`); err != nil {
			return err
		}
		_, err := tx.Exec(`DROP TABLE twitter_profiles`)
		return err
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfTableExists(conn, "legacy_twitter_profiles_import", "twitter_profiles")
	}
	return nil
}

func cleanupLegacyAvatarBannerMediaOnce(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "legacy_avatar_banner_media_cleanup", func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM media_files WHERE owner_type IN ('avatar', 'banner')`)
		return err
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfRows(conn, "legacy_avatar_banner_media_cleanup", `
			SELECT COUNT(*)
			FROM media_files
			WHERE owner_type IN ('avatar', 'banner')
		`)
	}
	return nil
}

func backfillSyncSeqOnce(conn *sql.DB) error {
	ran, err := runSchemaMigrationOnce(conn, "sync_seq_backfill", func(tx *sql.Tx) error {
		for _, stmt := range []string{
			"UPDATE feed_items SET sync_seq = rowid WHERE sync_seq = 0 OR sync_seq IS NULL",
			"UPDATE videos SET sync_seq = id WHERE sync_seq = 0 OR sync_seq IS NULL",
			"UPDATE channels SET sync_seq = id WHERE sync_seq = 0 OR sync_seq IS NULL",
		} {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !ran {
		warnIfRows(conn, "sync_seq_backfill", `
			SELECT COUNT(*) FROM (
				SELECT sync_seq FROM feed_items WHERE sync_seq = 0 OR sync_seq IS NULL
				UNION ALL SELECT sync_seq FROM videos WHERE sync_seq = 0 OR sync_seq IS NULL
				UNION ALL SELECT sync_seq FROM channels WHERE sync_seq = 0 OR sync_seq IS NULL
			)
		`)
	}
	return nil
}

func warnIfTableExists(conn *sql.DB, migrationName, tableName string) {
	var count int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		tableName,
	).Scan(&count); err != nil || count == 0 {
		return
	}
	log.Printf("schema migration %s already applied, but legacy table %s exists; leaving it for investigation", migrationName, tableName)
}

func columnExistsTx(tx *sql.Tx, tableName, columnName string) (bool, error) {
	rows, err := tx.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return false, err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     sql.NullString
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

func warnIfColumnExists(conn *sql.DB, migrationName, tableName, columnName string) {
	rows, err := conn.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     sql.NullString
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return
		}
		if name == columnName {
			log.Printf("schema migration %s already applied, but %s.%s still exists; leaving it for investigation", migrationName, tableName, columnName)
			return
		}
	}
}

func warnIfRows(conn *sql.DB, migrationName, countQuery string) {
	var count int
	if err := conn.QueryRow(countQuery).Scan(&count); err != nil || count == 0 {
		return
	}
	log.Printf("schema migration %s already applied, but %d legacy rows match its repair condition; leaving them for investigation", migrationName, count)
}
