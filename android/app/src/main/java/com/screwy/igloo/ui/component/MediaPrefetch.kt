package com.screwy.igloo.ui.component

import android.content.Context
import coil3.ImageLoader
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.foundation.lazy.staggeredgrid.LazyStaggeredGridState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.snapshotFlow
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.net.isIglooServerUrl
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import java.net.URI

private const val MediaPrefetchBefore = 6
private const val MediaPrefetchAfter = 18
private const val ImagePrefetchBefore = 2
private const val ImagePrefetchAfter = 8

internal data class MediaPrefetchTarget(
    val ownerId: String? = null,
    val ownerKind: OwnerKind = OwnerKind.Tweet,
    val channelIds: Set<String> = emptySet(),
)

internal data class ImagePrefetchTarget(
    val uri: MediaUri,
    val widthPx: Int,
    val heightPx: Int,
)

@Composable
internal fun GridMediaPrefetchEffect(
    gridState: LazyGridState,
    itemCount: Int,
    headerOffset: Int,
    resolvers: MediaResolvers,
    targetForIndex: (Int) -> List<MediaPrefetchTarget>,
) {
    val currentTargetForIndex by rememberUpdatedState(targetForIndex)
    LaunchedEffect(gridState, itemCount, headerOffset, resolvers) {
        snapshotFlow {
            gridState.layoutInfo.visibleItemsInfo
                .mapNotNull { visible ->
                    (visible.index - headerOffset).takeIf { it in 0 until itemCount }
                }
                .sorted()
        }
            .distinctUntilChanged()
            .collectLatest { visibleIndices ->
                val targets = prefetchWindowIndices(visibleIndices, itemCount)
                    .flatMap { currentTargetForIndex(it) }
                    .distinct()
                prefetchMediaTargets(targets, resolvers)
            }
    }
}

@Composable
internal fun ListMediaPrefetchEffect(
    listState: LazyListState,
    itemCount: Int,
    headerOffset: Int,
    resolvers: MediaResolvers,
    targetForIndex: (Int) -> List<MediaPrefetchTarget>,
) {
    val currentTargetForIndex by rememberUpdatedState(targetForIndex)
    LaunchedEffect(listState, itemCount, headerOffset, resolvers) {
        snapshotFlow {
            listState.layoutInfo.visibleItemsInfo
                .mapNotNull { visible ->
                    (visible.index - headerOffset).takeIf { it in 0 until itemCount }
                }
                .sorted()
        }
            .distinctUntilChanged()
            .collectLatest { visibleIndices ->
                val targets = prefetchWindowIndices(visibleIndices, itemCount)
                    .flatMap { currentTargetForIndex(it) }
                    .distinct()
                prefetchMediaTargets(targets, resolvers)
            }
    }
}

@Composable
internal fun ListImagePrefetchEffect(
    listState: LazyListState,
    itemCount: Int,
    headerOffset: Int,
    imageLoader: ImageLoader,
    context: Context,
    bearerToken: String?,
    iglooHost: String,
    requestKey: Any?,
    targetForIndex: (Int) -> List<ImagePrefetchTarget>,
) {
    val currentTargetForIndex by rememberUpdatedState(targetForIndex)
    LaunchedEffect(listState, itemCount, headerOffset, imageLoader, requestKey, bearerToken, iglooHost) {
        snapshotFlow {
            listState.layoutInfo.visibleItemsInfo
                .mapNotNull { visible ->
                    (visible.index - headerOffset).takeIf { it in 0 until itemCount }
                }
                .sorted()
        }
            .distinctUntilChanged()
            .collectLatest { visibleIndices ->
                val targets = prefetchWindowIndices(
                    visibleIndices = visibleIndices,
                    itemCount = itemCount,
                    before = ImagePrefetchBefore,
                    after = ImagePrefetchAfter,
                )
                    .flatMap { currentTargetForIndex(it) }
                    .distinct()
                prefetchImageTargets(
                    targets = targets,
                    imageLoader = imageLoader,
                    context = context,
                    bearerToken = bearerToken,
                    iglooHost = iglooHost,
                )
            }
    }
}

@Composable
internal fun StaggeredGridMediaPrefetchEffect(
    gridState: LazyStaggeredGridState,
    itemCount: Int,
    headerOffset: Int,
    resolvers: MediaResolvers,
    targetForIndex: (Int) -> List<MediaPrefetchTarget>,
) {
    val currentTargetForIndex by rememberUpdatedState(targetForIndex)
    LaunchedEffect(gridState, itemCount, headerOffset, resolvers) {
        snapshotFlow {
            gridState.layoutInfo.visibleItemsInfo
                .mapNotNull { visible ->
                    (visible.index - headerOffset).takeIf { it in 0 until itemCount }
                }
                .sorted()
        }
            .distinctUntilChanged()
            .collectLatest { visibleIndices ->
                val targets = prefetchWindowIndices(visibleIndices, itemCount)
                    .flatMap { currentTargetForIndex(it) }
                    .distinct()
                prefetchMediaTargets(targets, resolvers)
            }
    }
}

