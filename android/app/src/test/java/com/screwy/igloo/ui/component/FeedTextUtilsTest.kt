package com.screwy.igloo.ui.component

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Pure-function tests for shared feed text/action helpers.
 *
 * One app-wide format so a "5m ago" tweet on Feed reads identically to a "5m ago" video on a TikTok
 * channel grid:
 * - delta < 60s -> "Just now"
 * - delta < 60m -> "<n>m ago"
 * - delta < 24h -> "<n>h ago"
 * - delta < 7d -> "<n>d ago"
 * - delta < 30d -> "<n>w ago"
 * - delta < 365d -> "<n>mo ago"
 * - otherwise -> "<n>y ago"
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class FeedTextUtilsTest {

    private val now: Long = 1_700_000_000_000L
    private lateinit var context: Context

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
    }

    @Test
    fun sub_minute_delta_returns_just_now() {
        val published = now - TimeUnit.SECONDS.toMillis(30)
        assertEquals("Just now", localizedRelativeTime(context, published, now))
    }

    @Test
    fun five_minute_delta_returns_5m_ago() {
        val published = now - TimeUnit.MINUTES.toMillis(5)
        assertEquals("5m ago", localizedRelativeTime(context, published, now))
    }

    @Test
    fun three_hour_delta_returns_3h_ago() {
        val published = now - TimeUnit.HOURS.toMillis(3)
        assertEquals("3h ago", localizedRelativeTime(context, published, now))
    }

    @Test
    fun two_day_delta_returns_2d_ago() {
        val published = now - TimeUnit.DAYS.toMillis(2)
        assertEquals("2d ago", localizedRelativeTime(context, published, now))
    }

    @Test
    fun ten_day_delta_returns_1w_ago() {
        // Floor-divide: 10 days → 1 week (we don't round to nearest)
        val published = now - TimeUnit.DAYS.toMillis(10)
        assertEquals("1w ago", localizedRelativeTime(context, published, now))
    }

    @Test
    fun sixty_day_delta_returns_2mo_ago() {
        val published = now - TimeUnit.DAYS.toMillis(60)
        assertEquals("2mo ago", localizedRelativeTime(context, published, now))
    }

    @Test
    fun two_year_delta_returns_2y_ago() {
        val published = now - TimeUnit.DAYS.toMillis(365 * 2 + 1)
        assertEquals("2y ago", localizedRelativeTime(context, published, now))
    }

    @Test
    fun display_label_prefers_primary_name_without_at_prefix() {
        assertEquals(
            "Alice",
            displayLabel(primary = "@Alice", fallback = "Fallback", handle = "alice"),
        )
    }

    @Test
    fun display_label_prefers_distinct_fallback_when_primary_is_handle() {
        assertEquals(
            "Readable Creator",
            displayLabel(primary = "@creator", fallback = "Readable Creator", handle = "creator"),
        )
    }

    @Test
    fun display_label_falls_back_to_secondary_name_then_handle() {
        assertEquals(
            "Fallback",
            displayLabel(primary = " ", fallback = "Fallback", handle = "alice"),
        )
        assertEquals("alice", displayLabel(primary = null, fallback = null, handle = "alice"))
    }

    @Test
    fun should_show_handle_keeps_known_handle_when_display_matches_handle() {
        assertEquals(true, shouldShowHandle(displayLabel = "alice", handle = "Alice"))
        assertEquals(true, shouldShowHandle(displayLabel = "Alice Doe", handle = "alice"))
        assertEquals(false, shouldShowHandle(displayLabel = "Alice Doe", handle = ""))
    }

    @Test
    fun tiktok_handle_candidate_rejects_internal_ids() {
        assertEquals("Creator.One", tikTokHandleUnlessInternalId(" @Creator.One "))
        assertEquals("", tikTokHandleUnlessInternalId("7000000000000000001"))
        assertEquals(
            "",
            tikTokHandleUnlessInternalId(
                "MS4wLjABAAAANimIR6uNi69rFPkPOrdPgNIMp2fyxEqejtZTXpYL1cYb3DxzB-qGjWBE6XJGvA5J"
            ),
        )
    }

    @Test
    fun feed_share_url_prefers_server_canonical_url() {
        val item = FeedItemEntity(tweetId = "123", canonicalUrl = "https://x.com/alice/status/123")
        assertEquals("https://x.com/alice/status/123", feedShareUrl(item))
    }

    @Test
    fun feed_share_url_stays_blank_when_canonical_url_is_blank() {
        val item = FeedItemEntity(tweetId = "123", canonicalUrl = " ")
        assertEquals("", feedShareUrl(item))
    }

    @Test
    fun strip_reply_prefix_removes_redundant_leading_reply_handle() {
        val row =
            feedRow(
                item = FeedItemEntity(tweetId = "sample_tweet_0", isReply = true),
                authorHandle = "sample_reply_author",
                replyHandle = "sample_parent_author",
            )

        assertEquals(
            "only if it reaches 100 retweets",
            stripReplyPrefix(row, "@sample_parent_author only if it reaches 100 retweets"),
        )
        assertEquals(
            "only if it reaches 100 retweets",
            stripReplyPrefix(row, "@sample_parent_author: only if it reaches 100 retweets"),
        )
        assertEquals(
            "only if it reaches 100 retweets",
            stripReplyPrefix(
                row,
                "@sample_parent_author @sample_other_author only if it reaches 100 retweets",
            ),
        )
        assertEquals(
            "only if it reaches 100 retweets",
            stripReplyPrefix(
                row,
                "@sample_parent_author,@sample_other_author: only if it reaches 100 retweets",
            ),
        )
        assertEquals(
            "@sample_other_author only if it reaches 100 retweets",
            stripReplyPrefix(row, "@sample_other_author only if it reaches 100 retweets"),
        )
    }

    @Test
    fun native_thread_preview_ancestors_match_web_root_leaf_rule() {
        assertEquals(emptyList<String>(), nativeThreadPreviewAncestors(emptyList<String>()))
        assertEquals(listOf("root"), nativeThreadPreviewAncestors(listOf("root")))
        assertEquals(listOf("root"), nativeThreadPreviewAncestors(listOf("root", "parent")))
    }

    @Test
    fun mute_menu_actions_show_retweet_author_and_quote_targets() {
        val actions =
            feedMuteMenuActions(
                row =
                    feedRow(
                        item =
                            FeedItemEntity(
                                tweetId = "tweet_1",
                                channelId = "sample_author_channel",
                                quoteChannelId = "sample_quote_channel",
                                isRetweet = true,
                            ),
                        authorHandle = "quoted_author",
                        quoteAuthorHandle = "quote_author",
                    ),
                mutedChannelIds = setOf("sample_quote_channel"),
            )

        assertEquals(listOf("quoted_author", "quote_author"), actions.map { it.handle })
        assertEquals(listOf(false, true), actions.map { it.isMuted })
    }

    @Test
    fun mute_menu_actions_skip_non_retweet_author_target() {
        val actions =
            feedMuteMenuActions(
                row =
                    feedRow(
                        item =
                            FeedItemEntity(
                                tweetId = "tweet_2",
                                channelId = "sample_author_channel",
                            ),
                        authorHandle = "plain_author",
                    ),
                mutedChannelIds = emptySet(),
            )

        assertEquals(emptyList<String>(), actions.map { it.handle })
    }

    @Test
    fun quote_follow_target_uses_server_channel_id() {
        val target =
            feedQuoteFollowTarget(
                feedRow(
                    item =
                        FeedItemEntity(
                            tweetId = "tweet_3",
                            channelId = "twitter_parent_author",
                            quoteTweetId = "quote_1",
                            quoteChannelId = "sample_quote_channel",
                        ),
                    authorHandle = "parent_author",
                    quoteAuthorHandle = "@Quote_Author",
                )
            )

        assertEquals("sample_quote_channel", target?.channelId)
    }

    @Test
    fun quote_follow_target_hides_when_quote_author_is_followed() {
        val target =
            feedQuoteFollowTarget(
                feedRow(
                    item =
                        FeedItemEntity(
                            tweetId = "tweet_4",
                            channelId = "twitter_parent_author",
                            quoteTweetId = "quote_1",
                            quoteChannelId = "sample_quote_channel",
                        ),
                    authorHandle = "parent_author",
                    quoteAuthorHandle = "quote_author",
                    quoteChannelIsFollowed = 1,
                )
            )

        assertEquals(null, target)
    }

    @Test
    fun quote_follow_target_hides_for_self_quote() {
        val target =
            feedQuoteFollowTarget(
                feedRow(
                    item =
                        FeedItemEntity(
                            tweetId = "tweet_5",
                            channelId = "twitter_same_author",
                            quoteTweetId = "quote_1",
                            quoteChannelId = "twitter_same_author",
                        ),
                    authorHandle = "same_author",
                    quoteAuthorHandle = "same_author",
                )
            )

        assertEquals(null, target)
    }

    @Test
    fun main_feed_primary_actions_are_the_compact_igloo_row_without_star() {
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
    fun display_name_that_is_really_handle_can_supply_quote_handle() {
        assertEquals("unusual_whales", displayNameLooksLikeHandle("unusual_whales"))
        assertEquals("", displayNameLooksLikeHandle("Unusual Whales"))
    }

    private fun feedRow(
        item: FeedItemEntity,
        authorHandle: String? = null,
        quoteAuthorHandle: String? = null,
        replyHandle: String? = null,
        quoteChannelIsFollowed: Int = 0,
    ) =
        FeedRow(
            item = item,
            channelName = null,
            channelPlatform = "twitter",
            authorHandle = authorHandle,
            quoteAuthorHandle = quoteAuthorHandle,
            replyHandle = replyHandle,
            isLiked = 0,
            likedAt = null,
            isBookmarked = 0,
            bookmarkCategoryId = null,
            bookmarkCustomTitle = null,
            bookmarkedAt = null,
            channelIsFollowed = 0,
            channelIsStarred = 0,
            quoteChannelIsFollowed = quoteChannelIsFollowed,
        )
}
