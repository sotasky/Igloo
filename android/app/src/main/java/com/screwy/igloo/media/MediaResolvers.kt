package com.screwy.igloo.media

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.net.androidSyncAssetPath
import java.io.File
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf

sealed class MediaUri {
    data class Local(val file: File) : MediaUri()

    data class Remote(val url: String) : MediaUri()

    object Missing : MediaUri()
}

enum class OwnerKind {
    Tweet,
    TikTokVideo,
    InstagramReel,
    YouTubeVideo,
}

internal fun OwnerKind.assetOwnerKind(): String =
    when (this) {
        OwnerKind.Tweet -> "tweet"
        OwnerKind.TikTokVideo -> "tiktok_video"
        OwnerKind.InstagramReel -> "instagram_reel"
        OwnerKind.YouTubeVideo -> "youtube_video"
    }

internal fun ownerKindFromAssetOwnerKind(ownerKind: String): OwnerKind =
    when (ownerKind) {
        "tweet" -> OwnerKind.Tweet
        "tiktok_video" -> OwnerKind.TikTokVideo
        "instagram_reel" -> OwnerKind.InstagramReel
        "youtube_video" -> OwnerKind.YouTubeVideo
        else -> error("unknown asset owner kind: $ownerKind")
    }

interface MediaResolvers {
    suspend fun thumbnailForPost(ownerId: String, ownerKind: OwnerKind): MediaUri

    fun thumbnailForPostFlow(ownerId: String, ownerKind: OwnerKind): Flow<MediaUri>

    suspend fun avatarForChannel(channelId: String): MediaUri

    fun avatarForChannelFlow(channelId: String): Flow<MediaUri>

    suspend fun avatarForOwner(ownerId: String, ownerKind: String): MediaUri =
        if (ownerKind == "channel") avatarForChannel(ownerId) else MediaUri.Missing

    fun avatarForOwnerFlow(ownerId: String, ownerKind: String): Flow<MediaUri> =
        if (ownerKind == "channel") avatarForChannelFlow(ownerId) else flowOf(MediaUri.Missing)

    suspend fun bannerForChannel(channelId: String): MediaUri = MediaUri.Missing

    fun bannerForChannelFlow(channelId: String): Flow<MediaUri> = flowOf(MediaUri.Missing)

    suspend fun videoStream(videoId: String, ownerKind: OwnerKind): MediaUri

    fun videoStreamFlow(videoId: String, ownerKind: OwnerKind): Flow<MediaUri>
}

