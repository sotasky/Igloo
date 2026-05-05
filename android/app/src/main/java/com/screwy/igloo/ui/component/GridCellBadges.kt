package com.screwy.igloo.ui.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Photo
import androidx.compose.material.icons.filled.PhotoLibrary
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp

/**
 * Shared overlays for the 3-column thumbnail grids (Moments All view, TikTok /
 * Instagram channel grids, Bookmarks tab). Single source of truth so all three
 * surfaces render the same media-type icon (bottom-left) and relative timestamp
 * (bottom-right).
 *
 * The cells previously diverged: bookmarks showed a "play" glyph bottom-right,
 * MomentCell showed a duration pill (channel-scoped) plus a cloud-download badge
 * for offline media, and neither showed a timestamp. We now standardise on:
 *
 *   - bottom-left  → [MediaTypeBadge] (Video / Image / Slideshow)
 *   - bottom-right → [TimestampBadge] (relative time, "5m ago" / "1d ago" / …)
 *
 * The "not downloaded" cloud-download badge is intentionally dropped from the
 * grids — the existing alpha fade on [com.screwy.igloo.media.MediaUri.Remote]
 * already conveys the same signal without stealing the icon slot.
 */
enum class MediaType { Video, Image, Slideshow }

/**
 * Resolves a [MediaType] from the on-device shape used by both the videos table
 * (TikTok / Instagram / YouTube) and tweet feed items.
 *
 * `slideCount > 1` is the universal slideshow signal — Twitter multi-photo
 * tweets carry no `media_kind`, so this rule must fire first to catch them.
 * Falls back to [mediaKind] for the single-asset cases (single image vs video).
 */
internal fun mediaTypeFor(mediaKind: String?, slideCount: Int): MediaType {
    if (slideCount > 1) return MediaType.Slideshow
    val kind = mediaKind?.trim()?.lowercase().orEmpty()
    return when (kind) {
        "slideshow" -> MediaType.Slideshow
        "image", "photo" -> MediaType.Image
        else -> MediaType.Video
    }
}

/**
 * Bottom-left circular badge indicating the media kind of the underlying post.
 * Mirrors the size of the previous bookmarks "play" glyph so cells don't reflow.
 */
@Composable
fun BoxScope.MediaTypeBadge(mediaType: MediaType) {
    val icon: ImageVector = when (mediaType) {
        MediaType.Video -> Icons.Filled.PlayArrow
        MediaType.Image -> Icons.Filled.Photo
        MediaType.Slideshow -> Icons.Filled.PhotoLibrary
    }
    Box(
        modifier = Modifier
            .align(Alignment.BottomStart)
            .padding(6.dp)
            .size(22.dp)
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = 0.55f)),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(14.dp),
        )
    }
}

/**
 * Bottom-right relative-time pill. No-ops when [publishedAtMs] is unset (`<= 0`)
 * so legacy rows without an ingest timestamp don't render "55y ago".
 */
@Composable
fun BoxScope.TimestampBadge(publishedAtMs: Long) {
    if (publishedAtMs <= 0L) return
    Box(
        modifier = Modifier
            .align(Alignment.BottomEnd)
            .padding(6.dp)
            .clip(RoundedCornerShape(3.dp))
            .background(Color.Black.copy(alpha = 0.55f))
            .padding(horizontal = 4.dp, vertical = 1.dp),
    ) {
        Text(
            text = localizedRelativeTime(publishedAtMs),
            style = MaterialTheme.typography.labelSmall,
            color = Color.White,
        )
    }
}
