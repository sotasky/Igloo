package com.screwy.igloo.data

import com.screwy.igloo.data.entity.MediaInventoryEntity
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class MediaInventoryDaoTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    private fun entry(
        assetId: String,
        assetKind: String,
        ownerId: String? = null,
        bucket: String,
        state: String = "pending",
        localPath: String? = null,
        fileSize: Long? = null,
    ) = MediaInventoryEntity(
        assetId = assetId,
        assetKind = assetKind,
        ownerId = ownerId,
        scope = "sync_compat",
        bucket = bucket,
        serverUrl = "/assets/$assetId",
        state = state,
        localPath = localPath,
        fileSize = fileSize,
    )

    @Test fun forOwner_returnsAllRowsForOwner() = runBlocking {
        val dao = db.mediaInventoryDao()
        dao.upsert(listOf(
            entry("one", "post_thumbnail", ownerId = "post-1", bucket = "twitter_media"),
            entry("two", "post_media", ownerId = "post-1", bucket = "twitter_media"),
            entry("other", "post_thumbnail", ownerId = "post-2", bucket = "twitter_media"),
        ))

        assertEquals(listOf("one", "two"), dao.forOwner("post-1").map { it.assetId }.sorted())
    }

    @Test fun forOwnerFlow_updatesWhenRowsChange() = runBlocking {
        val dao = db.mediaInventoryDao()

        assertEquals(emptyList<MediaInventoryEntity>(), dao.forOwnerFlow("video-1").first())

        dao.upsert(entry("stream", "video_stream", ownerId = "video-1", bucket = "shorts_videos"))

        val rows = dao.forOwnerFlow("video-1").first()
        assertEquals(listOf("stream"), rows.map { it.assetId })
    }

    @Test fun forOwnerAndKindFlow_returnsMatchingKind() = runBlocking {
        val dao = db.mediaInventoryDao()
        dao.upsert(listOf(
            entry("thumb", "post_thumbnail", ownerId = "post-1", bucket = "twitter_media"),
            entry("media", "post_media", ownerId = "post-1", bucket = "twitter_media"),
        ))

        assertEquals("media", dao.forOwnerAndKindFlow("post-1", "post_media").first()?.assetId)
    }

    @Test fun resolveForOwner_prefersCachedOverPending() = runBlocking {
        val dao = db.mediaInventoryDao()
        dao.upsert(entry("pending", "post_thumbnail", ownerId = "post-1", bucket = "twitter_media"))
        dao.upsert(entry("cached", "post_thumbnail", ownerId = "post-1", bucket = "twitter_media", state = "cached"))

        val resolved = dao.resolveForOwner("post-1", "post_thumbnail")

        assertEquals("cached", resolved?.assetId)
        assertEquals("cached", resolved?.state)
    }

    @Test fun statsByBucket_aggregatesLegacyRows() = runBlocking {
        val dao = db.mediaInventoryDao()
        dao.upsert(listOf(
            entry("avatar-1", "avatar", bucket = "avatars", state = "cached", fileSize = 1000),
            entry("avatar-2", "avatar", bucket = "avatars"),
            entry("video-1", "video_stream", bucket = "shorts_videos", state = "failed"),
        ))

        val stats = dao.statsByBucket().associateBy { it.bucket }

        assertEquals(2, stats["avatars"]!!.entries)
        assertEquals(1, stats["avatars"]!!.cached)
        assertEquals(1000L, stats["avatars"]!!.bytes)
        assertEquals(1, stats["shorts_videos"]!!.failed)
    }

    @Test fun deleteBucket_scopedDelete() = runBlocking {
        val dao = db.mediaInventoryDao()
        dao.upsert(listOf(
            entry("avatar-1", "avatar", bucket = "avatars"),
            entry("stream-1", "video_stream", bucket = "shorts_videos"),
        ))

        assertEquals(1, dao.deleteBucket("avatars"))

        val stats = dao.statsByBucket().associateBy { it.bucket }
        assertNull(stats["avatars"])
        assertEquals(1, stats["shorts_videos"]?.entries)
    }

    @Test fun deleteForOwner_removesOnlyThatOwner() = runBlocking {
        val dao = db.mediaInventoryDao()
        dao.upsert(listOf(
            entry("one", "post_thumbnail", ownerId = "post-1", bucket = "twitter_media"),
            entry("two", "post_media", ownerId = "post-1", bucket = "twitter_media"),
            entry("other", "post_media", ownerId = "post-2", bucket = "twitter_media"),
        ))

        assertEquals(2, dao.deleteForOwner("post-1"))

        assertEquals(emptyList<MediaInventoryEntity>(), dao.forOwner("post-1"))
        assertEquals("other", dao.forOwner("post-2").single().assetId)
    }

    @Test fun deleteAll_clearsRows() = runBlocking {
        val dao = db.mediaInventoryDao()
        dao.upsert(listOf(
            entry("avatar-1", "avatar", bucket = "avatars"),
            entry("stream-1", "video_stream", bucket = "shorts_videos"),
        ))

        dao.deleteAll()

        assertEquals(0, dao.statsByBucket().sumOf { it.entries })
    }
}
