package com.screwy.igloo.feed

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.net.ServerBaseUrlProvider
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal class FeedMediaModelStore(
    private val db: IglooDatabase,
    private val baseUrlProvider: ServerBaseUrlProvider,
    private val scope: CoroutineScope,
) {
    private val _mediaModels = MutableStateFlow<Map<String, FeedMediaGridModel>>(emptyMap())
    val mediaModels: StateFlow<Map<String, FeedMediaGridModel>> = _mediaModels.asStateFlow()

    private val mediaModelWarmTimesMs = mutableMapOf<String, Long>()

    fun warmMediaModels(rows: List<FeedRow>) {
        val specs = rows
            .flatMap(::feedMediaOwnerSpecs)
            .distinctBy { it.ownerId }
        if (specs.isEmpty()) return

        publishFallbackModels(specs)

        val now = System.currentTimeMillis()
        val staleSpecs = specs.filter { spec ->
            val previous = mediaModelWarmTimesMs[spec.ownerId] ?: 0L
            now - previous >= MEDIA_MODEL_REWARM_MS
        }
        if (staleSpecs.isEmpty()) return
        staleSpecs.forEach { spec -> mediaModelWarmTimesMs[spec.ownerId] = now }

        scope.launch {
            val built = withContext(Dispatchers.IO) {
                buildFeedMediaGridModels(
                    db = db,
                    baseUrl = baseUrlProvider.baseUrl(),
                    specs = staleSpecs,
                )
            }
            if (built.isNotEmpty()) {
                _mediaModels.value = _mediaModels.value + built
            }
        }
    }

    private fun publishFallbackModels(specs: List<FeedMediaOwnerSpec>) {
        val current = _mediaModels.value
        val missing = specs
            .filterNot { current.containsKey(it.ownerId) }
            .associate { spec ->
                spec.ownerId to fallbackFeedMediaGridModel(
                    ownerId = spec.ownerId,
                    rawJson = spec.rawJson,
                )
            }
        if (missing.isNotEmpty()) {
            _mediaModels.value = current + missing
        }
    }

    private companion object {
        const val MEDIA_MODEL_REWARM_MS: Long = 30_000L
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

private suspend fun buildFeedMediaGridModels(
    db: IglooDatabase,
    baseUrl: String,
    specs: List<FeedMediaOwnerSpec>,
): Map<String, FeedMediaGridModel> =
    buildMap {
        specs.forEach { spec ->
            val inventoryRows = db.mediaInventoryDao().forOwner(spec.ownerId)
            val syncRows = db.androidSyncDao()
                .latestVerifiedAssetsForOwner(spec.ownerId, listOf("post_media"))
            put(
                spec.ownerId,
                buildFeedMediaGridModel(
                    ownerId = spec.ownerId,
                    rawJson = spec.rawJson,
                    inventoryRows = inventoryRows,
                    syncAssetRows = syncRows,
                    baseUrl = baseUrl,
                ),
            )
        }
    }
