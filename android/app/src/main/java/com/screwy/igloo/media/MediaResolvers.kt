package com.screwy.igloo.media

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.ChannelProfileDao
import com.screwy.igloo.data.dao.MediaInventoryDao
import com.screwy.igloo.data.dao.VideoDao
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.VideoEntity
import java.io.File
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOf

/**
 * Three resolver APIs consumed by UI composables — every media display concern
 * routes through one of these. No composable invents its own fallback chain.
 */

sealed class MediaUri {
    data class Local(val file: File) : MediaUri()
    data class Remote(val url: String) : MediaUri()
    object Missing : MediaUri()
}

enum class OwnerKind { Tweet, TikTokVideo, InstagramReel, YouTubeVideo }

internal fun ownerKindFromChannelId(channelId: String): OwnerKind = when (platformKeyFromChannelId(channelId)) {
    "instagram" -> OwnerKind.InstagramReel
    "youtube" -> OwnerKind.YouTubeVideo
    "twitter" -> OwnerKind.Tweet
    else -> OwnerKind.TikTokVideo
}

interface MediaResolvers {
    /**
     * For any content row (feed_items or videos). Returns the displayable URI for
     * the post's thumbnail — which may be the post's own media thumbnail, or
     * (per server cascade) a quote's media thumbnail when the post is text-only.
     * Server's cascade already decided; client just calls this.
     */
    suspend fun thumbnailForPost(ownerId: String, ownerKind: OwnerKind): MediaUri
    fun thumbnailForPostFlow(ownerId: String, ownerKind: OwnerKind): Flow<MediaUri>

    /**
     * For any channel. Returns the displayable URI for the avatar, or Missing
     * if neither Sync nor the retained inventory fallback can resolve it.
     */
    suspend fun avatarForChannel(channelId: String): MediaUri
    fun avatarForChannelFlow(channelId: String): Flow<MediaUri>

    suspend fun bannerForChannel(channelId: String): MediaUri = MediaUri.Missing
    fun bannerForChannelFlow(channelId: String): Flow<MediaUri> = flowOf(MediaUri.Missing)

    /**
     * For a playable video. Returns local file URI if cached, else the server's
     * streaming URL. ExoPlayer + Media3 handle both URI schemes transparently.
     */
    suspend fun videoStream(videoId: String): MediaUri
    fun videoStreamFlow(videoId: String): Flow<MediaUri>
}

/**
 * Implementation: prefers verified Sync local assets, then consults the retained
 * [MediaInventoryDao] fallback and server endpoints.
 *
 * Return rules (spec §6):
 * - state='cached' AND local_path non-null AND the file exists → Local(File(local_path)).
 * - Inventory row known (any state) → Remote(baseUrl + server_url).
 * - No local or inventory row:
 *   - known video row → Remote(baseUrl + /api/media/stream/{videoId}) so moments can
 *     play immediately even before Sync has verified a local file
 *   - otherwise → Missing.
 *
 * If state='cached' but the file is missing on disk (e.g., user cleared app data),
 * falls through to Remote so the UI can still render via network. Does NOT trigger
 * a re-download.
 *
 * [baseUrlProvider] is called at resolve time so it picks up any runtime changes
 * (e.g., TLS toggled) without requiring a new instance.
 */
