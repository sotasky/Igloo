package com.screwy.igloo.ui.component

import com.screwy.igloo.data.entity.ChannelDisplay
import com.screwy.igloo.data.entity.ChannelEntity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Pure-function tests for [defaultMediaIndices] and [buildPayload].
 */
class BookmarkSheetTest {

    private val target = BookmarkTarget(
        itemId = "item_1",
        authorHandle = "@author_handle",
        mediaCount = 3,
        sourceHandle = "source_handle",
        quoteAuthorHandle = "quote_handle",
        bodyText = "hello @extra_handle",
        isRetweet = true,
    )
	private val categories = listOf(
		BookmarkCategoryDisplay(categoryId = 7L, name = "reference"),
		BookmarkCategoryDisplay(categoryId = 9L, name = "favorites"),
	)

    // ── defaultMediaIndices ──────────────────────────────────────────────

    @Test
    fun defaultMediaIndices_no_existing_returns_full_range() {
        assertEquals(listOf(0, 1, 2), defaultMediaIndices(3, null))
    }

    @Test
    fun defaultMediaIndices_with_existing_echoes_existing() {
        assertEquals(listOf(0, 2), defaultMediaIndices(3, listOf(0, 2)))
    }

    @Test
    fun defaultMediaIndices_single_media_returns_singleton_list() {
        assertEquals(listOf(0), defaultMediaIndices(1, null))
    }

    @Test
    fun smartDefault_wins_when_no_existing() {
        assertEquals(listOf(1), defaultMediaIndices(3, existing = null, smartDefault = listOf(1)))
    }

    @Test
    fun existing_beats_smartDefault() {
        assertEquals(
            listOf(0, 2),
            defaultMediaIndices(3, existing = listOf(0, 2), smartDefault = listOf(1)),
        )
    }

    @Test
    fun bookmarkLabelPlaceholder_doesNotRenderDefaultTweetBody() {
        assertEquals(
            "Label (optional)",
            bookmarkLabelPlaceholder(" first row\nsecond row\r\nthird row ", "Label (optional)"),
        )
    }

    @Test
    fun bookmarkLabelTextFieldValue_placesCursorAtEnd() {
        val value = bookmarkLabelTextFieldValue("saved label")

        assertEquals("saved label", value.text)
        assertEquals(value.text.length, value.selection.start)
        assertEquals(value.text.length, value.selection.end)
    }

    // ── category selection helpers ───────────────────────────────────────

    @Test
    fun initialCategorySelection_prefers_existing_bookmark_category() {
        val existing = target.copy(
            currentBookmark = BookmarkState(
                categoryId = 9L,
                customTitle = null,
                mediaIndices = null,
            ),
        )
        assertEquals(9L, initialCategorySelection(existing, categories))
    }

    @Test
    fun initialCategorySelection_usesRememberedCategoryForNewBookmark() {
        assertEquals(7L, initialCategorySelection(target, categories, rememberedCategoryId = 7L))
    }

    @Test
    fun resolveCategorySelection_prefers_first_real_category_when_selection_missing() {
        val options = listOf(
            BookmarkCategoryDisplay(categoryId = -1L, name = "new stuff"),
            BookmarkCategoryDisplay(categoryId = 11L, name = "real"),
        )
        assertEquals(11L, resolveCategorySelection(selectedCategoryId = null, categories = options))
        assertEquals(11L, resolveCategorySelection(selectedCategoryId = -9L, categories = options))
    }

    @Test
    fun findCategoryByName_matches_case_insensitive_and_prefers_latest_row() {
        val options = listOf(
            BookmarkCategoryDisplay(categoryId = -1L, name = "Art"),
            BookmarkCategoryDisplay(categoryId = 42L, name = "art"),
        )
        assertEquals(42L, findCategoryByName(options, " ART ")!!.categoryId)
    }

    // ── buildPayload ─────────────────────────────────────────────────────

    @Test
    fun buildPayload_full_range_empty_title_normalizes_to_nulls() {
        val payload = buildPayload(
            target = target,
            categoryId = 5L,
            customTitle = "",
            mediaIndices = listOf(0, 1, 2),
            mediaCount = 3,
            selectedAccountHandles = setOf("author_handle"),
            availableAccountHandles = listOf("author_handle"),
        )
        assertEquals(5L, payload.categoryId)
        assertNull(payload.customTitle)
        assertNull(payload.mediaIndices)
        assertEquals(listOf("author_handle"), payload.accountHandles)
    }

    @Test
    fun buildPayload_partial_selection_keeps_indices_and_title() {
        val payload = buildPayload(
            target = target,
            categoryId = 5L,
            customTitle = "custom",
            mediaIndices = listOf(0),
            mediaCount = 3,
            selectedAccountHandles = setOf("source_handle"),
            availableAccountHandles = listOf("author_handle", "source_handle"),
        )
        assertEquals(5L, payload.categoryId)
        assertEquals("custom", payload.customTitle)
        assertEquals(listOf(0), payload.mediaIndices)
        assertEquals(listOf("source_handle"), payload.accountHandles)
    }

