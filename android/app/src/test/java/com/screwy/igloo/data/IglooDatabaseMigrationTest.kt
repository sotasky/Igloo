package com.screwy.igloo.data

import androidx.room.testing.MigrationTestHelper
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class IglooDatabaseMigrationTest {
    @get:Rule
    val helper =
        MigrationTestHelper(
            InstrumentationRegistry.getInstrumentation(),
            IglooDatabase::class.java,
        )

    @Test
    fun migration40To41DropsAssetChecksumWithoutLosingLocalState() {
        helper.createDatabase(DATABASE_NAME, 40).use { db ->
            db.execSQL(
                """
                INSERT INTO android_sync_assets (
                    asset_id,
                    asset_kind,
                    media_index,
                    owner_id,
                    owner_kind,
                    bucket,
                    content_type,
                    size_bytes,
                    sha256,
                    revision,
                    subtitle_is_auto,
                    state,
                    local_path,
                    verified_at_ms,
                    next_attempt_at_ms
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """.trimIndent(),
                arrayOf<Any?>(
                    "sample_asset",
                    "post_media",
                    2,
                    "sample_post",
                    "tweet",
                    "feed",
                    "image/jpeg",
                    123L,
                    "0".repeat(64),
                    7L,
                    1,
                    "ready",
                    "/sample/cache/file.jpg",
                    456L,
                    789L,
                ),
            )
            db.execSQL(
                "INSERT INTO preferences (`key`, `value`, `updated_at`) VALUES (?, ?, ?)",
                arrayOf<Any?>("theme", "sample_theme", 321L),
            )
        }

        helper.runMigrationsAndValidate(
            DATABASE_NAME,
            41,
            true,
            IglooMigrations.MIGRATION_40_41,
        ).use { db ->
            db.query("PRAGMA table_info(android_sync_assets)").use { cursor ->
                val nameIndex = cursor.getColumnIndexOrThrow("name")
                val columns = buildSet {
                    while (cursor.moveToNext()) add(cursor.getString(nameIndex))
                }
                assertFalse(columns.contains("sha256"))
            }
            db.query(
                """
                SELECT asset_id, size_bytes, revision, local_path, verified_at_ms, next_attempt_at_ms
                FROM android_sync_assets
                """.trimIndent(),
            ).use { cursor ->
                cursor.moveToFirst()
                assertEquals("sample_asset", cursor.getString(0))
                assertEquals(123L, cursor.getLong(1))
                assertEquals(7L, cursor.getLong(2))
                assertEquals("/sample/cache/file.jpg", cursor.getString(3))
                assertEquals(456L, cursor.getLong(4))
                assertEquals(789L, cursor.getLong(5))
            }
            db.query("SELECT value FROM preferences WHERE `key` = 'theme'").use { cursor ->
                cursor.moveToFirst()
                assertEquals("sample_theme", cursor.getString(0))
            }
        }
    }

    private companion object {
        const val DATABASE_NAME = "igloo-migration-test"
    }
}