class MediaResolversImpl(
    private val syncDao: AndroidSyncDao,
    private val baseUrlProvider: () -> String,
    private val prefs: PreferencesRepo,
    private val remoteFallbackAllowed: Flow<Boolean> = flowOf(true),
) : MediaResolvers {

    override suspend fun thumbnailForPost(ownerId: String, ownerKind: OwnerKind): MediaUri =
        selectThumbnail(
            rows = currentRows(ownerKind.assetOwnerKind(), ownerId),
            useDearrow = shouldUseDearrow(prefs.dearrowMode().first(), ownerKind),
            allowRemote = remoteFallbackAllowed.first(),
        )

    override fun thumbnailForPostFlow(ownerId: String, ownerKind: OwnerKind): Flow<MediaUri> =
        combine(
                currentRowsFlow(ownerKind.assetOwnerKind(), ownerId),
                prefs.dearrowMode(),
                remoteFallbackAllowed,
            ) { rows, mode, allowRemote ->
                selectThumbnail(rows, shouldUseDearrow(mode, ownerKind), allowRemote)
            }
            .distinctUntilChanged()

    override suspend fun avatarForChannel(channelId: String): MediaUri =
        avatarForOwner(channelId, "channel")

    override fun avatarForChannelFlow(channelId: String): Flow<MediaUri> =
        avatarForOwnerFlow(channelId, "channel")

    override suspend fun avatarForOwner(ownerId: String, ownerKind: String): MediaUri =
        selectAsset(currentRows(ownerKind, ownerId), "avatar", remoteFallbackAllowed.first())

    override fun avatarForOwnerFlow(ownerId: String, ownerKind: String): Flow<MediaUri> =
        currentAssetFlow(ownerKind, ownerId, "avatar")

    override suspend fun bannerForChannel(channelId: String): MediaUri =
        selectAsset(currentRows("channel", channelId), "banner", remoteFallbackAllowed.first())

    override fun bannerForChannelFlow(channelId: String): Flow<MediaUri> =
        currentAssetFlow("channel", channelId, "banner")

    override suspend fun videoStream(videoId: String, ownerKind: OwnerKind): MediaUri =
        selectVideo(currentRows(ownerKind.assetOwnerKind(), videoId), remoteFallbackAllowed.first())

    override fun videoStreamFlow(videoId: String, ownerKind: OwnerKind): Flow<MediaUri> =
        combine(currentRowsFlow(ownerKind.assetOwnerKind(), videoId), remoteFallbackAllowed) {
                rows,
                allowRemote ->
                selectVideo(rows, allowRemote)
            }
            .distinctUntilChanged()

    private suspend fun currentRows(
        ownerKind: String,
        ownerId: String,
    ): List<AndroidSyncAssetEntity> = currentRowsFlow(ownerKind, ownerId).first()

    private fun currentRowsFlow(
        ownerKind: String,
        ownerId: String,
    ): Flow<List<AndroidSyncAssetEntity>> = syncDao.assetsForOwnerFlow(ownerKind, ownerId)

    private fun currentAssetFlow(
        ownerKind: String,
        ownerId: String,
        assetKind: String,
    ): Flow<MediaUri> =
        combine(currentRowsFlow(ownerKind, ownerId), remoteFallbackAllowed) { rows, allowRemote ->
                selectAsset(rows, assetKind, allowRemote)
            }
            .distinctUntilChanged()

    private fun selectThumbnail(
        rows: List<AndroidSyncAssetEntity>,
        useDearrow: Boolean,
        allowRemote: Boolean,
    ): MediaUri {
        if (useDearrow) {
            selectAsset(rows, "dearrow_thumbnail", allowRemote).let {
                if (it !is MediaUri.Missing) return it
            }
        }
        selectAsset(rows, "post_thumbnail", allowRemote).let {
            if (it !is MediaUri.Missing) return it
        }
        val image =
            rows.firstOrNull {
                it.assetKind == "post_media" &&
                    it.contentType.orEmpty().startsWith("image/", ignoreCase = true)
            }
        return image.toMediaUri(allowRemote)
    }

    private fun selectVideo(rows: List<AndroidSyncAssetEntity>, allowRemote: Boolean): MediaUri {
        val row =
            rows.firstOrNull { it.assetKind == "video_stream" }
                ?: rows.firstOrNull {
                    it.assetKind == "post_media" &&
                        it.contentType.orEmpty().startsWith("video/", ignoreCase = true)
                }
        return row.toMediaUri(allowRemote)
    }

    private fun selectAsset(
        rows: List<AndroidSyncAssetEntity>,
        assetKind: String,
        allowRemote: Boolean,
    ): MediaUri = rows.firstOrNull { it.assetKind == assetKind }.toMediaUri(allowRemote)

    private fun AndroidSyncAssetEntity?.toMediaUri(allowRemote: Boolean): MediaUri {
        val row = this ?: return MediaUri.Missing
        if (!row.localPath.isNullOrBlank()) {
            return MediaUri.Local(File(row.localPath))
        }
        if (!allowRemote || row.state == "server_missing") {
            return MediaUri.Missing
        }
        val root = baseUrlProvider().trim().trimEnd('/')
        if (root.isBlank()) return MediaUri.Missing
        return MediaUri.Remote(root + androidSyncAssetPath(row.assetId, row.revision))
    }

    private fun shouldUseDearrow(mode: String, ownerKind: OwnerKind): Boolean =
        mode != "off" && ownerKind == OwnerKind.YouTubeVideo
}
