package com.screwy.igloo.feed

import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.component.MediaItem
import com.screwy.igloo.ui.component.MediaSet
import com.screwy.igloo.ui.nav.MediaOpenSnapshot

data class FeedMediaCellModel(
    val descriptor: FeedMediaCellDescriptor,
    val previewItem: MediaItem?,
)

data class FeedMediaGridModel(
    val ownerId: String,
    val cells: List<FeedMediaCellModel>,
    val inventoryLoaded: Boolean,
) {
    val mediaCount: Int
        get() = cells.size

    fun posterUriFor(index: Int): MediaUri {
        val cell = cells.getOrNull(index.coerceAtLeast(0)) ?: return MediaUri.Missing
        return mediaPosterUri(
            descriptor = cell.descriptor,
            previewItem = cell.previewItem,
        )
    }
}

internal fun buildFeedMediaGridModel(
    ownerId: String,
    rawJson: String?,
    inventoryRows: List<MediaInventoryEntity>,
    syncAssetRows: List<AndroidSyncAssetEntity>,
    baseUrl: String,
): FeedMediaGridModel {
    val descriptors = describeFeedMediaCells(rawJson)
    val previewItems = buildFeedPreviewItems(
        ownerId = ownerId,
        rawJson = rawJson,
        inventoryRows = inventoryRows.filter { it.assetKind == "post_media" },
        baseUrl = baseUrl,
        syncAssetRows = syncAssetRows,
    )
    return FeedMediaGridModel(
        ownerId = ownerId,
        inventoryLoaded = true,
        cells = descriptors.mapIndexed { index, descriptor ->
            FeedMediaCellModel(
                descriptor = descriptor,
                previewItem = previewItems.getOrNull(index),
            )
        },
    )
}

internal fun fallbackFeedMediaGridModel(
    ownerId: String,
    rawJson: String?,
): FeedMediaGridModel =
    FeedMediaGridModel(
        ownerId = ownerId,
        inventoryLoaded = false,
        cells = describeFeedMediaCells(rawJson).map { descriptor ->
            FeedMediaCellModel(
                descriptor = descriptor,
                previewItem = null,
            )
        },
    )

internal fun buildWarmFeedMediaSet(
    row: FeedRow,
    mediaModels: Map<String, FeedMediaGridModel>,
): MediaSet? {
    val parentModel = mediaModels[row.item.tweetId]
        ?: fallbackFeedMediaGridModel(row.item.tweetId, row.item.mediaJson)
    val quoteId = row.item.quoteTweetId.orEmpty()
    val quoteModel = if (quoteId.isNotBlank()) {
        mediaModels[quoteId] ?: fallbackFeedMediaGridModel(quoteId, row.item.quoteMediaJson)
    } else {
        null
    }
    val parentItems = parentModel.toWarmMediaItems()
    val quoteItems = quoteModel?.toWarmMediaItems().orEmpty()
    val items = parentItems + quoteItems
    if (items.isEmpty()) return null

    val displayName = row.item.authorDisplayName
        ?.trim()
        ?.takeIf { it.isNotBlank() }
        ?: row.channelName?.trim()?.takeIf { it.isNotBlank() }
        ?: ""
    return MediaSet(
        items = items,
        parentMediaCount = parentItems.size,
        parentIsTextOnly = parentItems.isEmpty(),
        authorHandle = row.item.authorHandle,
        authorDisplayName = displayName,
        authorChannelId = row.item.channelId.orEmpty(),
        bodyText = row.item.bodyText.orEmpty(),
        quoteBodyText = row.item.quoteBodyText.orEmpty(),
        canonicalUrl = canonicalTweetUrl(row.item),
        quoteCanonicalUrl = quoteCanonicalTweetUrl(row.item),
    )
}

internal fun buildFeedMediaOpenSnapshot(
    row: FeedRow,
    mediaIndex: Int,
    mediaModels: Map<String, FeedMediaGridModel> = emptyMap(),
    visibleMediaModel: FeedMediaGridModel? = null,
): MediaOpenSnapshot {
    val parentCount = countFeedMediaItems(row.item.mediaJson)
    val quoteCount = countFeedMediaItems(row.item.quoteMediaJson)
    val effectiveModels = mediaModels.toMutableMap()
    if (visibleMediaModel != null) {
        if (mediaIndex < parentCount) {
            effectiveModels[row.item.tweetId] = visibleMediaModel
        } else {
            row.item.quoteTweetId
                ?.takeIf { it.isNotBlank() }
                ?.let { quoteId -> effectiveModels[quoteId] = visibleMediaModel }
        }
    }

    val posterUri = if (mediaIndex < parentCount) {
        val model = effectiveModels[row.item.tweetId]
            ?: fallbackFeedMediaGridModel(row.item.tweetId, row.item.mediaJson)
        model.posterUriFor(mediaIndex)
    } else {
        val quoteIndex = mediaIndex - parentCount
        val quoteId = row.item.quoteTweetId.orEmpty()
        val model = effectiveModels[quoteId]
            ?: fallbackFeedMediaGridModel(quoteId, row.item.quoteMediaJson)
        model.posterUriFor(quoteIndex)
    }

    return MediaOpenSnapshot(
        ownerKind = "tweet",
        ownerId = row.item.tweetId,
        index = mediaIndex.coerceAtLeast(0),
        mediaCount = parentCount + quoteCount,
        posterUri = posterUri,
        isLiked = row.isLiked == 1,
        isBookmarked = row.isBookmarked == 1,
        mediaSet = buildWarmFeedMediaSet(row, effectiveModels),
    )
}

private fun FeedMediaGridModel.toWarmMediaItems(): List<MediaItem> =
    cells.mapNotNull { cell -> cell.previewItem ?: cell.descriptor.toWarmMediaItem() }

private fun FeedMediaCellDescriptor.toWarmMediaItem(): MediaItem? {
    val posterUri = posterUrl
        .takeIf { it.isNotBlank() }
        ?.let(MediaUri::Remote)
        ?: displayUrl.takeIf { it.isNotBlank() }?.let(MediaUri::Remote)
        ?: MediaUri.Missing
    return if (isVideo) {
        streamUrl
            .takeIf { it.isNotBlank() }
            ?.let { MediaItem.Video(MediaUri.Remote(it), posterUri, aspectRatio) }
    } else {
        displayUrl
            .takeIf { it.isNotBlank() }
            ?.let { MediaItem.Image(MediaUri.Remote(it), aspectRatio) }
    }
}

internal fun mediaPosterUri(
    descriptor: FeedMediaCellDescriptor,
    previewItem: MediaItem?,
): MediaUri {
    when (previewItem) {
        is MediaItem.Image -> return previewItem.uri
        is MediaItem.Video -> {
            if (previewItem.thumbnailUri !is MediaUri.Missing) return previewItem.thumbnailUri
        }
        is MediaItem.Gif -> Unit
        null -> Unit
    }
    val fallback = descriptor.posterUrl.ifBlank { descriptor.displayUrl }
    return fallback.takeIf { it.isNotBlank() }?.let(MediaUri::Remote) ?: MediaUri.Missing
}