    @Test
    fun buildPayload_blank_whitespace_title_is_nulled() {
        val payload = buildPayload(
            target = target,
            categoryId = 5L,
            customTitle = "  ",
            mediaIndices = listOf(0),
            mediaCount = 3,
            selectedAccountHandles = emptySet(),
            availableAccountHandles = emptyList(),
        )
        assertNull(payload.customTitle)
        assertEquals(listOf("author_handle"), payload.accountHandles)
    }

    @Test
    fun parseStoredHandles_supportsJsonAndCsv() {
        assertEquals(listOf("alice", "bob"), parseStoredHandles("""["alice","@bob"]"""))
        assertEquals(listOf("alice", "bob"), parseStoredHandles("alice,@bob"))
    }

    @Test
    fun parseStoredMediaIndices_supportsJsonAndCsv() {
        assertEquals(listOf(0, 2), parseStoredMediaIndices("[2,0,2]"))
        assertEquals(listOf(1, 3), parseStoredMediaIndices("3,1,3"))
    }

    @Test
    fun buildBookmarkAccountOptions_collectsAuthorSourceQuoteAndMentions() {
        assertEquals(
            listOf("author_handle", "source_handle", "quote_handle", "extra_handle"),
            buildBookmarkAccountOptions(target),
        )
    }

    @Test
    fun buildSubscriptionBookmarkAccountOptions_usesFollowedChannelHandles() {
        val internalNumericId = "7".repeat(16)
        val internalSecUid = listOf("MS4w", "LjAB", "sample", "internal", "id").joinToString("")
        val options = buildSubscriptionBookmarkAccountOptions(
            listOf(
                channelDisplay(
                    channelId = "twitter_sample_alpha",
                    sourceId = "sample_alpha",
                    name = "Sample Alpha",
                    platform = "twitter",
                    isFollowed = 1,
                    handle = "sample_alpha",
                    displayName = "Readable Alpha",
                ),
                channelDisplay(
                    channelId = "twitter_sample_beta",
                    sourceId = "sample_beta",
                    name = "Sample Beta",
                    platform = "twitter",
                    isFollowed = 0,
                    handle = "sample_beta",
                    displayName = "Readable Beta",
                ),
                channelDisplay(
                    channelId = "youtube_sample_channel",
                    sourceId = "sample_channel",
                    name = "Sample Video Channel",
                    platform = "youtube",
                    isFollowed = 1,
                    handle = "",
                    displayName = "Sample Video Channel",
                ),
                channelDisplay(
                    channelId = "tiktok_$internalNumericId",
                    sourceId = internalNumericId,
                    name = "Internal Numeric",
                    platform = "tiktok",
                    isFollowed = 1,
                    handle = "",
                    displayName = "Internal Numeric",
                ),
                channelDisplay(
                    channelId = "tiktok_$internalSecUid",
                    sourceId = internalSecUid,
                    name = "Internal SecUid",
                    platform = "tiktok",
                    isFollowed = 1,
                    handle = "",
                    displayName = "Internal SecUid",
                ),
            ),
        )

        assertEquals(listOf(BookmarkAccountOption("sample_alpha", "Readable Alpha", "twitter")), options)
    }

    @Test
    fun filterBookmarkAccountOptions_matchesHandleAndLabelAndSkipsExisting() {
        val options = listOf(
            BookmarkAccountOption("sample_alpha", "Readable Alpha", "twitter"),
            BookmarkAccountOption("sample_beta", "Readable Beta", "twitter"),
        )

        assertEquals(
            listOf(BookmarkAccountOption("sample_beta", "Readable Beta", "twitter")),
            filterBookmarkAccountOptions(
                query = "readable",
                options = options,
                existingHandles = listOf("@sample_alpha"),
            ),
        )
    }

    @Test
    fun initialSelectedAccountHandles_prefersCurrentBookmarkAccounts() {
        val editing = target.copy(
            currentBookmark = BookmarkState(
                categoryId = 7L,
                customTitle = null,
                mediaIndices = null,
                accountHandles = listOf("quote_handle"),
            ),
        )
        assertEquals(
            setOf("quote_handle"),
            initialSelectedAccountHandles(
                target = editing,
                accountHandles = buildBookmarkAccountOptions(editing),
                rememberedAccountHandles = listOf("source_handle"),
                bookmarkedHandleSet = setOf("author_handle"),
            ),
        )
    }

    @Test
    fun initialSelectedAccountHandles_usesRememberedAccountsBeforeBookmarkedFallback() {
        assertEquals(
            setOf("source_handle"),
            initialSelectedAccountHandles(
                target = target,
                accountHandles = buildBookmarkAccountOptions(target),
                rememberedAccountHandles = listOf("source_handle"),
                bookmarkedHandleSet = setOf("author_handle"),
            ),
        )
    }

    private fun channelDisplay(
        channelId: String,
        sourceId: String?,
        name: String,
        platform: String,
        isFollowed: Int,
        handle: String?,
        displayName: String?,
    ): ChannelDisplay =
        ChannelDisplay(
            channel = ChannelEntity(
                channelId = channelId,
                sourceId = sourceId,
                name = name,
                platform = platform,
            ),
            isStarred = 0,
            isFollowed = isFollowed,
            handle = handle,
            displayName = displayName,
        )
}
