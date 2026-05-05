package com.screwy.igloo.ui.component

import androidx.compose.foundation.border
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.screwy.igloo.ui.theme.IglooColors

enum class StoryRingState {
    None,
    Seen,
    New,
}

internal fun storyRingState(storyCount: Int, unseenCount: Int): StoryRingState = when {
    storyCount <= 0 -> StoryRingState.None
    unseenCount > 0 -> StoryRingState.New
    else -> StoryRingState.Seen
}

internal fun storyRingColor(state: StoryRingState, colors: IglooColors): Color? = when (state) {
    StoryRingState.None -> null
    StoryRingState.New -> colors.primary
    StoryRingState.Seen -> colors.onSurfaceMuted
}

internal fun Modifier.storyRingBorder(
    state: StoryRingState,
    colors: IglooColors,
    width: Dp = 3.dp,
): Modifier {
    val color = storyRingColor(state, colors) ?: return this
    return border(width = width, color = color, shape = CircleShape)
}
