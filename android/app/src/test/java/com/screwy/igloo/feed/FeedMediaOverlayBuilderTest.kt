package com.screwy.igloo.feed

import com.screwy.igloo.data.entity.FeedItemEntity
import org.junit.Assert.assertEquals
import org.junit.Test

class FeedMediaOverlayBuilderTest {
    @Test
    fun canonicalTweetUrl_usesOnlySyncedCanonicalUrl() {
        val item =
            FeedItemEntity(tweetId = "tw_1", canonicalUrl = "https://x.com/alice/status/tw_1")

        assertEquals("https://x.com/alice/status/tw_1", canonicalTweetUrl(item))
    }

    @Test
    fun canonicalTweetUrl_doesNotSynthesizeFromHandleAndId() {
        val item = FeedItemEntity(tweetId = "tw_1")

        assertEquals("", canonicalTweetUrl(item))
    }

    @Test
    fun quoteCanonicalTweetUrl_usesOnlySyncedQuoteCanonicalUrl() {
        val item =
            FeedItemEntity(
                tweetId = "tw_1",
                quoteTweetId = "quote_1",
                quoteCanonicalUrl = "https://x.com/bob/status/quote_1",
            )

        assertEquals("https://x.com/bob/status/quote_1", quoteCanonicalTweetUrl(item))
    }
}
