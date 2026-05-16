package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.dao.AndroidSyncContentPruneCounts
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncGenerationEntity
import com.screwy.igloo.data.entity.AndroidSyncItemEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.AndroidSyncApi
import com.screwy.igloo.net.AndroidSyncAssetDto
import com.screwy.igloo.net.AndroidSyncDecodeException
import com.screwy.igloo.net.AndroidSyncGenerationDto
import com.screwy.igloo.net.AndroidSyncHttpException
import com.screwy.igloo.net.AndroidSyncItemDto
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.media.ForegroundPromoter
import io.ktor.client.HttpClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.File
import java.io.IOException
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

private val androidSyncJson: Json = Json {
    ignoreUnknownKeys = true
    coerceInputValues = true
    isLenient = true
    encodeDefaults = true
}

const val ANDROID_SYNC_ITEM_IMPORTER_VERSION = 3

private fun AndroidSyncContentPruneCounts.hasDeletes(): Boolean =
    videos > 0 || feedItems > 0 || channels > 0 || channelProfiles > 0 || legacyAssets > 0 || sideRows > 0

private fun AssetFileDeleteStats.hasDeletes(): Boolean = files > 0

/**
 * Android sync mirror runner. This imports the server-owned immutable generation,
 * applies content bundles through the same BundleIngest path, downloads every
 * ready asset into an Android sync ledger, verifies exact size/hash, then reports
 * generation coverage.
 */
