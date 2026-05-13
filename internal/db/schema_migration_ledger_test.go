package db

import "testing"

func TestSchemaMigrationLedgerRecordsStartupRepairs(t *testing.T) {
	d := openSchemaSnapshotDB(t)

	for _, name := range []string{
		"drop_channel_check_interval",
		"legacy_android_v3_generation_cleanup",
		"drop_legacy_channel_avatars",
		"legacy_twitter_profiles_import",
		"legacy_avatar_banner_media_cleanup",
		"remove_youtube_comment_author_profiles",
		"sync_seq_backfill",
		"python_feed_media_legacy_fixes",
		"cleanup_retired_reading_feature",
		"repair_video_media_shapes",
	} {
		assertSchemaMigrationRecorded(t, d, name)
	}
}
