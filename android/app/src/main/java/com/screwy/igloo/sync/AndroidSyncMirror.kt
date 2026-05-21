package com.screwy.igloo.sync

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.AndroidSyncContentPruneCounts
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.AndroidSyncGenerationPruneCounts
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncGenerationEntity
import com.screwy.igloo.data.entity.AndroidSyncItemEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.ForegroundPromoter
import com.screwy.igloo.net.AndroidSyncApi
import com.screwy.igloo.net.AndroidSyncAssetDto
import com.screwy.igloo.net.AndroidSyncDecodeException
import com.screwy.igloo.net.AndroidSyncGenerationDto
import com.screwy.igloo.net.AndroidSyncHttpException
import com.screwy.igloo.net.AndroidSyncItemDto
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.perf.PerfProbe
import io.ktor.client.HttpClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
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

private fun AndroidSyncGenerationPruneCounts.hasDeletes(): Boolean =
    items > 0 || assets > 0 || generations > 0

private fun AssetFileDeleteStats.hasDeletes(): Boolean = files > 0

private fun elapsedMsSince(startedAtNanos: Long): Long =
    (System.nanoTime() - startedAtNanos) / 1_000_000L

/**
 * Android sync mirror runner. This imports the server-owned immutable generation,
 * applies content bundles through the same BundleIngest path, downloads every
 * ready asset into an Android sync ledger, verifies exact size/hash, then reports
 * generation coverage.
 */
