package db

func schemaQueueStatements() []string {
	return []string{
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

		`CREATE TABLE IF NOT EXISTS download_queue (
			video_id           TEXT PRIMARY KEY,
			owner_channel_id   TEXT NOT NULL,
			title              TEXT NOT NULL DEFAULT '',
			published_at_ms    INTEGER NOT NULL DEFAULT 0,
			status             TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'processing', 'blocked')),
			retry_count        INTEGER NOT NULL DEFAULT 0,
			next_attempt_at_ms INTEGER NOT NULL DEFAULT 0,
			last_error_kind    TEXT NOT NULL DEFAULT '',
			last_error         TEXT NOT NULL DEFAULT '',
			lease_owner        TEXT NOT NULL DEFAULT '',
			lease_until_ms     INTEGER NOT NULL DEFAULT 0,
			added_at_ms        INTEGER NOT NULL DEFAULT 0,
			started_at_ms      INTEGER NOT NULL DEFAULT 0
		)`,
	}
}
