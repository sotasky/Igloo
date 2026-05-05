package com.screwy.igloo.channel

import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class ChannelRouteResolverTest {

    @Test
    fun routeForHandle_prefersExistingSourceIdMatch() = runBlocking {
        val db = RoomTestSupport.freshDb()
        try {
            db.channelDao().upsert(
                ChannelEntity(
                    channelId = "twitter_alice_real",
                    sourceId = "alice",
                    name = "Alice",
                    platform = "twitter",
                ),
            )

            val route = ChannelRouteResolver.routeForHandle(db, "@Alice")

            assertEquals("channel/twitter_alice_real", route)
        } finally {
            db.close()
        }
    }

    @Test
    fun routeForHandle_prefersSourceIdMatchOnFallbackPlatform() = runBlocking {
        val db = RoomTestSupport.freshDb()
        try {
            db.channelDao().upsert(
                listOf(
                    ChannelEntity(
                        channelId = "twitter_alice",
                        sourceId = "alice",
                        name = "Alice X",
                        platform = "twitter",
                    ),
                    ChannelEntity(
                        channelId = "tiktok_alice",
                        sourceId = "alice",
                        name = "Alice TikTok",
                        platform = "tiktok",
                    ),
                ),
            )

            val route = ChannelRouteResolver.routeForHandle(db, "@Alice", fallbackPlatform = "tiktok")

            assertEquals("channel/tiktok_alice", route)
        } finally {
            db.close()
        }
    }

    @Test
    fun routeForHandle_keepsNonTwitterFallbackWhenOnlyOtherPlatformHandleExists() = runBlocking {
        val db = RoomTestSupport.freshDb()
        try {
            db.channelDao().upsert(
                ChannelEntity(
                    channelId = "twitter_alice",
                    sourceId = "alice",
                    name = "Alice X",
                    platform = "twitter",
                ),
            )

            val route = ChannelRouteResolver.routeForHandle(db, "@Alice", fallbackPlatform = "tiktok")

            assertEquals("channel/tiktok_alice", route)
        } finally {
            db.close()
        }
    }

    @Test
    fun routeForHandle_fallsBackToSyntheticPlatformRoute() = runBlocking {
        val db = RoomTestSupport.freshDb()
        try {
            val route = ChannelRouteResolver.routeForHandle(db, "@ghost_user", fallbackPlatform = "twitter")
            assertEquals("channel/twitter_ghost_user", route)
        } finally {
            db.close()
        }
    }

    @Test
    fun routeForHandle_usesProfileOnlyMatchOnFallbackPlatform() = runBlocking {
        val db = RoomTestSupport.freshDb()
        try {
            db.channelProfileDao().upsert(
                ChannelProfileEntity(
                    channelId = "youtube_UCprofileonly",
                    platform = "youtube",
                    handle = "@ProfileOnly",
                    displayName = "Profile Only",
                ),
            )

            val route = ChannelRouteResolver.routeForHandle(
                db = db,
                rawHandle = "@profileonly",
                fallbackPlatform = "youtube",
            )

            assertEquals("channel/youtube_UCprofileonly", route)
        } finally {
            db.close()
        }
    }

    @Test
    fun routeForHandle_usesProfileOnlyTwitterBeforeSyntheticFallback() = runBlocking {
        val db = RoomTestSupport.freshDb()
        try {
            db.channelProfileDao().upsert(
                ChannelProfileEntity(
                    channelId = "twitter_alice_real",
                    platform = "twitter",
                    handle = "alice",
                    displayName = "Alice",
                ),
            )

            val route = ChannelRouteResolver.routeForHandle(db, "@Alice")

            assertEquals("channel/twitter_alice_real", route)
        } finally {
            db.close()
        }
    }

    @Test
    fun routeForHandle_encodesExistingChannelIdsAsPathSegments() = runBlocking {
        val db = RoomTestSupport.freshDb()
        try {
            db.channelDao().upsert(
                ChannelEntity(
                    channelId = "twitter_alice/real",
                    sourceId = "alice",
                    name = "Alice",
                    platform = "twitter",
                ),
            )

            val route = ChannelRouteResolver.routeForHandle(db, "@Alice")

            assertEquals("channel/twitter_alice%2Freal", route)
        } finally {
            db.close()
        }
    }

    @Test
    fun normalizeHandle_stripsAtAndWhitespace() {
        assertEquals("alice", ChannelRouteResolver.normalizeHandle("  @Alice  "))
    }

    @Test
    fun normalizeHandle_stripsTrailingPunctuationWithoutDroppingInternalDots() {
        assertEquals("alice.smith", ChannelRouteResolver.normalizeHandle("@Alice.Smith,"))
    }
}
