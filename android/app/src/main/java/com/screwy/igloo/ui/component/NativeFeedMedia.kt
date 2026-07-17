// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.

package com.screwy.igloo.ui.component

import android.content.Context
import android.graphics.Rect
import android.view.View
import android.widget.ImageView
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.width
import androidx.compose.ui.unit.dp
import androidx.media3.common.MediaItem as Media3Item
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import androidx.recyclerview.widget.RecyclerView
import com.screwy.igloo.feed.FeedMediaCellModel
import com.screwy.igloo.media.MediaUri
import kotlin.math.abs

internal data class NativeVideoSlot(
    val key: String,
    val streamUri: MediaUri,
    val container: View,
    val playerView: PlayerView,
    val poster: ImageView,
)

internal data class NativeInlineVideoCandidate(
    val key: String,
    val streamUri: MediaUri,
    val visibleFraction: Float,
    val centerDistancePx: Float,
)

internal fun chooseNativeInlineVideoCandidate(
    candidates: Collection<NativeInlineVideoCandidate>,
    minVisibleFraction: Float = 0.25f,
): NativeInlineVideoCandidate? =
    candidates
        .asSequence()
        .filter { it.streamUri !is MediaUri.Missing }
        .filter { it.visibleFraction >= minVisibleFraction }
        .sortedWith(compareBy<NativeInlineVideoCandidate> { abs(it.centerDistancePx) }.thenBy { it.key })
        .firstOrNull()

internal enum class NativeInlineVideoSwitchDecision {
    ResumeActive,
    HandoffSameStream,
    PrepareNewStream,
}

internal fun nativeInlineVideoSwitchDecision(
    activeKey: String?,
    activeStreamUri: MediaUri?,
    selected: NativeInlineVideoCandidate,
): NativeInlineVideoSwitchDecision =
    when {
        selected.key == activeKey && selected.streamUri == activeStreamUri ->
            NativeInlineVideoSwitchDecision.ResumeActive
        selected.streamUri == activeStreamUri ->
            NativeInlineVideoSwitchDecision.HandoffSameStream
        else ->
            NativeInlineVideoSwitchDecision.PrepareNewStream
    }

internal fun nativeInlineVideoPosterVisibility(
    hasPlayer: Boolean,
    firstFrameRendered: Boolean,
): Int =
    if (shouldRenderVideoPosterOverlay(hasPlayer, firstFrameRendered)) View.VISIBLE else View.GONE

