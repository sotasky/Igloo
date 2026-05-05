package com.screwy.igloo.data

private val PlatformIdPrefixes = listOf(
    "tiktok_",
    "instagram_",
    "youtube_",
    "twitter_",
    "x_",
)

internal fun stripPlatformPrefix(value: String): String =
    PlatformIdPrefixes.fold(value) { stripped, prefix -> stripped.removePrefix(prefix) }

internal fun platformKeyFromChannelId(channelId: String): String =
    PlatformIdPrefixes.firstOrNull { channelId.startsWith(it) }
        ?.removeSuffix("_")
        ?.let { if (it == "x") "twitter" else it }
        ?: ""

internal fun isYoutubeChannelId(channelId: String): Boolean = channelId.startsWith("youtube_")
