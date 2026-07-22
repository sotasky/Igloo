package com.screwy.igloo.ui.component

import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.screwy.igloo.media.OwnerKind
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class MomentPagerSettlementTest {
    @get:Rule val composeRule = createComposeRule()

    @Test
    fun inserting_rows_before_the_current_video_does_not_report_another_settlement() {
        var items by
            mutableStateOf(
                listOf(
                    momentItem("older"),
                    momentItem("active"),
                    momentItem("newer"),
                )
            )
        val settledVideoIds = mutableListOf<String>()

        composeRule.setContent {
            val pagerItems = rememberMomentPagerSessionItems(items)
            val pagerState = rememberPagerState(initialPage = 1, pageCount = { pagerItems.size })
            MomentPagerSettlementEffect(
                pagerState = pagerState,
                items = pagerItems,
                onSettled = { settledVideoIds += it.videoId },
            )
            VerticalPager(
                state = pagerState,
                key = { page -> pagerItems[page].videoId },
                modifier = Modifier.size(width = 360.dp, height = 640.dp),
            ) { page ->
                Text(pagerItems[page].videoId)
            }
        }

        composeRule.runOnIdle { assertEquals(listOf("active"), settledVideoIds) }
        composeRule.runOnIdle { items = listOf(momentItem("backfill")) + items }
        composeRule.onNodeWithText("active").assertIsDisplayed()
        composeRule.runOnIdle { assertEquals(listOf("active"), settledVideoIds) }
    }

    private fun momentItem(videoId: String): MomentItem =
        MomentItem(
            videoId = videoId,
            channelId = "tiktok_sample",
            authorHandle = "@sample",
            description = videoId,
            likeCount = null,
            isLiked = false,
            isBookmarked = false,
            ownerKind = OwnerKind.TikTokVideo,
        )
}
