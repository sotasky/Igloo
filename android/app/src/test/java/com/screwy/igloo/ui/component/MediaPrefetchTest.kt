package com.screwy.igloo.ui.component

import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Test

class MediaPrefetchTest {

    @Test fun prefetchWindowIndices_extendsVisibleRangeWithinBounds() {
        assertEquals(
            (4..30).toList(),
            prefetchWindowIndices(
                visibleIndices = listOf(10, 11, 12),
                itemCount = 40,
                before = 6,
                after = 18,
            ),
        )
    }

    @Test fun prefetchWindowIndices_clampsAtListEdges() {
        assertEquals(
            (0..9).toList(),
            prefetchWindowIndices(
                visibleIndices = listOf(0, 1),
                itemCount = 10,
                before = 6,
                after = 18,
            ),
        )
    }

    @Test fun prefetchWindowIndices_ignoresEmptyInputs() {
        assertEquals(emptyList<Int>(), prefetchWindowIndices(emptyList(), itemCount = 10))
        assertEquals(emptyList<Int>(), prefetchWindowIndices(listOf(1, 2), itemCount = 0))
    }

    @Test fun imageWarmupWindow_isTighterThanModelWarmupWindow() {
        assertEquals(
            (8..20).toList(),
            prefetchWindowIndices(
                visibleIndices = listOf(10, 11, 12),
                itemCount = 40,
                before = 2,
                after = 8,
            ),
        )
    }

    @Test fun prefetchMediaTargetsWarmsAvatarsAndBannersForChannels() = runBlocking {
        val resolvers = RecordingMediaResolvers()

        prefetchMediaTargets(
            targets = listOf(
                MediaPrefetchTarget(channelIds = setOf("channel-a", " ", "channel-b")),
                MediaPrefetchTarget(channelIds = setOf("channel-a")),
            ),
            resolvers = resolvers,
        )

        assertEquals(2, resolvers.avatarFlowRequests.size)
        assertEquals(2, resolvers.bannerFlowRequests.size)
        assertEquals(setOf("channel-a", "channel-b"), resolvers.avatarFlowRequests.toSet())
        assertEquals(setOf("channel-a", "channel-b"), resolvers.bannerFlowRequests.toSet())
    }

    @Test fun isSafeRemotePrefetchUrl_allowsIglooHostAndPublicHttps() {
        assertEquals(true, isSafeRemotePrefetchUrl("http://192.168.1.20/image.jpg", iglooHost = "192.168.1.20"))
        assertEquals(true, isSafeRemotePrefetchUrl("https://cdn.example.com/image.jpg", iglooHost = "igloo.local"))
    }

    @Test fun isSafeRemotePrefetchUrl_blocksUnsafeTargets() {
        assertEquals(false, isSafeRemotePrefetchUrl("file:///etc/passwd", iglooHost = "igloo.local"))
        assertEquals(false, isSafeRemotePrefetchUrl("file://igloo.local/etc/passwd", iglooHost = "igloo.local"))
        assertEquals(false, isSafeRemotePrefetchUrl("content://igloo.local/media/1", iglooHost = "igloo.local"))
        assertEquals(false, isSafeRemotePrefetchUrl("http://127.0.0.1/admin", iglooHost = "igloo.local"))
        assertEquals(false, isSafeRemotePrefetchUrl("http://192.168.1.1/router", iglooHost = "igloo.local"))
        assertEquals(false, isSafeRemotePrefetchUrl("http://[::1]/", iglooHost = "igloo.local"))
    }

    private class RecordingMediaResolvers : MediaResolvers {
        val avatarFlowRequests = mutableListOf<String>()
        val bannerFlowRequests = mutableListOf<String>()

        override suspend fun thumbnailForPost(ownerId: String, ownerKind: OwnerKind): MediaUri = MediaUri.Missing
        override fun thumbnailForPostFlow(ownerId: String, ownerKind: OwnerKind): Flow<MediaUri> = flowOf(MediaUri.Missing)
        override suspend fun avatarForChannel(channelId: String): MediaUri = MediaUri.Missing
        override fun avatarForChannelFlow(channelId: String): Flow<MediaUri> {
            avatarFlowRequests += channelId
            return flowOf(MediaUri.Missing)
        }
        override suspend fun bannerForChannel(channelId: String): MediaUri = MediaUri.Missing
        override fun bannerForChannelFlow(channelId: String): Flow<MediaUri> {
            bannerFlowRequests += channelId
            return flowOf(MediaUri.Missing)
        }
        override suspend fun videoStream(videoId: String, ownerKind: OwnerKind): MediaUri = MediaUri.Missing
        override fun videoStreamFlow(videoId: String, ownerKind: OwnerKind): Flow<MediaUri> = flowOf(MediaUri.Missing)
    }
}
