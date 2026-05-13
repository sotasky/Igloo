package db

type schemaTableLifecycle string

const (
	schemaLifecycleArchive         schemaTableLifecycle = "archive"
	schemaLifecycleUserState       schemaTableLifecycle = "user_state"
	schemaLifecycleQueue           schemaTableLifecycle = "queue"
	schemaLifecycleDerivedCache    schemaTableLifecycle = "derived_cache"
	schemaLifecycleMaintainedState schemaTableLifecycle = "maintained_state"
	schemaLifecycleDiagnostic      schemaTableLifecycle = "diagnostic"
	schemaLifecycleLegacyMigration schemaTableLifecycle = "legacy_migration"
	schemaLifecycleSecurityState   schemaTableLifecycle = "security_state"
)

var schemaTableLifecycles = map[string]schemaTableLifecycle{
	"analytics_events":            schemaLifecycleDiagnostic,
	"analytics_rollups_daily":     schemaLifecycleDiagnostic,
	"android_sync_assets":         schemaLifecycleDerivedCache,
	"android_sync_generations":    schemaLifecycleDerivedCache,
	"android_sync_health_reports": schemaLifecycleDiagnostic,
	"android_sync_items":          schemaLifecycleDerivedCache,
	"assets":                      schemaLifecycleMaintainedState,
	"auth_refresh_tokens":         schemaLifecycleSecurityState,
	"auth_sessions":               schemaLifecycleSecurityState,
	"bookmark_categories":         schemaLifecycleUserState,
	"bookmarks":                   schemaLifecycleUserState,
	"channel_follows":             schemaLifecycleUserState,
	"channel_profiles":            schemaLifecycleArchive,
	"channel_queue":               schemaLifecycleQueue,
	"channel_settings":            schemaLifecycleUserState,
	"channel_stars":               schemaLifecycleUserState,
	"channels":                    schemaLifecycleArchive,
	"download_queue":              schemaLifecycleQueue,
	"downloader_operations":       schemaLifecycleDiagnostic,
	"feed_item_sources":           schemaLifecycleArchive,
	"feed_items":                  schemaLifecycleArchive,
	"feed_likes":                  schemaLifecycleUserState,
	"feed_media_jobs":             schemaLifecycleQueue,
	"feed_rank_snapshot":          schemaLifecycleDerivedCache,
	"feed_seen":                   schemaLifecycleUserState,
	"feed_share_account_affinity": schemaLifecycleDerivedCache,
	"feed_share_token_affinity":   schemaLifecycleDerivedCache,
	"feed_sources":                schemaLifecycleArchive,
	"ingest_state":                schemaLifecycleQueue,
	"media_files":                 schemaLifecycleArchive,
	"moment_views":                schemaLifecycleUserState,
	"muted_accounts":              schemaLifecycleUserState,
	"retweet_sources":             schemaLifecycleArchive,
	"search_channels_fts":         schemaLifecycleDerivedCache,
	"search_channels_fts_config":  schemaLifecycleDerivedCache,
	"search_channels_fts_content": schemaLifecycleDerivedCache,
	"search_channels_fts_data":    schemaLifecycleDerivedCache,
	"search_channels_fts_docsize": schemaLifecycleDerivedCache,
	"search_channels_fts_idx":     schemaLifecycleDerivedCache,
	"search_videos_fts":           schemaLifecycleDerivedCache,
	"search_videos_fts_config":    schemaLifecycleDerivedCache,
	"search_videos_fts_content":   schemaLifecycleDerivedCache,
	"search_videos_fts_data":      schemaLifecycleDerivedCache,
	"search_videos_fts_docsize":   schemaLifecycleDerivedCache,
	"search_videos_fts_idx":       schemaLifecycleDerivedCache,
	"schema_migrations":           schemaLifecycleLegacyMigration,
	"settings":                    schemaLifecycleUserState,
	"sponsorblock_checked":        schemaLifecycleArchive,
	"sponsorblock_segments":       schemaLifecycleArchive,
	"sync_changes":                schemaLifecycleUserState,
	"translation_jobs":            schemaLifecycleQueue,
	"translations":                schemaLifecycleDerivedCache,
	"video_comments":              schemaLifecycleArchive,
	"video_repost_sources":        schemaLifecycleArchive,
	"videos":                      schemaLifecycleArchive,
	"watch_history":               schemaLifecycleUserState,
}

// SchemaTableLifecycle returns the lifecycle classification for a schema table.
// It is used by runtime audit tooling; tests below keep this map in sync with
// the fresh schema.
func SchemaTableLifecycle(table string) (string, bool) {
	lifecycle, ok := schemaTableLifecycles[table]
	if !ok {
		return "", false
	}
	return string(lifecycle), true
}
