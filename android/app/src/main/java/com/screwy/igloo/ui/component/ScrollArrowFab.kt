package com.screwy.igloo.ui.component

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SmallFloatingActionButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import com.screwy.igloo.R
import com.screwy.igloo.ui.theme.iglooColors

internal data class ScrollArrowVisibility(
    val showTop: Boolean,
    val showBottom: Boolean,
)

internal fun scrollArrowVisibility(
    showScrollFabs: Boolean,
    itemCount: Int,
    visibleItemCount: Int,
    firstVisibleItemIndex: Int,
    firstVisibleItemScrollOffset: Int,
): ScrollArrowVisibility {
    if (!showScrollFabs || itemCount <= 0 || visibleItemCount <= 0) {
        return ScrollArrowVisibility(showTop = false, showBottom = false)
    }

    val isAtTop = firstVisibleItemIndex == 0 && firstVisibleItemScrollOffset == 0
    val hasBottomJumpTarget = itemCount > visibleItemCount * 2
    if (isAtTop) {
        return ScrollArrowVisibility(showTop = false, showBottom = hasBottomJumpTarget)
    }

    return ScrollArrowVisibility(
        showTop = firstVisibleItemIndex >= visibleItemCount,
        showBottom = false,
    )
}

@Composable
fun ScrollToTopFab(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    ScrollArrowFab(
        icon = Icons.Default.KeyboardArrowUp,
        contentDescription = stringResource(R.string.a11y_scroll_to_top),
        onClick = onClick,
        modifier = modifier,
    )
}

@Composable
fun ScrollToBottomFab(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    ScrollArrowFab(
        icon = Icons.Default.KeyboardArrowDown,
        contentDescription = stringResource(R.string.a11y_scroll_to_bottom),
        onClick = onClick,
        modifier = modifier,
    )
}

@Composable
private fun ScrollArrowFab(
    icon: ImageVector,
    contentDescription: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val colors = MaterialTheme.iglooColors
    SmallFloatingActionButton(
        onClick = onClick,
        modifier = modifier,
        containerColor = colors.primary,
        contentColor = colors.onPrimary,
    ) {
        Icon(icon, contentDescription = contentDescription)
    }
}
