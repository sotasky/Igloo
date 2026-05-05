package com.screwy.igloo.outbox

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.BookmarkEntity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.firstOrNull
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * OutboxWriter — verifies the enqueue transaction: server-time-corrected timestamp,
 * coalesce delete, insert, side-table apply, drain signal emit. 03-outbox.md §4.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class OutboxWriterTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 1_000L })
        writer = OutboxWriter(
            db = db,
            prefs = prefs,
            scope = scope,
            nowMsProvider = { 1_000L },
            // Shrink debounce so tests don't wait 3s for the drain signal to emit.
            writeDebounceMs = 50L,
        )
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    // ─── Side-table apply ─────────────────────────────────────────────────────

    @Test fun enqueue_like_set_writes_feed_like_and_seen() = runBlocking {
        writer.enqueue(OutboxKind.Like(tweetId = "t1", action = OutboxKind.Action.Set))
        assertTrue(db.feedLikeDao().exists("t1"))
        assertTrue(db.feedSeenDao().exists("t1"))
        assertEquals(1, db.outboxDao().countByState("pending"))
    }

    @Test fun enqueue_like_clear_deletes_feed_like() = runBlocking {
        writer.enqueue(OutboxKind.Like("t1", OutboxKind.Action.Set))
        writer.enqueue(OutboxKind.Like("t1", OutboxKind.Action.Clear))
        assertFalse(db.feedLikeDao().exists("t1"))
    }

    @Test fun enqueue_bookmark_set_writes_bookmark_row() = runBlocking {
        writer.enqueue(OutboxKind.Bookmark(videoId = "v1", action = OutboxKind.Action.Set, categoryId = 3))
        val row = db.bookmarkDao().getById("v1")
        assertNotNull(row)
        assertEquals(3L, row!!.categoryId)
    }

    @Test fun enqueue_channelSetting_upserts_row() = runBlocking {
        writer.enqueue(
            OutboxKind.ChannelSetting(
                channelId = "c1",
                settingField = "media_only",
                value = 1L,
                prevValue = null,
                prevExisted = false,
            ),
        )
        val row = db.channelSettingDao().getById("c1")
        assertEquals(1, row!!.mediaOnly)
    }

    @Test fun enqueue_momentsCursor_writes_prefs() = runBlocking {
        writer.enqueue(OutboxKind.MomentsCursor(videoId = "v1", positionMs = 4200L))
        assertEquals("v1", prefs.momentsResumeVideoId(scope = "all").first())
        assertEquals(4200L, prefs.momentsResumePositionMs(scope = "all").first())
    }

    // ─── Coalesce ─────────────────────────────────────────────────────────────

    @Test fun rapidFireToggles_collapseToOneRow() = runBlocking {
        writer.enqueue(OutboxKind.Like("t1", OutboxKind.Action.Set))
        writer.enqueue(OutboxKind.Like("t1", OutboxKind.Action.Clear))
        writer.enqueue(OutboxKind.Like("t1", OutboxKind.Action.Set))
        assertEquals(1, db.outboxDao().countByState("pending"))
    }

    @Test fun channelSettingDifferentFields_stayDistinct() = runBlocking {
        writer.enqueue(OutboxKind.ChannelSetting("c1", "media_only", 1, null, false))
        writer.enqueue(OutboxKind.ChannelSetting("c1", "max_videos", 50, null, false))
        assertEquals(2, db.outboxDao().countByState("pending"))
    }

    @Test fun seen_isFifo_noCoalesce() = runBlocking {
        writer.enqueue(OutboxKind.Seen("t1"))
        writer.enqueue(OutboxKind.Seen("t1"))
        writer.enqueue(OutboxKind.Seen("t2"))
        assertEquals(3, db.outboxDao().countByState("pending"))
    }

    // ─── Pre-image capture ─────────────────────────────────────────────────────

    @Test fun captureBookmark_previousRow() = runBlocking {
        db.bookmarkDao().upsert(
            BookmarkEntity(videoId = "v1", categoryId = 7, customTitle = "fav", bookmarkedAt = 100),
        )
        val pre = writer.capturePreviousBookmark("v1")
        assertTrue(pre.existed)
        assertEquals(7L, pre.categoryId)
        assertEquals("fav", pre.customTitle)
    }

    @Test fun captureBookmark_emptyWhenMissing() = runBlocking {
        val pre = writer.capturePreviousBookmark("v_missing")
        assertFalse(pre.existed)
    }

    // ─── Server-time-corrected timestamps ─────────────────────────────────────

    @Test fun enqueue_usesServerTimeOffsetForUpdatedAt() = runBlocking {
        prefs.setServerTimeOffsetMs(500L)
        // Wait for the PreferencesRepo init collector to pick up the write.
        withTimeoutOrNull(1000L) {
            while (prefs.serverTimeOffsetMsSync() != 500L) delay(10)
        }
        writer.enqueue(OutboxKind.Like("t1", OutboxKind.Action.Set))
        val row = db.outboxDao().claimPending(nowMs = 10_000L).single()
        val payload = Json.parseToJsonElement(row.payloadJson).jsonObject
        assertEquals(1_500L, payload["updated_at_ms"]!!.jsonPrimitive.content.toLong())
        assertEquals(1_500L, row.createdAtMs)
    }

    // ─── Drain signal ─────────────────────────────────────────────────────────

    @Test fun enqueue_emitsDebouncedDrainSignal() = runBlocking {
        val drainJob = scope.async { withTimeoutOrNull(1_500L) { writer.drainSignal.first() } }
        // Give the collector a tick to subscribe before we enqueue.
        delay(50)
        writer.enqueue(OutboxKind.Like("t1", OutboxKind.Action.Set))
        assertNotNull("drain signal not emitted within timeout", drainJob.await())
    }

    @Test fun signalDrainNow_bypassesDebounce() = runBlocking {
        val drainJob = scope.async { withTimeoutOrNull(500L) { writer.drainSignal.first() } }
        delay(30)
        writer.signalDrainNow()
        assertNotNull(drainJob.await())
    }
}
