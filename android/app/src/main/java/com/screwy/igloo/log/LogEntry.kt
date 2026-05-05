package com.screwy.igloo.log

/**
 * Logical log record, produced by `Logger` and consumed by `LogSink`.
 */
enum class LogLevel { Info, Error, Debug }

data class LogEntry(
    val level: LogLevel,
    val event: String,
    val fields: Map<String, Any?>,
    val timestampMs: Long,
)

/** Pluggable sink for log delivery. */
fun interface LogSink {
    suspend fun accept(entry: LogEntry)
}

/** Bounded in-memory buffer for tests and lightweight local sinks. */
class InMemoryLogSink(private val capacity: Int = 500) : LogSink {
    private val buffer = ArrayDeque<LogEntry>(capacity)
    private val lock = Any()

    override suspend fun accept(entry: LogEntry) {
        synchronized(lock) {
            if (buffer.size >= capacity) buffer.removeFirst()
            buffer.addLast(entry)
        }
    }

    fun snapshot(): List<LogEntry> = synchronized(lock) { buffer.toList() }

    fun size(): Int = synchronized(lock) { buffer.size }
}
