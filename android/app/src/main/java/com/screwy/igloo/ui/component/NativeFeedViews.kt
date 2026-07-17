// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.

package com.screwy.igloo.ui.component

import android.content.Context
import android.graphics.Color
import android.graphics.Typeface
import android.text.TextUtils
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageButton
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.compose.foundation.background
import androidx.compose.ui.unit.dp
import androidx.recyclerview.widget.RecyclerView
import com.screwy.igloo.R

internal const val NativeFeedQuoteHorizontalPaddingDp = 8

internal class NativeFeedChannelHeaderViews(context: Context) {
    val root: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(0, 0, 0, 0)
        clipChildren = false
        clipToPadding = false
        layoutParams = RecyclerView.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT,
        )
        tag = this@NativeFeedChannelHeaderViews
    }
    val bannerFrame: FrameLayout = FrameLayout(context).apply {
        clipChildren = false
        clipToPadding = false
        layoutParams = LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            dp(NativeChannelHeaderBannerFrameHeightDp),
        )
    }
    val banner: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
    }
    val bannerAvatar: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
        background = roundedFill(Color.DKGRAY, dp(999))
        clipToOutline = true
    }
    val inlineAvatar: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
        background = roundedFill(Color.DKGRAY, dp(999))
        clipToOutline = true
    }
    private val content: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(
            dp(ChannelProfileHeaderDefaults.CardHorizontalMarginDp),
            0,
            dp(ChannelProfileHeaderDefaults.CardHorizontalMarginDp),
            dp(ChannelProfileHeaderDefaults.CardHorizontalMarginDp),
        )
    }
    private val actionRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    private val infoCard: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(
            dp(ChannelProfileHeaderDefaults.CardHorizontalPaddingDp),
            dp(ChannelProfileHeaderDefaults.CardVerticalPaddingDp),
            dp(ChannelProfileHeaderDefaults.CardHorizontalPaddingDp),
            dp(ChannelProfileHeaderDefaults.CardVerticalPaddingDp),
        )
    }
    val follow: TextView = TextView(context).apply {
        gravity = Gravity.CENTER
        textSize = 16f
        setIncludeFontPadding(false)
        maxLines = 1
        setPadding(dp(20), 0, dp(20), 0)
    }
    val star: ImageButton = ImageButton(context).apply {
        background = null
        scaleType = ImageView.ScaleType.CENTER
        setPadding(dp(8), dp(8), dp(8), dp(8))
    }
    val menu: ImageButton = ImageButton(context).apply {
        background = null
        scaleType = ImageView.ScaleType.CENTER
        setPadding(dp(8), dp(8), dp(8), dp(8))
    }
    private val nameRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    val name: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderNameTextSp
        typeface = Typeface.DEFAULT_BOLD
        setIncludeFontPadding(false)
        setLineSpacing(0f, 1.0f)
        maxLines = 2
        ellipsize = TextUtils.TruncateAt.END
    }
    val verified: TextView = TextView(context).apply {
        text = "✓"
        gravity = Gravity.CENTER
        textSize = 16f
        setIncludeFontPadding(false)
        setTextColor(Color.WHITE)
        background = roundedFill(0xFF8DEBFF.toInt(), dp(999))
    }
    val handle: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderMetaTextSp
        setIncludeFontPadding(false)
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val bio: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderBioTextSp
        setIncludeFontPadding(false)
        setLineSpacing(0f, 1.0f)
        maxLines = 6
        ellipsize = TextUtils.TruncateAt.END
    }
    val website: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderBioTextSp
        setIncludeFontPadding(false)
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val stats: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderMetaTextSp
        setIncludeFontPadding(false)
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    init {
        bannerFrame.addView(
            banner,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                dp(NativeChannelHeaderBannerHeightDp),
            ).apply {
                gravity = Gravity.TOP
            },
        )
        bannerFrame.addView(
            bannerAvatar,
            FrameLayout.LayoutParams(
                dp(NativeChannelHeaderAvatarSizeDp),
                dp(NativeChannelHeaderAvatarSizeDp),
            ).apply {
                gravity = Gravity.START or Gravity.BOTTOM
                leftMargin = dp(16)
                bottomMargin = -dp(NativeChannelHeaderAvatarOverlapDp)
            },
        )
        root.addView(bannerFrame)

        actionRow.addView(
            inlineAvatar,
            LinearLayout.LayoutParams(
                dp(NativeChannelHeaderInlineAvatarSizeDp),
                dp(NativeChannelHeaderInlineAvatarSizeDp),
            ),
        )
        actionRow.addView(View(context), LinearLayout.LayoutParams(0, dp(NativeChannelHeaderActionRowHeightDp), 1f))
        actionRow.addView(follow, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(NativeChannelHeaderFollowHeightDp)))
        actionRow.addView(star, LinearLayout.LayoutParams(dp(NativeChannelHeaderIconButtonSizeDp), dp(NativeChannelHeaderIconButtonSizeDp)))
        actionRow.addView(menu, LinearLayout.LayoutParams(dp(NativeChannelHeaderIconButtonSizeDp), dp(NativeChannelHeaderIconButtonSizeDp)))
        content.addView(actionRow)

        nameRow.addView(name, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            weight = 0f
        })
        nameRow.addView(verified, LinearLayout.LayoutParams(dp(30), dp(30)).apply {
            marginStart = dp(8)
        })
        infoCard.addView(nameRow)
        infoCard.addView(
            handle,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.NameHandleSpacingDp)
            },
        )
        infoCard.addView(
            bio,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.SectionSpacingDp)
            },
        )
        infoCard.addView(
            website,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.SectionSpacingDp)
            },
        )
        infoCard.addView(
            stats,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.SectionSpacingDp)
            },
        )
        content.addView(
            infoCard,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT),
        )
        root.addView(content)
    }

    fun applyColors(colors: NativeFeedColors) {
        root.setBackgroundColor(colors.surface)
        bannerFrame.setBackgroundColor(colors.surfaceHighest)
        infoCard.background = roundedFill(colors.surfaceElevated, dp(ChannelProfileHeaderDefaults.CardRadiusDp))
        name.setTextColor(colors.onSurface)
        handle.setTextColor(colors.onSurfaceHandle)
        bio.setTextColor(colors.onSurface)
        stats.setTextColor(colors.onSurfaceMuted)
    }
}

