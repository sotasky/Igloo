package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.Query
import androidx.room.Transaction
import com.screwy.igloo.data.entity.OutboxEntity
import kotlinx.coroutines.flow.Flow

/**
 * Outbox DAO: drain queue, coalescing, and state transitions.
 *
 * Rows live in one of two states:
 *  - `pending` — eligible for drain once `next_attempt_at_ms <= now`.
 *  - `dead`   — terminal; rollback already fired. Swept by the 24h TTL clause.
 *
 * Indexes live on (state, next_attempt_at_ms) for the claim query and
 * (kind, field, item_id) for coalesce deletes — entity config carries them.
 */
@Dao
interface OutboxDao {

    // ─── Enqueue path ─────────────────────────────────────────────────────────

    /**
     * Coalesce + insert. Writer pre-computes the coalesce key based on the
     * mutation kind. Some kinds have no coalesce and pass noop args; the normal
     * shape is `(kind, field, item_id)`.
     *
     * Runs as one transaction so a concurrent enqueue can't land between the delete and
     * the insert and leave a duplicate pending row.
     */
    @Transaction
    suspend fun coalesceAndInsert(row: OutboxEntity) {
        deleteCoalesceMatch(kind = row.kind, field = row.field, itemId = row.itemId)
        insert(row)
    }

    @Insert
    suspend fun insert(row: OutboxEntity): Long

    @Query(
        """
        DELETE FROM outbox
        WHERE state = 'pending'
          AND kind = :kind
          AND (field IS :field OR (field IS NULL AND :field IS NULL))
          AND (item_id IS :itemId OR (item_id IS NULL AND :itemId IS NULL))
        """
    )
    suspend fun deleteCoalesceMatch(kind: String, field: String?, itemId: String?)

    /**
     * Singleton coalesce — delete every prior pending row of the kind (regardless of
     * item_id / field), then insert. Used by `moments_cursor`.
     */
    @Transaction
    suspend fun coalesceAndInsertSingleton(row: OutboxEntity) {
        deleteAllPendingOfKind(kind = row.kind)
        insert(row)
    }

    @Query("DELETE FROM outbox WHERE state = 'pending' AND kind = :kind")
    suspend fun deleteAllPendingOfKind(kind: String): Int

    // ─── Claim + drain path ───────────────────────────────────────────────────

    /**
     * Pending rows eligible for drain, FIFO by `created_at_ms`. Drain claims a
     * bounded batch, defaulting to 100.
     */
    @Query(
        """
        SELECT * FROM outbox
        WHERE state = 'pending' AND next_attempt_at_ms <= :nowMs
        ORDER BY created_at_ms
        LIMIT :limit
        """
    )
    suspend fun claimPending(nowMs: Long, limit: Int = 100): List<OutboxEntity>

    /**
     * All pending rows for a given kind (e.g., drain batches all pending `seen` / `log`
     * into one HTTP call). Kept separate from the generic claim so batchable kinds can
     * pull their own slice after the FIFO claim.
     */
    @Query(
        """
        SELECT * FROM outbox
        WHERE state = 'pending' AND kind = :kind AND next_attempt_at_ms <= :nowMs
        ORDER BY created_at_ms
        LIMIT :limit
        """
    )
    suspend fun claimKind(kind: String, nowMs: Long, limit: Int = 500): List<OutboxEntity>

    // ─── Result application ───────────────────────────────────────────────────

    @Query("DELETE FROM outbox WHERE id = :id")
    suspend fun completeAndDelete(id: Long)

    @Query("DELETE FROM outbox WHERE id IN (:ids)")
    suspend fun completeAndDeleteAll(ids: List<Long>)

    /**
     * Log-inspector retention: log kinds land in `state='acked'` after a successful
     * drain instead of being deleted, so the Logs screen can render shipped events.
     */
    @Query("UPDATE outbox SET state = 'acked', last_error_code = NULL, last_error_body = NULL WHERE id = :id")
    suspend fun markAcked(id: Long)

    /** Trim the oldest acked log rows beyond [keep] so outbox doesn't grow unbounded. */
    @Query(
        """
        DELETE FROM outbox
        WHERE state = 'acked'
          AND kind IN ('log', 'log_debug')
          AND id NOT IN (
            SELECT id FROM outbox
            WHERE state = 'acked' AND kind IN ('log', 'log_debug')
            ORDER BY created_at_ms DESC
            LIMIT :keep
          )
        """
    )
    suspend fun trimAckedLogs(keep: Int)

    @Query(
        """
        UPDATE outbox
        SET attempt_count = :attemptCount,
            next_attempt_at_ms = :nextAttemptAtMs,
            last_error_code = :errorCode,
            last_error_body = :errorBody
        WHERE id = :id
        """
    )
    suspend fun markPending(id: Long, attemptCount: Int, nextAttemptAtMs: Long, errorCode: Int?, errorBody: String?)

    @Query(
        """
        UPDATE outbox
        SET state = 'dead',
            last_error_code = :errorCode,
            last_error_body = :errorBody
        WHERE id = :id
        """
    )
    suspend fun markDead(id: Long, errorCode: Int?, errorBody: String?)

    // ─── Preserve-local filter ───────────────────────────────────────────────

    @Query(
        """
        SELECT EXISTS(
            SELECT 1 FROM outbox
            WHERE state = 'pending' AND kind = :kind
              AND (item_id IS :itemId OR (item_id IS NULL AND :itemId IS NULL))
              AND (field IS :field OR (field IS NULL AND :field IS NULL))
        )
        """
    )
    suspend fun hasPending(kind: String, itemId: String?, field: String?): Boolean

    // ─── TTL sweeps ──────────────────────────────────────────────────────────

    /** Clause 1: perpetually-failing pending rows flip to dead after 24h. */
    @Query(
        """
        UPDATE outbox
        SET state = 'dead',
            last_error_code = COALESCE(last_error_code, -1),
            last_error_body = COALESCE(last_error_body, 'TTL exceeded 24h pending')
        WHERE state = 'pending' AND created_at_ms < :cutoffMs
        """
    )
    suspend fun markStuckPendingDead(cutoffMs: Long): Int

    /** Clause 2: dead rows older than 24h get swept so the table doesn't grow unbounded. */
    @Query("DELETE FROM outbox WHERE state = 'dead' AND created_at_ms < :cutoffMs")
    suspend fun deleteOldDead(cutoffMs: Long): Int

    /** Dead-row feed for the in-app Logs view — oldest-first is fine; UI may re-order. */
    @Query("SELECT * FROM outbox WHERE state = 'dead' ORDER BY created_at_ms DESC LIMIT :limit")
    fun deadRowsFlow(limit: Int = 200): Flow<List<OutboxEntity>>

    // ─── Logs inspector ──────────────────────────────────────────────────────

    @Query(
        """
        SELECT * FROM outbox
        WHERE kind IN ('log', 'log_debug')
        ORDER BY created_at_ms DESC
        LIMIT :limit
        """
    )
    fun logRowsFlow(limit: Int = 500): Flow<List<OutboxEntity>>

    // ─── Wipe ────────────────────────────────────────────────────────────────

    @Query("DELETE FROM outbox")
    suspend fun deleteAll()

    @Query("SELECT COUNT(*) FROM outbox WHERE state = :state")
    suspend fun countByState(state: String): Int
}
