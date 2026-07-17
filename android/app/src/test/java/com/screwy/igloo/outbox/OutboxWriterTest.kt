package com.screwy.igloo.outbox

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.MutedChannelEntity
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class OutboxWriterTest {
    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter
    private lateinit var drainRequests: AtomicInteger

    @Before
    fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 1_000L })
        drainRequests = AtomicInteger()
        writer =
            OutboxWriter(
                db = db,
                prefs = prefs,
                scope = scope,
                onDrainRequested = { drainRequests.incrementAndGet() },
                nowMsProvider = { 1_000L },
                writeDebounceMs = 1L,
            )
    }

    @After
    fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test
    fun setIsOptimisticAndCoalescesToLatestAction() = runBlocking {
        writer.enqueue(OutboxKind.Like("item-1", OutboxKind.Action.Set))
        writer.enqueue(OutboxKind.Like("item-1", OutboxKind.Action.Clear))
        writer.enqueue(OutboxKind.Like("item-1", OutboxKind.Action.Set))

        assertTrue(db.feedLikeDao().exists("item-1"))
        assertTrue(db.feedSeenDao().exists("item-1"))
        assertEquals(1, db.outboxDao().countByState("pending"))
        val payload =
            Json.parseToJsonElement(db.outboxDao().pendingRows().single().payloadJson).jsonObject
        assertEquals("set", payload.getValue("action").jsonPrimitive.content)
    }

    @Test
    fun clearDoesNotDeleteCanonicalStateBeforeAck() = runBlocking {
        db.feedLikeDao().upsert(FeedLikeEntity("item-1", 1L))

        writer.enqueue(OutboxKind.Like("item-1", OutboxKind.Action.Clear))

        assertTrue(db.feedLikeDao().exists("item-1"))
    }

    @Test
    fun seenAndMomentViewsCoalesceByItem() = runBlocking {
        writer.enqueue(OutboxKind.Seen("item-1"))
        writer.enqueue(OutboxKind.Seen("item-1"))
        writer.enqueue(OutboxKind.MomentView("video-1"))
        writer.enqueue(OutboxKind.MomentView("video-1"))

        assertEquals(2, db.outboxDao().countByState("pending"))
        assertTrue(db.feedSeenDao().exists("item-1"))
        assertTrue(db.momentViewDao().exists("video-1"))
    }

    @Test
    fun channelMuteUsesServerChannelId() = runBlocking {
        writer.enqueue(OutboxKind.Mute("channel-1", OutboxKind.Action.Set))

        assertTrue(db.mutedChannelDao().exists("channel-1"))
        val payload =
            Json.parseToJsonElement(db.outboxDao().pendingRows().single().payloadJson).jsonObject
        assertEquals("channel-1", payload.getValue("channel_id").jsonPrimitive.content)
    }

    @Test
    fun clearMuteRemovesOptimisticLocalMuteImmediately() = runBlocking {
        db.mutedChannelDao().upsert(MutedChannelEntity("channel-1", 1L))

        writer.enqueue(OutboxKind.Mute("channel-1", OutboxKind.Action.Clear))

        assertFalse(db.mutedChannelDao().exists("channel-1"))
        val payload =
            Json.parseToJsonElement(db.outboxDao().pendingRows().single().payloadJson).jsonObject
        assertEquals("clear", payload.getValue("action").jsonPrimitive.content)
    }

    @Test
    fun includeRepostsIsOptimisticForAnyPlatformChannelId() = runBlocking {
        writer.enqueue(OutboxKind.ChannelSetting("tiktok_channel", "include_reposts", 0))
        writer.enqueue(OutboxKind.ChannelSetting("instagram_channel", "include_reposts", 0))

        assertEquals(0, db.channelSettingDao().getById("tiktok_channel")?.includeReposts)
        assertEquals(0, db.channelSettingDao().getById("instagram_channel")?.includeReposts)
    }

    @Test
    fun categoryCreatePersistsItsRequestId() = runBlocking {
        writer.enqueue(
            OutboxKind.CreateCategory(
                name = "Sample",
                provisionalId = -1L,
                requestId = "04e8af73-20b8-48f6-9d30-ca3b15349f83",
            )
        )

        val payload =
            Json.parseToJsonElement(db.outboxDao().pendingRows().single().payloadJson).jsonObject
        assertEquals(
            "04e8af73-20b8-48f6-9d30-ca3b15349f83",
            payload.getValue("request_id").jsonPrimitive.content,
        )
    }

    @Test
    fun mutationRequestsDrainButPersistedLogsDoNotSelfTrigger() = runBlocking {
        writer.enqueue(
            OutboxKind.Log(
                level = "info",
                event = "sample_event",
                fields = emptyMap(),
                timestampMs = 1_000L,
            )
        )
        delay(20L)
        assertEquals(0, drainRequests.get())

        writer.enqueue(OutboxKind.Seen("item-1"))
        delay(20L)
        assertEquals(1, drainRequests.get())
    }

    @Test
    fun serverTimeOffsetOwnsMutationTimestamp() = runBlocking {
        prefs.setServerTimeOffsetMs(500L)
        repeat(100) {
            if (prefs.serverTimeOffsetMsSync() == 500L) return@repeat
            delay(5L)
        }

        writer.enqueue(OutboxKind.Like("item-1", OutboxKind.Action.Set))

        val row = db.outboxDao().pendingRows().single()
        val payload = Json.parseToJsonElement(row.payloadJson).jsonObject
        assertEquals(1_500L, row.createdAtMs)
        assertEquals(1_500L, payload.getValue("updated_at_ms").jsonPrimitive.content.toLong())
    }

    @Test
    fun differentChannelSettingFieldsRemainIndependent() = runBlocking {
        writer.enqueue(OutboxKind.ChannelSetting("channel-1", "media_only", 1))
        writer.enqueue(OutboxKind.ChannelSetting("channel-1", "max_videos", 50))

        assertEquals(2, db.outboxDao().countByState("pending"))
        assertEquals(1, db.channelSettingDao().getById("channel-1")?.mediaOnly)
        assertEquals(50, db.channelSettingDao().getById("channel-1")?.maxVideos)
    }
}
