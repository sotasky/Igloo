package com.screwy.igloo.bookmarks

import com.screwy.igloo.data.entity.BookmarkItem
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.feed.parseFeedMediaDescriptors
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.media.ownerKindFromAssetOwnerKind
import com.screwy.igloo.ui.component.MomentItem
import com.screwy.igloo.ui.component.mediaTypeFor
import com.screwy.igloo.ui.component.normalizeHandle
import java.util.Base64

/**
 * Bookmark media type — videos resolve directly off `videos.media_kind` /
 * `slide_count` unless the videos row is an empty bookmark stub; tweets fall
 * back to descriptor count and kind so mixed photo/video posts stay slideshows.
 */
internal fun bookmarkMediaType(item: BookmarkItem) = when (val video = item.video) {
    null -> mediaTypeFor(bookmarkFeedMediaKind(item), bookmarkFeedSlideCount(item))
    else -> mediaTypeFor(bookmarkEffectiveMediaKind(item, video), bookmarkEffectiveSlideCount(item, video))
}

internal fun bookmarkPublishedAt(item: BookmarkItem): Long =
    item.bookmark.bookmarkedAt.takeIf { it > 0L }
        ?: item.video?.publishedAt?.takeIf { it > 0L }
        ?: item.feedItem?.publishedAt?.takeIf { it > 0L }
        ?: 0L

internal fun opensBookmarkInMomentsOverlay(item: BookmarkItem): Boolean =
    item.video != null || item.assetMediaCount > 0 || item.feedItem?.hasMomentStyleMedia() == true

internal fun bookmarkMomentPlaylistItems(
	items: List<BookmarkItem>,
	filter: BookmarkFilter,
): List<MomentItem> =
	filterBookmarkItems(items, filter)
		.filter(::opensBookmarkInMomentsOverlay)
		.map(::toBookmarkMomentItem)

internal fun bookmarkPlaylistId(filter: BookmarkFilter): String = when (filter) {
    BookmarkFilter.All -> BookmarkPlaylistAllId
    is BookmarkFilter.Category -> "$BookmarkPlaylistCategoryPrefix${filter.categoryId}"
    is BookmarkFilter.Label -> normalizeBookmarkLabel(filter.label)
        ?.let { label -> "$BookmarkPlaylistLabelPrefix${encodeBookmarkPlaylistLabel(label)}" }
        ?: BookmarkPlaylistNoLabelId
    BookmarkFilter.NoLabel -> BookmarkPlaylistNoLabelId
}

internal fun bookmarkFilterFromPlaylistId(playlistId: String?): BookmarkFilter {
    val id = playlistId?.trim()?.takeIf { it.isNotEmpty() } ?: return BookmarkFilter.All
    if (id == BookmarkPlaylistAllId) return BookmarkFilter.All
    if (id == BookmarkPlaylistNoLabelId) return BookmarkFilter.NoLabel
    if (id.startsWith(BookmarkPlaylistCategoryPrefix)) {
        val categoryId = id.removePrefix(BookmarkPlaylistCategoryPrefix).toLongOrNull()
        return categoryId?.let(BookmarkFilter::Category) ?: BookmarkFilter.All
    }
    if (id.startsWith(BookmarkPlaylistLabelPrefix)) {
        val label = decodeBookmarkPlaylistLabel(id.removePrefix(BookmarkPlaylistLabelPrefix))
            ?.let(::normalizeBookmarkLabel)
        return label?.let(BookmarkFilter::Label) ?: BookmarkFilter.All
    }
    return BookmarkFilter.All
}

internal fun toBookmarkMomentItem(item: BookmarkItem): MomentItem {
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
        mediaOwnerId = bookmarkMediaOwnerId(item),
        channelId = channelId,
        canonicalUrl = item.video?.canonicalUrl?.takeIf { it.isNotBlank() }
            ?: item.feedItem?.canonicalUrl.orEmpty(),
        authorDisplayName = bookmarkAuthorDisplayName(item),
        authorHandle = bookmarkAuthorHandle(item, channelId),
        description = description,
        likeCount = likeCount,
        isLiked = false,
        isBookmarked = true,
        mediaKind = item.video?.let { video -> bookmarkEffectiveMediaKind(item, video) } ?: feedMediaKind,
        slideCount = item.video?.let { video -> bookmarkEffectiveSlideCount(item, video) } ?: feedSlideCount,
        publishedAt = bookmarkPublishedAt(item),
		ownerKind = bookmarkOwnerKind(item),
        isAuthorFollowed = item.resolvedChannelIsFollowed == 1,
    )
}

internal fun bookmarkOwnerKind(item: BookmarkItem): OwnerKind {
	if (item.feedItem != null) return OwnerKind.Tweet
	return ownerKindFromAssetOwnerKind(requireNotNull(item.video).ownerKind)
}

internal fun bookmarkMediaOwnerId(item: BookmarkItem): String {
    val feedItem = item.feedItem ?: return item.bookmark.videoId
    val hasParentMedia = parseFeedMediaDescriptors(feedItem.mediaJson).isNotEmpty()
    val hasQuoteMedia = parseFeedMediaDescriptors(feedItem.quoteMediaJson).isNotEmpty()
    val quoteTweetId = feedItem.quoteTweetId?.trim().orEmpty()
    return if (!hasParentMedia && hasQuoteMedia && quoteTweetId.isNotBlank()) {
        quoteTweetId
    } else {
        item.bookmark.videoId
    }
}

