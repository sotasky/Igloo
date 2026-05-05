package com.screwy.igloo.ui.component

import android.content.Context
import android.content.Intent
import android.net.Uri

internal fun sharePlainText(context: Context, text: String, useEmbedFriendlySite: Boolean = false) {
    val trimmed = toShareUrl(text, useEmbedFriendlySite)
    if (trimmed.isBlank()) return
    val sendIntent = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, trimmed)
    }
    val chooser = Intent.createChooser(sendIntent, null).apply {
        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    }
    context.startActivity(chooser)
}

internal fun openExternalUrl(context: Context, url: String) {
    val trimmed = externalUrlForIntent(url)
    if (trimmed.isBlank()) return
    val intent = Intent(Intent.ACTION_VIEW, Uri.parse(trimmed)).apply {
        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    }
    context.startActivity(intent)
}

internal fun externalUrlForIntent(url: String): String {
    val trimmed = url.trim()
    if (trimmed.isBlank()) return ""
    val schemeEnd = trimmed.indexOf(':')
    val hasScheme = schemeEnd > 0 &&
        trimmed.substring(0, schemeEnd).matches(ExternalUrlSchemeRegex) &&
        !trimmed.substring(0, schemeEnd).contains('.')
    return if (hasScheme) trimmed else "https://$trimmed"
}

internal fun toFxTwitterUrl(url: String, useEmbedFriendlySite: Boolean = false): String {
    return toShareUrl(url, useEmbedFriendlySite)
}

internal fun toShareUrl(url: String, useEmbedFriendlySite: Boolean = false): String {
    val trimmed = url.trim()
    if (trimmed.isBlank()) return trimmed
    if (!useEmbedFriendlySite) return trimmed
    return trimmed
        .replace("https://twitter.com/", "https://fxtwitter.com/")
        .replace("http://twitter.com/", "https://fxtwitter.com/")
        .replace("https://x.com/", "https://fxtwitter.com/")
        .replace("http://x.com/", "https://fxtwitter.com/")
        .replace("https://www.tiktok.com/", "https://tnktok.com/")
        .replace("http://www.tiktok.com/", "https://tnktok.com/")
        .replace("https://tiktok.com/", "https://tnktok.com/")
        .replace("http://tiktok.com/", "https://tnktok.com/")
}

private val ExternalUrlSchemeRegex = Regex("""[A-Za-z][A-Za-z0-9+.-]*""")
