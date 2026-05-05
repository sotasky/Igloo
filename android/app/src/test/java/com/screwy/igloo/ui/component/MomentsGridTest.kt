package com.screwy.igloo.ui.component

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Pure-function tests for [formatDuration].
 *
 * Convention:
 *  - < 1h     -> "m:ss"
 *  - >= 1h    -> "h:mm:ss"
 *  - zero/neg -> "0:00"
 */
class MomentsGridTest {

    @Test
    fun forty_five_seconds() {
        assertEquals("0:45", formatDuration(45_000L))
    }

    @Test
    fun twelve_minutes_thirty_four_seconds() {
        assertEquals("12:34", formatDuration(754_000L))
    }

    @Test
    fun one_hour_two_minutes_six_seconds() {
        assertEquals("1:02:06", formatDuration(3_726_000L))
    }

    @Test
    fun zero_returns_double_zero() {
        assertEquals("0:00", formatDuration(0L))
    }

    @Test
    fun negative_clamps_to_zero() {
        assertEquals("0:00", formatDuration(-1_000L))
    }
}
