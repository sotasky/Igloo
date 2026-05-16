package com.screwy.igloo.perf

import android.os.Build
import android.os.SystemClock
import android.os.Trace
import android.util.Log
import java.util.Locale
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

internal object PerfProbe {
    private const val TAG = "IglooPerf"
    private const val MAX_SECTION_NAME = 120
    private const val ROOM_QUERY_SUMMARY_INTERVAL = 250
    private const val ROOM_INVALIDATION_SUMMARY_INTERVAL = 25
    private val collectorCounts = ConcurrentHashMap<String, AtomicInteger>()
    private val counters = ConcurrentHashMap<String, AtomicInteger>()
    private val roomQueryBuckets = ConcurrentHashMap<String, AtomicInteger>()
    private val roomInvalidationBuckets = ConcurrentHashMap<String, AtomicInteger>()
    private val roomQueryCount = AtomicInteger(0)
    private val roomInvalidationCount = AtomicInteger(0)
    private val asyncCookies = AtomicInteger(1)
    private val syncTraceDepth: ThreadLocal<Int> = ThreadLocal.withInitial { 0 }

    fun enabled(): Boolean = Log.isLoggable(TAG, Log.DEBUG)

    fun logsEnabled(): Boolean = enabled()

    fun begin(section: String) {
        if (!enabled()) return
        Trace.beginSection(sectionName(section))
        syncTraceDepth.set((syncTraceDepth.get() ?: 0) + 1)
    }

    fun end() {
        val depth = syncTraceDepth.get() ?: 0
        if (depth <= 0) return
        Trace.endSection()
        syncTraceDepth.set(depth - 1)
    }