class MediaResolversImpl(
    private val dao: MediaInventoryDao,
    private val syncDao: AndroidSyncDao,
    private val channelProfileDao: ChannelProfileDao,
    private val videoDao: VideoDao,
    private val baseUrlProvider: () -> String,
    private val prefs: PreferencesRepo,
) : MediaResolvers {

    private object MediaUriMemory {
        private const val MaxEntries = 4000
        private val entries = object : LinkedHashMap<String, MediaUri>(MaxEntries, 0.75f, true) {
            override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, MediaUri>?): Boolean =
                size > MaxEntries
        }

        fun get(key: String): MediaUri? = synchronized(entries) { entries[key] }

        fun put(key: String, uri: MediaUri) {
            if (uri is MediaUri.Missing) return
            synchronized(entries) { entries[key] = uri }
        }
    }

    override suspend fun thumbnailForPost(ownerId: String, ownerKind: OwnerKind): MediaUri {
        val video = videoDao.getById(ownerId)
        val mode = prefs.dearrowMode().first()
        if (shouldUseDearrow(mode, video)) {
            return resolveAsset(ownerId, "dearrow_thumbnail").orIfMissing { dearrowRemoteThumb(ownerId) }
        }
        val resolved = resolvePreferredAsset(ownerId, "post_thumbnail", "post_media")
        if (resolved !is MediaUri.Missing) return resolved
        return thumbnailFallback(ownerId, ownerKind, video)
    }

    override suspend fun avatarForChannel(channelId: String): MediaUri {
        return resolveAsset(channelId, "avatar")
            .orAvatarFallback(channelId, channelProfileDao.getById(channelId))
    }

    override suspend fun bannerForChannel(channelId: String): MediaUri {
        return resolveAsset(channelId, "banner")
            .orBannerFallback(channelId, channelProfileDao.getById(channelId))
    }

    override suspend fun videoStream(videoId: String): MediaUri {
        val resolved = resolveVideoAsset(videoId)
        if (resolved !is MediaUri.Missing) return resolved
        return videoStreamFallback(videoId)
    }

    override fun thumbnailForPostFlow(ownerId: String, ownerKind: OwnerKind): Flow<MediaUri> =
        combine(
            preferredAssetFlow(ownerId, "post_thumbnail", "post_media"),
            assetFlow(ownerId, "dearrow_thumbnail"),
            videoDao.getByIdFlow(ownerId),
            prefs.dearrowMode(),
        ) { resolved, dearrowResolved, video, mode ->
            if (shouldUseDearrow(mode, video)) {
                return@combine dearrowResolved.orIfMissing { dearrowRemoteThumb(ownerId) }
            }
            if (resolved !is MediaUri.Missing) resolved else thumbnailFallback(ownerId, ownerKind, video)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("thumbnail:$ownerKind:$ownerId")

    override fun avatarForChannelFlow(channelId: String): Flow<MediaUri> =
        combine(
            assetFlow(channelId, "avatar"),
            channelProfileDao.getByIdFlow(channelId),
        ) { resolved, profile ->
            resolved.orAvatarFallback(channelId, profile)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("avatar:$channelId")

    override fun bannerForChannelFlow(channelId: String): Flow<MediaUri> =
        combine(
            assetFlow(channelId, "banner"),
            channelProfileDao.getByIdFlow(channelId),
        ) { resolved, profile ->
            resolved.orBannerFallback(channelId, profile)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("banner:$channelId")

    /**
     * Server always has `/api/media/avatar/{channelId}` — serves cached bytes if
     * they exist, proxies otherwise. So when no synced local avatar is available
     * (e.g. an un-followed TikTok channel that showed up in the moments feed),
     * fall through to that endpoint instead of returning Missing.
     */
    private fun MediaUri.orAvatarFallback(channelId: String, profile: ChannelProfileEntity?): MediaUri {
        if (this !is MediaUri.Missing) return this
        profile?.avatarUrl?.toAbsoluteHttpUri()?.let { return MediaUri.Remote(it) }
        return MediaUri.Remote(baseUrlProvider() + "/api/media/avatar/" + channelId)
    }

    private fun MediaUri.orBannerFallback(channelId: String, profile: ChannelProfileEntity?): MediaUri {
        if (this !is MediaUri.Missing) return this
        val root = baseUrlProvider().trim().trimEnd('/')
        if (root.isBlank() || profile == null) return MediaUri.Missing
        val platform = profile.platform.trim().lowercase()
        val hasProfileBanner = !profile.bannerUrl.isNullOrBlank()
        val canHaveServerBanner = platform == "twitter" || platform == "x" ||
            platform == "tiktok" || platform == "youtube"
        if (!hasProfileBanner && !canHaveServerBanner) return MediaUri.Missing
        return MediaUri.Remote("$root/api/media/banner/$channelId")
    }

    override fun videoStreamFlow(videoId: String): Flow<MediaUri> =
        combine(
            videoAssetFlow(videoId),
            videoDao.getByIdFlow(videoId),
        ) { resolved, video ->
            if (resolved !is MediaUri.Missing) resolved else videoStreamFallback(videoId, video)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("video:$videoId")

    private suspend fun resolveAsset(ownerId: String, kind: String): MediaUri =
        resolveSyncAsset(ownerId, kind).orIfMissing {
            dao.resolveForOwner(ownerId, kind).toMediaUri()
        }

    private suspend fun resolvePreferredAsset(ownerId: String, primaryKind: String, fallbackKind: String): MediaUri =
        resolveSyncPreferredAsset(ownerId, primaryKind, fallbackKind).orIfMissing {
            resolveInventoryPreferredAsset(ownerId, primaryKind, fallbackKind)
        }

    private suspend fun resolveVideoAsset(videoId: String): MediaUri =
        syncDao.latestVerifiedVideoLocalPath(videoId).toExistingLocalUriOrMissing().orIfMissing {
            resolveInventoryPreferredAsset(videoId, "video_stream", "post_media")
        }

    private suspend fun resolveInventoryPreferredAsset(
        ownerId: String,
        primaryKind: String,
        fallbackKind: String,
    ): MediaUri =
        (dao.resolveForOwner(ownerId, primaryKind) ?: dao.resolveForOwner(ownerId, fallbackKind)).toMediaUri()

    private suspend fun resolveSyncAsset(ownerId: String, kind: String): MediaUri =
        syncDao.latestVerifiedLocalPath(ownerId, kind).toExistingLocalUriOrMissing()

    private suspend fun resolveSyncPreferredAsset(
        ownerId: String,
        primaryKind: String,
        fallbackKind: String,
    ): MediaUri {
        return resolveSyncAsset(ownerId, primaryKind).orIfMissing {
            resolveSyncAsset(ownerId, fallbackKind)
        }
    }

    private fun assetFlow(ownerId: String, kind: String): Flow<MediaUri> =
        combine(
            syncDao.latestVerifiedLocalPathFlow(ownerId, kind),
            dao.forOwnerAndKindFlow(ownerId, kind),
        ) { syncPath, row ->
            syncPath.toExistingLocalUriOrMissing().orIfMissing { row.toMediaUri() }
        }.distinctUntilChanged()

    private fun preferredAssetFlow(
        ownerId: String,
        primaryKind: String,
        fallbackKind: String,
    ): Flow<MediaUri> =
        combine(
            syncPreferredAssetFlow(ownerId, primaryKind, fallbackKind),
            inventoryPreferredAssetFlow(ownerId, primaryKind, fallbackKind),
        ) { syncResolved, inventoryResolved ->
            syncResolved.orIfMissing { inventoryResolved }
        }.distinctUntilChanged()

    private fun videoAssetFlow(videoId: String): Flow<MediaUri> =
        combine(
            syncDao.latestVerifiedVideoLocalPathFlow(videoId),
            inventoryPreferredAssetFlow(videoId, "video_stream", "post_media"),
        ) { syncPath, inventoryResolved ->
            syncPath.toExistingLocalUriOrMissing().orIfMissing { inventoryResolved }
        }.distinctUntilChanged()

    private fun inventoryPreferredAssetFlow(
        ownerId: String,
        primaryKind: String,
        fallbackKind: String,
    ): Flow<MediaUri> =
        combine(
            dao.forOwnerAndKindFlow(ownerId, primaryKind),
            dao.forOwnerAndKindFlow(ownerId, fallbackKind),
        ) { primary, fallback ->
            (primary ?: fallback).toMediaUri()
        }.distinctUntilChanged()

    private fun syncPreferredAssetFlow(
        ownerId: String,
        primaryKind: String,
        fallbackKind: String,
    ): Flow<MediaUri> =
        combine(
            syncDao.latestVerifiedLocalPathFlow(ownerId, primaryKind),
            syncDao.latestVerifiedLocalPathFlow(ownerId, fallbackKind),
        ) { primary, fallback ->
            primary.toExistingLocalUriOrMissing().orIfMissing {
                fallback.toExistingLocalUriOrMissing()
            }
        }.distinctUntilChanged()

    // ─── DeArrow helpers ──────────────────────────────────────────────────────

    /** True when mode is on (not "off") AND the video has a dearrow_thumb_path. */
    private fun shouldUseDearrow(mode: String, video: VideoEntity?): Boolean {
        if (mode == "off") return false
        return !video?.dearrowThumbPath.isNullOrBlank()
    }

    /**
     * Server URL for the DeArrow thumbnail variant. The server (Task 11)
     * serves the DeArrow frame for ?da=1, falling back to the original on miss.
     * Android sync `dearrow_thumbnail` assets are preferred when synced; this is
     * only the server fallback for unsynced or legacy rows.
     */
    private fun dearrowRemoteThumb(ownerId: String): MediaUri {
        val root = baseUrlProvider().trimEnd('/')
        if (root.isBlank()) return MediaUri.Missing
        return MediaUri.Remote("$root/api/media/thumbnail/$ownerId?da=1")
    }

    private suspend fun thumbnailFallback(ownerId: String, ownerKind: OwnerKind): MediaUri =
        thumbnailFallback(ownerId, ownerKind, videoDao.getById(ownerId))

    private fun thumbnailFallback(ownerId: String, ownerKind: OwnerKind, video: VideoEntity?): MediaUri {
        if (ownerKind == OwnerKind.Tweet) return MediaUri.Missing
        val existing = video ?: return MediaUri.Missing
        val root = baseUrlProvider().trimEnd('/')
        if (root.isBlank()) return MediaUri.Missing

        return when {
            existing.mediaKind.equals("slideshow", ignoreCase = true) ||
                existing.mediaKind.equals("image", ignoreCase = true) -> MediaUri.Remote("$root/api/media/slide/$ownerId/0")
            else -> MediaUri.Remote("$root/api/media/thumbnail/$ownerId")
        }
    }

    private suspend fun videoStreamFallback(videoId: String): MediaUri =
        videoStreamFallback(videoId, videoDao.getById(videoId))

    private fun videoStreamFallback(videoId: String, video: VideoEntity?): MediaUri {
        if (video == null) return MediaUri.Missing
        val root = baseUrlProvider().trimEnd('/')
        if (root.isBlank()) return MediaUri.Missing
        return MediaUri.Remote("$root/api/media/stream/$videoId")
    }

    private fun com.screwy.igloo.data.entity.MediaInventoryEntity?.toMediaUri(): MediaUri {
        if (this == null) return MediaUri.Missing
        if (state == "cached" && !localPath.isNullOrEmpty()) {
            val file = File(localPath)
            if (file.exists()) return MediaUri.Local(file)
            // File missing on disk despite cached state — fall through to Remote.
        }
        return MediaUri.Remote(baseUrlProvider() + serverUrl)
    }

    private fun String?.toExistingLocalUriOrMissing(): MediaUri {
        val path = this ?: return MediaUri.Missing
        val file = File(path)
        return if (file.exists()) MediaUri.Local(file) else MediaUri.Missing
    }

    private fun String.toAbsoluteHttpUri(): String? {
        val trimmed = trim()
        return if (trimmed.startsWith("https://") || trimmed.startsWith("http://")) trimmed else null
    }

    private inline fun MediaUri.orIfMissing(fallback: () -> MediaUri): MediaUri =
        if (this is MediaUri.Missing) fallback() else this

    private fun Flow<MediaUri>.withMediaUriMemory(key: String): Flow<MediaUri> = flow {
        MediaUriMemory.get(key)?.let { emit(it) }
        collect { uri ->
            MediaUriMemory.put(key, uri)
            emit(uri)
        }
    }.distinctUntilChanged()
}