internal class NativeInlineVideoManager(
    private val player: ExoPlayer,
) {
    private var activeKey: String? = null
    private var activeStreamUri: MediaUri? = null
    private var activePlayerView: PlayerView? = null
    private var activePoster: ImageView? = null
    private var firstFrameRendered = false

    private val listener = object : Player.Listener {
        override fun onRenderedFirstFrame() {
            firstFrameRendered = true
            activePlayerView?.visibility = View.VISIBLE
            activePlayerView?.alpha = 1f
            updateActivePosterVisibility()
        }
    }

    init {
        player.volume = 0f
        player.repeatMode = Player.REPEAT_MODE_ALL
        player.addListener(listener)
    }

    fun selectFrom(recyclerView: RecyclerView) {
        val slots = visibleVideoSlots(recyclerView)
        val candidates = slots.map { (_, candidate) -> candidate }
        val selected = chooseNativeInlineVideoCandidate(candidates)
        if (selected == null) {
            val active = activeKey?.let { key -> slots.firstOrNull { it.second.key == key }?.second }
            if (active != null && active.visibleFraction > 0.08f) {
                player.playWhenReady = true
                player.play()
                return
            }
            clearActive(pause = true)
            return
        }
        val slot = slots.firstOrNull { it.second.key == selected.key }?.first ?: return
        when (nativeInlineVideoSwitchDecision(activeKey, activeStreamUri, selected)) {
            NativeInlineVideoSwitchDecision.ResumeActive -> {
                attachTo(slot, keepPrepared = true)
                player.playWhenReady = true
                player.play()
                return
            }
            NativeInlineVideoSwitchDecision.HandoffSameStream -> {
                attachTo(slot, keepPrepared = true)
                activeKey = selected.key
                player.playWhenReady = true
                player.play()
                return
            }
            NativeInlineVideoSwitchDecision.PrepareNewStream -> Unit
        }

        val mediaItem = selected.streamUri.toMedia3ItemOrNull() ?: return
        firstFrameRendered = false
        attachTo(slot, keepPrepared = false)
        activeKey = selected.key
        activeStreamUri = selected.streamUri
        player.setMediaItem(mediaItem)
        player.prepare()
        player.playWhenReady = true
        player.play()
    }

    fun detachSlot(key: String) {
        if (key != activeKey) return
        clearActive(pause = true)
    }

    fun pause() {
        player.playWhenReady = false
        player.pause()
    }

    fun release() {
        player.removeListener(listener)
        clearActive(pause = false)
        player.release()
    }

    private fun attachTo(slot: NativeVideoSlot, keepPrepared: Boolean) {
        if (activePlayerView !== slot.playerView) {
            activePlayerView?.player = null
            activePlayerView?.visibility = View.GONE
            activePlayerView?.alpha = 0f
            activePoster?.visibility = View.VISIBLE
            slot.playerView.player = player
            activePlayerView = slot.playerView
            activePoster = slot.poster
        }
        slot.playerView.visibility = View.VISIBLE
        slot.playerView.alpha = if (keepPrepared && firstFrameRendered) 1f else 0f
        updateActivePosterVisibility()
    }

    private fun updateActivePosterVisibility() {
        activePoster?.visibility =
            nativeInlineVideoPosterVisibility(
                hasPlayer = activePlayerView?.player != null,
                firstFrameRendered = firstFrameRendered,
            )
    }

    private fun clearActive(pause: Boolean) {
        if (pause) pause()
        activePlayerView?.player = null
        activePlayerView?.visibility = View.GONE
        activePlayerView?.alpha = 0f
        activePoster?.visibility = View.VISIBLE
        activePlayerView = null
        activePoster = null
        activeKey = null
        activeStreamUri = null
        firstFrameRendered = false
    }

    private fun visibleVideoSlots(recyclerView: RecyclerView): List<Pair<NativeVideoSlot, NativeInlineVideoCandidate>> {
        val viewport = Rect(0, recyclerView.paddingTop, recyclerView.width, recyclerView.height - recyclerView.paddingBottom)
        val viewportCenter = viewport.centerY().toFloat()
        val result = mutableListOf<Pair<NativeVideoSlot, NativeInlineVideoCandidate>>()
        for (index in 0 until recyclerView.childCount) {
            val holder = recyclerView.getChildViewHolder(recyclerView.getChildAt(index)) as? NativeFeedViewHolder ?: continue
            holder.videoSlotsForSelection().forEach { slot ->
                val rect = Rect()
                if (!slot.container.getGlobalVisibleRect(rect)) return@forEach
                val rvRect = Rect()
                recyclerView.getGlobalVisibleRect(rvRect)
                rect.offset(-rvRect.left, -rvRect.top)
                val visibleFraction = nativeVisibleHeightFraction(rect, viewport.height())
                result += slot to NativeInlineVideoCandidate(
                    key = slot.key,
                    streamUri = slot.streamUri,
                    visibleFraction = visibleFraction,
                    centerDistancePx = rect.centerY().toFloat() - viewportCenter,
                )
            }
        }
        return result
    }
}

internal fun nativeVisibleHeightFraction(bounds: Rect, viewportHeight: Int): Float {
    if (viewportHeight <= 0 || bounds.height() <= 0) return 0f
    val visibleTop = bounds.top.coerceAtLeast(0)
    val visibleBottom = bounds.bottom.coerceAtMost(viewportHeight)
    val visibleHeight = (visibleBottom - visibleTop).coerceAtLeast(0)
    return visibleHeight.toFloat() / bounds.height().toFloat()
}

