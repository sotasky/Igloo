package com.screwy.igloo.ui.component

import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.screwy.igloo.R

/** Confirmation shared by all entry points that remove only the primary video binary. */
@Composable
internal fun DeleteDownloadedVideoDialog(
    videoId: String?,
    onDismiss: () -> Unit,
    onConfirm: (videoId: String) -> Unit,
) {
    val id = videoId ?: return
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.action_delete_downloaded_video)) },
        text = { Text(stringResource(R.string.confirm_delete_downloaded_video_body)) },
        confirmButton = {
            TextButton(onClick = { onConfirm(id) }) {
                Text(
                    text = stringResource(R.string.action_delete),
                    color = MaterialTheme.colorScheme.error,
                )
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text(stringResource(R.string.action_cancel))
            }
        },
    )
}
