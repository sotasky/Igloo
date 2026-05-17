package com.screwy.igloo.sync

import com.screwy.igloo.net.Reachability
import com.screwy.igloo.outbox.OutboxDrainRunner
import com.screwy.igloo.outbox.OutboxDrainSignal
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class SchedulerTest {
    @Test
    fun triggerAllStartsSyncWhenOutboxCompletesBeforeInboundFinishes() = runTest {
        val foreground = MutableSharedFlow<Boolean>(extraBufferCapacity = 1)
        val reachability = Reachability(
            scope = this,
            probe = { true },
            foregroundFlow = foreground,
        )
        reachability.markOnline()

        val inbound = FakeInbound()
        val outbox = FakeOutboxDrain()
        val retentionReplay = FakeRetentionReplay()
        val androidSync = FakeAndroidSync()
        val mutationDelta = FakeMutationDelta()
        val scheduler = Scheduler(
            scope = this,
            inbound = inbound,
            outbox = outbox,
            androidSync = androidSync,
            retentionReplay = retentionReplay,
            reachability = reachability,
            foregroundFlow = foreground,
            writer = FakeOutboxSignal(),
            mutationDelta = mutationDelta,
            logger = FakeSchedulerLogger(),
        )

        scheduler.start()
        runCurrent()
        androidSync.clear()
        scheduler.triggerAll()
        outbox.passCompleted.emit(Unit)
        runCurrent()

        assertEquals(2, androidSync.triggerCount)
        assertEquals(1, inbound.triggerCount)
        assertEquals(listOf("android", "android", "inbound"), androidSync.events + inbound.events)

        scheduler.stopAll()
    }

    @Test
    fun triggerStreamMergesScopedRequestsAfterOutboxCompletion() = runTest {
        val foreground = MutableSharedFlow<Boolean>(extraBufferCapacity = 1)
        val reachability = Reachability(
            scope = this,
            probe = { true },
            foregroundFlow = foreground,
        )
        reachability.markOnline()

        val inbound = FakeInbound()
        val outbox = FakeOutboxDrain()
        val androidSync = FakeAndroidSync()
        val mutationDelta = FakeMutationDelta()
        val scheduler = Scheduler(
            scope = this,
            inbound = inbound,
            outbox = outbox,
            androidSync = androidSync,
            retentionReplay = FakeRetentionReplay(),
            reachability = reachability,
            foregroundFlow = foreground,
            writer = FakeOutboxSignal(),
            mutationDelta = mutationDelta,
            logger = FakeSchedulerLogger(),
        )

        scheduler.start()
        runCurrent()
        androidSync.clear()
        inbound.clear()
        mutationDelta.clear()
        scheduler.triggerStream(SyncStream.Feed)
        scheduler.triggerStream(SyncStream.Channels)
        outbox.passCompleted.emit(Unit)
        runCurrent()

        assertEquals(1, androidSync.triggerCount)
        assertEquals(listOf(setOf(SyncStream.Feed, SyncStream.Channels)), inbound.triggerStreamsCalls)
        assertEquals(1, mutationDelta.syncCount)

        scheduler.stopAll()
    }

    @Test
    fun foregroundMutationDeltaRankChangeTriggersAndroidSyncRefresh() = runTest {
        val foreground = MutableSharedFlow<Boolean>(extraBufferCapacity = 1)
        val reachability = Reachability(
            scope = this,
            probe = { true },
            foregroundFlow = foreground,
        )
        reachability.markOnline()

        val androidSync = FakeAndroidSync()
        val mutationDelta = FakeMutationDelta(result = MutationDeltaResult(rankAffecting = true))
        val scheduler = Scheduler(
            scope = this,
            inbound = FakeInbound(),
            outbox = FakeOutboxDrain(),
            androidSync = androidSync,
            retentionReplay = FakeRetentionReplay(),
            reachability = reachability,
            foregroundFlow = foreground,
            writer = FakeOutboxSignal(),
            mutationDelta = mutationDelta,
            logger = FakeSchedulerLogger(),
        )

        scheduler.start()
        runCurrent()
        androidSync.clear()
        mutationDelta.clear()

        foreground.emit(true)
        runCurrent()

        assertEquals(1, mutationDelta.syncCount)
        assertEquals(2, androidSync.triggerCount)

        scheduler.stopAll()
    }

    private class FakeInbound : InboundSyncRunner {
        var triggerCount = 0
            private set
        val triggerStreamsCalls = mutableListOf<Set<SyncStream>>()
        val events = mutableListOf<String>()

        override fun trigger() {
            triggerCount += 1
            events += "inbound"
        }

        override fun triggerStreams(streams: Set<SyncStream>) {
            triggerStreamsCalls += streams
        }

        override suspend fun run() {
            awaitCancellation()
        }

        fun clear() {
            triggerCount = 0
            triggerStreamsCalls.clear()
            events.clear()
        }
    }

    private class FakeAndroidSync : AndroidSyncRunner {
        var triggerCount = 0
            private set
        val events = mutableListOf<String>()

        override fun trigger() {
            triggerCount += 1
            events += "android"
        }

        override suspend fun run() {
            awaitCancellation()
        }

        fun clear() {
            triggerCount = 0
            events.clear()
        }
    }

    private class FakeOutboxDrain : OutboxDrainRunner {
        override val passCompleted = MutableSharedFlow<Unit>(extraBufferCapacity = 1)

        override fun wireWriter(writer: OutboxDrainSignal) = Unit

        override fun trigger() = Unit

        override suspend fun run() {
            awaitCancellation()
        }
    }

    private class FakeOutboxSignal : OutboxDrainSignal {
        override val drainSignal: SharedFlow<Unit> = MutableSharedFlow(extraBufferCapacity = 1)
    }

    private class FakeRetentionReplay : RetentionReplayRunner {
        override fun start() = Unit
        override fun stop() = Unit
    }

    private class FakeMutationDelta(
        private val result: MutationDeltaResult = MutationDeltaResult(),
    ) : MutationDeltaRunner {
        var syncCount = 0
            private set

        override suspend fun sync(): MutationDeltaResult {
            syncCount += 1
            return result
        }

        fun clear() {
            syncCount = 0
        }
    }

    private class FakeSchedulerLogger : SchedulerLogger {
        override fun info(event: String, fields: Map<String, Any?>) = Unit
    }
}
