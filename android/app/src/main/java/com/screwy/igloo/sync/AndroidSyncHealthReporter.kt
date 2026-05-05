package com.screwy.igloo.sync

import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.AndroidSyncApi
import com.screwy.igloo.net.AndroidSyncHealthBytePayload
import com.screwy.igloo.net.AndroidSyncHealthCountPayload
import com.screwy.igloo.net.AndroidSyncHealthRequest
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import kotlinx.coroutines.CancellationException

internal class AndroidSyncHealthReporter(
    private val dao: AndroidSyncDao,
    private val api: AndroidSyncApi,
    private val reachability: Reachability,
    private val logger: Logger,
    private val nowMsProvider: () -> Long,
) {
    suspend fun report(generationId: String, retention: AndroidSyncRetentionRequest): Boolean {
        val counts = dao.healthCounts(generationId)
        val req = AndroidSyncHealthRequest(
            generation_id = generationId,
            reported_at_ms = nowMsProvider(),
            retention = retention,
            counts = AndroidSyncHealthCountPayload(
                total = counts.total,
                verified = counts.verified,
                pending = counts.pending,
                failed = counts.failed,
                missing = counts.missing,
            ),
            bytes = AndroidSyncHealthBytePayload(verified = counts.verifiedBytes),
        )
        val uploaded = try {
            api.health(req).status.value in 200..299
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (e.isLikelyTransportFailure()) reachability.downgrade()
            false
        }
        logger.info(
            event = "android_sync_health_reported",
            fields = mapOf(
                "generation_id" to generationId,
                "uploaded" to uploaded,
                "verified" to counts.verified,
                "pending" to counts.pending,
                "failed" to counts.failed,
                "missing" to counts.missing,
                "total" to counts.total,
                "verified_bytes" to counts.verifiedBytes,
            ),
        )
        return uploaded
    }
}
