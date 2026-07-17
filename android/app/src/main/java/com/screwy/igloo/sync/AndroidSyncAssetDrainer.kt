package com.screwy.igloo.sync

import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.ForegroundPromoter
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.AndroidSyncHttpException
import com.screwy.igloo.net.androidSyncAssetPath
import io.ktor.client.HttpClient
import io.ktor.client.plugins.timeout
import io.ktor.client.request.prepareGet
import io.ktor.client.statement.bodyAsChannel
import io.ktor.client.statement.bodyAsText
import io.ktor.utils.io.ByteReadChannel
import io.ktor.utils.io.readAvailable
import java.io.File
import java.io.IOException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch

class AndroidSyncAssetChangedException :
    IllegalStateException("Android asset descriptor changed while downloading")

internal class AndroidSyncAssetDrainer(
    private val dao: AndroidSyncDao,
    private val client: HttpClient,
    private val baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
    private val foregroundPromoter: ForegroundPromoter,
    mediaRoot: File,
    private val logger: Logger,
    private val nowMsProvider: () -> Long,
) {
    private val syncRoot = File(mediaRoot, "sync").apply { mkdirs() }

    suspend fun drain(youtubeCutoffMs: Long) {
        var downloaded = 0
        var verifiedExisting = 0
        var deferred = 0
        var stale = false
        var promoted = false
        val claimableBeforeMs = nowMsProvider()
        try {
            while (!stale) {
                val assets =
                    dao.claimableAssets(
                        nowMs = claimableBeforeMs,
                        youtubeCutoffMs = youtubeCutoffMs,
                        limit = ASSET_CLAIM_BATCH_SIZE,
                    )
                if (assets.isEmpty()) break
                if (!promoted) {
                    foregroundPromoter.startDownloading(listOf(SYNC_DRAIN_TOKEN))
                    promoted = true
                }
                coroutineScope {
                    val work = Channel<AndroidSyncAssetEntity>(ASSET_WORKER_COUNT * 2)
                    val results = Channel<DrainResult>(ASSET_WORKER_COUNT * 2)
                    val producer =
                        launch(Dispatchers.IO) {
                            assets.forEach { work.send(it) }
                            work.close()
                        }
                    val workers =
                        List(ASSET_WORKER_COUNT) {
                            launch(Dispatchers.IO) {
                                for (asset in work) results.send(downloadOrVerify(asset))
                            }
                        }
                    val closer = launch {
                        producer.join()
                        workers.joinAll()
                        results.close()
                    }
                    for (result in results) {
                        when (result) {
                            DrainResult.Downloaded -> downloaded++
                            DrainResult.VerifiedExisting -> verifiedExisting++
                            DrainResult.Deferred -> deferred++
                            DrainResult.AssetChanged -> stale = true
                        }
                    }
                    closer.join()
                }
            }
        } finally {
            if (promoted) foregroundPromoter.finishedBatch(listOf(SYNC_DRAIN_TOKEN))
        }
        if (!promoted) return
        logger.info(
            event = "android_sync_asset_drain_done",
            fields =
                mapOf(
                    "downloaded" to downloaded,
                    "verified_existing" to verifiedExisting,
                    "deferred" to deferred,
                ),
        )
        if (stale) throw AndroidSyncAssetChangedException()
    }

    fun deleteFilesForAssets(
        assets: List<AndroidSyncAssetEntity>,
        retainedPaths: List<String>,
    ): AssetFileDeleteStats {
        if (assets.isEmpty()) return AssetFileDeleteStats()
        val root = syncRoot.absoluteFile
        if (!root.exists()) return AssetFileDeleteStats()
        val rootPath = normalizedAbsolutePath(root)
        val rootPrefix = rootPath + File.separator
        val retained =
            retainedPaths.mapNotNullTo(hashSetOf()) { path ->
                runCatching { normalizedAbsolutePath(File(path)) }.getOrNull()
            }
        val candidates = linkedSetOf<File>()
        assets.forEach { asset ->
            asset.localPath?.takeIf(String::isNotBlank)?.let { candidates += File(it) }
            val finalFile = finalFileFor(asset)
            candidates += finalFile
            candidates += File(finalFile.parentFile, finalFile.name + ".part")
        }

        var files = 0
        var bytes = 0L
        candidates.forEach { file ->
            val path = runCatching { normalizedAbsolutePath(file) }.getOrNull() ?: return@forEach
            if (
                !(path == rootPath || path.startsWith(rootPrefix)) ||
                    path in retained ||
                    !file.isFile
            )
                return@forEach
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

    private suspend fun downloadOrVerify(asset: AndroidSyncAssetEntity): DrainResult {
        val finalFile = finalFileFor(asset)
        if (finalFile.exists() && verifyFile(finalFile, asset)) {
            markVerified(asset, finalFile)
            return DrainResult.VerifiedExisting
        }
        if (finalFile.isFile) finalFile.delete()

        finalFile.parentFile?.mkdirs()
        val partFile = File(finalFile.parentFile, finalFile.name + ".part")
        partFile.delete()
        try {
            val url =
                baseUrlProvider.baseUrl().trimEnd('/') +
                    androidSyncAssetPath(asset.assetId, asset.revision)
            return client
                .prepareGet(url) {
                    timeout {
                        requestTimeoutMillis = ASSET_REQUEST_TIMEOUT_MS
                        connectTimeoutMillis = ASSET_CONNECT_TIMEOUT_MS
                        socketTimeoutMillis = ASSET_SOCKET_TIMEOUT_MS
                    }
                }
                .execute { response ->
                    when (val status = response.status.value) {
                        404 -> defer(asset, changed = true)
                        409 -> {
                            val error = AndroidSyncHttpException(
                                label = "asset:${asset.assetId}",
                                statusCode = status,
                                body = response.bodyAsText(),
                            )
                            defer(asset, changed = error.isAssetChanged)
                        }
                        408,
                        429 -> defer(asset)
                        in 500..599 -> defer(asset)
                        !in 200..299 -> defer(asset)
                        else -> {
                            copyAssetBodyToFile(response.bodyAsChannel(), partFile, asset.sizeBytes)
                            if (!verifyFile(partFile, asset)) {
                                partFile.delete()
                                defer(asset, changed = true)
                            } else {
                                Files.move(
                                    partFile.toPath(),
                                    finalFile.toPath(),
                                    StandardCopyOption.ATOMIC_MOVE,
                                    StandardCopyOption.REPLACE_EXISTING,
                                )
                                markVerified(asset, finalFile)
                                DrainResult.Downloaded
                            }
                        }
                    }
                }
        } catch (e: CancellationException) {
            partFile.delete()
            throw e
        } catch (e: Exception) {
            partFile.delete()
            if (e is IOException || e.isLikelyTransportFailure()) reachability.downgrade()
            return defer(asset)
        }
    }

    private suspend fun markVerified(asset: AndroidSyncAssetEntity, file: File) {
        val nowMs = nowMsProvider()
        if (dao.markVerified(asset.assetId, asset.revision, file.absolutePath, nowMs) == 0) {
            file.delete()
            return
        }
        if (asset.ownerKind == "youtube_video" && asset.isYoutubeVideoPrimaryBinary()) {
            dao.markOfflineYoutubeVideoDownloaded(asset.ownerId, nowMs)
        }
    }

    private suspend fun defer(asset: AndroidSyncAssetEntity, changed: Boolean = false): DrainResult {
        dao.deferAsset(asset.assetId, asset.revision, nowMsProvider() + ASSET_RETRY_MS)
        return if (changed) DrainResult.AssetChanged else DrainResult.Deferred
    }

    private suspend fun copyAssetBodyToFile(
        channel: ByteReadChannel,
        file: File,
        expectedBytes: Long,
    ) {
        var total = 0L
        val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
        file.outputStream().use { output ->
            while (true) {
                val read = channel.readAvailable(buffer, 0, buffer.size)
                if (read == -1) break
                if (read == 0) continue
                total += read
                if (total > expectedBytes) throw IOException("asset response exceeds manifest size")
                output.write(buffer, 0, read)
            }
        }
        if (total != expectedBytes) throw IOException("asset response does not match manifest size")
    }

    private fun finalFileFor(asset: AndroidSyncAssetEntity): File =
        File(
            File(syncRoot, safePathSegment(asset.bucket)),
            buildString {
                append(safePathSegment(asset.assetId))
                append("-r")
                append(asset.revision)
                append("-s")
                append(asset.sizeBytes)
                append('-')
                append(safePathSegment(asset.contentType.orEmpty()))
                append('.')
                append(extFor(asset.contentType))
            },
        )

    private fun verifyFile(file: File, asset: AndroidSyncAssetEntity): Boolean =
        file.isFile && file.length() == asset.sizeBytes

    private sealed interface DrainResult {
        data object VerifiedExisting : DrainResult

        data object Downloaded : DrainResult

        data object Deferred : DrainResult

        data object AssetChanged : DrainResult
    }

    private companion object {
        const val ASSET_WORKER_COUNT = 4
        const val ASSET_CLAIM_BATCH_SIZE = 64
        const val ASSET_REQUEST_TIMEOUT_MS = 15 * 60 * 1000L
        const val ASSET_CONNECT_TIMEOUT_MS = 30 * 1000L
        const val ASSET_SOCKET_TIMEOUT_MS = 2 * 60 * 1000L
        const val ASSET_RETRY_MS = 30_000L
        const val SYNC_DRAIN_TOKEN = "__android_sync_drain__"

        fun extFor(contentType: String?): String =
            when (contentType?.substringBefore(";")?.trim()?.lowercase()) {
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
            raw.map { c -> if (c.isLetterOrDigit() || c == '.' || c == '_' || c == '-') c else '_' }
                .joinToString("")
    }
}

internal data class AssetFileDeleteStats(val files: Int = 0, val bytes: Long = 0)
