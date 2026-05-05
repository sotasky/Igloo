package com.screwy.igloo.ui.component

import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.isIglooServerUrl as isIglooServerUrlForHost
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.compose.koinInject

/**
 * Shared media helpers.
 *
 * `mediaAlpha` implements the "Igloo Remote while offline fades to 0.55"
 * pattern. Public CDN remotes are not gated by Igloo server reachability:
 * offline-first means offline from Igloo, not necessarily offline from the
 * public internet.
 */

internal fun shouldRenderVideoPosterOverlay(
    hasPlayer: Boolean,
    firstFrameRendered: Boolean,
): Boolean = !hasPlayer || !firstFrameRendered

/**
 * Returns the display alpha for a [MediaUri] per §8 media-state patterns:
 *  - [MediaUri.Local] renders at full opacity.
 *  - [MediaUri.Missing] renders its fallback at full opacity (never faded).
 *  - [MediaUri.Remote] renders faded only when it points at the Igloo server
 *    and server reachability is offline.
 */
@Composable
fun mediaAlpha(uri: MediaUri): Float = when (uri) {
    is MediaUri.Local   -> 1f
    is MediaUri.Missing -> 1f
    is MediaUri.Remote  -> if (isIglooRemoteOffline(uri)) 0.55f else 1f
}

/**
 * Reads the [Reachability] state machine and returns `true` only when the server
 * was last seen Online. `Unknown` is treated as offline (matching §8: "faded
 * because not cached" should fire when we can't prove the server is reachable).
 */
@Composable
fun isOnline(): Boolean {
    val reachability: Reachability = koinInject()
    val state by reachability.state.collectAsStateWithLifecycle()
    return state is Reachability.State.Online
}

/**
 * True only when the URI is served by Igloo and the Igloo server is currently
 * unreachable. Direct public media URLs should still get a chance to render/play.
 */
@Composable
fun isIglooRemoteOffline(uri: MediaUri): Boolean {
    if (uri !is MediaUri.Remote) return false
    return isIglooRemoteOfflineUrl(uri.url)
}

@Composable
fun isIglooRemoteOfflineUrl(url: String): Boolean {
    val iglooHostProvider: IglooHostProvider = koinInject()
    return isIglooServerUrl(url, iglooHostProvider.hostSync()) && !isOnline()
}

internal fun isIglooServerUrl(url: String, iglooHost: String): Boolean {
    return isIglooServerUrlForHost(url, iglooHost)
}

/**
 * Shared "cloud-download pending" badge overlay per §8 lines 1334-1337.
 *
 * Renders a 12dp cloud-download icon in the bottom-right of the containing [Box],
 * tinted [com.screwy.igloo.ui.theme.IglooColors.onSurfaceMuted]. Callers
 * render this inside a `Box { AsyncImage(...); if (isIglooRemoteOffline(uri))
 * DownloadPendingBadge() }` pattern to signal "this will download when tapped."
 */
@Composable
fun BoxScope.DownloadPendingBadge() {
    Icon(
        imageVector = Icons.Default.CloudDownload,
        contentDescription = null,
        tint = MaterialTheme.iglooColors.onSurfaceMuted,
        modifier = Modifier
            .align(Alignment.BottomEnd)
            .padding(2.dp)
            .size(12.dp),
    )
}
