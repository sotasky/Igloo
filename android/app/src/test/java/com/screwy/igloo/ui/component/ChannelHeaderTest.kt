package com.screwy.igloo.ui.component

import com.screwy.igloo.data.entity.ChannelDisplay
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Pure-function tests for channel profile header model construction. */
class ChannelHeaderTest {

    @Test
    fun parsePlatform_youtube_lowercase() {
        assertEquals(Platform.YouTube, parsePlatform("youtube"))
    }

    @Test
    fun parsePlatform_youtube_uppercase_is_case_insensitive() {
        assertEquals(Platform.YouTube, parsePlatform("YOUTUBE"))
    }

    @Test
    fun parsePlatform_tiktok() {
        assertEquals(Platform.TikTok, parsePlatform("tiktok"))
    }

    @Test
    fun parsePlatform_null_returns_null() {
        assertNull(parsePlatform(null))
    }

    @Test
    fun parsePlatform_unknown_returns_null() {
        assertNull(parsePlatform("unknown"))
    }

    @Test
    fun channelProfileHeaderUiModel_buildsSharedDisplayMediaAndLinkState() {
        val model =
            channelProfileHeaderUiModel(
                channel = channel(platform = "tiktok", channelId = "tiktok_alice", isStarred = 1),
                profile =
                    ChannelProfileEntity(
                        channelId = "tiktok_alice",
                        platform = "tiktok",
                        handle = "@Alice",
                        displayName = "Alice Liddell",
                        bio = " Bio @bob https://example.test ",
                        website = " https://site.example ",
                        followers = 2_630_000,
                        following = 485,
                        verified = true,
                        verifiedType = "business",
                    ),
                labels = labels(),
            )

        assertEquals("tiktok_alice", model.channelId)
        assertEquals(Platform.TikTok, model.platform)
        assertEquals("Alice Liddell", model.displayName)
        assertEquals("Alice", model.handle)
        assertEquals("TikTok", model.platformLabel)
        assertEquals("TikTok", model.openLabel)
        assertNull(model.platformUrl)
        assertEquals("Bio @bob https://example.test", model.bio)
        assertEquals("https://site.example", model.website)
        assertEquals(listOf("485 Follows", "2.6M Fans"), model.stats)
        assertEquals("Locked", model.protectedText)
        assertTrue(model.isVerified)
        assertTrue(model.isFollowed)
        assertTrue(model.isStarred)
        assertEquals(ChannelProfileHeaderLinkColorRole.Primary, model.linkColorRole)
    }

    @Test
    fun channelProfileHeaderUiModel_prefersStoredChannelUrlOverProfileUrl() {
        val model =
            channelProfileHeaderUiModel(
                channel =
                    channel(
                        platform = "tiktok",
                        channelId = "tiktok_alice",
                        url = "https://www.tiktok.com/@stored",
                    ),
                profile =
                    ChannelProfileEntity(
                        channelId = "tiktok_alice",
                        platform = "tiktok",
                        handle = "alice",
                    ),
                labels = labels(),
            )

        assertEquals("https://www.tiktok.com/@stored", model.platformUrl)
    }

    @Test
    fun channelProfileHeaderUiModel_doesNotInventPlatformUrl() {
        val model =
            channelProfileHeaderUiModel(
                channel = channel(platform = "tiktok", channelId = "tiktok_alice", url = null),
                profile = null,
                labels = labels(),
            )

        assertNull(model.platformUrl)
    }

    @Test
    fun channelProfileHeaderUiModel_rejectsTiktokInternalSourceId() {
        val model =
            channelProfileHeaderUiModel(
                channel =
                    channel(
                        platform = "tiktok",
                        channelId = "tiktok_7000000000000000001",
                        sourceId = "7000000000000000001",
                        name = "Readable Creator",
                    ),
                profile = null,
                labels = labels(),
            )

        assertEquals("Readable Creator", model.displayName)
        assertEquals("", model.handle)
    }

    @Test
    fun channelProfileHeaderUiModel_usesPlatformLabelsAndYoutubeSubscribers() {
        val labels = labels()

        assertEquals(
            "X",
            channelProfileHeaderUiModel(channel("twitter", "twitter_alice"), null, labels = labels)
                .platformLabel,
        )
        assertEquals(
            "TikTok",
            channelProfileHeaderUiModel(channel("tiktok", "tiktok_alice"), null, labels = labels)
                .platformLabel,
        )
        assertEquals(
            "Instagram",
            channelProfileHeaderUiModel(
                    channel("instagram", "instagram_alice"),
                    null,
                    labels = labels,
                )
                .platformLabel,
        )

        val youtube =
            channelProfileHeaderUiModel(
                channel = channel(platform = "youtube", channelId = "youtube_alice"),
                profile =
                    ChannelProfileEntity(
                        channelId = "youtube_alice",
                        platform = "youtube",
                        handle = "alice",
                        followers = 12_300,
                        following = 25,
                    ),
                labels = labels,
            )
        assertEquals("YouTube", youtube.platformLabel)
        assertEquals(listOf("25 Follows", "12.3K Subs"), youtube.stats)
    }

    private fun channel(
        platform: String,
        channelId: String,
        sourceId: String = "@alice",
        name: String = "alice",
        url: String? = null,
        isFollowed: Int = 1,
        isStarred: Int = 0,
    ): ChannelDisplay =
        ChannelDisplay(
            channel =
                ChannelEntity(
                    channelId = channelId,
                    sourceId = sourceId,
                    name = name,
                    url = url,
                    platform = platform,
                ),
            isStarred = isStarred,
            isFollowed = isFollowed,
        )

    private fun labels(): ChannelProfileHeaderLabels =
        ChannelProfileHeaderLabels(
            following = "Follows",
            followers = "Fans",
            subscribers = "Subs",
            protectedAccount = "Locked",
            browser = "Browser",
        )
}
