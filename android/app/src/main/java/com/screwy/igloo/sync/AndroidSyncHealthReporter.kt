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
import java.util.concurrent.TimeUnit

internal class AndroidSyncHealthReporter(
    private val dao: AndroidSyncDao,
    private val api: AndroidSyncApi,
    private val reachability: Reachability,
    private val logger: Logger,
    private val nowMsProvider: () -> Long,
) {
    suspend fun report(cursor: String, retention: AndroidSyncRetentionRequest): Boolean {
        val counts = dao.healthCounts()
        val req = AndroidSyncHealthRequest(
            cursor = cursor,
            reported_at_ms = nowMsProvider(),
            retention = retention,
            counts = AndroidSyncHealthCountPayload(
                total = counts.total,
                verified = counts.verified,
                pending = counts.pending,
                missing = counts.missing,
            ),
            bytes = AndroidSyncHealthBytePayload(verified = counts.verifiedBytes),
        )
        val uploadStartedAt = System.nanoTime()
        val uploaded = try {
            api.health(req).status.value in 200..299
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (e.isLikelyTransportFailure()) reachability.downgrade()
            false
        }
        val uploadElapsedMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - uploadStartedAt)
        logger.info(
            event = "android_sync_health_reported",
            fields = mapOf(
                "cursor" to cursor,
                "uploaded" to uploaded,
                "upload_elapsed_ms" to uploadElapsedMs,
                "verified" to counts.verified,
                "pending" to counts.pending,
                "missing" to counts.missing,
                "total" to counts.total,
                "verified_bytes" to counts.verifiedBytes,
            ),
        )
        return uploaded
    }
}
