package com.screwy.igloo.ui.component

import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.TextLayoutResult
import com.screwy.igloo.ui.theme.iglooColors

/**
 * URL-only linkified text.
 * Shares [annotateUrls] with [AtMentionText] — no @mention detection.
 */
@Composable
fun LinkifyText(
    text: String,
    onUrlClick: (url: String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val urlColor = MaterialTheme.iglooColors.info
    val onSurface = MaterialTheme.iglooColors.onSurface

    val annotated = remember(text, urlColor) { annotateUrls(text, urlColor) }
    var layout by remember { mutableStateOf<TextLayoutResult?>(null) }

    Text(
        text = annotated,
        style = LocalTextStyle.current.copy(color = onSurface),
        modifier = modifier.pointerInput(annotated) {
            detectTapGestures { pos ->
                val l = layout ?: return@detectTapGestures
                val offset = l.getOffsetForPosition(pos)
                val hit = annotated.getStringAnnotations(offset, offset).firstOrNull()
                if (hit?.tag == "url") onUrlClick(hit.item)
            }
        },
        onTextLayout = { layout = it },
    )
}
