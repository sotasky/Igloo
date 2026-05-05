package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Muted accounts list state. Backs `MutedAccountsRoute`.
 *
 * Lists handles from the `muted_accounts` side table; unmute enqueues an outbox
 * `Mute(Clear)`. Writer's local-apply deletes the row, so the flow re-emits
 * without the handle immediately.
 */
class MutedAccountsViewModel(
    db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
) : ViewModel() {

    val handles: StateFlow<List<String>> = db.mutedAccountDao().allFlow()
        .map { rows -> rows.map { it.handle } }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    /** Count of muted handles — used by the Feed sub-screen's trailing value. */
    val count: StateFlow<Int> = handles
        .map { it.size }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = 0,
        )

    fun unmute(handle: String) {
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Mute(handle = handle, action = OutboxKind.Action.Clear))
        }
    }

    fun clearAll() {
        val current = handles.value
        viewModelScope.launch {
            current.forEach { handle ->
                outboxWriter.enqueue(OutboxKind.Mute(handle = handle, action = OutboxKind.Action.Clear))
            }
        }
    }
}
