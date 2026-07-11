package db

func schemaUserStateStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS channel_follows (
			channel_id  TEXT    PRIMARY KEY,
			followed_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS channel_stars (
			channel_id TEXT    PRIMARY KEY,
			starred_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS channel_settings (
			channel_id           TEXT PRIMARY KEY,
			media_only           INTEGER,
			include_reposts      INTEGER,
			media_download_limit INTEGER,
			max_videos           INTEGER,
			download_subtitles   INTEGER,
			updated_at           INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT NOT NULL,
			value TEXT,
			PRIMARY KEY (key)
		)`,

		`CREATE TABLE IF NOT EXISTS mutation_clocks (
			kind          TEXT NOT NULL CHECK (kind IN ('like', 'bookmark', 'follow', 'star', 'mute', 'progress')),
			item_key      TEXT NOT NULL,
			action        TEXT NOT NULL CHECK (action IN ('set', 'clear')),
			updated_at_ms INTEGER NOT NULL,
			PRIMARY KEY (kind, item_key)
		) WITHOUT ROWID`,

		`CREATE TABLE IF NOT EXISTS category_create_receipts (
			request_id     TEXT PRIMARY KEY,
			category_id    INTEGER NOT NULL,
			provisional_id TEXT NOT NULL,
			created_at_ms  INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS moments_cursors (
			scope         TEXT PRIMARY KEY CHECK (scope IN ('all', 'following', 'stories')),
			video_id      TEXT NOT NULL,
			position_ms   INTEGER NOT NULL DEFAULT 0,
			sort_at_ms    INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS watch_history (
			video_id          TEXT PRIMARY KEY,
			playback_position REAL NOT NULL DEFAULT 0,
			duration          REAL,
			updated_at_ms     INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS feed_seen (
			tweet_id TEXT PRIMARY KEY,
			seen_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS moment_views (
			video_id TEXT PRIMARY KEY,
			viewed_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_moment_views_date ON moment_views(viewed_at DESC)`,

		`CREATE TABLE IF NOT EXISTS feed_likes (
			tweet_id TEXT PRIMARY KEY,
			liked_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS bookmarks (
			video_id TEXT PRIMARY KEY,
			category_id INTEGER NOT NULL DEFAULT 0,
			custom_title TEXT,
			account_handles TEXT,
			media_indices TEXT,
			bookmarked_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS bookmark_categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			archive_path TEXT,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS muted_channels (
			channel_id TEXT PRIMARY KEY,
			muted_at INTEGER NOT NULL DEFAULT 0
		)`,
	}
}
