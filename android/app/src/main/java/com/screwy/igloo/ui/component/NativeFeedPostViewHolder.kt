// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.

package com.screwy.igloo.ui.component

import android.content.Context
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.text.TextUtils
import android.text.method.LinkMovementMethod
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageButton
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.ui.unit.dp
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import androidx.recyclerview.widget.RecyclerView
import coil3.ImageLoader
import coil3.target.ImageViewTarget
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.FeedMediaCellModel
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.SocialPostModel
import com.screwy.igloo.feed.buildSocialPostModel
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal class NativeFeedViewHolder(
    context: Context,
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private val scope: CoroutineScope,
    private val getColors: () -> NativeFeedColors,
    private val getBaseUrl: () -> String,
    private val getCallbacks: () -> NativeFeedCallbacks,
    private val inlineVideoManager: NativeInlineVideoManager,
) : RecyclerView.ViewHolder(NativeFeedCardViews(context).root) {
    private val views: NativeFeedCardViews = itemView.tag as NativeFeedCardViews
    private val videoSlots = mutableListOf<NativeVideoSlot>()
    private val avatarJobs = mutableMapOf<ImageView, Job>()
    private var boundRow: NativeFeedAdapterItem.Post? = null
    private var showTranslatedBody = true
    private var showTranslatedQuote = true
    private var bodyExpanded = false

    fun bind(adapterRow: NativeFeedAdapterItem.Post) {
        val previousId = boundRow?.id
        if (previousId != adapterRow.id) {
            showTranslatedBody = true
            showTranslatedQuote = true
            bodyExpanded = false
        }
        boundRow = adapterRow
        videoSlots.forEach { inlineVideoManager.detachSlot(it.key) }
        videoSlots.clear()

        val colors = getColors()
        val callbacks = getCallbacks()
        val post = adapterRow.post
        val row = adapterRow.threaded.row
        val item = row.item
        val shareUrl = feedShareUrl(item).trim()
        val bodyTranslation = item.bodyTranslation?.takeIf { it.isNotBlank() }
        val quoteTranslation = item.quoteTranslation?.takeIf { it.isNotBlank() }
        val bodyText = if (showTranslatedBody && bodyTranslation != null) {
            bodyTranslation
        } else {
            item.bodyText.orEmpty()
        }.let { stripReplyPrefix(item, it) }
        val translationPill = nativeTranslationPillForText(
            lang = item.lang,
            sourceLang = item.bodySourceLang,
            text = item.bodyText,
            active = showTranslatedBody && bodyTranslation != null,
            enabled = bodyTranslation != null,
        )

        views.applyColors(colors)
        views.root.setOnClickListener { callbacks.onRowClick(row) }
        views.root.setOnLongClickListener {
            showMenu(row, post)
            true
        }

        bindThread(adapterRow.threaded, colors, callbacks)
        bindRetweeter(item, callbacks, colors)
        bindHeader(
            header = views.header,
            channelId = item.channelId.orEmpty(),
            explicitAvatarUrl = item.authorAvatarUrl,
            displayName = post.author.displayName,
            handle = post.author.handle,
            timestamp = localizedRelativeTime(views.root.context, item.publishedAt),
            showFollow = item.channelId?.isNotBlank() == true,
            isFollowed = row.channelIsFollowed == 1,
            colors = colors,
            translation = translationPill,
            onClick = {
                if (post.author.channelId.isNotBlank()) {
                    callbacks.onProfileOpen(post)
                } else if (item.authorHandle.isNotBlank()) {
                    callbacks.onMentionClick(item.authorHandle)
                }
            },
            onFollowClick = {
                val channelId = item.channelId?.takeIf { it.isNotBlank() } ?: return@bindHeader
                if (row.channelIsFollowed == 1) {
                    callbacks.onRequestUnfollowConfirmation(channelId)
                } else {
                    callbacks.onFollowToggle(channelId, true)
                }
            },
            onTranslationClick = {
                if (bodyTranslation != null) {
                    showTranslatedBody = !showTranslatedBody
                    bind(adapterRow)
                }
            },
        )

        bindReply(item, callbacks, colors, visible = adapterRow.threaded.chain.isEmpty())
        bindBody(
            textView = views.body,
            moreView = views.showMore,
            text = bodyText,
            colors = colors,
            callbacks = callbacks,
        )
        bindMediaGrid(
            container = views.media,
            ownerKeyPrefix = item.tweetId,
            row = row,
            grid = post.media.grid,
            mediaIndexOffset = 0,
            colors = colors,
            callbacks = callbacks,
        )
        bindQuote(row, post, quoteTranslation, colors, callbacks)
        bindActions(row, post, shareUrl, colors, callbacks)
        bindThreadCapsule(adapterRow.threaded, colors, callbacks)
    }

    fun recycle() {
        videoSlots.forEach { inlineVideoManager.detachSlot(it.key) }
        videoSlots.clear()
        cancelAvatarJobs()
        views.media.removeAllViews()
        views.quoteMedia.removeAllViews()
        boundRow = null
    }

    fun videoSlotsForSelection(): List<NativeVideoSlot> = videoSlots

    private fun bindThread(
        threaded: ThreadedFeedRow,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        cancelAvatarJobsUnder(views.thread)
        views.thread.removeAllViews()
        val chain = threaded.chain
        if (chain.isEmpty()) {
            views.thread.visibility = View.GONE
            return
        }

        views.thread.visibility = View.VISIBLE
        val visibleChain = nativeThreadPreviewAncestors(chain)
        visibleChain.forEachIndexed { index, row ->
            val params = verticalSpacingLayoutParams().apply {
                if (index == 0) topMargin = 0
            }
            views.thread.addView(threadAncestorView(row, colors, callbacks), params)
        }
    }

    private fun bindThreadCapsule(
        threaded: ThreadedFeedRow,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val chain = threaded.chain
        if (chain.isEmpty()) {
            views.threadCapsule.visibility = View.GONE
            views.threadCapsule.setOnClickListener(null)
            views.threadCapsuleText.text = ""
            cancelAvatarJobsUnder(views.threadCapsuleAvatars)
            views.threadCapsuleAvatars.removeAllViews()
            return
        }
        val context = views.root.context
        val postCount = threadCapsulePostCount(threaded)
        val peopleCount = threadCapsulePeopleCount(threaded)
        views.threadCapsule.visibility = View.VISIBLE
        views.threadCapsuleText.text = context.getString(R.string.feed_thread_capsule, postCount, peopleCount) +
            " - " + context.getString(R.string.feed_thread_open_inline)
        views.threadCapsule.contentDescription = context.getString(
            R.string.feed_thread_open_capsule_a11y,
            postCount,
            peopleCount,
        )
        views.threadCapsuleText.setTextColor(colors.onSurfaceMuted)
        views.threadCapsule.background = roundedStroke(Color.TRANSPARENT, colors.borderSubtle, dp(1), dp(14))
        views.threadCapsule.setOnClickListener { callbacks.onRowClick(threaded.row) }
        bindThreadCapsuleAvatars(threaded, colors)
    }

    private fun threadAncestorView(
        row: FeedRow,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ): LinearLayout {
        val item = row.item
        val authorHandle = normalizeHandle(item.authorHandle)
        val authorDisplay = displayLabel(
            primary = item.authorDisplayName,
            fallback = row.channelName,
            handle = authorHandle,
        )
        val container = LinearLayout(views.root.context).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(0, dp(5), 0, dp(5))
            setOnClickListener { callbacks.onRowClick(row) }
        }
        val header = NativeIdentityHeaderViews(views.root.context)
        bindHeader(
            header = header,
            channelId = item.channelId.orEmpty(),
            explicitAvatarUrl = item.authorAvatarUrl,
            displayName = authorDisplay,
            handle = authorHandle,
            timestamp = localizedRelativeTime(views.root.context, item.publishedAt),
            showFollow = false,
            isFollowed = false,
            colors = colors,
            translation = null,
            onClick = {
                if (authorHandle.isNotBlank()) {
                    callbacks.onMentionClick(authorHandle)
                }
            },
            onFollowClick = {},
        )
        container.addView(header.root)

        val body = stripReplyPrefix(item, item.bodyText.orEmpty())
        if (body.isNotBlank()) {
            container.addView(
                bodyText(views.root.context).apply {
                    setTextColor(colors.onSurface)
                    bindMentionText(this, body, colors, callbacks)
                    maxLines = NativeFeedBodyCollapsedLines
                    ellipsize = TextUtils.TruncateAt.END
                },
            )
        }
        container.addView(threadAncestorActions(row, colors, callbacks))
        return container
    }

    private fun bindRetweeter(
        item: FeedItemEntity,
        callbacks: NativeFeedCallbacks,
        colors: NativeFeedColors,
    ) {
        if (!item.isRetweet) {
            views.retweeter.visibility = View.GONE
            views.retweeter.setCompoundDrawablesWithIntrinsicBounds(null, null, null, null)
            return
        }
        val context = views.root.context
        val handle = item.retweetedByHandle ?: item.sourceHandle ?: ""
        val label = item.retweetedByDisplayName?.takeIf { it.isNotBlank() }
            ?: normalizeHandle(handle).takeIf { it.isNotBlank() }
        val icon = context.getDrawable(R.drawable.ic_feed_repost_24)?.mutate()?.apply {
            setTint(colors.onSurfaceMuted)
            setBounds(0, 0, dp(16), dp(16))
        }
        views.retweeter.visibility = View.VISIBLE
        views.retweeter.text = if (label.isNullOrBlank()) {
            context.getString(R.string.feed_reposted_someone)
        } else {
            context.getString(R.string.feed_reposted_single, label)
        }
        views.retweeter.setCompoundDrawables(icon, null, null, null)
        views.retweeter.compoundDrawablePadding = dp(4)
        views.retweeter.setTextColor(colors.onSurfaceMuted)
        views.retweeter.setOnClickListener {
            handle.takeIf { it.isNotBlank() }?.let(callbacks.onMentionClick)
        }
    }

    private fun bindReply(
        item: FeedItemEntity,
        callbacks: NativeFeedCallbacks,
        colors: NativeFeedColors,
        visible: Boolean = true,
    ) {
        val replyHandle = normalizeHandle(item.replyToHandle)
        if (!visible || replyHandle.isBlank()) {
            views.reply.visibility = View.GONE
            views.reply.setOnClickListener(null)
            return
        }
        views.reply.visibility = View.VISIBLE
        views.reply.text = views.root.context.getString(R.string.feed_replying_to, replyHandle)
        views.reply.setTextColor(colors.primary)
        views.reply.setOnClickListener { boundRow?.threaded?.row?.let(callbacks.onRowClick) }
    }

    private fun bindBody(
        textView: TextView,
        moreView: TextView,
        text: String,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        if (text.isBlank()) {
            textView.visibility = View.GONE
            moreView.visibility = View.GONE
            return
        }
        textView.visibility = View.VISIBLE
        bindMentionText(textView, text, colors, callbacks)
        val shouldClamp = nativeShouldClampBody(text)
        textView.maxLines = if (bodyExpanded || !shouldClamp) Int.MAX_VALUE else NativeFeedBodyCollapsedLines
        textView.ellipsize = if (bodyExpanded || !shouldClamp) null else TextUtils.TruncateAt.END
        moreView.visibility = if (shouldClamp) View.VISIBLE else View.GONE
        moreView.text = textView.context.getString(
            if (bodyExpanded) R.string.action_show_less else R.string.action_read_more,
        )
        moreView.setTextColor(colors.primary)
        moreView.setOnClickListener {
            bodyExpanded = !bodyExpanded
            textView.maxLines = if (bodyExpanded) Int.MAX_VALUE else NativeFeedBodyCollapsedLines
            textView.ellipsize = if (bodyExpanded) null else TextUtils.TruncateAt.END
            moreView.text = textView.context.getString(
                if (bodyExpanded) R.string.action_show_less else R.string.action_read_more,
            )
        }
    }

    private fun bindQuote(
        row: FeedRow,
        post: SocialPostModel,
        quoteTranslation: String?,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val item = row.item
        val quoteId = item.quoteTweetId?.trim().orEmpty()
        if (quoteId.isBlank()) {
            views.quote.visibility = View.GONE
            views.quote.setOnClickListener(null)
            views.quoteBody.setOnClickListener(null)
            return
        }
        views.quote.visibility = View.VISIBLE
        views.quote.background = roundedStroke(colors.surfaceElevated, colors.borderSubtle, dp(1), dp(8))
        views.quote.setOnClickListener { callbacks.onQuoteOpen(quoteId) }
        val quoteHandle = normalizeHandle(item.quoteAuthorHandle)
            .ifBlank { displayNameLooksLikeHandle(item.quoteAuthorDisplayName) }
        val quoteDisplay = displayLabel(
            primary = item.quoteAuthorDisplayName,
            fallback = null,
            handle = quoteHandle,
        )
        val quoteChannelId = row.quoteChannelId?.takeIf { it.isNotBlank() } ?: "twitter_${quoteHandle.lowercase()}"
        val quoteTimestamp = item.quotePublishedAt
            .takeIf { it > 0L }
            ?.let { localizedRelativeTime(views.root.context, it) }
            .orEmpty()
        val followTarget = feedQuoteFollowTarget(row)
        val quoteTranslationPill = nativeTranslationPillForText(
            lang = item.quoteLang,
            sourceLang = item.quoteSourceLang,
            text = item.quoteBodyText,
            active = showTranslatedQuote && quoteTranslation != null,
            enabled = quoteTranslation != null,
        )

        bindHeader(
            header = views.quoteHeader,
            channelId = quoteChannelId,
            explicitAvatarUrl = item.quoteAuthorAvatarUrl,
            displayName = quoteDisplay,
            handle = quoteHandle,
            timestamp = quoteTimestamp,
            showFollow = followTarget != null,
            isFollowed = false,
            colors = colors,
            translation = quoteTranslationPill,
            onClick = { if (quoteHandle.isNotBlank()) callbacks.onMentionClick(quoteHandle) },
            onFollowClick = { followTarget?.let { callbacks.onFollowToggle(it.channelId, true) } },
            onTranslationClick = {
                if (quoteTranslation != null) {
                    showTranslatedQuote = !showTranslatedQuote
                    boundRow?.let { bind(it) }
                }
            },
        )

        val quoteBody = if (showTranslatedQuote && quoteTranslation != null) {
            quoteTranslation
        } else {
            item.quoteBodyText.orEmpty()
        }
        if (quoteBody.isBlank()) {
            views.quoteBody.visibility = View.GONE
            views.quoteBody.setOnClickListener(null)
        } else {
            views.quoteBody.visibility = View.VISIBLE
            views.quoteBody.setTextColor(colors.onSurface)
            views.quoteBody.movementMethod = null
            views.quoteBody.text = quoteBody
            views.quoteBody.maxLines = NativeFeedQuoteCollapsedLines
            views.quoteBody.ellipsize = TextUtils.TruncateAt.END
            views.quoteBody.setOnClickListener { callbacks.onQuoteOpen(quoteId) }
        }
        val parentCount = post.media.grid.mediaCount
        val quoteMedia = post.quoteMedia?.grid
        if (quoteMedia == null || quoteMedia.mediaCount == 0) {
            views.quoteMedia.visibility = View.GONE
            views.quoteMedia.removeAllViews()
        } else {
            views.quoteMedia.visibility = View.VISIBLE
            bindMediaGrid(
                container = views.quoteMedia,
                ownerKeyPrefix = item.quoteTweetId.orEmpty(),
                row = row,
                grid = quoteMedia,
                mediaIndexOffset = parentCount,
                colors = colors,
                callbacks = callbacks,
            )
        }
    }

    private fun bindActions(
        row: FeedRow,
        post: SocialPostModel,
        shareUrl: String,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        bindActionButtons(views.actions, row, post, shareUrl, colors, callbacks)
        configureMenuButton(views.menu, colors)
        views.menu.setOnClickListener { showMenu(views.menu, row, post, shareUrl) }
    }

    private fun showMenu(row: FeedRow, post: SocialPostModel, shareUrl: String = feedShareUrl(row.item).trim()) {
        showMenu(views.menu, row, post, shareUrl)
    }

    private fun showMenu(anchor: View, row: FeedRow, post: SocialPostModel, shareUrl: String = feedShareUrl(row.item).trim()) {
        val callbacks = getCallbacks()
        val context = views.root.context
        val items = mutableListOf<NativeFeedMenuItem>()
        val channelId = row.item.channelId?.trim().orEmpty()
        if (shareUrl.isNotBlank()) {
            items += NativeFeedMenuItem(
                label = context.getString(R.string.action_open_on_x),
                action = {
                    openExternalUrl(context, shareUrl)
                },
            )
        }
        if (channelId.isNotBlank()) {
            items += NativeFeedMenuItem(
                label = context.getString(
                    if (row.channelIsStarred == 1) R.string.action_unstar_channel else R.string.action_star_channel,
                ),
                action = {
                    callbacks.onStarToggle(channelId, row.channelIsStarred == 0)
                },
            )
        }
        feedMuteMenuActions(row, callbacks.mutedHandles).forEach { action ->
            items += NativeFeedMenuItem(
                label = context.getString(
                    if (action.isMuted) R.string.action_unmute_account_handle else R.string.action_mute_account_handle,
                    action.handle,
                ),
                action = {
                    if (action.isMuted) {
                        callbacks.onMuteToggle(action.handle, false)
                    } else {
                        callbacks.onRequestMuteConfirmation(action)
                    }
                },
            )
        }
        if (items.isEmpty()) {
            items += NativeFeedMenuItem(
                label = context.getString(R.string.feed_open_thread),
                action = {
                    callbacks.onRowClick(post.row)
                },
            )
        }
        showNativeFeedPopup(anchor, getColors(), items)
    }

    private fun threadAncestorActions(
        row: FeedRow,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ): LinearLayout {
        val context = views.root.context
        val post = buildSocialPostModel(row, emptyMap())
        val shareUrl = feedShareUrl(row.item).trim()
        val actions = LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        val menu = ImageButton(context)
        configureMenuButton(menu, colors)
        menu.setOnClickListener { showMenu(menu, row, post, shareUrl) }
        return LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(menu, LinearLayout.LayoutParams(dp(48), dp(36)))
            addView(View(context), LinearLayout.LayoutParams(0, dp(40), 1f))
            bindActionButtons(actions, row, post, shareUrl, colors, callbacks)
            addView(actions, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(40)))
        }
    }

    private fun bindActionButtons(
        actions: LinearLayout,
        row: FeedRow,
        post: SocialPostModel,
        shareUrl: String,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val canOpenExternal = shareUrl.isNotBlank()
        actions.removeAllViews()
        NativeFeedPrimaryActions.forEach { action ->
            val button = actionIconButton(views.root.context, colors)
            button.contentDescription = action.contentDescription(views.root.context, post)
            button.isEnabled = when (action) {
                NativeFeedPrimaryAction.Share -> canOpenExternal
                NativeFeedPrimaryAction.Like,
                NativeFeedPrimaryAction.Bookmark -> true
            }
            val selected = when (action) {
                NativeFeedPrimaryAction.Like -> post.actions.isLiked
                NativeFeedPrimaryAction.Bookmark -> post.actions.isBookmarked
                else -> false
            }
            button.setImageResource(action.iconRes(selected))
            button.setColorFilter(
                when {
                    !button.isEnabled -> colors.onSurfaceFaint
                    selected -> colors.primary
                    else -> colors.onSurfaceMuted
                }
            )
            button.setOnClickListener {
                when (action) {
                    NativeFeedPrimaryAction.Share -> sharePlainText(
                        views.root.context,
                        shareUrl,
                        callbacks.useEmbedFriendlyShareLinks,
                    )
                    NativeFeedPrimaryAction.Like -> callbacks.onLikeToggle(row.item.tweetId, row.isLiked == 0)
                    NativeFeedPrimaryAction.Bookmark -> callbacks.onBookmarkToggle(row)
                }
            }
            actions.addView(button)
        }
    }

    private fun configureMenuButton(menu: ImageButton, colors: NativeFeedColors) {
        menu.background = null
        menu.scaleType = ImageView.ScaleType.CENTER
        menu.setPadding(dp(10), dp(6), dp(10), dp(6))
        menu.setImageResource(R.drawable.ic_feed_more_vert_24)
        menu.setColorFilter(colors.onSurfaceMuted)
        menu.contentDescription = views.root.context.getString(R.string.action_more)
    }

    private fun threadCapsulePostCount(threaded: ThreadedFeedRow): Int =
        threaded.chain.size + 1

    private fun threadCapsulePeopleCount(threaded: ThreadedFeedRow): Int {
        val handles = linkedSetOf<String>()
        (threaded.chain + threaded.row).forEach { row ->
            normalizeHandle(row.item.authorHandle)
                .takeIf { it.isNotBlank() }
                ?.let(handles::add)
        }
        return handles.size
    }

    private fun bindThreadCapsuleAvatars(threaded: ThreadedFeedRow, colors: NativeFeedColors) {
        cancelAvatarJobsUnder(views.threadCapsuleAvatars)
        views.threadCapsuleAvatars.removeAllViews()

        val participants = threadCapsuleParticipantRows(threaded).take(3)
        views.threadCapsuleAvatars.visibility = if (participants.isEmpty()) View.GONE else View.VISIBLE
        participants.forEachIndexed { index, row ->
            val avatar = ImageView(views.root.context).apply {
                scaleType = ImageView.ScaleType.CENTER_CROP
                background = roundedFill(colors.surfaceVariant, dp(999))
                clipToOutline = true
            }
            loadAvatar(
                imageView = avatar,
                channelId = row.item.channelId.orEmpty(),
                explicitAvatarUrl = row.item.authorAvatarUrl,
            )
            views.threadCapsuleAvatars.addView(
                avatar,
                LinearLayout.LayoutParams(dp(20), dp(20)).apply {
                    if (index > 0) marginStart = -dp(6)
                },
            )
        }
    }

    private fun threadCapsuleParticipantRows(threaded: ThreadedFeedRow): List<FeedRow> {
        val seenHandles = linkedSetOf<String>()
        return (threaded.chain + threaded.row).filter { row ->
            val handle = normalizeHandle(row.item.authorHandle)
            handle.isNotBlank() && seenHandles.add(handle)
        }
    }

    private fun bindHeader(
        header: NativeIdentityHeaderViews,
        channelId: String,
        explicitAvatarUrl: String?,
        displayName: String,
        handle: String,
        timestamp: String,
        showFollow: Boolean,
        isFollowed: Boolean,
        colors: NativeFeedColors,
        translation: NativeTranslationPill? = null,
        onClick: () -> Unit,
        onFollowClick: () -> Unit,
        onTranslationClick: () -> Unit = {},
    ) {
        header.root.setOnClickListener { onClick() }
        header.avatar.setOnClickListener { onClick() }
        loadAvatar(header.avatar, channelId, explicitAvatarUrl)
        header.name.text = displayName.ifBlank { handle }
        header.name.setTextColor(colors.onSurface)
        val normalizedHandle = normalizeHandle(handle)
        header.meta.text = when {
            normalizedHandle.isNotBlank() && timestamp.isNotBlank() -> "@$normalizedHandle · $timestamp"
            normalizedHandle.isNotBlank() -> "@$normalizedHandle"
            else -> timestamp
        }
        header.meta.setTextColor(colors.onSurfaceHandle)
        bindTranslationPill(header, translation, colors, onTranslationClick)
        header.follow.visibility = if (showFollow) View.VISIBLE else View.GONE
        header.follow.text = views.root.context.getString(
            if (isFollowed) R.string.action_following else R.string.action_follow,
        )
        header.follow.setTextColor(if (isFollowed) colors.onSurface else colors.onPrimary)
        header.follow.background = roundedFill(if (isFollowed) colors.surfaceHighest else colors.primary, dp(999))
        header.follow.setOnClickListener { onFollowClick() }
    }

    private fun bindTranslationPill(
        header: NativeIdentityHeaderViews,
        translation: NativeTranslationPill?,
        colors: NativeFeedColors,
        onTranslationClick: () -> Unit,
    ) {
        if (translation == null) {
            header.translate.visibility = View.GONE
            header.translate.setOnClickListener(null)
            return
        }
        header.translate.visibility = View.VISIBLE
        header.translate.isEnabled = translation.enabled
        header.translate.alpha = if (translation.enabled) 1f else 0.65f
        header.translate.contentDescription = header.root.context.getString(R.string.settings_auto_translate)
        header.translateIcon.setColorFilter(if (translation.active) colors.primary else colors.onSurfaceMuted)
        header.translateLabel.text = if (translation.active) translation.sourceLangLabel else ""
        header.translateLabel.visibility = if (translation.active && translation.sourceLangLabel.isNotBlank()) {
            View.VISIBLE
        } else {
            View.GONE
        }
        header.translateLabel.setTextColor(colors.primary)
        header.translate.setOnClickListener {
            if (translation.enabled) onTranslationClick()
        }
    }

    private fun bindMentionText(
        textView: TextView,
        text: String,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        textView.setTextColor(colors.onSurface)
        textView.setLinkTextColor(colors.primary)
        textView.highlightColor = Color.TRANSPARENT
        textView.movementMethod = LinkMovementMethod.getInstance()
        textView.text = clickableText(
            raw = text,
            linkColor = colors.primary,
            onMentionClick = callbacks.onMentionClick,
            onUrlClick = { url -> openExternalUrl(textView.context, url) },
        )
    }

    private fun bindMediaGrid(
        container: LinearLayout,
        ownerKeyPrefix: String,
        row: FeedRow,
        grid: FeedMediaGridModel,
        mediaIndexOffset: Int,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        container.removeAllViews()
        if (grid.mediaCount == 0) {
            container.visibility = View.GONE
            return
        }
        container.visibility = View.VISIBLE
        if (grid.mediaCount == 1) {
            val cell = grid.cells.first()
            val aspect = nativeStableSingleMediaAspectRatio(cell)
            val dimensions = nativeSingleMediaDimensions(container.context, aspect)
            val frame = FrameLayout(container.context).apply {
                setBackgroundColor(Color.TRANSPARENT)
                clipToOutline = true
                background = roundedFill(Color.TRANSPARENT, dp(8))
            }
            frame.layoutParams = LinearLayout.LayoutParams(
                dimensions.widthPx,
                dimensions.heightPx,
            ).apply { gravity = Gravity.START }
            bindMediaCell(
                parent = frame,
                ownerKey = "$ownerKeyPrefix:0",
                row = row,
                grid = grid,
                cell = cell,
                mediaIndex = mediaIndexOffset,
                isSingle = true,
                colors = colors,
                callbacks = callbacks,
            )
            container.addView(frame)
        } else {
            val context = container.context
            val gridWidth = nativeMediaGridWidthPx(context)
            val gap = dp(2)
            val displayCells = grid.cells.take(4)
            fun frameFor(index: Int, cell: FeedMediaCellModel): FrameLayout {
                val frame = FrameLayout(container.context).apply {
                    setBackgroundColor(colors.surface)
                    clipToOutline = true
                    background = roundedFill(colors.surface, dp(8))
                }
                bindMediaCell(
                    parent = frame,
                    ownerKey = "$ownerKeyPrefix:$index",
                    row = row,
                    grid = grid,
                    cell = cell,
                    mediaIndex = mediaIndexOffset + index,
                    colors = colors,
                    callbacks = callbacks,
                )
                if (index == 3 && grid.mediaCount > 4) {
                    frame.addView(
                        TextView(context).apply {
                            text = "+${grid.mediaCount - 4}"
                            textSize = 24f
                            setTypeface(typeface, Typeface.BOLD)
                            setTextColor(Color.WHITE)
                            gravity = Gravity.CENTER
                            background = GradientDrawable().apply {
                                setColor(Color.argb(145, 0, 0, 0))
                            }
                        },
                        FrameLayout.LayoutParams(
                            ViewGroup.LayoutParams.MATCH_PARENT,
                            ViewGroup.LayoutParams.MATCH_PARENT,
                        ),
                    )
                }
                return frame
            }

            when (displayCells.size) {
                2 -> {
                    val cellSize = (gridWidth - gap) / 2
                    val rowLayout = LinearLayout(context).apply {
                        orientation = LinearLayout.HORIZONTAL
                    }
                    rowLayout.layoutParams = LinearLayout.LayoutParams(gridWidth, cellSize)
                    displayCells.forEachIndexed { index, cell ->
                        rowLayout.addView(
                            frameFor(index, cell),
                            LinearLayout.LayoutParams(cellSize, ViewGroup.LayoutParams.MATCH_PARENT).apply {
                                if (index > 0) marginStart = gap
                            },
                        )
                    }
                    container.addView(rowLayout)
                }
                3 -> {
                    val gridHeight = (gridWidth / 1.6f).toInt()
                    val columnWidth = (gridWidth - gap) / 2
                    val rightCellHeight = (gridHeight - gap) / 2
                    val rowLayout = LinearLayout(context).apply {
                        orientation = LinearLayout.HORIZONTAL
                    }
                    rowLayout.layoutParams = LinearLayout.LayoutParams(gridWidth, gridHeight)
                    rowLayout.addView(
                        frameFor(0, displayCells[0]),
                        LinearLayout.LayoutParams(columnWidth, ViewGroup.LayoutParams.MATCH_PARENT),
                    )
                    val rightColumn = LinearLayout(context).apply {
                        orientation = LinearLayout.VERTICAL
                    }
                    rowLayout.addView(
                        rightColumn,
                        LinearLayout.LayoutParams(columnWidth, ViewGroup.LayoutParams.MATCH_PARENT).apply {
                            marginStart = gap
                        },
                    )
                    rightColumn.addView(
                        frameFor(1, displayCells[1]),
                        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, rightCellHeight),
                    )
                    rightColumn.addView(
                        frameFor(2, displayCells[2]),
                        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, rightCellHeight).apply {
                            topMargin = gap
                        },
                    )
                    container.addView(rowLayout)
                }
                else -> {
                    val cellSize = (gridWidth - gap) / 2
                    val gridLayout = LinearLayout(context).apply {
                        orientation = LinearLayout.VERTICAL
                    }
                    gridLayout.layoutParams = LinearLayout.LayoutParams(gridWidth, gridWidth)
                    repeat(2) { rowIndex ->
                        val rowLayout = LinearLayout(context).apply {
                            orientation = LinearLayout.HORIZONTAL
                        }
                        gridLayout.addView(
                            rowLayout,
                            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, cellSize).apply {
                                if (rowIndex > 0) topMargin = gap
                            },
                        )
                        repeat(2) { columnIndex ->
                            val index = rowIndex * 2 + columnIndex
                            rowLayout.addView(
                                frameFor(index, displayCells[index]),
                                LinearLayout.LayoutParams(cellSize, ViewGroup.LayoutParams.MATCH_PARENT).apply {
                                    if (columnIndex > 0) marginStart = gap
                                },
                            )
                        }
                    }
                    container.addView(gridLayout)
                }
            }
        }
    }

    @androidx.annotation.OptIn(markerClass = [UnstableApi::class])
    private fun bindMediaCell(
        parent: FrameLayout,
        ownerKey: String,
        row: FeedRow,
        grid: FeedMediaGridModel,
        cell: FeedMediaCellModel,
        mediaIndex: Int,
        isSingle: Boolean = false,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val image = ImageView(parent.context).apply {
            scaleType = nativeMediaScaleTypeFor(cell.descriptor, isSingle)
            setBackgroundColor(if (isSingle) Color.TRANSPARENT else colors.surface)
        }
        parent.addView(
            image,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ),
        )
        loadMediaImage(
            image,
            cell.artworkUri(),
            parent.layoutParams?.width ?: 0,
            parent.layoutParams?.height ?: 0,
        )

        if (cell.descriptor.isVideo) {
            val playerView = PlayerView(parent.context).apply {
                useController = false
                resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                setShutterBackgroundColor(Color.TRANSPARENT)
                visibility = View.GONE
                alpha = 0f
            }
            parent.addView(
                playerView,
                FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT,
                ),
            )
            val streamUri = cell.streamUri()
            if (streamUri !is MediaUri.Missing) {
                val slot = NativeVideoSlot(
                    key = ownerKey,
                    streamUri = streamUri,
                    container = parent,
                    playerView = playerView,
                    poster = image,
                )
                videoSlots += slot
            }
        }

        parent.setOnClickListener {
            callbacks.onMediaOpen(row, mediaIndex, grid)
        }
    }

    private fun loadAvatar(imageView: ImageView, channelId: String, explicitAvatarUrl: String?) {
        val requestKey = "avatar:${channelId.trim()}:${explicitAvatarUrl.orEmpty().trim()}"
        imageView.tag = requestKey
        avatarJobs.remove(imageView)?.cancel()
        imageView.setImageDrawable(null)
        imageView.background = roundedFill(getColors().surfaceVariant, dp(999))

        val explicitUri = explicitAvatarUrl?.trim()
            ?.takeIf { it.isNotBlank() }
            ?.let(::avatarRemoteUri)
        if (channelId.isBlank()) {
            if (explicitUri != null) loadAvatarUri(imageView, explicitUri)
            return
        }

        avatarJobs[imageView] = scope.launch {
            val resolved = withContext(Dispatchers.IO) {
                mediaResolvers.avatarForChannel(channelId)
            }
            if (imageView.tag != requestKey) return@launch
            loadAvatarUri(
                imageView = imageView,
                uri = resolved.takeUnless { it is MediaUri.Missing } ?: explicitUri ?: MediaUri.Missing,
            )
        }
    }

    private fun loadAvatarUri(imageView: ImageView, uri: MediaUri) {
        val request = buildMediaImageRequest(
            context = imageView.context,
            uri = uri,
            bearerToken = authTokens.bearerTokenSync(),
            iglooHost = iglooHostProvider.hostSync(),
            widthPx = dp(48),
            heightPx = dp(48),
        )?.newBuilder(imageView.context)
            ?.target(ImageViewTarget(imageView))
            ?.build()
        if (request != null) {
            imageLoader.enqueue(request)
        }
    }

    private fun avatarRemoteUri(url: String): MediaUri.Remote {
        val resolved = when {
            url.startsWith("http://") || url.startsWith("https://") -> url
            url.startsWith("/") -> getBaseUrl().trim().trimEnd('/') + url
            else -> url
        }
        return MediaUri.Remote(resolved)
    }

    private fun cancelAvatarJobs() {
        avatarJobs.values.forEach { it.cancel() }
        avatarJobs.clear()
    }

    private fun cancelAvatarJobsUnder(view: View) {
        if (view is ImageView) {
            avatarJobs.remove(view)?.cancel()
        }
        if (view is ViewGroup) {
            for (index in 0 until view.childCount) {
                cancelAvatarJobsUnder(view.getChildAt(index))
            }
        }
    }

    private fun loadMediaImage(imageView: ImageView, uri: MediaUri, widthPx: Int, heightPx: Int) {
        val request = buildMediaImageRequest(
            context = imageView.context,
            uri = uri,
            bearerToken = authTokens.bearerTokenSync(),
            iglooHost = iglooHostProvider.hostSync(),
            widthPx = widthPx.takeIf { it > 0 } ?: imageView.resources.displayMetrics.widthPixels,
            heightPx = heightPx.takeIf { it > 0 } ?: imageView.resources.displayMetrics.widthPixels,
        )?.newBuilder(imageView.context)
            ?.target(ImageViewTarget(imageView))
            ?.build()
        if (request != null) {
            imageLoader.enqueue(request)
        } else {
            imageView.setImageDrawable(null)
            imageView.setBackgroundColor(getColors().surfaceVariant)
        }
    }
}
