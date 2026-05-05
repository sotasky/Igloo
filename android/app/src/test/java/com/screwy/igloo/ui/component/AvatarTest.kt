package com.screwy.igloo.ui.component

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AvatarTest {

    @Test
    fun igloo_host_urls_require_auth_headers() {
        assertTrue(
            shouldAuthorizeIglooImage(
                url = "https://igloo.example.com/api/media/avatar/tiktok_creator_1",
                iglooHost = "igloo.example.com",
            ),
        )
    }

    @Test
    fun host_match_is_case_insensitive() {
        assertTrue(
            shouldAuthorizeIglooImage(
                url = "https://IGLOO.EXAMPLE.COM/api/media/avatar/tiktok_creator_1",
                iglooHost = "igloo.example.com",
            ),
        )
    }

    @Test
    fun cdn_urls_do_not_require_auth_headers() {
        assertFalse(
            shouldAuthorizeIglooImage(
                url = "https://pbs.twimg.com/profile_images/test.jpg",
                iglooHost = "igloo.example.com",
            ),
        )
    }

    @Test
    fun malformed_urls_do_not_require_auth_headers() {
        assertFalse(
            shouldAuthorizeIglooImage(
                url = "/api/media/avatar/tiktok_creator_1",
                iglooHost = "igloo.example.com",
            ),
        )
    }

    @Test
    fun public_video_urls_are_not_igloo_server_urls() {
        assertFalse(
            isIglooServerUrl(
                url = "https://video.twimg.com/ext_tw_video/1/pu/vid/avc1/720x1280/a.mp4",
                iglooHost = "igloo.example.com",
            ),
        )
    }

    @Test
    fun igloo_stream_urls_are_igloo_server_urls() {
        assertTrue(
            isIglooServerUrl(
                url = "https://igloo.example.com/api/media/stream/tweet_1",
                iglooHost = "igloo.example.com",
            ),
        )
    }

}
