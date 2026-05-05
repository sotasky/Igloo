package com.screwy.igloo.media

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import com.screwy.igloo.log.Logger

private const val DRAIN_TOKEN = "__media_drain__"

/**
 * Manages foreground-service promotion while media downloads are in flight.
 * Used by Android sync to keep long media drains alive when foreground-service
 * starts are allowed.
 *
 * Thread-safe: all mutable state is protected by [lock].
 */
open class ForegroundPromoter(
    private val context: Context,
    private val logger: Logger,
) {

    private val inflight = mutableSetOf<String>()
    private val lock = Any()

    open fun startActiveDrain() {
        startDownloading(listOf(DRAIN_TOKEN))
    }

    open fun finishActiveDrain() {
        finishedBatch(listOf(DRAIN_TOKEN))
    }

    /**
     * Record [assetIds] as in-flight and start the foreground service if this is
     * the first in-flight entry.
     */
    open fun startDownloading(assetIds: Collection<String>) {
        synchronized(lock) {
            val wasEmpty = inflight.isEmpty()
            inflight.addAll(assetIds)
            if (wasEmpty && inflight.isNotEmpty()) {
                logger.debug(
                    event = "media_foreground_service_start",
                    fields = mapOf("count" to inflight.size.toString()),
                )
                val intent = Intent(context, MediaForegroundService::class.java)
                try {
                    ContextCompat.startForegroundService(context, intent)
                } catch (e: Exception) {
                    logger.info(
                        event = "media_foreground_service_start_skipped",
                        fields = mapOf(
                            "count" to inflight.size.toString(),
                            "class" to (e::class.simpleName ?: "Exception"),
                            "error" to (e.message ?: e::class.simpleName.orEmpty()),
                        ),
                    )
                }
            }
        }
    }

    /**
     * Remove [assetIds] from the in-flight set. Stops the foreground service
     * once the set is empty.
     */
    open fun finishedBatch(assetIds: Collection<String>) {
        synchronized(lock) {
            inflight.removeAll(assetIds.toSet())
            if (inflight.isEmpty()) {
                logger.debug(
                    event = "media_foreground_service_stop",
                    fields = emptyMap(),
                )
                try {
                    context.stopService(Intent(context, MediaForegroundService::class.java))
                } catch (e: Exception) {
                    logger.info(
                        event = "media_foreground_service_stop_skipped",
                        fields = mapOf(
                            "class" to (e::class.simpleName ?: "Exception"),
                            "error" to (e.message ?: e::class.simpleName.orEmpty()),
                        ),
                    )
                }
            }
        }
    }

}
