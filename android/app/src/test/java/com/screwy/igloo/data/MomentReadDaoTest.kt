package com.screwy.igloo.data

import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.MutedChannelEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.VideoRepostSourceEntity
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class MomentReadDaoTest {
    private lateinit var db: IglooDatabase

    @Before
    fun setUp() {
        db = RoomTestSupport.freshDb()
    }

    @After
    fun tearDown() {
        db.close()
    }

    @Test
    fun repostSettingsApplyToTiktokAndInstagramSources() = runBlocking {
        seedRepostedMoment(
            videoId = "tiktok_video",
            ownerId = "tiktok_author",
            reposterId = "tiktok_reposter",
            platform = "tiktok",
            repostedAtMs = 100L,
        )
        seedRepostedMoment(
            videoId = "instagram_video",
            ownerId = "instagram_author",
            reposterId = "instagram_reposter",
            platform = "instagram",
            repostedAtMs = 200L,
        )
        db.channelSettingDao()
            .upsert(
                listOf(
                    ChannelSettingEntity("tiktok_reposter", includeReposts = 1),
                    ChannelSettingEntity("instagram_reposter", includeReposts = 1),
                )
            )

        val rows = db.momentReadDao().momentsAllFlow().first()

        assertEquals(
            listOf("tiktok_video", "instagram_video"),
            rows.map { it.video.videoId },
        )

        db.channelSettingDao()
            .upsert(
                listOf(
                    ChannelSettingEntity("tiktok_reposter", includeReposts = 0),
                    ChannelSettingEntity("instagram_reposter", includeReposts = 0),
                )
            )

        assertVisibleVideoIds(emptyList())
    }

    @Test
    fun mutedCanonicalOwnerIsHiddenFromEveryMomentsListAndResumeLookup() = runBlocking {
        seedRepostedMoment(
            videoId = "tiktok_video",
            ownerId = "tiktok_author",
            reposterId = "tiktok_reposter",
            platform = "tiktok",
            repostedAtMs = 100L,
        )
        db.channelFollowDao().upsert(ChannelFollowEntity("tiktok_author", 1L))
        db.mutedChannelDao().upsert(MutedChannelEntity("tiktok_author", 2L))

        assertVisibleVideoIds(emptyList())
        assertTrue(db.momentReadDao().momentsFollowingFlow().first().isEmpty())
        assertTrue(db.momentReadDao().playerMomentsFollowingFlow().first().isEmpty())
        assertNull(db.momentReadDao().momentSortAtFlow("tiktok_video").first())
    }

    @Test
    fun mutedSoleReposterHidesItsMomentAndResumeLookup() = runBlocking {
        seedRepostedMoment(
            videoId = "instagram_video",
            ownerId = "instagram_author",
            reposterId = "instagram_reposter",
            platform = "instagram",
            repostedAtMs = 100L,
        )
        db.mutedChannelDao().upsert(MutedChannelEntity("instagram_reposter", 2L))

        assertVisibleVideoIds(emptyList())
        assertNull(db.momentReadDao().momentSortAtFlow("instagram_video").first())
    }

    @Test
    fun unmutedAlternateReposterKeepsMomentVisibleAndBecomesItsHead() = runBlocking {
        seedRepostedMoment(
            videoId = "instagram_video",
            ownerId = "instagram_author",
            reposterId = "instagram_muted_reposter",
            platform = "instagram",
            repostedAtMs = 300L,
        )
        seedReposter("instagram_enabled_reposter", "instagram")
        db.videoRepostSourceDao()
            .upsert(
                listOf(
                    VideoRepostSourceEntity(
                        videoId = "instagram_video",
                        reposterChannelId = "instagram_enabled_reposter",
                        repostedAtMs = 200L,
                        firstSeenAtMs = 200L,
                        updatedAtMs = 200L,
                    )
                )
            )
        db.mutedChannelDao().upsert(MutedChannelEntity("instagram_muted_reposter", 400L))

        val grid = db.momentReadDao().momentsAllFlow().first().single()
        val player = db.momentReadDao().playerMomentsAllFlow().first().single()

        assertEquals("instagram_enabled_reposter", grid.reposterChannelId)
        assertEquals(1, grid.repostCount)
        assertEquals(200L, grid.effectiveMomentAtMs)
        assertEquals(grid, player)
        assertEquals(200L, db.momentReadDao().momentSortAtFlow("instagram_video").first())
    }

    @Test
    fun directlyFollowedOwnerKeepsOriginalMomentTimeWithRepostMetadata() = runBlocking {
        seedRepostedMoment(
            videoId = "tiktok_video",
            ownerId = "tiktok_author",
            reposterId = "tiktok_reposter",
            platform = "tiktok",
            repostedAtMs = 300L,
        )
        db.channelFollowDao().upsert(ChannelFollowEntity("tiktok_author", 1L))

        val grid = db.momentReadDao().momentsAllFlow().first().single()
        val player = db.momentReadDao().playerMomentsAllFlow().first().single()

        assertEquals("tiktok_reposter", grid.reposterChannelId)
        assertEquals(1, grid.repostIntroduced)
        assertEquals(1L, grid.effectiveMomentAtMs)
        assertEquals(grid, player)
        assertEquals(1L, db.momentReadDao().momentSortAtFlow("tiktok_video").first())
    }

    @Test
    fun explicitRepostTimeWinsOverLaterDiscoveryFallback() = runBlocking {
        seedRepostedMoment(
            videoId = "tiktok_video",
            ownerId = "tiktok_author",
            reposterId = "tiktok_explicit_reposter",
            platform = "tiktok",
            repostedAtMs = 100L,
        )
        seedReposter("tiktok_discovery_reposter", "tiktok")
        db.videoRepostSourceDao()
            .upsert(
                listOf(
                    VideoRepostSourceEntity(
                        videoId = "tiktok_video",
                        reposterChannelId = "tiktok_discovery_reposter",
                        repostedAtMs = 0L,
                        firstSeenAtMs = 500L,
                        updatedAtMs = 500L,
                    )
                )
            )

        val grid = db.momentReadDao().momentsAllFlow().first().single()

        assertEquals("tiktok_explicit_reposter", grid.reposterChannelId)
        assertEquals(100L, grid.effectiveMomentAtMs)
        assertEquals(100L, db.momentReadDao().momentSortAtFlow("tiktok_video").first())
    }

    @Test
    fun mutedCanonicalOwnerIsHiddenFromStoryTrayAndPlaylistQueries() = runBlocking {
        val channelId = "tiktok_story_author"
        seedChannel(channelId, "tiktok")
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId, 1L))
        db.videoDao()
            .upsert(
                VideoEntity(
                    videoId = "tiktok_story_video",
                    channelId = channelId,
                    ownerKind = "channel",
                    publishedAt = 100L,
                    sourceKind = "story",
                )
            )

        assertEquals(
            listOf(channelId),
            db.momentReadDao().storyChannelsFlow(0L).first().map { it.channelId },
        )
        assertEquals(
            listOf(channelId),
            db.momentReadDao().storyStatusesFlow(0L).first().map { it.channelId },
        )
        assertEquals(
            listOf("tiktok_story_video"),
            db.momentReadDao().storyPlaylistFlow(channelId, 0L).first().map { it.video.videoId },
        )
        assertEquals(
            listOf("tiktok_story_video"),
            db.momentReadDao().storyTrayPlaylistFlow(0L).first().map { it.video.videoId },
        )

        db.mutedChannelDao().upsert(MutedChannelEntity(channelId, 2L))

        assertTrue(db.momentReadDao().storyChannelsFlow(0L).first().isEmpty())
        assertTrue(db.momentReadDao().storyStatusesFlow(0L).first().isEmpty())
        assertTrue(db.momentReadDao().storyPlaylistFlow(channelId, 0L).first().isEmpty())
        assertTrue(db.momentReadDao().storyTrayPlaylistFlow(0L).first().isEmpty())
    }

    private suspend fun assertVisibleVideoIds(expected: List<String>) {
        assertEquals(
            expected,
            db.momentReadDao().momentsAllFlow().first().map { it.video.videoId },
        )
        assertEquals(
            expected,
            db.momentReadDao().playerMomentsAllFlow().first().map { it.video.videoId },
        )
    }

    private suspend fun seedRepostedMoment(
        videoId: String,
        ownerId: String,
        reposterId: String,
        platform: String,
        repostedAtMs: Long,
    ) {
        seedChannel(ownerId, platform)
        seedReposter(reposterId, platform)
        db.videoDao()
            .upsert(
                VideoEntity(
                    videoId = videoId,
                    channelId = ownerId,
                    ownerKind = "channel",
                    publishedAt = 1L,
                )
            )
        db.videoRepostSourceDao()
            .upsert(
                listOf(
                    VideoRepostSourceEntity(
                        videoId = videoId,
                        reposterChannelId = reposterId,
                        repostedAtMs = repostedAtMs,
                        firstSeenAtMs = repostedAtMs,
                        updatedAtMs = repostedAtMs,
                    )
                )
            )
    }

    private suspend fun seedReposter(channelId: String, platform: String) {
        seedChannel(channelId, platform)
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId, 1L))
    }

    private suspend fun seedChannel(channelId: String, platform: String) {
        db.channelDao().upsert(ChannelEntity(channelId, channelId, channelId, null, platform))
    }
}
