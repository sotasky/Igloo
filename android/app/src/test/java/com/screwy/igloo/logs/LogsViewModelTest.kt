package com.screwy.igloo.logs

import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.outbox.OutboxKind
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-helper coverage for the logs route. VM wiring itself is trivial; the
 * parse + format helpers carry all the risk.
 */
class LogsViewModelTest {

    @Test fun parseLogRow_parsesServerStreamEntity() {
        val entity = OutboxEntity(
            id = 1L,
            kind = OutboxKind.CODE_LOG,
            itemId = null,
            field = null,
            payloadJson = """{
                "level":"info",
                "event":"sync_started",
                "timestamp_ms":1700000000000,
                "fields":{"stream":"feed","count":"12"},
                "updated_at_ms":1700000000000
            }""".trimIndent(),
            state = "pending",
            createdAtMs = 1700000000000L,
        )

        val row = parseLogRow(entity)

        assertNotNull(row)
        assertEquals("server", row!!.stream)
        assertEquals("info", row.level)
        assertEquals("sync_started", row.event)
        assertEquals(mapOf("stream" to "feed", "count" to "12"), row.fields)
        assertEquals(1700000000000L, row.timestampMs)
    }

    @Test fun parseLogRow_parsesDebugStreamEntityWithNullLevel() {
        val entity = OutboxEntity(
            id = 2L,
            kind = OutboxKind.CODE_LOG_DEBUG,
            itemId = null,
            field = null,
            payloadJson = """{
                "event":"cache_hit",
                "timestamp_ms":1700000001000,
                "fields":{"bucket":"avatars"},
                "updated_at_ms":1700000001000
            }""".trimIndent(),
            state = "pending",
            createdAtMs = 1700000001000L,
        )

        val row = parseLogRow(entity)

        assertNotNull(row)
        assertEquals("debug", row!!.stream)
        assertNull(row.level)
        assertEquals("cache_hit", row.event)
        assertEquals(mapOf("bucket" to "avatars"), row.fields)
    }

    @Test fun parseLogRow_returnsNullOnMalformedPayload() {
        val entity = OutboxEntity(
            id = 3L,
            kind = OutboxKind.CODE_LOG,
            itemId = null,
            field = null,
            payloadJson = "not valid json {{{",
            state = "pending",
            createdAtMs = 1700000002000L,
        )
        assertNull(parseLogRow(entity))
    }

    @Test fun parseLogRow_returnsNullForNonLogKind() {
        val entity = OutboxEntity(
            id = 4L,
            kind = OutboxKind.CODE_LIKE,
            itemId = "t1",
            field = null,
            payloadJson = """{"tweet_id":"t1","action":"set"}""",
            state = "pending",
            createdAtMs = 1700000003000L,
        )
        assertNull(parseLogRow(entity))
    }

    @Test fun formatLogTimestamp_producesExpectedShape() {
        // "HH:mm:ss MM-dd" — loose assertion on length/separators so the test stays
        // locale + timezone-agnostic.
        val out = formatLogTimestamp(1700000000000L)
        // 8 chars time + space + 5 chars date == 14
        assertEquals(14, out.length)
        // time has two colons, date has one dash
        assertEquals(2, out.substring(0, 8).count { it == ':' })
        assertEquals(1, out.substring(9).count { it == '-' })
    }

    @Test fun toPlainTextLine_includesTimestampStreamLevelEventAndFields() {
        val row = LogRowDisplay(
            id = 1L,
            timestampMs = 1700000000000L,
            stream = "server",
            level = "error",
            state = "pending",
            event = "drain_failed",
            fields = mapOf("code" to "503", "kind" to "like"),
            subsystem = Subsystem.Other,
        )
        val line = row.toPlainTextLine()
        assertTrue(line.contains("[server]"))
        assertTrue(line.contains("error"))
        assertTrue(line.contains("drain_failed"))
        assertTrue(line.contains("code=503"))
        assertTrue(line.contains("kind=like"))
    }

    @Test fun displayFieldValue_truncatesLargeFields() {
        val out = displayFieldValue("a".repeat(1_205))

        assertEquals(1_204, out.length)
        assertTrue(out.endsWith("\n..."))
    }

    @Test fun eventTemplate_rendersPlaceholdersOnAndroidRegexEngine() {
        val template = EventTemplate("Cleared {bucket|human} cache; kept {count} rows")

        val out = template.render(mapOf("bucket" to "feed_items", "count" to "12"))

        assertEquals("Cleared feed items cache; kept 12 rows", out)
    }
}
