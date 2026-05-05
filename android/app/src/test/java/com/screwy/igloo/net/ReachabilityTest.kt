package com.screwy.igloo.net

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/**
 * Reachability state-machine behavior — foreground transitions drive probes, downgrade()
 * flips to Offline immediately, offline-loop retries until a probe succeeds.
 */
class ReachabilityTest {

    private lateinit var scope: CoroutineScope

    @Before fun setUp() {
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After fun tearDown() {
        scope.cancel()
    }

    private suspend fun waitFor(timeoutMs: Long = 3_000L, predicate: () -> Boolean) {
        withTimeout(timeoutMs) { while (!predicate()) delay(10) }
    }

    @Test fun successfulProbeOnForeground_setsOnline() = runBlocking {
        val foreground = MutableSharedFlow<Boolean>(replay = 1, extraBufferCapacity = 4)
        val reachability = Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = foreground,
        )
        reachability.start()
        foreground.emit(true)

        waitFor { reachability.state.value is Reachability.State.Online }
        assertEquals(Reachability.State.Online, reachability.state.value)
    }

    @Test fun failedProbeOnForeground_setsOffline() = runBlocking {
        val foreground = MutableSharedFlow<Boolean>(replay = 1, extraBufferCapacity = 4)
        val reachability = Reachability(
            scope = scope,
            probe = { false },
            foregroundFlow = foreground,
        )
        reachability.start()
        foreground.emit(true)

        waitFor { reachability.state.value is Reachability.State.Offline }
        assertEquals(Reachability.State.Offline, reachability.state.value)
    }

    @Test fun downgradeFromOnline_flipsToOffline() = runBlocking {
        val foreground = MutableSharedFlow<Boolean>(replay = 1, extraBufferCapacity = 4)
        val reachability = Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = foreground,
        )
        reachability.start()
        foreground.emit(true)
        waitFor { reachability.state.value is Reachability.State.Online }

        reachability.downgrade()
        assertEquals(Reachability.State.Offline, reachability.state.value)
    }

    @Test fun offlineProbeLoop_recoversWhenProbeSucceeds() = runBlocking {
        val foreground = MutableSharedFlow<Boolean>(replay = 1, extraBufferCapacity = 4)
        val succeedAfter = AtomicInteger(3) // first 3 probes fail, then succeed
        val probeAttempts = AtomicInteger(0)
        val reachability = Reachability(
            scope = scope,
            probe = {
                probeAttempts.incrementAndGet()
                succeedAfter.decrementAndGet() <= 0
            },
            foregroundFlow = foreground,
            offlineProbeIntervalMs = 40L,
        )
        reachability.start()
        foreground.emit(true)

        waitFor(timeoutMs = 5_000L) { reachability.state.value is Reachability.State.Online }
        assertTrue("expected multiple probe attempts, got ${probeAttempts.get()}", probeAttempts.get() >= 3)
    }

    @Test fun backgroundTransition_doesNotBlockShutdown() = runBlocking {
        val foreground = MutableSharedFlow<Boolean>(replay = 1, extraBufferCapacity = 4)
        val probeRan = AtomicBoolean(false)
        val reachability = Reachability(
            scope = scope,
            probe = { probeRan.set(true); true },
            foregroundFlow = foreground,
            offlineProbeIntervalMs = 30L,
        )
        reachability.start()

        foreground.emit(true)
        waitFor { probeRan.get() }

        // Background flip — doesn't crash, state retained.
        foreground.emit(false)
        delay(100)
        // Either retained Online, or still unchanged.
        assertTrue(reachability.state.value !is Reachability.State.Unknown)
    }
}