class AndroidSyncMirror(
    private val db: IglooDatabase,
    private val dao: AndroidSyncDao,
    private val outboxDao: OutboxDao,
    private val api: AndroidSyncApi,
    client: HttpClient,
    baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
    private val foregroundPromoter: ForegroundPromoter,
    mediaRoot: File,
    private val logger: Logger,
    private val retentionProvider: suspend () -> AndroidSyncRetentionRequest = {
        AndroidSyncRetentionRequest(feedDays = 7, youtubeDays = 7, momentsDays = 7, storyHours = 48)
    },
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) {

    private val triggerChannel = Channel<Unit>(capacity = Channel.CONFLATED)
    private val healthReporter = AndroidSyncHealthReporter(
        dao = dao,
        api = api,
        reachability = reachability,
        logger = logger,
        nowMsProvider = nowMsProvider,
    )
    private val assetDrainer = AndroidSyncAssetDrainer(
        dao = dao,
        client = client,
        baseUrlProvider = baseUrlProvider,
        reachability = reachability,
        foregroundPromoter = foregroundPromoter,
        mediaRoot = mediaRoot,
        logger = logger,
        healthReporter = healthReporter,
        nowMsProvider = nowMsProvider,
    )
    private val triggerSeq = AtomicLong(0L)
    private val completedTriggerSeq = AtomicLong(0L)
    private val syncActive = AtomicBoolean(false)

    fun trigger() {
        triggerSeq.incrementAndGet()
        triggerChannel.trySend(Unit)
    }

    fun hasPendingOrActiveWork(): Boolean =
        syncActive.get() || completedTriggerSeq.get() < triggerSeq.get()

    suspend fun run() {
        while (true) {
            triggerChannel.receive()
            val observedTriggerSeq = triggerSeq.get()
            syncActive.set(true)
            try {
                syncOnce()
                completedTriggerSeq.set(maxOf(completedTriggerSeq.get(), observedTriggerSeq))
            } catch (e: CancellationException) {
                throw e
            } catch (e: AndroidSyncStaleGenerationException) {
                logger.info(
                    event = "android_sync_generation_stale_retry",
                    fields = mapOf("generation_id" to e.generationId),
                )
                delay(SYNC_FAILURE_RETRY_MS)
                trigger()
            } catch (e: Exception) {
                logger.error(
                    event = "android_sync_unhandled",
                    fields = mapOf("error" to (e.message ?: e::class.simpleName.orEmpty())),
                    throwable = e,
                )
                delay(SYNC_FAILURE_RETRY_MS)
                trigger()
            } finally {
                syncActive.set(false)
            }
        }
    }

    suspend fun syncOnce() {
        val startedAt = nowMsProvider()
        foregroundPromoter.startActiveDrain()
        try {
            logger.info(
                event = "android_sync_generation_request",
                fields = mapOf("reachability" to reachability.state.value::class.simpleName.orEmpty()),
            )
            val retention = retentionProvider()
            val latest = withMetadataRetry("latest_generation") {
                api.latestGeneration(retention)
            }
            val generation = latest.generation
            dao.upsertGeneration(generation.toEntity())
            logger.info(
                event = "android_sync_generation_start",
                fields = mapOf(
                    "generation_id" to generation.generation_id,
                    "items" to generation.item_count,
                    "assets" to generation.asset_count,
                    "ready_assets" to generation.ready_asset_count,
                    "server_missing_assets" to generation.server_missing_asset_count,
                ),
            )

            importAssets(generation.generation_id)
            healthReporter.report(generation.generation_id, retention)
            coroutineScope {
                val itemImport = async(Dispatchers.IO) { importItems(generation.generation_id) }
                val assetDrain = async(Dispatchers.IO) { assetDrainer.drain(generation.generation_id, retention) }
                itemImport.await()
                assetDrain.await()
            }
            healthReporter.report(generation.generation_id, retention)
            val contentPruned = dao.pruneContentOutsideGeneration(generation.generation_id)
            if (contentPruned.hasDeletes()) {
                logger.info(
                    event = "android_sync_content_pruned",
                    fields = mapOf(
                        "generation_id" to generation.generation_id,
                        "videos" to contentPruned.videos,
                        "feed_items" to contentPruned.feedItems,
                        "channels" to contentPruned.channels,
                        "channel_profiles" to contentPruned.channelProfiles,
                        "legacy_assets" to contentPruned.legacyAssets,
                        "side_rows" to contentPruned.sideRows,
                    ),
                )
            }
            val orphanAssetFiles = assetDrainer.deleteUnreferencedAssetFiles(
                dao.verifiedLocalPaths(generation.generation_id),
            )
            if (orphanAssetFiles.hasDeletes()) {
                logger.info(
                    event = "android_sync_orphan_asset_files_pruned",
                    fields = mapOf(
                        "generation_id" to generation.generation_id,
                        "files" to orphanAssetFiles.files,
                        "bytes" to orphanAssetFiles.bytes,
                    ),
                )
            }
            val prunedGenerations = dao.pruneGenerationsExcept(generation.generation_id)
            if (prunedGenerations > 0) {
                logger.info(
                    event = "android_sync_generations_pruned",
                    fields = mapOf(
                        "generation_id" to generation.generation_id,
                        "pruned_generations" to prunedGenerations,
                    ),
                )
            }

            logger.info(
                event = "android_sync_generation_done",
                fields = mapOf(
                    "generation_id" to generation.generation_id,
                    "duration_ms" to (nowMsProvider() - startedAt),
                ),
            )
            if (latest.refreshing) {
                logger.info(
                    event = "android_sync_generation_refresh_pending",
                    fields = mapOf("generation_id" to generation.generation_id),
                )
                trigger()
            }
        } finally {
            foregroundPromoter.finishActiveDrain()
        }
    }

    private suspend fun importItems(generationId: String) {
        if (dao.countItemsImportCompleteForImporter(generationId, ANDROID_SYNC_ITEM_IMPORTER_VERSION) > 0) {
            logger.info(
                event = "android_sync_items_import_skipped",
                fields = mapOf(
                    "generation_id" to generationId,
                    "reason" to "already_imported",
                    "importer_version" to ANDROID_SYNC_ITEM_IMPORTER_VERSION,
                ),
            )
            return
        }
        val ingest = BundleIngest(db, nowMsProvider)
        val guard = PreserveLocalGuard(outboxDao)
        val storedImporterVersion = dao.itemImporterVersion(generationId) ?: 0
        val resumeAfter = if (storedImporterVersion == ANDROID_SYNC_ITEM_IMPORTER_VERSION) {
            dao.maxImportedItemSeq(generationId).takeIf { it > 0L }
        } else {
            null
        }
        var after: String? = resumeAfter?.toString()
        var total = resumeAfter?.toInt() ?: 0
        if (resumeAfter != null) {
            logger.info(
                event = "android_sync_items_resume",
                fields = mapOf("generation_id" to generationId, "after" to resumeAfter),
            )
        }
        while (true) {
            val page = withMetadataRetry("items", "generation_id" to generationId, "after" to (after ?: "")) {
                api.items(generationId, after)
            }
            if (page.items.isNotEmpty()) {
                dao.upsertItems(page.items.map { it.toEntity(generationId) })
                val changedSeqs = dao.changedItemSeqsFromPreviousImportedGenerations(
                    generationId = generationId,
                    afterSeq = after?.toLongOrNull() ?: 0L,
                    toSeq = page.items.maxOf { it.seq },
                    importerVersion = ANDROID_SYNC_ITEM_IMPORTER_VERSION,
                ).toSet()
                val skippedUnchanged = page.items.size - changedSeqs.size
                for (item in page.items) {
                    if (item.seq !in changedSeqs) continue
                    val result = ingest.ingest(item.payload, guard)
                    if (result is IngestResult.ParseFailure) {
                        logger.info(
                            event = "android_sync_item_parse_failed",
                            fields = mapOf(
                                "generation_id" to generationId,
                                "kind" to item.item_kind,
                                "item_id" to item.item_id,
                                "error" to (result.cause.message ?: result.cause::class.simpleName.orEmpty()),
                            ),
                        )
                    }
                }
                if (skippedUnchanged > 0) {
                    logger.info(
                        event = "android_sync_items_unchanged_skipped",
                        fields = mapOf(
                            "generation_id" to generationId,
                            "after" to (after ?: ""),
                            "skipped" to skippedUnchanged,
                            "changed" to changedSeqs.size,
                        ),
                    )
                }
                total += page.items.size
            }
            logger.info(
                event = "android_sync_items_page",
                fields = mapOf(
                    "generation_id" to generationId,
                    "after" to (after ?: ""),
                    "count" to page.items.size,
                    "total" to total,
                    "next" to page.next,
                    "end" to page.end_of_stream,
                ),
            )
            if (page.end_of_stream) break
            if (page.next.isBlank() || page.next == after) {
                logger.info(
                    event = "android_sync_items_marker_stalled",
                    fields = mapOf("generation_id" to generationId, "next" to page.next),
                )
                throw IllegalStateException("Android sync items marker stalled for generation $generationId")
            }
            after = page.next
        }
        dao.markItemsImported(generationId, nowMsProvider(), ANDROID_SYNC_ITEM_IMPORTER_VERSION)
        logger.info(
            event = "android_sync_items_imported",
            fields = mapOf(
                "generation_id" to generationId,
                "count" to total,
                "importer_version" to ANDROID_SYNC_ITEM_IMPORTER_VERSION,
            ),
        )
    }

    private suspend fun importAssets(generationId: String) {
        if (dao.countAssetsImportCompleteForCurrentContract(generationId) > 0) {
            logger.info(
                event = "android_sync_assets_import_skipped",
                fields = mapOf("generation_id" to generationId, "reason" to "already_imported"),
            )
            return
        }
        val refreshCompleteImport = dao.countAssetsImportComplete(generationId) > 0
        val resumeAfter = if (refreshCompleteImport) null else dao.maxImportedAssetSeq(generationId).takeIf { it > 0L }
        var after: String? = resumeAfter?.toString()
        var total = resumeAfter?.toInt() ?: 0
        if (refreshCompleteImport) {
            logger.info(
                event = "android_sync_assets_contract_refresh",
                fields = mapOf("generation_id" to generationId),
            )
        }
        if (resumeAfter != null) {
            logger.info(
                event = "android_sync_assets_resume",
                fields = mapOf("generation_id" to generationId, "after" to resumeAfter),
            )
        }
        while (true) {
            val page = withMetadataRetry("assets", "generation_id" to generationId, "after" to (after ?: "")) {
                api.assets(generationId, after)
            }
            if (page.assets.isNotEmpty()) {
                dao.importAssets(page.assets.map { it.toEntity(generationId) }, nowMsProvider())
                total += page.assets.size
            }
            logger.info(
                event = "android_sync_assets_page",
                fields = mapOf(
                    "generation_id" to generationId,
                    "after" to (after ?: ""),
                    "count" to page.assets.size,
                    "total" to total,
                    "next" to page.next,
                    "end" to page.end_of_stream,
                ),
            )
            if (page.end_of_stream) break
            if (page.next.isBlank() || page.next == after) {
                logger.info(
                    event = "android_sync_assets_marker_stalled",
                    fields = mapOf("generation_id" to generationId, "next" to page.next),
                )
                throw IllegalStateException("Android sync assets marker stalled for generation $generationId")
            }
            after = page.next
        }
        val adopted = dao.adoptVerifiedAssetsFromPreviousGenerations(generationId, nowMsProvider())
        if (adopted > 0) {
            logger.info(
                event = "android_sync_assets_adopted_verified",
                fields = mapOf("generation_id" to generationId, "count" to adopted),
            )
        }
        dao.markAssetsImported(generationId, nowMsProvider())
        logger.info(
            event = "android_sync_assets_imported",
            fields = mapOf("generation_id" to generationId, "count" to total),
        )
    }

    private suspend fun <T> withMetadataRetry(
        label: String,
        vararg fields: Pair<String, Any?>,
        block: suspend () -> T,
    ): T {
        var attempt = 0
        while (true) {
            try {
                return block()
            } catch (e: CancellationException) {
                throw e
            } catch (e: AndroidSyncStaleGenerationException) {
                throw e
            } catch (e: Exception) {
                if (e.isTerminalMetadataFailure()) throw e
                if (e.isLikelyTransportFailure()) reachability.downgrade()
                val retryDelay = METADATA_RETRY_DELAYS_MS.getOrElse(attempt) { METADATA_RETRY_DELAYS_MS.last() }
                logger.info(
                    event = "android_sync_metadata_retry",
                    fields = mapOf(
                        "label" to label,
                        "attempt" to (attempt + 1),
                        "delay_ms" to retryDelay,
                        "error" to (e.message ?: e::class.simpleName.orEmpty()),
                    ) + fields.toMap(),
                )
                attempt++
                delay(retryDelay)
            }
        }
    }

    private companion object {
        const val SYNC_FAILURE_RETRY_MS = 30_000L

        val METADATA_RETRY_DELAYS_MS = listOf(1_000L, 5_000L, 15_000L)
    }
}

