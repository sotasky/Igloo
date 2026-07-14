package com.screwy.igloo.sync

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncHeadEntity
import com.screwy.igloo.data.entity.AndroidSyncStateEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.ForegroundPromoter
import com.screwy.igloo.net.AndroidSyncApi
import com.screwy.igloo.net.AndroidSyncAssetDto
import com.screwy.igloo.net.AndroidSyncChangeDto
import com.screwy.igloo.net.AndroidSyncDecodeException
import com.screwy.igloo.net.AndroidSyncHttpException
import com.screwy.igloo.net.AndroidSyncPageResponse
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import io.ktor.client.HttpClient
import java.io.File
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay

class AndroidSyncMirror(
    private val db: IglooDatabase,
    private val dao: AndroidSyncDao,
    private val api: AndroidSyncApi,
    client: HttpClient,
    baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
    foregroundPromoter: ForegroundPromoter,
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
    private val serverNowMsProvider: () -> Long = { System.currentTimeMillis() },
    private val metadataRetryDelaysMs: List<Long> = METADATA_RETRY_DELAYS_MS,
) {
    private val healthReporter =
        AndroidSyncHealthReporter(
            dao = dao,
            api = api,
            reachability = reachability,
            logger = logger,
            nowMsProvider = serverNowMsProvider,
        )
    private val assetDrainer =
        AndroidSyncAssetDrainer(
            dao = dao,
            client = client,
            baseUrlProvider = baseUrlProvider,
            reachability = reachability,
            foregroundPromoter = foregroundPromoter,
            mediaRoot = mediaRoot,
            logger = logger,
            nowMsProvider = serverNowMsProvider,
        )

    suspend fun syncOnce(protectionChanged: Boolean = false) {
        val retention = retentionProvider().validated()
        syncMetadata(retention)
        prune(retention, protectionChanged)

        var state = requireChangesState()
        healthReporter.report(state.cursor, retention)
        try {
            assetDrainer.drain()
        } catch (e: AndroidSyncAssetChangedException) {
            syncMetadata(retention)
            prune(retention, sweepHeadlessContent = false)
            state = requireChangesState()
            assetDrainer.drain()
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            healthReporter.report(state.cursor, retention)
            throw e
        }
        healthReporter.report(requireChangesState().cursor, retention)
    }

    suspend fun prune() {
        prune(retentionProvider().validated(), sweepHeadlessContent = true)
    }

    suspend fun requestBootstrap() {
        dao.syncState()?.let { dao.upsertSyncState(it.copy(bootstrapRequired = true)) }
    }

    private suspend fun syncMetadata(retention: AndroidSyncRetentionRequest) {
        while (true) {
            var state = dao.syncState()
            if (state == null) {
                beginBootstrap(retention)
                continue
            }
            try {
                when (state.mode) {
                    MODE_BOOTSTRAP -> runPages(state, state.retention(), bootstrap = true)
                    MODE_CHANGES -> {
                        runPages(state, retention, bootstrap = false)
                        state = requireNotNull(dao.syncState())
                        if (state.bootstrapRequired) {
                            beginBootstrap(retention)
                            continue
                        }
                        return
                    }
                    else -> error("unknown Android sync mode: ${state.mode}")
                }
            } catch (e: AndroidSyncHttpException) {
                if (!e.isSyncResetRequired) throw e
                beginBootstrap(retention)
            }
        }
    }

    private suspend fun beginBootstrap(retention: AndroidSyncRetentionRequest) {
        db.withTransaction {
            dao.markHeadsUnseen()
            dao.upsertSyncState(
                AndroidSyncStateEntity(
                    mode = MODE_BOOTSTRAP,
                    cursor = "",
                    feedDays = retention.feedDays,
                    youtubeDays = retention.youtubeDays,
                    momentsDays = retention.momentsDays,
                    storyHours = retention.storyHours,
                )
            )
        }
    }

    private suspend fun runPages(
        initial: AndroidSyncStateEntity,
        retention: AndroidSyncRetentionRequest,
        bootstrap: Boolean,
    ) {
        var state = initial
        while (true) {
            val page =
                withMetadataRetry(if (bootstrap) "bootstrap" else "changes") {
                    if (bootstrap) {
                        api.bootstrap(retention, state.cursor.ifEmpty { null })
                    } else {
                        api.changes(retention, state.cursor)
                    }
                }
            page.validate(state.cursor)
            deleteFiles(applyPage(state, page, retention, bootstrap))
            if (page.end_of_stream) return
            state = requireNotNull(dao.syncState())
        }
    }

    private suspend fun applyPage(
        state: AndroidSyncStateEntity,
        page: AndroidSyncPageResponse,
        retention: AndroidSyncRetentionRequest,
        bootstrap: Boolean,
    ): List<AndroidSyncAssetEntity> =
        db.withTransaction {
            val deletedAssets = mutableListOf<AndroidSyncAssetEntity>()
            val overlay = PendingMutationOverlay.capture(db.outboxDao().pendingRows())

            var selectionExpanded = false
            if (!bootstrap) {
                for (change in page.changes) {
                    if (change.isThinState() && expandsSelection(change)) {
                        selectionExpanded = true
                        break
                    }
                }
            }

            page.changes.filter(AndroidSyncChangeDto::isThinState).forEach { change ->
                applyThinState(change, deletedAssets)
            }
            overlay.restore(db)

            val protectedContent =
                if (page.changes.any { it.isPrimaryContent() && it.operation == OP_DELETE }) {
                    dao.protectedContentIds().toHashSet()
                } else {
                    emptySet()
                }
            page.changes.filter(AndroidSyncChangeDto::isPrimaryContent).forEach { change ->
                applyPrimary(change, protectedContent, deletedAssets)
            }

            page.changes.filter { it.owner_kind == "retweet_sources" }.forEach { change ->
                applySecondary(change, emptySet(), emptySet(), false, deletedAssets)
            }

            val protectedChannels =
                if (
                    page.changes.any {
                        it.owner_kind == "channel" && (!bootstrap || it.operation == OP_DELETE)
                    }
                ) {
                    dao.protectedChannelIds().toHashSet()
                } else {
                    emptySet()
                }
            page.changes
                .filterNot {
                    it.isThinState() || it.isPrimaryContent() ||
                        it.owner_kind == "retweet_sources" || it.owner_kind == "asset"
                }
                .forEach { change ->
                    applySecondary(change, protectedChannels, emptySet(), bootstrap, deletedAssets)
                }

            val retainedAssetOwners =
                if (
                    page.changes.any {
                        it.owner_kind == "asset" && (!bootstrap || it.operation == OP_DELETE)
                    }
                ) {
                    dao.retainedAssetOwnerIds().toHashSet()
                } else {
                    emptySet()
                }
            page.changes.filter { it.owner_kind == "asset" }.forEach { change ->
                applySecondary(change, protectedChannels, retainedAssetOwners, bootstrap, deletedAssets)
            }

            if (bootstrap) {
                if (page.end_of_stream) {
                    sweepBootstrap(deletedAssets)
                    sweepHeadlessThinState()
                    overlay.restore(db)
                    cleanupOrphans(deletedAssets, sweepHeadlessContent = true)
                }
            } else if (
                page.changes.any { it.operation == OP_DELETE || it.replacesDependencies() }
            ) {
                cleanupOrphans(
                    deletedAssets,
                    sweepHeadlessContent = page.changes.any(AndroidSyncChangeDto::releasesProtection),
                )
            }

            val nextState =
                if (bootstrap && page.end_of_stream) {
                    state.copy(
                        mode = MODE_CHANGES,
                        cursor = page.next_cursor,
                        bootstrapRequired = false,
                    )
                } else {
                    state.copy(
                        cursor = page.next_cursor,
                        feedDays = retention.feedDays,
                        youtubeDays = retention.youtubeDays,
                        momentsDays = retention.momentsDays,
                        storyHours = retention.storyHours,
                        bootstrapRequired = state.bootstrapRequired || selectionExpanded,
                    )
                }
            if (nextState != state) dao.upsertSyncState(nextState)
            deletedAssets
        }

    private suspend fun expandsSelection(change: AndroidSyncChangeDto): Boolean =
        when (change.owner_kind) {
            "channel_follow" ->
                change.operation == OP_UPSERT && !db.channelFollowDao().exists(change.owner_id)
            "channel_setting" -> {
                val previous = db.channelSettingDao().getById(change.owner_id)?.includeReposts
                val next =
                    if (change.operation == OP_DELETE) null
                    else AndroidSyncChangeDecoder.channelSetting(change).includeReposts
                (previous == 0 && next != 0) || (previous == null && next == 1)
            }
            "setting" -> change.owner_id in SELECTION_SETTING_KEYS
            else -> false
        }

    private suspend fun sweepBootstrap(deletedAssets: MutableList<AndroidSyncAssetEntity>) {
        val protectedContent = dao.protectedContentIds().toHashSet()
        val protectedChannels = dao.protectedChannelIds().toHashSet()
        val retainedAssetOwners = dao.retainedAssetOwnerIds().toHashSet()
        dao.unseenHeads().forEach { head ->
            deleteOwner(
                ownerKind = head.ownerKind,
                ownerId = head.ownerId,
                protectedContent = protectedContent,
                protectedChannels = protectedChannels,
                retainedAssetOwners = retainedAssetOwners,
                deletedAssets = deletedAssets,
            )
        }
    }

    private fun sweepHeadlessThinState() {
        val sqlite = db.openHelper.writableDatabase
        MIRRORED_THIN_STATE.forEach { owner ->
            sqlite.execSQL(
                """
                DELETE FROM ${owner.table}
                WHERE ${owner.predicate}
                  AND NOT EXISTS (
                      SELECT 1 FROM android_sync_heads h
                      WHERE h.owner_kind = '${owner.kind}'
                        AND h.owner_id = ${owner.idExpression}
                  )
                """.trimIndent()
            )
        }
    }

    private suspend fun applyThinState(
        change: AndroidSyncChangeDto,
        deletedAssets: MutableList<AndroidSyncAssetEntity>,
    ) {
        if (change.operation == OP_DELETE) {
            deleteOwner(change.owner_kind, change.owner_id, deletedAssets = deletedAssets)
            return
        }

        when (change.owner_kind) {
            "feed_like" -> db.feedLikeDao().upsert(AndroidSyncChangeDecoder.feedLike(change))
            "bookmark" -> db.bookmarkDao().upsert(AndroidSyncChangeDecoder.bookmark(change))
            "bookmark_category" ->
                db.bookmarkCategoryDao().upsert(AndroidSyncChangeDecoder.bookmarkCategory(change))
            "feed_seen" -> db.feedSeenDao().upsert(AndroidSyncChangeDecoder.feedSeen(change))
            "moment_view" -> db.momentViewDao().upsert(AndroidSyncChangeDecoder.momentView(change))
            "watch_history" ->
                db.watchHistoryDao().upsert(AndroidSyncChangeDecoder.watchHistory(change))
            "muted_channel" -> {
                db.mutedChannelDao().upsert(AndroidSyncChangeDecoder.mutedChannel(change))
            }
            "channel_follow" -> {
                db.channelFollowDao().upsert(AndroidSyncChangeDecoder.channelFollow(change))
            }
            "channel_star" -> {
                db.channelStarDao().upsert(AndroidSyncChangeDecoder.channelStar(change))
            }
            "channel_setting" -> {
                db.channelSettingDao().upsert(AndroidSyncChangeDecoder.channelSetting(change))
            }
            "moments_cursor" ->
                db.momentsCursorDao().upsert(AndroidSyncChangeDecoder.momentsCursor(change))
            "setting" -> AndroidSyncChangeDecoder.setting(change)
            else -> error("unknown thin Android sync owner: ${change.owner_kind}")
        }
        dao.upsertHead(change.toHead())
    }

    private suspend fun applyPrimary(
        change: AndroidSyncChangeDto,
        protectedContent: Set<String>,
        deletedAssets: MutableList<AndroidSyncAssetEntity>,
    ) {
        if (change.operation == OP_DELETE) {
            deleteOwner(
                change.owner_kind,
                change.owner_id,
                protectedContent = protectedContent,
                deletedAssets = deletedAssets,
            )
            return
        }
        when (change.owner_kind) {
            "feed" -> applyFeed(AndroidSyncChangeDecoder.feed(change))
            "video" -> applyVideo(AndroidSyncChangeDecoder.video(change))
        }
        dao.upsertHead(change.toHead())
    }

    private suspend fun applySecondary(
        change: AndroidSyncChangeDto,
        protectedChannels: Set<String>,
        retainedAssetOwners: Set<String>,
        selectedSnapshot: Boolean,
        deletedAssets: MutableList<AndroidSyncAssetEntity>,
    ) {
        if (change.operation == OP_DELETE) {
            deleteOwner(
                change.owner_kind,
                change.owner_id,
                protectedChannels = protectedChannels,
                retainedAssetOwners = retainedAssetOwners,
                deletedAssets = deletedAssets,
            )
            return
        }

        val accepted =
            when (change.owner_kind) {
                "channel" -> {
                    if (!selectedSnapshot && change.owner_id !in protectedChannels) false
                    else {
                        applyChannelBundle(AndroidSyncChangeDecoder.channel(change))
                        true
                    }
                }
                "retweet_sources" -> {
                    if (!dao.hasContentHash(change.owner_id)) false
                    else {
                        replaceRetweetSources(change.owner_id, AndroidSyncChangeDecoder.retweetSources(change))
                        true
                    }
                }
                "feed_rank" -> {
                    if (!dao.hasFeed(change.owner_id)) false
                    else {
                        db.feedRankDao().upsert(listOf(AndroidSyncChangeDecoder.feedRank(change)))
                        true
                    }
                }
                "asset" -> {
                    val asset = AndroidSyncChangeDecoder.asset(change)
                    if (!selectedSnapshot && asset.owner_id !in retainedAssetOwners) false
                    else {
                        upsertAsset(asset, dao.asset(asset.asset_id), deletedAssets)
                        true
                    }
                }
                else -> error("unknown Android sync owner: ${change.owner_kind}")
            }
        if (accepted) dao.upsertHead(change.toHead())
    }

    private suspend fun applyFeed(row: com.screwy.igloo.data.entity.FeedItemEntity) {
        db.feedItemDao().upsert(row)
    }

    private suspend fun applyVideo(decoded: AndroidVideoUpsert) {
        db.videoDao().upsert(decoded.item)
        val id = decoded.item.videoId
        db.videoCommentDao().deleteForVideo(id)
        db.videoRepostSourceDao().deleteForVideo(id)
        db.sponsorBlockSegmentDao().deleteForVideo(id)
        db.sponsorBlockCheckedDao().deleteForVideo(id)
        if (decoded.comments.isNotEmpty()) db.videoCommentDao().upsert(decoded.comments)
        if (decoded.repostSources.isNotEmpty()) db.videoRepostSourceDao().upsert(decoded.repostSources)
        if (decoded.sponsorBlockSegments.isNotEmpty())
            db.sponsorBlockSegmentDao().upsert(decoded.sponsorBlockSegments)
        decoded.sponsorBlockChecked?.let { db.sponsorBlockCheckedDao().upsert(it) }
    }

    private suspend fun applyChannelBundle(decoded: AndroidChannelUpsert) {
        decoded.channel?.let { db.channelDao().upsert(it) }
        if (decoded.profile == null) {
            decoded.channel?.let { db.channelProfileDao().delete(it.channelId) }
        } else {
            db.channelProfileDao().upsert(decoded.profile)
        }
    }

    private suspend fun replaceRetweetSources(
        contentHash: String,
        rows: List<com.screwy.igloo.data.entity.RetweetSourceEntity>,
    ) {
        db.retweetSourceDao().deleteForContentHash(contentHash)
        if (rows.isNotEmpty()) db.retweetSourceDao().upsert(rows)
    }

    private suspend fun upsertAsset(
        asset: AndroidSyncAssetDto,
        existing: AndroidSyncAssetEntity?,
        deletedAssets: MutableList<AndroidSyncAssetEntity>,
    ) {
        asset.validate()
        val next = asset.toEntity(existing)
        if (existing?.localPath != null && next.localPath == null) deletedAssets += existing
        dao.upsertAsset(next)
    }

    private suspend fun deleteOwner(
        ownerKind: String,
        ownerId: String,
        protectedContent: Set<String> = emptySet(),
        protectedChannels: Set<String> = emptySet(),
        retainedAssetOwners: Set<String> = emptySet(),
        deletedAssets: MutableList<AndroidSyncAssetEntity>,
    ) {
        when (ownerKind) {
            "feed_like" -> db.feedLikeDao().delete(ownerId)
            "bookmark" -> db.bookmarkDao().delete(ownerId)
            "bookmark_category" ->
                db.bookmarkCategoryDao().delete(ownerId.toLongOrNull() ?: error("invalid bookmark category id"))
            "feed_seen" -> db.feedSeenDao().delete(ownerId)
            "moment_view" -> db.momentViewDao().delete(ownerId)
            "watch_history" -> db.watchHistoryDao().delete(ownerId)
            "muted_channel" -> db.mutedChannelDao().delete(ownerId)
            "channel_follow" -> db.channelFollowDao().delete(ownerId)
            "channel_star" -> db.channelStarDao().delete(ownerId)
            "channel_setting" -> db.channelSettingDao().delete(ownerId)
            "moments_cursor" -> db.momentsCursorDao().delete(ownerId)
            "setting" -> Unit
            "feed" -> if (ownerId !in protectedContent) db.feedItemDao().deleteByIds(listOf(ownerId))
            "video" -> if (ownerId !in protectedContent) db.videoDao().deleteByIds(listOf(ownerId))
            "channel" -> if (ownerId !in protectedChannels) {
                db.channelDao().deleteByIds(listOf(ownerId))
                db.channelProfileDao().delete(ownerId)
            }
            "retweet_sources" -> db.retweetSourceDao().deleteForContentHash(ownerId)
            "feed_rank" -> db.feedRankDao().deleteForTweets(listOf(ownerId))
            "asset" -> {
                val row = dao.asset(ownerId)
                if (row == null || row.localPath == null || row.ownerId !in retainedAssetOwners) {
                    if (row != null) deletedAssets += row
                    dao.deleteAsset(ownerId)
                }
            }
            else -> error("unknown Android sync owner: $ownerKind")
        }
        dao.deleteHead(ownerKind, ownerId)
    }

    private suspend fun prune(
        retention: AndroidSyncRetentionRequest,
        sweepHeadlessContent: Boolean,
    ) {
        val deleted =
            db.withTransaction {
                val now = serverNowMsProvider()
                val expired =
                    dao.expiredHeads(
                        retention.feedDays,
                        now - retention.feedDays.daysMs(),
                        retention.youtubeDays,
                        now - retention.youtubeDays.daysMs(),
                        retention.momentsDays,
                        now - retention.momentsDays.daysMs(),
                        retention.storyHours,
                        now - retention.storyHours.hoursMs(),
                    )
                val deletedAssets = mutableListOf<AndroidSyncAssetEntity>()

                if (expired.isNotEmpty()) {
                    val protectedContent = dao.protectedContentIds().toHashSet()
                    val protectedChannels = dao.protectedChannelIds().toHashSet()
                    val retainedAssetOwners = dao.retainedAssetOwnerIds().toHashSet()
                    expired.forEach { head ->
                        deleteOwner(
                            ownerKind = head.ownerKind,
                            ownerId = head.ownerId,
                            protectedContent = protectedContent,
                            protectedChannels = protectedChannels,
                            retainedAssetOwners = retainedAssetOwners,
                            deletedAssets = deletedAssets,
                        )
                    }
                }
                if (expired.isNotEmpty() || sweepHeadlessContent) {
                    cleanupOrphans(deletedAssets, sweepHeadlessContent)
                }
                dao.syncState()
                    ?.takeIf {
                        it.mode == MODE_CHANGES &&
                            (it.feedDays != retention.feedDays ||
                                it.youtubeDays != retention.youtubeDays ||
                                it.momentsDays != retention.momentsDays ||
                                it.storyHours != retention.storyHours)
                    }
                    ?.let {
                    dao.upsertSyncState(
                        it.copy(
                            feedDays = retention.feedDays,
                            youtubeDays = retention.youtubeDays,
                            momentsDays = retention.momentsDays,
                            storyHours = retention.storyHours,
                        )
                    )
                }
                deletedAssets
            }
        deleteFiles(deleted)
    }

    private suspend fun cleanupOrphans(
        deletedAssets: MutableList<AndroidSyncAssetEntity>,
        sweepHeadlessContent: Boolean,
    ) {
        if (sweepHeadlessContent) {
            val protectedContent = dao.protectedContentIds().toHashSet()
            deleteHeadlessContent("feed", db.feedItemDao().allIds(), protectedContent) {
                db.feedItemDao().deleteByIds(it)
            }
            deleteHeadlessContent("video", db.videoDao().allIds(), protectedContent) {
                db.videoDao().deleteByIds(it)
            }
        }

        db.videoCommentDao().deleteOrphans()
        db.videoRepostSourceDao().deleteOrphans()
        db.sponsorBlockSegmentDao().deleteOrphans()
        db.sponsorBlockCheckedDao().deleteOrphans()
        db.retweetSourceDao().deleteOrphans()
        db.feedRankDao().deleteOrphans()

        val protectedChannels = dao.protectedChannelIds().toHashSet()
        db.channelDao().allIds().filterNot(protectedChannels::contains).chunked(PRUNE_BATCH_SIZE).forEach {
            db.channelDao().deleteByIds(it)
        }
        db.channelProfileDao().deleteUnreferenced()

        val retainedAssetOwners = dao.retainedAssetOwnerIds().toHashSet()
        val orphanAssets = dao.allAssets().filterNot { it.ownerId in retainedAssetOwners }
        if (orphanAssets.isNotEmpty()) {
            deletedAssets += orphanAssets
            orphanAssets.map(AndroidSyncAssetEntity::assetId).chunked(PRUNE_BATCH_SIZE).forEach {
                dao.deleteAssets(it)
            }
        }
        dao.deleteOrphanHeads()
    }

    private suspend fun deleteHeadlessContent(
        ownerKind: String,
        ids: List<String>,
        protectedContent: Set<String>,
        delete: suspend (List<String>) -> Unit,
    ) {
        val heads = dao.headIds(ownerKind).toHashSet()
        ids.filterNot { it in heads || it in protectedContent }
            .chunked(PRUNE_BATCH_SIZE)
            .forEach { delete(it) }
    }

    private suspend fun deleteFiles(rows: List<AndroidSyncAssetEntity>) {
        if (rows.isEmpty()) return
        assetDrainer.deleteFilesForAssets(rows, dao.verifiedLocalPaths())
    }

    private suspend fun requireChangesState(): AndroidSyncStateEntity =
        requireNotNull(dao.syncState()).also {
            check(it.mode == MODE_CHANGES && it.cursor.isNotBlank()) { "Android sync cursor is not ready" }
        }

    private suspend fun <T> withMetadataRetry(label: String, block: suspend () -> T): T {
        var failures = 0
        while (true) {
            try {
                return block()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if (e.isTerminalMetadataFailure()) throw e
                if (e.isLikelyTransportFailure()) reachability.downgrade()
                failures++
                if (failures > metadataRetryDelaysMs.size) throw e
                logger.info(
                    event = "android_sync_metadata_retry",
                    fields = mapOf("label" to label, "attempt" to failures),
                )
                delay(metadataRetryDelaysMs[failures - 1])
            }
        }
    }

    private companion object {
        const val MODE_BOOTSTRAP = "bootstrap"
        const val MODE_CHANGES = "changes"
        const val OP_UPSERT = "upsert"
        const val OP_DELETE = "delete"
        const val PRUNE_BATCH_SIZE = 400
        val METADATA_RETRY_DELAYS_MS = listOf(1_000L, 5_000L, 15_000L)
        val SELECTION_SETTING_KEYS =
            setOf(
                "moments_include_reposts_default",
                "instagram_include_tagged_default",
                "include_reposts_default",
            )
        val MIRRORED_THIN_STATE =
            listOf(
                MirroredThinState("feed_like", "feed_likes", "feed_likes.tweet_id"),
                MirroredThinState("bookmark", "bookmarks", "bookmarks.video_id"),
                MirroredThinState(
                    "bookmark_category",
                    "bookmark_categories",
                    "CAST(bookmark_categories.category_id AS TEXT)",
                ),
                MirroredThinState("feed_seen", "feed_seen", "feed_seen.tweet_id"),
                MirroredThinState("moment_view", "moment_views", "moment_views.video_id"),
                MirroredThinState("watch_history", "watch_history", "watch_history.video_id"),
                MirroredThinState("muted_channel", "muted_channels", "muted_channels.channel_id"),
                MirroredThinState("channel_follow", "channel_follows", "channel_follows.channel_id"),
                MirroredThinState("channel_star", "channel_stars", "channel_stars.channel_id"),
                MirroredThinState("channel_setting", "channel_settings", "channel_settings.channel_id"),
                MirroredThinState(
                    "moments_cursor",
                    "moments_cursors",
                    "moments_cursors.scope",
                    "moments_cursors.scope != 'stories'",
                ),
            )
    }
}

