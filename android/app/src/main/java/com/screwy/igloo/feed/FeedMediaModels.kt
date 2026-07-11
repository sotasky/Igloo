package com.screwy.igloo.feed

import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.ui.component.MediaItem

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

    val mediaSetItemCount: Int
        get() = cells.count { it.previewItem != null }
}

internal fun buildFeedMediaGridModel(
    ownerId: String,
    rawJson: String?,
    assetRows: List<AndroidSyncAssetEntity>,
    baseUrl: String,
    allowRemote: Boolean = true,
): FeedMediaGridModel {
    val descriptors = describeFeedMediaCells(rawJson)
    val previewItemsByIndex = buildFeedPreviewItemsByIndex(
        ownerId = ownerId,
        rawJson = rawJson,
        assetRows = assetRows,
        baseUrl = baseUrl,
        allowRemote = allowRemote,
    )
    return FeedMediaGridModel(
        ownerId = ownerId,
        inventoryLoaded = true,
        cells = descriptors.mapIndexed { index, descriptor ->
            val previewItem = previewItemsByIndex[index]
            FeedMediaCellModel(
                descriptor = when (previewItem) {
                    is MediaItem.Video, is MediaItem.Gif -> descriptor.copy(isVideo = true)
                    is MediaItem.Image -> descriptor.copy(isVideo = false)
                    null -> descriptor
                },
                previewItem = previewItem,
            )
        },
    )
}

internal fun feedMediaViewerIndex(
    grid: FeedMediaGridModel,
    cellIndex: Int,
    mediaIndexOffset: Int,
): Int? {
    if (grid.cells.getOrNull(cellIndex)?.previewItem == null) return null
    return mediaIndexOffset + grid.cells.take(cellIndex).count { it.previewItem != null }
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
