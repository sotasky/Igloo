package com.screwy.igloo.logs

import org.junit.Assert.assertEquals
import org.junit.Test

class SubsystemTest {

    @Test fun `event subsystem field wins over prefix`() {
        assertEquals(
            Subsystem.Outbox,
            deriveSubsystem("android_sync_metadata_retry", mapOf("subsystem" to "outbox")),
        )
    }

    @Test fun `outbox_ prefix maps to Outbox`() {
        assertEquals(Subsystem.Outbox, deriveSubsystem("outbox_row_post_failed", emptyMap()))
        assertEquals(Subsystem.Outbox, deriveSubsystem("outbox_drain_skipped_offline", emptyMap()))
    }

    @Test fun `sync-family prefixes map to Sync`() {
        assertEquals(Subsystem.Sync, deriveSubsystem("android_sync_metadata_retry", emptyMap()))
        assertEquals(Subsystem.Sync, deriveSubsystem("sync_pass_failed", emptyMap()))
        assertEquals(Subsystem.Sync, deriveSubsystem("periodic_sync_triggered", emptyMap()))
    }

    @Test fun `media-family prefixes map to Media`() {
        assertEquals(Subsystem.Media, deriveSubsystem("media_foreground_service_start", emptyMap()))
        assertEquals(Subsystem.Media, deriveSubsystem("cache_cleared", emptyMap()))
    }

    @Test fun `app_start maps to App`() {
        assertEquals(Subsystem.App, deriveSubsystem("app_start", emptyMap()))
    }

    @Test fun `unknown event falls back to Other`() {
        assertEquals(Subsystem.Other, deriveSubsystem("totally_unknown_event", emptyMap()))
    }
}
