package com.screwy.igloo.player

import androidx.annotation.StringRes
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity

internal const val SponsorBlockModeAsk = "ask"
internal const val SponsorBlockModeSilent = "silent"

internal data class SponsorBlockUiSegment(
    val source: SponsorBlockSegmentEntity,
    val startMs: Long,
    val endMs: Long,
    val category: String,
    val mode: String,
) {
    val key: String = "$category:$startMs:$endMs"
}

internal fun buildSponsorBlockUiSegments(
    segments: List<SponsorBlockSegmentEntity>,
    modes: Map<String, String>,
): List<SponsorBlockUiSegment> = segments.mapNotNull { entity ->
    val mode = modes[entity.category.lowercase()] ?: return@mapNotNull null
    entity.toSponsorBlockUiSegment(mode)
}

private fun SponsorBlockSegmentEntity.toSponsorBlockUiSegment(mode: String): SponsorBlockUiSegment? {
    if (mode == "off") return null
    val startMs = (startTime * 1000.0).toLong().coerceAtLeast(0L)
    val endMs = (endTime * 1000.0).toLong().coerceAtLeast(startMs)
    if (endMs <= startMs) return null
    return SponsorBlockUiSegment(
        source = this,
        startMs = startMs,
        endMs = endMs,
        category = category.lowercase(),
        mode = mode,
    )
}

internal fun sponsorBlockModeMap(
    sponsor: String,
    selfPromo: String,
    interaction: String,
    intro: String,
    outro: String,
    preview: String,
    filler: String,
    music: String,
): Map<String, String> = mapOf(
    "sponsor" to sponsor,
    "selfpromo" to selfPromo,
    "interaction" to interaction,
    "intro" to intro,
    "outro" to outro,
    "preview" to preview,
    "filler" to filler,
    "music_offtopic" to music,
)

@StringRes
internal fun sponsorBlockLabelRes(category: String): Int = when (category.lowercase()) {
    "sponsor" -> R.string.sponsorblock_category_sponsor
    "selfpromo" -> R.string.sponsorblock_category_selfpromo
    "interaction" -> R.string.sponsorblock_category_interaction
    "intro" -> R.string.sponsorblock_category_intro
    "outro" -> R.string.sponsorblock_category_outro
    "preview" -> R.string.sponsorblock_category_preview
    "filler" -> R.string.sponsorblock_category_filler
    "music_offtopic" -> R.string.sponsorblock_category_music_offtopic
    else -> R.string.sponsorblock_segment_fallback
}
