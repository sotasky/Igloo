package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.MutedChannelDisplay
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class MutedAccountsViewModel(
    db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
) : ViewModel() {

    val channels: StateFlow<List<MutedChannelDisplay>> = db.mutedChannelDao().displayFlow()
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val count: StateFlow<Int> = channels
        .map { it.size }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = 0,
        )

    fun unmute(channelId: String) {
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Mute(channelId = channelId, action = OutboxKind.Action.Clear))
        }
    }

    fun clearAll() {
        val current = channels.value
        viewModelScope.launch {
            current.forEach { channel ->
                outboxWriter.enqueue(
                    OutboxKind.Mute(channelId = channel.muted.channelId, action = OutboxKind.Action.Clear),
                )
            }
        }
    }
}
