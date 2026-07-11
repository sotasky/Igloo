package com.screwy.igloo.data

/** Pure title resolver for DeArrow display mode. */
object Dearrow {

    /**
     * Resolve the display title for a video given the user's DeArrow mode.
     *
     * Fallback chain:
     *  - "casual"  → casual → community → original
     *  - "default" → community → original
     *  - anything else ("off") → original
     *
     * All nullable inputs are treated as absent (blank is also absent).
     */
    fun resolveTitle(
        mode: String,
        original: String?,
        community: String?,
        casual: String?,
    ): String {
        val orig = original ?: ""
        return when (mode) {
            "casual" -> casual?.takeIf { it.isNotBlank() }
                ?: community?.takeIf { it.isNotBlank() }
                ?: orig

            "default" -> community?.takeIf { it.isNotBlank() }
                ?: orig

            else -> orig
        }
    }

}
