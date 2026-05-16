package com.screwy.igloo.data

import android.content.Context
import android.database.sqlite.SQLiteDatabase
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.json.JSONObject
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class IglooMigrationTest {
    @Test fun committedRoomSchemasStartAtSupportedBaseline() {
        val schemaDir = schemaDir()
        assertTrue("missing Room schema directory: ${schemaDir.absolutePath}", schemaDir.isDirectory)

        val versions = schemaDir.listFiles()
            .orEmpty()
            .mapNotNull { file -> file.name.removeSuffix(".json").toIntOrNull() }
            .sorted()

        assertFalse("Room schema directory has no JSON snapshots", versions.isEmpty())
        assertEquals(
            (IglooMigrations.SUPPORTED_SCHEMA_BASELINE_VERSION..IglooMigrations.CURRENT_SCHEMA_VERSION).toList(),
            versions,
        )
    }

    @Test fun migration29To30AddsLanguageSourceColumnsWithoutDroppingFeedRows() {
        val dbName = "igloo-migration-29-30"
        val context: Context = ApplicationProvider.getApplicationContext()
        val sqlite = createDatabaseFromSchemaSnapshot(
            context,
            dbName,
            IglooMigrations.SUPPORTED_SCHEMA_BASELINE_VERSION,
        )
        try {
            sqlite.execSQL(
                """
                INSERT INTO feed_items (
                    tweet_id, author_handle, body_text, quote_body_text,
                    is_retweet, quote_published_at, is_reply, is_ghost,
                    published_at, sync_seq
                ) VALUES (
                    'tweet-29', 'author', 'body', 'quote',
                    0, 0, 0, 0,
                    123, 7
                )
                """.trimIndent(),
            )
        } finally {
            sqlite.close()
        }

        val roomDb = Room.databaseBuilder(context, IglooDatabase::class.java, dbName)
            .addMigrations(
                IglooMigrations.MIGRATION_29_30,
                IglooMigrations.MIGRATION_30_31,
                IglooMigrations.MIGRATION_31_32,
                IglooMigrations.MIGRATION_32_33,
                IglooMigrations.MIGRATION_33_34,
                IglooMigrations.MIGRATION_34_35,
            )
            .allowMainThreadQueries()
            .build()

        try {
            val readable = roomDb.openHelper.readableDatabase
            assertEquals(IglooMigrations.CURRENT_SCHEMA_VERSION, readable.version)
            val cursor = readable.query(
                """
                SELECT body_text, body_source_lang, quote_body_text, quote_source_lang
                FROM feed_items
                WHERE tweet_id = 'tweet-29'
                """.trimIndent(),
            )
            cursor.use {
                assertTrue(it.moveToFirst())
                assertEquals("body", it.getString(0))
                assertNull(it.getString(1))
                assertEquals("quote", it.getString(2))
                assertNull(it.getString(3))
            }
        } finally {
            roomDb.close()
            context.deleteDatabase(dbName)
        }
    }

    @Test fun migration30To31DropsChannelCheckIntervalWithoutDroppingChannels() {
        val dbName = "igloo-migration-30-31"
        val context: Context = ApplicationProvider.getApplicationContext()
        val sqlite = createDatabaseFromSchemaSnapshot(
            context,
            dbName,
            30,
        )
        try {
            sqlite.execSQL(
                """
                INSERT INTO channels (
                    channel_id, source_id, name, url, platform, avatar_url,
                    quality, check_interval, last_checked, created_at
                ) VALUES (
                    'youtube_UCmigration', 'UCmigration', 'Migration Channel',
                    'https://www.youtube.com/channel/UCmigration', 'youtube', '/avatar',
                    '1080p', 6, 1234, 5678
                )
                """.trimIndent(),
            )
        } finally {
            sqlite.close()
        }

        val roomDb = Room.databaseBuilder(context, IglooDatabase::class.java, dbName)
            .addMigrations(
                IglooMigrations.MIGRATION_30_31,
                IglooMigrations.MIGRATION_31_32,
                IglooMigrations.MIGRATION_32_33,
                IglooMigrations.MIGRATION_33_34,
                IglooMigrations.MIGRATION_34_35,
            )
            .allowMainThreadQueries()
            .build()

        try {
            val readable = roomDb.openHelper.readableDatabase
            assertEquals(IglooMigrations.CURRENT_SCHEMA_VERSION, readable.version)
            readable.query("PRAGMA table_info(channels)").use { cursor ->
                while (cursor.moveToNext()) {
                    assertFalse(cursor.getString(1) == "check_interval")
                }
            }
            readable.query(
                """
                SELECT source_id, name, quality, last_checked, created_at
                FROM channels
                WHERE channel_id = 'youtube_UCmigration'
                """.trimIndent(),
            ).use {
                assertTrue(it.moveToFirst())
                assertEquals("UCmigration", it.getString(0))
                assertEquals("Migration Channel", it.getString(1))
                assertEquals("1080p", it.getString(2))
                assertEquals(1234L, it.getLong(3))
                assertEquals(5678L, it.getLong(4))
            }
        } finally {
            roomDb.close()
            context.deleteDatabase(dbName)
        }
    }

    @Test fun migration31To32AddsItemImporterVersionWithoutDroppingImportMarkers() {
        val dbName = "igloo-migration-31-32"
        val context: Context = ApplicationProvider.getApplicationContext()
        val sqlite = createDatabaseFromSchemaSnapshot(
            context,
            dbName,
            31,
        )
        try {
            sqlite.execSQL(
                """
                INSERT INTO android_sync_generations (
                    generation_id, created_at_ms, status, source_version, retention_json,
                    item_count, asset_count, ready_asset_count, server_missing_asset_count,
                    total_bytes, content_counts_json, asset_counts_json,
                    items_imported_at_ms, assets_imported_at_ms
                ) VALUES (
                    'android-v3-old', 1, 'ready', 'source', '{}',
                    1, 0, 0, 0, 0, '{}', '{}', 123, 456
                )
                """.trimIndent(),
            )
        } finally {
            sqlite.close()
        }

        val roomDb = Room.databaseBuilder(context, IglooDatabase::class.java, dbName)
            .addMigrations(
                IglooMigrations.MIGRATION_31_32,
                IglooMigrations.MIGRATION_32_33,
                IglooMigrations.MIGRATION_33_34,
                IglooMigrations.MIGRATION_34_35,
            )
            .allowMainThreadQueries()
            .build()

        try {
            val readable = roomDb.openHelper.readableDatabase
            assertEquals(IglooMigrations.CURRENT_SCHEMA_VERSION, readable.version)
            readable.query(
                """
                SELECT items_imported_at_ms, assets_imported_at_ms, items_importer_version
                FROM android_sync_generations
                WHERE generation_id = 'android-v3-old'
                """.trimIndent(),
            ).use {
                assertTrue(it.moveToFirst())
                assertEquals(123L, it.getLong(0))
                assertEquals(456L, it.getLong(1))
                assertEquals(0, it.getInt(2))
            }
        } finally {
            roomDb.close()
            context.deleteDatabase(dbName)
        }
    }

    @Test fun migration32To33AddsSyncAssetMediaIndexWithoutDroppingAssets() {
        val dbName = "igloo-migration-32-33"
        val context: Context = ApplicationProvider.getApplicationContext()
        val sqlite = createDatabaseFromSchemaSnapshot(
            context,
            dbName,
            32,
        )
        try {
            sqlite.execSQL(
                """
                INSERT INTO android_sync_generations (
                    generation_id, created_at_ms, status, source_version, retention_json,
                    item_count, asset_count, ready_asset_count, server_missing_asset_count,
                    total_bytes, content_counts_json, asset_counts_json,
                    items_imported_at_ms, assets_imported_at_ms, items_importer_version
                ) VALUES (
                    'android-v3-media-index', 1, 'ready', 'source', '{}',
                    0, 1, 1, 0, 0, '{}', '{}', 123, 456, 1
                )
                """.trimIndent(),
            )
            sqlite.execSQL(
                """
                INSERT INTO android_sync_assets (
                    generation_id, seq, asset_id, asset_kind, owner_id, owner_kind,
                    bucket, server_url, size_bytes, server_state, subtitle_is_auto,
                    effective_recency_ms, state, attempt_count, next_attempt_at_ms, updated_at_ms
                ) VALUES (
                    'android-v3-media-index', 1, 'asset-one', 'post_media', 'owner-one', 'tweet',
                    'twitter_media', '/api/media/slide/owner-one/0', 12, 'ready', 1,
                    10, 'desired', 0, 0, 20
                )
                """.trimIndent(),
            )
        } finally {
            sqlite.close()
        }

        val roomDb = Room.databaseBuilder(context, IglooDatabase::class.java, dbName)
            .addMigrations(
                IglooMigrations.MIGRATION_32_33,
                IglooMigrations.MIGRATION_33_34,
                IglooMigrations.MIGRATION_34_35,
            )
            .allowMainThreadQueries()
            .build()

        try {
            val readable = roomDb.openHelper.readableDatabase
            assertEquals(IglooMigrations.CURRENT_SCHEMA_VERSION, readable.version)
            readable.query(
                """
                SELECT asset_id, media_index
                FROM android_sync_assets
                WHERE generation_id = 'android-v3-media-index'
                """.trimIndent(),
            ).use {
                assertTrue(it.moveToFirst())
                assertEquals("asset-one", it.getString(0))
                assertEquals(0, it.getInt(1))
            }
        } finally {
            roomDb.close()
            context.deleteDatabase(dbName)
        }
    }

    @Test fun migration33To34AddsFeedReplyParentIndex() {
        val dbName = "igloo-migration-33-34"
        val context: Context = ApplicationProvider.getApplicationContext()
        val sqlite = createDatabaseFromSchemaSnapshot(
            context,
            dbName,
            33,
        )
        sqlite.close()

        val roomDb = Room.databaseBuilder(context, IglooDatabase::class.java, dbName)
            .addMigrations(IglooMigrations.MIGRATION_33_34, IglooMigrations.MIGRATION_34_35)
            .allowMainThreadQueries()
            .build()

        try {
            val readable = roomDb.openHelper.readableDatabase
            assertEquals(IglooMigrations.CURRENT_SCHEMA_VERSION, readable.version)
            readable.query("PRAGMA index_list(feed_items)").use { cursor ->
                var found = false
                while (cursor.moveToNext()) {
                    if (cursor.getString(1) == "idx_feed_items_reply_parent") {
                        found = true
                    }
                }
                assertTrue(found)
            }
        } finally {
            roomDb.close()
            context.deleteDatabase(dbName)
        }
    }

    @Test fun migration34To35AddsFeedThreadContext() {
        val dbName = "igloo-migration-34-35"
        val context: Context = ApplicationProvider.getApplicationContext()
        val sqlite = createDatabaseFromSchemaSnapshot(
            context,
            dbName,
            34,
        )
        sqlite.close()

        val roomDb = Room.databaseBuilder(context, IglooDatabase::class.java, dbName)
            .addMigrations(IglooMigrations.MIGRATION_34_35)
            .allowMainThreadQueries()
            .build()

        try {
            val readable = roomDb.openHelper.readableDatabase
            assertEquals(IglooMigrations.CURRENT_SCHEMA_VERSION, readable.version)
            readable.query("PRAGMA table_info(feed_thread_context)").use { cursor ->
                val columns = mutableSetOf<String>()
                while (cursor.moveToNext()) {
                    columns += cursor.getString(1)
                }
                assertTrue(columns.containsAll(listOf("leaf_tweet_id", "root_tweet_id", "ancestor_tweet_id", "ancestor_order")))
            }
        } finally {
            roomDb.close()
            context.deleteDatabase(dbName)
        }
    }

    private fun createDatabaseFromSchemaSnapshot(
        context: Context,
        dbName: String,
        version: Int,
    ): SQLiteDatabase {
        context.deleteDatabase(dbName)
        val dbFile = context.getDatabasePath(dbName)
        dbFile.parentFile?.mkdirs()
        val db = SQLiteDatabase.openOrCreateDatabase(dbFile, null)
        val database = JSONObject(schemaFile(version).readText()).getJSONObject("database")

        val entities = database.getJSONArray("entities")
        for (i in 0 until entities.length()) {
            val entity = entities.getJSONObject(i)
            val tableName = entity.getString("tableName")
            db.execSQL(entity.getString("createSql").replace(TABLE_NAME_PLACEHOLDER, tableName))

            val indices = entity.optJSONArray("indices") ?: continue
            for (j in 0 until indices.length()) {
                db.execSQL(
                    indices.getJSONObject(j)
                        .getString("createSql")
                        .replace(TABLE_NAME_PLACEHOLDER, tableName),
                )
            }
        }

        val setupQueries = database.getJSONArray("setupQueries")
        for (i in 0 until setupQueries.length()) {
            db.execSQL(setupQueries.getString(i))
        }
        db.version = version
        return db
    }

    private fun schemaFile(version: Int): File = File(schemaDir(), "$version.json")

    private fun schemaDir(): File = File("schemas/${IglooDatabase::class.java.canonicalName}")

    private companion object {
        const val TABLE_NAME_PLACEHOLDER = "\${TABLE_NAME}"
    }
}
