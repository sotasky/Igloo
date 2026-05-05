package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class RetentionReplayCoordinatorTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var logger: Logger

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        logger = Logger(prefs = prefs, sink = InMemoryLogSink(), scope = scope, nowMsProvider = { 0L })
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test fun wideningFeedRetention_resetsFeedOnly_andTriggersReplay() = runBlocking {
        db.cursorDao().upsert("feed", "101", 0L)
        db.cursorDao().upsert("shorts", "303", 0L)

        val trigger = RecordingReplayTrigger()
        val syncTrigger = RecordingSyncTrigger()
        val coordinator = RetentionReplayCoordinator(
            scope = scope,
            prefs = prefs,
            cursorDao = db.cursorDao(),
            replayTrigger = trigger,
            syncTrigger = syncTrigger::trigger,
            logger = logger,
        )
        coordinator.start()
        delay(200)

        prefs.setRetentionDaysFeed(14)

        waitFor {
            db.cursorDao().get("feed") == null && trigger.count == 1 && syncTrigger.count == 1
        }

        assertNull(db.cursorDao().get("feed"))
        assertNotNull("unrelated streams must survive", db.cursorDao().get("shorts"))
    }

    @Test fun narrowingRetention_doesNotResetCursors_orTriggerReplay_butRefreshesSyncPrune() = runBlocking {
        prefs.setRetentionDaysYoutube(30)
        db.cursorDao().upsert("youtube_videos", "101", 0L)

        val trigger = RecordingReplayTrigger()
        val syncTrigger = RecordingSyncTrigger()
        val coordinator = RetentionReplayCoordinator(
            scope = scope,
            prefs = prefs,
            cursorDao = db.cursorDao(),
            replayTrigger = trigger,
            syncTrigger = syncTrigger::trigger,
            logger = logger,
        )
        coordinator.start()
        delay(200)

        prefs.setRetentionDaysYoutube(7)
        delay(200)

        assertEquals(0, trigger.count)
        assertEquals(1, syncTrigger.count)
        assertNotNull(db.cursorDao().get("youtube_videos"))
    }

    private suspend fun waitFor(predicate: suspend () -> Boolean) {
        withTimeout(5_000L) {
            while (!predicate()) delay(10)
        }
    }

    private class RecordingReplayTrigger : SyncReplayTrigger {
        @Volatile var count: Int = 0
        override fun triggerReplay() {
            count++
        }
    }

    private class RecordingSyncTrigger {
        @Volatile var count: Int = 0
        fun trigger() {
            count++
        }
    }
}
