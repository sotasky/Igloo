package com.screwy.igloo.feed

import com.screwy.igloo.data.entity.FeedItemEntity
import org.junit.Assert.assertEquals
import org.junit.Test

class FeedMediaOverlayBuilderTest {
    @Test fun canonicalTweetUrl_usesOnlySyncedCanonicalUrl() {
        val item = FeedItemEntity(
            tweetId = "tw_1",
            sourceHandle = "source_user",
            authorHandle = "alice",
            canonicalUrl = "https://x.com/alice/status/tw_1",
        )

        assertEquals("https://x.com/alice/status/tw_1", canonicalTweetUrl(item))
    }

    @Test fun canonicalTweetUrl_doesNotSynthesizeFromHandleAndId() {
        val item = FeedItemEntity(
            tweetId = "tw_1",
            sourceHandle = "source_user",
            authorHandle = "alice",
        )

        assertEquals("", canonicalTweetUrl(item))
    }

    @Test fun quoteCanonicalTweetUrl_usesOnlySyncedQuoteCanonicalUrl() {
        val item = FeedItemEntity(
            tweetId = "tw_1",
            authorHandle = "alice",
            quoteTweetId = "quote_1",
            quoteAuthorHandle = "bob",
            quoteCanonicalUrl = "https://x.com/bob/status/quote_1",
        )

        assertEquals("https://x.com/bob/status/quote_1", quoteCanonicalTweetUrl(item))
    }
}
