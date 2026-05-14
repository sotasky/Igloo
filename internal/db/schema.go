package db

import (
	"database/sql"
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

	// Migrations: add columns to pre-existing tables (idempotent — duplicate column errors are expected).
	// These handle DBs created by the Python server before the Go rewrite owned these tables.
	// SQLite does not allow ADD COLUMN with CURRENT_TIMESTAMP default, so nullable columns are used.
	phaseStart = time.Now()
	migrations := []string{
		"ALTER TABLE download_queue ADD COLUMN error TEXT DEFAULT ''",
		"ALTER TABLE download_queue ADD COLUMN published_at_ms INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE download_queue ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE download_queue ADD COLUMN lease_until_ms INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE download_queue ADD COLUMN next_attempt_at_ms INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE download_queue ADD COLUMN last_error_kind TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE download_queue ADD COLUMN last_error_strategy TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE download_queue ADD COLUMN tool TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE download_queue ADD COLUMN cookie_label TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE feed_media_jobs ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE feed_media_jobs ADD COLUMN lease_until_ms INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE feed_media_jobs ADD COLUMN next_attempt_at_ms INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE feed_media_jobs ADD COLUMN last_error_kind TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE feed_media_jobs ADD COLUMN tool TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE feed_media_jobs ADD COLUMN cookie_label TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE feed_media_jobs ADD COLUMN started_at_ms INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE feed_media_jobs ADD COLUMN completed_at_ms INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE feed_items ADD COLUMN sync_seq INTEGER DEFAULT 0",
		"ALTER TABLE feed_items ADD COLUMN algo_interest REAL DEFAULT 0",
		"ALTER TABLE feed_items ADD COLUMN algo_scored_at INTEGER DEFAULT 0",
		"ALTER TABLE videos ADD COLUMN media_kind TEXT DEFAULT ''",
		"ALTER TABLE videos ADD COLUMN slide_count INTEGER DEFAULT 0",
		"ALTER TABLE videos ADD COLUMN source_kind TEXT DEFAULT ''",
		// #6 bundle-delta endpoints: each primary-row table needs a
		// per-row monotonic counter so the client's `since=<marker>`
		// query can fetch only what's changed since its last cursor.
		"ALTER TABLE videos ADD COLUMN sync_seq INTEGER DEFAULT 0",
		"ALTER TABLE channels ADD COLUMN sync_seq INTEGER DEFAULT 0",
		// DeArrow enrichment columns (nullable — NULL = unchecked).
		"ALTER TABLE videos ADD COLUMN dearrow_title TEXT",
		"ALTER TABLE videos ADD COLUMN dearrow_title_casual TEXT",
		"ALTER TABLE videos ADD COLUMN dearrow_thumb_path TEXT",
		"ALTER TABLE videos ADD COLUMN dearrow_checked_at INTEGER",
		"ALTER TABLE feed_items ADD COLUMN is_reply INTEGER DEFAULT 0",
		"ALTER TABLE feed_items ADD COLUMN is_ghost INTEGER DEFAULT 0",
		"ALTER TABLE android_sync_assets ADD COLUMN is_auto INTEGER",
		"ALTER TABLE android_sync_assets ADD COLUMN audio_language TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE android_sync_assets ADD COLUMN media_index INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE assets ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE assets ADD COLUMN lease_until_ms INTEGER NOT NULL DEFAULT 0",
	}
	for _, m := range migrations {
		_, _ = conn.Exec(m) // errors are expected when column already exists
	}
	reportPhase(opts.Phase, "schema.add_columns", phaseStart)

	phaseStart = time.Now()
	if err := dropChannelCheckIntervalOnce(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.drop_channel_check_interval", phaseStart)

	// Create indexes for sync_seq delta-sync queries.
	phaseStart = time.Now()
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_sync_seq ON feed_items(sync_seq)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_sync_seq ON videos(sync_seq)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_channels_sync_seq ON channels(sync_seq)")
	_, _ =

		// Performance indexes for page load queries.
		conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_channel_published ON videos(channel_id, published_at DESC)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_source_kind ON videos(source_kind, published_at DESC)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_media_shape ON videos(media_kind, slide_count)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_media_files_owner ON media_files(owner_id, media_index)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_media_files_type_id ON media_files(owner_type, id)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_media_files_type_owner ON media_files(owner_type, owner_id, media_index)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_media_jobs_status_tweet ON feed_media_jobs(status, tweet_id)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_media_jobs_ready ON feed_media_jobs(status, next_attempt_at_ms, lease_until_ms, priority, updated_at)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_download_queue_ready ON download_queue(status, next_attempt_at_ms, lease_until_ms, priority, added_at)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_translation_jobs_ready ON translation_jobs(status, next_attempt_at, priority, updated_at)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_author_lower ON feed_items(LOWER(author_handle))")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_media_author ON feed_items(author_handle COLLATE NOCASE, published_at DESC) WHERE media_json IS NOT NULL AND media_json != '' AND media_json != '[]' AND is_retweet = 0")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_bookmarks_user_date ON bookmarks(user_id, bookmarked_at DESC)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_bookmarks_video_id ON bookmarks(video_id)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_channels_platform ON channels(platform)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_algo ON feed_items(algo_interest DESC, published_at DESC)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_published ON feed_items(published_at DESC, tweet_id DESC)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_reply_parent ON feed_items(reply_to_status, published_at, tweet_id) WHERE reply_to_status IS NOT NULL AND reply_to_status != ''")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_unscored ON feed_items(algo_scored_at) WHERE algo_scored_at = 0")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_quote ON feed_items(quote_tweet_id) WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_content_hash ON feed_items(content_hash) WHERE content_hash IS NOT NULL AND content_hash != ''")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_channel_profiles_refresh ON channel_profiles(tombstone, fetched_at) WHERE tombstone = 0")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_channel_profiles_platform ON channel_profiles(platform)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_pos ON feed_rank_snapshot(username, rank_position)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_at ON feed_rank_snapshot(username, computed_at)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_score ON feed_rank_snapshot(username, final_score DESC)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_video ON video_repost_sources(video_id)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_reposter ON video_repost_sources(reposter_channel_id)")
	_, _ = conn.Exec("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_time ON video_repost_sources(reposted_at_ms DESC, first_seen_at_ms DESC)")
	reportPhase(opts.Phase, "schema.indexes", phaseStart)

	phaseStart = time.Now()
	if err := cleanupLegacyAndroidSyncGenerations(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.android_sync_cleanup", phaseStart)

	phaseStart = time.Now()
	if err := runLegacyTableRepairs(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.legacy_table_repairs", phaseStart)

	phaseStart = time.Now()
	if err := cleanupYouTubeCommentAuthorProfilesOnce(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.youtube_comment_profile_cleanup", phaseStart)

	// Backfill: seed sync_seq for existing rows that have no value yet.
	phaseStart = time.Now()
	if err := backfillSyncSeqOnce(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.sync_seq_backfill", phaseStart)

	// Normalize Python-era data: fix 1-based media indices and legacy statuses.
	// These run before status migration because 'ready' identifies Python-era rows.
	phaseStart = time.Now()
	if err := runFeedMediaLegacyFixes(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.feed_media_legacy_fixes", phaseStart)

	reportPhase(opts.Phase, "schema.total", totalStart)
	return nil
}