internal fun nativeStableSingleMediaAspectRatio(cell: com.screwy.igloo.feed.FeedMediaCellDescriptor): Float =
    when {
        cell.aspectRatioKnown -> cell.aspectRatio.takeIf { it.isFinite() && it > 0f } ?: 1f
        cell.isVideo -> 16f / 9f
        else -> 1f
    }

internal fun nativeStableSingleMediaAspectRatio(cell: FeedMediaCellModel): Float =
    nativeStableSingleMediaAspectRatio(cell.descriptor)

internal data class NativeMediaDimensions(
    val widthPx: Int,
    val heightPx: Int,
)

internal fun nativeSingleMediaDimensions(
    maxWidthPx: Int,
    aspectRatio: Float,
    maxHeightPx: Int = dp(560),
): NativeMediaDimensions {
    val safeRatio = aspectRatio.takeIf { it.isFinite() && it > 0f } ?: 1f
    val maxWidth = maxWidthPx.coerceAtLeast(1)
    val maxHeight = maxHeightPx.coerceAtLeast(1)
    val fullWidthHeight = (maxWidth / safeRatio).toInt()
    val height = fullWidthHeight.coerceAtMost(maxHeight).coerceAtLeast(1)
    val width = (height * safeRatio).toInt().coerceAtMost(maxWidth).coerceAtLeast(1)
    return NativeMediaDimensions(widthPx = width, heightPx = height)
}

internal fun nativeMultiMediaCellDimensions(
    visibleCellCount: Int,
    cellIndex: Int,
    gridWidthPx: Int,
    gapPx: Int,
): NativeMediaDimensions {
    val cellWidth = (gridWidthPx - gapPx).coerceAtLeast(1) / 2
    return when (visibleCellCount) {
        2 -> NativeMediaDimensions(widthPx = cellWidth, heightPx = cellWidth)
        3 -> {
            val gridHeight = (gridWidthPx / 1.6f).toInt().coerceAtLeast(1)
            val rightCellHeight = (gridHeight - gapPx).coerceAtLeast(1) / 2
            if (cellIndex == 0) {
                NativeMediaDimensions(widthPx = cellWidth, heightPx = gridHeight)
            } else {
                NativeMediaDimensions(widthPx = cellWidth, heightPx = rightCellHeight)
            }
        }
        else -> NativeMediaDimensions(widthPx = cellWidth, heightPx = cellWidth)
    }
}

internal fun nativeMediaGridWidthPx(context: Context): Int =
    (context.resources.displayMetrics.widthPixels - dp(36)).coerceAtLeast(dp(160))

internal fun nativeQuoteMediaGridWidthPx(
    mediaGridWidthPx: Int,
    horizontalPaddingPx: Int,
): Int =
    (mediaGridWidthPx - horizontalPaddingPx * 2).coerceAtLeast(1)

internal fun nativeMediaScaleTypeFor(
    cell: com.screwy.igloo.feed.FeedMediaCellDescriptor,
    isSingle: Boolean = false,
): ImageView.ScaleType =
    if (isSingle && cell.aspectRatioKnown && !cell.isVideo) {
        ImageView.ScaleType.FIT_START
    } else {
        ImageView.ScaleType.CENTER_CROP
    }

internal fun FeedMediaCellModel.artworkUri(): MediaUri {
    when (val item = previewItem) {
        is MediaItem.Image -> return item.uri
        is MediaItem.Video -> if (item.thumbnailUri !is MediaUri.Missing) return item.thumbnailUri
        is MediaItem.Gif -> return item.streamUri
        null -> Unit
    }
	return MediaUri.Missing
}

internal fun FeedMediaCellModel.streamUri(): MediaUri {
    when (val item = previewItem) {
        is MediaItem.Video -> return item.streamUri
        is MediaItem.Gif -> return item.streamUri
        is MediaItem.Image, null -> Unit
    }
	return MediaUri.Missing
}

private fun MediaUri.toMedia3ItemOrNull(): Media3Item? = when (this) {
    is MediaUri.Local -> Media3Item.fromUri(file.toURI().toString())
    is MediaUri.Remote -> Media3Item.fromUri(url)
    MediaUri.Missing -> null
}
