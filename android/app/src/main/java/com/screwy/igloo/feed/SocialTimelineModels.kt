package com.screwy.igloo.feed

import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.component.displayLabel
import com.screwy.igloo.ui.component.normalizeHandle
import com.screwy.igloo.ui.nav.ProfileOpenSnapshot

data class SocialProfileModel(
    val channelId: String,
    val handle: String,
    val displayName: String,
)

data class SocialActionState(
    val isLiked: Boolean,
    val isBookmarked: Boolean,
    val isAuthorFollowed: Boolean,
    val isAuthorStarred: Boolean,
)

data class SocialMediaModel(
    val ownerId: String,
    val grid: FeedMediaGridModel,
)

data class SocialPostModel(
    val row: FeedRow,
    val author: SocialProfileModel,
    val actions: SocialActionState,
    val media: SocialMediaModel,
    val quoteMedia: SocialMediaModel?,
) {
    val stableKey: String = row.item.tweetId
    val contentType: String = if (row.item.isReply) "social_reply" else "social_post"
}

internal fun buildSocialPostModel(
    row: FeedRow,
    mediaModels: Map<String, FeedMediaGridModel>,
): SocialPostModel {
    val item = row.item
    val authorHandle = normalizeHandle(item.authorHandle)
    val author = SocialProfileModel(
        channelId = item.channelId.orEmpty(),
        handle = authorHandle,
        displayName = displayLabel(
            primary = item.authorDisplayName,
            fallback = row.channelName,
            handle = authorHandle,
        ),
    )
    val media = mediaModels[item.tweetId]
        ?: fallbackFeedMediaGridModel(item.tweetId, item.mediaJson)
    val quoteId = item.quoteTweetId?.takeIf { it.isNotBlank() }
    val quoteMedia = quoteId?.let { id ->
        SocialMediaModel(
            ownerId = id,
            grid = mediaModels[id] ?: fallbackFeedMediaGridModel(id, item.quoteMediaJson),
        )
    }

    return SocialPostModel(
        row = row,
        author = author,
        actions = SocialActionState(
            isLiked = row.isLiked == 1,
            isBookmarked = row.isBookmarked == 1,
            isAuthorFollowed = row.channelIsFollowed == 1,
            isAuthorStarred = row.channelIsStarred == 1,
        ),
        media = SocialMediaModel(ownerId = item.tweetId, grid = media),
        quoteMedia = quoteMedia,
    )
}

internal fun buildProfileOpenSnapshot(
    post: SocialPostModel,
    baseUrl: String,
): ProfileOpenSnapshot? {
    val channelId = post.author.channelId.takeIf { it.isNotBlank() } ?: return null
    val root = baseUrl.trim().trimEnd('/')
    val platform = post.row.channelPlatform
        ?.trim()
        ?.takeIf { it.isNotBlank() }
        ?: platformKeyFromChannelId(channelId)
    return ProfileOpenSnapshot(
        channelId = channelId,
        displayName = post.author.displayName,
        handle = post.author.handle,
        platform = platform,
        isFollowed = post.actions.isAuthorFollowed,
        isStarred = post.actions.isAuthorStarred,
        avatarUri = profileAvatarUri(
            root = root,
            channelId = channelId,
        ),
        bannerUri = profileBannerUri(
            root = root,
            channelId = channelId,
            platform = platform,
        ),
    )
}

private fun profileAvatarUri(
    root: String,
    channelId: String,
): MediaUri {
    if (root.isBlank()) return MediaUri.Missing
    return MediaUri.Remote("$root/api/media/avatar/$channelId")
}

private fun profileBannerUri(
    root: String,
    channelId: String,
    platform: String,
): MediaUri {
    if (root.isBlank() || platform.lowercase() !in BannerCapablePlatforms) return MediaUri.Missing
    return MediaUri.Remote("$root/api/media/banner/$channelId")
}

private val BannerCapablePlatforms = setOf("twitter", "x", "tiktok", "youtube")