internal fun Throwable.isLikelyTransportFailure(): Boolean {
    generateSequence(this) { it.cause }.forEach { cause ->
        if (cause is AndroidSyncHttpException && cause.downgradesReachability) return true
        if (cause is IOException) return true
        val simpleName = cause::class.simpleName.orEmpty()
        if (simpleName == "ConnectTimeoutException" ||
            simpleName == "SocketTimeoutException" ||
            simpleName == "HttpRequestTimeoutException"
        ) {
            return true
        }
        val message = cause.message?.lowercase().orEmpty()
        if (message.contains("failed to connect") ||
            message.contains("unable to resolve host") ||
            message.contains("timeout") ||
            message.contains("connection reset") ||
            message.contains("socket closed") ||
            message.contains("network"))
        {
            return true
        }
    }
    return false
}

private fun Throwable.isTerminalMetadataFailure(): Boolean {
    generateSequence(this) { it.cause }.forEach { cause ->
        if (cause is AndroidSyncDecodeException) return true
        if (cause is AndroidSyncHttpException && !cause.isTransient) return true
    }
    return false
}

private fun AndroidSyncGenerationDto.toEntity(): AndroidSyncGenerationEntity =
    AndroidSyncGenerationEntity(
        generationId = generation_id,
        createdAtMs = created_at_ms,
        status = status,
        sourceVersion = source_version,
        retentionJson = androidSyncJson.encodeToString(retention),
        itemCount = item_count,
        assetCount = asset_count,
        readyAssetCount = ready_asset_count,
        serverMissingAssetCount = server_missing_asset_count,
        totalBytes = total_bytes,
        contentCountsJson = androidSyncJson.encodeToString(content_counts),
        assetCountsJson = androidSyncJson.encodeToString(asset_counts),
    )

private fun AndroidSyncItemDto.toEntity(generationId: String): AndroidSyncItemEntity =
    AndroidSyncItemEntity(
        generationId = generationId,
        seq = seq,
        itemKind = item_kind,
        itemId = item_id,
        payloadJson = androidSyncJson.encodeToString(payload),
    )

private fun AndroidSyncAssetDto.toEntity(generationId: String): AndroidSyncAssetEntity =
    AndroidSyncAssetEntity(
        generationId = generationId,
        seq = seq,
        assetId = asset_id,
        assetKind = asset_kind,
        mediaIndex = media_index,
        ownerId = owner_id,
        ownerKind = owner_kind,
        bucket = bucket,
        serverUrl = server_url,
        contentType = content_type,
        sizeBytes = size_bytes,
        sha256 = sha256,
        serverState = state,
        requiredReason = required_reason,
        subtitleIsAuto = is_auto ?: true,
        audioLanguage = audio_language,
        effectiveRecencyMs = effective_recency_ms,
        state = if (state == "server_missing") "server_missing" else "desired",
        updatedAtMs = System.currentTimeMillis(),
    )
