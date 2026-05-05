package com.screwy.igloo.player

internal data class VttCueBlock(
    val startMs: Long,
    val endMs: Long,
    val lines: List<String>,
)

internal fun parseVttCueBlocks(content: String): List<VttCueBlock> {
    if (content.isBlank()) return emptyList()
    val lines = content.replace("\r\n", "\n").replace('\r', '\n').lines()
    val out = mutableListOf<VttCueBlock>()
    var i = 0
    while (i < lines.size) {
        val line = lines[i].trim()
        if (line.isEmpty() || isVttPreambleLine(line)) {
            i++
            continue
        }
        val arrowIdx = line.indexOf("-->")
        if (arrowIdx < 0) {
            i++
            continue
        }
        val start = parseVttTimestampMs(line.substring(0, arrowIdx).trim())
        val end = parseVttTimestampMs(
            line.substring(arrowIdx + 3)
                .trim()
                .substringBefore(' '),
        )
        i++
        val cueLines = mutableListOf<String>()
        while (i < lines.size) {
            val body = lines[i]
            if (body.isBlank()) break
            cueLines += body.trim()
            i++
        }
        if (start != null && end != null) {
            out += VttCueBlock(startMs = start, endMs = end, lines = cueLines)
        }
    }
    return out
}

/** `HH:MM:SS.mmm` or `MM:SS.mmm` -> milliseconds. Returns null on malformed input. */
internal fun parseVttTimestampMs(raw: String): Long? {
    val parts = raw.replace(',', '.').split(':')
    if (parts.size !in 2..3) return null
    val hours = if (parts.size == 3) parts[0].toLongOrNull() ?: return null else 0L
    val minutes = parts[parts.size - 2].toLongOrNull() ?: return null
    val seconds = parts[parts.size - 1].toDoubleOrNull() ?: return null
    return ((hours * 3600 + minutes * 60) * 1000 + seconds * 1000).toLong()
}

private fun isVttPreambleLine(line: String): Boolean =
    line.equals("WEBVTT", ignoreCase = true) ||
        line.startsWith("WEBVTT", ignoreCase = true) ||
        line.startsWith("NOTE", ignoreCase = true) ||
        line.startsWith("STYLE", ignoreCase = true) ||
        line.startsWith("REGION", ignoreCase = true)
