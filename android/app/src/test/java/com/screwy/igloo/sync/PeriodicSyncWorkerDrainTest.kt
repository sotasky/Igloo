package com.screwy.igloo.sync

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PeriodicSyncWorkerDrainTest {

    @Test fun drainCompletesAfterRequiredIdlePolls() = runBlocking {
        var now = 0L
        val result = awaitSyncDrainOrCap(
            maxRunDurationMs = 100,
            pollIntervalMs = 10,
            startupGraceMs = 0,
            idleStreakRequired = 2,
            nowMs = { now },
            delayMs = { now += it },
        ) {
            PeriodicSyncDrainStatus(
                generationId = "android-sync-test",
                incompleteImports = 0,
                activeOrEligibleAssets = 0,
                runnerWork = false,
            )
        }

        assertTrue(result.completed)
        assertEquals(0, result.remainingWork)
        assertEquals(20, result.elapsedMs)
    }

    @Test fun drainReportsIncompleteWhenCapExpiresWithRemainingWork() = runBlocking {
        var now = 0L
        val result = awaitSyncDrainOrCap(
            maxRunDurationMs = 30,
            pollIntervalMs = 10,
            startupGraceMs = 0,
            idleStreakRequired = 2,
            nowMs = { now },
            delayMs = { now += it },
        ) {
            PeriodicSyncDrainStatus(
                generationId = "android-sync-test",
                incompleteImports = 1,
                activeOrEligibleAssets = 2,
                runnerWork = true,
            )
        }

        assertFalse(result.completed)
        assertEquals(4, result.remainingWork)
        assertEquals(1, result.incompleteImports)
        assertTrue(result.runnerWork)
    }
}
