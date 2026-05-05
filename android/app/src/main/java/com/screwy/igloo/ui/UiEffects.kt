package com.screwy.igloo.ui

import androidx.annotation.StringRes
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow

/**
 * One-way event bus from non-UI layers (reconcilers, outbox dispatcher, auth) to the
 * Compose layer. `AppNavHost` owns route-level navigation/login effects; scaffolded
 * routes collect snackbar/dialog effects through `MainScaffold`.
 *
 *  - `replay = 0` — new subscribers don't see past effects.
 *  - `extraBufferCapacity = 16` — covers bursts (e.g., multiple rollbacks per drain).
 *  - `DROP_OLDEST` on overflow so a stuck subscriber can't wedge the emitter.
 *  - `emit` uses `tryEmit` so non-suspend callers (interceptors, reconcilers) stay
 *    non-blocking.
 */
sealed interface UiEffect {
    data class Toast(val message: String, val longDuration: Boolean = false) : UiEffect
    data class ToastRes(
        @param:StringRes val resId: Int,
        val formatArgs: List<Any> = emptyList(),
        val longDuration: Boolean = false,
    ) : UiEffect
    data class DialogError(val title: String, val body: String) : UiEffect
    data class NavigateTo(val route: String) : UiEffect
    data object RequireLogin : UiEffect
}

class UiEffects {

    private val _flow = MutableSharedFlow<UiEffect>(
        replay = 0,
        extraBufferCapacity = 16,
        onBufferOverflow = BufferOverflow.DROP_OLDEST,
    )

    val flow: SharedFlow<UiEffect> = _flow.asSharedFlow()

    /** Non-suspending fire-and-forget. Returns `true` if buffered, `false` if dropped. */
    fun emit(effect: UiEffect): Boolean = _flow.tryEmit(effect)
}
