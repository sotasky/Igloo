package com.screwy.igloo.logs

import androidx.annotation.StringRes
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.R
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.outbox.OutboxKind
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/**
 * Logs route state. Reads the most-recent `outbox` rows of kind `log` / `log_debug` and
 * parses each payload JSON into a [LogRowDisplay]. Filter chip state is client-side.
 */
class LogsViewModel(
    db: IglooDatabase,
) : ViewModel() {

    private val _filter = MutableStateFlow(LogFilter.All)
    val filter: StateFlow<LogFilter> = _filter.asStateFlow()

    /** Forces a subscriber re-sync — the underlying DAO flow is already reactive, so
     *  this is a no-op nudge that matches the refresh icon's affordance. */
    fun refresh() { /* no-op; DAO flow is already live */ }

    /** Parsed rows, newest-first. Malformed entries are dropped silently. */
    val rows: StateFlow<List<LogRowDisplay>> = db.outboxDao()
        .logRowsFlow(limit = LOG_LIMIT)
        .map { entities -> entities.mapNotNull { parseLogRow(it) } }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val filteredRows: StateFlow<List<LogRowDisplay>> = combine(rows, _filter) { rs, f ->
        rs.filter { f.matches(it) }
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = emptyList(),
    )

    fun setFilter(f: LogFilter) { _filter.value = f }

    companion object {
        const val LOG_LIMIT = 150
    }
}

/**
 * Filter chips shown above the log list. `All` is the default. `Errors` filters by
 * level; `Sync`/`Outbox`/`Media` filter by the row's derived subsystem; `Debug`
 * filters by stream (the Logger channel).
 */
enum class LogFilter(@param:StringRes val labelRes: Int) {
    All(R.string.logs_filter_all),
    Errors(R.string.logs_filter_errors),
    Sync(R.string.logs_filter_sync),
    Outbox(R.string.logs_filter_outbox),
    Media(R.string.logs_filter_media),
    Debug(R.string.logs_filter_debug);

    fun matches(row: LogRowDisplay): Boolean = when (this) {
        All     -> true
        Errors  -> row.level == "error"
        Sync    -> row.subsystem == Subsystem.Sync
        Outbox  -> row.subsystem == Subsystem.Outbox
        Media   -> row.subsystem == Subsystem.Media
        Debug   -> row.stream == "debug"
    }
}

/**
 * Flattened display payload. Derived from [OutboxEntity] via [parseLogRow] so the
 * UI never inspects raw JSON. Malformed entries return null from the parser.
 */
data class LogRowDisplay(
    val id: Long,
    val timestampMs: Long,
    val stream: String,        // "server" or "debug"
    val level: String?,         // "info" | "error" | null
    val state: String,          // "pending" | "dead" | ...
    val event: String,
    val fields: Map<String, String>,
    val subsystem: Subsystem,   // derived at parse time, see deriveSubsystem()
)

/** Lenient JSON reader — unknown keys are ignored so future payload additions don't crash. */
private val logJson = Json { ignoreUnknownKeys = true; isLenient = true }

/**
 * Parse an `outbox` row of kind `log` or `log_debug` into a [LogRowDisplay].
 * Returns null for malformed payloads or wrong kinds — the route drops them.
 *
 * Payload shape matches `OutboxWriter.buildPayload`:
 *  - kind `log`:       { level, event, timestamp_ms, fields: {...}, updated_at_ms }
 *  - kind `log_debug`: { event, timestamp_ms, fields: {...}, updated_at_ms }
 */
internal fun parseLogRow(entity: OutboxEntity): LogRowDisplay? {
    val stream = when (entity.kind) {
        OutboxKind.CODE_LOG -> "server"
        OutboxKind.CODE_LOG_DEBUG -> "debug"
        else -> return null
    }
    val obj: JsonObject = runCatching {
        logJson.parseToJsonElement(entity.payloadJson).jsonObject
    }.getOrElse { return null }

    val event = obj["event"]?.jsonPrimitive?.contentOrNull ?: return null
    val level = obj["level"]?.jsonPrimitive?.contentOrNull  // null for debug stream
    val timestampMs = obj["timestamp_ms"]?.jsonPrimitive?.longOrNull ?: entity.createdAtMs
    val fields: Map<String, String> = (obj["fields"] as? JsonObject)
        ?.entries
        ?.associate { (k, v) -> k to ((v as? JsonPrimitive)?.contentOrNull ?: v.toString()) }
        ?: emptyMap()

    return LogRowDisplay(
        id = entity.id,
        timestampMs = timestampMs,
        stream = stream,
        level = level,
        state = entity.state,
        event = event,
        fields = fields,
        subsystem = deriveSubsystem(event, fields),
    )
}

/** Pure "HH:mm:ss MM-dd" formatter — separate from the VM so tests can exercise it. */
internal fun formatLogTimestamp(ms: Long): String {
    val fmt = SimpleDateFormat("HH:mm:ss MM-dd", Locale.getDefault())
    return fmt.format(Date(ms))
}

/**
 * One-line plaintext rendering used by the share-to-clipboard action. Kept pure so
 * the route concatenates the list and hands the result to `ClipboardManager`.
 */
internal fun LogRowDisplay.toPlainTextLine(): String {
    val ts = formatLogTimestamp(timestampMs)
    val lvl = level ?: ""
    val fields = if (fields.isEmpty()) "" else " " + fields.entries.joinToString(" ") { (k, v) -> "$k=$v" }
    return "$ts [$stream] $lvl $event$fields".trim()
}
