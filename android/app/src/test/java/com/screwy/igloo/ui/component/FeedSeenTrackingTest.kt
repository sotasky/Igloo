package com.screwy.igloo.ui.component

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-logic tests for the native feed scroll-seen pipeline.
 *
 * The batcher adds each id exactly once and emits the accumulated batch on
 * [SeenBatcher.flush], clearing the pending buffer. [SeenBatcher.flush] is a
 * no-op when the buffer is empty (no redundant emits).
 */
class FeedSeenTrackingTest {
    @Test
    fun add_ignores_duplicates() {
        val emitted = mutableListOf<List<String>>()
        val batcher = SeenBatcher { emitted.add(it) }

        batcher.add("a")
        batcher.add("a")
        batcher.add("b")
        batcher.flush()

        assertEquals(1, emitted.size)
        assertEquals(listOf("a", "b"), emitted.first())
    }

    @Test
    fun flush_clears_pending() {
        val emitted = mutableListOf<List<String>>()
        val batcher = SeenBatcher { emitted.add(it) }

        batcher.add("x")
        batcher.flush()
        batcher.flush()

        assertEquals(1, emitted.size)
        assertEquals(listOf("x"), emitted.first())
    }

    @Test
    fun flush_empty_does_not_emit() {
        val emitted = mutableListOf<List<String>>()
        val batcher = SeenBatcher { emitted.add(it) }

        batcher.flush()

        assertTrue(emitted.isEmpty())
    }

    @Test
    fun add_after_flush_emits_only_new_ids() {
        val emitted = mutableListOf<List<String>>()
        val batcher = SeenBatcher { emitted.add(it) }

        batcher.add("a")
        batcher.flush()
        batcher.add("a")
        batcher.add("b")
        batcher.flush()

        assertEquals(2, emitted.size)
        assertEquals(listOf("a"), emitted[0])
        assertEquals(listOf("b"), emitted[1])
    }

    @Test
    fun passedRowsTracker_marks_only_rows_actually_scrolled_past() {
        val emitted = mutableListOf<List<String>>()
        val batcher = SeenBatcher { emitted.add(it) }
        val tracker = PassedFeedRowsTracker(batcher)

        tracker.onViewportChanged(
            rowIds = listOf("a", "b", "c", "d"),
            firstVisibleIndex = 0,
        )
        tracker.onViewportChanged(
            rowIds = listOf("a", "b", "c", "d"),
            firstVisibleIndex = 2,
        )
        batcher.flush()

        assertEquals(listOf(listOf("a", "b")), emitted)
    }

    @Test
    fun passedRowsTracker_doesNotSnowballWhenSeenRowsDisappear() {
        val emitted = mutableListOf<List<String>>()
        val batcher = SeenBatcher { emitted.add(it) }
        val tracker = PassedFeedRowsTracker(batcher)

        tracker.onViewportChanged(
            rowIds = listOf("a", "b", "c", "d"),
            firstVisibleIndex = 0,
        )
        tracker.onViewportChanged(
            rowIds = listOf("a", "b", "c", "d"),
            firstVisibleIndex = 1,
        )
        batcher.flush()

        tracker.onViewportChanged(
            rowIds = listOf("b", "c", "d"),
            firstVisibleIndex = 1,
        )
        batcher.flush()

        assertEquals(listOf(listOf("a")), emitted)
    }
}
