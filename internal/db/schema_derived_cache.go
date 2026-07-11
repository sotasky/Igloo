package db

func schemaDerivedCacheStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS translations (
			tweet_id        TEXT NOT NULL,
			field           TEXT NOT NULL,
			source_lang     TEXT NOT NULL,
			target_lang     TEXT NOT NULL,
			translated_text TEXT NOT NULL,
			translated_at   INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tweet_id, field, target_lang)
		)`,

		`CREATE TABLE IF NOT EXISTS feed_share_account_affinity (
			handle           TEXT PRIMARY KEY,
			score            REAL DEFAULT 0,
			last_event_at_ms INTEGER,
			event_count      INTEGER DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS feed_share_token_affinity (
			token            TEXT PRIMARY KEY,
			score            REAL DEFAULT 0,
			last_event_at_ms INTEGER,
			event_count      INTEGER DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS feed_rank_snapshot (
			tweet_id        TEXT    PRIMARY KEY,
			rank_position   INTEGER NOT NULL,
			base_score      REAL    NOT NULL,
			decay_factor    REAL    NOT NULL,
			freshness_bonus REAL    NOT NULL,
			jitter          REAL    NOT NULL,
			diversity_demoted_by REAL NOT NULL DEFAULT 0,
			final_score     REAL    NOT NULL,
			computed_at     INTEGER NOT NULL
		)`,
	}
}
