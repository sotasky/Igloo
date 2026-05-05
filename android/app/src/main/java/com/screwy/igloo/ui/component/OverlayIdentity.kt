package com.screwy.igloo.ui.component

import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.statusBars
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import kotlin.math.max

/**
 * Shared top inset for the top-left avatar/name chrome used by fullscreen feed
 * media and moments overlays.
 */
@Composable
internal fun overlayIdentityTopPadding() = with(LocalDensity.current) {
    max(WindowInsets.statusBars.getTop(this).toDp().value + 10f, 34f).dp
}
