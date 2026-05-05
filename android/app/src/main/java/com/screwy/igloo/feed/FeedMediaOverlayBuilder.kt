package com.screwy.igloo.feed

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.component.MediaItem
import com.screwy.igloo.ui.component.MediaSet
import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive

private val feedMediaJson = Json { ignoreUnknownKeys = true }

internal data class FeedMediaDescriptor(
    val type: String,
    val url: String?,
    val thumbnailUrl: String?,
    val width: Int?,
    val height: Int?,
) {
    fun aspectRatioOrNull(): Float? =
        aspectRatio(width, height)
            ?: aspectRatioFromUrl(url)
            ?: aspectRatioFromUrl(thumbnailUrl)

    fun aspectRatio(defaultValue: Float = 1f): Float {
        return aspectRatioOrNull() ?: defaultValue
    }
}

/**
 * Flattened per-cell descriptor used by the feed card's inline grid — drops the
 * parser's type-specific branching so the grid only has to ask "what URL do I
 * paint, and is it a video cell?". For video/gif descriptors the `url` field is
 * an mp4 stream that Coil cannot decode, so [displayUrl] always prefers
 * `thumbnail_url` and falls back to `url` only for photos.
 */
data class FeedMediaCellDescriptor(
    val displayUrl: String,
    val streamUrl: String,
    val posterUrl: String,
    val isVideo: Boolean,
    val aspectRatio: Float,
    val aspectRatioKnown: Boolean = true,
)

/**
 * Flattens `media_json` into one [FeedMediaCellDescriptor] per slide. The grid
 * component consumes this directly; no inventory plumbing is required at
 * render time because the media route builds the inventory-aware MediaSet when the
 * user actually taps through.
 */
internal fun describeFeedMediaCells(rawJson: String?): List<FeedMediaCellDescriptor> =
    parseFeedMediaDescriptors(rawJson).map { descriptor ->
        val type = descriptor.type.lowercase()
        val isVideo = type == "video" || type == "gif" || type == "animated_gif"
        val thumb = descriptor.thumbnailUrl?.takeIf { it.isNotBlank() }
        val aspectRatio = descriptor.aspectRatioOrNull()
        val streamUrl = if (isVideo) descriptor.url?.takeIf { it.isNotBlank() }.orEmpty() else ""
        val display = when {
            thumb != null -> thumb
            isVideo -> ""
            else -> descriptor.url?.takeIf { it.isNotBlank() }.orEmpty()
        }
        FeedMediaCellDescriptor(
            displayUrl = display,
            streamUrl = streamUrl,
            posterUrl = thumb.orEmpty(),
            isVideo = isVideo,
            aspectRatio = aspectRatio ?: FeedUnknownMediaAspectRatio,
            aspectRatioKnown = aspectRatio != null,
        )
    }

internal const val FeedUnknownMediaAspectRatio: Float = 1f

