package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesProductionLikeLegacySchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "igloo.db")
	dataDir := filepath.Join(tmpDir, "data")

	seedProductionLikeLegacySchema(t, dbPath)

	d, err := Open(dbPath, dataDir)
	if err != nil {
		t.Fatalf("Open legacy fixture: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	assertSchemaColumnMissing(t, d.conn, "channels", "check_interval")
	for table, columns := range map[string][]string{
		"channels":            {"sync_seq"},
		"videos":              {"media_kind", "slide_count", "source_kind", "sync_seq", "dearrow_title", "dearrow_checked_at"},
		"feed_items":          {"sync_seq", "algo_interest", "algo_scored_at", "is_reply", "is_ghost"},
		"feed_media_jobs":     {"lease_owner", "lease_until_ms", "next_attempt_at_ms", "last_error_kind", "tool", "cookie_label", "started_at_ms", "completed_at_ms"},
		"download_queue":      {"error", "published_at_ms", "lease_owner", "lease_until_ms", "next_attempt_at_ms", "last_error_kind", "last_error_strategy", "tool", "cookie_label"},
		"android_sync_assets": {"is_auto", "audio_language", "media_index"},
		"assets":              {"lease_owner", "lease_until_ms"},
	} {
		for _, column := range columns {
			assertSchemaColumnExists(t, d.conn, table, column)
		}
	}

	if got := schemaTestCount(t, d.conn, `SELECT COUNT(*) FROM settings WHERE key IN ('youtube_check_interval', 'shorts_check_interval', 'instagram_check_interval')`); got != 0 {
		t.Fatalf("retired interval settings = %d, want 0", got)
	}
	assertSchemaTableMissing(t, d.conn, "channel_avatars")
	assertSchemaTableMissing(t, d.conn, "twitter_profiles")
	if got := schemaTestCount(t, d.conn, `SELECT COUNT(*) FROM channel_profiles WHERE channel_id = 'twitter_sample_profile' AND handle = 'sample_profile'`); got != 1 {
		t.Fatalf("imported legacy twitter profiles = %d, want 1", got)
	}

	if got := schemaTestCount(t, d.conn, `SELECT COUNT(*) FROM media_files WHERE owner_type IN ('avatar', 'banner')`); got != 0 {
		t.Fatalf("legacy avatar/banner media rows = %d, want 0", got)
	}
	var minIndex, maxIndex int
	if err := d.conn.QueryRow(`SELECT MIN(media_index), MAX(media_index) FROM media_files WHERE owner_type = 'feed_media' AND owner_id = 'sample_tweet_media'`).Scan(&minIndex, &maxIndex); err != nil {
		t.Fatalf("feed media index range: %v", err)
	}
	if minIndex != 0 || maxIndex != 1 {
		t.Fatalf("feed media index range = %d..%d, want 0..1", minIndex, maxIndex)
	}

	var jobStatus, jobKind string
	var jobSlideCount int
	if err := d.conn.QueryRow(`SELECT status, media_kind, slide_count FROM feed_media_jobs WHERE tweet_id = 'sample_tweet_media'`).Scan(&jobStatus, &jobKind, &jobSlideCount); err != nil {
		t.Fatalf("feed media job: %v", err)
	}
	if jobStatus != "completed" || jobKind != "slideshow" || jobSlideCount != 2 {
		t.Fatalf("feed media job = status %q kind %q slides %d, want completed slideshow 2", jobStatus, jobKind, jobSlideCount)
	}

	var videoKind string
	var videoSlideCount int
	var videoSyncSeq int64
	if err := d.conn.QueryRow(`SELECT media_kind, slide_count, sync_seq FROM videos WHERE video_id = 'sample_tweet_media'`).Scan(&videoKind, &videoSlideCount, &videoSyncSeq); err != nil {
		t.Fatalf("video shape: %v", err)
	}
	if videoKind != "slideshow" || videoSlideCount != 2 || videoSyncSeq == 0 {
		t.Fatalf("video shape = kind %q slides %d sync_seq %d, want slideshow 2 nonzero sync_seq", videoKind, videoSlideCount, videoSyncSeq)
	}

	if got := schemaTestCount(t, d.conn, `SELECT COUNT(*) FROM android_sync_generations WHERE generation_id = 'android-v3-legacy'`); got != 0 {
		t.Fatalf("legacy android v3 generations = %d, want 0", got)
	}
	if got := schemaTestCount(t, d.conn, `SELECT COUNT(*) FROM android_sync_generations WHERE generation_id = 'android-sync-current'`); got != 1 {
		t.Fatalf("current android generations = %d, want 1", got)
	}

	for _, name := range []string{
		"drop_channel_check_interval",
		"legacy_android_v3_generation_cleanup",
		"drop_legacy_channel_avatars",
		"legacy_twitter_profiles_import",
		"legacy_avatar_banner_media_cleanup",
		"sync_seq_backfill",
		"python_feed_media_legacy_fixes",
		"repair_video_media_shapes",
	} {
		assertSchemaMigrationRecorded(t, d, name)
	}

	if err := EnsureSchema(d.conn); err != nil {
		t.Fatalf("EnsureSchema second run on migrated fixture: %v", err)
	}
}

func seedProductionLikeLegacySchema(t *testing.T, dbPath string) {
	t.Helper()
	seed, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = seed.Close()
	}()

	for _, stmt := range []string{
		`CREATE TABLE channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT UNIQUE NOT NULL,
			source_id TEXT,
			name TEXT NOT NULL,
			url TEXT,
			platform TEXT,
			quality TEXT,
			check_interval INTEGER,
			last_checked INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO channels (channel_id, source_id, name, url, platform, quality, check_interval, last_checked, created_at)
		 VALUES ('youtube_sample_channel', 'sample_source', 'Sample Channel', 'https://example.invalid/channel', 'youtube', 'best', 6, 10, 1)`,

		`CREATE TABLE videos (
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
			metadata_json TEXT
		)`,
		`INSERT INTO videos (video_id, channel_id, title, description, duration, thumbnail_path, file_path, file_size, published_at, downloaded_at, metadata_json)
		 VALUES ('sample_tweet_media', 'twitter_sample_profile', 'Legacy media', 'sample', 0, '', '', 0, 1000, 0, '{}')`,

		`CREATE TABLE feed_items (
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
		`INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, author_display_name, body_text, lang,
			is_retweet, quote_media_json, media_json, canonical_url, published_at,
			fetched_at, content_hash, canonical_tweet_id, media_status
		) VALUES (
			'sample_tweet_media', 'sample_source', 'sample_profile', 'Sample Legacy',
			'legacy fixture row', 'en', 0, '[]', '[{"type":"photo"}]',
			'https://example.invalid/sample/status/sample_tweet_media', 1000, 1001,
			'hash_legacy_media', 'sample_tweet_media', 'ready'
		)`,

		`CREATE TABLE settings (
			user_id TEXT NOT NULL DEFAULT '',
			key TEXT NOT NULL,
			value TEXT,
			PRIMARY KEY (user_id, key)
		)`,
		`INSERT INTO settings (user_id, key, value) VALUES ('', 'youtube_check_interval', '6')`,
		`INSERT INTO settings (user_id, key, value) VALUES ('feed', 'shorts_check_interval', '3')`,

		`CREATE TABLE media_files (
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
		`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size, created_at)
		 VALUES ('feed_media', 'sample_tweet_media', 1, '/tmp/legacy-1.jpg', 'image', '', 10, 1)`,
		`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size, created_at)
		 VALUES ('feed_media', 'sample_tweet_media', 2, '/tmp/legacy-2.jpg', 'image', '', 11, 1)`,
		`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size, created_at)
		 VALUES ('avatar', 'twitter_sample_profile', 0, '/tmp/avatar.jpg', 'image', '', 12, 1)`,
		`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size, created_at)
		 VALUES ('banner', 'twitter_sample_profile', 0, '/tmp/banner.jpg', 'image', '', 13, 1)`,

		`CREATE TABLE feed_media_jobs (
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
		`INSERT INTO feed_media_jobs (tweet_id, tweet_url, source_handle, status, media_kind, slide_count, retry_count, priority, created_at, updated_at)
		 VALUES ('sample_tweet_media', 'https://example.invalid/sample/status/sample_tweet_media', 'sample_source', 'ready', 'image', 0, 0, 1, 1, 1)`,

		`CREATE TABLE download_queue (
			video_id    TEXT PRIMARY KEY,
			channel_id  TEXT    NOT NULL,
			title       TEXT    DEFAULT '',
			status      TEXT    DEFAULT 'pending',
			priority    INTEGER DEFAULT 0,
			retry_count INTEGER DEFAULT 0,
			added_at    INTEGER NOT NULL DEFAULT 0,
			started_at  INTEGER NOT NULL DEFAULT 0,
			completed_at INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO download_queue (video_id, channel_id, title, status, priority, retry_count, added_at, started_at, completed_at)
		 VALUES ('legacy_download', 'youtube_sample_channel', 'Legacy download', 'pending', 1, 0, 1, 0, 0)`,

		`CREATE TABLE android_sync_generations (
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
		`CREATE TABLE android_sync_items (
			generation_id TEXT NOT NULL,
			seq           INTEGER NOT NULL,
			item_kind     TEXT NOT NULL,
			item_id       TEXT NOT NULL,
			payload_json  TEXT NOT NULL,
			PRIMARY KEY (generation_id, seq)
		)`,
		`CREATE TABLE android_sync_assets (
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
			effective_recency_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (generation_id, seq)
		)`,
		`CREATE TABLE android_sync_health_reports (
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
		`INSERT INTO android_sync_generations (generation_id, created_at_ms, status, source_version, retention_json, item_count, asset_count, ready_asset_count, server_missing_asset_count, total_bytes)
		 VALUES ('android-v3-legacy', 1, 'ready', 'legacy-source', '{}', 1, 1, 1, 0, 1)`,
		`INSERT INTO android_sync_generations (generation_id, created_at_ms, status, source_version, retention_json, item_count, asset_count, ready_asset_count, server_missing_asset_count, total_bytes)
		 VALUES ('android-sync-current', 2, 'ready', 'current-source', '{}', 1, 1, 1, 0, 1)`,
		`INSERT INTO android_sync_items (generation_id, seq, item_kind, item_id, payload_json)
		 VALUES ('android-v3-legacy', 1, 'videos', 'legacy-video', '{}')`,
		`INSERT INTO android_sync_items (generation_id, seq, item_kind, item_id, payload_json)
		 VALUES ('android-sync-current', 1, 'videos', 'current-video', '{}')`,
		`INSERT INTO android_sync_assets (generation_id, seq, asset_id, asset_kind, owner_id, owner_kind, bucket, server_url, content_type, size_bytes, sha256, state, required_reason, effective_recency_ms)
		 VALUES ('android-v3-legacy', 1, 'legacy-asset', 'video_stream', 'legacy-video', 'video', 'videos', '/asset/legacy', 'video/mp4', 1, 'sha', 'ready', 'retention', 1)`,
		`INSERT INTO android_sync_assets (generation_id, seq, asset_id, asset_kind, owner_id, owner_kind, bucket, server_url, content_type, size_bytes, sha256, state, required_reason, effective_recency_ms)
		 VALUES ('android-sync-current', 1, 'current-asset', 'video_stream', 'current-video', 'video', 'videos', '/asset/current', 'video/mp4', 1, 'sha', 'ready', 'retention', 1)`,
		`INSERT INTO android_sync_health_reports (generation_id, reported_at_ms, payload_json, verified_assets, pending_assets, failed_assets, missing_assets, total_assets, verified_bytes)
		 VALUES ('android-v3-legacy', 1, '{}', 1, 0, 0, 0, 1, 1)`,
		`INSERT INTO android_sync_health_reports (generation_id, reported_at_ms, payload_json, verified_assets, pending_assets, failed_assets, missing_assets, total_assets, verified_bytes)
		 VALUES ('android-sync-current', 2, '{}', 1, 0, 0, 0, 1, 1)`,

		`CREATE TABLE channel_avatars (channel_id TEXT PRIMARY KEY, avatar_url TEXT)`,
		`INSERT INTO channel_avatars (channel_id, avatar_url) VALUES ('twitter_sample_profile', 'https://example.invalid/avatar.jpg')`,

		`CREATE TABLE twitter_profiles (
			handle TEXT PRIMARY KEY,
			display_name TEXT,
			bio TEXT,
			website TEXT,
			followers INTEGER DEFAULT 0,
			following INTEGER DEFAULT 0,
			verified INTEGER DEFAULT 0,
			verified_type TEXT,
			protected INTEGER DEFAULT 0,
			banner_url TEXT,
			fetched_at INTEGER NOT NULL DEFAULT 0,
			fail_count INTEGER DEFAULT 0,
			next_retry_at INTEGER NOT NULL DEFAULT 0,
			tombstone INTEGER DEFAULT 0
		)`,
		`INSERT INTO twitter_profiles (handle, display_name, bio, website, followers, following, verified, verified_type, protected, banner_url, fetched_at, fail_count, next_retry_at, tombstone)
		 VALUES ('sample_profile', 'Sample Legacy', 'bio', 'https://example.invalid', 10, 2, 1, 'blue', 0, 'https://example.invalid/banner.jpg', 100, 0, 0, 0)`,
	} {
		if _, err := seed.Exec(stmt); err != nil {
			t.Fatalf("seed legacy fixture statement %q: %v", stmt, err)
		}
	}
}

func assertSchemaColumnExists(t *testing.T, conn *sql.DB, tableName, columnName string) {
	t.Helper()
	if !schemaTestColumnExists(t, conn, tableName, columnName) {
		t.Fatalf("%s.%s should exist", tableName, columnName)
	}
}

func assertSchemaColumnMissing(t *testing.T, conn *sql.DB, tableName, columnName string) {
	t.Helper()
	if schemaTestColumnExists(t, conn, tableName, columnName) {
		t.Fatalf("%s.%s should be absent", tableName, columnName)
	}
}

func schemaTestColumnExists(t *testing.T, conn *sql.DB, tableName, columnName string) bool {
	t.Helper()
	rows, err := conn.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		t.Fatalf("table_info %s: %v", tableName, err)
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
			t.Fatalf("scan table_info %s: %v", tableName, err)
		}
		if name == columnName {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", tableName, err)
	}
	return false
}

func assertSchemaTableMissing(t *testing.T, conn *sql.DB, tableName string) {
	t.Helper()
	if got := schemaTestCount(t, conn, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, tableName); got != 0 {
		t.Fatalf("table %s exists, want missing", tableName)
	}
}

func schemaTestCount(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}
