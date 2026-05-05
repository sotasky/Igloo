package com.screwy.igloo.outbox

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.Reachability
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.merge

/**
 * Outbox drain loop.
 *
 * Three trigger sources fan into the merged signal flow:
 *  - write-time — `OutboxWriter.drainSignal` (already debounced 3s).
 *  - reachability upgrade — `Scheduler.observeReachabilityUpgrades`.
 *  - periodic — WorkManager-driven `scheduler.triggerAll()`.
 *
 * Drain pass shape:
 *  1. Reachability gate — if offline, log and wait for next signal.
 *  2. Claim up to 100 eligible pending rows (FIFO by `created_at_ms`).
 *  3. Group by `kind`:
 *     - `seen` / `log` / `log_debug`     → single batched HTTP call.
 *     - everything else                   → one HTTP call per row.
 *  4. For each group, call `OutboxDispatcher.dispatch(batch)`.
 *  5. Apply per-row result:
 *     - `Ack`         → delete the row.
 *     - `Retry`       → bump `attempt_count` + schedule backoff.
 *     - `Dead`        → mark dead + run rollback in one transaction.
 *     - `AuthRefresh` → wait for auth refresh, then keep the row pending.
 *  6. TTL sweep — 24h-stuck-pending → dead, 24h-dead → delete.
 *  7. If more eligible rows remain, loop without waiting on signal (flush-through).
 */
