package com.screwy.igloo.auth

import androidx.annotation.StringRes
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.R
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * State + input holder for `LoginRoute`. Delegates the actual auth dance to [AuthRepo];
 * translates [AuthRepo.LoginResult] into inline-error text per `07-ui-design-system.md`
 * §4 (login spec).
 *
 * Server URL pre-fills from `AuthRepo.serverUrlSync()`. Public builds leave it blank
 * unless `DEFAULT_SERVER_URL` is supplied at build time.
 */
class LoginViewModel(
    private val authRepo: AuthRepo,
    private val onLoginSuccess: () -> Unit = {},
) : ViewModel() {

    data class UiState(
        val serverUrl: String = "",
        val username: String = "",
        val password: String = "",
        val status: Status = Status.Idle,
    ) {
        val submitEnabled: Boolean
            get() = status != Status.Loading &&
                serverUrl.isNotBlank() && username.isNotBlank() && password.isNotEmpty()
    }

    sealed class Status {
        data object Idle : Status()
        data object Loading : Status()
        data class Error(@param:StringRes val resId: Int) : Status()
    }

    private val _state = MutableStateFlow(UiState(serverUrl = authRepo.serverUrlSync()))
    val state: StateFlow<UiState> = _state.asStateFlow()

    fun onServerUrlChange(value: String) {
        _state.update { it.copy(serverUrl = value, status = clearErrorOnEdit(it.status)) }
    }

    fun onUsernameChange(value: String) {
        _state.update { it.copy(username = value, status = clearErrorOnEdit(it.status)) }
    }

    fun onPasswordChange(value: String) {
        _state.update { it.copy(password = value, status = clearErrorOnEdit(it.status)) }
    }

    fun onSubmit() {
        val snapshot = _state.value
        if (!snapshot.submitEnabled) return
        _state.update { it.copy(status = Status.Loading) }
        viewModelScope.launch {
            val result = authRepo.login(
                serverUrl = snapshot.serverUrl,
                username = snapshot.username.trim(),
                password = snapshot.password,
            )
            when (result) {
                is AuthRepo.LoginResult.Success -> {
                    _state.update { it.copy(status = Status.Idle, password = "") }
                    onLoginSuccess()
                }
                is AuthRepo.LoginResult.BadCredentials ->
                    _state.update { it.copy(status = Status.Error(R.string.login_error_invalid_credentials)) }
                is AuthRepo.LoginResult.NetworkError ->
                    _state.update { it.copy(status = Status.Error(R.string.login_error_reach_server)) }
                is AuthRepo.LoginResult.ServerError ->
                    _state.update { it.copy(status = Status.Error(R.string.login_error_server_try_again)) }
            }
        }
    }

    private fun clearErrorOnEdit(current: Status): Status =
        if (current is Status.Error) Status.Idle else current
}
