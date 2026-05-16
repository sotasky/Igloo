package com.screwy.igloo.sync

import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.ForegroundPromoter
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import io.ktor.client.HttpClient
import io.ktor.client.plugins.timeout
import io.ktor.client.request.prepareGet
import io.ktor.client.statement.bodyAsChannel
import io.ktor.utils.io.ByteReadChannel
import io.ktor.utils.io.readAvailable
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch
import java.io.File
import java.io.IOException
import java.security.MessageDigest

class AndroidSyncStaleGenerationException(val generationId: String) :
    IllegalStateException("Android sync generation $generationId has stale asset bytes; request latest generation")

internal class AndroidSyncAssetDrainer(
    private val dao: AndroidSyncDao,
    private val client: HttpClient,
    private val baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
    private val foregroundPromoter: ForegroundPromoter,
    mediaRoot: File,
    private val logger: Logger,
    private val healthReporter: AndroidSyncHealthReporter,
    private val nowMsProvider: () -> Long,
) {
    private val syncRoot: File = File(mediaRoot, "sync").apply { mkdirs() }

    suspend fun drain(generationId: String, retention: AndroidSyncRetentionRequest) {
        var downloaded = 0
        var verifiedExisting = 0
        var deferred = 0
        var healthUploadFailures = 0
        var promoted = false
        var completedSinceHealth = 0
        var staleGeneration = false
        dao.resetDownloading(generationId = generationId, nowMs = nowMsProvider())
        try {
            coroutineScope {
                val work = Channel<AndroidSyncAssetEntity>(capacity = ASSET_WORKER_COUNT * 2)
                val results = Channel<DrainResult>(capacity = ASSET_WORKER_COUNT * 2)

                val producer = launch(Dispatchers.IO) {
                    try {
                        while (true) {
                            val batch = dao.claimableAssets(generationId, nowMsProvider(), ASSET_CLAIM_BATCH_SIZE)
                            if (batch.isEmpty()) break
                            if (!promoted) {
                                foregroundPromoter.startDownloading(listOf(SYNC_DRAIN_TOKEN))
                                promoted = true
                            }
                            for (asset in batch) {
                                dao.markDownloading(asset.generationId, asset.assetId, asset.assetKind, nowMsProvider())
                                work.send(asset)
                            }
                        }
                    } finally {
                        work.close()
                    }
                }

                val workers = List(ASSET_WORKER_COUNT) {
                    launch(Dispatchers.IO) {
                        for (asset in work) {
                            results.send(downloadOrVerify(asset))
                        }
                    }
                }

                val closer = launch {
                    producer.join()
                    workers.joinAll()
                    results.close()
                }

                for (result in results) {
                    when (result) {
                        DrainResult.VerifiedExisting -> verifiedExisting++
                        DrainResult.Downloaded -> downloaded++
                        DrainResult.Deferred -> deferred++
                        DrainResult.StaleGeneration -> staleGeneration = true
                        DrainResult.Failed -> Unit
                    }
                    completedSinceHealth++
                    if (completedSinceHealth >= HEALTH_REPORT_EVERY_ASSETS) {
                        completedSinceHealth = 0
                        val uploaded = healthReporter.report(generationId, retention)
                        if (!uploaded) {
                            healthUploadFailures++
                            logger.info(
                                event = "android_sync_health_upload_failed_continuing",
                                fields = mapOf(
                                    "generation_id" to generationId,
                                    "downloaded" to downloaded,
                                    "verified_existing" to verifiedExisting,
                                    "deferred" to deferred,
                                    "pending" to dao.countPending(generationId),
                                    "health_upload_failures" to healthUploadFailures,
                                ),
                            )
                        }
                    }
                }
                closer.join()
            }
        } finally {
            if (promoted) {
                foregroundPromoter.finishedBatch(listOf(SYNC_DRAIN_TOKEN))
            }
        }
        if (completedSinceHealth > 0) {
            val uploaded = healthReporter.report(generationId, retention)
            if (!uploaded) healthUploadFailures++
        }
        logger.info(
            event = "android_sync_asset_drain_done",
            fields = mapOf(
                "generation_id" to generationId,
                "downloaded" to downloaded,
                "verified_existing" to verifiedExisting,
                "deferred" to deferred,
                "pending" to dao.countPending(generationId),
                "health_upload_failures" to healthUploadFailures,
                "stale_generation" to staleGeneration,
            ),
        )
        if (staleGeneration) {
            logger.info(
                event = "android_sync_generation_stale_assets",
                fields = mapOf("generation_id" to generationId),
            )
            throw AndroidSyncStaleGenerationException(generationId)
        }
    }

    fun deleteUnreferencedAssetFiles(retainedPaths: List<String>): AssetFileDeleteStats {
        val root = syncRoot.absoluteFile
        if (!root.exists()) return AssetFileDeleteStats()
        val rootPath = normalizedAbsolutePath(root)
        val rootPrefix = rootPath + File.separator
        val retained = retainedPaths.mapNotNull { path ->
            runCatching { normalizedAbsolutePath(File(path)) }.getOrNull()
        }.filter { it.isUnder(rootPath, rootPrefix) }.toHashSet()

        var files = 0
        var bytes = 0L
        root.walkTopDown().forEach { file ->
            if (!file.isFile) return@forEach
            val path = runCatching { normalizedAbsolutePath(file) }.getOrNull() ?: return@forEach
            if (!path.startsWith(rootPrefix) || path in retained) return@forEach
            val size = file.length()
            if (file.delete()) {
                files++
                bytes += size
            }
        }
        return AssetFileDeleteStats(files, bytes)
    }

    private fun normalizedAbsolutePath(file: File): String =
        file.absoluteFile.toPath().normalize().toString()

    private fun String.isUnder(rootPath: String, rootPrefix: String): Boolean =
        this == rootPath || startsWith(rootPrefix)

    private suspend fun downloadOrVerify(asset: AndroidSyncAssetEntity): DrainResult {
        val maxDownloadBytes = maxDownloadBytesFor(asset)
        if (maxDownloadBytes == null) {
            markAssetFailed(asset, "missing_integrity_metadata")
            return DrainResult.Failed
        }

        val finalFile = finalFileFor(asset)
        if (finalFile.exists() && verifyFile(finalFile, asset)) {
            dao.markVerified(
                asset.generationId,
                asset.assetId,
                asset.assetKind,
                finalFile.absolutePath,
                finalFile.length(),
                nowMsProvider(),
            )
            return DrainResult.VerifiedExisting
        }

        finalFile.parentFile?.mkdirs()
        val partFile = File(finalFile.parentFile, finalFile.name + ".part")
        partFile.delete()
        try {
            val result = client.prepareGet(baseUrlProvider.baseUrl() + asset.serverUrl) {
                timeout {
                    requestTimeoutMillis = ASSET_REQUEST_TIMEOUT_MS
                    connectTimeoutMillis = ASSET_CONNECT_TIMEOUT_MS
                    socketTimeoutMillis = ASSET_SOCKET_TIMEOUT_MS
                }
            }.execute { response ->
                val status = response.status.value
                when {
                    status == 404 -> {
                        dao.markServerMissing(asset.generationId, asset.assetId, asset.assetKind, "404", nowMsProvider())
                        DrainResult.Failed
                    }
                    status == 408 || status == 429 || status >= 500 -> {
                        deferAsset(asset, "http_$status")
                        DrainResult.Deferred
                    }
                    status == 409 -> {
                        deferStaleAsset(asset, "stale_generation_asset_changed")
                    }
                    status !in 200..299 -> {
                        markAssetFailed(asset, "http_$status")
                        DrainResult.Failed
                    }
                    else -> {
                        copyAssetBodyToFile(response.bodyAsChannel(), partFile, maxDownloadBytes)
                        if (!verifyFile(partFile, asset)) {
                            partFile.delete()
                            if (asset.serverUrl.isGenerationScopedAssetUrl()) {
                                deferStaleAsset(asset, "stale_generation_asset_verify_failed")
                            } else {
                                markAssetFailed(asset, "verify_failed")
                                DrainResult.Failed
                            }
                        } else {
                            if (finalFile.exists()) finalFile.delete()
                            if (!partFile.renameTo(finalFile)) {
                                partFile.delete()
                                markAssetFailed(asset, "rename_failed")
                                DrainResult.Failed
                            } else {
                                dao.markVerified(
                                    asset.generationId,
                                    asset.assetId,
                                    asset.assetKind,
                                    finalFile.absolutePath,
                                    finalFile.length(),
                                    nowMsProvider(),
                                )
                                logger.debug(
                                    event = "android_sync_asset_verified",
                                    fields = mapOf(
                                        "generation_id" to asset.generationId,
                                        "asset_id" to asset.assetId,
                                        "bytes" to finalFile.length(),
                                    ),
                                )
                                DrainResult.Downloaded
                            }
                        }
                    }
                }
            }
            return result
        } catch (e: CancellationException) {
            partFile.delete()
            throw e
        } catch (e: AssetTooLargeException) {
            partFile.delete()
            markAssetFailed(asset, "response_too_large")
            return DrainResult.Failed
        } catch (e: IOException) {
            partFile.delete()
            reachability.downgrade()
            deferAsset(asset, e.message?.take(120) ?: "io_error")
            return DrainResult.Deferred
        } catch (e: Exception) {
            partFile.delete()
            val error = e.message?.take(120) ?: e::class.simpleName.orEmpty()
            if (e.isLikelyTransportFailure()) {
                reachability.downgrade()
                deferAsset(asset, error)
                return DrainResult.Deferred
            }
            markAssetFailed(asset, error)
        }
        return DrainResult.Failed
    }

    private fun maxDownloadBytesFor(asset: AndroidSyncAssetEntity): Long? {
        if (asset.sizeBytes > 0) return asset.sizeBytes
        if (!asset.sha256.isNullOrBlank()) return UNKNOWN_SIZE_ASSET_MAX_BYTES
        return null
    }

    private suspend fun copyAssetBodyToFile(channel: ByteReadChannel, file: File, maxBytes: Long) {
        var total = 0L
        val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
        file.outputStream().use { output ->
            while (true) {
                val read = channel.readAvailable(buffer, 0, buffer.size)
                if (read == -1) break
                if (read == 0) continue
                total += read
                if (total > maxBytes) throw AssetTooLargeException(maxBytes)
                output.write(buffer, 0, read)
            }
        }
    }

    private class AssetTooLargeException(val maxBytes: Long) :
        IOException("asset response exceeded $maxBytes bytes")

    private suspend fun markAssetFailed(asset: AndroidSyncAssetEntity, error: String) {
        val next = nowMsProvider() + backoffMs(asset.attemptCount + 1)
        dao.markFailed(asset.generationId, asset.assetId, asset.assetKind, next, error, nowMsProvider())
        logger.info(
            event = "android_sync_asset_failed",
            fields = mapOf(
                "generation_id" to asset.generationId,
                "asset_id" to asset.assetId,
                "asset_kind" to asset.assetKind,
                "owner_id" to asset.ownerId,
                "owner_kind" to asset.ownerKind,
                "error" to error,
            ),
        )
    }

    private suspend fun deferAsset(asset: AndroidSyncAssetEntity, error: String) {
        val next = nowMsProvider() + TRANSIENT_ASSET_RETRY_MS
        dao.deferAsset(asset.generationId, asset.assetId, asset.assetKind, next, error, nowMsProvider())
    }

    private suspend fun deferStaleAsset(asset: AndroidSyncAssetEntity, reason: String): DrainResult {
        deferAsset(asset, reason)
        logger.info(
            event = "android_sync_asset_stale",
            fields = mapOf(
                "generation_id" to asset.generationId,
                "asset_id" to asset.assetId,
                "asset_kind" to asset.assetKind,
                "owner_id" to asset.ownerId,
                "owner_kind" to asset.ownerKind,
                "reason" to reason,
            ),
        )
        return DrainResult.StaleGeneration
    }

    private fun finalFileFor(asset: AndroidSyncAssetEntity): File {
        val bucketDir = File(syncRoot, safePathSegment(asset.bucket)).apply { mkdirs() }
        return File(bucketDir, safePathSegment(asset.assetId) + "." + extFor(asset.contentType))
    }

    private fun verifyFile(file: File, asset: AndroidSyncAssetEntity): Boolean {
        if (!file.exists() || !file.isFile) return false
        if (asset.sizeBytes > 0 && file.length() != asset.sizeBytes) return false
        val expected = asset.sha256?.takeIf { it.isNotBlank() } ?: return true
        return sha256Hex(file).equals(expected, ignoreCase = true)
    }

    private fun String.isGenerationScopedAssetUrl(): Boolean =
        startsWith("/api/android/sync/generation/") && contains("/assets/")

    private sealed interface DrainResult {
        data object VerifiedExisting : DrainResult
        data object Downloaded : DrainResult
        data object Deferred : DrainResult
        data object StaleGeneration : DrainResult
        data object Failed : DrainResult
    }

    private companion object {
        const val ASSET_WORKER_COUNT = 32
        const val ASSET_CLAIM_BATCH_SIZE = ASSET_WORKER_COUNT
        const val HEALTH_REPORT_EVERY_ASSETS = ASSET_WORKER_COUNT
        const val ASSET_REQUEST_TIMEOUT_MS = 15 * 60 * 1000L
        const val ASSET_CONNECT_TIMEOUT_MS = 30 * 1000L
        const val ASSET_SOCKET_TIMEOUT_MS = 2 * 60 * 1000L
        const val UNKNOWN_SIZE_ASSET_MAX_BYTES = 512L * 1024L * 1024L
        const val TRANSIENT_ASSET_RETRY_MS = 30_000L
        const val SYNC_DRAIN_TOKEN = "__android_sync_drain__"

        fun backoffMs(attemptsMade: Int): Long = when (attemptsMade) {
            0 -> 0L
            1 -> 30_000L
            2 -> 2 * 60_000L
            3 -> 10 * 60_000L
            4 -> 30 * 60_000L
            else -> 60 * 60_000L
        }

        fun extFor(contentType: String?): String = when (contentType?.substringBefore(";")?.trim()?.lowercase()) {
            "image/jpeg" -> "jpg"
            "image/png" -> "png"
            "image/webp" -> "webp"
            "image/gif" -> "gif"
            "video/mp4" -> "mp4"
            "video/webm" -> "webm"
            "video/x-matroska" -> "mkv"
            "video/quicktime" -> "mov"
            "audio/mpeg" -> "mp3"
            "audio/mp4" -> "m4a"
            "audio/aac" -> "aac"
            "audio/ogg" -> "ogg"
            "text/vtt" -> "vtt"
            else -> "bin"
        }

        fun safePathSegment(raw: String): String =
            raw.map { c -> if (c.isLetterOrDigit() || c == '.' || c == '_' || c == '-') c else '_' }.joinToString("")

        fun sha256Hex(file: File): String {
            val digest = MessageDigest.getInstance("SHA-256")
            file.inputStream().use { input ->
                val buf = ByteArray(DEFAULT_BUFFER_SIZE)
                while (true) {
                    val read = input.read(buf)
                    if (read <= 0) break
                    digest.update(buf, 0, read)
                }
            }
            return digest.digest().joinToString("") { "%02x".format(it) }
        }
    }
}

internal data class AssetFileDeleteStats(
    val files: Int = 0,
    val bytes: Long = 0,
)
