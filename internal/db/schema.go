package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// EnsureSchema creates all tables the server needs.
// Called from Open() only (not OpenReadOnly).
// All statements use IF NOT EXISTS, so they are safe to re-run on every start.
func EnsureSchema(conn *sql.DB) error {
	return EnsureSchemaWithOptions(conn, EnsureSchemaOptions{})
}

func EnsureSchemaWithOptions(conn *sql.DB, opts EnsureSchemaOptions) error {
	totalStart := time.Now()
	phaseStart := time.Now()

	stmts := schemaCreateStatements()

	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			return err
		}
	}
	reportPhase(opts.Phase, "schema.create_tables", phaseStart)

	phaseStart = time.Now()
	if err := ensureAndroidSyncRevisionTriggers(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.android_sync_revision_triggers", phaseStart)

	phaseStart = time.Now()
	if err := ensureAndroidSyncHeadTriggers(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.android_sync_head_triggers", phaseStart)

	phaseStart = time.Now()
	indexes := []string{
		// Performance indexes for page load queries.
		"CREATE INDEX IF NOT EXISTS idx_videos_channel_published ON videos(channel_id, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_videos_source_kind ON videos(source_kind, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_videos_media_shape ON videos(media_kind, slide_count)",
		"CREATE INDEX IF NOT EXISTS idx_download_queue_ready ON download_queue(status, next_attempt_at_ms, lease_until_ms, priority, added_at)",
		"CREATE INDEX IF NOT EXISTS idx_translation_jobs_ready ON translation_jobs(status, next_attempt_at, priority, updated_at)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_author_fetched ON feed_items(channel_id, fetched_at DESC, published_at DESC, tweet_id DESC) WHERE channel_id IS NOT NULL AND channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_fetched ON feed_items(fetched_at DESC, tweet_id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_media_author ON feed_items(channel_id, published_at DESC) WHERE media_json IS NOT NULL AND media_json != '' AND media_json != '[]' AND is_retweet = 0",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_source_channel ON feed_items(source_channel_id)",
		"CREATE INDEX IF NOT EXISTS idx_bookmarks_date ON bookmarks(bookmarked_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_channels_platform ON channels(platform)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_algo ON feed_items(algo_interest DESC, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_published ON feed_items(published_at DESC, tweet_id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_reply_parent ON feed_items(reply_to_status, published_at, tweet_id) WHERE reply_to_status IS NOT NULL AND reply_to_status != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_unscored ON feed_items(algo_scored_at) WHERE algo_scored_at = 0",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_quote ON feed_items(quote_tweet_id) WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_content_hash ON feed_items(content_hash) WHERE content_hash IS NOT NULL AND content_hash != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_canonical_tweet ON feed_items(canonical_tweet_id) WHERE canonical_tweet_id IS NOT NULL AND canonical_tweet_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_channel_profiles_refresh ON channel_profiles(tombstone, fetched_at) WHERE tombstone = 0",
		"CREATE INDEX IF NOT EXISTS idx_channel_profiles_platform ON channel_profiles(platform)",
		"CREATE INDEX IF NOT EXISTS idx_channel_profiles_twitter_handle ON channel_profiles(LOWER(COALESCE(handle, ''))) WHERE platform = 'twitter' AND tombstone = 0",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_channel ON feed_items(channel_id, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_quote_channel ON feed_items(quote_channel_id, published_at DESC) WHERE quote_channel_id IS NOT NULL AND quote_channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_reposter_channel ON feed_items(reposter_channel_id, published_at DESC) WHERE reposter_channel_id IS NOT NULL AND reposter_channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_reply_channel ON feed_items(reply_channel_id, published_at DESC) WHERE reply_channel_id IS NOT NULL AND reply_channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_profile_jobs_claim ON profile_jobs(completed_revision, requested_revision, next_attempt_at_ms, lease_until_ms, requested_at_ms)",
		"CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_pos ON feed_rank_snapshot(rank_position)",
		"CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_at ON feed_rank_snapshot(computed_at)",
		"CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_score ON feed_rank_snapshot(final_score DESC)",
		"CREATE INDEX IF NOT EXISTS idx_video_repost_sources_video ON video_repost_sources(video_id)",
		"CREATE INDEX IF NOT EXISTS idx_video_repost_sources_reposter ON video_repost_sources(reposter_channel_id)",
		"CREATE INDEX IF NOT EXISTS idx_video_repost_sources_time ON video_repost_sources(reposted_at_ms DESC, first_seen_at_ms DESC)",
	}
	for _, idx := range indexes {
		if _, err := conn.Exec(idx); err != nil {
			return err
		}
	}
	reportPhase(opts.Phase, "schema.indexes", phaseStart)

	reportPhase(opts.Phase, "schema.total", totalStart)
	return nil
}

func schemaPresent(conn *sql.DB) (bool, error) {
	var present bool
	err := conn.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM sqlite_schema
			WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		)
	`).Scan(&present)
	return present, err
}

// ValidateCurrentSchema rejects databases that were not built from the exact
// current schema. Existing databases are never migrated at normal startup.
func ValidateCurrentSchema(conn *sql.DB) error {
	actual, err := schemaSignature(conn)
	if err != nil {
		return err
	}
	expectedDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	if err := EnsureSchema(expectedDB); err != nil {
		_ = expectedDB.Close()
		return err
	}
	expected, signatureErr := schemaSignature(expectedDB)
	closeErr := expectedDB.Close()
	if signatureErr != nil {
		return signatureErr
	}
	if closeErr != nil {
		return closeErr
	}
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("database schema does not match the current contract")
	}
	return nil
}

func schemaSignature(conn *sql.DB) ([]string, error) {
	rows, err := conn.Query(`
		SELECT type, name, tbl_name, COALESCE(sql, '')
		FROM sqlite_schema
		WHERE type IN ('table', 'index', 'trigger', 'view')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return nil, err
	}
	var signature []string
	var tables []string
	for rows.Next() {
		var objectType, name, table, definition string
		if err := rows.Scan(&objectType, &name, &table, &definition); err != nil {
			_ = rows.Close()
			return nil, err
		}
		signature = append(signature, strings.Join([]string{"object", objectType, name, table, strings.TrimSpace(definition)}, "\x00"))
		if objectType == "table" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, table := range tables {
		columns, err := conn.Query(`PRAGMA table_xinfo(` + quoteSchemaIdentifier(table) + `)`)
		if err != nil {
			return nil, err
		}
		for columns.Next() {
			var cid, notNull, primaryKey, hidden int
			var name, columnType string
			var defaultValue sql.NullString
			if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
				_ = columns.Close()
				return nil, err
			}
			signature = append(signature, fmt.Sprintf(
				"column\x00%s\x00%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d",
				table, cid, name, columnType, notNull, defaultValue.String, primaryKey, hidden,
			))
		}
		if err := columns.Err(); err != nil {
			_ = columns.Close()
			return nil, err
		}
		if err := columns.Close(); err != nil {
			return nil, err
		}
	}
	return signature, nil
}

func quoteSchemaIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
