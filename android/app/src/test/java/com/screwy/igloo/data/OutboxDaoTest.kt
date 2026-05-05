package com.screwy.igloo.data

import com.screwy.igloo.data.entity.OutboxEntity
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Outbox DAO: claim ordering, coalesce atomicity, state transitions, 24h TTL sweeps,
 * preserve-local predicate. Index usage for the claim query is asserted via raw
 * `EXPLAIN QUERY PLAN` against the Room-owned connection.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class OutboxDaoTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    private fun pending(
        kind: String, itemId: String? = null, field: String? = null,
        payload: String = "{}", createdAtMs: Long = 0, nextAttemptAtMs: Long = 0,
    ) = OutboxEntity(
        kind = kind, itemId = itemId, field = field, payloadJson = payload,
        createdAtMs = createdAtMs, nextAttemptAtMs = nextAttemptAtMs,
    )

    @Test fun insertAndClaim_fifoOrder() = runBlocking {
        val dao = db.outboxDao()
        dao.insert(pending(kind = "like", itemId = "t1", createdAtMs = 300))
        dao.insert(pending(kind = "like", itemId = "t2", createdAtMs = 100))
        dao.insert(pending(kind = "like", itemId = "t3", createdAtMs = 200))

        val claimed = dao.claimPending(nowMs = 10_000)
        assertEquals(listOf("t2", "t3", "t1"), claimed.map { it.itemId }) // created_at_ms order
    }

    @Test fun claimPending_respectsNextAttempt() = runBlocking {
        val dao = db.outboxDao()
        dao.insert(pending(kind = "like", itemId = "t1", createdAtMs = 10, nextAttemptAtMs = 0))
        dao.insert(pending(kind = "like", itemId = "t2", createdAtMs = 20, nextAttemptAtMs = 5_000))
        val claimed = dao.claimPending(nowMs = 1_000)
        assertEquals(listOf("t1"), claimed.map { it.itemId }) // t2 not yet eligible
    }

    @Test fun coalesceAndInsert_replacesPriorRow() = runBlocking {
        val dao = db.outboxDao()
        dao.coalesceAndInsert(pending(kind = "like", itemId = "t1", payload = """{"a":"set"}""", createdAtMs = 10))
        dao.coalesceAndInsert(pending(kind = "like", itemId = "t1", payload = """{"a":"clear"}""", createdAtMs = 20))
        val all = dao.claimPending(nowMs = 1_000)
        assertEquals(1, all.size)
        assertEquals("""{"a":"clear"}""", all.first().payloadJson)
    }

    @Test fun coalesceAndInsert_perFieldCoalesce() = runBlocking {
        val dao = db.outboxDao()
        dao.coalesceAndInsert(pending(kind = "channel_setting", itemId = "ch", field = "media_only",      createdAtMs = 1))
        dao.coalesceAndInsert(pending(kind = "channel_setting", itemId = "ch", field = "include_reposts", createdAtMs = 2))
        dao.coalesceAndInsert(pending(kind = "channel_setting", itemId = "ch", field = "media_only",      createdAtMs = 3))
        // Expect 2 rows: one per field; the second media_only replaced the first.
        val all = dao.claimPending(nowMs = 1_000)
        assertEquals(2, all.size)
        val fields = all.mapNotNull { it.field }.sorted()
        assertEquals(listOf("include_reposts", "media_only"), fields)
    }

    @Test fun claimKind_scopedByKind() = runBlocking {
        val dao = db.outboxDao()
        dao.insert(pending(kind = "seen", itemId = "t1", createdAtMs = 10))
        dao.insert(pending(kind = "seen", itemId = "t2", createdAtMs = 20))
        dao.insert(pending(kind = "like", itemId = "t3", createdAtMs = 30))
        val seen = dao.claimKind(kind = "seen", nowMs = 1_000)
        assertEquals(listOf("t1", "t2"), seen.map { it.itemId })
    }

    @Test fun hasPending_matchesCoalesceKey() = runBlocking {
        val dao = db.outboxDao()
        dao.insert(pending(kind = "channel_setting", itemId = "ch", field = "media_only"))
        assertTrue(dao.hasPending(kind = "channel_setting", itemId = "ch", field = "media_only"))
        assertFalse(dao.hasPending(kind = "channel_setting", itemId = "ch", field = "max_videos"))
        assertFalse(dao.hasPending(kind = "like", itemId = "ch", field = null))
    }

    @Test fun markDead_removesFromPendingClaim() = runBlocking {
        val dao = db.outboxDao()
        val id = dao.insert(pending(kind = "bookmark", itemId = "v"))
        dao.markDead(id, errorCode = 422, errorBody = "invalid")
        assertTrue(dao.claimPending(nowMs = 1_000).isEmpty())
        assertEquals(1, dao.countByState("dead"))
    }

    @Test fun markPending_reschedulesWithBackoff() = runBlocking {
        val dao = db.outboxDao()
        val id = dao.insert(pending(kind = "bookmark", itemId = "v", createdAtMs = 0, nextAttemptAtMs = 0))
        dao.markPending(id, attemptCount = 1, nextAttemptAtMs = 30_000, errorCode = 500, errorBody = "retry")
        assertTrue(dao.claimPending(nowMs = 10_000).isEmpty())
        val claimedLater = dao.claimPending(nowMs = 60_000)
        assertEquals(1, claimedLater.size)
        assertEquals(1, claimedLater.first().attemptCount)
    }

    @Test fun completeAndDelete_removesRow() = runBlocking {
        val dao = db.outboxDao()
        val id = dao.insert(pending(kind = "like", itemId = "t"))
        dao.completeAndDelete(id)
        assertTrue(dao.claimPending(nowMs = 1_000).isEmpty())
    }

    @Test fun ttlClauses_flipStuckPendingAndSweepDead() = runBlocking {
        val dao = db.outboxDao()
        val now = 100_000_000L
        val day = 86_400_000L
        // Old pending — outside 24h window
        dao.insert(pending(kind = "like", itemId = "old", createdAtMs = now - day - 1))
        // Fresh pending — well inside window
        dao.insert(pending(kind = "like", itemId = "new", createdAtMs = now - 1_000))
        assertEquals(2, dao.countByState("pending"))

        val flipped = dao.markStuckPendingDead(cutoffMs = now - day)
        assertEquals(1, flipped)
        assertEquals(1, dao.countByState("pending"))
        assertEquals(1, dao.countByState("dead"))

        val deleted = dao.deleteOldDead(cutoffMs = now - day)
        assertEquals(1, deleted)
        assertEquals(0, dao.countByState("dead"))
    }

    @Test fun logRowsFlow_returnsLogKindsOnly() = runBlocking {
        val dao = db.outboxDao()
        dao.insert(pending(kind = "log",       itemId = null, createdAtMs = 30))
        dao.insert(pending(kind = "log_debug", itemId = null, createdAtMs = 20))
        dao.insert(pending(kind = "like",      itemId = "t",  createdAtMs = 10))
        val rows = dao.logRowsFlow().first()
        assertEquals(2, rows.size)
        assertTrue(rows.all { it.kind in listOf("log", "log_debug") })
        // DESC by created_at_ms
        assertEquals("log",       rows[0].kind)
        assertEquals("log_debug", rows[1].kind)
    }

    @Test fun claimPending_usesOutboxClaimIndex() = runBlocking {
        // Seed enough rows that the query planner would prefer the covering index
        // over a sequential scan. We assert the plan mentions `idx_outbox_claim` — the
        // design doc's core discipline is that claim is index-backed.
        val dao = db.outboxDao()
        repeat(200) { i ->
            dao.insert(pending(kind = "like", itemId = "t$i", createdAtMs = i.toLong()))
        }
        val plan = mutableListOf<String>()
        db.openHelper.readableDatabase.query(
            "EXPLAIN QUERY PLAN SELECT * FROM outbox " +
                "WHERE state = 'pending' AND next_attempt_at_ms <= 100 " +
                "ORDER BY created_at_ms LIMIT 100",
        ).use { cursor ->
            while (cursor.moveToNext()) {
                plan += (0 until cursor.columnCount).joinToString(" ") { cursor.getString(it) ?: "" }
            }
        }
        val joined = plan.joinToString("\n")
        assertTrue(
            "Expected EXPLAIN QUERY PLAN to name idx_outbox_claim — got: $joined",
            joined.contains("idx_outbox_claim"),
        )
    }
}
