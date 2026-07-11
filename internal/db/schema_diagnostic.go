package db

func schemaDiagnosticStatements() []string {
	return []string{
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

		`CREATE TABLE IF NOT EXISTS android_sync_health_reports (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			cursor          TEXT NOT NULL,
			reported_at_ms  INTEGER NOT NULL,
			payload_json    TEXT NOT NULL,
			verified_assets INTEGER NOT NULL DEFAULT 0,
			pending_assets  INTEGER NOT NULL DEFAULT 0,
			missing_assets  INTEGER NOT NULL DEFAULT 0,
			total_assets    INTEGER NOT NULL DEFAULT 0,
			verified_bytes  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_android_sync_health_cursor ON android_sync_health_reports(cursor, reported_at_ms DESC)`,

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
	}
}
