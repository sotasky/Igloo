package com.screwy.igloo.log

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.sync.SchedulerLogger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

/**
 * Outbox-backed emit API.
 *
 *  - `info` / `error` → `OutboxKind.Log(level=info|error, ...)` — always captured;
 *     stream=server.log on the wire.
 *  - `debug` → `OutboxKind.LogDebug(...)` — gated by `preferences.debug_mode`. Hot-path
 *     short-circuits before allocating the payload when debug is off; stream=debug.log.
 *
 * Timestamps are server-time-corrected (`deviceNow + serverTimeOffsetMsSync`) so LWW
 * + cross-device comparisons run in the server's logical time domain.
 *
 * Kept loosely coupled from the outbox so tests and pre-login state can provide
 * lightweight sinks without changing the logger API.
 */
class Logger(
    private val prefs: PreferencesRepo,
    private val sink: LogSink,
    private val scope: CoroutineScope,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) : SchedulerLogger {

    fun info(event: String) {
        info(event, emptyMap())
    }

    override fun info(event: String, fields: Map<String, Any?>) {
        val entry = LogEntry(
            level = LogLevel.Info,
            event = event,
            fields = fields,
            timestampMs = nowMsProvider() + prefs.serverTimeOffsetMsSync(),
        )
        dispatch(entry)
    }

    fun error(event: String, fields: Map<String, Any?> = emptyMap(), throwable: Throwable? = null) {
        val merged = if (throwable != null) {
            fields + mapOf(
                "error" to throwable.toString(),
                "stack" to throwable.stackTraceToString(),
            )
        } else fields
        val entry = LogEntry(
            level = LogLevel.Error,
            event = event,
            fields = merged,
            timestampMs = nowMsProvider() + prefs.serverTimeOffsetMsSync(),
        )
        dispatch(entry)
    }

    /**
     * Hot-path gated. Must short-circuit BEFORE constructing the payload or allocating
     * the fields map.
     *
     * Callers doing expensive debug string construction should still guard with their
     * own `if (Logger.debugEnabled()) { ... }` — we expose the cached read below.
     */
    fun debug(event: String, fields: Map<String, Any?> = emptyMap()) {
        if (!prefs.debugModeSync()) return
        val entry = LogEntry(
            level = LogLevel.Debug,
            event = event,
            fields = fields,
            timestampMs = nowMsProvider() + prefs.serverTimeOffsetMsSync(),
        )
        dispatch(entry)
    }

    /** Caller-side hot-path gate for deferring expensive payload construction. */
    fun debugEnabled(): Boolean = prefs.debugModeSync()

    private fun dispatch(entry: LogEntry) {
        // Mirror every emit to Android logcat so developers still see something even
        // if the outbox-backed sink fails (pre-login DB state, disk pressure, etc.).
        val tag = "Igloo/${entry.level.name}"
        val line = "${entry.event} ${entry.fields}"
        when (entry.level) {
            LogLevel.Error -> android.util.Log.e(tag, line)
            LogLevel.Info -> android.util.Log.i(tag, line)
            LogLevel.Debug -> android.util.Log.d(tag, line)
        }
        // Fire-and-forget: log emit must never block the caller. `LogSink` impls run on
        // I/O dispatcher when they hit Room. Sink failures are surfaced via logcat so a
        // silent outbox write never hides all telemetry.
        scope.launch(Dispatchers.IO) {
            try {
                sink.accept(entry)
            } catch (t: Throwable) {
                android.util.Log.w("Igloo/LogSink", "sink failed for ${entry.event}: $t")
            }
        }
    }
}
