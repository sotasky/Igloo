package com.screwy.igloo.player

import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R

@Composable
internal fun SponsorBlockSkipButton(
    segment: SponsorBlockUiSegment?,
    bottomPadding: Dp,
    onSkip: (SponsorBlockUiSegment) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (segment == null) return
    Button(
        onClick = { onSkip(segment) },
        modifier = modifier.padding(end = 12.dp, bottom = bottomPadding),
        colors = ButtonDefaults.buttonColors(
            containerColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.92f),
        ),
        shape = RoundedCornerShape(8.dp),
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
    ) {
        Text(
            text = stringResource(R.string.action_skip),
            style = MaterialTheme.typography.labelLarge,
            color = Color.White,
        )
    }
}

@Composable
internal fun SponsorBlockAutoSkipNotification(
    message: String?,
    bottomPadding: Dp,
    modifier: Modifier = Modifier,
) {
    if (message.isNullOrBlank()) return
    Surface(
        modifier = modifier.padding(bottom = bottomPadding),
        color = Color.Black.copy(alpha = 0.78f),
        shape = RoundedCornerShape(8.dp),
    ) {
        Text(
            text = message,
            style = MaterialTheme.typography.labelMedium,
            color = Color.White,
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
        )
    }
}
