package com.screwy.igloo.ui.component

import android.content.Context
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.feed.canonicalTweetUrl
import java.util.concurrent.TimeUnit

internal data class FeedMuteMenuAction(
    val handle: String,
    val isMuted: Boolean,
)

internal data class FeedFollowTarget(
    val channelId: String,
    val isFollowed: Boolean,
)

internal fun feedQuoteFollowTarget(row: FeedRow): FeedFollowTarget? {
    val quoteHandle = normalizeHandle(row.item.quoteAuthorHandle).lowercase()
    if (quoteHandle.isBlank()) return null

    val parentHandle = normalizeHandle(row.item.authorHandle).lowercase()
    val parentChannelId = row.item.channelId?.trim().orEmpty()
    val quoteChannelId = row.quoteChannelId
        ?.trim()
        ?.takeIf { it.isNotBlank() }
        ?: "twitter_$quoteHandle"

    if (quoteChannelId.equals(parentChannelId, ignoreCase = true)) return null
    if (quoteHandle == parentHandle) return null

    return FeedFollowTarget(
        channelId = quoteChannelId,
        isFollowed = row.quoteChannelIsFollowed == 1,
    ).takeUnless { it.isFollowed }
}

internal fun feedMuteMenuActions(
    row: FeedRow,
    mutedHandles: Set<String>,
): List<FeedMuteMenuAction> {
    val actions = linkedMapOf<String, FeedMuteMenuAction>()
    val authorHandle = normalizeHandle(row.item.authorHandle).lowercase()
    val quoteHandle = normalizeHandle(row.item.quoteAuthorHandle).lowercase()

    if (row.item.isRetweet && authorHandle.isNotBlank()) {
        actions[authorHandle] = FeedMuteMenuAction(
            handle = authorHandle,
            isMuted = authorHandle in mutedHandles,
        )
    }
    if (quoteHandle.isNotBlank() && quoteHandle != authorHandle) {
        actions[quoteHandle] = FeedMuteMenuAction(
            handle = quoteHandle,
            isMuted = quoteHandle in mutedHandles,
        )
    }

    return actions.values.toList()
}

private data class RelativeTimeLabel(
    @param:StringRes val resId: Int,
    val count: Int? = null,
)

private fun relativeTimeLabel(
    publishedAtMs: Long,
    nowMs: Long = System.currentTimeMillis(),
    useWeeks: Boolean = true,
): RelativeTimeLabel {
    val deltaMs = (nowMs - publishedAtMs).coerceAtLeast(0)
    val seconds = TimeUnit.MILLISECONDS.toSeconds(deltaMs)
    val minutes = TimeUnit.MILLISECONDS.toMinutes(deltaMs)
    val hours = TimeUnit.MILLISECONDS.toHours(deltaMs)
    val days = TimeUnit.MILLISECONDS.toDays(deltaMs)
    return when {
        seconds < 60 -> RelativeTimeLabel(R.string.time_just_now)
        minutes < 60 -> RelativeTimeLabel(R.string.time_minutes_ago, minutes.toInt())
        hours < 24 -> RelativeTimeLabel(R.string.time_hours_ago, hours.toInt())
        days < 7 -> RelativeTimeLabel(R.string.time_days_ago, days.toInt())
        useWeeks && days < 30 -> RelativeTimeLabel(R.string.time_weeks_ago, (days / 7).toInt())
        days < 365 -> RelativeTimeLabel(R.string.time_months_ago, (days / 30).toInt())
        else -> RelativeTimeLabel(R.string.time_years_ago, (days / 365).toInt())
    }
}

internal fun localizedRelativeTime(
    context: Context,
    publishedAtMs: Long,
    nowMs: Long = System.currentTimeMillis(),
    useWeeks: Boolean = true,
): String {
    if (publishedAtMs <= 0L) return ""
    val label = relativeTimeLabel(publishedAtMs, nowMs, useWeeks)
    return if (label.count == null) {
        context.getString(label.resId)
    } else {
        context.getString(label.resId, label.count)
    }
}

@Composable
internal fun localizedRelativeTime(
    publishedAtMs: Long,
    nowMs: Long = System.currentTimeMillis(),
    useWeeks: Boolean = true,
): String {
    if (publishedAtMs <= 0L) return ""
    val label = relativeTimeLabel(publishedAtMs, nowMs, useWeeks)
    return if (label.count == null) {
        stringResource(label.resId)
    } else {
        stringResource(label.resId, label.count)
    }
}

internal fun normalizeHandle(raw: String?): String =
    raw?.trim()?.removePrefix("@")?.trim()?.takeIf { it.isNotBlank() } ?: ""

internal fun platformHandleCandidate(platform: String?, raw: String?): String {
    val handle = normalizeHandle(raw)
    return if (platform.equals("tiktok", ignoreCase = true)) {
        tikTokHandleUnlessInternalId(handle)
    } else {
        handle
    }
}

internal fun tikTokHandleUnlessInternalId(raw: String?): String {
    val handle = normalizeHandle(raw)
    return handle.takeUnless { isTikTokInternalId(it) }.orEmpty()
}

private fun isTikTokInternalId(raw: String): Boolean {
    val value = raw.trim().removePrefix("@").lowercase()
    return value.isLongNumericId() || value.startsWith("ms4wljab")
}

private fun String.isLongNumericId(): Boolean =
    length >= 16 && all { it in '0'..'9' }

internal fun displayLabel(
    primary: String?,
    handle: String,
    fallback: String? = null,
): String {
    val normalizedHandle = normalizeHandle(handle)
    val normalizedPrimary = normalizeHandle(primary)
    val normalizedFallback = normalizeHandle(fallback)
    val label = when {
        normalizedPrimary.isNotBlank() &&
            normalizedPrimary == normalizedHandle &&
            normalizedFallback.isNotBlank() &&
            normalizedFallback != normalizedHandle -> normalizedFallback
        normalizedPrimary.isNotBlank() -> normalizedPrimary
        normalizedFallback.isNotBlank() -> normalizedFallback
        else -> ""
    }
    return label.ifBlank { handle }
}

@Suppress("UNUSED_PARAMETER")
internal fun shouldShowHandle(displayLabel: String, handle: String): Boolean =
    handle.isNotBlank()

internal fun displayNameLooksLikeHandle(raw: String?): String {
    val value = normalizeHandle(raw)
    return value
        .takeIf { it.length in 1..30 }
        ?.takeIf { candidate -> candidate.all { it.isLetterOrDigit() || it == '_' } }
        .orEmpty()
}

internal fun feedShareUrl(item: FeedItemEntity): String =
    canonicalTweetUrl(item)
