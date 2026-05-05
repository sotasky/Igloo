package com.screwy.igloo.ui.component

import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import org.junit.Assert.assertEquals
import org.junit.Test

class MediaCellModelTest {

    @Test
    fun explicitFallbackThumbnailWins() {
        val fallback = MediaUri.Local(java.io.File("/tmp/poster.jpg"))
        val model = MediaCellModel(
            mediaId = "video-1",
            ownerKind = OwnerKind.TikTokVideo,
            fallbackThumbnailUri = fallback,
        )

        assertEquals(fallback, model.initialThumbnailUri("https://igloo.example"))
    }

    @Test
    fun thumbnailPathResolvesAbsoluteAndServerRelativePaths() {
        assertEquals(
            MediaUri.Remote("https://cdn.example/thumb.jpg"),
            MediaCellModel(
                mediaId = "video-1",
                ownerKind = OwnerKind.YouTubeVideo,
                thumbnailPath = " https://cdn.example/thumb.jpg ",
            ).initialThumbnailUri("https://igloo.example"),
        )
        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/thumbnail/video-1"),
            MediaCellModel(
                mediaId = "video-1",
                ownerKind = OwnerKind.YouTubeVideo,
                thumbnailPath = "/api/media/thumbnail/video-1",
            ).initialThumbnailUri("https://igloo.example/"),
        )
    }

    @Test
    fun generatedServerFallbackUsesMediaShape() {
        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/slide/video-1/0"),
            MediaCellModel(
                mediaId = "video-1",
                ownerKind = OwnerKind.TikTokVideo,
                mediaKind = "slideshow",
                slideCount = 4,
            ).initialThumbnailUri("https://igloo.example"),
        )
        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/thumbnail/video-2"),
            MediaCellModel(
                mediaId = "video-2",
                ownerKind = OwnerKind.YouTubeVideo,
                mediaKind = "video",
            ).initialThumbnailUri("https://igloo.example"),
        )
    }

    @Test
    fun tweetServerFallbackIsExplicit() {
        val model = MediaCellModel(
            mediaId = "tweet-1",
            ownerKind = OwnerKind.Tweet,
            mediaKind = "video",
        )

        assertEquals(MediaUri.Missing, model.initialThumbnailUri("https://igloo.example"))
        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/thumbnail/tweet-1"),
            model.copy(allowServerThumbnailFallback = true)
                .initialThumbnailUri("https://igloo.example"),
        )
    }

    @Test
    fun displayThumbnailFallsBackOnlyWhenResolvedIsMissing() {
        val resolved = MediaUri.Local(java.io.File("/tmp/current.jpg"))
        val fallback = MediaUri.Remote("https://igloo.example/api/media/thumbnail/video-1")

        assertEquals(resolved, displayMediaCellThumbnail(resolved, fallback))
        assertEquals(fallback, displayMediaCellThumbnail(MediaUri.Missing, fallback))
    }
}
