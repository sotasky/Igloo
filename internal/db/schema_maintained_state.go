package db

func schemaMaintainedStateStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS assets (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id           TEXT UNIQUE NOT NULL,
			asset_kind         TEXT NOT NULL,
			owner_kind         TEXT NOT NULL,
			owner_id           TEXT NOT NULL,
			media_index        INTEGER NOT NULL DEFAULT 0,
			source_url         TEXT NOT NULL DEFAULT '',
			file_path          TEXT NOT NULL DEFAULT '',
			content_type       TEXT NOT NULL DEFAULT '',
			size_bytes         INTEGER NOT NULL DEFAULT 0,
			sha256             TEXT NOT NULL DEFAULT '',
			file_mtime_ns      INTEGER NOT NULL DEFAULT 0,
			revision           INTEGER NOT NULL DEFAULT 0,
			is_auto            INTEGER,
			audio_language     TEXT NOT NULL DEFAULT '',
			state              TEXT NOT NULL DEFAULT 'queued',
			required_reason    TEXT NOT NULL DEFAULT '',
			last_error_kind    TEXT NOT NULL DEFAULT '',
			last_error         TEXT NOT NULL DEFAULT '',
			attempts           INTEGER NOT NULL DEFAULT 0,
			next_attempt_at_ms INTEGER NOT NULL DEFAULT 0,
			lease_owner        TEXT NOT NULL DEFAULT '',
			lease_until_ms     INTEGER NOT NULL DEFAULT 0,
			created_at_ms      INTEGER NOT NULL DEFAULT 0,
			updated_at_ms      INTEGER NOT NULL DEFAULT 0,
			UNIQUE(asset_kind, owner_kind, owner_id, media_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_owner ON assets(owner_kind, owner_id, asset_kind, media_index)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_claim ON assets(state, next_attempt_at_ms, updated_at_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_kind_state ON assets(asset_kind, state)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_ready_file_path ON assets(file_path) WHERE state = 'ready'`,
	}
}
