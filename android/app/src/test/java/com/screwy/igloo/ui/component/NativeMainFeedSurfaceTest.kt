package com.screwy.igloo.ui.component

import android.graphics.Bitmap
import android.graphics.Rect
import android.text.style.ClickableSpan
import android.widget.ImageView
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.feed.FeedMediaCellDescriptor
import com.screwy.igloo.feed.FeedMediaCellModel
import com.screwy.igloo.media.MediaUri
import java.io.File
import java.io.FileOutputStream
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class NativeMainFeedSurfaceTest {
    @Test
    fun nativeFeedPrimaryActionsKeepStarOutOfTheLeftRow() {
        assertEquals(
            listOf(
                NativeFeedPrimaryAction.Share,
                NativeFeedPrimaryAction.Like,
                NativeFeedPrimaryAction.Bookmark,
            ),
            NativeFeedPrimaryActions,
        )
    }

    @Test
    fun nativeInlineVideoSelectorChoosesClosestVisibleCandidateDuringScroll() {
        val selected = chooseNativeInlineVideoCandidate(
            candidates = listOf(
                NativeInlineVideoCandidate(
                    key = "upper",
                    streamUri = MediaUri.Remote("https://example.test/upper.mp4"),
                    visibleFraction = 0.9f,
                    centerDistancePx = 480f,
                ),
                NativeInlineVideoCandidate(
                    key = "center",
                    streamUri = MediaUri.Remote("https://example.test/center.mp4"),
                    visibleFraction = 0.3f,
                    centerDistancePx = 10f,
                ),
            ),
        )

        assertEquals("center", selected?.key)
    }

    @Test
    fun nativeInlineVideoSameUriHandoffDoesNotPrepareANewStream() {
        val uri = MediaUri.Remote("https://example.test/same.mp4")
        val selected = NativeInlineVideoCandidate(
            key = "new-holder",
            streamUri = uri,
            visibleFraction = 1f,
            centerDistancePx = 0f,
        )

        assertEquals(
            NativeInlineVideoSwitchDecision.HandoffSameStream,
            nativeInlineVideoSwitchDecision(
                activeKey = "old-holder",
                activeStreamUri = uri,
                selected = selected,
            ),
        )
    }

    @Test
    fun nativeStableAspectRatioClampsBeforeImageLoad() {
        assertEquals(0.55f, nativeStableSingleMediaAspectRatio(cell(0.1f)), 0.0001f)
        assertEquals(1.25f, nativeStableSingleMediaAspectRatio(cell(1.25f)), 0.0001f)
        assertEquals(2.4f, nativeStableSingleMediaAspectRatio(cell(9f)), 0.0001f)
        assertEquals(1f, nativeStableSingleMediaAspectRatio(cell(1.7f, known = false)), 0.0001f)
        assertEquals(16f / 9f, nativeStableSingleMediaAspectRatio(cell(1f, known = false, isVideo = true)), 0.0001f)
    }

    @Test
    fun nativeStableAspectRatioUsesLocalImageBoundsWhenDescriptorIsUnknown() {
        val image = File.createTempFile("igloo-feed-aspect", ".png").also { it.deleteOnExit() }
        val bitmap = Bitmap.createBitmap(220, 110, Bitmap.Config.ARGB_8888)
        FileOutputStream(image).use { output ->
            assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output))
        }
        bitmap.recycle()

        val model = FeedMediaCellModel(
            descriptor = cell(1f, known = false),
            previewItem = MediaItem.Image(MediaUri.Local(image), aspectRatio = 1f),
        )

        assertEquals(2f, nativeStableSingleMediaAspectRatio(model), 0.0001f)
    }

    @Test
    fun nativeMediaScaleTypeUsesWebFeedRules() {
        assertEquals(ImageView.ScaleType.FIT_START, nativeMediaScaleTypeFor(cell(0.7f), isSingle = true))
        assertEquals(ImageView.ScaleType.CENTER_CROP, nativeMediaScaleTypeFor(cell(1.25f)))
        assertEquals(ImageView.ScaleType.CENTER_CROP, nativeMediaScaleTypeFor(cell(1.25f, isVideo = true)))
        assertEquals(ImageView.ScaleType.CENTER_CROP, nativeMediaScaleTypeFor(cell(1f, known = false), isSingle = true))
    }

    @Test
    fun nativeSingleMediaUsesTransparentLeftAlignedStage() {
        val text = nativeFeedSource()
        val mediaGridText = text.substringAfter("private fun bindMediaGrid")
            .substringBefore("private fun bindMediaCell")
        val mediaCellText = text.substringAfter("private fun bindMediaCell")
            .substringBefore("private fun loadAvatar")

        assertTrue(mediaGridText.contains("nativeStableSingleMediaAspectRatio(cell)"))
        assertTrue(mediaGridText.contains("background = roundedFill(Color.TRANSPARENT, dp(8))"))
        assertTrue(mediaCellText.contains("setBackgroundColor(if (isSingle) Color.TRANSPARENT else colors.surface)"))
        assertEquals(ImageView.ScaleType.FIT_START, nativeMediaScaleTypeFor(cell(1f), isSingle = true))
    }

    @Test
    fun nativeTranslationPillMatchesWebTimestampContract() {
        val active = nativeTranslationPillFor(
            item = FeedItemEntity(
                tweetId = "tweet-1",
                authorHandle = "alice",
                bodyText = "サンプル本文",
                lang = "ja",
                bodyTranslation = "hello",
                bodySourceLang = "Japanese",
            ),
            active = true,
            enabled = true,
        )
        val inactive = nativeTranslationPillFor(
            item = FeedItemEntity(
                tweetId = "tweet-2",
                authorHandle = "alice",
                bodyText = "hola",
                lang = "es",
                bodySourceLang = "Spanish",
            ),
            active = false,
            enabled = false,
        )
        val english = nativeTranslationPillFor(
            item = FeedItemEntity(
                tweetId = "tweet-3",
                authorHandle = "alice",
                bodyText = "hello",
                lang = "en",
            ),
            active = false,
            enabled = false,
        )

        assertEquals(NativeTranslationPill(sourceLangLabel = "Japanese", active = true, enabled = true), active)
        assertEquals(NativeTranslationPill(sourceLangLabel = "Spanish", active = false, enabled = false), inactive)
        assertEquals(null, english)
    }

    @Test
    fun nativeTranslationPillsArePerTextField() {
        val englishBody = nativeTranslationPillForText(
            lang = "en",
            sourceLang = null,
            text = "plain parent text",
            active = false,
            enabled = false,
        )
        val translatedQuote = nativeTranslationPillForText(
            lang = "fr",
            sourceLang = "French",
            text = "texte cite",
            active = true,
            enabled = true,
        )

        assertEquals(null, englishBody)
        assertEquals(NativeTranslationPill(sourceLangLabel = "French", active = true, enabled = true), translatedQuote)
    }

    @Test
    fun nativeTranslationPillUsesTranslatorLanguageLabel() {
        val translatedBody = nativeTranslationPillForText(
            lang = "kr",
            sourceLang = "Korean",
            text = "안녕하세요",
            active = true,
            enabled = true,
        )

        assertEquals(NativeTranslationPill(sourceLangLabel = "Korean", active = true, enabled = true), translatedBody)
    }

    @Test
    fun nativeVisibleFractionUsesCurrentViewportWithoutSettleDelay() {
        assertEquals(1f, nativeVisibleHeightFraction(Rect(0, 100, 100, 300), 600), 0.0001f)
        assertEquals(0.5f, nativeVisibleHeightFraction(Rect(0, -100, 100, 100), 600), 0.0001f)
        assertEquals(0f, nativeVisibleHeightFraction(Rect(0, 700, 100, 900), 600), 0.0001f)
    }

    @Test
    fun nativeFeedScrollRestoreFindsTheSamePostAfterRecreation() {
        val items = listOf(
            NativeFeedAdapterItem.Post(threadedRow("tweet-1"), post("tweet-1")),
            NativeFeedAdapterItem.Post(threadedRow("tweet-2"), post("tweet-2")),
            NativeFeedAdapterItem.Post(threadedRow("tweet-3"), post("tweet-3")),
        )

        assertEquals(
            1,
            nativeFeedRestoreAdapterIndex(items, NativeFeedScrollAnchor(rowId = "tweet-2", offsetPx = -42)),
        )
    }

    @Test
    fun nativeMainFeedRowsDoNotUseComposeView() {
        val text = nativeFeedSource()
        assertFalse(text.contains("ComposeView"))
    }

    @Test
    fun nativeMainFeedKeepsRefreshAndAvatarsNative() {
        val text = nativeFeedSource()
        assertFalse(text.contains("PullToRefreshBox"))
        assertTrue(text.contains("SwipeRefreshLayout"))
        assertTrue(text.contains("avatarForChannel"))
    }

    @Test
    fun nativeFeedCardMovesTranslationIntoHeaderMetaRow() {
        val text = nativeFeedSource()
        val cardViewsText = text.substringAfter("internal class NativeFeedCardViews")
            .substringBefore("internal class NativeIdentityHeaderViews")
        val headerViewsText = text.substringAfter("internal class NativeIdentityHeaderViews")
            .substringBefore("internal data class NativeVideoSlot")

        assertFalse(cardViewsText.contains("val translate: TextView"))
        assertFalse(cardViewsText.contains("root.addView(translate)"))
        assertTrue(headerViewsText.contains("val translate: LinearLayout"))
        assertTrue(headerViewsText.contains("R.drawable.ic_feed_translate_24"))
        assertFalse(headerViewsText.contains("metaRow.addView(meta, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))"))
        assertTrue(headerViewsText.contains("metaRow.addView(meta, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))"))
        assertTrue(text.contains("bindTranslationPill(header, translation, colors, onTranslationClick)"))
    }

    @Test
    fun nativeQuoteCardsClampTextAndOpenQuotedTweet() {
        val text = nativeFeedSource()
        val quoteText = text.substringAfter("private fun bindQuote")
            .substringBefore("private fun bindActions")

        assertTrue(quoteText.contains("callbacks.onQuoteOpen(quoteId)"))
        assertTrue(quoteText.contains("views.quoteBody.maxLines = NativeFeedQuoteCollapsedLines"))
        assertTrue(quoteText.contains("views.quoteBody.ellipsize = TextUtils.TruncateAt.END"))
        assertFalse(quoteText.contains("bindMentionText(views.quoteBody"))
    }

    @Test
    fun nativeFeedRendersThreadChainsWithRootPreviewAndCapsule() {
        val text = nativeFeedSource()
        val threadText = text.substringAfter("private fun bindThread")
            .substringBefore("private fun bindRetweeter")

        assertTrue(threadText.contains("nativeThreadPreviewAncestors(chain)"))
        assertFalse(threadText.contains("feed_thread_load_more_replies"))
        assertTrue(threadText.contains("R.string.feed_thread_capsule"))
        assertTrue(threadText.contains("callbacks.onRowClick(threaded.row)"))
        assertTrue(threadText.contains("stripReplyPrefix(item, item.bodyText.orEmpty())"))
    }

    @Test
    fun nativeFeedMenusUseThemePopupAndVerticalOverflowIcon() {
        val text = nativeFeedSource()
        val cardViewsText = text.substringAfter("internal class NativeFeedCardViews")
            .substringBefore("internal class NativeIdentityHeaderViews")

        assertFalse(text.contains("PopupMenu"))
        assertTrue(text.contains("PopupWindow"))
        assertTrue(text.contains("showNativeFeedPopup"))
        assertTrue(text.contains("roundedStroke(colors.surfaceElevated, colors.borderSubtle"))
        assertTrue(cardViewsText.contains("val menu: ImageButton"))
        assertTrue(text.contains("R.drawable.ic_feed_more_vert_24"))
    }

    @Test
    fun nativeFeedConfirmationsAreHostedByComposeDialogs() {
        val text = nativeFeedSource()
        val nativeHolderText = text.substringAfter("internal class NativeFeedViewHolder")
            .substringBefore("private fun bindHeader")

        assertFalse(text.contains("android.app.AlertDialog"))
        assertFalse(text.contains("AlertDialog.Builder"))
        assertTrue(text.contains("pendingUnfollowChannelId"))
        assertTrue(text.contains("pendingMuteAction"))
        assertTrue(text.contains("onRequestUnfollowConfirmation"))
        assertTrue(text.contains("onRequestMuteConfirmation"))
        assertFalse(nativeHolderText.contains("confirmUnfollow("))
        assertFalse(nativeHolderText.contains("confirmMute("))
    }

    @Test
    fun nativeBodyClampUsesLocalizedReadMoreAndShowLess() {
        val text = nativeFeedSource()
        val bindBodyText = text.substringAfter("private fun bindBody")
            .substringBefore("private fun bindQuote")

        assertTrue(bindBodyText.contains("R.string.action_read_more"))
        assertTrue(bindBodyText.contains("R.string.action_show_less"))
        assertFalse(bindBodyText.contains("Show full text"))
        assertTrue(nativeShouldClampBody("x".repeat(421)))
        assertFalse(nativeShouldClampBody("short"))
    }

    @Test
    fun nativeChannelHeaderUsesHeroGeometryWithoutBlankBand() {
        assertEquals(NativeChannelHeaderBannerHeightDp, NativeChannelHeaderBannerFrameHeightDp)
        assertEquals(NativeChannelHeaderAvatarSizeDp / 2, NativeChannelHeaderAvatarOverlapDp)
        assertTrue(NativeChannelHeaderBannerFrameHeightDp > 148)
        assertTrue(NativeChannelHeaderAvatarSizeDp > 92)
        assertTrue(NativeChannelHeaderActionRowHeightDp > NativeChannelHeaderAvatarOverlapDp)
        assertTrue(NativeChannelHeaderActionRowHeightDp <= NativeChannelHeaderAvatarOverlapDp + 8)
        assertTrue(NativeChannelHeaderFollowHeightDp <= 44)
        assertTrue(NativeChannelHeaderIconButtonSizeDp <= 40)
        assertTrue(NativeChannelHeaderNameTextSp <= 28f)
        assertTrue(NativeChannelHeaderBioTextSp <= 16f)
        assertTrue(NativeChannelHeaderMetaTextSp <= 17f)
    }

    @Test
    fun nativeChannelHeaderKeepsActionIconsCompact() {
        val text = nativeFeedSource()
        val headerText = text.substringAfter("internal class NativeFeedChannelHeaderViewHolder")
            .substringBefore("internal class NativeFeedViewHolder")
        val headerViewsText = text.substringAfter("internal class NativeFeedChannelHeaderViews")

        assertFalse(headerText.contains("android.R.drawable.btn_star_big_off"))
        assertFalse(headerText.contains("views.menu.text"))
        assertTrue(headerText.contains("R.drawable.ic_channel_star_24"))
        assertTrue(headerText.contains("R.drawable.ic_channel_star_border_24"))
        assertTrue(headerText.contains("R.drawable.ic_channel_more_horiz_24"))
        assertTrue(headerViewsText.contains("val menu: ImageButton"))
    }

    @Test
    fun nativeChannelHeaderBioUsesClickableMentionsAndUrls() {
        val text = nativeFeedSource()
        val headerText = text.substringAfter("internal class NativeFeedChannelHeaderViewHolder")
            .substringBefore("fun recycle()")

        assertTrue(headerText.contains("bindHeaderBio"))
        assertTrue(headerText.contains("LinkMovementMethod.getInstance()"))
        assertTrue(headerText.contains("clickableText("))
        assertTrue(headerText.contains("colors.channelProfileHeaderLinkColor(header.linkColorRole)"))
        assertTrue(headerText.contains("linkColor = linkColor"))
        assertTrue(headerText.contains("onMentionClick = callbacks.onMentionClick"))
        assertTrue(headerText.contains("onUrlClick = { url -> openExternalUrl"))
        assertFalse(headerText.contains("colors.info"))
    }

    @Test
    fun nativeClickableTextMarksMentionAndUrlSpans() {
        val text = clickableText(
            raw = "Ins: @example https://example.test",
            linkColor = 0xFF00FF,
            onMentionClick = {},
            onUrlClick = {},
        )

        assertEquals(2, text.getSpans(0, text.length, ClickableSpan::class.java).size)
    }

    @Test
    fun bareProfileWebsitesOpenAsHttpsUrls() {
        assertEquals("https://youtube.com/@example", externalUrlForIntent("youtube.com/@example"))
        assertEquals("https://example.com:443/path", externalUrlForIntent("example.com:443/path"))
        assertEquals("mailto:test@example.com", externalUrlForIntent("mailto:test@example.com"))
        assertEquals("https://example.test/path", externalUrlForIntent(" https://example.test/path "))
    }

    @Test
    fun nativeChannelHeaderUsesCompactNativeTextPadding() {
        val text = nativeFeedSource()
        val headerViewsText = text.substringAfter("internal class NativeFeedChannelHeaderViews")
            .substringBefore("internal class NativeFeedCardViews")

        assertTrue(headerViewsText.contains("setIncludeFontPadding(false)"))
        assertTrue(headerViewsText.contains("setLineSpacing(0f, 1.0f)"))
    }

    @Test
    fun nativeChannelHeaderKeepsBioInThemeCard() {
        val text = nativeFeedSource()
        val headerViewsText = text.substringAfter("internal class NativeFeedChannelHeaderViews")
            .substringBefore("internal class NativeFeedCardViews")

        assertTrue(headerViewsText.contains("val infoCard: LinearLayout"))
        assertTrue(headerViewsText.contains("ChannelProfileHeaderDefaults.CardHorizontalMarginDp"))
        assertTrue(headerViewsText.contains("ChannelProfileHeaderDefaults.CardHorizontalPaddingDp"))
        assertTrue(headerViewsText.contains("ChannelProfileHeaderDefaults.CardVerticalPaddingDp"))
        assertTrue(headerViewsText.contains("infoCard.background = roundedFill(colors.surfaceElevated, dp(ChannelProfileHeaderDefaults.CardRadiusDp))"))
        assertInfoCardChild(headerViewsText, "nameRow")
        assertInfoCardChild(headerViewsText, "handle")
        assertInfoCardChild(headerViewsText, "bio")
        assertInfoCardChild(headerViewsText, "website")
        assertInfoCardChild(headerViewsText, "stats")
        assertTrue(Regex("""content\.addView\(\s*infoCard\b""").containsMatchIn(headerViewsText))
        assertFalse(headerViewsText.contains("content.addView(nameRow)"))
        assertFalse(headerViewsText.contains("content.addView(bio)"))
        assertFalse(headerViewsText.contains("separator"))
    }

    private fun cell(
        aspectRatio: Float,
        known: Boolean = true,
        isVideo: Boolean = false,
    ): FeedMediaCellDescriptor =
        FeedMediaCellDescriptor(
            displayUrl = "https://example.test/image.jpg",
            streamUrl = "",
            posterUrl = "",
            isVideo = isVideo,
            aspectRatio = aspectRatio,
            aspectRatioKnown = known,
        )

    private fun threadedRow(tweetId: String) = com.screwy.igloo.data.entity.ThreadedFeedRow(
        row = feedRow(tweetId),
        chain = emptyList(),
    )

    private fun post(tweetId: String): com.screwy.igloo.feed.SocialPostModel {
        val row = feedRow(tweetId)
        return com.screwy.igloo.feed.SocialPostModel(
            row = row,
            author = com.screwy.igloo.feed.SocialProfileModel(
                channelId = "twitter_alice",
                handle = "alice",
                displayName = "Alice",
            ),
            actions = com.screwy.igloo.feed.SocialActionState(
                isLiked = false,
                isBookmarked = false,
                isAuthorFollowed = false,
                isAuthorStarred = false,
            ),
            media = com.screwy.igloo.feed.SocialMediaModel(
                ownerId = tweetId,
                grid = com.screwy.igloo.feed.FeedMediaGridModel(
                    ownerId = tweetId,
                    cells = emptyList(),
                    inventoryLoaded = true,
                ),
            ),
            quoteMedia = null,
        )
    }

    private fun feedRow(tweetId: String) = com.screwy.igloo.data.entity.FeedRow(
        item = FeedItemEntity(tweetId = tweetId, authorHandle = "alice"),
        channelName = "Alice",
        channelAvatarUrl = null,
        channelPlatform = "twitter",
        isLiked = 0,
        likedAt = null,
        isBookmarked = 0,
        bookmarkCategoryId = null,
        bookmarkCustomTitle = null,
        bookmarkedAt = null,
        channelIsFollowed = 0,
        channelIsStarred = 0,
    )

    private fun source(relative: String): String {
        val userDir = System.getProperty("user.dir").orEmpty()
        val root = generateSequence(File(userDir).absoluteFile) { it.parentFile }
            .firstOrNull { File(it, "app/src/$relative").isFile }
            ?: error("Could not locate Android source root from $userDir")
        return File(root, "app/src/$relative").readText()
    }

    private fun nativeFeedSource(): String =
        listOf(
            "NativeMainFeedSurface.kt",
            "NativeMainFeedController.kt",
            "NativeFeedAdapter.kt",
            "NativeFeedChannelHeaderViewHolder.kt",
            "NativeFeedPostViewHolder.kt",
            "NativeFeedViews.kt",
            "NativeFeedMedia.kt",
            "NativeFeedUiPrimitives.kt",
        ).joinToString("\n") { filename ->
            source("main/java/com/screwy/igloo/ui/component/$filename")
        }

    private fun assertInfoCardChild(source: String, child: String) {
        assertTrue(Regex("""infoCard\.addView\(\s*$child\b""").containsMatchIn(source))
    }
}
