package com.screwy.igloo.data

import kotlinx.coroutines.runBlocking
import com.screwy.igloo.data.entity.AndroidSyncGenerationEntity
import com.screwy.igloo.data.entity.AndroidSyncItemEntity
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class IglooDatabaseTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    /** Sanity: schema compiles + every declared DAO accessor resolves. */
    @Test fun schemaOpensCleanly() = runBlocking {
        assertEquals(0, db.feedItemDao().count())
        assertEquals(0, db.videoDao().count())
        // Reaching these at all means KSP registered each entity + DAO accessor.
        db.channelDao()
        db.outboxDao()
        db.mediaInventoryDao()
        db.preferenceDao()
        db.cursorDao()
        db.videoRepostSourceDao()
        assertTrue(db.isOpen)
    }

    @Test fun videoRepostSourcesTableAndIndexesExist() {
        val cursor = db.openHelper.readableDatabase.query(
            """
            SELECT COUNT(*)
            FROM sqlite_master
            WHERE name IN (
                'video_repost_sources',
                'idx_video_repost_sources_video',
                'idx_video_repost_sources_reposter',
                'idx_video_repost_sources_time'
            )
            """.trimIndent(),
        )
        cursor.use {
            assertTrue(it.moveToFirst())
            assertEquals(4, it.getInt(0))
        }
    }

    @Test fun videosTableHasCanonicalUrlColumn() {
        assertTableHasColumns("videos", "canonical_url")
    }

    @Test fun videosTableHasMediaModeColumn() {
        assertTableHasColumns("videos", "media_mode")
    }

    @Test fun videosTableHasDisplayTitleColumns() {
        assertTableHasColumns("videos", "display_title", "display_title_casual")
    }

    @Test fun videosTableHasSourceKindColumn() {
        assertTableHasColumns("videos", "source_kind")
    }

    @Test fun videosTableHasDurationLabelColumn() {
        assertTableHasColumns("videos", "duration_label")
    }

    @Test fun feedItemsTableHasQuoteCanonicalUrlColumn() {
        assertTableHasColumns("feed_items", "quote_canonical_url")
    }

    @Test fun videoCommentsTableHasPresentationColumns() {
        assertTableHasColumns("video_comments", "thread_order", "thread_depth", "parent_order", "reply_to_author", "is_creator", "like_count_label")
    }

    @Test fun channelProfilesTableHasCountLabelColumns() {
        assertTableHasColumns("channel_profiles", "followers_label", "following_label")
    }

    @Test fun channelProfilesTableHasProfileUrlColumn() {
        assertTableHasColumns("channel_profiles", "profile_url")
    }

    private fun assertTableHasColumns(table: String, vararg names: String) {
        val cursor = db.openHelper.readableDatabase.query("PRAGMA table_info($table)")
        cursor.use {
            val found = mutableSetOf<String>()
            while (it.moveToNext()) {
                found += it.getString(it.getColumnIndexOrThrow("name"))
            }
            names.forEach { name -> assertTrue("$table missing $name", name in found) }
        }
    }

    private fun assertTableCount(table: String, count: Int) {
        val cursor = db.openHelper.readableDatabase.query("SELECT COUNT(*) FROM $table")
        cursor.use {
            assertTrue(it.moveToFirst())
            assertEquals(table, count, it.getInt(0))
        }
    }

    @Test fun videoRepostSourcesTableHasAuthorLabelColumn() {
        val cursor = db.openHelper.readableDatabase.query("PRAGMA table_info(video_repost_sources)")
        cursor.use {
            var found = false
            while (it.moveToNext()) {
                if (it.getString(it.getColumnIndexOrThrow("name")) == "repost_author_label") {
                    found = true
                    break
                }
            }
            assertTrue(found)
        }
    }

    @Test fun androidSyncAssetOwnerLookupIndexExists() {
        val cursor = db.openHelper.readableDatabase.query(
            """
            SELECT COUNT(*)
            FROM sqlite_master
            WHERE type = 'index'
              AND name IN (
                  'idx_android_sync_assets_owner_kind_state',
                  'idx_android_sync_items_kind_identity'
              )
            """.trimIndent(),
        )
        cursor.use {
            assertTrue(it.moveToFirst())
            assertEquals(2, it.getInt(0))
        }
    }

    @Test fun androidSyncChangedItemsIgnoreUnchangedPayloadsFromImportedGenerations() = runBlocking {
        val dao = db.androidSyncDao()
        dao.upsertGeneration(androidSyncGeneration("old", itemsImportedAtMs = 123L))
        dao.upsertGeneration(androidSyncGeneration("new"))
        dao.upsertItems(
            listOf(
                AndroidSyncItemEntity("old", 1, "feed_items", "same", """{"value":1}"""),
                AndroidSyncItemEntity("old", 2, "feed_items", "changed", """{"value":1}"""),
                AndroidSyncItemEntity("new", 1, "feed_items", "same", """{"value":1}"""),
                AndroidSyncItemEntity("new", 2, "feed_items", "changed", """{"value":2}"""),
                AndroidSyncItemEntity("new", 3, "feed_items", "new", """{"value":1}"""),
            ),
        )

        assertEquals(
            listOf(2L, 3L),
            dao.changedItemSeqsFromPreviousImportedGenerations("new", afterSeq = 0, toSeq = 3),
        )
    }

    @Test fun androidSyncGenerationRefreshPreservesImportMarkers() = runBlocking {
        val dao = db.androidSyncDao()
        dao.upsertGeneration(
            androidSyncGeneration(
                generationId = "gen",
                itemsImportedAtMs = 100L,
                assetsImportedAtMs = 200L,
            ),
        )

        dao.upsertGeneration(androidSyncGeneration("gen"))

        assertEquals(0, dao.countLatestIncompleteImports())
    }

    @Test fun androidSyncRenameMigrationClearsOnlySyncLedger() {
        val writable = db.openHelper.writableDatabase
        writable.execSQL(
            """
            INSERT INTO channels (channel_id, name, platform, created_at)
            VALUES ('channel-1', 'Channel', 'youtube', 1)
            """.trimIndent(),
        )
        writable.execSQL(
            """
            INSERT INTO videos (
                video_id, channel_id, title, duration_label,
                published_at, downloaded_at, slide_count, sync_seq
            ) VALUES (
                'durable-video', 'channel-1', 'Durable', '',
                1, 1, 0, 1
            )
            """.trimIndent(),
        )
        writable.execSQL(
            """
            INSERT INTO android_sync_generations (
                generation_id, created_at_ms, status, source_version, retention_json,
                item_count, asset_count, ready_asset_count, server_missing_asset_count,
                total_bytes, content_counts_json, asset_counts_json,
                items_imported_at_ms, assets_imported_at_ms
            ) VALUES (
                'android-v3-old', 1, 'ready', 'old-source', '{}',
                1, 1, 1, 0, 1, '{}', '{}', 1, 1
            )
            """.trimIndent(),
        )
        writable.execSQL(
            """
            INSERT INTO android_sync_items (generation_id, seq, item_kind, item_id, payload_json)
            VALUES ('android-v3-old', 1, 'videos', 'durable-video', '{}')
            """.trimIndent(),
        )
        writable.execSQL(
            """
            INSERT INTO android_sync_assets (
                generation_id, seq, asset_id, asset_kind, owner_id, owner_kind,
                bucket, server_url, content_type, size_bytes, sha256, server_state,
                required_reason, subtitle_is_auto, audio_language, effective_recency_ms,
                state, local_path, file_size, verified_at_ms, attempt_count,
                next_attempt_at_ms, last_error, updated_at_ms
            ) VALUES (
                'android-v3-old', 1, 'asset-1', 'video_stream', 'durable-video', 'video',
                'videos', '/asset', 'video/mp4', 1, 'sha', 'ready',
                'retention', 1, NULL, 1, 'verified', '/tmp/old.mp4', 1, 1, 0, 0, NULL, 1
            )
            """.trimIndent(),
        )

        IglooMigrations.MIGRATION_28_29.migrate(writable)

        assertTableCount("android_sync_generations", 0)
        assertTableCount("android_sync_items", 0)
        assertTableCount("android_sync_assets", 0)
        assertTableCount("videos", 1)
    }

    @Test fun sanitizeUsername_normalizes() {
        assertEquals("anonymous", IglooDatabase.sanitizeUsername(""))
        assertEquals("alice",     IglooDatabase.sanitizeUsername("Alice"))
        assertEquals("bob.smith", IglooDatabase.sanitizeUsername("bob.smith"))
        assertEquals("c_a_r_o_l", IglooDatabase.sanitizeUsername("c a r o l"))
        assertEquals("a_b_c",     IglooDatabase.sanitizeUsername("a/b\\c"))
        assertEquals(
            "igloo-alice.db",
            IglooDatabase.fileNameFor("Alice"),
        )
    }

    private fun androidSyncGeneration(
        generationId: String,
        itemsImportedAtMs: Long? = null,
        assetsImportedAtMs: Long? = null,
    ): AndroidSyncGenerationEntity =
        AndroidSyncGenerationEntity(
            generationId = generationId,
            createdAtMs = if (generationId == "old") 1L else 2L,
            status = "ready",
            sourceVersion = generationId,
            retentionJson = "{}",
            itemCount = 0,
            assetCount = 0,
            readyAssetCount = 0,
            serverMissingAssetCount = 0,
            totalBytes = 0,
            itemsImportedAtMs = itemsImportedAtMs,
            assetsImportedAtMs = assetsImportedAtMs,
        )
}
