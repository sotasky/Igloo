package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EnsureSchema creates the current schema for a new database. Existing
// databases advance through ApplySchemaMigrations before validation.
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

	phaseStart = time.Now()
	if err := ensureAndroidSyncRevisionTriggers(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.android_sync_revision_triggers", phaseStart)

	phaseStart = time.Now()
	if err := ensureAndroidSyncHeadTriggers(conn); err != nil {
		return err
	}
	reportPhase(opts.Phase, "schema.android_sync_head_triggers", phaseStart)

	phaseStart = time.Now()
	indexes := []string{
		// Performance indexes for page load queries.
		"CREATE INDEX IF NOT EXISTS idx_videos_channel_published ON videos(channel_id, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_videos_source_kind ON videos(source_kind, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_videos_media_shape ON videos(media_kind, slide_count)",
		"CREATE INDEX IF NOT EXISTS idx_download_queue_ready ON download_queue(status, next_attempt_at_ms, lease_until_ms, added_at_ms)",
		"CREATE INDEX IF NOT EXISTS idx_download_queue_lease ON download_queue(status, lease_until_ms)",
		"CREATE INDEX IF NOT EXISTS idx_translation_jobs_ready ON translation_jobs(target_lang, status, priority DESC, updated_at, tweet_id, field, next_attempt_at)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_author_fetched ON feed_items(channel_id, fetched_at DESC, published_at DESC, tweet_id DESC) WHERE channel_id IS NOT NULL AND channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_fetched ON feed_items(fetched_at DESC, tweet_id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_media_author ON feed_items(channel_id, published_at DESC) WHERE media_json IS NOT NULL AND media_json != '' AND media_json != '[]' AND is_retweet = 0",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_source_channel ON feed_items(source_channel_id)",
		"CREATE INDEX IF NOT EXISTS idx_bookmarks_date ON bookmarks(bookmarked_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_channels_platform ON channels(platform)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_algo ON feed_items(algo_interest DESC, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_published ON feed_items(published_at DESC, tweet_id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_reply_parent ON feed_items(reply_to_status, published_at, tweet_id) WHERE reply_to_status IS NOT NULL AND reply_to_status != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_unscored ON feed_items(algo_scored_at) WHERE algo_scored_at = 0",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_quote ON feed_items(quote_tweet_id) WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_content_hash ON feed_items(content_hash) WHERE content_hash IS NOT NULL AND content_hash != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_canonical_tweet ON feed_items(canonical_tweet_id) WHERE canonical_tweet_id IS NOT NULL AND canonical_tweet_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_seen_cover ON feed_items(tweet_id, quote_tweet_id, canonical_tweet_id, channel_id, source_channel_id, is_ghost)",
		"CREATE INDEX IF NOT EXISTS idx_channel_profiles_refresh ON channel_profiles(tombstone, fetched_at) WHERE tombstone = 0",
		"CREATE INDEX IF NOT EXISTS idx_channel_profiles_platform ON channel_profiles(platform)",
		"CREATE INDEX IF NOT EXISTS idx_channel_profiles_twitter_handle ON channel_profiles(LOWER(COALESCE(handle, ''))) WHERE platform = 'twitter' AND tombstone = 0",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_channel ON feed_items(channel_id, published_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_quote_channel ON feed_items(quote_channel_id, published_at DESC) WHERE quote_channel_id IS NOT NULL AND quote_channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_reposter_channel ON feed_items(reposter_channel_id, published_at DESC) WHERE reposter_channel_id IS NOT NULL AND reposter_channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_feed_items_reply_channel ON feed_items(reply_channel_id, published_at DESC) WHERE reply_channel_id IS NOT NULL AND reply_channel_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_profile_jobs_claim ON profile_jobs(requested_at_ms DESC, channel_id, next_attempt_at_ms, lease_until_ms, lease_owner) WHERE requested_revision > completed_revision",
		"CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_pos ON feed_rank_snapshot(rank_position)",
		"CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_at ON feed_rank_snapshot(computed_at)",
		"CREATE INDEX IF NOT EXISTS idx_feed_rank_snapshot_score ON feed_rank_snapshot(final_score DESC)",
		"CREATE INDEX IF NOT EXISTS idx_video_repost_sources_video ON video_repost_sources(video_id)",
		"CREATE INDEX IF NOT EXISTS idx_video_repost_sources_reposter ON video_repost_sources(reposter_channel_id)",
		"CREATE INDEX IF NOT EXISTS idx_video_repost_sources_time ON video_repost_sources(reposted_at_ms DESC, first_seen_at_ms DESC)",
	}
	for _, idx := range indexes {
		if _, err := conn.Exec(idx); err != nil {
			return err
		}
	}
	reportPhase(opts.Phase, "schema.indexes", phaseStart)

	reportPhase(opts.Phase, "schema.total", totalStart)
	return nil
}

func schemaPresent(conn *sql.DB) (bool, error) {
	var present bool
	err := conn.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM sqlite_schema
			WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		)
	`).Scan(&present)
	return present, err
}

// ValidateCurrentSchema rejects databases that do not match the current
// logical schema after ordered migrations have completed.
func ValidateCurrentSchema(conn *sql.DB) error {
	actual, err := schemaSignature(conn)
	if err != nil {
		return err
	}
	expectedDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	expectedDB.SetMaxOpenConns(1)
	if err := EnsureSchema(expectedDB); err != nil {
		_ = expectedDB.Close()
		return err
	}
	expected, signatureErr := schemaSignature(expectedDB)
	closeErr := expectedDB.Close()
	if signatureErr != nil {
		return signatureErr
	}
	if closeErr != nil {
		return closeErr
	}
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("database schema does not match the current contract: %s", schemaSignatureDifference(actual, expected))
	}
	return nil
}

func schemaSignatureDifference(actual, expected []string) string {
	actualSet := make(map[string]bool, len(actual))
	for _, entry := range actual {
		actualSet[entry] = true
	}
	for _, entry := range expected {
		if !actualSet[entry] {
			return "missing " + strings.ReplaceAll(entry, "\x00", " ")
		}
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, entry := range expected {
		expectedSet[entry] = true
	}
	for _, entry := range actual {
		if !expectedSet[entry] {
			return "unexpected " + strings.ReplaceAll(entry, "\x00", " ")
		}
	}
	return "different object definitions"
}

func schemaSignature(conn *sql.DB) ([]string, error) {
	rows, err := conn.Query(`
		SELECT type, name, tbl_name, COALESCE(sql, '')
		FROM sqlite_schema
		WHERE type IN ('table', 'index', 'trigger', 'view')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return nil, err
	}
	var signature []string
	tableDefinitions := make(map[string]string)
	for rows.Next() {
		var objectType, name, table, definition string
		if err := rows.Scan(&objectType, &name, &table, &definition); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if objectType == "table" {
			signature = append(signature, strings.Join([]string{"object", objectType, name, table}, "\x00"))
			tableDefinitions[name] = definition
		} else {
			signature = append(signature, strings.Join([]string{"object", objectType, name, table, strings.TrimSpace(definition)}, "\x00"))
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for table, definition := range tableDefinitions {
		tableSignature, err := logicalTableSignature(conn, table, definition)
		if err != nil {
			return nil, err
		}
		signature = append(signature, tableSignature...)
	}
	sort.Strings(signature)
	return signature, nil
}

// logicalTableSignature deliberately reads SQLite's schema pragmas rather
// than comparing CREATE TABLE text. ADD COLUMN preserves data but changes the
// stored SQL and column order, neither of which changes the table contract.
func logicalTableSignature(conn *sql.DB, table, definition string) ([]string, error) {
	var signature []string
	for _, option := range tableOptions(definition) {
		signature = append(signature, "table_option\x00"+table+"\x00"+option)
	}
	columns, err := conn.Query(`PRAGMA table_xinfo(` + quoteSchemaIdentifier(table) + `)`)
	if err != nil {
		return nil, err
	}
	for columns.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue sql.NullString
		if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			_ = columns.Close()
			return nil, err
		}
		signature = append(signature, fmt.Sprintf(
			"column\x00%s\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d",
			table, name, columnType, notNull, defaultValue.String, primaryKey, hidden,
		))
	}
	if err := columns.Err(); err != nil {
		_ = columns.Close()
		return nil, err
	}
	if err := columns.Close(); err != nil {
		return nil, err
	}

	foreignKeys, err := conn.Query(`PRAGMA foreign_key_list(` + quoteSchemaIdentifier(table) + `)`)
	if err != nil {
		return nil, err
	}
	for foreignKeys.Next() {
		var id, sequence int
		var referencedTable, from, to, onUpdate, onDelete, match string
		if err := foreignKeys.Scan(&id, &sequence, &referencedTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			_ = foreignKeys.Close()
			return nil, err
		}
		signature = append(signature, strings.Join([]string{"foreign_key", table, referencedTable, from, to, onUpdate, onDelete, match}, "\x00"))
	}
	if err := foreignKeys.Err(); err != nil {
		_ = foreignKeys.Close()
		return nil, err
	}
	if err := foreignKeys.Close(); err != nil {
		return nil, err
	}

	indexes, err := conn.Query(`PRAGMA index_list(` + quoteSchemaIdentifier(table) + `)`)
	if err != nil {
		return nil, err
	}
	type schemaIndex struct {
		name string
	}
	var schemaIndexes []schemaIndex
	for indexes.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := indexes.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = indexes.Close()
			return nil, err
		}
		signature = append(signature, fmt.Sprintf("index\x00%s\x00%s\x00%d\x00%s\x00%d", table, name, unique, origin, partial))
		schemaIndexes = append(schemaIndexes, schemaIndex{name: name})
	}
	if err := indexes.Err(); err != nil {
		_ = indexes.Close()
		return nil, err
	}
	if err := indexes.Close(); err != nil {
		return nil, err
	}
	for _, index := range schemaIndexes {
		indexColumns, err := conn.Query(`PRAGMA index_xinfo(` + quoteSchemaIdentifier(index.name) + `)`)
		if err != nil {
			return nil, err
		}
		for indexColumns.Next() {
			var indexSequence, columnID, descending, key int
			var columnName sql.NullString
			var collation string
			if err := indexColumns.Scan(&indexSequence, &columnID, &columnName, &descending, &collation, &key); err != nil {
				_ = indexColumns.Close()
				return nil, err
			}
			signature = append(signature, fmt.Sprintf(
				"index_column\x00%s\x00%s\x00%d\x00%s\x00%d\x00%s\x00%d",
				table, index.name, indexSequence, columnName.String, descending, collation, key,
			))
		}
		if err := indexColumns.Err(); err != nil {
			_ = indexColumns.Close()
			return nil, err
		}
		if err := indexColumns.Close(); err != nil {
			return nil, err
		}
	}

	for _, check := range tableCheckExpressions(definition) {
		signature = append(signature, "check\x00"+table+"\x00"+check)
	}
	sort.Strings(signature)
	return signature, nil
}