internal fun NativeFeedColors.channelProfileHeaderLinkColor(role: ChannelProfileHeaderLinkColorRole): Int =
    when (role) {
        ChannelProfileHeaderLinkColorRole.Primary -> primary
    }

internal class NativeFeedCardViews(context: Context) {
    val root: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(10), dp(8), dp(10), dp(8))
        layoutParams = RecyclerView.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT,
        ).apply {
            setMargins(dp(8), dp(2), dp(8), dp(2))
        }
        tag = this@NativeFeedCardViews
    }
    val thread: LinearLayout = LinearLayout(context).apply { orientation = LinearLayout.VERTICAL }
    val retweeter: TextView = smallText(context)
    val header: NativeIdentityHeaderViews = NativeIdentityHeaderViews(context)
    val reply: TextView = smallText(context)
    val body: TextView = bodyText(context)
    val showMore: TextView = smallText(context)
    val media: LinearLayout = LinearLayout(context).apply { orientation = LinearLayout.VERTICAL }
    val quote: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(
            dp(NativeFeedQuoteHorizontalPaddingDp),
            dp(8),
            dp(NativeFeedQuoteHorizontalPaddingDp),
            dp(8),
        )
    }
    val quoteHeader: NativeIdentityHeaderViews = NativeIdentityHeaderViews(context)
    val quoteBody: TextView = quoteText(context)
    val quoteMedia: LinearLayout = LinearLayout(context).apply { orientation = LinearLayout.VERTICAL }
    val actionContainer: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    val threadCapsule: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER
        setPadding(dp(10), dp(7), dp(10), dp(7))
    }
    val threadCapsuleAvatars: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        clipChildren = false
        clipToPadding = false
    }
    val threadCapsuleText: TextView = smallText(context).apply {
        gravity = Gravity.CENTER
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val menu: ImageButton = ImageButton(context).apply {
        background = null
        scaleType = ImageView.ScaleType.CENTER
        setPadding(dp(10), dp(6), dp(10), dp(6))
        setImageResource(R.drawable.ic_feed_more_vert_24)
        contentDescription = context.getString(R.string.action_more)
        layoutParams = LinearLayout.LayoutParams(dp(48), dp(36))
    }

    init {
        root.addView(thread)
        root.addView(retweeter)
        root.addView(header.root)
        root.addView(reply)
        root.addView(body)
        root.addView(showMore)
        root.addView(media, LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
        quote.addView(quoteHeader.root)
        quote.addView(quoteBody)
        quote.addView(quoteMedia, LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
        root.addView(quote, verticalSpacingLayoutParams())
        root.addView(
            actionContainer,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(40)),
        )
        threadCapsule.addView(
            threadCapsuleAvatars,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(22)).apply {
                marginEnd = dp(7)
            },
        )
        threadCapsule.addView(
            threadCapsuleText,
            LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f),
        )
        root.addView(threadCapsule, verticalSpacingLayoutParams())
    }

    fun applyColors(colors: NativeFeedColors) {
        root.background = roundedFill(colors.surfaceElevated, dp(8))
        listOf(retweeter, reply, showMore, threadCapsuleText).forEach { it.setTextColor(colors.onSurfaceMuted) }
        body.setTextColor(colors.onSurface)
        quoteBody.setTextColor(colors.onSurface)
    }
}

internal class NativeIdentityHeaderViews(context: Context) {
    val root: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        setPadding(0, dp(2), 0, dp(2))
    }
    val avatar: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
        background = roundedFill(Color.DKGRAY, dp(999))
        clipToOutline = true
    }
    private val textColumn: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER_VERTICAL
    }
    private val nameRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    private val metaRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    val name: TextView = TextView(context).apply {
        textSize = 17f
        typeface = Typeface.DEFAULT_BOLD
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val follow: TextView = TextView(context).apply {
        gravity = Gravity.CENTER
        textSize = 13f
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
        setPadding(dp(10), dp(4), dp(10), dp(4))
    }
    val meta: TextView = smallText(context).apply {
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val translate: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        visibility = View.GONE
        isClickable = true
        isFocusable = true
        setPadding(dp(4), 0, 0, 0)
    }
    val translateIcon: ImageView = ImageView(context).apply {
        setImageResource(R.drawable.ic_feed_translate_24)
    }
    val translateLabel: TextView = smallText(context).apply {
        textSize = 11f
        typeface = Typeface.DEFAULT_BOLD
        setPadding(dp(2), 0, 0, 0)
    }

    init {
        root.addView(avatar, LinearLayout.LayoutParams(dp(42), dp(42)))
        nameRow.addView(name, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        nameRow.addView(follow, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(30)))
        textColumn.addView(nameRow)
        metaRow.addView(meta, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        translate.addView(translateIcon, LinearLayout.LayoutParams(dp(15), dp(15)))
        translate.addView(translateLabel, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        metaRow.addView(translate, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        metaRow.addView(View(context), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        textColumn.addView(metaRow)
        root.addView(textColumn, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
            marginStart = dp(8)
        })
    }
}
