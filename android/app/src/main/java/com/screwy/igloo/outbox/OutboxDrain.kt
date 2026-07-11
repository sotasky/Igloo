package com.screwy.igloo.outbox

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.IglooError
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.iglooJson
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject

data class OutboxPassResult(
    val reconcileMutations: Boolean = false,
    val protectionChanged: Boolean = false,
)

class OutboxDrain(
    private val outboxDao: OutboxDao,
    private val dispatcher: OutboxDispatcher,
    private val db: IglooDatabase,
    private val prefs: PreferencesRepo,
    private val reachability: Reachability,
    private val logger: Logger,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) {
    suspend fun runOnce(): OutboxPassResult {
        dropPendingDebugLogsWhenDisabled()
        if (reachability.state.value is Reachability.State.Offline) return OutboxPassResult()

        var reconcile = false
        var protectionChanged = false
        var authRefreshRequired = false
        while (true) {
            val nowMs = nowMsProvider()
            val batch = outboxDao.claimPending(nowMs, CLAIM_LIMIT)
            if (batch.isEmpty()) break
            for (group in groupForDispatch(batch, nowMs)) {
                val results = dispatcher.dispatch(group)
                val acked = group.filter { results[it.id] == OutboxDispatcher.Result.Ack }
                applyAcks(acked)
                if (acked.any(OutboxEntity::isProtectionClear)) protectionChanged = true
                for (row in group) {
                    when (val result = results[row.id] ?: continue) {
                        OutboxDispatcher.Result.Ack -> Unit
                        OutboxDispatcher.Result.Reconcile -> {
                            outboxDao.completeAndDelete(row.id)
                            reconcile = true
                        }
                        is OutboxDispatcher.Result.Retry -> scheduleRetry(row, result.error)
                        is OutboxDispatcher.Result.Dead -> {
                            discardRejected(row, result.error)
                            if (!row.isClear()) reconcile = true
                        }
                        OutboxDispatcher.Result.AuthRefresh -> authRefreshRequired = true
                    }
                }
                if (authRefreshRequired) break
            }
            if (authRefreshRequired) break
            if (batch.size < CLAIM_LIMIT) break
        }
        return OutboxPassResult(reconcile, protectionChanged)
    }

    private suspend fun groupForDispatch(
        firstClaim: List<OutboxEntity>,
        nowMs: Long,
    ): List<List<OutboxEntity>> {
        val seenBatch =
            if (firstClaim.any { it.kind == OutboxKind.CODE_SEEN }) {
                outboxDao.claimKind(OutboxKind.CODE_SEEN, nowMs, SEEN_BATCH_LIMIT)
            } else emptyList()
        val logBatch =
            if (firstClaim.any { it.kind == OutboxKind.CODE_LOG }) {
                outboxDao.claimKind(OutboxKind.CODE_LOG, nowMs, LOG_BATCH_LIMIT)
            } else emptyList()
        val debugBatch =
            if (firstClaim.any { it.kind == OutboxKind.CODE_LOG_DEBUG }) {
                outboxDao.claimKind(OutboxKind.CODE_LOG_DEBUG, nowMs, LOG_BATCH_LIMIT)
            } else emptyList()
        val batched = (seenBatch + logBatch + debugBatch).mapTo(hashSetOf()) { it.id }
        return buildList {
            if (seenBatch.isNotEmpty()) add(seenBatch)
            if (logBatch.isNotEmpty()) add(logBatch)
            if (debugBatch.isNotEmpty()) add(debugBatch)
            firstClaim.filter { it.id !in batched }.forEach { add(listOf(it)) }
        }
    }

    private suspend fun applyAcks(rows: List<OutboxEntity>) {
        if (rows.isEmpty()) return
        db.withTransaction {
            rows.filter(OutboxEntity::isClear).forEach { finalizeClear(it) }
            rows
                .filterNot(OutboxEntity::isLogKind)
                .map(OutboxEntity::id)
                .takeIf(List<Long>::isNotEmpty)
                ?.let { outboxDao.completeAndDeleteAll(it) }
            rows
                .filter(OutboxEntity::isLogKind)
                .map(OutboxEntity::id)
                .takeIf(List<Long>::isNotEmpty)
                ?.let {
                    outboxDao.markAcked(it)
                    outboxDao.trimAckedLogs(LOGS_INSPECTOR_CAP)
                }
        }
    }

    private suspend fun finalizeClear(row: OutboxEntity) {
        val id = row.itemId ?: return
        when (row.kind) {
            OutboxKind.CODE_LIKE -> db.feedLikeDao().delete(id)
            OutboxKind.CODE_BOOKMARK -> db.bookmarkDao().delete(id)
            OutboxKind.CODE_FOLLOW -> db.channelFollowDao().delete(id)
            OutboxKind.CODE_STAR -> db.channelStarDao().delete(id)
            OutboxKind.CODE_MUTE -> db.mutedChannelDao().delete(id)
        }
    }

    private suspend fun scheduleRetry(row: OutboxEntity, error: IglooError) {
        val attempts = row.attemptCount + 1
        outboxDao.markPending(
            id = row.id,
            attemptCount = attempts,
            nextAttemptAtMs = nowMsProvider() + backoffMs(attempts),
            errorCode = error.status,
            errorBody = error.errorMessage?.take(200),
        )
    }

    private suspend fun discardRejected(row: OutboxEntity, error: IglooError) {
        db.withTransaction {
            outboxDao.completeAndDelete(row.id)
        }
        if (!row.isLogKind()) {
            logger.error(
                event = "outbox_row_dead",
                fields =
                    mapOf(
                        "id" to row.id.toString(),
                        "kind" to row.kind,
                        "status" to (error.status?.toString() ?: "?"),
                        "code" to error.errorCode.orEmpty(),
                    ),
            )
        }
    }

    private suspend fun dropPendingDebugLogsWhenDisabled() {
        if (!prefs.debugModeSync()) outboxDao.deleteAllPendingOfKind(OutboxKind.CODE_LOG_DEBUG)
    }

    private fun backoffMs(attempts: Int): Long =
        when (attempts) {
            1 -> 30_000L
            2 -> 2 * 60_000L
            3 -> 10 * 60_000L
            4 -> 30 * 60_000L
            else -> 60 * 60_000L
        }

    private companion object {
        const val CLAIM_LIMIT = 100
        const val SEEN_BATCH_LIMIT = 500
        const val LOG_BATCH_LIMIT = 100
        const val LOGS_INSPECTOR_CAP = 500
    }
}

private fun OutboxEntity.payload(): JsonObject =
    runCatching { iglooJson.parseToJsonElement(payloadJson).jsonObject }
        .getOrDefault(JsonObject(emptyMap()))

private fun OutboxEntity.isClear(): Boolean =
    (payload()["action"] as? JsonPrimitive)?.contentOrNull == "clear"

private fun OutboxEntity.isProtectionClear(): Boolean =
    isClear() &&
        kind in
            setOf(
                OutboxKind.CODE_LIKE,
                OutboxKind.CODE_BOOKMARK,
                OutboxKind.CODE_FOLLOW,
                OutboxKind.CODE_STAR,
                OutboxKind.CODE_MUTE,
            )

private fun OutboxEntity.isLogKind(): Boolean =
    kind == OutboxKind.CODE_LOG || kind == OutboxKind.CODE_LOG_DEBUG
