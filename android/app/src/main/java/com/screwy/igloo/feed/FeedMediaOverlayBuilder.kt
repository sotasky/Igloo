package com.screwy.igloo.feed

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.androidSyncAssetPath
import com.screwy.igloo.ui.component.MediaItem
import com.screwy.igloo.ui.component.MediaSet
import kotlinx.coroutines.flow.first
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

    fun streamAspectRatioOrNull(): Float? = aspectRatioFromUrl(url)

    fun displayAspectRatioOrNull(isVideo: Boolean): Float? =
        if (isVideo) streamAspectRatioOrNull() ?: aspectRatioOrNull() else aspectRatioOrNull()

    fun displayAspectRatio(isVideo: Boolean, defaultValue: Float = 1f): Float =
        displayAspectRatioOrNull(isVideo) ?: defaultValue
}

data class FeedMediaCellDescriptor(
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
        val aspectRatio = descriptor.displayAspectRatioOrNull(isVideo)
        FeedMediaCellDescriptor(
            isVideo = isVideo,
            aspectRatio = aspectRatio ?: FeedUnknownMediaAspectRatio,
            aspectRatioKnown = aspectRatio != null,
        )
    }

internal const val FeedUnknownMediaAspectRatio: Float = 1f

internal fun buildFeedMediaSet(
    row: FeedRow,
    assetRows: List<AndroidSyncAssetEntity>,
    baseUrl: String,
    allowRemote: Boolean = true,
): MediaSet? {
    val parentItems = buildOwnerItems(
        ownerId = row.item.tweetId,
        rawJson = row.item.mediaJson,
        assetRows = assetRows.filter { it.ownerId == row.item.tweetId },
        baseUrl = baseUrl,
        allowRemote = allowRemote,
    )
    val quoteItems = buildOwnerItems(
        ownerId = row.item.quoteTweetId,
        rawJson = row.item.quoteMediaJson,
        assetRows = assetRows.filter { it.ownerId == row.item.quoteTweetId },
        baseUrl = baseUrl,
        allowRemote = allowRemote,
    )
    val items = parentItems + quoteItems
    if (items.isEmpty()) return null

    return MediaSet(items = items)
}

internal suspend fun loadFeedMediaAssetRows(
    db: IglooDatabase,
    row: FeedRow,
): List<AndroidSyncAssetEntity> = buildList {
    addAll(
        db.androidSyncDao().assetsForOwnerFlow("tweet", row.item.tweetId)
            .first()
            .filter { it.assetKind == "post_media" || it.assetKind == "post_thumbnail" },
    )
    val quoteId = row.item.quoteTweetId
    if (!quoteId.isNullOrBlank()) {
        addAll(
            db.androidSyncDao().assetsForOwnerFlow("tweet", quoteId)
                .first()
                .filter { it.assetKind == "post_media" || it.assetKind == "post_thumbnail" },
        )
    }
}

internal fun buildFeedPreviewItemsByIndex(
    ownerId: String?,
    rawJson: String?,
    assetRows: List<AndroidSyncAssetEntity>,
    baseUrl: String,
    allowRemote: Boolean = true,
): Map<Int, MediaItem> = buildOwnerItemsByIndex(
    ownerId = ownerId,
    rawJson = rawJson,
    assetRows = assetRows,
    baseUrl = baseUrl,
    allowRemote = allowRemote,
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
    assetRows: List<AndroidSyncAssetEntity>,
    baseUrl: String,
    allowRemote: Boolean,
): List<MediaItem> = buildOwnerItemsByIndex(
    ownerId = ownerId,
    rawJson = rawJson,
    assetRows = assetRows,
    baseUrl = baseUrl,
    allowRemote = allowRemote,
).values.toList()

private fun buildOwnerItemsByIndex(
    ownerId: String?,
    rawJson: String?,
    assetRows: List<AndroidSyncAssetEntity>,
    baseUrl: String,
    allowRemote: Boolean,
): Map<Int, MediaItem> {
    if (ownerId.isNullOrBlank()) return emptyMap()
    val descriptors = parseFeedMediaDescriptors(rawJson)
    val rowsByIndex = latestSyncRowsByIndex(assetRows, "post_media")
    val thumbnailRow = latestSyncRowsByIndex(assetRows, "post_thumbnail")[0]
    val count = if (descriptors.isNotEmpty()) {
        descriptors.size
    } else {
        maxOf(
            (rowsByIndex.keys.maxOrNull() ?: -1) + 1,
        )
    }
    if (count == 0) return emptyMap()

    return buildMap(count) {
        for (index in 0 until count) {
            val descriptor = descriptors.getOrNull(index)
            val row = rowsByIndex[index]
            val item = buildMediaItem(descriptor, row, thumbnailRow, baseUrl, allowRemote)
            if (item != null) put(index, item)
        }
    }
}

private fun buildMediaItem(
    descriptor: FeedMediaDescriptor?,
    row: AndroidSyncAssetEntity?,
    thumbnailRow: AndroidSyncAssetEntity?,
    baseUrl: String,
    allowRemote: Boolean,
): MediaItem? {
    if (row == null) return null
    val mediaUri = row.toMediaUri(baseUrl, allowRemote) ?: return null
    val contentType = row.contentType?.trim()?.lowercase().orEmpty()
    val isVideo = contentType.startsWith("video/") || contentType == "image/gif"
    val aspectRatio = descriptor?.displayAspectRatio(isVideo) ?: 1f

    return when {
        contentType.startsWith("video/") -> MediaItem.Video(
            streamUri = mediaUri,
            thumbnailUri = thumbnailRow.toMediaUri(baseUrl, allowRemote) ?: MediaUri.Missing,
            aspectRatio = aspectRatio,
        )
        contentType == "image/gif" -> MediaItem.Gif(
            streamUri = mediaUri,
            aspectRatio = aspectRatio,
        )
        contentType.startsWith("image/") -> MediaItem.Image(
            uri = mediaUri,
            aspectRatio = aspectRatio,
        )
        else -> null
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

private fun AndroidSyncAssetEntity?.toMediaUri(baseUrl: String, allowRemote: Boolean): MediaUri? {
    if (this == null) return null
    if (!localPath.isNullOrEmpty()) {
        return MediaUri.Local(java.io.File(localPath))
    }
    if (state == "server_missing" || !allowRemote) return null
    val root = baseUrl.trim().trimEnd('/')
    if (root.isBlank()) return null
    return MediaUri.Remote(root + androidSyncAssetPath(assetId, revision))
}

private fun latestSyncRowsByIndex(
    rows: List<AndroidSyncAssetEntity>,
    assetKind: String,
): Map<Int, AndroidSyncAssetEntity> {
    if (rows.isEmpty()) return emptyMap()
    val result = LinkedHashMap<Int, AndroidSyncAssetEntity>()
    rows.asSequence()
        .filter { it.assetKind == assetKind }
        .sortedWith(
            compareByDescending<AndroidSyncAssetEntity> { it.verifiedAtMs ?: 0L }
                .thenByDescending { it.revision }
                .thenBy { it.assetId },
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

private fun assetIndex(row: AndroidSyncAssetEntity): Int = row.mediaIndex
