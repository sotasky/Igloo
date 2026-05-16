package com.screwy.igloo.data

import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteDatabase

/**
 * Explicit Room migrations. Every schema bump on `IglooDatabase` adds one
 * here — destructive fallback drops the cache and orphans cached media on
 * disk (files stay; inventory rows go), so trivial column adds must use a
 * proper `ALTER TABLE` migration instead.
 *
 * Committed Room schema JSON starts at [SUPPORTED_SCHEMA_BASELINE_VERSION].
 * Older migration objects stay here for installed databases that still need
 * them, but tests and architecture checks should not expect pruned internal
 * snapshots before that baseline.
 */
object IglooMigrations {
    const val SUPPORTED_SCHEMA_BASELINE_VERSION = 29
    const val CURRENT_SCHEMA_VERSION = 35

    /** Adds `media_inventory.audio_language` for the subtitle auto-on rule. */
    val MIGRATION_7_8 = object : Migration(7, 8) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE media_inventory ADD COLUMN audio_language TEXT DEFAULT NULL")
        }
    }

    val MIGRATION_8_9 = object : Migration(8, 9) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                """
                CREATE TABLE IF NOT EXISTS android_sync_generations (
                    generation_id TEXT NOT NULL PRIMARY KEY,
                    created_at_ms INTEGER NOT NULL,
                    status TEXT NOT NULL,
                    source_version TEXT NOT NULL,
                    retention_json TEXT NOT NULL,
                    item_count INTEGER NOT NULL,
                    asset_count INTEGER NOT NULL,
                    ready_asset_count INTEGER NOT NULL,
                    server_missing_asset_count INTEGER NOT NULL,
                    total_bytes INTEGER NOT NULL,
                    content_counts_json TEXT NOT NULL DEFAULT '{}',
                    asset_counts_json TEXT NOT NULL DEFAULT '{}',
                    items_imported_at_ms INTEGER,
                    assets_imported_at_ms INTEGER,
                    items_importer_version INTEGER NOT NULL DEFAULT 0
                )
                """.trimIndent(),
            )
            db.execSQL(
                """
                CREATE TABLE IF NOT EXISTS android_sync_items (
                    generation_id TEXT NOT NULL,
                    seq INTEGER NOT NULL,
                    item_kind TEXT NOT NULL,
                    item_id TEXT NOT NULL,
                    payload_json TEXT NOT NULL,
                    PRIMARY KEY (generation_id, seq)
                )
                """.trimIndent(),
            )
            db.execSQL("CREATE UNIQUE INDEX IF NOT EXISTS idx_android_sync_items_identity ON android_sync_items(generation_id, item_kind, item_id)")
            db.execSQL(
                """
                CREATE TABLE IF NOT EXISTS android_sync_assets (
                    generation_id TEXT NOT NULL,
                    seq INTEGER NOT NULL,
                    asset_id TEXT NOT NULL,
                    asset_kind TEXT NOT NULL,
                    owner_id TEXT NOT NULL,
                    owner_kind TEXT NOT NULL,
                    bucket TEXT NOT NULL,
                    server_url TEXT NOT NULL,
                    content_type TEXT,
                    size_bytes INTEGER NOT NULL,
                    sha256 TEXT,
                    server_state TEXT NOT NULL,
                    required_reason TEXT,
                    effective_recency_ms INTEGER NOT NULL,
                    state TEXT NOT NULL,
                    local_path TEXT,
                    file_size INTEGER,
                    verified_at_ms INTEGER,
                    attempt_count INTEGER NOT NULL,
                    next_attempt_at_ms INTEGER NOT NULL,
                    last_error TEXT,
                    updated_at_ms INTEGER NOT NULL,
                    PRIMARY KEY (generation_id, asset_id, asset_kind)
                )
                """.trimIndent(),
            )
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_android_sync_assets_page ON android_sync_assets(generation_id, seq)")
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_android_sync_assets_claim ON android_sync_assets(generation_id, state, next_attempt_at_ms)")
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_android_sync_assets_bucket ON android_sync_assets(generation_id, bucket)")
        }
    }

    val MIGRATION_9_10 = object : Migration(9, 10) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE feed_items ADD COLUMN is_reply INTEGER NOT NULL DEFAULT 0")
            db.execSQL("ALTER TABLE feed_items ADD COLUMN is_ghost INTEGER NOT NULL DEFAULT 0")
        }
    }

    val MIGRATION_10_11 = object : Migration(10, 11) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                "CREATE INDEX IF NOT EXISTS idx_android_sync_assets_identity_state " +
                    "ON android_sync_assets(asset_id, asset_kind, server_state, state)",
            )
        }
    }

    val MIGRATION_11_12 = object : Migration(11, 12) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                "CREATE INDEX IF NOT EXISTS idx_android_sync_assets_owner_kind_state " +
                    "ON android_sync_assets(owner_id, asset_kind, server_state, state, verified_at_ms, generation_id)",
            )
        }
    }

    val MIGRATION_12_13 = object : Migration(12, 13) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                "CREATE INDEX IF NOT EXISTS idx_android_sync_items_kind_identity " +
                    "ON android_sync_items(item_kind, item_id, generation_id)",
            )
        }
    }

    val MIGRATION_13_14 = object : Migration(13, 14) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE android_sync_assets ADD COLUMN subtitle_is_auto INTEGER NOT NULL DEFAULT 1")
            db.execSQL("ALTER TABLE android_sync_assets ADD COLUMN audio_language TEXT DEFAULT NULL")
        }
    }

    val MIGRATION_14_15 = object : Migration(14, 15) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                """
                CREATE TABLE IF NOT EXISTS feed_timeline_entries (
                    surface TEXT NOT NULL,
                    position INTEGER NOT NULL,
                    tweet_id TEXT NOT NULL,
                    captured_at_ms INTEGER NOT NULL,
                    PRIMARY KEY(surface, position)
                )
                """.trimIndent(),
            )
            db.execSQL(
                """
                CREATE UNIQUE INDEX IF NOT EXISTS idx_feed_timeline_entries_surface_tweet
                ON feed_timeline_entries(surface, tweet_id)
                """.trimIndent(),
            )
            db.execSQL(
                """
                CREATE INDEX IF NOT EXISTS idx_feed_timeline_entries_capture
                ON feed_timeline_entries(surface, captured_at_ms)
                """.trimIndent(),
            )
        }
    }

    val MIGRATION_15_16 = object : Migration(15, 16) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                """
                CREATE INDEX IF NOT EXISTS idx_feed_rank_position
                ON feed_rank(rank_position, tweet_id)
                """.trimIndent(),
            )
        }
    }

    val MIGRATION_16_17 = object : Migration(16, 17) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                """
                CREATE TABLE IF NOT EXISTS video_repost_sources (
                    video_id TEXT NOT NULL,
                    reposter_channel_id TEXT NOT NULL,
                    reposter_handle TEXT NOT NULL DEFAULT '',
                    reposter_display_name TEXT,
                    reposted_at_ms INTEGER NOT NULL DEFAULT 0,
                    first_seen_at_ms INTEGER NOT NULL DEFAULT 0,
                    updated_at_ms INTEGER NOT NULL DEFAULT 0,
                    PRIMARY KEY(video_id, reposter_channel_id)
                )
                """.trimIndent(),
            )
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_video ON video_repost_sources(video_id)")
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_video_repost_sources_reposter ON video_repost_sources(reposter_channel_id)")
            db.execSQL(
                """
                CREATE INDEX IF NOT EXISTS idx_video_repost_sources_time
                ON video_repost_sources(reposted_at_ms DESC, first_seen_at_ms DESC)
                """.trimIndent(),
            )
        }
    }

    val MIGRATION_17_18 = object : Migration(17, 18) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE videos ADD COLUMN canonical_url TEXT DEFAULT NULL")
        }
    }

    val MIGRATION_18_19 = object : Migration(18, 19) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE video_repost_sources ADD COLUMN repost_author_label TEXT NOT NULL DEFAULT ''")
            db.execSQL(
                """
                UPDATE video_repost_sources
                SET repost_author_label = CASE
                    WHEN TRIM(COALESCE(reposter_display_name, '')) != '' THEN TRIM(reposter_display_name)
                    WHEN TRIM(COALESCE(reposter_handle, '')) = '' THEN ''
                    WHEN SUBSTR(TRIM(reposter_handle), 1, 1) = '@' THEN TRIM(reposter_handle)
                    ELSE '@' || TRIM(reposter_handle)
                END
                WHERE TRIM(COALESCE(repost_author_label, '')) = ''
                """.trimIndent(),
            )
        }
    }

    val MIGRATION_19_20 = object : Migration(19, 20) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE videos ADD COLUMN media_mode TEXT DEFAULT NULL")
            db.execSQL(
                """
                UPDATE videos
                SET media_mode = CASE
                    WHEN COALESCE(slide_count, 0) > 1
                         OR LOWER(TRIM(COALESCE(media_kind, ''))) = 'slideshow'
                        THEN 'slideshow'
                    WHEN LOWER(TRIM(COALESCE(media_kind, ''))) IN ('image', 'photo')
                        THEN 'image'
                    ELSE 'video'
                END
                WHERE TRIM(COALESCE(media_mode, '')) = ''
                """.trimIndent(),
            )
        }
    }

    val MIGRATION_20_21 = object : Migration(20, 21) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE videos ADD COLUMN display_title TEXT DEFAULT NULL")
            db.execSQL("ALTER TABLE videos ADD COLUMN display_title_casual TEXT DEFAULT NULL")
            db.execSQL(
                """
                UPDATE videos
                SET display_title = CASE
                        WHEN TRIM(COALESCE(dearrow_title, '')) != '' THEN dearrow_title
                        ELSE title
                    END,
                    display_title_casual = CASE
                        WHEN TRIM(COALESCE(dearrow_title_casual, '')) != '' THEN dearrow_title_casual
                        WHEN TRIM(COALESCE(dearrow_title, '')) != '' THEN dearrow_title
                        ELSE title
                    END
                WHERE TRIM(COALESCE(display_title, '')) = ''
                   OR TRIM(COALESCE(display_title_casual, '')) = ''
                """.trimIndent(),
            )
        }
    }

    val MIGRATION_21_22 = object : Migration(21, 22) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE feed_items ADD COLUMN quote_canonical_url TEXT DEFAULT NULL")
        }
    }

    val MIGRATION_22_23 = object : Migration(22, 23) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE videos ADD COLUMN source_kind TEXT DEFAULT ''")
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_videos_source_kind ON videos(source_kind, published_at DESC)")
        }
    }

    val MIGRATION_23_24 = object : Migration(23, 24) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE video_comments ADD COLUMN thread_order INTEGER NOT NULL DEFAULT 0")
            db.execSQL("ALTER TABLE video_comments ADD COLUMN thread_depth INTEGER NOT NULL DEFAULT 0")
            db.execSQL("ALTER TABLE video_comments ADD COLUMN parent_order INTEGER NOT NULL DEFAULT 0")
            db.execSQL("ALTER TABLE video_comments ADD COLUMN reply_to_author TEXT NOT NULL DEFAULT ''")
            db.execSQL("ALTER TABLE video_comments ADD COLUMN is_creator INTEGER NOT NULL DEFAULT 0")
        }
    }

    val MIGRATION_24_25 = object : Migration(24, 25) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE video_comments ADD COLUMN like_count_label TEXT NOT NULL DEFAULT ''")
        }
    }

    val MIGRATION_25_26 = object : Migration(25, 26) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE channel_profiles ADD COLUMN followers_label TEXT NOT NULL DEFAULT ''")
            db.execSQL("ALTER TABLE channel_profiles ADD COLUMN following_label TEXT NOT NULL DEFAULT ''")
        }
    }

    val MIGRATION_26_27 = object : Migration(26, 27) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE videos ADD COLUMN duration_label TEXT NOT NULL DEFAULT ''")
        }
    }

    val MIGRATION_27_28 = object : Migration(27, 28) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE channel_profiles ADD COLUMN profile_url TEXT DEFAULT NULL")
        }
    }

    val MIGRATION_28_29 = object : Migration(28, 29) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("DELETE FROM android_sync_assets")
            db.execSQL("DELETE FROM android_sync_items")
            db.execSQL("DELETE FROM android_sync_generations")
        }
    }

    val MIGRATION_29_30 = object : Migration(29, 30) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE feed_items ADD COLUMN body_source_lang TEXT DEFAULT NULL")
            db.execSQL("ALTER TABLE feed_items ADD COLUMN quote_source_lang TEXT DEFAULT NULL")
        }
    }

    val MIGRATION_30_31 = object : Migration(30, 31) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                """
                CREATE TABLE IF NOT EXISTS channels_new (
                    channel_id TEXT NOT NULL,
                    source_id TEXT,
                    name TEXT NOT NULL,
                    url TEXT,
                    platform TEXT NOT NULL,
                    avatar_url TEXT,
                    quality TEXT,
                    last_checked INTEGER,
                    created_at INTEGER NOT NULL,
                    PRIMARY KEY(channel_id)
                )
                """.trimIndent(),
            )
            db.execSQL(
                """
                INSERT INTO channels_new (
                    channel_id, source_id, name, url, platform,
                    avatar_url, quality, last_checked, created_at
                )
                SELECT channel_id, source_id, name, url, platform,
                       avatar_url, quality, last_checked, created_at
                FROM channels
                """.trimIndent(),
            )
            db.execSQL("DROP TABLE channels")
            db.execSQL("ALTER TABLE channels_new RENAME TO channels")
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_channels_platform ON channels(platform)")
        }
    }

    val MIGRATION_31_32 = object : Migration(31, 32) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE android_sync_generations ADD COLUMN items_importer_version INTEGER NOT NULL DEFAULT 0")
        }
    }

    val MIGRATION_32_33 = object : Migration(32, 33) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("ALTER TABLE android_sync_assets ADD COLUMN media_index INTEGER NOT NULL DEFAULT 0")
        }
    }

    val MIGRATION_33_34 = object : Migration(33, 34) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_feed_items_reply_parent ON feed_items(reply_to_status)")
        }
    }

    val MIGRATION_34_35 = object : Migration(34, 35) {
        override fun migrate(db: SupportSQLiteDatabase) {
            db.execSQL(
                """
                CREATE TABLE IF NOT EXISTS feed_thread_context (
                    leaf_tweet_id TEXT NOT NULL,
                    root_tweet_id TEXT NOT NULL,
                    ancestor_tweet_id TEXT NOT NULL,
                    ancestor_order INTEGER NOT NULL,
                    PRIMARY KEY(leaf_tweet_id, ancestor_order)
                )
                """.trimIndent(),
            )
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_feed_thread_context_leaf ON feed_thread_context(leaf_tweet_id)")
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_feed_thread_context_root ON feed_thread_context(root_tweet_id)")
            db.execSQL("CREATE INDEX IF NOT EXISTS idx_feed_thread_context_ancestor ON feed_thread_context(ancestor_tweet_id)")
        }
    }

    val ALL: Array<Migration> = arrayOf(
        MIGRATION_7_8,
        MIGRATION_8_9,
        MIGRATION_9_10,
        MIGRATION_10_11,
        MIGRATION_11_12,
        MIGRATION_12_13,
        MIGRATION_13_14,
        MIGRATION_14_15,
        MIGRATION_15_16,
        MIGRATION_16_17,
        MIGRATION_17_18,
        MIGRATION_18_19,
        MIGRATION_19_20,
        MIGRATION_20_21,
        MIGRATION_21_22,
        MIGRATION_22_23,
        MIGRATION_23_24,
        MIGRATION_24_25,
        MIGRATION_25_26,
        MIGRATION_26_27,
        MIGRATION_27_28,
        MIGRATION_28_29,
        MIGRATION_29_30,
        MIGRATION_30_31,
        MIGRATION_31_32,
        MIGRATION_32_33,
        MIGRATION_33_34,
        MIGRATION_34_35,
    )
}
