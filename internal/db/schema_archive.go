package db

func schemaArchiveStatements() []string {
	return []string{
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

		`CREATE TABLE IF NOT EXISTS videos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id TEXT UNIQUE NOT NULL,
			channel_id TEXT NOT NULL,
			owner_kind TEXT NOT NULL CHECK(owner_kind IN ('tweet', 'youtube_video', 'instagram_reel', 'tiktok_video')),
			title TEXT,
			description TEXT,
			duration INTEGER,
			published_at INTEGER NOT NULL DEFAULT 0,
			downloaded_at INTEGER NOT NULL DEFAULT 0,
			is_temp INTEGER DEFAULT 0,
			is_pinned INTEGER DEFAULT 0,
			metadata_json TEXT,
			media_kind TEXT DEFAULT '',
			slide_count INTEGER DEFAULT 0,
			source_kind TEXT DEFAULT '',
			dearrow_title TEXT,
			dearrow_title_casual TEXT,
			dearrow_checked_at INTEGER
		)`,

		`CREATE TABLE IF NOT EXISTS feed_items (
			tweet_id TEXT PRIMARY KEY,
			source_channel_id TEXT,
			channel_id TEXT,
			body_text TEXT,
			lang TEXT,
			is_retweet INTEGER DEFAULT 0,
			quote_tweet_id TEXT,
			quote_channel_id TEXT,
			quote_body_text TEXT,
			quote_lang TEXT,
			quote_media_json TEXT,
			media_json TEXT,
			canonical_url TEXT,
			reply_channel_id TEXT,
			reply_to_status TEXT,
			reposter_channel_id TEXT,
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
			algo_interest REAL DEFAULT 0,
			algo_scored_at INTEGER DEFAULT 0
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

		`CREATE TABLE IF NOT EXISTS video_comments (
			video_id TEXT NOT NULL,
			comment_id TEXT NOT NULL,
			parent_id TEXT,
			author_name TEXT,
			author_id TEXT,
			text TEXT,
			like_count INTEGER,
			published_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(video_id, comment_id)
		) WITHOUT ROWID`,

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

		`CREATE TABLE IF NOT EXISTS retweet_sources (
			content_hash TEXT NOT NULL,
			retweeter_channel_id TEXT NOT NULL,
			tweet_id TEXT NOT NULL,
			published_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (content_hash, retweeter_channel_id)
		)`,

		`CREATE TABLE IF NOT EXISTS video_repost_sources (
			video_id TEXT NOT NULL,
			reposter_channel_id TEXT NOT NULL,
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
			observed_at_ms INTEGER NOT NULL DEFAULT 0,
			fetched_at     INTEGER NOT NULL DEFAULT 0,
			tombstone      INTEGER DEFAULT 0
		)`,

		`CREATE VIEW IF NOT EXISTS feed_items_resolved AS
			SELECT fi.*,
			       COALESCE(source_profile.handle, '') AS source_handle,
			       COALESCE(author_profile.handle, '') AS author_handle,
			       COALESCE(author_profile.display_name, '') AS author_display_name,
			       CASE WHEN COALESCE(fi.channel_id, '') = '' THEN ''
			            ELSE '/api/media/avatar/' || fi.channel_id END AS author_avatar_url,
			       COALESCE(reposter_profile.handle, '') AS retweeted_by_handle,
			       COALESCE(reposter_profile.display_name, '') AS retweeted_by_display_name,
			       COALESCE(quote_profile.display_name, '') AS quote_author_display_name,
			       COALESCE(quote_profile.handle, '') AS quote_author_handle,
			       CASE WHEN COALESCE(fi.quote_channel_id, '') = '' THEN ''
			            ELSE '/api/media/avatar/' || fi.quote_channel_id END AS quote_author_avatar_url,
			       COALESCE(reply_profile.handle, '') AS reply_to_handle
			FROM feed_items fi
			LEFT JOIN channel_profiles source_profile
			  ON source_profile.channel_id = fi.source_channel_id AND source_profile.tombstone = 0
			LEFT JOIN channel_profiles author_profile
			  ON author_profile.channel_id = fi.channel_id AND author_profile.tombstone = 0
			LEFT JOIN channel_profiles reposter_profile
			  ON reposter_profile.channel_id = fi.reposter_channel_id AND reposter_profile.tombstone = 0
			LEFT JOIN channel_profiles quote_profile
			  ON quote_profile.channel_id = fi.quote_channel_id AND quote_profile.tombstone = 0
			LEFT JOIN channel_profiles reply_profile
			  ON reply_profile.channel_id = fi.reply_channel_id AND reply_profile.tombstone = 0`,

		`CREATE VIEW IF NOT EXISTS retweet_sources_resolved AS
			SELECT rs.content_hash, rs.retweeter_channel_id,
			       COALESCE(cp.handle, '') AS retweeter_handle,
			       cp.display_name AS retweeter_display_name,
			       rs.tweet_id, rs.published_at
			FROM retweet_sources rs
			LEFT JOIN channel_profiles cp
			  ON cp.channel_id = rs.retweeter_channel_id AND cp.tombstone = 0`,

		`CREATE VIEW IF NOT EXISTS video_repost_sources_resolved AS
			SELECT vrs.video_id, vrs.reposter_channel_id,
			       COALESCE(cp.handle, '') AS reposter_handle,
			       cp.display_name AS reposter_display_name,
			       vrs.reposted_at_ms, vrs.first_seen_at_ms, vrs.updated_at_ms
			FROM video_repost_sources vrs
			LEFT JOIN channel_profiles cp
			  ON cp.channel_id = vrs.reposter_channel_id AND cp.tombstone = 0`,

		`CREATE TABLE IF NOT EXISTS profile_jobs (
			channel_id          TEXT PRIMARY KEY,
			requested_revision  INTEGER NOT NULL DEFAULT 1,
			completed_revision  INTEGER NOT NULL DEFAULT 0,
			requested_at_ms     INTEGER NOT NULL DEFAULT 0,
			lease_owner         TEXT NOT NULL DEFAULT '',
			lease_until_ms      INTEGER NOT NULL DEFAULT 0,
			attempts            INTEGER NOT NULL DEFAULT 0,
			next_attempt_at_ms  INTEGER NOT NULL DEFAULT 0,
			last_error          TEXT NOT NULL DEFAULT '',
			updated_at_ms       INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (channel_id) REFERENCES channel_profiles(channel_id) ON DELETE CASCADE
		)`,
	}
}
