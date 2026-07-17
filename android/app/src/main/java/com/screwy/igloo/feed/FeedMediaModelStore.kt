package com.screwy.igloo.feed

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.ui.component.MediaItem
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

@OptIn(ExperimentalCoroutinesApi::class)
internal class FeedMediaModelStore(
    private val db: IglooDatabase,
    private val baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
    private val scope: CoroutineScope,
) {
    private val observedSpecs = MutableStateFlow<List<FeedMediaOwnerSpec>>(emptyList())
    // Keep locally ready media available during a reverse scroll without widening the Room query.
    private val recentLocalModels =
        LinkedHashMap<String, FeedMediaGridModel>(FeedMediaLocalModelCacheCapacity, 0.75f, true)

    val mediaModels: StateFlow<Map<String, FeedMediaGridModel>> = observedSpecs
        .flatMapLatest { specs ->
            if (specs.isEmpty()) {
                recentLocalModels.clear()
                flowOf(emptyMap())
            } else {
                combine(
                    db.androidSyncDao().assetsForOwnersFlow(
                        ownerKind = "tweet",
                        ownerIds = specs.map { it.ownerId },
                    ),
                    reachability.state,
                ) { assetRows, reachabilityState ->
                        val rowsByOwner = assetRows.groupBy { it.ownerId }
                        val activeModels =
                            specs.associate { spec ->
                                spec.ownerId to buildFeedMediaGridModel(
                                    ownerId = spec.ownerId,
                                    rawJson = spec.rawJson,
                                    assetRows = rowsByOwner[spec.ownerId].orEmpty(),
                                    baseUrl = baseUrlProvider.baseUrl(),
                                    allowRemote = reachabilityState is Reachability.State.Online,
                                )
                            }
                        retainRecentLocalModels(activeModels)
                        recentLocalModels + activeModels
                }
            }
        }
        .flowOn(Dispatchers.Default)
        .stateIn(scope, SharingStarted.Eagerly, emptyMap())

    fun setMediaModelRows(rows: List<FeedRow>) {
        observedSpecs.value = rows
            .flatMap(::feedMediaOwnerSpecs)
            .distinctBy { it.ownerId }
    }

    private fun retainRecentLocalModels(activeModels: Map<String, FeedMediaGridModel>) {
        activeModels.forEach { (ownerId, model) ->
            if (model.hasOnlyLocalPreviewItems()) {
                recentLocalModels[ownerId] = model
            } else {
                recentLocalModels.remove(ownerId)
            }
        }
        while (recentLocalModels.size > FeedMediaLocalModelCacheCapacity) {
            val iterator = recentLocalModels.entries.iterator()
            iterator.next()
            iterator.remove()
        }
    }
}

private const val FeedMediaLocalModelCacheCapacity = 72

private fun FeedMediaGridModel.hasOnlyLocalPreviewItems(): Boolean =
    cells.isNotEmpty() &&
        cells.all { cell ->
            when (val item = cell.previewItem) {
                is MediaItem.Image -> item.uri is MediaUri.Local
                is MediaItem.Video ->
                    item.streamUri is MediaUri.Local &&
                        (item.thumbnailUri is MediaUri.Local || item.thumbnailUri is MediaUri.Missing)
                is MediaItem.Gif -> item.streamUri is MediaUri.Local
                null -> false
            }
        }

internal data class FeedMediaOwnerSpec(
    val ownerId: String,
    val rawJson: String?,
)

internal fun feedMediaOwnerSpecs(row: FeedRow): List<FeedMediaOwnerSpec> = buildList {
    row.item.mediaJson
        ?.takeIf { it.isNotBlank() }
        ?.let { raw -> add(FeedMediaOwnerSpec(row.item.tweetId, raw)) }
    val quoteId = row.item.quoteTweetId?.takeIf { it.isNotBlank() }
    val quoteJson = row.item.quoteMediaJson?.takeIf { it.isNotBlank() }
    if (quoteId != null && quoteJson != null) {
        add(FeedMediaOwnerSpec(quoteId, quoteJson))
    }
}
