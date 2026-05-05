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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import com.screwy.igloo.ui.theme.iglooColors

/**
 * Body text with tappable `@handle` and `http(s)://` spans.
 */
@Composable
fun AtMentionText(
    text: String,
    onMentionClick: (handle: String) -> Unit,
    onUrlClick: (url: String) -> Unit = { },
    maxLines: Int = Int.MAX_VALUE,
    style: TextStyle = LocalTextStyle.current,
    mentionColorOverride: Color? = null,
    urlColorOverride: Color? = null,
    modifier: Modifier = Modifier,
    overflow: TextOverflow = TextOverflow.Clip,
    onPlainTextClick: (() -> Unit)? = null,
    onTextLayout: (TextLayoutResult) -> Unit = { },
) {
    val mentionColor = mentionColorOverride ?: MaterialTheme.iglooColors.primary
    val urlColor = urlColorOverride ?: MaterialTheme.iglooColors.info
    val baseColor = if (style.color == Color.Unspecified) {
        MaterialTheme.iglooColors.onSurface
    } else {
        style.color
    }

    val annotated = remember(text, mentionColor, urlColor) {
        annotateMentionsAndUrls(text, mentionColor, urlColor)
    }
    var layout by remember { mutableStateOf<TextLayoutResult?>(null) }

    Text(
        text = annotated,
        style = style.copy(color = baseColor),
        maxLines = maxLines,
        modifier = modifier.pointerInput(annotated) {
            detectTapGestures { pos ->
                val l = layout ?: return@detectTapGestures
                val offset = l.getOffsetForPosition(pos)
                val hit = annotatedTextLinkAt(annotated, offset)
                when (hit?.tag) {
                    "mention" -> onMentionClick(hit.item)
                    "url"     -> onUrlClick(hit.item)
                    else      -> onPlainTextClick?.invoke()
                }
            }
        },
        overflow = overflow,
        onTextLayout = {
            layout = it
            onTextLayout(it)
        },
    )
}

internal fun annotatedTextLinkAt(
    annotated: AnnotatedString,
    offset: Int,
): AnnotatedString.Range<String>? = annotated
    .getStringAnnotations(offset, offset)
    .firstOrNull { it.tag == "mention" || it.tag == "url" }

internal val MENTION_REGEX = Regex("""@[A-Za-z0-9_](?:[A-Za-z0-9._-]*[A-Za-z0-9_])?""")
internal val URL_REGEX = Regex("https?://\\S+")

/**
 * Pure helper — annotates every `@handle` span with tag="mention"/item=handle (no `@`),
 * every `http(s)://...` span with tag="url"/item=url. Handle/URL ranges are disjoint
 * because URLs start with `h` and mentions with `@`.
 */
fun annotateMentionsAndUrls(
    text: String,
    mentionColor: Color,
    urlColor: Color,
): AnnotatedString = buildAnnotatedString {
    val mentions = MENTION_REGEX.findAll(text).map { m -> Span(m.range.first, m.range.last + 1, "mention", m.value.drop(1)) }
    val urls = URL_REGEX.findAll(text).map { m -> Span(m.range.first, m.range.last + 1, "url", m.value) }
    val all = (mentions + urls).sortedBy { it.start }

    var cursor = 0
    for (s in all) {
        if (s.start > cursor) append(text.substring(cursor, s.start))
        val segment = text.substring(s.start, s.end)
        pushStringAnnotation(tag = s.tag, annotation = s.item)
        val style = when (s.tag) {
            "mention" -> SpanStyle(color = mentionColor)
            "url"     -> SpanStyle(color = urlColor, textDecoration = TextDecoration.Underline)
            else      -> SpanStyle()
        }
        withStyle(style) { append(segment) }
        pop()
        cursor = s.end
    }
    if (cursor < text.length) append(text.substring(cursor))
}

/** URL-only counterpart to [annotateMentionsAndUrls]. */
fun annotateUrls(text: String, urlColor: Color): AnnotatedString = buildAnnotatedString {
    var cursor = 0
    for (m in URL_REGEX.findAll(text)) {
        if (m.range.first > cursor) append(text.substring(cursor, m.range.first))
        pushStringAnnotation(tag = "url", annotation = m.value)
        withStyle(SpanStyle(color = urlColor, textDecoration = TextDecoration.Underline)) {
            append(m.value)
        }
        pop()
        cursor = m.range.last + 1
    }
    if (cursor < text.length) append(text.substring(cursor))
}

private data class Span(val start: Int, val end: Int, val tag: String, val item: String)
