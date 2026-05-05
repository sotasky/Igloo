package com.screwy.igloo.sync

import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.outbox.OutboxDrain
import com.screwy.igloo.outbox.OutboxWriter
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Sync fan-out orchestrator.
 *
 * Owns the long-lived coroutines for `InboundReconciler`, `OutboxDrain`,
 * `AndroidSyncMirror`, and stitches `OutboxWriter`'s debounced drain signal
 * into the drain loop. Trigger fan-out:
 *  - `triggerAll()`              — app launch, foreground resume, WorkManager tick.
 *  - `triggerStream(stream)`     — pull-to-refresh scoped to one screen's stream.
 *  - reachability upgrade        — drain, inbound, and Sync fire immediately.
 *  - outbox writer debounce      — 3s collecting window → drain signal.
 *
 * Android sync owns media downloads.
 */
class Scheduler(
    private val scope: CoroutineScope,
    private val inbound: InboundReconciler,
    private val outbox: OutboxDrain,
    private val androidSync: AndroidSyncMirror,
    private val retentionReplay: RetentionReplayCoordinator,
    private val reachability: Reachability,
    private val foregroundFlow: Flow<Boolean>,
    private val writer: OutboxWriter,
    private val mutationDelta: MutationDeltaSync,
    private val logger: Logger,
) {

    private val running = mutableListOf<Job>()
    private val pendingLock = Any()
    private val pendingAfterOutbox = PendingAfterOutbox()

    /** Start sync workers + the reachability-upgrade signal. Idempotent. */
    fun start() {
        if (running.any { it.isActive }) return
        running.clear()

        outbox.wireWriter(writer)
        retentionReplay.start()

        running += scope.launch { inbound.run() }
        running += scope.launch { outbox.run() }
        running += scope.launch { androidSync.run() }
        running += scope.launch { observeOutboxCompletion() }
        running += scope.launch { observeForegroundStarts() }
        running += scope.launch { observeMutationPolling() }
        running += scope.launch { observeReachabilityUpgrades() }
        androidSync.trigger()
    }

    /**
     * Cancel every sync job and the reachability collector. Called by
     * `AuthRepo.logout` before the per-user DB is closed so no reconciler is
     * mid-query on a connection that's about to go away.
     */
    fun stopAll() {
        retentionReplay.stop()
        running.forEach { it.cancel() }
        running.clear()
    }

    /** Queue a full ordered sync cycle: outbox first, then inbound, then media. */
    fun triggerAll() {
        requestAfterOutbox(
            TriggerEvent.Inbound(SyncStream.ALL),
            TriggerEvent.MutationDelta,
        )
        androidSync.trigger()
        outbox.trigger()
    }

    /** Queue a scoped ordered sync cycle: outbox first, then the requested stream(s). */
    fun triggerStream(stream: SyncStream) {
        requestAfterOutbox(
            TriggerEvent.Inbound(setOf(stream)),
            TriggerEvent.MutationDelta,
        )
        outbox.trigger()
    }

    private suspend fun observeForegroundStarts() {
        foregroundFlow.collect { inForeground ->
            if (inForeground) {
                triggerAll()
            }
        }
    }

    /**
     * Reachability upgrade (offline → online) triggers outbox drain + inbound sync.
     * Sync is also poked independently so generation import and asset drain keep
     * moving even if one inbound stream times out.
     */
    private suspend fun observeReachabilityUpgrades() {
        var previous = reachability.state.value
        reachability.state.collect { current ->
            val upgraded = previous !is Reachability.State.Online && current is Reachability.State.Online
            previous = current
            if (upgraded) {
                requestAfterOutbox(
                    TriggerEvent.Inbound(SyncStream.ALL),
                    TriggerEvent.MutationDelta,
                )
                androidSync.trigger()
                outbox.trigger()
            }
        }
    }

    private suspend fun observeOutboxCompletion() {
        outbox.passCompleted.collect {
            val request = synchronized(pendingLock) { pendingAfterOutbox.drain() }

            if (request.runMutationDelta) runMutationDelta()
            if (request.inboundStreams.isEmpty()) return@collect

            androidSync.trigger()
            if (request.inboundStreams == SyncStream.ALL) {
                inbound.trigger()
            } else {
                inbound.triggerStreams(request.inboundStreams)
            }
        }
    }

    private fun requestAfterOutbox(vararg events: TriggerEvent) {
        synchronized(pendingLock) {
            events.forEach(pendingAfterOutbox::record)
        }
    }

    private suspend fun observeMutationPolling() = coroutineScope {
        var pollingJob: Job? = null
        try {
            foregroundFlow.collect { inForeground ->
                pollingJob?.cancel()
                pollingJob = null
                if (inForeground) {
                    pollingJob = launch {
                        while (isActive) {
                            runMutationDelta()
                            delay(MUTATION_DELTA_POLL_MS)
                        }
                    }
                }
            }
        } finally {
            pollingJob?.cancel()
        }
    }

    private suspend fun runMutationDelta() {
        try {
            val result = mutationDelta.sync()
            if (result.rankAffecting) {
                androidSync.trigger()
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            logger.info(
                event = "mutation_delta_sync_failed",
                fields = mapOf("error" to (e.message ?: e::class.simpleName.orEmpty())),
            )
        }
    }

    private companion object {
        const val MUTATION_DELTA_POLL_MS = 5_000L
    }

    private sealed class TriggerEvent {
        data class Inbound(val streams: Set<SyncStream>) : TriggerEvent()
        object MutationDelta : TriggerEvent()
    }

    private data class PendingRequest(
        val inboundStreams: Set<SyncStream>,
        val runMutationDelta: Boolean,
    )

    private class PendingAfterOutbox {
        private val inboundStreams = linkedSetOf<SyncStream>()
        private var runMutationDelta = false

        fun record(event: TriggerEvent) {
            when (event) {
                is TriggerEvent.Inbound -> mergeInbound(event.streams)
                TriggerEvent.MutationDelta -> runMutationDelta = true
            }
        }

        fun drain(): PendingRequest {
            val request = PendingRequest(
                inboundStreams = inboundStreams.toSet(),
                runMutationDelta = runMutationDelta,
            )
            inboundStreams.clear()
            runMutationDelta = false
            return request
        }

        private fun mergeInbound(streams: Set<SyncStream>) {
            if (streams == SyncStream.ALL) {
                inboundStreams.clear()
                inboundStreams.addAll(SyncStream.ALL)
            } else if (inboundStreams != SyncStream.ALL) {
                inboundStreams.addAll(streams)
            }
        }
    }
}
