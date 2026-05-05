package com.screwy.igloo.moments

enum class ShortsPlaylistType(val routeValue: String) {
    Moments("moments"),
    AllMoments("all_moments"),
    Bookmarks("bookmarks"),
    Channel("channel"),
    Story("story"),
    StoryTray("stories"),
}

data class ShortsPlaylistSpec(
    val type: ShortsPlaylistType,
    val playlistId: String,
) {
    val routePlaylistType: String
        get() = type.routeValue

    val routePlaylistId: String
        get() = when (type) {
            ShortsPlaylistType.Channel,
            ShortsPlaylistType.Story -> playlistId.trim()
            ShortsPlaylistType.StoryTray -> RootPlaylistId
            else -> RootPlaylistId
        }

    val recordsMomentsCursor: Boolean
        get() = type == ShortsPlaylistType.Moments || type == ShortsPlaylistType.AllMoments

    companion object {
        const val RootPlaylistId = "_"

        fun moments(): ShortsPlaylistSpec =
            ShortsPlaylistSpec(ShortsPlaylistType.Moments, RootPlaylistId)

        fun allMoments(): ShortsPlaylistSpec =
            ShortsPlaylistSpec(ShortsPlaylistType.AllMoments, RootPlaylistId)

        fun bookmarks(): ShortsPlaylistSpec =
            ShortsPlaylistSpec(ShortsPlaylistType.Bookmarks, RootPlaylistId)

        fun channel(channelId: String?): ShortsPlaylistSpec? {
            val id = channelId?.trim()?.takeIf { it.isNotEmpty() } ?: return null
            return ShortsPlaylistSpec(ShortsPlaylistType.Channel, id)
        }

        fun story(channelId: String?): ShortsPlaylistSpec? {
            val id = channelId?.trim()?.takeIf { it.isNotEmpty() } ?: return null
            return ShortsPlaylistSpec(ShortsPlaylistType.Story, id)
        }

        fun storyTray(): ShortsPlaylistSpec =
            ShortsPlaylistSpec(ShortsPlaylistType.StoryTray, RootPlaylistId)

        fun decode(routeType: String?, routeId: String?): ShortsPlaylistSpec? {
            val type = ShortsPlaylistType.entries.firstOrNull { it.routeValue == routeType?.trim() }
                ?: return null
            return when (type) {
                ShortsPlaylistType.Moments -> moments()
                ShortsPlaylistType.AllMoments -> allMoments()
                ShortsPlaylistType.Bookmarks -> bookmarks()
                ShortsPlaylistType.Channel -> channel(routeId)
                ShortsPlaylistType.Story -> story(routeId)
                ShortsPlaylistType.StoryTray -> storyTray()
            }
        }
    }
}

internal fun shortsStartIndex(videoIds: List<String>, requestedVideoId: String?): Int {
    val target = requestedVideoId?.trim()?.takeIf { it.isNotEmpty() } ?: return 0
    return videoIds.indexOf(target).takeIf { it >= 0 } ?: 0
}