private data class MirroredThinState(
    val kind: String,
    val table: String,
    val idExpression: String,
    val predicate: String = "1",
)

internal fun Throwable.isLikelyTransportFailure(): Boolean {
    generateSequence(this) { it.cause }.forEach { cause ->
        if (cause is AndroidSyncHttpException && cause.downgradesReachability) return true
        if (cause is IOException) return true
        val name = cause::class.simpleName.orEmpty()
        if (name in setOf("ConnectTimeoutException", "SocketTimeoutException", "HttpRequestTimeoutException")) {
            return true
        }
        val message = cause.message?.lowercase().orEmpty()
        if (
            message.contains("failed to connect") ||
                message.contains("socket failed") ||
                message.contains("unable to resolve host") ||
                message.contains("timeout") ||
                message.contains("connection reset") ||
                message.contains("socket closed")
        ) {
            return true
        }
    }
    return false
}

private fun Throwable.isTerminalMetadataFailure(): Boolean =
    generateSequence(this) { it.cause }.any { cause ->
        cause is AndroidSyncDecodeException ||
            (cause is AndroidSyncHttpException && !cause.isTransient)
    }

private fun AndroidSyncPageResponse.validate(previousCursor: String) {
    require(next_cursor.isNotBlank()) { "blank Android sync cursor" }
    if (!end_of_stream) require(next_cursor != previousCursor) { "Android sync cursor stalled" }
    require(changes.map { it.owner_kind to it.owner_id }.toSet().size == changes.size) {
        "duplicate Android sync owner in page"
    }
    changes.forEach(AndroidSyncChangeDto::validate)
}