internal fun buildFeedMediaSet(
    row: FeedRow,
    inventoryRows: List<MediaInventoryEntity>,
    baseUrl: String,
): MediaSet? {
    val parentItems = buildOwnerItems(
        ownerId = row.item.tweetId,
        rawJson = row.item.mediaJson,
        inventoryRows = inventoryRows.filter { it.ownerId == row.item.tweetId && it.assetKind == "post_media" },
        baseUrl = baseUrl,
    )
    val quoteItems = buildOwnerItems(
        ownerId = row.item.quoteTweetId,
        rawJson = row.item.quoteMediaJson,
        inventoryRows = inventoryRows.filter { it.ownerId == row.item.quoteTweetId && it.assetKind == "post_media" },
        baseUrl = baseUrl,
    )
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

internal suspend fun loadFeedMediaInventoryRows(
    db: IglooDatabase,
    row: FeedRow,
): List<MediaInventoryEntity> = buildList {
    addAll(db.mediaInventoryDao().forOwner(row.item.tweetId))
    val quoteId = row.item.quoteTweetId
    if (!quoteId.isNullOrBlank()) {
        addAll(db.mediaInventoryDao().forOwner(quoteId))
    }
}

internal fun buildFeedPreviewItems(
    ownerId: String?,
    rawJson: String?,
    inventoryRows: List<MediaInventoryEntity>,
    baseUrl: String,
    syncAssetRows: List<AndroidSyncAssetEntity> = emptyList(),
): List<MediaItem> = buildOwnerItems(
    ownerId = ownerId,
    rawJson = rawJson,
    inventoryRows = inventoryRows,
    baseUrl = baseUrl,
    syncAssetRows = syncAssetRows,
)

internal fun canonicalTweetUrl(item: FeedItemEntity): String =
    item.canonicalUrl?.trim().orEmpty()

internal fun quoteCanonicalTweetUrl(item: FeedItemEntity): String =
    item.quoteCanonicalUrl?.trim().orEmpty()

internal fun mediaViewerInitialIndex(
    row: FeedRow,
    tappedIndex: Int,
): Int {
    @Suppress("UNUSED_PARAMETER") row
    return tappedIndex.coerceAtLeast(0)
}

internal fun feedMediaCount(item: FeedItemEntity): Int =
    countFeedMediaItems(item.mediaJson) + countFeedMediaItems(item.quoteMediaJson)

internal fun countFeedMediaItems(rawJson: String?): Int =
    parseFeedMediaDescriptors(rawJson).size

private fun buildOwnerItems(
    ownerId: String?,
    rawJson: String?,
    inventoryRows: List<MediaInventoryEntity>,
    baseUrl: String,
    syncAssetRows: List<AndroidSyncAssetEntity> = emptyList(),
): List<MediaItem> {
    if (ownerId.isNullOrBlank()) return emptyList()
    val descriptors = parseFeedMediaDescriptors(rawJson)
    val rowsByIndex = inventoryRows.sortedBy(::assetIndex)
    val syncRowsByIndex = latestSyncRowsByIndex(syncAssetRows)
    val count = maxOf(
        descriptors.size,
        rowsByIndex.size,
        (syncRowsByIndex.keys.maxOrNull() ?: -1) + 1,
    )
    if (count == 0) return emptyList()

    return buildList(count) {
        for (index in 0 until count) {
            val descriptor = descriptors.getOrNull(index)
            val row = rowsByIndex.getOrNull(index)
            val syncRow = syncRowsByIndex[index]
            val item = buildMediaItem(descriptor, row, syncRow, baseUrl)
            if (item != null) add(item)
        }
    }
}

private fun buildMediaItem(
    descriptor: FeedMediaDescriptor?,
    row: MediaInventoryEntity?,
    syncRow: AndroidSyncAssetEntity?,
    baseUrl: String,
): MediaItem? {
    val type = descriptor?.type.orEmpty()
    val aspectRatio = descriptor?.aspectRatio() ?: 1f
    val mediaUri = syncRow.toMediaUri(baseUrl) ?: row.toMediaUri(baseUrl) ?: descriptor?.remoteUri()
    if (mediaUri == null) return null

    return when (type.lowercase()) {
        "video" -> MediaItem.Video(
            streamUri = mediaUri,
            thumbnailUri = descriptor?.thumbnailUrl?.let(MediaUri::Remote) ?: MediaUri.Missing,
            aspectRatio = aspectRatio,
        )
        "gif", "animated_gif" -> MediaItem.Gif(
            streamUri = mediaUri,
            aspectRatio = aspectRatio,
        )
        else -> MediaItem.Image(
            uri = mediaUri,
            aspectRatio = aspectRatio,
        )
    }
}

internal fun parseFeedMediaDescriptors(rawJson: String?): List<FeedMediaDescriptor> {
    if (rawJson.isNullOrBlank()) return emptyList()
    val element = runCatching { feedMediaJson.parseToJsonElement(rawJson) }.getOrNull() ?: return emptyList()
    val array = element as? JsonArray ?: return emptyList()
    return array.mapNotNull(::parseFeedMediaDescriptor)
}

private fun parseFeedMediaDescriptor(element: JsonElement): FeedMediaDescriptor? {
    val obj = element as? JsonObject ?: return null
    val streamUrl = obj.string("stream_url")
    val url = streamUrl?.takeIf { it.isNotBlank() } ?: obj.string("url")
    val thumbnailUrl = obj.string("preview_url")
        ?: obj.string("thumbnail_url")
        ?: obj.string("thumbnail")
    val type = when {
        !obj.string("type").isNullOrBlank() -> obj.string("type").orEmpty()
        !obj.string("kind").isNullOrBlank() -> obj.string("kind").orEmpty()
        url?.contains("/video.twimg.com/", ignoreCase = true) == true -> "video"
        else -> "photo"
    }
    return FeedMediaDescriptor(
        type = type,
        url = url,
        thumbnailUrl = thumbnailUrl,
        width = obj.int("width"),
        height = obj.int("height"),
    )
}

private fun JsonObject.string(key: String): String? =
    get(key)?.jsonPrimitive?.contentOrNull

private fun JsonObject.int(key: String): Int? =
    get(key)?.jsonPrimitive?.intOrNull

private fun FeedMediaDescriptor.remoteUri(): MediaUri? =
    url?.takeIf { it.isNotBlank() }?.let(MediaUri::Remote)

private fun MediaInventoryEntity?.toMediaUri(baseUrl: String): MediaUri? {
    if (this == null) return null
    if (state == "cached" && !localPath.isNullOrEmpty()) {
        val file = File(localPath)
        if (file.exists()) return MediaUri.Local(file)
    }
    return MediaUri.Remote(baseUrl + serverUrl)
}

private fun AndroidSyncAssetEntity?.toMediaUri(baseUrl: String): MediaUri? {
    if (this == null) return null
    if (state == "verified" && !localPath.isNullOrEmpty()) {
        val file = File(localPath)
        if (file.exists()) return MediaUri.Local(file)
    }
    if (serverState != "ready") return null
    return MediaUri.Remote(baseUrl + serverUrl)
}

private fun latestSyncRowsByIndex(rows: List<AndroidSyncAssetEntity>): Map<Int, AndroidSyncAssetEntity> {
    if (rows.isEmpty()) return emptyMap()
    val result = LinkedHashMap<Int, AndroidSyncAssetEntity>()
    rows.asSequence()
        .filter { it.assetKind == "post_media" }
        .sortedWith(
            compareByDescending<AndroidSyncAssetEntity> { it.verifiedAtMs ?: 0L }
                .thenByDescending { it.generationId }
                .thenBy { it.seq },
        )
        .forEach { row -> result.putIfAbsent(assetIndex(row), row) }
    return result
}

private val dimensionPattern = Regex("""/(\d{2,4})x(\d{2,4})(?:/|$)""")

private fun aspectRatio(width: Int?, height: Int?): Float? {
    val w = width ?: return null
    val h = height ?: return null
    if (w <= 0 || h <= 0) return null
    return w.toFloat() / h.toFloat()
}

private fun aspectRatioFromUrl(rawUrl: String?): Float? {
    val url = rawUrl?.trim()?.takeIf { it.isNotBlank() } ?: return null
    val match = dimensionPattern.find(url) ?: return null
    val width = match.groupValues.getOrNull(1)?.toIntOrNull() ?: return null
    val height = match.groupValues.getOrNull(2)?.toIntOrNull() ?: return null
    return aspectRatio(width, height)
}

private fun assetIndex(row: MediaInventoryEntity): Int {
    val suffix = row.assetId.substringAfterLast('_', missingDelimiterValue = "")
    return suffix.toIntOrNull() ?: 0
}

private fun assetIndex(row: AndroidSyncAssetEntity): Int {
    val suffix = row.assetId.substringAfterLast('_', missingDelimiterValue = "")
    return suffix.toIntOrNull() ?: androidSyncSlideIndex(row.serverUrl)
}

private fun androidSyncSlideIndex(serverUrl: String): Int {
    val suffix = serverUrl.trimEnd('/').substringAfterLast('/', missingDelimiterValue = "")
    return suffix.toIntOrNull() ?: 0
}
