package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.PreferencesRepo
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class PlaybackSettingsViewModel(
    private val prefs: PreferencesRepo,
) : ViewModel() {

    val autoplay: StateFlow<Boolean> =
        prefs.autoplay().stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5_000L),
            PreferencesRepo.Defaults.AUTOPLAY,
        )

    val muteDefault: StateFlow<Boolean> =
        prefs.muteDefault().stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5_000L),
            PreferencesRepo.Defaults.MUTE_DEFAULT,
        )

    val playbackSpeedDefault: StateFlow<String> =
        prefs.playbackSpeedDefault().stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5_000L),
            PreferencesRepo.Defaults.PLAYBACK_SPEED_DEFAULT,
        )

    fun setAutoplay(value: Boolean) {
        viewModelScope.launch { prefs.setAutoplay(value) }
    }

    fun setMuteDefault(value: Boolean) {
        viewModelScope.launch { prefs.setMuteDefault(value) }
    }

    fun setPlaybackSpeedDefault(value: String) {
        viewModelScope.launch { prefs.setPlaybackSpeedDefault(value) }
    }
}