private fun AndroidSyncChangeDto.validate() {
    require(owner_kind in OWNER_KINDS && owner_id.isNotBlank()) { "invalid Android sync owner" }
    require(operation == "upsert" || operation == "delete") { "invalid Android sync operation" }
    require(retention_bucket in RETENTION_BUCKETS && retain_at_ms >= 0) { "invalid Android sync retention" }
    require((operation == "upsert") == (payload != null)) { "Android sync payload does not match operation" }
}

private fun AndroidSyncChangeDto.isThinState(): Boolean = owner_kind in THIN_OWNER_KINDS

private fun AndroidSyncChangeDto.isPrimaryContent(): Boolean = owner_kind == "feed" || owner_kind == "video"

private fun AndroidSyncChangeDto.releasesProtection(): Boolean =
    operation == "delete" && (owner_kind == "feed_like" || owner_kind == "bookmark")

private fun AndroidSyncChangeDto.replacesDependencies(): Boolean =
    operation == "upsert" && owner_kind in DEPENDENCY_REPLACING_OWNER_KINDS

private fun AndroidSyncChangeDto.toHead() =
    AndroidSyncHeadEntity(owner_kind, owner_id, retention_bucket, retain_at_ms)

private fun AndroidSyncRetentionRequest.validated(): AndroidSyncRetentionRequest = also {
    require(feedDays >= 0 && youtubeDays >= 0 && momentsDays >= 0 && storyHours >= 0) {
        "negative Android retention"
    }
}