    fun beginAsync(section: String): Int {
        if (!enabled()) return 0
        val cookie = asyncCookies.getAndIncrement()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            Trace.beginAsyncSection(sectionName(section), cookie)
        }
        return cookie
    }

    fun endAsync(section: String, cookie: Int) {
        if (cookie == 0) return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            Trace.endAsyncSection(sectionName(section), cookie)
        }
    }

    inline fun <T> trace(section: String, block: () -> T): T {
        begin(section)
        return try {
            block()
        } finally {
            end()
        }
    }

    inline fun <T> timed(
        event: String,
        fields: Map<String, Any?> = emptyMap(),
        block: () -> T,
    ): T {
        if (!enabled()) return block()
        val started = SystemClock.elapsedRealtimeNanos()
        val cookie = beginAsync(event)
        return try {
            block()
        } finally {
            endAsync(event, cookie)
            log(event, fields + ("duration_ms" to elapsedMsSince(started)))
        }
    }

    inline fun <T> timed(
        event: String,
        crossinline fields: () -> Map<String, Any?>,
        block: () -> T,
    ): T {
        if (!enabled()) return block()
        val started = SystemClock.elapsedRealtimeNanos()
        val cookie = beginAsync(event)
        return try {
            block()
        } finally {
            endAsync(event, cookie)
            log(event) { fields() + ("duration_ms" to elapsedMsSince(started)) }
        }
    }

    suspend inline fun <T> timedSuspend(
        event: String,
        fields: Map<String, Any?> = emptyMap(),
        crossinline block: suspend () -> T,
    ): T {
        if (!enabled()) return block()
        val started = SystemClock.elapsedRealtimeNanos()
        val cookie = beginAsync(event)
        return try {
            block()
        } finally {
            endAsync(event, cookie)
            log(event, fields + ("duration_ms" to elapsedMsSince(started)))
        }
    }

    suspend inline fun <T> timedSuspend(
        event: String,
        crossinline fields: () -> Map<String, Any?>,
        crossinline block: suspend () -> T,
    ): T {
        if (!enabled()) return block()
        val started = SystemClock.elapsedRealtimeNanos()
        val cookie = beginAsync(event)
        return try {
            block()
        } finally {
            endAsync(event, cookie)
            log(event) { fields() + ("duration_ms" to elapsedMsSince(started)) }
        }
    }

    fun log(event: String, fields: Map<String, Any?> = emptyMap()) {
        if (!enabled()) return
        Log.d(TAG, formatLine(event, fields))
    }

    inline fun log(event: String, crossinline fields: () -> Map<String, Any?>) {
        if (!enabled()) return
        Log.d(TAG, formatLine(event, fields()))
    }

    fun incrementCounter(name: String, delta: Int = 1): Int {
        if (!enabled()) return 0
        val value = counters.getOrPut(name) { AtomicInteger(0) }.addAndGet(delta)
        setCounter(name, value)
        return value
    }

    fun setCounter(name: String, value: Int) {
        if (!enabled()) return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            Trace.setCounter(sectionName(name), value.toLong())
        }
    }

    fun collectorStart(kind: String, fields: Map<String, Any?> = emptyMap()): String {
        if (!enabled()) return ""
        val key = fields.entries
            .joinToString(separator = "|", prefix = kind) { (field, value) -> "$field=$value" }
        val active = collectorCounts.getOrPut(key) { AtomicInteger(0) }.incrementAndGet()
        setCounter("${counterName(kind)}_active", active)
        log("${kind}_collector_start", fields + ("active" to active))
        return key
    }

    inline fun collectorStart(kind: String, crossinline fields: () -> Map<String, Any?>): String {
        if (!enabled()) return ""
        return collectorStart(kind, fields())
    }

    fun collectorEnd(kind: String, key: String, fields: Map<String, Any?> = emptyMap()) {
        if (key.isEmpty() || !enabled()) return
        val active = collectorCounts[key]?.decrementAndGet()?.coerceAtLeast(0) ?: 0
        setCounter("${counterName(kind)}_active", active)
        log("${kind}_collector_end", fields + ("active" to active))
    }

    inline fun collectorEnd(
        kind: String,
        key: String,
        crossinline fields: () -> Map<String, Any?>,
    ) {
        if (key.isEmpty() || !enabled()) return
        collectorEnd(kind, key, fields())
    }

    fun roomQuery(sql: String, argCount: Int) {
        if (!enabled()) return
        val count = roomQueryCount.incrementAndGet()
        val op = sql.trimStart().substringBefore(' ', missingDelimiterValue = "").uppercase(Locale.US)
        val tables = roomTables(sql)
        val bucket = "$op|$tables|$argCount"
        val bucketCount = roomQueryBuckets.getOrPut(bucket) { AtomicInteger(0) }.incrementAndGet()
        if (bucketCount == 1 || bucketCount % ROOM_QUERY_SUMMARY_INTERVAL == 0) {
            setCounter("igloo_room_query_count", count)
            log(
                event = "room_query_summary",
                fields = mapOf(
                    "count" to count,
                    "bucket_count" to bucketCount,
                    "op" to op,
                    "tables" to tables,
                    "args" to argCount,
                ),
            )
        }
    }

    fun roomInvalidated(tables: Set<String>) {
        if (!enabled()) return
        val count = roomInvalidationCount.incrementAndGet()
        val tableList = tables.sorted().joinToString(",")
        val bucketCount = roomInvalidationBuckets.getOrPut(tableList) { AtomicInteger(0) }.incrementAndGet()
        if (bucketCount == 1 || bucketCount % ROOM_INVALIDATION_SUMMARY_INTERVAL == 0) {
            setCounter("igloo_room_invalidation_count", count)
            log(
                event = "room_invalidated_summary",
                fields = mapOf(
                    "count" to count,
                    "bucket_count" to bucketCount,
                    "tables" to tableList,
                ),
            )
        }
    }

    fun uriKind(value: Any?): String = when {
        value == null -> "null"
        value.javaClass.simpleName == "Local" -> "local"
        value.javaClass.simpleName == "Remote" -> "remote"
        else -> "missing"
    }

    fun elapsedMsSince(startedNanos: Long): Long =
        TimeUnit.NANOSECONDS.toMillis(SystemClock.elapsedRealtimeNanos() - startedNanos)

    private fun sectionName(raw: String): String {
        val clean = raw.replace('\n', ' ').replace('\r', ' ')
        return if (clean.length <= MAX_SECTION_NAME) clean else clean.take(MAX_SECTION_NAME)
    }

    private fun counterName(raw: String): String =
        raw.lowercase(Locale.US).replace(Regex("[^a-z0-9_]+"), "_").trim('_')

    private fun formatLine(event: String, fields: Map<String, Any?>): String {
        if (fields.isEmpty()) return event
        return buildString {
            append(event)
            fields.entries
                .sortedBy { it.key }
                .forEach { (key, value) ->
                    append(' ')
                    append(key)
                    append('=')
                    append(value)
                }
        }
    }

    private fun roomTables(sql: String): String {
        val lower = sql.lowercase(Locale.US)
        val tables = KnownTables.filter { table ->
            lower.contains(" $table") || lower.contains("`$table`")
        }
        return tables.joinToString(",").ifBlank { "unknown" }
    }

    private val KnownTables = listOf(
        "android_sync_assets",
        "android_sync_generations",
        "android_sync_items",
        "media_inventory",
        "videos",
        "feed_items",
        "feed_likes",
        "bookmarks",
        "bookmark_categories",
        "bookmark_labels",
        "moment_views",
        "watch_history",
        "channels",
        "channel_profiles",
        "channel_follows",
        "channel_stars",
        "channel_settings",
        "outbox",
        "preferences",
        "feed_seen",
        "feed_rank",
        "feed_thread_context",
        "retweet_sources",
        "sponsorblock_segments",
        "sponsorblock_checked",
        "video_comments",
        "video_repost_sources",
    )
}
