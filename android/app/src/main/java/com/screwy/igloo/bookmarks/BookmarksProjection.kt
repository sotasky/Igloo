package com.screwy.igloo.bookmarks

import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.BookmarkItem
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.feed.parseFeedMediaDescriptors
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.ui.component.MediaCellModel
import com.screwy.igloo.ui.component.MomentItem
import com.screwy.igloo.ui.component.mediaTypeFor
import com.screwy.igloo.ui.component.normalizeHandle

/**
 * Bookmark media type — videos resolve directly off `videos.media_kind` /
 * `slide_count`; tweets fall back to the descriptor-count heuristic that's been
 * the bookmark module's slideshow detector (multi-photo tweet = slideshow,
 * single photo = image, else video/gif).
 */
internal fun bookmarkMediaType(item: BookmarkItem) = when (val video = item.video) {
    null -> mediaTypeFor(bookmarkFeedMediaKind(item), bookmarkFeedSlideCount(item))
    else -> mediaTypeFor(video.mediaMode?.takeIf { it.isNotBlank() } ?: video.mediaKind, video.slideCount)
}

internal fun bookmarkPublishedAt(item: BookmarkItem): Long =
    item.video?.publishedAt
        ?: item.feedItem?.publishedAt
        ?: 0L

internal fun opensBookmarkInMomentsOverlay(item: BookmarkItem): Boolean =
    item.video != null || item.feedItem?.hasMomentStyleMedia() == true

internal fun toBookmarkMomentItem(item: BookmarkItem, baseUrl: String = ""): MomentItem {
    val feedMediaKind = bookmarkFeedMediaKind(item)
    val feedSlideCount = bookmarkFeedSlideCount(item)
    val channelId = bookmarkChannelId(item)
    val description = item.video?.description
        ?.takeIf { !it.isNullOrBlank() }
        ?: item.video?.title
        ?: item.feedItem?.bodyText
        ?: item.bookmark.customTitle
        ?: ""
    val likeCount = item.feedItem?.likes
        ?.coerceIn(0L, Int.MAX_VALUE.toLong())
        ?.toInt()

    return MomentItem(
        videoId = item.bookmark.videoId,
        channelId = channelId,
        canonicalUrl = item.video?.canonicalUrl?.takeIf { it.isNotBlank() }
            ?: item.feedItem?.canonicalUrl.orEmpty(),
        authorDisplayName = bookmarkAuthorDisplayName(item),
        authorHandle = bookmarkAuthorHandle(item, channelId),
        description = description,
        likeCount = likeCount,
        isLiked = false,
        isBookmarked = true,
        mediaKind = item.video?.mediaMode?.takeIf { it.isNotBlank() } ?: item.video?.mediaKind ?: feedMediaKind,
        slideCount = item.video?.slideCount ?: feedSlideCount,
        publishedAt = item.video?.publishedAt ?: item.feedItem?.publishedAt ?: 0L,
        ownerKind = bookmarkOwnerKind(item),
        fallbackThumbnailUri = item.initialThumbnailUri(baseUrl),
        isAuthorFollowed = item.resolvedChannelIsFollowed == 1,
    )
}

internal fun bookmarkFallbackPlatform(item: BookmarkItem): String {
    val channelId = item.resolvedChannelId ?: item.video?.channelId ?: item.feedItem?.channelId
    return channelId
        ?.substringBefore('_')
        ?.takeIf { it.isNotBlank() }
        ?: if (item.feedItem != null) "twitter" else "youtube"
}

internal fun bookmarkOwnerKind(item: BookmarkItem): OwnerKind {
    if (item.feedItem != null) return OwnerKind.Tweet
    return when (bookmarkFallbackPlatform(item)) {
        "tiktok" -> OwnerKind.TikTokVideo
        "instagram" -> OwnerKind.InstagramReel
        "twitter", "x" -> OwnerKind.Tweet
        else -> OwnerKind.YouTubeVideo
    }
}

internal fun bookmarkChannelId(item: BookmarkItem): String {
    val channelId = item.resolvedChannelId ?: item.video?.channelId ?: item.feedItem?.channelId
    if (!channelId.isNullOrBlank()) return channelId
    val handle = normalizeHandle(
        item.feedItem?.authorHandle
            ?: item.resolvedChannelSourceId
            ?: item.bookmark.videoId,
    ).ifBlank { item.bookmark.videoId }
    return "${bookmarkFallbackPlatform(item)}_$handle"
}

internal fun bookmarkAuthorDisplayName(item: BookmarkItem): String? =
    preferredBookmarkDisplayName(
        primary = item.feedItem?.authorDisplayName,
        channelName = item.resolvedChannelName,
        handle = item.feedItem?.authorHandle
            ?: item.feedItem?.sourceHandle
            ?: item.resolvedChannelSourceId,
    )

internal fun bookmarkAuthorHandle(item: BookmarkItem, channelId: String): String {
    val handle = normalizeHandle(
        item.feedItem?.authorHandle
            ?: item.feedItem?.sourceHandle
            ?: item.resolvedChannelSourceId
            ?: stripPlatformPrefix(channelId),
    )
    return if (handle.isNotBlank()) "@$handle" else item.bookmark.videoId
}

private fun bookmarkFeedMediaKind(item: BookmarkItem): String? {
    val descriptors = bookmarkFeedMediaDescriptors(item)
    if (descriptors.isEmpty()) return null
    if (descriptors.size > 1) return "slideshow"

    return when (descriptors.first().type.trim().lowercase()) {
        "photo" -> "image"
        else -> descriptors.first().type.trim().lowercase()
    }
}

private fun bookmarkFeedSlideCount(item: BookmarkItem): Int {
    val descriptors = bookmarkFeedMediaDescriptors(item)
    return when {
        descriptors.size > 1 -> descriptors.size
        bookmarkFeedMediaKind(item) == "image" -> 1
        else -> 0
    }
}

private fun bookmarkFeedMediaDescriptors(item: BookmarkItem) = item.feedItem?.let { feedItem ->
    parseFeedMediaDescriptors(feedItem.mediaJson)
        .ifEmpty { parseFeedMediaDescriptors(feedItem.quoteMediaJson) }
}.orEmpty()

private fun preferredBookmarkDisplayName(
    primary: String?,
    channelName: String?,
    handle: String?,
): String? {
    val normalizedHandle = normalizeHandle(handle)
    val candidates = listOf(primary, channelName)
    return candidates
        .mapNotNull { candidate -> candidate?.trim()?.removePrefix("@")?.takeIf { it.isNotBlank() } }
        .firstOrNull { !it.equals(normalizedHandle, ignoreCase = true) }
        ?: candidates.firstOrNull { !it.isNullOrBlank() }?.trim()?.removePrefix("@")
}

internal fun BookmarkItem.initialThumbnailUri(baseUrl: String): MediaUri {
    val ownerKind = bookmarkOwnerKind(this)
    return MediaCellModel(
        mediaId = bookmark.videoId,
        ownerKind = ownerKind,
        thumbnailPath = video?.thumbnailPath,
        mediaKind = video?.mediaMode?.takeIf { it.isNotBlank() } ?: video?.mediaKind ?: bookmarkFeedMediaKind(this),
        slideCount = video?.slideCount ?: bookmarkFeedSlideCount(this),
        allowServerThumbnailFallback = true,
    ).initialThumbnailUri(baseUrl)
}

private fun FeedItemEntity.hasMomentStyleMedia(): Boolean =
    !mediaJson.isNullOrBlank() || !quoteMediaJson.isNullOrBlank()
