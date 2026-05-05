package com.screwy.igloo.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import com.screwy.igloo.R

/**
 * Three-state holder for a route's primary content flow.
 *
 *  - [Loading] — Room has not emitted yet; routes paint a thin top progress bar.
 *  - [Empty]   — first emission was the empty list; routes render a muted message.
 *  - [Data]    — first non-empty emission arrived; routes render normal content.
 *
 * The generic payload [T] lets routes ship whatever shape they render when non-empty
 * (rows list, grid, etc.) without forcing it into the sealed type.
 */
sealed interface UiState<out T> {
    data object Loading : UiState<Nothing>
    data object Empty : UiState<Nothing>
    data class Data<T>(val value: T) : UiState<T>
}

/**
 * Branches render between the three [UiState] cases. `content` is only composed on
 * [UiState.Data]; the route pulls data via its own StateFlow/reactive APIs rather than
 * through the lambda parameter.
 */
@Composable
fun <T> UiStateSwitch(
    state: UiState<T>,
    emptyMessage: String? = null,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    when (state) {
        UiState.Loading -> Box(
            modifier = modifier.fillMaxSize(),
            contentAlignment = Alignment.TopCenter,
        ) {
            LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
        }
        UiState.Empty -> Box(
            modifier = modifier.fillMaxSize(),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                text = emptyMessage ?: stringResource(R.string.status_nothing_here_yet),
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        is UiState.Data -> content()
    }
}
