package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.CursorDao
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ChannelsApi
import com.screwy.igloo.net.DeltaResponse
import com.screwy.igloo.net.FeedApi
import com.screwy.igloo.net.ShortsApi
import com.screwy.igloo.net.VideoApi
import io.ktor.client.plugins.ResponseException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import java.io.IOException

/**
 * Inbound sync loop (bundle delta ingest).
 *
 * Responsibilities:
 *  - Iterate the four streams in priority order on every trigger.
 *  - For each stream, loop cursor-paged calls until `end_of_stream: true`.
 *  - Ingest each bundle through `BundleIngest` (runs its own transaction per bundle).
 *  - Advance `cursors[stream]` as pages succeed; stop only on end-of-stream, cancellation,
 *    transport failure, or a protocol-stall guard.
 *  - On HTTP/parse failure, bail the stream, preserve the cursor, let the next trigger retry.
 *
 * Streams are sequential within one trigger (they share `cursors` table + Room write
 * transactions); triggers themselves are conflated via the channel so rapid-fire
 * pull-to-refresh collapses into one pass.
 */
class InboundReconciler(
    private val db: IglooDatabase,
    private val prefs: PreferencesRepo,
    private val cursorDao: CursorDao,
    private val outboxDao: OutboxDao,
    private val feedApi: FeedApi,
    private val videoApi: VideoApi,
    private val shortsApi: ShortsApi,
    private val channelsApi: ChannelsApi,
    private val rankRefreshTrigger: () -> Unit = {},
    private val reachability: Reachability,
    private val logger: Logger,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) : InboundSyncRunner {
    private companion object {
        val RETRY_DELAYS_MS = longArrayOf(750L, 1_500L)
    }

    private val triggerChannel = Channel<Set<SyncStream>>(capacity = Channel.CONFLATED)

    /** Fire a full pass (all streams). Conflated — rapid triggers coalesce. */
    override fun trigger() {
        triggerChannel.trySend(SyncStream.ALL)
    }

    /** Fire a single-stream pass (used for targeted pull-to-refresh). */
    fun triggerStream(stream: SyncStream) {
        triggerChannel.trySend(setOf(stream))
    }

    /** Fire an explicit multi-stream pass (scheduler unions scoped requests). */
    override fun triggerStreams(streams: Set<SyncStream>) {
        triggerChannel.trySend(streams)
    }

    override suspend fun run() {
        while (true) {
            try {
                val streams = triggerChannel.receive()
                runPass(streams)
            }
            catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // Any exception escaping the per-stream handler is a bug; log and keep
                // the long-lived loop alive so the next trigger can retry.
                logger.error("inbound_reconciler_unhandled", mapOf("class" to (e::class.simpleName ?: "?")), e)
            }
        }
    }

    private suspend fun runPass(streams: Set<SyncStream>) {
        if (!isOnline()) {
            logger.debug(
                event = "inbound_pass_skipped_offline",
                fields = mapOf("state" to reachability.state.value::class.simpleName.orEmpty()),
            )
            return
        }
        logger.info(
            event = "inbound_pass_start",
            fields = mapOf("streams" to streams.joinToString(",") { it.cursorKey }),
        )
        val ingest = BundleIngest(db, nowMsProvider)
        val guard = PreserveLocalGuard(outboxDao)
        for (stream in streams.sortedBy { it.priority }) {
            if (!isOnline()) {
                logger.debug(
                    event = "inbound_pass_aborted_offline",
                    fields = mapOf("stream" to stream.cursorKey),
                )
                return
            }
            runStream(stream, ingest, guard)
        }
        if (SyncStream.Feed in streams && isOnline()) {
            rankRefreshTrigger()
        }
        logger.info(event = "inbound_pass_done", fields = emptyMap())
    }

    private suspend fun runStream(stream: SyncStream, ingest: BundleIngest, guard: PreserveLocalGuard) {
        var sinceMarker = cursorDao.get(stream.cursorKey)?.cursor
        while (true) {
            val requestMarker = sinceMarker
            val response = fetchDelta(stream, requestMarker) ?: return  // failure already logged
            var parseFailures = 0
            for (bundle in response.bundles) {
                val result = ingest.ingest(bundle, guard)
                if (result is IngestResult.ParseFailure) {
                    parseFailures++
                    logger.debug(
                        event = "bundle_parse_failure",
                        fields = mapOf(
                            "stream" to stream.cursorKey,
                            "kind" to bundle.primary_kind,
                            "error" to (result.cause.message ?: result.cause::class.simpleName.orEmpty()),
                        ),
                    )
                } else if (result is IngestResult.UnknownKind) {
                    logger.debug(
                        event = "bundle_unknown_kind",
                        fields = mapOf("stream" to stream.cursorKey, "kind" to result.kind),
                    )
                }
            }
            if (parseFailures > 0) {
                logger.error(
                    event = "stream_parse_failed",
                    fields = mapOf(
                        "stream" to stream.cursorKey,
                        "failed" to parseFailures.toString(),
                        "count" to response.bundles.size.toString(),
                    ),
                )
                return
            }

            logger.info(
                event = "stream_page_applied",
                fields = mapOf(
                    "stream" to stream.cursorKey,
                    "count" to response.bundles.size.toString(),
                    "request_marker" to (requestMarker ?: ""),
                    "next_marker" to response.next_marker,
                    "end_of_stream" to response.end_of_stream.toString(),
                ),
            )

            if (response.end_of_stream) {
                if (response.next_marker.isNotEmpty() && response.next_marker != sinceMarker) {
                    sinceMarker = response.next_marker
                    cursorDao.upsert(stream = stream.cursorKey, cursor = sinceMarker, nowMs = nowMsProvider())
                }
                return
            }

            val nextMarker = response.next_marker
            if (nextMarker.isBlank() || nextMarker == requestMarker) {
                logger.info(
                    event = "stream_marker_stalled",
                    fields = mapOf(
                        "stream" to stream.cursorKey,
                        "request_marker" to requestMarker,
                        "next_marker" to nextMarker,
                    ),
                )
                return
            }

            sinceMarker = nextMarker
            cursorDao.upsert(stream = stream.cursorKey, cursor = sinceMarker, nowMs = nowMsProvider())
        }
    }

    /**
     * Run one API call for the given stream. Returns `null` on failure (caller bails
     * the stream; next trigger retries from the unchanged cursor).
     */
    private suspend fun fetchDelta(stream: SyncStream, since: String?): DeltaResponse? {
        var attempt = 0
        while (true) {
            try {
                return callApi(stream, since)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ResponseException) {
                logger.info(
                    event = "stream_fetch_response_error",
                    fields = mapOf(
                        "stream" to stream.cursorKey,
                        "status" to e.response.status.value.toString(),
                    ),
                )
                return null
            } catch (e: Exception) {
                val retryable = shouldRetryFetch(e)
                if (!retryable || attempt >= RETRY_DELAYS_MS.lastIndex) {
                    logger.info(
                        event = "stream_fetch_exception",
                        fields = mapOf(
                            "stream" to stream.cursorKey,
                            "error" to (e.message ?: e::class.simpleName.orEmpty()),
                            "attempt" to (attempt + 1).toString(),
                        ),
                    )
                    return null
                }
                logger.info(
                    event = "stream_fetch_retry",
                    fields = mapOf(
                        "stream" to stream.cursorKey,
                        "attempt" to (attempt + 1).toString(),
                        "error" to (e.message ?: e::class.simpleName.orEmpty()),
                    ),
                )
                delay(RETRY_DELAYS_MS[attempt])
                attempt++
            }
        }
    }

    private fun shouldRetryFetch(e: Exception): Boolean {
        if (e is IOException) return true
        val msg = e.message?.lowercase().orEmpty()
        return msg.contains("timeout") ||
            msg.contains("failed to connect") ||
            msg.contains("unable to resolve host")
    }

    private suspend fun callApi(stream: SyncStream, since: String?): DeltaResponse =
        when (stream) {
            SyncStream.Channels -> channelsApi.channelsDelta(since)
            SyncStream.Feed     -> feedApi.feedDelta(since, currentFeedCutoffMs())
            SyncStream.Shorts   -> shortsApi.shortsDelta(since)
            SyncStream.Youtube  -> videoApi.videosDelta(since)
        }

    private suspend fun currentFeedCutoffMs(): Long? {
        val days = prefs.retentionDaysFeed().first()
        if (days <= 0) return null
        val nowMs = nowMsProvider() + prefs.serverTimeOffsetMsSync()
        return nowMs - days * 86_400_000L
    }

    private fun isOnline(): Boolean = reachability.state.value is Reachability.State.Online

}
