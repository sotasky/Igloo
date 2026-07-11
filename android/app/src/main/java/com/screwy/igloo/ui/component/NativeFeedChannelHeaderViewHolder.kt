// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.

package com.screwy.igloo.ui.component

import android.content.Context
import android.graphics.Color
import android.text.method.LinkMovementMethod
import android.view.View
import android.widget.ImageView
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.ui.unit.dp
import androidx.recyclerview.widget.RecyclerView
import coil3.ImageLoader
import coil3.request.Disposable
import coil3.target.ImageViewTarget
import com.screwy.igloo.R
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch

internal class NativeFeedChannelHeaderViewHolder(
    context: Context,
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private val scope: CoroutineScope,
    private val getColors: () -> NativeFeedColors,
    private val getCallbacks: () -> NativeFeedCallbacks,
) : RecyclerView.ViewHolder(NativeFeedChannelHeaderViews(context).root) {
    private val views: NativeFeedChannelHeaderViews = itemView.tag as NativeFeedChannelHeaderViews
    private var avatarJob: Job? = null
    private var bannerJob: Job? = null
    private var avatarRequest: Disposable? = null
    private var bannerRequest: Disposable? = null

    fun bind(header: ChannelProfileHeaderUiModel) {
        val colors = getColors()
        val callbacks = getCallbacks()
        views.applyColors(colors)

        avatarJob?.cancel()
        avatarRequest?.dispose()
        avatarRequest = null
        views.bannerFrame.visibility = View.GONE
        views.banner.setImageDrawable(null)
        views.bannerAvatar.visibility = View.GONE
        views.inlineAvatar.visibility = View.VISIBLE
        views.inlineAvatar.setImageDrawable(null)
        loadBanner(header)

        views.name.text = header.displayName
        views.verified.visibility = if (header.isVerified) View.VISIBLE else View.GONE
        views.handle.text = header.handle.takeIf { it.isNotBlank() }?.let { "@$it" }.orEmpty()
        views.handle.visibility = if (views.handle.text.isNullOrBlank()) View.GONE else View.VISIBLE
        bindHeaderBio(header, colors, callbacks)
        val linkColor = colors.channelProfileHeaderLinkColor(header.linkColorRole)
        views.website.text = header.website
        views.website.visibility = if (header.website.isBlank()) View.GONE else View.VISIBLE
        views.website.setTextColor(linkColor)
        views.website.setOnClickListener {
            openExternalUrl(views.root.context, header.website)
        }
        views.stats.text = header.stats.joinToString("    ")
        views.stats.visibility = if (views.stats.text.isNullOrBlank()) View.GONE else View.VISIBLE

        views.follow.text = views.root.context.getString(
            if (header.isFollowed) R.string.action_following else R.string.action_follow
        )
        views.follow.setTextColor(if (header.isFollowed) colors.onSurface else colors.onPrimary)
        views.follow.background = if (header.isFollowed) {
            roundedStroke(colors.surface, colors.primary, dp(1), dp(999))
        } else {
            roundedFill(colors.primary, dp(999))
        }
        views.follow.setOnClickListener { callbacks.onHeaderFollowToggle(!header.isFollowed) }

        views.star.setImageResource(
            if (header.isStarred) R.drawable.ic_channel_star_24 else R.drawable.ic_channel_star_border_24,
        )
        views.star.setColorFilter(if (header.isStarred) colors.primary else colors.onSurfaceMuted)
        views.star.contentDescription = views.root.context.getString(
            if (header.isStarred) R.string.action_unstar_channel else R.string.action_star_channel,
        )
        views.star.setOnClickListener { callbacks.onHeaderStarToggle(!header.isStarred) }

        views.menu.setImageResource(R.drawable.ic_channel_more_horiz_24)
        views.menu.setColorFilter(colors.onSurfaceMuted)
        views.menu.contentDescription = views.root.context.getString(R.string.action_more)
        views.menu.setOnClickListener { showHeaderMenu(header) }
    }

    private fun bindHeaderBio(
        header: ChannelProfileHeaderUiModel,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val rawBio = when {
            header.isProtected -> header.protectedText
            else -> header.bio
        }
        views.bio.visibility = if (rawBio.isBlank()) View.GONE else View.VISIBLE
        if (rawBio.isBlank()) {
            views.bio.text = ""
            views.bio.movementMethod = null
            return
        }

        views.bio.setTextColor(if (header.isProtected) colors.onSurfaceMuted else colors.onSurface)
        if (header.isProtected) {
            views.bio.text = rawBio
            views.bio.movementMethod = null
            return
        }

        val linkColor = colors.channelProfileHeaderLinkColor(header.linkColorRole)
        views.bio.setLinkTextColor(linkColor)
        views.bio.highlightColor = Color.TRANSPARENT
        views.bio.movementMethod = LinkMovementMethod.getInstance()
        views.bio.text = clickableText(
            raw = rawBio,
            linkColor = linkColor,
            onMentionClick = callbacks.onMentionClick,
            onUrlClick = { url -> openExternalUrl(views.bio.context, url) },
        )
    }

    fun recycle() {
        avatarJob?.cancel()
        bannerJob?.cancel()
        avatarRequest?.dispose()
        bannerRequest?.dispose()
        avatarRequest = null
        bannerRequest = null
        views.banner.setImageDrawable(null)
        views.bannerAvatar.setImageDrawable(null)
        views.inlineAvatar.setImageDrawable(null)
    }

    private fun showHeaderMenu(header: ChannelProfileHeaderUiModel) {
        val callbacks = getCallbacks()
        val context = views.root.context
        val items = buildList {
            add(
                NativeFeedMenuItem(
                    label = context.getString(
                        if (header.isFollowed) R.string.action_unfollow_account else R.string.action_follow_account,
                    ),
                    action = { callbacks.onHeaderFollowToggle(!header.isFollowed) },
                ),
            )
            add(
                NativeFeedMenuItem(
                    label = context.getString(R.string.action_refresh_channel),
                    action = callbacks.onHeaderRefresh,
                ),
            )
            if (!header.platformUrl.isNullOrBlank()) {
                add(
                    NativeFeedMenuItem(
                        label = context.getString(R.string.action_open_in, header.openLabel),
                        action = callbacks.onHeaderOpenInPlatform,
                    ),
                )
            }
        }
        showNativeFeedPopup(
            anchor = views.menu,
            colors = getColors(),
            items = items,
        )
    }

    private fun loadAvatar(imageView: ImageView, header: ChannelProfileHeaderUiModel) {
		val requestKey = "channel-avatar:${header.channelId}"
        imageView.tag = requestKey
        avatarJob?.cancel()
        avatarRequest?.dispose()
        avatarRequest = null
        imageView.setImageDrawable(null)
        val colors = getColors()
        val isBannerAvatar = imageView == views.bannerAvatar
        imageView.setPadding(0, 0, 0, 0)
        imageView.elevation = if (isBannerAvatar) dp(6).toFloat() else 0f
        imageView.translationZ = if (isBannerAvatar) dp(6).toFloat() else 0f
        imageView.background = roundedFill(colors.surfaceVariant, dp(999))
        avatarJob = scope.launch {
            mediaResolvers.avatarForChannelFlow(header.channelId).collect { resolved ->
                if (imageView.tag != requestKey) return@collect
                avatarRequest?.dispose()
                imageView.setImageDrawable(null)
                avatarRequest = loadImage(
                    imageView = imageView,
					uri = resolved,
                    widthPx = imageView.layoutParams?.width ?: dp(92),
                    heightPx = imageView.layoutParams?.height ?: dp(92),
                )
            }
        }
    }

    private fun loadBanner(header: ChannelProfileHeaderUiModel) {
		val requestKey = "channel-banner:${header.channelId}"
        views.banner.tag = requestKey
        bannerJob?.cancel()
        bannerRequest?.dispose()
        bannerRequest = null
        views.banner.setImageDrawable(null)
        bannerJob = scope.launch {
            mediaResolvers.bannerForChannelFlow(header.channelId).collect { resolved ->
                if (views.banner.tag != requestKey) return@collect
                bannerRequest?.dispose()
                bannerRequest = null
                views.banner.setImageDrawable(null)
                if (resolved is MediaUri.Missing) {
                    views.bannerFrame.visibility = View.GONE
                    views.bannerAvatar.visibility = View.GONE
                    views.inlineAvatar.visibility = View.VISIBLE
                    loadAvatar(views.inlineAvatar, header)
                    return@collect
                }
				views.bannerFrame.visibility = View.VISIBLE
				views.bannerAvatar.visibility = View.VISIBLE
				views.inlineAvatar.visibility = View.GONE
				bannerRequest = loadImage(
					imageView = views.banner,
					uri = resolved,
                    widthPx = views.banner.resources.displayMetrics.widthPixels,
                    heightPx = dp(NativeChannelHeaderBannerHeightDp),
                )
				loadAvatar(views.bannerAvatar, header)
            }
        }
    }

    private fun loadImage(imageView: ImageView, uri: MediaUri, widthPx: Int, heightPx: Int): Disposable? {
        val request = buildMediaImageRequest(
            context = imageView.context,
            uri = uri,
            bearerToken = authTokens.bearerTokenSync(),
            iglooHost = iglooHostProvider.hostSync(),
            widthPx = widthPx,
            heightPx = heightPx,
        )?.newBuilder(imageView.context)
            ?.target(ImageViewTarget(imageView))
            ?.build()
        return request?.let(imageLoader::enqueue)
    }
}