internal fun bookmarkChannelId(item: BookmarkItem): String {
	val channelId = item.resolvedChannelId ?: item.video?.channelId ?: item.feedItem?.channelId
	if (!channelId.isNullOrBlank()) return channelId
	return ""
}

internal fun bookmarkAuthorDisplayName(item: BookmarkItem): String? =
    preferredBookmarkDisplayName(
        primary = item.feedAuthorDisplayName,
        channelName = item.resolvedChannelName,
        handle = item.feedAuthorHandle
            ?: item.feedSourceHandle
            ?: item.resolvedChannelSourceId,
    )

internal fun bookmarkAuthorHandle(item: BookmarkItem, channelId: String): String {
    val handle = normalizeHandle(
        item.feedAuthorHandle
            ?: item.feedSourceHandle
            ?: item.resolvedChannelSourceId
            ?: stripPlatformPrefix(channelId),
    )
    return if (handle.isNotBlank()) "@$handle" else item.bookmark.videoId
}

private fun bookmarkFeedMediaKind(item: BookmarkItem): String? {
    val descriptors = bookmarkFeedMediaDescriptors(item)
    if (bookmarkShouldUseAssetShape(item, descriptors.size)) {
        return bookmarkAssetMediaKind(item)
    }
    if (descriptors.isEmpty()) return null
    if (descriptors.size > 1) return "slideshow"

    return when (descriptors.first().type.trim().lowercase()) {
        "photo" -> "image"
        else -> descriptors.first().type.trim().lowercase()
    }
}

private fun bookmarkFeedSlideCount(item: BookmarkItem): Int {
    val descriptors = bookmarkFeedMediaDescriptors(item)
    if (bookmarkShouldUseAssetShape(item, descriptors.size)) {
        return bookmarkAssetSlideCount(item)
    }
    return when {
        descriptors.size > 1 -> descriptors.size
        bookmarkFeedMediaKind(item) == "image" -> 1
        else -> 0
    }
}

private fun bookmarkShouldUseAssetShape(item: BookmarkItem, descriptorCount: Int): Boolean =
    item.assetMediaCount > 0 && (descriptorCount == 0 || item.assetMediaCount >= descriptorCount)

private fun bookmarkAssetMediaKind(item: BookmarkItem): String? {
    val assetCount = item.assetMediaCount.coerceAtLeast(0)
    return when {
        assetCount > 1 -> "slideshow"
        assetCount == 1 && item.assetVideoCount > 0 -> "video"
        assetCount == 1 -> "image"
        else -> null
    }
}

private fun bookmarkAssetSlideCount(item: BookmarkItem): Int {
    val assetCount = item.assetMediaCount.coerceAtLeast(0)
    return when {
        assetCount > 1 -> assetCount
        assetCount == 1 && item.assetVideoCount <= 0 -> 1
        else -> 0
    }
}

private fun bookmarkFeedMediaDescriptors(item: BookmarkItem) = item.feedItem?.let { feedItem ->
    parseFeedMediaDescriptors(feedItem.mediaJson)
        .ifEmpty { parseFeedMediaDescriptors(feedItem.quoteMediaJson) }
}.orEmpty()

private fun bookmarkEffectiveMediaKind(item: BookmarkItem, video: VideoEntity): String? {
    val videoKind = video.mediaKind?.takeIf { it.isNotBlank() }
    val feedKind = bookmarkFeedMediaKind(item)
    val feedSlideCount = bookmarkFeedSlideCount(item)
    val videoSlideCount = video.slideCount.coerceAtLeast(0)
    return if (feedKind != null && (videoKind == null || (feedSlideCount > 1 && videoSlideCount < feedSlideCount))) {
        feedKind
    } else {
        videoKind ?: feedKind
    }
}

private fun bookmarkEffectiveSlideCount(item: BookmarkItem, video: VideoEntity): Int {
    val feedSlideCount = bookmarkFeedSlideCount(item)
    val videoSlideCount = video.slideCount.coerceAtLeast(0)
    return when {
        feedSlideCount > 1 && videoSlideCount < feedSlideCount -> feedSlideCount
        videoSlideCount > 0 -> videoSlideCount
        else -> feedSlideCount
    }
}

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

private fun FeedItemEntity.hasMomentStyleMedia(): Boolean =
    !mediaJson.isNullOrBlank() || !quoteMediaJson.isNullOrBlank()

private const val BookmarkPlaylistAllId = "_"
private const val BookmarkPlaylistNoLabelId = "no_label"
private const val BookmarkPlaylistCategoryPrefix = "category_"
private const val BookmarkPlaylistLabelPrefix = "label_"

private fun encodeBookmarkPlaylistLabel(label: String): String =
    Base64.getUrlEncoder().withoutPadding().encodeToString(label.toByteArray(Charsets.UTF_8))

private fun decodeBookmarkPlaylistLabel(value: String): String? =
    runCatching {
        String(Base64.getUrlDecoder().decode(value), Charsets.UTF_8)
    }.getOrNull()
