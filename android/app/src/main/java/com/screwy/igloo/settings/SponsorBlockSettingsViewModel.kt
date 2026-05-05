package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.PreferencesRepo
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class SponsorBlockSettingsViewModel(
    private val prefs: PreferencesRepo,
) : ViewModel() {

    val sbSponsor: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_SPONSOR)
    val sbSelfPromo: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_SELF_PROMO)
    val sbInteraction: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_INTERACTION)
    val sbIntro: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_INTRO)
    val sbOutro: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_OUTRO)
    val sbPreview: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_PREVIEW)
    val sbFiller: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_FILLER)
    val sbMusicOffTopic: StateFlow<String> = sbFlow(PreferencesRepo.Keys.SB_MUSIC_OFFTOPIC)

    val dearrowMode: StateFlow<String> =
        prefs.dearrowMode().stateDefault(PreferencesRepo.Defaults.DEARROW_MODE)

    fun setSponsorBlock(key: String, value: String) {
        viewModelScope.launch { prefs.putString(key, value) }
    }

    fun setDearrowMode(value: String) {
        viewModelScope.launch {
            prefs.putString(
                PreferencesRepo.Keys.DEARROW_MODE,
                PreferencesRepo.Defaults.normalizeDearrowMode(value),
            )
        }
    }

    private fun sbFlow(key: String): StateFlow<String> {
        val default = SponsorBlockSettings.sbDefault(key)
        return prefs.flowString(key, default = default).stateDefault(default)
    }

    private fun <T> Flow<T>.stateDefault(initial: T): StateFlow<T> =
        stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), initial)
}