class AndroidSyncMirror(
    private val scope: CoroutineScope,
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
        AndroidSyncRetentionRequest(
            feedDays = PreferencesRepo.Defaults.RETENTION_DAYS_FEED,
            youtubeDays = PreferencesRepo.Defaults.RETENTION_DAYS_YOUTUBE,
            momentsDays = PreferencesRepo.Defaults.RETENTION_DAYS_MOMENTS,
            storyHours = PreferencesRepo.Defaults.STORIES_WINDOW_HOURS,
        )
    },
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
    private val refreshRetryDelayMs: Long = SYNC_REFRESH_RETRY_MS,
    private val metadataRetryDelaysMs: List<Long> = METADATA_RETRY_DELAYS_MS,
    private val refreshRetryEnabledProvider: () -> Boolean = { true },
) : AndroidSyncRunner {

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
    private val refreshRetryScheduled = AtomicBoolean(false)
    private var lastOrphanAssetWalkGenerationId: String? = null

    override fun trigger() {
        triggerSeq.incrementAndGet()
        triggerChannel.trySend(Unit)
    }

    fun hasPendingOrActiveWork(): Boolean =
        syncActive.get() || completedTriggerSeq.get() < triggerSeq.get()

    override suspend fun run() {
        while (true) {
            triggerChannel.receive()
            val observedTriggerSeq = triggerSeq.get()
            syncActive.set(true)
            try {
                syncOnce()
                markTriggerCompleted(observedTriggerSeq)
            } catch (e: CancellationException) {
                throw e
            } catch (e: AndroidSyncStaleGenerationException) {
                val retryEnabled = refreshRetryEnabledProvider()
                logger.info(
                    event = "android_sync_generation_stale_retry",
                    fields = mapOf(
                        "generation_id" to e.generationId,
                        "retry_scheduled" to retryEnabled.toString(),
                    ),
                )
                markTriggerCompleted(observedTriggerSeq)
                if (retryEnabled) scheduleRefreshRetry()
            } catch (e: Exception) {
                logger.error(
                    event = "android_sync_unhandled",
                    fields = mapOf("error" to (e.message ?: e::class.simpleName.orEmpty())),
                    throwable = e,
                )
                markTriggerCompleted(observedTriggerSeq)
            } finally {
                syncActive.set(false)
            }
        }
    }

    private fun markTriggerCompleted(triggerSeqValue: Long) {
        completedTriggerSeq.set(maxOf(completedTriggerSeq.get(), triggerSeqValue))
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
            val contentPruned = pruneContentOutsideGenerationMeasured(generation.generation_id)
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
            val prunedGenerations = pruneGenerationsExceptMeasured(generation.generation_id)
            if (prunedGenerations.hasDeletes()) {
                logger.info(
                    event = "android_sync_generations_pruned",
                    fields = mapOf(
                        "generation_id" to generation.generation_id,
                        "items" to prunedGenerations.items,
                        "assets" to prunedGenerations.assets,
                        "generations" to prunedGenerations.generations,
                    ),
                )
            }
            runOrSkipOrphanAssetFileCleanup(
                generationId = generation.generation_id,
                contentPruned = contentPruned,
                generationPruned = prunedGenerations,
            )

            logger.info(
                event = "android_sync_generation_done",
                fields = mapOf(
                    "generation_id" to generation.generation_id,
                    "duration_ms" to (nowMsProvider() - startedAt),
                ),
            )
            if (latest.refreshing) {
                val retryEnabled = refreshRetryEnabledProvider()
                logger.info(
                    event = "android_sync_generation_refresh_pending",
                    fields = mapOf(
                        "generation_id" to generation.generation_id,
                        "retry_scheduled" to retryEnabled.toString(),
                    ),
                )
                if (retryEnabled) scheduleRefreshRetry()
            }
        } finally {
            foregroundPromoter.finishActiveDrain()
        }
    }

    private fun scheduleRefreshRetry() {
        if (!refreshRetryScheduled.compareAndSet(false, true)) return
        scope.launch {
            try {
                delay(refreshRetryDelayMs)
                trigger()
            } finally {
                refreshRetryScheduled.set(false)
            }
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
        dao.markItemsImportStarted(generationId, ANDROID_SYNC_ITEM_IMPORTER_VERSION)
        val resumeAfter = dao.importedItemSeq(generationId).takeIf { it > 0L }
        var after: String? = resumeAfter?.toString()
        var total = resumeAfter?.toInt() ?: 0
        if (resumeAfter != null) {
            logger.info(
                event = "android_sync_items_resume",
                fields = mapOf("generation_id" to generationId, "after" to resumeAfter),
            )
        }
        while (true) {
            val measuredPage = withMetadataRetry("items", "generation_id" to generationId, "after" to (after ?: "")) {
                api.measuredItems(generationId, after)
            }
            val page = measuredPage.value
            var ledgerWriteMs = 0L
            var ledgerRowsWritten = 0
            var changedItemQueryMs = 0L
            var changedCount = 0
            var skippedUnchanged = 0
            var ingestTransactions = 0
            var ingestOk = 0
            var ingestUnknown = 0
            var ingestParseFailures = 0
            var ingestTransactionMs = 0L
            if (page.items.isNotEmpty()) {
                val itemRows = page.items.map { it.toEntity(generationId) }
                val ledgerWriteStartedAt = System.nanoTime()
                ledgerRowsWritten = dao.upsertChangedItems(itemRows)
                ledgerWriteMs = elapsedMsSince(ledgerWriteStartedAt)
                val changedQueryStartedAt = System.nanoTime()
                val changedSeqs = dao.changedItemSeqsFromPreviousImportedGenerations(
                    generationId = generationId,
                    afterSeq = after?.toLongOrNull() ?: 0L,
                    toSeq = page.items.maxOf { it.seq },
                    importerVersion = ANDROID_SYNC_ITEM_IMPORTER_VERSION,
                ).toSet()
                changedItemQueryMs = elapsedMsSince(changedQueryStartedAt)
                changedCount = changedSeqs.size
                skippedUnchanged = page.items.size - changedSeqs.size
                val changedItems = page.items.filter { item -> item.seq in changedSeqs }
                if (changedItems.isNotEmpty()) {
                    val ingestStartedAt = System.nanoTime()
                    val results = ingest.ingestBatch(changedItems.map { it.payload }, guard)
                    ingestTransactionMs += elapsedMsSince(ingestStartedAt)
                    ingestTransactions = 1
                    for ((item, result) in changedItems.zip(results)) {
                        if (result is IngestResult.ParseFailure) {
                            ingestParseFailures++
                            logger.info(
                                event = "android_sync_item_parse_failed",
                                fields = mapOf(
                                    "generation_id" to generationId,
                                    "kind" to item.item_kind,
                                    "item_id" to item.item_id,
                                    "error" to (result.cause.message ?: result.cause::class.simpleName.orEmpty()),
                                ),
                            )
                        } else if (result is IngestResult.UnknownKind) {
                            ingestUnknown++
                        } else {
                            ingestOk++
                        }
                    }
                }
                if (skippedUnchanged > 0) {
                    logger.info(
                        event = "android_sync_items_unchanged_skipped",
                        fields = mapOf(
                            "generation_id" to generationId,
                            "after" to (after ?: ""),
                            "skipped" to skippedUnchanged,
                            "changed" to changedCount,
                        ),
                    )
                }
                dao.markItemsImportPageComplete(
                    generationId = generationId,
                    importedSeq = page.items.maxOf { it.seq },
                    importerVersion = ANDROID_SYNC_ITEM_IMPORTER_VERSION,
                )
                total += page.items.size
            }
            logger.info(
                event = "android_sync_items_page",
                fields = mapOf(
                    "generation_id" to generationId,
                    "after" to (after ?: ""),
                    "count" to page.items.size,
                    "total" to total,
                    "page_bytes" to measuredPage.byteCount,
                    "decode_ms" to measuredPage.decodeDurationMs,
                    "ledger_write_ms" to ledgerWriteMs,
                    "ledger_rows_written" to ledgerRowsWritten,
                    "changed_item_query_ms" to changedItemQueryMs,
                    "changed" to changedCount,
                    "skipped" to skippedUnchanged,
                    "ingest_transactions" to ingestTransactions,
                    "ingest_ok" to ingestOk,
                    "ingest_unknown" to ingestUnknown,
                    "ingest_parse_failed" to ingestParseFailures,
                    "ingest_transaction_ms" to ingestTransactionMs,
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

    private suspend fun pruneGenerationsExceptMeasured(generationId: String): AndroidSyncGenerationPruneCounts =
        db.withTransaction {
            val items = timedPruneStep("delete_items_for_other_generations") {
                dao.deleteItemsForOtherGenerations(generationId)
            }
            val assets = timedPruneStep("delete_assets_for_other_generations") {
                dao.deleteAssetsForOtherGenerations(generationId)
            }
            val generations = timedPruneStep("delete_other_generations") {
                dao.deleteOtherGenerations(generationId)
            }
            AndroidSyncGenerationPruneCounts(items = items, assets = assets, generations = generations)
        }

    private suspend fun runOrSkipOrphanAssetFileCleanup(
        generationId: String,
        contentPruned: AndroidSyncContentPruneCounts,
        generationPruned: AndroidSyncGenerationPruneCounts,
    ) {
        val firstWalkForGeneration = lastOrphanAssetWalkGenerationId != generationId
        if (!firstWalkForGeneration && !contentPruned.hasDeletes() && !generationPruned.hasDeletes()) {
            PerfProbe.log(event = "android_sync_orphan_asset_files_walk_skipped") {
                mapOf("generation_id" to generationId, "reason" to "unchanged_generation")
            }
            return
        }

        val orphanAssetFiles = assetDrainer.deleteUnreferencedAssetFiles(
            dao.verifiedLocalPaths(generationId),
        )
        lastOrphanAssetWalkGenerationId = generationId
        PerfProbe.log(
            event = "android_sync_orphan_asset_files_walk",
        ) {
            mapOf(
                "generation_id" to generationId,
                "files_walked" to orphanAssetFiles.walkedFiles,
                "files_deleted" to orphanAssetFiles.files,
                "bytes_deleted" to orphanAssetFiles.bytes,
                "duration_ms" to orphanAssetFiles.walkDurationMs,
                "reason" to when {
                    firstWalkForGeneration -> "first_generation_pass"
                    contentPruned.hasDeletes() -> "content_pruned"
                    else -> "generation_pruned"
                },
            )
        }
        if (orphanAssetFiles.hasDeletes()) {
            logger.info(
                event = "android_sync_orphan_asset_files_pruned",
                fields = mapOf(
                    "generation_id" to generationId,
                    "files" to orphanAssetFiles.files,
                    "bytes" to orphanAssetFiles.bytes,
                    "walked_files" to orphanAssetFiles.walkedFiles,
                    "walk_duration_ms" to orphanAssetFiles.walkDurationMs,
                ),
            )
        }
    }

    private suspend fun pruneContentOutsideGenerationMeasured(generationId: String): AndroidSyncContentPruneCounts =
        db.withTransaction {
            val videos = timedPruneStep("delete_videos_outside_generation") {
                dao.deleteVideosOutsideGeneration(generationId)
            }
            val feedItems = timedPruneStep("delete_feed_items_outside_generation") {
                dao.deleteFeedItemsOutsideGeneration(generationId)
            }
            val sideRows = timedPruneStep("delete_video_comments_without_video") {
                dao.deleteVideoCommentsWithoutVideo()
            } + timedPruneStep("delete_video_repost_sources_without_video") {
                dao.deleteVideoRepostSourcesWithoutVideo()
            } + timedPruneStep("delete_sponsorblock_segments_without_video") {
                dao.deleteSponsorBlockSegmentsWithoutVideo()
            } + timedPruneStep("delete_sponsorblock_checks_without_video") {
                dao.deleteSponsorBlockChecksWithoutVideo()
            } + timedPruneStep("delete_watch_history_without_video") {
                dao.deleteWatchHistoryWithoutVideo()
            } + timedPruneStep("delete_moment_views_without_video") {
                dao.deleteMomentViewsWithoutVideo()
            } + timedPruneStep("delete_feed_seen_without_feed_item") {
                dao.deleteFeedSeenWithoutFeedItem()
            } + timedPruneStep("delete_feed_rank_without_feed_item") {
                dao.deleteFeedRankWithoutFeedItem()
            } + timedPruneStep("delete_feed_thread_context_without_feed_item") {
                dao.deleteFeedThreadContextWithoutFeedItem()
            } + timedPruneStep("delete_retweet_sources_without_feed_item") {
                dao.deleteRetweetSourcesWithoutFeedItem()
            } + timedPruneStep("delete_channel_follows_outside_generation") {
                dao.deleteChannelFollowsOutsideGeneration(generationId)
            } + timedPruneStep("delete_channel_stars_outside_generation") {
                dao.deleteChannelStarsOutsideGeneration(generationId)
            } + timedPruneStep("delete_channel_settings_outside_generation") {
                dao.deleteChannelSettingsOutsideGeneration(generationId)
            }
            val channels = timedPruneStep("delete_channels_outside_generation") {
                dao.deleteChannelsOutsideGeneration(generationId)
            }
            val channelProfiles = timedPruneStep("delete_channel_profiles_without_channel") {
                dao.deleteChannelProfilesWithoutChannel(generationId)
            }
            val legacyAssets = timedPruneStep("delete_legacy_assets_without_owner") {
                dao.deleteLegacyAssetsWithoutOwner()
            }
            AndroidSyncContentPruneCounts(
                videos = videos,
                feedItems = feedItems,
                channels = channels,
                channelProfiles = channelProfiles,
                legacyAssets = legacyAssets,
                sideRows = sideRows,
            )
        }

    private suspend fun timedPruneStep(
        step: String,
        block: suspend () -> Int,
    ): Int {
        if (!PerfProbe.enabled()) return block()
        val startedAt = android.os.SystemClock.elapsedRealtimeNanos()
        var rows: Int? = null
        val traceName = "android_sync_prune_$step"
        val cookie = PerfProbe.beginAsync(traceName)
        return try {
            block().also { rows = it }
        } finally {
            PerfProbe.endAsync(traceName, cookie)
            PerfProbe.log(
                event = "android_sync_prune_step",
            ) {
                mapOf(
                    "step" to step,
                    "rows" to (rows ?: "failed"),
                    "duration_ms" to PerfProbe.elapsedMsSince(startedAt),
                )
            }
        }
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
        var failures = 0
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
                failures++
                if (failures > metadataRetryDelaysMs.size) {
                    logger.info(
                        event = "android_sync_metadata_retry_exhausted",
                        fields = mapOf(
                            "label" to label,
                            "attempts" to failures,
                            "error" to (e.message ?: e::class.simpleName.orEmpty()),
                        ) + fields.toMap(),
                    )
                    throw e
                }
                val retryDelay = metadataRetryDelaysMs[failures - 1]
                logger.info(
                    event = "android_sync_metadata_retry",
                    fields = mapOf(
                        "label" to label,
                        "attempt" to failures,
                        "delay_ms" to retryDelay,
                        "error" to (e.message ?: e::class.simpleName.orEmpty()),
                    ) + fields.toMap(),
                )
                delay(retryDelay)
            }
        }
    }

    private companion object {
        const val SYNC_REFRESH_RETRY_MS = 30_000L

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
            message.contains("socket failed") ||
            message.contains("operation not permitted") ||
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
