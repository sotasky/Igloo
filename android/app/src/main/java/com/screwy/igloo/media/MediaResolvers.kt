package com.screwy.igloo.media

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.ChannelProfileDao
import com.screwy.igloo.data.dao.MediaInventoryDao
import com.screwy.igloo.data.dao.VideoDao
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.perf.PerfProbe
import java.io.File
import java.net.URI
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
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
 * - Inventory row known and remote fallback is allowed → Remote(baseUrl + server_url).
 * - No local or inventory row:
 *   - known video row and remote fallback is allowed → Remote(baseUrl + server media route)
 *   - otherwise → Missing.
 *
 * If state='cached' but the file is missing on disk, remote fallback is suppressed
 * whenever reachability is not explicitly online.
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
    private val remoteFallbackAllowed: Flow<Boolean> = flowOf(true),
    logger: Logger? = null,
    resolverFallbackFlushEvery: Int = RESOLVER_FALLBACK_FLUSH_EVERY,
) : MediaResolvers {
    private val fallbackTelemetry = MediaResolverFallbackTelemetry(logger, resolverFallbackFlushEvery)

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
        val allowRemoteFallback = remoteFallbackAllowed.first()
        if (shouldUseDearrow(mode, video)) {
            return resolveAsset(
                ownerId = ownerId,
                kind = "dearrow_thumbnail",
                allowRemoteFallback = allowRemoteFallback,
                route = "thumbnail_for_post",
                ownerKind = ownerKind.telemetryName(),
            ).orIfMissing {
                dearrowRemoteThumb(ownerId, ownerKind, allowRemoteFallback)
            }
        }
        val resolved = resolvePostThumbnailAsset(
            ownerId = ownerId,
            allowRemoteFallback = allowRemoteFallback,
            route = "thumbnail_for_post",
            ownerKind = ownerKind.telemetryName(),
        )
        if (resolved !is MediaUri.Missing) return resolved
        return thumbnailFallback(ownerId, ownerKind, video, allowRemoteFallback)
    }

    override suspend fun avatarForChannel(channelId: String): MediaUri {
        val allowRemoteFallback = remoteFallbackAllowed.first()
        return resolveAsset(channelId, "avatar", allowRemoteFallback, route = "avatar_for_channel", ownerKind = "channel")
            .orAvatarFallback(channelId, channelProfileDao.getById(channelId), allowRemoteFallback)
    }

    override suspend fun bannerForChannel(channelId: String): MediaUri {
        val allowRemoteFallback = remoteFallbackAllowed.first()
        return resolveAsset(channelId, "banner", allowRemoteFallback, route = "banner_for_channel", ownerKind = "channel")
            .orBannerFallback(channelId, channelProfileDao.getById(channelId), allowRemoteFallback)
    }

    override suspend fun videoStream(videoId: String): MediaUri {
        val allowRemoteFallback = remoteFallbackAllowed.first()
        val resolved = resolveVideoAsset(videoId, allowRemoteFallback)
        if (resolved !is MediaUri.Missing) return resolved
        return videoStreamFallback(videoId, allowRemoteFallback)
    }

    override fun thumbnailForPostFlow(ownerId: String, ownerKind: OwnerKind): Flow<MediaUri> =
        combine(
            postThumbnailAssetFlow(ownerId, "thumbnail_for_post", ownerKind.telemetryName()),
            assetFlow(ownerId, "dearrow_thumbnail", "thumbnail_for_post", ownerKind.telemetryName()),
            videoDao.getByIdFlow(ownerId),
            prefs.dearrowMode(),
            remoteFallbackAllowed,
        ) { resolved, dearrowResolved, video, mode, allowRemoteFallback ->
            if (shouldUseDearrow(mode, video)) {
                return@combine dearrowResolved.orIfMissing { dearrowRemoteThumb(ownerId, ownerKind, allowRemoteFallback) }
            }
            if (resolved !is MediaUri.Missing) resolved else thumbnailFallback(ownerId, ownerKind, video, allowRemoteFallback)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("thumbnail:$ownerKind:$ownerId")
            .profileMediaResolverFlow(
                resolver = "thumbnail",
                ownerKind = ownerKind.telemetryName(),
                assetKinds = "post_thumbnail,post_media,dearrow_thumbnail",
            )

    override fun avatarForChannelFlow(channelId: String): Flow<MediaUri> =
        combine(
            assetFlow(channelId, "avatar", "avatar_for_channel", "channel"),
            channelProfileDao.getByIdFlow(channelId),
            remoteFallbackAllowed,
        ) { resolved, profile, allowRemoteFallback ->
            resolved.orAvatarFallback(channelId, profile, allowRemoteFallback)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("avatar:$channelId")
            .profileMediaResolverFlow(
                resolver = "avatar",
                ownerKind = "channel",
                assetKinds = "avatar",
            )

    override fun bannerForChannelFlow(channelId: String): Flow<MediaUri> =
        combine(
            assetFlow(channelId, "banner", "banner_for_channel", "channel"),
            channelProfileDao.getByIdFlow(channelId),
            remoteFallbackAllowed,
        ) { resolved, profile, allowRemoteFallback ->
            resolved.orBannerFallback(channelId, profile, allowRemoteFallback)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("banner:$channelId")
            .profileMediaResolverFlow(
                resolver = "banner",
                ownerKind = "channel",
                assetKinds = "banner",
            )

    /**
     * Server always has `/api/media/avatar/{channelId}` — serves cached bytes if
     * they exist, proxies otherwise. So when no synced local avatar is available
     * (e.g. an un-followed TikTok channel that showed up in the moments feed),
     * fall through to that endpoint instead of returning Missing.
     */
    private fun MediaUri.orAvatarFallback(
        channelId: String,
        profile: ChannelProfileEntity?,
        allowRemoteFallback: Boolean,
    ): MediaUri {
        if (this !is MediaUri.Missing) return this
        if (!allowRemoteFallback) return MediaUri.Missing
        profile?.avatarUrl?.toAbsoluteHttpUri()?.let {
            return MediaUri.Remote(it).recordRemoteFallback("avatar_for_channel", "channel", "avatar")
        }
        return MediaUri.Remote(baseUrlProvider() + "/api/media/avatar/" + channelId)
            .recordRemoteFallback("avatar_for_channel", "channel", "avatar")
    }

    private fun MediaUri.orBannerFallback(
        channelId: String,
        profile: ChannelProfileEntity?,
        allowRemoteFallback: Boolean,
    ): MediaUri {
        if (this !is MediaUri.Missing) return this
        if (!allowRemoteFallback) return MediaUri.Missing
        val root = baseUrlProvider().trim().trimEnd('/')
        if (root.isBlank() || profile == null) return MediaUri.Missing
        val platform = profile.platform.trim().lowercase()
        val hasProfileBanner = !profile.bannerUrl.isNullOrBlank()
        val canHaveServerBanner = platform == "twitter" || platform == "x" ||
            platform == "tiktok" || platform == "youtube"
        if (!hasProfileBanner && !canHaveServerBanner) return MediaUri.Missing
        return MediaUri.Remote("$root/api/media/banner/$channelId")
            .recordRemoteFallback("banner_for_channel", "channel", "banner")
    }

    override fun videoStreamFlow(videoId: String): Flow<MediaUri> =
        combine(
            videoAssetFlow(videoId),
            videoDao.getByIdFlow(videoId),
            remoteFallbackAllowed,
        ) { resolved, video, allowRemoteFallback ->
            if (resolved !is MediaUri.Missing) resolved else videoStreamFallback(videoId, video, allowRemoteFallback)
        }
            .distinctUntilChanged()
            .withMediaUriMemory("video:$videoId")
            .profileMediaResolverFlow(
                resolver = "video_stream",
                ownerKind = "video",
                assetKinds = "video_stream,post_media",
            )

    private suspend fun resolveAsset(
        ownerId: String,
        kind: String,
        allowRemoteFallback: Boolean,
        route: String,
        ownerKind: String,
    ): MediaUri =
        resolveSyncAsset(ownerId, kind).orIfMissing {
            dao.resolveForOwner(ownerId, kind).toMediaUri(allowRemoteFallback, route, ownerKind, kind)
        }

    private suspend fun resolveVideoAsset(videoId: String, allowRemoteFallback: Boolean): MediaUri =
        syncDao.latestVerifiedVideoLocalPath(videoId).toExistingLocalUriOrMissing().orIfMissing {
            resolveInventoryPreferredAsset(
                ownerId = videoId,
                primaryKind = "video_stream",
                fallbackKind = "post_media",
                allowRemoteFallback = allowRemoteFallback,
                route = "video_stream",
                ownerKind = "video",
            )
        }

    private suspend fun resolveInventoryPreferredAsset(
        ownerId: String,
        primaryKind: String,
        fallbackKind: String,
        allowRemoteFallback: Boolean,
        route: String,
        ownerKind: String,
    ): MediaUri =
        (dao.resolveForOwner(ownerId, primaryKind) ?: dao.resolveForOwner(ownerId, fallbackKind))
            .toMediaUri(allowRemoteFallback, route, ownerKind, primaryKind)

    private suspend fun resolveSyncAsset(ownerId: String, kind: String): MediaUri =
        syncDao.latestVerifiedLocalPath(ownerId, kind).toExistingLocalUriOrMissing()

    private suspend fun resolvePostThumbnailAsset(
        ownerId: String,
        allowRemoteFallback: Boolean,
        route: String,
        ownerKind: String,
    ): MediaUri =
        resolveSyncAsset(ownerId, "post_thumbnail").orIfMissing {
            syncDao.latestVerifiedPostMediaImageLocalPath(ownerId).toExistingLocalUriOrMissing()
        }.orIfMissing {
            resolveInventoryPreferredAsset(ownerId, "post_thumbnail", "post_media", allowRemoteFallback, route, ownerKind)
        }

    private fun assetFlow(ownerId: String, kind: String, route: String, ownerKind: String): Flow<MediaUri> =
        combine(
            syncDao.latestVerifiedLocalPathFlow(ownerId, kind),
            dao.forOwnerAndKindFlow(ownerId, kind),
            remoteFallbackAllowed,
        ) { syncPath, row, allowRemoteFallback ->
            syncPath.toExistingLocalUriOrMissing().orIfMissing {
                row.toMediaUri(allowRemoteFallback, route, ownerKind, kind)
            }
        }.distinctUntilChanged()

    private fun postThumbnailAssetFlow(ownerId: String, route: String, ownerKind: String): Flow<MediaUri> =
        combine(
            assetFlow(ownerId, "post_thumbnail", route, ownerKind),
            syncDao.latestVerifiedPostMediaImageLocalPathFlow(ownerId),
            inventoryPreferredAssetFlow(ownerId, "post_thumbnail", "post_media", route, ownerKind),
        ) { thumbnail, syncPostMediaImagePath, inventoryResolved ->
            thumbnail.orIfMissing {
                syncPostMediaImagePath.toExistingLocalUriOrMissing()
            }.orIfMissing {
                inventoryResolved
            }
        }.distinctUntilChanged()

    private fun videoAssetFlow(videoId: String): Flow<MediaUri> =
        combine(
            syncDao.latestVerifiedVideoLocalPathFlow(videoId),
            inventoryPreferredAssetFlow(videoId, "video_stream", "post_media", "video_stream", "video"),
        ) { syncPath, inventoryResolved ->
            syncPath.toExistingLocalUriOrMissing().orIfMissing { inventoryResolved }
        }.distinctUntilChanged()

    private fun inventoryPreferredAssetFlow(
        ownerId: String,
        primaryKind: String,
        fallbackKind: String,
        route: String,
        ownerKind: String,
    ): Flow<MediaUri> =
        combine(
            dao.forOwnerAndKindFlow(ownerId, primaryKind),
            dao.forOwnerAndKindFlow(ownerId, fallbackKind),
            remoteFallbackAllowed,
        ) { primary, fallback, allowRemoteFallback ->
            (primary ?: fallback).toMediaUri(allowRemoteFallback, route, ownerKind, primaryKind)
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
    private fun dearrowRemoteThumb(ownerId: String, ownerKind: OwnerKind, allowRemoteFallback: Boolean): MediaUri {
        if (!allowRemoteFallback) return MediaUri.Missing
        val root = baseUrlProvider().trimEnd('/')
        if (root.isBlank()) return MediaUri.Missing
        return MediaUri.Remote("$root/api/media/thumbnail/$ownerId?da=1")
            .recordRemoteFallback("thumbnail_for_post", ownerKind.telemetryName(), "dearrow_thumbnail")
    }

    private suspend fun thumbnailFallback(ownerId: String, ownerKind: OwnerKind, allowRemoteFallback: Boolean): MediaUri =
        thumbnailFallback(ownerId, ownerKind, videoDao.getById(ownerId), allowRemoteFallback)

    private fun thumbnailFallback(
        ownerId: String,
        ownerKind: OwnerKind,
        video: VideoEntity?,
        allowRemoteFallback: Boolean,
    ): MediaUri {
        if (!allowRemoteFallback) return MediaUri.Missing
        if (ownerKind == OwnerKind.Tweet) return MediaUri.Missing
        val existing = video ?: return MediaUri.Missing
        val root = baseUrlProvider().trimEnd('/')
        if (root.isBlank()) return MediaUri.Missing

        return when {
            existing.mediaKind.equals("slideshow", ignoreCase = true) ||
                existing.mediaKind.equals("image", ignoreCase = true) -> MediaUri.Remote("$root/api/media/slide/$ownerId/0")
            else -> MediaUri.Remote("$root/api/media/thumbnail/$ownerId")
        }.recordRemoteFallback("thumbnail_for_post", ownerKind.telemetryName(), "post_thumbnail")
    }

    private suspend fun videoStreamFallback(videoId: String, allowRemoteFallback: Boolean): MediaUri =
        videoStreamFallback(videoId, videoDao.getById(videoId), allowRemoteFallback)

    private fun videoStreamFallback(videoId: String, video: VideoEntity?, allowRemoteFallback: Boolean): MediaUri {
        if (!allowRemoteFallback) return MediaUri.Missing
        if (video == null) return MediaUri.Missing
        val root = baseUrlProvider().trimEnd('/')
        if (root.isBlank()) return MediaUri.Missing
        return MediaUri.Remote("$root/api/media/stream/$videoId")
            .recordRemoteFallback("video_stream", video.telemetryOwnerKind(), "video_stream")
    }

    private fun MediaInventoryEntity?.toMediaUri(
        allowRemoteFallback: Boolean,
        route: String,
        ownerKind: String,
        requestedAssetKind: String,
    ): MediaUri {
        if (this == null) return MediaUri.Missing
        if (state == "cached" && !localPath.isNullOrEmpty()) {
            val file = File(localPath)
            if (file.exists()) return MediaUri.Local(file)
        }
        if (!allowRemoteFallback) return MediaUri.Missing
        return MediaUri.Remote(baseUrlProvider() + serverUrl)
            .recordRemoteFallback(route, ownerKind, assetKind.ifBlank { requestedAssetKind })
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

    private fun MediaUri.recordRemoteFallback(route: String, ownerKind: String, assetKind: String): MediaUri {
        if (this is MediaUri.Remote) {
            fallbackTelemetry.record(
                route = route,
                ownerKind = ownerKind,
                assetKind = assetKind,
                urlClass = mediaResolverUrlClass(url),
            )
        }
        return this
    }

    private fun Flow<MediaUri>.withMediaUriMemory(key: String): Flow<MediaUri> = flow {
        MediaUriMemory.get(key)?.let { emit(it) }
        collect { uri ->
            MediaUriMemory.put(key, uri)
            emit(uri)
        }
    }.distinctUntilChanged()

    private fun Flow<MediaUri>.profileMediaResolverFlow(
        resolver: String,
        ownerKind: String,
        assetKinds: String,
    ): Flow<MediaUri> = flow {
        if (!PerfProbe.enabled()) {
            collect { emit(it) }
            return@flow
        }
        fun fields() = mapOf(
            "resolver" to resolver,
            "owner_kind" to ownerKind,
            "asset_kinds" to assetKinds,
        )
        val key = PerfProbe.collectorStart("media_resolver", ::fields)
        try {
            collect { uri ->
                PerfProbe.incrementCounter("igloo_media_resolver_emit_count")
                PerfProbe.log(
                    event = "media_resolver_emit",
                ) { fields() + ("uri" to PerfProbe.uriKind(uri)) }
                emit(uri)
            }
        } finally {
            PerfProbe.collectorEnd("media_resolver", key, ::fields)
        }
    }.distinctUntilChanged()

    private fun OwnerKind.telemetryName(): String = when (this) {
        OwnerKind.Tweet -> "tweet"
        OwnerKind.TikTokVideo -> "tiktok_video"
        OwnerKind.InstagramReel -> "instagram_reel"
        OwnerKind.YouTubeVideo -> "youtube_video"
    }

    private fun VideoEntity.telemetryOwnerKind(): String =
        ownerKindFromChannelId(channelId).telemetryName()

    private companion object {
        const val RESOLVER_FALLBACK_FLUSH_EVERY = 32
    }
}

private data class MediaResolverFallbackKey(
    val route: String,
    val ownerKind: String,
    val assetKind: String,
    val urlClass: String,
)

private class MediaResolverFallbackTelemetry(
    private val logger: Logger?,
    flushEvery: Int,
) {
    private val flushEvery = flushEvery.coerceAtLeast(1)
    private val counts = ConcurrentHashMap<MediaResolverFallbackKey, AtomicInteger>()
    private val seenKeys = ConcurrentHashMap<MediaResolverFallbackKey, Boolean>()
    private val pending = AtomicInteger(0)
    private val lock = Any()

    fun record(route: String, ownerKind: String, assetKind: String, urlClass: String) {
        val target = logger ?: return
        val key = MediaResolverFallbackKey(route, ownerKind, assetKind, urlClass)
        val counter = counts[key] ?: synchronized(lock) {
            counts[key] ?: AtomicInteger(0).also { counts[key] = it }
        }
        counter.incrementAndGet()
        val firstForKey = seenKeys.putIfAbsent(key, true) == null
        if (firstForKey || pending.incrementAndGet() >= flushEvery) flush(target)
    }

    private fun flush(target: Logger) {
        synchronized(lock) {
            pending.set(0)
            counts.forEach { (key, countRef) ->
                val count = countRef.getAndSet(0)
                if (count > 0) {
                    target.info(
                        event = "media_resolver_remote_fallbacks",
                        fields = mapOf(
                            "route" to key.route,
                            "owner_kind" to key.ownerKind,
                            "asset_kind" to key.assetKind,
                            "url_class" to key.urlClass,
                            "count" to count,
                        ),
                    )
                }
            }
        }
    }
}

private fun mediaResolverUrlClass(url: String): String {
    val trimmed = url.trim()
    val path = runCatching { URI(trimmed).path.orEmpty() }.getOrDefault("")
    return when {
        path.startsWith("/api/media/avatar/") -> "igloo_media_avatar"
        path.startsWith("/api/media/banner/") -> "igloo_media_banner"
        path.startsWith("/api/media/thumbnail/") -> "igloo_media_thumbnail"
        path.startsWith("/api/media/slide/") -> "igloo_media_slide"
        path.startsWith("/api/media/stream/") -> "igloo_media_stream"
        path.startsWith("/api/media/") -> "igloo_media_other"
        path.startsWith("/api/") -> "igloo_api_other"
        trimmed.startsWith("https://") || trimmed.startsWith("http://") -> "external_http"
        else -> "unknown"
    }
}