class OutboxDrain(
    private val outboxDao: OutboxDao,
    private val dispatcher: OutboxDispatcher,
    private val db: IglooDatabase,
    private val prefs: PreferencesRepo,
    private val reachability: Reachability,
    private val logger: Logger,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) {

    /** Local manual trigger used from scheduler / tests when no shared signal is wired. */
    private val localTrigger = MutableSharedFlow<Unit>(
        replay = 0,
        extraBufferCapacity = 8,
    )

    /** Write-time `OutboxWriter.drainSignal` — installed via `wireWriter` at Koin-time. */
    private var writerSignal: SharedFlow<Unit>? = null

    /** Emits once after an online drain pass completes (even when the queue was already empty). */
    private val _passCompleted = MutableSharedFlow<Unit>(
        replay = 0,
        extraBufferCapacity = 8,
    )
    val passCompleted: SharedFlow<Unit> = _passCompleted

    fun wireWriter(writer: OutboxWriter) {
        writerSignal = writer.drainSignal
    }

    fun trigger() {
        localTrigger.tryEmit(Unit)
    }

    suspend fun run() {
        val upstream = writerSignal?.let { merge(localTrigger, it) } ?: localTrigger
        upstream.collect {
            try {
                runDrainPass()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                logger.error("outbox_drain_unhandled", mapOf("class" to (e::class.simpleName ?: "?")), e)
            }
        }
    }

    /** One drain pass; may loop internally when the claim still has rows. */
    internal suspend fun runDrainPass() {
        dropPendingDebugLogsWhenDisabled()
        if (reachability.state.value !is Reachability.State.Online) {
            logger.debug("outbox_drain_skipped_offline", mapOf("state" to reachability.state.value::class.simpleName.orEmpty()))
            return
        }

        // TTL sweep runs once per pass — cheap, idempotent.
        ttlSweep()

        while (true) {
            val nowMs = nowMsProvider()
            val batch = outboxDao.claimPending(nowMs = nowMs, limit = CLAIM_LIMIT)
            if (batch.isEmpty()) break

            val groups = groupForDispatch(batch, nowMs)
            for (group in groups) {
                val results = dispatcher.dispatch(group)
                applyResults(group, results)
            }

            if (batch.size < CLAIM_LIMIT) break
            // Else: flush-through loop — more eligible rows pending.
        }
        _passCompleted.tryEmit(Unit)
    }

    // ─── Grouping ─────────────────────────────────────────────────────────────

    /**
     * Group rows into dispatch batches:
     *  - batchable kinds (`seen` / `log` / `log_debug`) pull in their whole pending slice
     *    at claim time (up to per-kind caps) so one HTTP call carries them all.
     *  - non-batchable kinds dispatch one row per call, in FIFO order.
     */
    private suspend fun groupForDispatch(
        firstClaim: List<OutboxEntity>,
        nowMs: Long,
    ): List<List<OutboxEntity>> {
        val seenSeen = firstClaim.any { it.kind == OutboxKind.CODE_SEEN }
        val seenLog = firstClaim.any { it.kind == OutboxKind.CODE_LOG }
        val seenDebug = firstClaim.any { it.kind == OutboxKind.CODE_LOG_DEBUG }

        // Pull the wider per-kind batches so we hit the 500/100 caps in §5.
        val seenBatch = if (seenSeen) outboxDao.claimKind(OutboxKind.CODE_SEEN, nowMs, limit = SEEN_BATCH_LIMIT) else emptyList()
        val logBatch = if (seenLog) outboxDao.claimKind(OutboxKind.CODE_LOG, nowMs, limit = LOG_BATCH_LIMIT) else emptyList()
        val debugBatch = if (seenDebug) outboxDao.claimKind(OutboxKind.CODE_LOG_DEBUG, nowMs, limit = LOG_BATCH_LIMIT) else emptyList()

        // Every ID we've pulled for a batch gets excluded from the per-row slice below
        // so a single row can't end up in two batches this pass.
        val batchedIds: Set<Long> = buildSet {
            addAll(seenBatch.map { it.id })
            addAll(logBatch.map { it.id })
            addAll(debugBatch.map { it.id })
        }
        val perRowCandidates = firstClaim.filter { it.id !in batchedIds }

        val out = mutableListOf<List<OutboxEntity>>()
        // Preserve FIFO ordering across groups — batches lead by their earliest row, then
        // remaining rows in their arrival order.
        if (seenBatch.isNotEmpty())  out += seenBatch
        if (logBatch.isNotEmpty())   out += logBatch
        if (debugBatch.isNotEmpty()) out += debugBatch
        for (row in perRowCandidates) out += listOf(row)
        return out
    }

    // ─── Result application ───────────────────────────────────────────────────

    private suspend fun applyResults(batch: List<OutboxEntity>, results: Map<Long, OutboxDispatcher.Result>) {
        for (row in batch) {
            val result = results[row.id] ?: continue
            when (result) {
                is OutboxDispatcher.Result.Ack -> {
                    if (row.kind == OutboxKind.CODE_LOG || row.kind == OutboxKind.CODE_LOG_DEBUG) {
                        // Retain shipped logs so the Logs inspector can show history.
                        outboxDao.markAcked(row.id)
                        outboxDao.trimAckedLogs(keep = LOGS_INSPECTOR_CAP)
                    } else {
                        outboxDao.completeAndDelete(row.id)
                    }
                }
                is OutboxDispatcher.Result.Retry -> scheduleRetry(row, result.error)
                is OutboxDispatcher.Result.Dead -> markDeadAndRollback(row, result.error)
                is OutboxDispatcher.Result.AuthRefresh -> {
                    // Leave row in `pending` with `next_attempt_at_ms=0` so drain retries
                    // as soon as the auth layer upgrades state. 401 doesn't count against
                    // the retry attempt budget.
                    if (!row.isLogKind()) {
                        logger.debug("outbox_row_auth_refresh", mapOf("id" to row.id.toString(), "kind" to row.kind))
                    }
                }
            }
        }
    }

    private suspend fun scheduleRetry(row: OutboxEntity, err: com.screwy.igloo.net.IglooError) {
        val nextAttempt = row.attemptCount + 1
        val delayMs = backoffMs(nextAttempt)
        outboxDao.markPending(
            id = row.id,
            attemptCount = nextAttempt,
            nextAttemptAtMs = nowMsProvider() + delayMs,
            errorCode = err.status,
            errorBody = err.errorMessage?.take(200),
        )
        if (!row.isLogKind()) {
            logger.debug(
                event = "outbox_row_retry",
                fields = mapOf(
                    "id" to row.id.toString(),
                    "kind" to row.kind,
                    "attempt" to nextAttempt.toString(),
                    "delay_ms" to delayMs.toString(),
                    "status" to (err.status?.toString() ?: "?"),
                ),
            )
        }
    }

    private suspend fun markDeadAndRollback(row: OutboxEntity, err: com.screwy.igloo.net.IglooError) {
        db.withTransaction {
            outboxDao.markDead(id = row.id, errorCode = err.status, errorBody = err.errorMessage?.take(200))
            dispatcher.rollback(row)
        }
        if (!row.isLogKind()) {
            logger.error(
                event = "outbox_row_dead",
                fields = mapOf(
                    "id" to row.id.toString(),
                    "kind" to row.kind,
                    "status" to (err.status?.toString() ?: "?"),
                    "code" to err.errorCode.orEmpty(),
                ),
            )
        }
    }

    // ─── TTL sweep ────────────────────────────────────────────────────────────

    private suspend fun ttlSweep() {
        val cutoff = nowMsProvider() - TTL_CUTOFF_MS
        // Clause 1: flip stuck-pending to dead + rollback them.
        val stuckPending = outboxDao.claimPending(nowMs = nowMsProvider(), limit = 1000)
            .filter { it.createdAtMs < cutoff }
        for (row in stuckPending) {
            markDeadAndRollback(
                row = row,
                err = com.screwy.igloo.net.IglooError.Dead(-1, "ttl_expired", "TTL exceeded 24h pending"),
            )
        }
        // Clause 2: drop dead rows older than 24h.
        val dropped = outboxDao.deleteOldDead(cutoffMs = cutoff)
        if (dropped > 0) {
            logger.debug("outbox_ttl_gc_dead", mapOf("count" to dropped.toString()))
        }
    }

    private suspend fun dropPendingDebugLogsWhenDisabled() {
        if (prefs.debugModeSync()) return
        val dropped = outboxDao.deleteAllPendingOfKind(OutboxKind.CODE_LOG_DEBUG)
        if (dropped > 0) {
            logger.info("outbox_debug_backlog_dropped", mapOf("count" to dropped))
        }
    }

    // ─── Backoff schedule ────────────────────────────────────────────────────

    /**
     * Delay before the next retry. Argument is "attempts made so far", including the
     * failure we just observed. After 1 failure, wait 30s before attempt 2;
     * after 2 failures, wait 2 min before attempt 3, etc.
     */
    private fun backoffMs(attemptsMade: Int): Long = when (attemptsMade) {
        0    -> 0L                  // defensive — shouldn't happen
        1    -> 30_000L
        2    -> 2 * 60_000L
        3    -> 10 * 60_000L
        4    -> 30 * 60_000L
        else -> 60 * 60_000L
    }

    companion object {
        private const val CLAIM_LIMIT = 100
        private const val SEEN_BATCH_LIMIT = 500
        private const val LOG_BATCH_LIMIT = 100
        private const val LOGS_INSPECTOR_CAP = 500
        private const val TTL_CUTOFF_MS = 24L * 60L * 60L * 1000L
    }
}

private fun OutboxEntity.isLogKind(): Boolean =
    kind == OutboxKind.CODE_LOG || kind == OutboxKind.CODE_LOG_DEBUG
