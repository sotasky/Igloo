package com.screwy.igloo.logs

import org.junit.Assert.assertTrue
import org.junit.Test

class LogFilterTest {

    private val rows = listOf(
        row(event = "app_start", subsystem = Subsystem.App, stream = "server", level = "info"),
        row(event = "outbox_row_post_failed", subsystem = Subsystem.Outbox, stream = "server", level = "error"),
        row(event = "android_sync_metadata_retry", subsystem = Subsystem.Sync, stream = "server", level = "info"),
        row(event = "media_foreground_service_start", subsystem = Subsystem.Media, stream = "server", level = "info"),
        row(event = "outbox_drain_skipped_offline", subsystem = Subsystem.Outbox, stream = "debug", level = null),
    )

    @Test fun `All passes every row`() =
        assertTrue(rows.all(LogFilter.All::matches))

    @Test fun `Errors keeps only error level`() =
        assertTrue(rows.filter(LogFilter.Errors::matches).all { it.level == "error" })

    @Test fun `Sync keeps only sync subsystem`() =
        assertTrue(rows.filter(LogFilter.Sync::matches).all { it.subsystem == Subsystem.Sync })

    @Test fun `Outbox keeps only outbox subsystem`() =
        assertTrue(rows.filter(LogFilter.Outbox::matches).all { it.subsystem == Subsystem.Outbox })

    @Test fun `Media keeps only media subsystem`() =
        assertTrue(rows.filter(LogFilter.Media::matches).all { it.subsystem == Subsystem.Media })

    @Test fun `Debug keeps only debug stream`() =
        assertTrue(rows.filter(LogFilter.Debug::matches).all { it.stream == "debug" })

    private fun row(
        event: String,
        subsystem: Subsystem,
        stream: String,
        level: String?,
    ): LogRowDisplay = LogRowDisplay(
        id = 0, timestampMs = 0L, stream = stream, level = level,
        state = "pending", event = event, fields = emptyMap(), subsystem = subsystem,
    )
}
