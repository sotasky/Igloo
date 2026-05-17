package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.auth.AccountSessionActions
import com.screwy.igloo.auth.LogoutReason
import com.screwy.igloo.data.PreferencesRepo
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class AccountSettingsViewModel(
    private val prefs: PreferencesRepo,
    private val authRepo: AccountSessionActions,
) : ViewModel() {

    val serverUrl: StateFlow<String> =
        prefs.serverUrl().stateDefault(PreferencesRepo.Defaults.SERVER_URL)

    fun setServerUrl(value: String) {
        viewModelScope.launch {
            prefs.setServerUrl(value)
            authRepo.updateServerUrl(value)
        }
    }

    fun logout() {
        viewModelScope.launch { authRepo.logout(LogoutReason.UserInitiated) }
    }

    private fun <T> Flow<T>.stateDefault(initial: T): StateFlow<T> =
        stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), initial)
}
