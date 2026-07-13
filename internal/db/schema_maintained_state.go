package db

func schemaMaintainedStateStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS android_feed_retention (
			id               INTEGER PRIMARY KEY CHECK (id = 1),
			feed_days        INTEGER NOT NULL,
			reconciled_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS media_objects (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			object_id            TEXT UNIQUE NOT NULL,
			object_key           TEXT UNIQUE NOT NULL,
			source_url           TEXT NOT NULL DEFAULT '',
			published_source_url TEXT NOT NULL DEFAULT '',
			storage_class        TEXT NOT NULL CHECK(storage_class IN ('state_ssd', 'bulk_hdd')),
			desired_revision     INTEGER NOT NULL DEFAULT 1,
			published_revision   INTEGER NOT NULL DEFAULT 0,
			file_path            TEXT NOT NULL DEFAULT '',
			content_type         TEXT NOT NULL DEFAULT '',
			size_bytes           INTEGER NOT NULL DEFAULT 0,
			sha256               TEXT NOT NULL DEFAULT '',
			file_mtime_ns        INTEGER NOT NULL DEFAULT 0,
			job_state            TEXT NOT NULL DEFAULT 'queued',
			last_error_kind      TEXT NOT NULL DEFAULT '',
			last_error           TEXT NOT NULL DEFAULT '',
			attempts             INTEGER NOT NULL DEFAULT 0,
			next_attempt_at_ms   INTEGER NOT NULL DEFAULT 0,
			lease_owner          TEXT NOT NULL DEFAULT '',
			lease_until_ms       INTEGER NOT NULL DEFAULT 0,
			created_at_ms        INTEGER NOT NULL DEFAULT 0,
			updated_at_ms        INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_media_objects_claim
		 ON media_objects(job_state, next_attempt_at_ms, lease_until_ms, storage_class, updated_at_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_media_objects_ready_file_path
		 ON media_objects(file_path) WHERE published_revision > 0`,
		`CREATE TABLE IF NOT EXISTS assets (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id           TEXT UNIQUE NOT NULL,
			asset_kind         TEXT NOT NULL,
			owner_kind         TEXT NOT NULL,
			owner_id           TEXT NOT NULL,
			media_index        INTEGER NOT NULL DEFAULT 0,
			object_id          TEXT NOT NULL,
			desired_object_id  TEXT NOT NULL,
			lifecycle_state    TEXT NOT NULL DEFAULT 'active' CHECK(lifecycle_state IN ('active', 'pruned')),
			revision           INTEGER NOT NULL DEFAULT 1,
			is_auto            INTEGER,
			audio_language     TEXT NOT NULL DEFAULT '',
			required_reason    TEXT NOT NULL DEFAULT '',
			created_at_ms      INTEGER NOT NULL DEFAULT 0,
			updated_at_ms      INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (object_id) REFERENCES media_objects(object_id),
			FOREIGN KEY (desired_object_id) REFERENCES media_objects(object_id),
			UNIQUE(asset_kind, owner_kind, owner_id, media_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_owner ON assets(owner_kind, owner_id, asset_kind, media_index)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_object ON assets(object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_desired_object ON assets(desired_object_id)`,
		`CREATE TABLE IF NOT EXISTS video_desires (
			source_channel_id TEXT NOT NULL,
			source_component  TEXT NOT NULL,
			video_id          TEXT NOT NULL,
			source_position   INTEGER NOT NULL DEFAULT 0,
			lane              TEXT NOT NULL CHECK(lane IN ('current', 'backfill')),
			PRIMARY KEY (source_channel_id, source_component, video_id)
		) WITHOUT ROWID`,
		`CREATE INDEX IF NOT EXISTS idx_video_desires_video ON video_desires(video_id, lane, source_position)`,
		`CREATE INDEX IF NOT EXISTS idx_video_desires_source_position ON video_desires(source_channel_id, source_component, source_position, video_id)`,
	}
}
