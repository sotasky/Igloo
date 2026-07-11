package db

import (
	"database/sql"
	"fmt"
	"strings"
)

type androidSyncHeadTable struct {
	table         string
	ownerKind     string
	idColumn      string
	updateColumns string
}

var androidSyncPayloadUpdateColumns = map[string]string{
	"channels": `channel_id, source_id, name, url, platform`,
	"feed_items": `tweet_id, body_text, lang,
		is_retweet, quote_tweet_id, quote_body_text, quote_lang,
		quote_media_json, media_json, canonical_url, reply_to_status,
		is_reply, is_ghost, quote_published_at, views, likes, retweets, published_at,
		content_hash, canonical_tweet_id, source_channel_id, channel_id, reposter_channel_id,
		quote_channel_id, reply_channel_id`,
	"videos": `video_id, channel_id, owner_kind, title, description, duration, published_at,
		metadata_json, media_kind, slide_count, source_kind, dearrow_title,
		dearrow_title_casual`,
}

var androidSyncHeadTables = []androidSyncHeadTable{
	{table: "channels", ownerKind: "channel", idColumn: "channel_id", updateColumns: androidSyncPayloadUpdateColumns["channels"]},
	{table: "channel_profiles", ownerKind: "channel", idColumn: "channel_id", updateColumns: `channel_id, platform, handle, display_name, bio, website,
		followers, following, verified, verified_type, protected, tombstone`},
	{table: "feed_items", ownerKind: "feed", idColumn: "tweet_id", updateColumns: androidSyncPayloadUpdateColumns["feed_items"]},
	{table: "translations", ownerKind: "feed", idColumn: "tweet_id", updateColumns: "tweet_id, field, source_lang, target_lang, translated_text, translated_at"},
	{table: "videos", ownerKind: "video", idColumn: "video_id", updateColumns: androidSyncPayloadUpdateColumns["videos"]},
	{table: "video_comments", ownerKind: "video", idColumn: "video_id", updateColumns: "video_id, comment_id, parent_id, author_name, author_id, text, like_count, published_at"},
	{table: "video_repost_sources", ownerKind: "video", idColumn: "video_id", updateColumns: "video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms"},
	{table: "sponsorblock_checked", ownerKind: "video", idColumn: "video_id", updateColumns: "video_id, checked_at, video_age_at_check"},
	{table: "sponsorblock_segments", ownerKind: "video", idColumn: "video_id", updateColumns: "video_id, start_time, end_time, category"},
	{table: "retweet_sources", ownerKind: "retweet_sources", idColumn: "content_hash", updateColumns: "content_hash, retweeter_channel_id, tweet_id, published_at"},
	{table: "feed_rank_snapshot", ownerKind: "feed_rank", idColumn: "tweet_id", updateColumns: "tweet_id, rank_position, base_score, decay_factor, freshness_bonus, jitter, diversity_demoted_by, final_score, computed_at"},
	{table: "assets", ownerKind: "asset", idColumn: "asset_id", updateColumns: `asset_id, asset_kind, owner_kind, owner_id, media_index,
		file_path, content_type, size_bytes, sha256, is_auto, audio_language, state, required_reason`},
	{table: "feed_likes", ownerKind: "feed_like", idColumn: "tweet_id", updateColumns: "tweet_id, liked_at"},
	{table: "bookmarks", ownerKind: "bookmark", idColumn: "video_id", updateColumns: "video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at"},
	{table: "bookmark_categories", ownerKind: "bookmark_category", idColumn: "id", updateColumns: "id, name, archive_path, created_at"},
	{table: "feed_seen", ownerKind: "feed_seen", idColumn: "tweet_id", updateColumns: "tweet_id, seen_at"},
	{table: "moment_views", ownerKind: "moment_view", idColumn: "video_id", updateColumns: "video_id, viewed_at"},
	{table: "watch_history", ownerKind: "watch_history", idColumn: "video_id", updateColumns: "video_id, playback_position, duration, updated_at_ms"},
	{table: "muted_channels", ownerKind: "muted_channel", idColumn: "channel_id", updateColumns: "channel_id, muted_at"},
	{table: "channel_follows", ownerKind: "channel_follow", idColumn: "channel_id", updateColumns: "channel_id, followed_at"},
	{table: "channel_stars", ownerKind: "channel_star", idColumn: "channel_id", updateColumns: "channel_id, starred_at"},
	{table: "channel_settings", ownerKind: "channel_setting", idColumn: "channel_id", updateColumns: "channel_id, media_only, include_reposts, media_download_limit, max_videos, download_subtitles, updated_at"},
	{table: "moments_cursors", ownerKind: "moments_cursor", idColumn: "scope", updateColumns: "scope, video_id, position_ms, sort_at_ms, updated_at_ms"},
}

func schemaAndroidSyncRevisionStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS android_sync_clock (
			id       INTEGER PRIMARY KEY CHECK (id = 1),
			epoch    TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT OR IGNORE INTO android_sync_clock (id, epoch, revision)
		 VALUES (1, lower(hex(randomblob(16))), 0)`,
		`CREATE TABLE IF NOT EXISTS android_sync_heads (
			owner_kind TEXT NOT NULL,
			owner_id   TEXT NOT NULL,
			revision   INTEGER NOT NULL,
			PRIMARY KEY (owner_kind, owner_id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_android_sync_heads_revision
		 ON android_sync_heads(revision)`,
	}
}

func ensureAndroidSyncHeadTriggers(conn *sql.DB) error {
	for _, spec := range androidSyncHeadTables {
		id := spec.idColumn
		insert := fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS android_sync_head_%s_insert
			 AFTER INSERT ON %s
			 WHEN TRIM(CAST(NEW.%s AS TEXT)) != ''
			 BEGIN
			   UPDATE android_sync_clock SET revision = revision + 1 WHERE id = 1;
			   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
			   VALUES (%s, CAST(NEW.%s AS TEXT), (SELECT revision FROM android_sync_clock WHERE id = 1))
			   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
			     revision = excluded.revision;
			 END`,
			spec.table, spec.table, id, quoteAndroidSyncSQLString(spec.ownerKind), id,
		)
		update := fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS android_sync_head_%s_update
			 AFTER UPDATE OF %s ON %s
			 WHEN (%s) AND TRIM(CAST(NEW.%s AS TEXT)) != ''
			 BEGIN
			   UPDATE android_sync_clock SET revision = revision + 1
			   WHERE id = 1 AND OLD.%s IS NOT NEW.%s;
			   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
			   SELECT %s, CAST(OLD.%s AS TEXT), revision
			   FROM android_sync_clock
			   WHERE id = 1 AND OLD.%s IS NOT NEW.%s AND TRIM(CAST(OLD.%s AS TEXT)) != ''
			   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
			     revision = excluded.revision;
			   UPDATE android_sync_clock SET revision = revision + 1 WHERE id = 1;
			   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
			   VALUES (%s, CAST(NEW.%s AS TEXT), (SELECT revision FROM android_sync_clock WHERE id = 1))
			   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
			     revision = excluded.revision;
			 END`,
			spec.table, spec.updateColumns, spec.table,
			androidSyncColumnsChanged(spec.updateColumns), id,
			id, id,
			quoteAndroidSyncSQLString(spec.ownerKind), id, id, id, id,
			quoteAndroidSyncSQLString(spec.ownerKind), id,
		)
		deleteTrigger := fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS android_sync_head_%s_delete
			 AFTER DELETE ON %s
			 WHEN TRIM(CAST(OLD.%s AS TEXT)) != ''
			 BEGIN
			   UPDATE android_sync_clock SET revision = revision + 1 WHERE id = 1;
			   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
			   VALUES (%s, CAST(OLD.%s AS TEXT), (SELECT revision FROM android_sync_clock WHERE id = 1))
			   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
			     revision = excluded.revision;
			 END`,
			spec.table, spec.table, id, quoteAndroidSyncSQLString(spec.ownerKind), id,
		)
		for _, statement := range []string{insert, update, deleteTrigger} {
			if _, err := conn.Exec(statement); err != nil {
				return fmt.Errorf("create Android sync head trigger for %s: %w", spec.table, err)
			}
		}
	}
	for _, statement := range androidSyncAuxiliaryHeadTriggers() {
		if _, err := conn.Exec(statement); err != nil {
			return fmt.Errorf("create Android sync auxiliary head trigger: %w", err)
		}
	}
	return nil
}