private fun AndroidSyncStateEntity.retention() =
    AndroidSyncRetentionRequest(feedDays, youtubeDays, momentsDays, storyHours)

private fun Int.daysMs(): Long = toLong() * 86_400_000L

private fun Int.hoursMs(): Long = toLong() * 3_600_000L

private fun AndroidSyncAssetDto.toEntity(existing: AndroidSyncAssetEntity?): AndroidSyncAssetEntity {
    val readyContentType = content_type.ifBlank { null }
    val unchanged =
        state == "ready" &&
            existing?.revision == revision &&
            existing.sizeBytes == size_bytes &&
            existing.contentType == readyContentType
    val keepVerified = unchanged || state == "server_missing"
    return AndroidSyncAssetEntity(
        assetId = asset_id,
        assetKind = asset_kind,
        mediaIndex = media_index,
        ownerId = owner_id,
        ownerKind = owner_kind,
        bucket = bucket,
        contentType = readyContentType,
        sizeBytes = size_bytes,
        revision = revision,
        subtitleIsAuto = is_auto ?: true,
        state = state,
        localPath = existing?.localPath.takeIf { keepVerified },
        verifiedAtMs = existing?.verifiedAtMs.takeIf { keepVerified },
        nextAttemptAtMs = existing?.nextAttemptAtMs?.takeIf { unchanged } ?: 0,
    )
}

