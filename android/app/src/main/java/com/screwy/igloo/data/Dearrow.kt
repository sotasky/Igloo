package com.screwy.igloo.data

/**
 * Pure title + thumbnail resolver for DeArrow display mode.
 *
 * Mirrors the web Go resolver semantics exactly — same fallback chain,
 * same mode strings ("off" / "default" / "casual").
 *
 * Scope: thumbnails are served by the server via `?da=1` on the standard
 * thumbnail URL. No offline pre-materialization in this pass — the user
 * sees DeArrow thumbs when online with mode != off, original thumb when
 * offline or mode == off.
 */
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
        displayTitle: String? = null,
        displayTitleCasual: String? = null,
    ): String {
        val orig = original ?: ""
        return when (mode) {
            "casual" -> displayTitleCasual?.takeIf { it.isNotBlank() }
                ?: casual?.takeIf { it.isNotBlank() }
                ?: displayTitle?.takeIf { it.isNotBlank() }
                ?: community?.takeIf { it.isNotBlank() }
                ?: orig

            "default" -> displayTitle?.takeIf { it.isNotBlank() }
                ?: community?.takeIf { it.isNotBlank() }
                ?: orig

            else -> orig
        }
    }

    /**
     * Returns "?da=1" when the user has DeArrow on AND the video has a
     * dearrow_thumb_path (meaning the server cached a DeArrow frame). The
     * caller appends this to the server thumbnail URL; the server (Task 11)
     * serves the DeArrow variant for ?da=1, falling back to the original on
     * miss — safe to append unconditionally when the condition is met.
     *
     * Returns "" otherwise so the thumbnail URL is unchanged.
     */
    fun thumbnailUrlSuffix(mode: String, dearrowThumbPath: String?): String =
        if (mode != "off" && !dearrowThumbPath.isNullOrBlank()) "?da=1" else ""
}
