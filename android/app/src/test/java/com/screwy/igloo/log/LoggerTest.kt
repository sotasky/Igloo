package com.screwy.igloo.log

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Logger behavior — `debug` short-circuits when `prefs.debug_mode` is false, both
 * `info` and `error` always fire, timestamps reflect the server-time offset, and
 * `error` includes `error` + `stack` fields when a Throwable is supplied.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class LoggerTest {

    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var sink: InMemoryLogSink
    private lateinit var logger: Logger
    private var now: Long = 1_000L

    @Before fun setUp() {
        val db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { now })
        sink = InMemoryLogSink()
        logger = Logger(prefs = prefs, sink = sink, scope = scope, nowMsProvider = { now })
    }

    @After fun tearDown() {
        scope.cancel()
    }

    private suspend fun waitForSize(expected: Int, timeoutMs: Long = 3_000L) {
        withTimeout(timeoutMs) { while (sink.size() != expected) delay(10) }
    }

    @Test fun info_enqueuesEntry() = runBlocking {
        logger.info("app_start", mapOf("v" to "1.0"))
        waitForSize(1)
        val entry = sink.snapshot().single()
        assertEquals(LogLevel.Info, entry.level)
        assertEquals("app_start", entry.event)
        assertEquals("1.0", entry.fields["v"])
    }

    @Test fun error_includesStackTrace() = runBlocking {
        val cause = IllegalStateException("boom")
        logger.error("sync_failed", mapOf("stream" to "feed"), throwable = cause)
        waitForSize(1)
        val entry = sink.snapshot().single()
        assertEquals(LogLevel.Error, entry.level)
        assertEquals("feed", entry.fields["stream"])
        val errField = entry.fields["error"] as? String
        val stackField = entry.fields["stack"] as? String
        assertTrue(errField?.contains("boom") == true)
        assertTrue(stackField?.contains("IllegalStateException") == true)
    }

    @Test fun debug_shortCircuitsWhenPrefOff() = runBlocking {
        // Default debug_mode is false.
        logger.debug("render_feed", mapOf("count" to "40"))
        delay(150)
        assertEquals(0, sink.size())
    }

    @Test fun debug_firesWhenPrefOn() = runBlocking {
        prefs.setDebugMode(true)
        // Wait for the sync cache to reflect the flip.
        withTimeout(3_000L) { while (!prefs.debugModeSync()) delay(10) }

        logger.debug("render_feed", mapOf("count" to "40"))
        waitForSize(1)
        val entry = sink.snapshot().single()
        assertEquals(LogLevel.Debug, entry.level)
    }

    @Test fun timestamps_useServerTimeOffset() = runBlocking {
        now = 500L
        prefs.setServerTimeOffsetMs(2500L)
        withTimeout(3_000L) { while (prefs.serverTimeOffsetMsSync() != 2500L) delay(10) }

        logger.info("tick")
        waitForSize(1)
        val entry = sink.snapshot().single()
        assertEquals(500L + 2500L, entry.timestampMs)
    }

    @Test fun debugEnabled_reflectsPref() = runBlocking {
        assertTrue(!logger.debugEnabled())
        prefs.setDebugMode(true)
        withTimeout(3_000L) { while (!logger.debugEnabled()) delay(10) }
        assertTrue(logger.debugEnabled())
    }
}