@Composable
internal fun StaggeredGridImagePrefetchEffect(
    gridState: LazyStaggeredGridState,
    itemCount: Int,
    headerOffset: Int,
    imageLoader: ImageLoader,
    context: Context,
    bearerToken: String?,
    iglooHost: String,
    requestKey: Any?,
    targetForIndex: (Int) -> List<ImagePrefetchTarget>,
) {
    val currentTargetForIndex by rememberUpdatedState(targetForIndex)
    LaunchedEffect(gridState, itemCount, headerOffset, imageLoader, requestKey, bearerToken, iglooHost) {
        snapshotFlow {
            gridState.layoutInfo.visibleItemsInfo
                .mapNotNull { visible ->
                    (visible.index - headerOffset).takeIf { it in 0 until itemCount }
                }
                .sorted()
        }
            .distinctUntilChanged()
            .collectLatest { visibleIndices ->
                val targets = prefetchWindowIndices(
                    visibleIndices = visibleIndices,
                    itemCount = itemCount,
                    before = ImagePrefetchBefore,
                    after = ImagePrefetchAfter,
                )
                    .flatMap { currentTargetForIndex(it) }
                    .distinct()
                prefetchImageTargets(
                    targets = targets,
                    imageLoader = imageLoader,
                    context = context,
                    bearerToken = bearerToken,
                    iglooHost = iglooHost,
                )
            }
    }
}

internal fun prefetchWindowIndices(
    visibleIndices: List<Int>,
    itemCount: Int,
    before: Int = MediaPrefetchBefore,
    after: Int = MediaPrefetchAfter,
): List<Int> {
    if (itemCount <= 0 || visibleIndices.isEmpty()) return emptyList()
    val first = visibleIndices.minOrNull()?.coerceIn(0, itemCount - 1) ?: return emptyList()
    val last = visibleIndices.maxOrNull()?.coerceIn(0, itemCount - 1) ?: return emptyList()
    val start = (first - before).coerceAtLeast(0)
    val end = (last + after).coerceAtMost(itemCount - 1)
    return (start..end).toList()
}

internal suspend fun prefetchMediaTargets(
    targets: List<MediaPrefetchTarget>,
    resolvers: MediaResolvers,
) = coroutineScope {
    targets.distinct().forEach { target ->
        target.ownerId?.takeIf { it.isNotBlank() }?.let { ownerId ->
            launch { resolvers.thumbnailForPostFlow(ownerId, target.ownerKind).first() }
        }
    }
    targets
        .flatMap { it.channelIds }
        .map { it.trim() }
        .filter { it.isNotEmpty() }
        .distinct()
        .forEach { channelId ->
            launch { resolvers.avatarForChannelFlow(channelId).first() }
            launch { resolvers.bannerForChannelFlow(channelId).first() }
        }
}

internal fun prefetchImageTargets(
    targets: List<ImagePrefetchTarget>,
    imageLoader: ImageLoader,
    context: Context,
    bearerToken: String?,
    iglooHost: String,
) {
    targets
        .distinct()
        .filter { target ->
            val uri = target.uri
            uri !is MediaUri.Remote || isSafeRemotePrefetchUrl(uri.url, iglooHost)
        }
        .forEach { target ->
            buildMediaImageRequest(
                context = context,
                uri = target.uri,
                bearerToken = bearerToken,
                iglooHost = iglooHost,
                widthPx = target.widthPx,
                heightPx = target.heightPx,
            )?.let(imageLoader::enqueue)
        }
}

internal fun isSafeRemotePrefetchUrl(url: String, iglooHost: String): Boolean {
    if (url.isBlank()) return false
    val parsed = runCatching { URI(url) }.getOrNull() ?: return false
    val scheme = parsed.scheme?.lowercase() ?: return false
    if (scheme != "http" && scheme != "https") return false
    if (isIglooServerUrl(url, iglooHost)) return true
    val host = parsed.host?.lowercase() ?: return false
    if (host == "localhost") return false
    return !isPrivateOrLocalLiteralHost(host)
}

private fun isPrivateOrLocalLiteralHost(host: String): Boolean {
    val ipv4 = IPV4_REGEX.matchEntire(host)?.groupValues?.drop(1)?.mapNotNull(String::toIntOrNull)
    if (ipv4 != null && ipv4.size == 4) {
        val (a, b, _, _) = ipv4
        return a == 10 ||
            (a == 172 && b in 16..31) ||
            (a == 192 && b == 168) ||
            a == 127 ||
            (a == 169 && b == 254) ||
            a == 0
    }
    val normalized = host.removePrefix("[").removeSuffix("]")
    if (!normalized.contains(":")) return false
    return normalized == "::1" ||
        normalized.startsWith("fc", ignoreCase = true) ||
        normalized.startsWith("fd", ignoreCase = true) ||
        normalized.startsWith("fe8", ignoreCase = true) ||
        normalized.startsWith("fe9", ignoreCase = true) ||
        normalized.startsWith("fea", ignoreCase = true) ||
        normalized.startsWith("feb", ignoreCase = true)
}

private val IPV4_REGEX = Regex("""^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$""")
