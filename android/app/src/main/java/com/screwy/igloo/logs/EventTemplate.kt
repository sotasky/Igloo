package com.screwy.igloo.logs

/**
 * Human-readable rendering unit for a log entry. [sentence] may contain placeholders:
 *  - `{name}`       → raw field value
 *  - `{name|kb}`    → numeric bytes reformatted (53 KB / 1.2 MB / 2.0 GB)
 *  - `{name|human}` → snake_case field value rendered as space-separated text
 *
 * [expandFields] lists the field keys shown in the expanded detail grid. Empty list
 * means "show every field" — used as the fallback when a row is tapped to expand.
 */
data class EventTemplate(
    val sentence: String,
    val expandFields: List<String> = emptyList(),
) {
    fun render(fields: Map<String, String>): String =
        PLACEHOLDER.replace(sentence) { match ->
            val (name, formatter) = parse(match.groupValues[1])
            val raw = fields[name] ?: return@replace "?"
            when (formatter) {
                null -> raw
                "kb" -> formatBytes(raw.toLongOrNull() ?: return@replace raw)
                "human" -> raw.replace('_', ' ')
                else -> raw
            }
        }

    private fun parse(expr: String): Pair<String, String?> {
        val pipe = expr.indexOf('|')
        return if (pipe < 0) expr to null else expr.substring(0, pipe) to expr.substring(pipe + 1)
    }

    companion object {
        private val PLACEHOLDER = Regex("""\{([^{}]+)\}""")

        private fun formatBytes(bytes: Long): String {
            if (bytes < 1024) return "$bytes B"
            val kb = bytes / 1024.0
            if (kb < 1024) return String.format("%.0f KB", kb)
            val mb = kb / 1024.0
            if (mb < 1024) return String.format("%.1f MB", mb)
            val gb = mb / 1024.0
            return String.format("%.2f GB", gb)
        }
    }
}
