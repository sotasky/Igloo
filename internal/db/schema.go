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

	stmts := []string{
		// ── Legacy tables (originally created by Python server) ──

		`CREATE TABLE IF NOT EXISTS channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT UNIQUE NOT NULL,
			source_id TEXT,
			name TEXT NOT NULL,
			url TEXT,
			platform TEXT,
			quality TEXT,
			last_checked INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS channel_follows (
			user_id     TEXT    NOT NULL,
			channel_id  TEXT    NOT NULL,
			followed_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, channel_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_channel_follows_channel ON channel_follows(channel_id)`,

		`CREATE TABLE IF NOT EXISTS channel_stars (
			user_id    TEXT    NOT NULL,
			channel_id TEXT    NOT NULL,
			starred_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, channel_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_channel_stars_channel ON channel_stars(channel_id)`,

		`CREATE TABLE IF NOT EXISTS channel_settings (
			channel_id           TEXT PRIMARY KEY,
			media_only           INTEGER,
			include_reposts      INTEGER,
			media_download_limit INTEGER,
			max_videos           INTEGER,
			download_subtitles   INTEGER,
			updated_at           INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (channel_id) REFERENCES channels(channel_id) ON DELETE CASCADE
		)`,

		`CREATE TABLE IF NOT EXISTS videos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id TEXT UNIQUE NOT NULL,
			channel_id TEXT NOT NULL,
			title TEXT,
			description TEXT,
			duration INTEGER,
			thumbnail_path TEXT,
			file_path TEXT,
			file_size INTEGER,
			published_at INTEGER NOT NULL DEFAULT 0,
			downloaded_at INTEGER NOT NULL DEFAULT 0,
			watched INTEGER DEFAULT 0,
			is_temp INTEGER DEFAULT 0,
			is_pinned INTEGER DEFAULT 0,
			metadata_json TEXT,
			source_kind TEXT DEFAULT '',
			dearrow_title TEXT,
			dearrow_title_casual TEXT,
			dearrow_thumb_path TEXT,
			dearrow_checked_at INTEGER
		)`,

		`CREATE TABLE IF NOT EXISTS feed_items (
			tweet_id TEXT PRIMARY KEY,
			source_handle TEXT,
			author_handle TEXT NOT NULL,
			author_display_name TEXT,
			author_avatar_url TEXT,
			body_text TEXT,
			lang TEXT,
			is_retweet INTEGER DEFAULT 0,
			retweeted_by_handle TEXT,
			retweeted_by_display_name TEXT,
			quote_tweet_id TEXT,
			quote_author_handle TEXT,
			quote_author_display_name TEXT,
			quote_author_avatar_url TEXT,
			quote_body_text TEXT,
			quote_lang TEXT,
			quote_media_json TEXT,
			media_json TEXT,
			canonical_url TEXT,
			reply_to_handle TEXT,
			reply_to_status TEXT,
			is_reply INTEGER DEFAULT 0,
			is_ghost INTEGER DEFAULT 0,
			quote_published_at INTEGER NOT NULL DEFAULT 0,
			views INTEGER,
			likes INTEGER,
			retweets INTEGER,
			published_at INTEGER NOT NULL DEFAULT 0,
			fetched_at INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT,
			canonical_tweet_id TEXT,
			media_status TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS feed_sources (
			source_id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			source_type TEXT NOT NULL,
			external_id TEXT NOT NULL,
			label TEXT NOT NULL,
			url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_checked INTEGER,
			last_ok INTEGER,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_feed_sources_platform ON feed_sources(platform, enabled)`,

		`CREATE TABLE IF NOT EXISTS feed_item_sources (
			tweet_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			first_seen_at INTEGER NOT NULL DEFAULT 0,
			last_seen_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tweet_id, source_id),
			FOREIGN KEY (source_id) REFERENCES feed_sources(source_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_feed_item_sources_source ON feed_item_sources(source_id, last_seen_at DESC)`,

		`CREATE TABLE IF NOT EXISTS settings (
			user_id TEXT NOT NULL DEFAULT '',
			key TEXT NOT NULL,
			value TEXT,
			PRIMARY KEY (user_id, key)
		)`,

		`CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at_ms INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS downloader_operations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			operation TEXT NOT NULL,
			platform TEXT NOT NULL,
			subject TEXT NOT NULL DEFAULT '',
			tool TEXT NOT NULL,
			started_at_ms INTEGER NOT NULL,
			ended_at_ms INTEGER NOT NULL,
			status TEXT NOT NULL,
			error_kind TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			cookie_label TEXT NOT NULL DEFAULT '',
			elapsed_ms INTEGER NOT NULL DEFAULT 0,
			item_count INTEGER NOT NULL DEFAULT 0,
			media_count INTEGER NOT NULL DEFAULT 0,
			file_count INTEGER NOT NULL DEFAULT 0,
			bytes INTEGER NOT NULL DEFAULT 0,
			summary_json TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_downloader_operations_recent ON downloader_operations(started_at_ms DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_downloader_operations_summary ON downloader_operations(platform, operation, status, error_kind)`,

		`CREATE TABLE IF NOT EXISTS video_comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id TEXT NOT NULL,
			comment_id TEXT NOT NULL,
			parent_id TEXT,
			author_name TEXT,
			author_id TEXT,
			author_thumbnail TEXT,
			text TEXT,
			like_count INTEGER,
			published_at INTEGER NOT NULL DEFAULT 0,
			platform TEXT DEFAULT 'youtube',
			fetched_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE(video_id, comment_id)
		)`,

		`CREATE TABLE IF NOT EXISTS watch_history (
			user_id TEXT NOT NULL,
			video_id TEXT NOT NULL,
			playback_position REAL DEFAULT 0,
			duration REAL,
			progress_updated_at_ms INTEGER,
			progress_source TEXT,
			last_watched INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, video_id)
		)`,

		`CREATE TABLE IF NOT EXISTS sponsorblock_checked (
			video_id TEXT PRIMARY KEY,
			checked_at INTEGER NOT NULL DEFAULT 0,
			video_age_at_check TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS sponsorblock_segments (
			video_id TEXT NOT NULL,
			start_time REAL NOT NULL,
			end_time REAL NOT NULL,
			category TEXT NOT NULL,
			PRIMARY KEY (video_id, start_time)
		)`,

		`CREATE TABLE IF NOT EXISTS feed_seen (
			username TEXT NOT NULL,
			tweet_id TEXT NOT NULL,
			seen_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (username, tweet_id)
		)`,

		`CREATE TABLE IF NOT EXISTS moment_views (
			username TEXT NOT NULL,
			video_id TEXT NOT NULL,
			viewed_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (username, video_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_moment_views_user_date ON moment_views(username, viewed_at DESC)`,

		`CREATE TABLE IF NOT EXISTS feed_likes (
			username TEXT NOT NULL,
			tweet_id TEXT NOT NULL,
			source_handle TEXT,
			author_handle TEXT,
			author_display_name TEXT,
			body_text TEXT,
			link TEXT,
			canonical_x_link TEXT,
			published_at INTEGER NOT NULL DEFAULT 0,
			media_url TEXT,
			avatar_url TEXT,
			media_json TEXT,
			platform TEXT,
			quote_payload_json TEXT,
			liked_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (username, tweet_id)
		)`,

		`CREATE TABLE IF NOT EXISTS bookmarks (
			user_id TEXT NOT NULL DEFAULT '',
			video_id TEXT NOT NULL,
			category_id INTEGER DEFAULT 0,
			custom_title TEXT,
			account_handles TEXT,
			media_indices TEXT,
			bookmarked_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, video_id)
		)`,

		`CREATE TABLE IF NOT EXISTS bookmark_categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL,
			archive_path TEXT,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS sync_changes (
			version INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			item_id TEXT NOT NULL,
			value TEXT,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS muted_accounts (
			handle TEXT PRIMARY KEY,
			muted_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS retweet_sources (
			content_hash TEXT NOT NULL,
			retweeter_handle TEXT NOT NULL,
			retweeter_display_name TEXT,
			tweet_id TEXT NOT NULL,
			published_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (content_hash, retweeter_handle)
		)`,

		`CREATE TABLE IF NOT EXISTS video_repost_sources (
			video_id TEXT NOT NULL,
			reposter_channel_id TEXT NOT NULL,
			reposter_handle TEXT NOT NULL DEFAULT '',
			reposter_display_name TEXT,
			reposted_at_ms INTEGER NOT NULL DEFAULT 0,
			first_seen_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (video_id, reposter_channel_id)
		)`,

		`CREATE TABLE IF NOT EXISTS channel_profiles (
			channel_id     TEXT PRIMARY KEY,
			platform       TEXT NOT NULL,
			handle         TEXT,
			display_name   TEXT,
			bio            TEXT,
			website        TEXT,
			followers      INTEGER DEFAULT 0,
			following      INTEGER DEFAULT 0,
			verified       INTEGER DEFAULT 0,
			verified_type  TEXT,
			protected      INTEGER DEFAULT 0,
			avatar_url     TEXT,
			banner_url     TEXT,
			fetched_at     INTEGER NOT NULL DEFAULT 0,
			fail_count     INTEGER DEFAULT 0,
			next_retry_at  INTEGER NOT NULL DEFAULT 0,
			tombstone      INTEGER DEFAULT 0
		)`,

		// ── Go-owned tables ──
		`CREATE TABLE IF NOT EXISTS media_files (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_type  TEXT    NOT NULL,
			owner_id    TEXT    NOT NULL,
			media_index INTEGER DEFAULT 0,
			file_path   TEXT    NOT NULL,
			media_type  TEXT,
			source_url  TEXT,
			file_size   INTEGER,
			created_at  INTEGER NOT NULL DEFAULT 0,
			UNIQUE(owner_type, owner_id, media_index)
		)`,

		`CREATE TABLE IF NOT EXISTS android_sync_generations (
			generation_id              TEXT PRIMARY KEY,
			created_at_ms              INTEGER NOT NULL,
			status                     TEXT NOT NULL,
			source_version             TEXT NOT NULL,
			retention_json             TEXT NOT NULL,
			item_count                 INTEGER NOT NULL DEFAULT 0,
			asset_count                INTEGER NOT NULL DEFAULT 0,
			ready_asset_count          INTEGER NOT NULL DEFAULT 0,
			server_missing_asset_count INTEGER NOT NULL DEFAULT 0,
			total_bytes                INTEGER NOT NULL DEFAULT 0,
			content_counts_json        TEXT NOT NULL DEFAULT '{}',
			asset_counts_json          TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_android_sync_generations_latest ON android_sync_generations(status, created_at_ms DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_android_sync_generations_source ON android_sync_generations(source_version)`,

		`CREATE TABLE IF NOT EXISTS android_sync_items (
			generation_id TEXT NOT NULL,
			seq           INTEGER NOT NULL,
			item_kind     TEXT NOT NULL,
			item_id       TEXT NOT NULL,
			payload_json  TEXT NOT NULL,
			PRIMARY KEY (generation_id, seq),
			FOREIGN KEY (generation_id) REFERENCES android_sync_generations(generation_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_android_sync_items_page ON android_sync_items(generation_id, seq)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_android_sync_items_identity ON android_sync_items(generation_id, item_kind, item_id)`,

		`CREATE TABLE IF NOT EXISTS android_sync_assets (
			generation_id        TEXT NOT NULL,
			seq                  INTEGER NOT NULL,
			asset_id             TEXT NOT NULL,
			asset_kind           TEXT NOT NULL,
			owner_id             TEXT NOT NULL,
			owner_kind           TEXT NOT NULL,
			bucket               TEXT NOT NULL,
			server_url           TEXT NOT NULL,
			content_type         TEXT NOT NULL DEFAULT '',
			size_bytes           INTEGER NOT NULL DEFAULT 0,
			sha256               TEXT NOT NULL DEFAULT '',
			state                TEXT NOT NULL DEFAULT 'ready',
			required_reason      TEXT NOT NULL DEFAULT '',
			is_auto              INTEGER,
			audio_language       TEXT NOT NULL DEFAULT '',
			effective_recency_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (generation_id, seq),
			FOREIGN KEY (generation_id) REFERENCES android_sync_generations(generation_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_android_sync_assets_page ON android_sync_assets(generation_id, seq)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_android_sync_assets_identity ON android_sync_assets(generation_id, asset_id, asset_kind)`,
		`CREATE INDEX IF NOT EXISTS idx_android_sync_assets_lookup ON android_sync_assets(asset_id, asset_kind, generation_id)`,

		`CREATE TABLE IF NOT EXISTS android_sync_health_reports (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			generation_id   TEXT NOT NULL,
			reported_at_ms  INTEGER NOT NULL,
			payload_json    TEXT NOT NULL,
			verified_assets INTEGER NOT NULL DEFAULT 0,
			pending_assets  INTEGER NOT NULL DEFAULT 0,
			failed_assets   INTEGER NOT NULL DEFAULT 0,
			missing_assets  INTEGER NOT NULL DEFAULT 0,
			total_assets    INTEGER NOT NULL DEFAULT 0,
			verified_bytes  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_android_sync_health_generation ON android_sync_health_reports(generation_id, reported_at_ms DESC)`,

		`CREATE TABLE IF NOT EXISTS ingest_state (
			handle          TEXT PRIMARY KEY,
			fail_count      INTEGER DEFAULT 0,
			next_retry_at   REAL,
			last_success_at REAL,
			last_attempt_at REAL,
			last_error      TEXT,
			last_http_status INTEGER,
			avg_latency_ms  REAL,
			updated_at      INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS feed_media_jobs (
			tweet_id      TEXT PRIMARY KEY,
			tweet_url     TEXT,
			source_handle TEXT,
			status        TEXT    DEFAULT 'queued',
			media_kind    TEXT,
			slide_count   INTEGER DEFAULT 0,
			retry_count   INTEGER DEFAULT 0,
			priority      INTEGER DEFAULT 0,
			last_error    TEXT,
			created_at    INTEGER NOT NULL DEFAULT 0,
			updated_at    INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS translations (
			tweet_id        TEXT NOT NULL,
			field           TEXT NOT NULL,
			source_lang     TEXT NOT NULL,
			target_lang     TEXT NOT NULL,
			translated_text TEXT NOT NULL,
			translated_at   INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tweet_id, field, target_lang)
		)`,

		`CREATE TABLE IF NOT EXISTS translation_jobs (
			tweet_id        TEXT NOT NULL,
			field           TEXT NOT NULL,
			target_lang     TEXT NOT NULL,
			source_hash     TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'queued',
			priority        INTEGER NOT NULL DEFAULT 0,
			attempts        INTEGER NOT NULL DEFAULT 0,
			next_attempt_at INTEGER NOT NULL DEFAULT 0,
			last_error_kind TEXT NOT NULL DEFAULT '',
			last_error      TEXT NOT NULL DEFAULT '',
			created_at      INTEGER NOT NULL DEFAULT 0,
			updated_at      INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tweet_id, field, target_lang)
		)`,

		`CREATE TABLE IF NOT EXISTS channel_queue (
			channel_id TEXT PRIMARY KEY,
			status     TEXT    DEFAULT 'pending',
			priority   INTEGER DEFAULT 0,
			added_at   INTEGER NOT NULL DEFAULT 0,
			started_at INTEGER NOT NULL DEFAULT 0,
			completed_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS download_queue (
			video_id    TEXT PRIMARY KEY,
			channel_id  TEXT    NOT NULL,
			title       TEXT    DEFAULT '',
			published_at_ms INTEGER NOT NULL DEFAULT 0,
			status      TEXT    DEFAULT 'pending',
			priority    INTEGER DEFAULT 0,
			error       TEXT    DEFAULT '',
			retry_count INTEGER DEFAULT 0,
			added_at    INTEGER NOT NULL DEFAULT 0,
			started_at  INTEGER NOT NULL DEFAULT 0,
			completed_at INTEGER NOT NULL DEFAULT 0
		)`,

		// ── Analytics (Python-era, migrated to Go) ──

		`CREATE TABLE IF NOT EXISTS analytics_events (
			event_id     TEXT PRIMARY KEY,
			event_type   TEXT NOT NULL,
			timestamp_ms INTEGER NOT NULL,
			screen       TEXT,
			content_type TEXT,
			elapsed_ms   INTEGER DEFAULT 0,
			extra_json   TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS analytics_rollups_daily (
			day            TEXT NOT NULL,
			event_type     TEXT NOT NULL,
			screen         TEXT NOT NULL DEFAULT '',
			content_type   TEXT NOT NULL DEFAULT '',
			count          INTEGER DEFAULT 0,
			total_elapsed_ms INTEGER DEFAULT 0,
			PRIMARY KEY (day, event_type, screen, content_type)
		)`,

		`CREATE TABLE IF NOT EXISTS feed_share_account_affinity (
			username         TEXT NOT NULL,
			handle           TEXT NOT NULL,
			score            REAL DEFAULT 0,
			last_event_at_ms INTEGER,
			event_count      INTEGER DEFAULT 0,
			PRIMARY KEY (username, handle)
		)`,

		`CREATE TABLE IF NOT EXISTS feed_share_token_affinity (
			username         TEXT NOT NULL,
			token            TEXT NOT NULL,
			score            REAL DEFAULT 0,
			last_event_at_ms INTEGER,
			event_count      INTEGER DEFAULT 0,
			PRIMARY KEY (username, token)
		)`,

		`CREATE TABLE IF NOT EXISTS feed_rank_snapshot (
			username        TEXT    NOT NULL,
			tweet_id        TEXT    NOT NULL,
			rank_position   INTEGER NOT NULL,
			base_score      REAL    NOT NULL,
			decay_factor    REAL    NOT NULL,
			freshness_bonus REAL    NOT NULL,
			jitter          REAL    NOT NULL,
			diversity_demoted_by REAL NOT NULL DEFAULT 0,
			final_score     REAL    NOT NULL,
			computed_at     INTEGER NOT NULL,
			PRIMARY KEY (username, tweet_id)
		)`,

		// ── Auth (server-side-changes #16) ──
		// auth_sessions tracks each login. A single UPDATE revokes a session,
		// killing every paired access + refresh token on the next probe.
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			session_id         TEXT PRIMARY KEY,
			username           TEXT NOT NULL,
			created_at_ms      INTEGER NOT NULL,
			last_active_at_ms  INTEGER NOT NULL,
			revoked            INTEGER NOT NULL DEFAULT 0,
			revoke_reason      TEXT
		)`,

		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(username, revoked)`,

		// auth_refresh_tokens holds the single-use refresh credential for
		// each session. consumed_at_ms is NULL until the first rotation;
		// seeing a second consume of the same token_id triggers replay
		// detection and revokes the whole session.
		`CREATE TABLE IF NOT EXISTS auth_refresh_tokens (
			token_id           TEXT PRIMARY KEY,
			session_id         TEXT NOT NULL,
			issued_at_ms       INTEGER NOT NULL,
			expires_at_ms      INTEGER NOT NULL,
			consumed_at_ms     INTEGER,
			FOREIGN KEY (session_id) REFERENCES auth_sessions(session_id) ON DELETE CASCADE
		)`,

		`CREATE INDEX IF NOT EXISTS idx_auth_refresh_session ON auth_refresh_tokens(session_id)`,
	}

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
	}
	for _, m := range migrations {
		conn.Exec(m) // errors are expected when column already exists
	}
	reportPhase(opts.Phase, "schema.add_columns", phaseStart)

	phaseStart = time.Now()
	if err := dropChannelCheckIntervalOnce(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.drop_channel_check_interval", phaseStart)

	// Create indexes for sync_seq delta-sync queries.
	phaseStart = time.Now()
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_sync_seq ON feed_items(sync_seq)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_sync_seq ON videos(sync_seq)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_channels_sync_seq ON channels(sync_seq)")

	// Performance indexes for page load queries.
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_channel_published ON videos(channel_id, published_at DESC)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_source_kind ON videos(source_kind, published_at DESC)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_videos_media_shape ON videos(media_kind, slide_count)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_media_files_owner ON media_files(owner_id, media_index)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_media_files_type_id ON media_files(owner_type, id)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_media_files_type_owner ON media_files(owner_type, owner_id, media_index)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_media_jobs_status_tweet ON feed_media_jobs(status, tweet_id)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_translation_jobs_ready ON translation_jobs(status, next_attempt_at, priority, updated_at)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_author_lower ON feed_items(LOWER(author_handle))")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_media_author ON feed_items(author_handle COLLATE NOCASE, published_at DESC) WHERE media_json IS NOT NULL AND media_json != '' AND media_json != '[]' AND is_retweet = 0")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_bookmarks_user_date ON bookmarks(user_id, bookmarked_at DESC)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_bookmarks_video_id ON bookmarks(video_id)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_channels_platform ON channels(platform)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_algo ON feed_items(algo_interest DESC, published_at DESC)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_published ON feed_items(published_at DESC, tweet_id DESC)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_unscored ON feed_items(algo_scored_at) WHERE algo_scored_at = 0")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_quote ON feed_items(quote_tweet_id) WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_items_content_hash ON feed_items(content_hash) WHERE content_hash IS NOT NULL AND content_hash != ''")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_channel_profiles_refresh ON channel_profiles(tombstone, fetched_at) WHERE tombstone = 0")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_channel_profiles_platform ON channel_profiles(platform)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_pos ON feed_rank_snapshot(username, rank_position)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_at ON feed_rank_snapshot(username, computed_at)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_score ON feed_rank_snapshot(username, final_score DESC)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_video ON video_repost_sources(video_id)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_reposter ON video_repost_sources(reposter_channel_id)")
	conn.Exec("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_time ON video_repost_sources(reposted_at_ms DESC, first_seen_at_ms DESC)")
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
