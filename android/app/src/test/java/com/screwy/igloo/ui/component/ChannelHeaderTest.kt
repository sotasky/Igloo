package com.screwy.igloo.ui.component

import com.screwy.igloo.data.entity.ChannelDisplay
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.media.MediaUri
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-function tests for channel profile header model construction.
 */
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
    fun channelBannerUrl_triesServerBannerForTiktokProfileEvenWhenProfileUrlIsSyntheticOrMissing() {
        val profile = ChannelProfileEntity(
            channelId = "tiktok_alice",
            platform = "tiktok",
            handle = "alice",
            bannerUrl = "",
        )

        assertEquals(
            "https://igloo.example/api/media/banner/tiktok_alice",
            channelBannerUrl(
                baseUrl = "https://igloo.example/",
                channelId = "tiktok_alice",
                profile = profile,
                platform = Platform.TikTok,
            ),
        )
    }

    @Test
    fun channelBannerUrl_reservesKnownPlatformBannerBeforeProfileArrives() {
        assertEquals(
            "https://igloo.example/api/media/banner/tiktok_alice",
            channelBannerUrl(
                baseUrl = "https://igloo.example",
                channelId = "tiktok_alice",
                profile = null,
                platform = Platform.TikTok,
            ),
        )
    }

    @Test
    fun channelBannerUrl_requiresPlatformOrProfileBanner() {
        assertNull(channelBannerUrl("https://igloo.example", "unknown", profile = null, platform = null))
    }

    @Test
    fun channelProfileHeaderUiModel_keepsTiktokGeometryStableBeforeAndAfterProfileLoads() {
        val channel = channel(platform = "unknown", channelId = "tiktok_alice")
        val before = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example",
            channel = channel,
            profile = null,
            labels = labels(),
        )
        val after = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example",
            channel = channel,
            profile = ChannelProfileEntity(
                channelId = "tiktok_alice",
                platform = "tiktok",
                handle = "alice",
                displayName = "Alice",
                bannerUrl = "https://cdn.example/banner.jpg",
            ),
            labels = labels(),
        )

        assertEquals(true, before.hasBannerSlot)
        assertEquals(before.hasBannerSlot, after.hasBannerSlot)
        assertEquals(before.bannerHeightDp, after.bannerHeightDp)
        assertEquals(before.avatarSizeDp, after.avatarSizeDp)
        assertEquals(before.avatarOverlapDp, after.avatarOverlapDp)
    }

    @Test
    fun channelProfileHeaderUiModel_buildsSharedDisplayMediaAndLinkState() {
        val avatar = MediaUri.Local(File("/tmp/avatar.jpg"))
        val banner = MediaUri.Remote("https://cdn.example/banner.jpg")
        val model = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example/",
            channel = channel(platform = "tiktok", channelId = "tiktok_alice", isStarred = 1),
            profile = ChannelProfileEntity(
                channelId = "tiktok_alice",
                platform = "tiktok",
                handle = "@Alice",
                displayName = "Alice Liddell",
                bio = " Bio @bob https://example.test ",
                website = " https://site.example ",
                followers = 2_630_000,
                followersLabel = "server-fans",
                following = 485,
                followingLabel = "server-follows",
                verified = true,
                verifiedType = "business",
                bannerUrl = "",
                profileUrl = "https://www.tiktok.com/@Alice",
            ),
            initialAvatarUri = avatar,
            initialBannerUri = banner,
            labels = labels(),
        )

        assertEquals("tiktok_alice", model.channelId)
        assertEquals(Platform.TikTok, model.platform)
        assertEquals("Alice Liddell", model.displayName)
        assertEquals("Alice", model.handle)
        assertEquals("TikTok", model.platformLabel)
        assertEquals("TikTok", model.openLabel)
        assertEquals("https://www.tiktok.com/@Alice", model.platformUrl)
        assertEquals(avatar, model.initialAvatarUri)
        assertEquals(banner, model.initialBannerUri)
        assertEquals("https://igloo.example/api/media/banner/tiktok_alice", model.fallbackBannerUrl)
        assertTrue(model.hasBannerSlot)
        assertEquals(ChannelProfileHeaderDefaults.ComposeBannerHeightDp, model.bannerHeightDp)
        assertEquals(ChannelProfileHeaderDefaults.ComposeAvatarSizeDp, model.avatarSizeDp)
        assertEquals(ChannelProfileHeaderDefaults.ComposeAvatarOverlapDp, model.avatarOverlapDp)
        assertEquals("Bio @bob https://example.test", model.bio)
        assertEquals("https://site.example", model.website)
        assertEquals(listOf("server-follows Follows", "server-fans Fans"), model.stats)
        assertEquals("Locked", model.protectedText)
        assertTrue(model.isVerified)
        assertTrue(model.isFollowed)
        assertTrue(model.isStarred)
        assertEquals(ChannelProfileHeaderLinkColorRole.Primary, model.linkColorRole)
    }

    @Test
    fun channelProfileHeaderUiModel_prefersStoredChannelUrlOverProfileUrl() {
        val model = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example",
            channel = channel(
                platform = "tiktok",
                channelId = "tiktok_alice",
                url = "https://www.tiktok.com/@stored",
            ),
            profile = ChannelProfileEntity(
                channelId = "tiktok_alice",
                platform = "tiktok",
                handle = "alice",
                profileUrl = "https://www.tiktok.com/@profile",
            ),
            labels = labels(),
        )

        assertEquals("https://www.tiktok.com/@stored", model.platformUrl)
    }

    @Test
    fun channelProfileHeaderUiModel_usesSyncedProfileUrlWhenChannelUrlMissing() {
        val model = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example",
            channel = channel(
                platform = "youtube",
                channelId = "youtube_UCprofileonly",
                url = null,
            ),
            profile = ChannelProfileEntity(
                channelId = "youtube_UCprofileonly",
                platform = "youtube",
                handle = "profileonly",
                profileUrl = "https://www.youtube.com/@profileonly",
            ),
            labels = labels(),
        )

        assertEquals("https://www.youtube.com/@profileonly", model.platformUrl)
    }

    @Test
    fun channelProfileHeaderUiModel_doesNotInventPlatformUrl() {
        val model = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example",
            channel = channel(
                platform = "tiktok",
                channelId = "tiktok_alice",
                url = null,
            ),
            profile = null,
            labels = labels(),
        )

        assertNull(model.platformUrl)
    }

    @Test
    fun channelProfileHeaderUiModel_rejectsTiktokInternalSourceId() {
        val model = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example",
            channel = channel(
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

        assertEquals("X", channelProfileHeaderUiModel("https://igloo.example", channel("twitter", "twitter_alice"), null, labels = labels).platformLabel)
        assertEquals("TikTok", channelProfileHeaderUiModel("https://igloo.example", channel("tiktok", "tiktok_alice"), null, labels = labels).platformLabel)
        assertEquals("Instagram", channelProfileHeaderUiModel("https://igloo.example", channel("instagram", "instagram_alice"), null, labels = labels).platformLabel)

        val youtube = channelProfileHeaderUiModel(
            baseUrl = "https://igloo.example",
            channel = channel(platform = "youtube", channelId = "youtube_alice"),
            profile = ChannelProfileEntity(
                channelId = "youtube_alice",
                platform = "youtube",
                handle = "alice",
                followers = 12_300,
                followersLabel = "12.3K",
                following = 25,
                followingLabel = "25",
            ),
            labels = labels,
        )
        assertEquals("YouTube", youtube.platformLabel)
        assertEquals(listOf("25 Follows", "12.3K Subs"), youtube.stats)
    }

    @Test
    fun selectedChannelBannerUriKeepsWarmSnapshotWhenResolverStartsMissing() {
        val snapshot = MediaUri.Remote("https://igloo.example/api/media/banner/tiktok_alice")

        assertEquals(
            snapshot,
            selectedChannelBannerUri(
                resolvedBannerUri = MediaUri.Missing,
                fallbackBannerUri = snapshot,
            ),
        )
    }

    @Test
    fun initialChannelBannerUriPrefersWarmSnapshotOverModelUrl() {
        val snapshot = MediaUri.Local(File("/tmp/banner.jpg"))

        assertEquals(
            snapshot,
            initialChannelBannerUri(
                modelBannerUrl = "https://igloo.example/api/media/banner/tiktok_alice",
                snapshotBannerUri = snapshot,
            ),
        )
    }

    @Test
    fun composeChannelHeaderKeepsProfileInfoInSharedThemeCard() {
        val text = source("main/java/com/screwy/igloo/ui/component/ChannelHeader.kt")
        val cardText = text.substringAfter("private fun ChannelProfileInfoCard")
            .substringBefore("@Composable\nprivate fun HeaderActionRow")

        assertTrue(text.contains("fun ComposeChannelHeader("))
        assertTrue(text.contains("header: ChannelProfileHeaderUiModel"))
        assertTrue(cardText.contains("RoundedCornerShape(ChannelProfileHeaderDefaults.CardRadiusDp.dp)"))
        assertTrue(cardText.contains("ChannelProfileHeaderDefaults.CardHorizontalPaddingDp.dp"))
        assertTrue(cardText.contains("ChannelProfileHeaderDefaults.CardVerticalPaddingDp.dp"))
        assertTrue(cardText.contains("colors.channelProfileHeaderLinkColor(header.linkColorRole)"))
        assertTrue(cardText.contains("mentionColorOverride = linkColor"))
        assertTrue(cardText.contains("urlColorOverride = linkColor"))
        assertFalse(cardText.contains("colors.info"))
    }

    @Test
    fun composeChannelHeaderCardMatchesNativeFeedCardWidth() {
        val text = source("main/java/com/screwy/igloo/ui/component/ChannelHeader.kt")

        assertTrue(text.contains(".padding(horizontal = ChannelProfileHeaderDefaults.CardHorizontalMarginDp.dp)"))
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
            channel = ChannelEntity(
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

    private fun source(relative: String): String {
        val userDir = System.getProperty("user.dir").orEmpty()
        val root = generateSequence(File(userDir).absoluteFile) { it.parentFile }
            .firstOrNull { File(it, "app/src/$relative").isFile }
            ?: error("Could not locate Android source root from $userDir")
        return File(root, "app/src/$relative").readText()
    }
}
