package com.screwy.igloo.data

import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteDatabase

object IglooMigrations {
    val MIGRATION_40_41 =
        object : Migration(40, 41) {
            override fun migrate(db: SupportSQLiteDatabase) {
                db.execSQL(
                    """
                    CREATE TABLE IF NOT EXISTS `android_sync_assets_new` (
                        `asset_id` TEXT NOT NULL,
                        `asset_kind` TEXT NOT NULL,
                        `media_index` INTEGER NOT NULL,
                        `owner_id` TEXT NOT NULL,
                        `owner_kind` TEXT NOT NULL,
                        `bucket` TEXT NOT NULL,
                        `content_type` TEXT,
                        `size_bytes` INTEGER NOT NULL,
                        `revision` INTEGER NOT NULL,
                        `subtitle_is_auto` INTEGER NOT NULL,
                        `state` TEXT NOT NULL,
                        `local_path` TEXT,
                        `verified_at_ms` INTEGER,
                        `next_attempt_at_ms` INTEGER NOT NULL,
                        PRIMARY KEY(`asset_id`)
                    )
                    """.trimIndent(),
                )
                db.execSQL(
                    """
                    INSERT INTO `android_sync_assets_new` (
                        `asset_id`,
                        `asset_kind`,
                        `media_index`,
                        `owner_id`,
                        `owner_kind`,
                        `bucket`,
                        `content_type`,
                        `size_bytes`,
                        `revision`,
                        `subtitle_is_auto`,
                        `state`,
                        `local_path`,
                        `verified_at_ms`,
                        `next_attempt_at_ms`
                    )
                    SELECT
                        `asset_id`,
                        `asset_kind`,
                        `media_index`,
                        `owner_id`,
                        `owner_kind`,
                        `bucket`,
                        `content_type`,
                        `size_bytes`,
                        `revision`,
                        `subtitle_is_auto`,
                        `state`,
                        `local_path`,
                        `verified_at_ms`,
                        `next_attempt_at_ms`
                    FROM `android_sync_assets`
                    """.trimIndent(),
                )
                db.execSQL("DROP TABLE `android_sync_assets`")
                db.execSQL("ALTER TABLE `android_sync_assets_new` RENAME TO `android_sync_assets`")
                db.execSQL(
                    """
                    CREATE INDEX IF NOT EXISTS `idx_android_sync_assets_claim`
                    ON `android_sync_assets` (`state`, `next_attempt_at_ms`)
                    """.trimIndent(),
                )
                db.execSQL(
                    """
                    CREATE INDEX IF NOT EXISTS `idx_android_sync_assets_owner`
                    ON `android_sync_assets` (`owner_kind`, `owner_id`, `asset_kind`, `media_index`)
                    """.trimIndent(),
                )
            }
        }
}