internal fun AndroidSyncAssetDto.validate() {
    require(asset_id.isNotBlank() && asset_kind.isNotBlank() && revision > 0) {
        "invalid asset identity"
    }
    require(owner_id.isNotBlank() && owner_kind.isNotBlank() && bucket.isNotBlank()) {
        "asset owner is incomplete"
    }
    require(media_index >= 0) { "negative asset media index" }
    require(asset_kind != "subtitle" || is_auto != null) { "subtitle source kind is missing" }
    when (state) {
        "ready" ->
            require(size_bytes > 0L && content_type.isNotBlank()) {
                "ready asset has invalid transfer metadata"
            }
        "server_missing" ->
            require(size_bytes == 0L && content_type.isBlank()) {
                "missing asset carries ready metadata"
            }
        else -> error("unknown asset state: $state")
    }
}

private val OWNER_KINDS =
    setOf(
        "feed",
        "video",
        "channel",
        "retweet_sources",
        "feed_rank",
        "asset",
        "feed_like",
        "bookmark",
        "bookmark_category",
        "feed_seen",
        "moment_view",
        "watch_history",
        "muted_channel",
        "channel_follow",
        "channel_star",
        "channel_setting",
        "moments_cursor",
        "setting",
    )

private val THIN_OWNER_KINDS =
    setOf(
        "feed_like",
        "bookmark",
        "bookmark_category",
        "feed_seen",
        "moment_view",
        "watch_history",
        "muted_channel",
        "channel_follow",
        "channel_star",
        "channel_setting",
        "moments_cursor",
        "setting",
    )

private val DEPENDENCY_REPLACING_OWNER_KINDS = setOf("feed", "video", "retweet_sources")

private val RETENTION_BUCKETS = setOf("", "feed", "youtube", "moments", "story")
