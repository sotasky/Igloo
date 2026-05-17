package com.screwy.igloo.sync

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.CursorDao
import com.screwy.igloo.log.Logger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Watches retention-window prefs and forces replay when a window widens.
 *
 * The local DB is a disposable cache; widening retention means older rows that
 * were previously outside the local content window must become eligible again.
 * Reset the affected inbound stream cursors and let Sync materialize the media
 * for whatever content becomes desired.
 */
class RetentionReplayCoordinator(
    private val scope: CoroutineScope,
    private val prefs: PreferencesRepo,
    private val cursorDao: CursorDao,
    private val replayTrigger: SyncReplayTrigger,
    private val syncTrigger: () -> Unit,
    private val logger: Logger,
) : RetentionReplayRunner {

    private var observerJob: Job? = null

    override fun start() {
        if (observerJob?.isActive == true) return
        observerJob = scope.launch(start = CoroutineStart.UNDISPATCHED) {
            var previous = RetentionSnapshot(
                feed = prefs.retentionDaysFeed().first(),
                moments = prefs.retentionDaysMoments().first(),
                youtube = prefs.retentionDaysYoutube().first(),
                stories = prefs.storiesWindowHours().first(),
            )
            combine(
                prefs.retentionDaysFeed(),
                prefs.retentionDaysMoments(),
                prefs.retentionDaysYoutube(),
                prefs.storiesWindowHours(),
            ) { feed, moments, youtube, stories ->
                RetentionSnapshot(feed = feed, moments = moments, youtube = youtube, stories = stories)
            }
                .distinctUntilChanged()
                .collect { current ->
                    val prior = previous
                    previous = current
                    handleChange(prior, current)
                }
        }
    }

    override fun stop() {
        observerJob?.cancel()
        observerJob = null
    }

    private suspend fun handleChange(previous: RetentionSnapshot, current: RetentionSnapshot) {
        if (previous == current) return

        val resetStreams = linkedSetOf<String>()
        val widenedBuckets = mutableListOf<String>()

        if (widened(previous.feed, current.feed)) {
            widenedBuckets += "feed"
            resetStreams += "feed"
        }
        if (widened(previous.moments, current.moments)) {
            widenedBuckets += "moments"
            resetStreams += "shorts"
        }
        if (widened(previous.stories, current.stories)) {
            widenedBuckets += "stories"
            resetStreams += "shorts"
        }
        if (widened(previous.youtube, current.youtube)) {
            widenedBuckets += "youtube"
            resetStreams += "youtube_videos"
        }

        if (resetStreams.isEmpty()) {
            logger.info(
                event = "retention_prune_refresh",
                fields = mapOf(
                    "previous" to previous.toString(),
                    "current" to current.toString(),
                ),
            )
            syncTrigger()
            return
        }

        for (stream in resetStreams) {
            cursorDao.delete(stream)
        }

        logger.info(
            event = "retention_replay_reset",
            fields = mapOf(
                "buckets" to widenedBuckets.joinToString(","),
                "streams" to resetStreams.joinToString(","),
                "previous" to previous.toString(),
                "current" to current.toString(),
            ),
        )

        replayTrigger.triggerReplay()
        syncTrigger()
    }

    private fun widened(previous: Int, current: Int): Boolean = when {
        previous == 0 -> false
        current == 0 -> true
        current > previous -> true
        else -> false
    }

    private data class RetentionSnapshot(
        val feed: Int,
        val moments: Int,
        val youtube: Int,
        val stories: Int,
    )
}
