package com.screwy.igloo.logs

import org.junit.Assert.assertEquals
import org.junit.Test

class EventTemplateTest {

    @Test fun `raw field interpolation`() {
        val tpl = EventTemplate("Applied {count} mutations from server")
        assertEquals(
            "Applied 5 mutations from server",
            tpl.render(mapOf("count" to "5")),
        )
    }

    @Test fun `bytes to kb formatter rounds`() {
        val tpl = EventTemplate("Downloaded — {bytes|kb}")
        assertEquals("Downloaded — 53 KB", tpl.render(mapOf("bytes" to "54321")))
    }

    @Test fun `bytes to kb upgrades to MB`() {
        val tpl = EventTemplate("Downloaded — {bytes|kb}")
        assertEquals("Downloaded — 2.0 MB", tpl.render(mapOf("bytes" to "2097152")))
    }

    @Test fun `human formatter space-separates snake_case`() {
        val tpl = EventTemplate("Promote skipped — {reason|human}")
        assertEquals(
            "Promote skipped — no pending",
            tpl.render(mapOf("reason" to "no_pending")),
        )
    }

    @Test fun `missing field renders as question mark`() {
        val tpl = EventTemplate("kind={kind}")
        assertEquals("kind=?", tpl.render(emptyMap()))
    }
}