func androidSyncAuxiliaryHeadTriggers() []string {
	relevantSettings := `'moments_include_reposts_default', 'instagram_include_tagged_default',
		'include_reposts_default', 'translate_target_lang', 'translate_skip_langs'`
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS android_sync_head_settings_insert
		 AFTER INSERT ON settings
		 WHEN NEW.key IN (` + relevantSettings + `)
		 BEGIN
		   UPDATE android_sync_clock SET revision = revision + 1 WHERE id = 1;
		   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		   VALUES ('setting', NEW.key, (SELECT revision FROM android_sync_clock WHERE id = 1))
		   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
		     revision = excluded.revision;
		 END`,
		`CREATE TRIGGER IF NOT EXISTS android_sync_head_settings_update
		 AFTER UPDATE OF key, value ON settings
		 WHEN (OLD.key IS NOT NEW.key OR OLD.value IS NOT NEW.value)
		   AND (OLD.key IN (` + relevantSettings + `) OR NEW.key IN (` + relevantSettings + `))
		 BEGIN
		   UPDATE android_sync_clock SET revision = revision + 1
		   WHERE id = 1 AND OLD.key IS NOT NEW.key AND OLD.key IN (` + relevantSettings + `);
		   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		   SELECT 'setting', OLD.key, revision
		   FROM android_sync_clock
		   WHERE id = 1 AND OLD.key IS NOT NEW.key AND OLD.key IN (` + relevantSettings + `)
		   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
		     revision = excluded.revision;
		   UPDATE android_sync_clock SET revision = revision + 1
		   WHERE id = 1 AND NEW.key IN (` + relevantSettings + `);
		   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		   SELECT 'setting', NEW.key, revision
		   FROM android_sync_clock
		   WHERE id = 1 AND NEW.key IN (` + relevantSettings + `)
		   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
		     revision = excluded.revision;
		 END`,
		`CREATE TRIGGER IF NOT EXISTS android_sync_head_settings_delete
		 AFTER DELETE ON settings
		 WHEN OLD.key IN (` + relevantSettings + `)
		 BEGIN
		   UPDATE android_sync_clock SET revision = revision + 1 WHERE id = 1;
		   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		   VALUES ('setting', OLD.key, (SELECT revision FROM android_sync_clock WHERE id = 1))
		   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
		     revision = excluded.revision;
		 END`,
	}
	triggers = append(triggers, androidSyncProtectionHydrationTriggers("feed_likes", "tweet_id")...)
	triggers = append(triggers, androidSyncProtectionHydrationTriggers("bookmarks", "video_id")...)
	return append(triggers, androidSyncFeedOldEdgeTriggers()...)
}

func androidSyncFeedOldEdgeTriggers() []string {
	hashUpdateWhere := "peer.content_hash = OLD.content_hash AND peer.tweet_id != OLD.tweet_id"
	hashDeleteWhere := "peer.content_hash = OLD.content_hash"
	return []string{
		androidSyncFeedOldEdgeTrigger(
			"hash_update", "UPDATE OF content_hash",
			"OLD.content_hash IS NOT NEW.content_hash AND COALESCE(OLD.content_hash, '') != ''",
			"(SELECT MIN(peer.tweet_id) FROM feed_items peer WHERE "+hashUpdateWhere+")",
			"SELECT 1 FROM feed_items peer WHERE "+hashUpdateWhere,
		),
		androidSyncFeedOldEdgeTrigger(
			"hash_delete", "DELETE", "COALESCE(OLD.content_hash, '') != ''",
			"(SELECT MIN(peer.tweet_id) FROM feed_items peer WHERE "+hashDeleteWhere+")",
			"SELECT 1 FROM feed_items peer WHERE "+hashDeleteWhere,
		),
		androidSyncFeedOldEdgeTrigger(
			"quote_update", "UPDATE OF quote_tweet_id",
			"OLD.quote_tweet_id IS NOT NEW.quote_tweet_id AND COALESCE(OLD.quote_tweet_id, '') != ''",
			"OLD.quote_tweet_id", "SELECT 1 FROM feed_items WHERE tweet_id = OLD.quote_tweet_id",
		),
		androidSyncFeedOldEdgeTrigger(
			"quote_delete", "DELETE", "COALESCE(OLD.quote_tweet_id, '') != ''",
			"OLD.quote_tweet_id", "SELECT 1 FROM feed_items WHERE tweet_id = OLD.quote_tweet_id",
		),
		androidSyncFeedOldEdgeTrigger(
			"reply_update", "UPDATE OF reply_to_status",
			"OLD.reply_to_status IS NOT NEW.reply_to_status AND COALESCE(OLD.reply_to_status, '') != ''",
			"OLD.reply_to_status", "SELECT 1 FROM feed_items WHERE tweet_id = OLD.reply_to_status",
		),
		androidSyncFeedOldEdgeTrigger(
			"reply_delete", "DELETE", "COALESCE(OLD.reply_to_status, '') != ''",
			"OLD.reply_to_status", "SELECT 1 FROM feed_items WHERE tweet_id = OLD.reply_to_status",
		),
	}
}

func androidSyncFeedOldEdgeTrigger(name, event, when, ownerID, survivorQuery string) string {
	return fmt.Sprintf(
		`CREATE TRIGGER IF NOT EXISTS android_sync_head_feed_old_%s
		 AFTER %s ON feed_items
		 WHEN %s
		 BEGIN
		   UPDATE android_sync_clock SET revision = revision + 1
		   WHERE id = 1 AND EXISTS (%s);
		   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		   SELECT 'feed', %s, revision
		   FROM android_sync_clock
		   WHERE id = 1 AND EXISTS (%s)
		   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET revision = excluded.revision;
		 END`,
		name, event, when, survivorQuery, ownerID, survivorQuery,
	)
}

func androidSyncProtectionHydrationTriggers(table, idColumn string) []string {
	type event struct {
		name, operation, owner, when string
	}
	events := []event{
		{name: "insert", operation: "INSERT", owner: "NEW." + idColumn, when: "1"},
		{name: "delete", operation: "DELETE", owner: "OLD." + idColumn, when: "1"},
		{name: "update_old", operation: "UPDATE OF " + idColumn, owner: "OLD." + idColumn, when: "OLD." + idColumn + " IS NOT NEW." + idColumn},
		{name: "update_new", operation: "UPDATE OF " + idColumn, owner: "NEW." + idColumn, when: "OLD." + idColumn + " IS NOT NEW." + idColumn},
	}
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS android_sync_head_%s_hydrate_%s
		 AFTER %s ON %s
		 WHEN (%s) AND TRIM(CAST(%s AS TEXT)) != ''
		 BEGIN
		   UPDATE android_sync_clock SET revision = revision + 1 WHERE id = 1;
		   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		   VALUES ('feed', CAST(%s AS TEXT), (SELECT revision FROM android_sync_clock WHERE id = 1))
		   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
		     revision = excluded.revision;
		   UPDATE android_sync_clock SET revision = revision + 1 WHERE id = 1;
		   INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		   VALUES ('video', CAST(%s AS TEXT), (SELECT revision FROM android_sync_clock WHERE id = 1))
		   ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
		     revision = excluded.revision;
		 END`,
			table, event.name, event.operation, table, event.when, event.owner, event.owner, event.owner,
		))
	}
	return out
}

func quoteAndroidSyncSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func ensureAndroidSyncRevisionTriggers(conn *sql.DB) error {
	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS android_sync_revision_assets_insert
		 AFTER INSERT ON assets
		 WHEN NEW.state != 'pruned'
		 BEGIN
		   UPDATE assets SET revision = MAX(revision, 1) WHERE id = NEW.id;
		 END`,
		`CREATE TRIGGER IF NOT EXISTS android_sync_revision_assets_update
		 AFTER UPDATE OF asset_id, asset_kind, owner_kind, owner_id, media_index,
		                 file_path, content_type, size_bytes, sha256, is_auto,
		                 audio_language, state, required_reason
		 ON assets
		 WHEN OLD.asset_id IS NOT NEW.asset_id
		   OR OLD.asset_kind IS NOT NEW.asset_kind
		   OR OLD.owner_kind IS NOT NEW.owner_kind
		   OR OLD.owner_id IS NOT NEW.owner_id
		   OR OLD.media_index IS NOT NEW.media_index
		   OR (CASE
		         WHEN OLD.state = 'pruned' THEN 'pruned'
		         WHEN OLD.state = 'ready' THEN 'ready'
		         WHEN OLD.state IN ('server_missing', 'permanent_missing') THEN 'missing'
		         ELSE 'pending'
		       END) IS NOT (CASE
		         WHEN NEW.state = 'pruned' THEN 'pruned'
		         WHEN NEW.state = 'ready' THEN 'ready'
		         WHEN NEW.state IN ('server_missing', 'permanent_missing') THEN 'missing'
		         ELSE 'pending'
		       END)
		   OR ((OLD.state IN ('ready', 'server_missing', 'permanent_missing')
		        OR NEW.state IN ('ready', 'server_missing', 'permanent_missing'))
		       AND (OLD.file_path IS NOT NEW.file_path
		         OR OLD.content_type IS NOT NEW.content_type
		         OR OLD.size_bytes IS NOT NEW.size_bytes
		         OR OLD.sha256 IS NOT NEW.sha256
		         OR OLD.is_auto IS NOT NEW.is_auto
		         OR OLD.audio_language IS NOT NEW.audio_language
		         OR OLD.required_reason IS NOT NEW.required_reason))
		 BEGIN
		   UPDATE assets SET revision = revision + 1 WHERE id = NEW.id;
		 END`,
	}
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			return fmt.Errorf("create Android sync revision trigger: %w", err)
		}
	}
	return nil
}

func androidSyncColumnsChanged(columns string) string {
	parts := strings.Split(columns, ",")
	changed := make([]string, 0, len(parts))
	for _, part := range parts {
		column := strings.TrimSpace(part)
		changed = append(changed, "OLD."+column+" IS NOT NEW."+column)
	}
	return strings.Join(changed, " OR ")
}
