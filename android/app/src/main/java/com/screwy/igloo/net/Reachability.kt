package com.screwy.igloo.net

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Server-specific reachability state machine.
 *
 * "Online" here means the Igloo server answered a probe — NOT `ConnectivityManager`
 * level connectivity. Wi-Fi connected while home server is down = Offline.
 *
 * Probe cadence by state:
 *  - Foreground + Offline: loops every 30s hitting `probe()`.
 *  - Foreground + Online:  passive — drain/sync call failures invoke `downgrade()`.
 *  - Background:           no active probing (WorkManager ticks serve as the probe).
 *
 * Kept loosely coupled: `probe` and `foregroundFlow` are passed in so tests can
 * substitute. The production wiring injects `HealthApi::health` + a `ProcessLifecycleOwner`
 * adapter.
 */
class Reachability(
    private val scope: CoroutineScope,
    private val probe: suspend () -> Boolean,
    private val foregroundFlow: kotlinx.coroutines.flow.Flow<Boolean>,
    private val offlineProbeIntervalMs: Long = 30_000L,
) {

    sealed interface State {
        data object Online : State
        data object Offline : State
        data object Unknown : State
    }

    private val _state = MutableStateFlow<State>(State.Unknown)
    val state: StateFlow<State> = _state.asStateFlow()

    private var probeJob: Job? = null
    private var supervisorJob: Job? = null
    @Volatile private var foreground: Boolean = false

    /** Start the state machine. Idempotent — calling twice is a no-op. */
    fun start() {
        if (supervisorJob?.isActive == true) return
        supervisorJob = scope.launch {
            foregroundFlow.distinctUntilChanged().collect { inForeground ->
                foreground = inForeground
                if (inForeground) {
                    // Immediate probe on foreground, then enter/exit the loop based on
                    // the result.
                    val ok = runCatching { probe() }.getOrDefault(false)
                    setState(if (ok) State.Online else State.Offline)
                    if (!ok) startOfflineLoop() else stopOfflineLoop()
                } else {
                    // Background: stop active probing. State retained so reconcilers
                    // can read `Online`/`Offline` last-known value.
                    stopOfflineLoop()
                }
            }
        }
    }

    /**
     * Hook called by interceptors / reconcilers when an HTTP call fails with a
     * network-level error while the client was Online. Lets the state transition to
     * Offline without waiting for the 30s probe loop to catch up.
     */
    fun downgrade() {
        if (_state.value is State.Offline) return
        setState(State.Offline)
        if (foreground) startOfflineLoop()
    }

    /** Called by the envelope parser on a successful 2xx — soft upgrade. */
    fun markOnline() {
        if (_state.value is State.Online) return
        setState(State.Online)
        stopOfflineLoop()
    }

    private fun setState(next: State) {
        _state.value = next
    }

    private fun startOfflineLoop() {
        if (probeJob?.isActive == true) return
        probeJob = scope.launch {
            while (isActive) {
                delay(offlineProbeIntervalMs)
                val ok = runCatching { probe() }.getOrDefault(false)
                if (ok) {
                    setState(State.Online)
                    break
                }
            }
        }
    }

    private fun stopOfflineLoop() {
        probeJob?.cancel()
        probeJob = null
    }
}
