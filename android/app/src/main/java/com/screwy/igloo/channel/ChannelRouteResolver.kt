package com.screwy.igloo.channel

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.ui.nav.IglooNavigation
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.RouteRegistry

/**
 * Resolves a UI route for a handle-like identifier.
 *
 * Preferred order:
 *  1. existing `channels.source_id` match (most correct, preserves real platform/id)
 *  2. existing `channels.channel_id` match
 *  3. synthetic `{fallbackPlatform}_{handle}` route
 *
 * Channel pages are the stable destination for author/mention navigation, so the
 * UI should navigate even when the channel row is not yet cached locally.
 */
object ChannelRouteResolver {

    private val TRAILING_HANDLE_PUNCTUATION = charArrayOf(
        '.',
        ',',
        ':',
        ';',
        '!',
        '?',
        ')',
        ']',
        '}',
        '"',
        '\'',
    )

    suspend fun routeForHandle(
        db: IglooDatabase,
        rawHandle: String,
        fallbackPlatform: String = "twitter",
    ): String {
        val normalized = normalizeHandle(rawHandle)
        if (normalized.isEmpty()) return RouteRegistry.Feed.route

        val platform = normalizePlatform(fallbackPlatform)
        db.channelDao().findBySourceIdAndPlatform(normalized, platform)?.let {
            return channelRoute(it.channelId)
        }
        val syntheticChannelId = "${platform}_$normalized"
        db.channelDao().getById(syntheticChannelId)?.let {
            return channelRoute(it.channelId)
        }
        db.channelProfileDao().findByHandleAndPlatform(normalized, platform)?.let {
            return channelRoute(it.channelId)
        }
        if (platform != "twitter") return channelRoute(syntheticChannelId)
        db.channelDao().findBySourceId(normalized)?.let {
            return channelRoute(it.channelId)
        }
        db.channelProfileDao().findByHandle(normalized)?.let {
            return channelRoute(it.channelId)
        }
        return channelRoute(syntheticChannelId)
    }

    internal fun normalizeHandle(rawHandle: String): String =
        rawHandle
            .trim()
            .removePrefix("@")
            .trim()
            .trimEnd(*TRAILING_HANDLE_PUNCTUATION)
            .lowercase()

    private fun normalizePlatform(platform: String): String {
        val normalized = platform.trim().lowercase()
        return if (normalized == "x") "twitter" else normalized.ifBlank { "twitter" }
    }

    private fun channelRoute(channelId: String): String =
        IglooNavigation.routeForChannel(channelId, IglooNavigationSource.Resolver)
            ?: RouteRegistry.Feed.route
}
