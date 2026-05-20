package com.screwy.igloo.player

import android.content.Context
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.click
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.dp
import androidx.media3.exoplayer.ExoPlayer
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class PlayerGesturesCallbackTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun tap_uses_latest_controls_visibility_callback_after_recompose() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val player = ExoPlayer.Builder(context).build()
        lateinit var controlsVisible: MutableState<Boolean>
        var tapCount = 0

        try {
            composeRule.setContent {
                controlsVisible = remember { mutableStateOf(true) }
                PlayerGestureHarness(
                    player = player,
                    controlsVisible = controlsVisible.value,
                    onTap = { tapCount += 1 },
                    onControlsVisibleChange = { controlsVisible.value = it },
                )
            }

            composeRule.runOnIdle {
                controlsVisible.value = false
            }
            composeRule.waitForIdle()
            composeRule.runOnIdle {
                assertFalse(controlsVisible.value)
            }

            composeRule.onNodeWithTag(GestureTag)
                .performTouchInput { click() }
            composeRule.mainClock.advanceTimeBy(500L)
            composeRule.waitForIdle()

            composeRule.runOnIdle {
                assertEquals(1, tapCount)
                assertTrue(controlsVisible.value)
            }
        } finally {
            player.release()
        }
    }
}

@Composable
private fun PlayerGestureHarness(
    player: ExoPlayer,
    controlsVisible: Boolean,
    onTap: () -> Unit,
    onControlsVisibleChange: (Boolean) -> Unit,
) {
    Box(Modifier.size(100.dp)) {
        PlayerGestures(
            player = player,
            onTap = {
                onTap()
                onControlsVisibleChange(!controlsVisible)
            },
            modifier = Modifier
                .fillMaxSize()
                .testTag(GestureTag),
        )
    }
}

private const val GestureTag = "player-gestures"