func tableOptions(definition string) []string {
	normalized := strings.Join(strings.Fields(strings.ToUpper(definition)), " ")
	var options []string
	if strings.HasSuffix(normalized, " WITHOUT ROWID") {
		options = append(options, "without_rowid")
	}
	if strings.HasSuffix(normalized, " STRICT") || strings.HasSuffix(normalized, " STRICT, WITHOUT ROWID") || strings.HasSuffix(normalized, " WITHOUT ROWID, STRICT") {
		options = append(options, "strict")
	}
	return options
}

func tableCheckExpressions(definition string) []string {
	upper := strings.ToUpper(definition)
	var checks []string
	for offset := 0; offset < len(upper); {
		index := strings.Index(upper[offset:], "CHECK")
		if index < 0 {
			break
		}
		index += offset
		open := index + len("CHECK")
		for open < len(definition) && (definition[open] == ' ' || definition[open] == '\n' || definition[open] == '\t' || definition[open] == '\r') {
			open++
		}
		if open == len(definition) || definition[open] != '(' {
			offset = open
			continue
		}
		depth := 0
		quote := byte(0)
		for end := open; end < len(definition); end++ {
			ch := definition[end]
			if quote != 0 {
				if ch == quote {
					if end+1 < len(definition) && definition[end+1] == quote {
						end++
						continue
					}
					quote = 0
				}
				continue
			}
			if ch == '\'' || ch == '"' || ch == '`' {
				quote = ch
				continue
			}
			switch ch {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					checks = append(checks, strings.Join(strings.Fields(definition[open:end+1]), " "))
					offset = end + 1
					goto next
				}
			}
		}
		break
	next:
	}
	sort.Strings(checks)
	return checks
}

func quoteSchemaIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
