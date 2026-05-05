package com.screwy.igloo.player

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
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle

internal const val TAG_MENTION = "mention"
internal const val TAG_URL = "url"
internal const val TAG_TIMESTAMP = "timestamp"

internal val PLAYER_URL_REGEX = Regex("""https?://\S+""")
internal val PLAYER_TIMESTAMP_REGEX = Regex("""\b(\d{1,2}):(\d{2})(?::(\d{2}))?\b""")
internal val PLAYER_MENTION_REGEX = Regex("""@[A-Za-z0-9_](?:[A-Za-z0-9._-]*[A-Za-z0-9_])?""")

@Composable
fun PlayerLinkedText(
    text: String,
    onMentionClick: (String) -> Unit,
    onUrlClick: (String) -> Unit,
    onTimestampClick: (Long) -> Unit,
    modifier: Modifier = Modifier,
    maxLines: Int = Int.MAX_VALUE,
    style: TextStyle = LocalTextStyle.current,
) {
    val linkColor = MaterialTheme.colorScheme.primary
    val annotated = remember(text, linkColor) {
        annotatePlayerLinkedText(text, linkColor)
    }
    var layout by remember { mutableStateOf<TextLayoutResult?>(null) }

    Text(
        text = annotated,
        style = style,
        maxLines = maxLines,
        modifier = modifier.pointerInput(annotated) {
            detectTapGestures { pos ->
                val l = layout ?: return@detectTapGestures
                val offset = l.getOffsetForPosition(pos)
                val hit = annotated.getStringAnnotations(offset, offset).firstOrNull()
                when (hit?.tag) {
                    TAG_MENTION -> onMentionClick(hit.item)
                    TAG_URL -> onUrlClick(hit.item)
                    TAG_TIMESTAMP -> onTimestampClick(hit.item.toLong())
                }
            }
        },
        onTextLayout = { layout = it },
    )
}

internal fun annotatePlayerLinkedText(
    text: String,
    linkColor: androidx.compose.ui.graphics.Color,
): AnnotatedString {
    val spans = mutableListOf<PlayerTextSpan>()

    PLAYER_URL_REGEX.findAll(text).forEach { match ->
        spans += PlayerTextSpan(
            start = match.range.first,
            end = match.range.last + 1,
            tag = TAG_URL,
            item = match.value,
        )
    }

    PLAYER_TIMESTAMP_REGEX.findAll(text).forEach { match ->
        val start = match.range.first
        val end = match.range.last + 1
        if (spans.any { it.overlaps(start, end) }) return@forEach
        val groups = match.groupValues
        val timestampMs = when {
            groups[3].isNotEmpty() -> {
                ((groups[1].toLong() * 3600L) + (groups[2].toLong() * 60L) + groups[3].toLong()) * 1000L
            }
            else -> {
                ((groups[1].toLong() * 60L) + groups[2].toLong()) * 1000L
            }
        }
        spans += PlayerTextSpan(
            start = start,
            end = end,
            tag = TAG_TIMESTAMP,
            item = timestampMs.toString(),
        )
    }

    PLAYER_MENTION_REGEX.findAll(text).forEach { match ->
        val start = match.range.first
        val end = match.range.last + 1
        if (spans.any { it.overlaps(start, end) }) return@forEach
        spans += PlayerTextSpan(
            start = start,
            end = end,
            tag = TAG_MENTION,
            item = match.value.drop(1),
        )
    }

    spans.sortBy { it.start }

    return buildAnnotatedString {
        var cursor = 0
        for (span in spans) {
            if (span.start > cursor) append(text.substring(cursor, span.start))
            pushStringAnnotation(tag = span.tag, annotation = span.item)
            withStyle(SpanStyle(color = linkColor)) {
                append(text.substring(span.start, span.end))
            }
            pop()
            cursor = span.end
        }
        if (cursor < text.length) append(text.substring(cursor))
    }
}

private data class PlayerTextSpan(
    val start: Int,
    val end: Int,
    val tag: String,
    val item: String,
) {
    fun overlaps(otherStart: Int, otherEnd: Int): Boolean =
        start < otherEnd && end > otherStart
}
