package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.PreferencesRepo
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class FeedSettingsViewModel(
    private val prefs: PreferencesRepo,
) : ViewModel() {

    val includeReposts: StateFlow<Boolean> =
        prefs.flowBool(PreferencesRepo.Keys.INCLUDE_REPOSTS_DEFAULT, default = true)
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), true)

    val mediaOnly: StateFlow<Boolean> =
        prefs.flowBool(PreferencesRepo.Keys.MEDIA_ONLY_DEFAULT, default = false)
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), false)

    fun setIncludeReposts(value: Boolean) {
        viewModelScope.launch { prefs.putBool(PreferencesRepo.Keys.INCLUDE_REPOSTS_DEFAULT, value) }
    }

    fun setMediaOnly(value: Boolean) {
        viewModelScope.launch { prefs.putBool(PreferencesRepo.Keys.MEDIA_ONLY_DEFAULT, value) }
    }
}
