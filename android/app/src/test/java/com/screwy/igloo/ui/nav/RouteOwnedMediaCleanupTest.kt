package com.screwy.igloo.ui.nav

import java.io.File
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RouteOwnedMediaCleanupTest {
    @Test
    fun feedLikedThreadAndChannelViewModelsDoNotRetainRouteLocalMediaOverlayState() {
        val files = listOf(
            "feed/FeedViewModel.kt",
            "liked/LikedViewModel.kt",
            "thread/ThreadViewModel.kt",
            "channel/ChannelViewModel.kt",
        )

        files.forEach { path ->
            val text = source("main/java/com/screwy/igloo/$path")
            assertFalse("$path still owns mediaOverlay state", text.contains("mediaOverlay"))
            assertFalse("$path still owns overlayRow state", text.contains("overlayRow"))
            assertFalse("$path still opens local media overlays", text.contains("fun openMedia("))
            assertFalse("$path still closes local media overlays", text.contains("fun closeMedia("))
            assertFalse("$path still owns overlay actions", text.contains("toggleOverlay"))
            assertFalse("$path still opens overlay authors", text.contains("openOverlayAuthor"))
        }
    }

    @Test
    fun nativeFeedSurfaceDoesNotAcceptDeadMediaOverlayInputs() {
        val text = source("main/java/com/screwy/igloo/ui/component/NativeMainFeedSurface.kt")
        listOf(
            "mediaOverlay:",
            "mediaOverlayInitialIndex",
            "mediaOverlayInitialVideoPositionMs",
            "overlayRow:",
            "onCloseMedia:",
            "onToggleOverlayBookmark:",
            "onToggleOverlayLike:",
            "onOpenOverlayAuthor:",
        ).forEach { forbidden ->
            assertFalse("NativeFeedSurface still exposes $forbidden", text.contains(forbidden))
        }
    }

    @Test
    fun routesDoNotPassNullOverlayPlaceholders() {
        val files = listOf(
            "feed/FeedRoute.kt",
            "liked/LikedRoute.kt",
        )
        files.forEach { path ->
            val text = source("main/java/com/screwy/igloo/$path")
            assertFalse("$path still passes mediaOverlay placeholder args", text.contains("mediaOverlay = null"))
            assertFalse("$path still passes overlayRow placeholder args", text.contains("overlayRow = null"))
        }
    }

    @Test
    fun feedMediaRouteIsDirectFullscreenSoScaffoldChromeCannotFlash() {
        val text = source("main/java/com/screwy/igloo/ui/nav/AppNavHost.kt")
        assertTrue("Media route should be direct fullscreen hosting", text.contains("directDestination(RouteRegistry.Media)"))
        assertFalse("Media route should not be scaffold hosted", text.contains("scaffoldDestination(navController, RouteRegistry.Media)"))
    }

    @Test
    fun mediaRouteObservesActionSideTablesInsteadOfOnlyEntrySnapshot() {
        val text = source("main/java/com/screwy/igloo/media/MediaRouteViewModel.kt")
        assertTrue("Media route lost its live like state flow", text.contains("db.feedLikeDao().getByIdFlow(ownerId)"))
        assertTrue("Media route lost its live bookmark state flow", text.contains("db.bookmarkDao().getByIdFlow(ownerId)"))
    }

    @Test
    fun oldComposeFeedStackIsRemovedFromMainSources() {
        listOf(
            "ui/component/FeedColumn.kt",
            "ui/component/FeedCard.kt",
            "ui/component/ThreadStack.kt",
            "ui/component/QuotedPostCard.kt",
            "ui/component/FeedMediaGrid.kt",
            "ui/component/FeedInlineVideoCoordinator.kt",
            "ui/component/FeedTimelineState.kt",
            "ui/timeline/TimelineState.kt",
        ).forEach { path ->
            assertFalse("$path should not remain after native feed migration", exists("main/java/com/screwy/igloo/$path"))
        }
    }

    @Test
    fun feedComponentsDoNotKeepDeadInlineResumePlumbing() {
        val files = listOf(
            "thread/ThreadRoute.kt",
            "ui/component/NativeMainFeedSurface.kt",
            "ui/component/MediaViewer.kt",
        )

        files.forEach { path ->
            val text = source("main/java/com/screwy/igloo/$path")
            assertFalse("$path still carries inline resume requests", text.contains("InlineVideoResumeRequest"))
            assertFalse("$path still passes inline video resume state", text.contains("inlineVideoResumeRequest"))
            assertFalse("$path still suspends inline playback for route-local overlays", text.contains("inlinePlaybackSuspended"))
            assertFalse("$path still passes dead media click positions", text.contains("playbackPositionMs"))
        }
    }

    private fun source(relative: String): String {
        val userDir = System.getProperty("user.dir").orEmpty()
        val root = generateSequence(File(userDir).absoluteFile) { it.parentFile }
            .firstOrNull { File(it, "app/src/$relative").isFile }
            ?: error("Could not locate Android source root from $userDir")
        return File(root, "app/src/$relative").readText()
    }

    private fun exists(relative: String): Boolean {
        val userDir = System.getProperty("user.dir").orEmpty()
        val root = generateSequence(File(userDir).absoluteFile) { it.parentFile }
            .firstOrNull { File(it, "app/src").isDirectory }
            ?: error("Could not locate Android source root from $userDir")
        return File(root, "app/src/$relative").exists()
    }
}
