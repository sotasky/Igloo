package com.screwy.igloo.feed

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.component.MediaItem
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class FeedMediaModelStoreTest {
    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope

    @Before
    fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After
    fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test
    fun observedOwnersReactToAssetReadinessAndRetainLocalModelsAcrossWindowChanges() = runBlocking {
        val dao = db.androidSyncDao()
        dao.upsertAsset(
            AndroidSyncAssetEntity(
                assetId = "asset-1",
                assetKind = "post_media",
                ownerId = "sample_tweet",
                ownerKind = "tweet",
                bucket = "twitter_media",
                contentType = "image/jpeg",
                sizeBytes = 1,
                revision = 1,
            )
        )
        val reachability =
            Reachability(scope, probe = { true }, foregroundFlow = flowOf(false)).apply {
                markOnline()
            }
        val store =
            FeedMediaModelStore(
                db = db,
                baseUrlProvider = ServerBaseUrlProvider { "https://igloo.example" },
                reachability = reachability,
                scope = scope,
            )

        store.setMediaModelRows(listOf(feedRow("sample_tweet"), feedRow("tweet-2")))
        withTimeout(2_000) { store.mediaModels.first { it.keys == setOf("sample_tweet", "tweet-2") } }

        store.setMediaModelRows(listOf(feedRow("tweet-2")))
        assertEquals(
            setOf("tweet-2"),
            withTimeout(2_000) { store.mediaModels.first { it.keys == setOf("tweet-2") } }.keys,
        )

        store.setMediaModelRows(listOf(feedRow("sample_tweet"), feedRow("tweet-2")))
        withTimeout(2_000) { store.mediaModels.first { it.keys == setOf("sample_tweet", "tweet-2") } }

        val localFile = File("/verified/sample.jpg")
        dao.markVerified("asset-1", 1, localFile.absolutePath, 2)
        val models =
            withTimeout(2_000) {
                store.mediaModels.first { rows ->
                    val item = rows["sample_tweet"]?.cells?.singleOrNull()?.previewItem
                    item is MediaItem.Image && item.uri is MediaUri.Local
                }
            }
        val item = models.getValue("sample_tweet").cells.single().previewItem as MediaItem.Image
        assertEquals(localFile, (item.uri as MediaUri.Local).file)

        store.setMediaModelRows(listOf(feedRow("tweet-2")))
        assertEquals(
            setOf("sample_tweet", "tweet-2"),
            withTimeout(2_000) {
                store.mediaModels.first { it.keys == setOf("sample_tweet", "tweet-2") }
            }.keys,
        )
    }

    private fun feedRow(tweetId: String) =
        FeedRow(
            item =
                FeedItemEntity(
                    tweetId = tweetId,
                    mediaJson = """[{"type":"photo","url":"https://example.test/sample.jpg"}]""",
                ),
            channelName = null,
            channelPlatform = "twitter",
            authorHandle = "sample_author",
            isLiked = 0,
            likedAt = null,
            isBookmarked = 0,
            bookmarkCategoryId = null,
            bookmarkCustomTitle = null,
            bookmarkedAt = null,
            channelIsFollowed = 0,
            channelIsStarred = 0,
        )

}
