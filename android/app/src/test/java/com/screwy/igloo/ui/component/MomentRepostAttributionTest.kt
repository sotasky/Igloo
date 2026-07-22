package com.screwy.igloo.ui.component

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material3.Text
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.click
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeLeft
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.ui.theme.IglooTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class MomentRepostAttributionTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun repost_author_stays_tappable_above_drawer_gesture_handle() {
        var clickedChannelId: String? = null

        composeRule.setContent {
            IglooTheme {
                Box(modifier = Modifier.size(width = 360.dp, height = 640.dp)) {
                    MomentDrawerGestureHandle(onOpenDrawer = {})
                    CollapsedDescription(
                        item = sampleRepostItem(),
                        expanded = false,
                        onMentionClick = {},
                        onChannelClick = {},
                        onReposterChannelClick = { clickedChannelId = it },
                        onExpandedChange = {},
                        modifier =
                            Modifier.align(Alignment.BottomStart).padding(bottom = 16.dp),
                    )
                }
            }
        }

        // This point is both in the author's annotated range and inside the 96dp left-edge
        // drawer target. A real touch must reach the author annotation, not the drawer's
        // sibling pointer input.
        composeRule.onNodeWithText("Sample Reposter", substring = true).performTouchInput {
            click(Offset(x = 4f, y = centerY))
        }

        composeRule.runOnIdle {
            assertEquals("tiktok_sample_reposter", clickedChannelId)
        }
    }

    @Test
    fun drawer_edge_long_press_opens_moment_actions_without_opening_drawer() {
        var actionCount = 0
        var drawerCount = 0

        composeRule.setContent {
            IglooTheme {
                Box(modifier = Modifier.size(width = 360.dp, height = 640.dp)) {
                    MomentDrawerGestureHandle(
                        onOpenDrawer = { drawerCount++ },
                        onLongPress = { actionCount++ },
                        modifier = Modifier.testTag(DrawerEdgeTag),
                    )
                }
            }
        }

        composeRule.onNodeWithTag(DrawerEdgeTag).performTouchInput { longClick() }

        composeRule.runOnIdle {
            assertEquals(1, actionCount)
            assertEquals(0, drawerCount)
        }
    }

    @Test
    fun drawer_edge_swipe_still_opens_the_drawer_without_actions() {
        var actionCount = 0
        var drawerCount = 0

        composeRule.setContent {
            IglooTheme {
                Box(modifier = Modifier.size(width = 360.dp, height = 640.dp)) {
                    MomentDrawerGestureHandle(
                        onOpenDrawer = { drawerCount++ },
                        onLongPress = { actionCount++ },
                        modifier = Modifier.testTag(DrawerEdgeTag),
                    )
                }
            }
        }

        composeRule.onNodeWithTag(DrawerEdgeTag).performTouchInput {
            down(Offset(x = 4f, y = centerY))
            moveTo(Offset(x = width * 0.9f, y = centerY), delayMillis = 100L)
            up()
        }

        composeRule.runOnIdle {
            assertEquals(0, actionCount)
            assertEquals(1, drawerCount)
        }
    }

    @Test
    fun stationary_moment_gestures_leave_slideshow_swipes_to_the_pager() {
        var currentSlide: () -> Int = { 0 }
        var tapCount = 0
        var longPressCount = 0

        composeRule.setContent {
            val pagerState = rememberPagerState(pageCount = { 2 })
            currentSlide = { pagerState.currentPage }
            Box(modifier = Modifier.size(width = 360.dp, height = 640.dp)) {
                HorizontalPager(
                    state = pagerState,
                    modifier = Modifier.fillMaxSize().testTag(SlideshowTag),
                ) { page ->
                    Box(
                        modifier =
                            Modifier.fillMaxSize()
                                .momentStationaryGestures(
                                    onTap = { tapCount++ },
                                    onLongPress = { longPressCount++ },
                                )
                    ) {
                        Text("Slide ${page + 1}")
                    }
                }
            }
        }

        val slideshow = composeRule.onNodeWithTag(SlideshowTag, useUnmergedTree = true)
        slideshow.performTouchInput { swipeLeft() }
        composeRule.waitUntil { currentSlide() == 1 }

        composeRule.runOnIdle {
            assertEquals(0, tapCount)
            assertEquals(0, longPressCount)
        }

        slideshow.performTouchInput { click() }
        slideshow.performTouchInput { longClick() }

        composeRule.runOnIdle {
            assertEquals(1, tapCount)
            assertEquals(1, longPressCount)
        }
    }

    @Test
    fun slideshow_edge_swipe_opens_profile_once_after_the_threshold() {
        val tracker = MomentSlideshowEdgeSwipeTracker()

        assertFalse(tracker.onUnconsumedDrag(deltaX = -30f, thresholdPx = 80f))
        assertTrue(tracker.onUnconsumedDrag(deltaX = -51f, thresholdPx = 80f))
        assertFalse(tracker.onUnconsumedDrag(deltaX = -100f, thresholdPx = 80f))

        tracker.reset()
        assertTrue(tracker.onUnconsumedDrag(deltaX = -81f, thresholdPx = 80f))
    }

    @Test
    fun fallback_long_press_keeps_actions_available_without_ready_video() {
        assertTrue(
            shouldUseMomentActionFallbackLongPress(
                storyMode = false,
                hasMomentActions = true,
            )
        )
        assertFalse(
            shouldUseMomentActionFallbackLongPress(
                storyMode = true,
                hasMomentActions = true,
            )
        )
        assertFalse(
            shouldUseMomentActionFallbackLongPress(
                storyMode = false,
                hasMomentActions = false,
            )
        )
    }

    private fun sampleRepostItem(): MomentItem =
        MomentItem(
            videoId = "sample-repost-video",
            channelId = "tiktok_sample_author",
            authorHandle = "@sample_author",
            description = "",
            likeCount = null,
            isLiked = false,
            isBookmarked = false,
            ownerKind = OwnerKind.TikTokVideo,
            repostIntroduced = true,
            reposterChannelId = "tiktok_sample_reposter",
            repostAuthorLabel = "Sample Reposter",
        )
}

private const val DrawerEdgeTag = "moment-drawer-edge"
private const val SlideshowTag = "moment-slideshow"
