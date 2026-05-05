package com.screwy.igloo.auth

import android.content.Context
import com.screwy.igloo.R
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.net.AuthApi
import com.screwy.igloo.net.LoginResponse
import com.screwy.igloo.net.RefreshResponse
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Source of truth for auth state: tokens, username, server URL. Implements
 * [AuthTokenProvider] so the HTTP stack reads the current bearer token sync via
 * [bearerTokenSync]; an in-memory `StateFlow` mirrors [AuthStorage]
 * so reads stay off the disk hot path.
 *
 * Drives the login / logout / refresh flows:
 *
 *  - `login` — POSTs `/api/auth/login`, persists tokens + server URL + username, opens
 *    the per-user Room DB, kicks off post-login bootstrap (reachability / scheduler /
 *    first log emit) on the supplied callback.
 *  - `logout` — fire-and-forget `/api/auth/logout`, stops the scheduler, closes + deletes
 *    the per-user DB, wipes the per-user media dir, clears auth keys except the server
 *    URL. Surfaces a reason toast when non-user-initiated.
 *  - `onAuthExpired` — 401 handshake. Branches on envelope `error_code`: refresh-eligible
 *    (`access_token_expired`, null) → rotates tokens via `/api/auth/refresh`; terminal
 *    (`session_revoked`, `refresh_token_*`, `legacy_token_invalid`, `access_token_invalid`)
 *    → logout with the matching reason + emit `UiEffect.RequireLogin`.
 *
 * `authApiProvider` is a lambda (resolved lazily on first access) to break the Koin
 * cycle: `AuthRepo → AuthApi → HttpClient → AuthTokenProvider (AuthRepo)`.
 */
class AuthRepo(
    private val context: Context,
    private val storage: AuthStorage,
    private val databaseHolder: DatabaseHolder,
    private val uiEffects: UiEffects,
    private val applicationScope: CoroutineScope,
    authApiProvider: () -> AuthApi,
    private val stopReconcilersOnLogout: () -> Unit = {},
    private val prefsUpdater: suspend (serverUrl: String) -> Unit = {},
    private val onPostLoginBootstrap: () -> Unit = {},
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) : AuthTokenProvider {

    private val authApi: AuthApi by lazy(authApiProvider)

    private val accessTokenCache = MutableStateFlow(storage.getString(AuthKeys.ACCESS_TOKEN))
    private val refreshTokenCache = MutableStateFlow(storage.getString(AuthKeys.REFRESH_TOKEN))
    private val usernameCache = MutableStateFlow(storage.getString(AuthKeys.USERNAME))
    private val isAdminCache = MutableStateFlow(storage.getBoolean(AuthKeys.IS_ADMIN) ?: false)
    private val serverUrlCache = MutableStateFlow(
        storage.getString(AuthKeys.SERVER_URL) ?: PreferencesRepo.Defaults.SERVER_URL,
    )

    val accessTokenFlow: StateFlow<String?> = accessTokenCache.asStateFlow()
    val usernameFlow: StateFlow<String?> = usernameCache.asStateFlow()
    val serverUrlFlow: StateFlow<String> = serverUrlCache.asStateFlow()

    // ─── Sync accessors (hot-path reads) ─────────────────────────────────────

    override fun bearerTokenSync(): String? = accessTokenCache.value
    fun accessTokenSync(): String? = accessTokenCache.value
    fun refreshTokenSync(): String? = refreshTokenCache.value
    fun usernameSync(): String? = usernameCache.value
    fun isAdminSync(): Boolean = isAdminCache.value
    fun serverUrlSync(): String = serverUrlCache.value
    fun serverHostSync(): String =
        runCatching { java.net.URI(serverUrlCache.value).host?.lowercase() ?: "" }.getOrDefault("")

    /** True when we have a usable access token that hasn't expired. */
    fun isLoggedInSync(): Boolean {
        val token = accessTokenCache.value ?: return false
        if (token.isBlank()) return false
        val expiry = storage.getLong(AuthKeys.ACCESS_EXPIRES_AT_MS) ?: return true
        return expiry > nowMsProvider()
    }

    /**
     * True when enough persisted state exists to open the local per-user database even
     * if the short-lived access token needs a background refresh.
     */
    fun hasRestorableSessionSync(): Boolean {
        if (usernameCache.value.isNullOrBlank()) return false
        if (refreshTokenCache.value.isNullOrBlank()) return false
        val refreshExpiry = storage.getLong(AuthKeys.REFRESH_EXPIRES_AT_MS) ?: return true
        return refreshExpiry > nowMsProvider()
    }

    fun canOpenLocalSessionSync(): Boolean =
        isLoggedInSync() || hasRestorableSessionSync()

    // ─── Login ───────────────────────────────────────────────────────────────

    sealed class LoginResult {
        data object Success : LoginResult()
        data object BadCredentials : LoginResult()
        data object NetworkError : LoginResult()
        data class ServerError(val message: String) : LoginResult()
    }

    /**
     * Normalizes + persists the server URL, POSTs `/api/auth/login`, persists tokens,
     * opens the per-user Room DB, triggers post-login bootstrap, and mirrors the URL
     * into Room `preferences`. Returns a classified [LoginResult]; the login screen
     * translates to inline error text.
     */
    suspend fun login(serverUrl: String, username: String, password: String): LoginResult {
        val normalized = normalizeServerUrl(serverUrl)
        persistServerUrl(normalized)

        val response = try {
            authApi.login(username, password)
        } catch (e: io.ktor.client.plugins.ClientRequestException) {
            return if (e.response.status.value == 401) LoginResult.BadCredentials
            else LoginResult.ServerError("server ${e.response.status.value}")
        } catch (e: io.ktor.client.plugins.ServerResponseException) {
            return LoginResult.ServerError("server ${e.response.status.value}")
        } catch (e: Throwable) {
            return LoginResult.NetworkError
        }

        persistLoginTokens(response)
        databaseHolder.openForUser(username)
        // Mirror the URL into the now-open Room `preferences` table so Settings
        // can display + edit it. Failure here is non-fatal (auth storage is the
        // bootstrap source of truth).
        applicationScope.launch { runCatching { prefsUpdater(normalized) } }
        onPostLoginBootstrap()

        return LoginResult.Success
    }

    // ─── Logout ──────────────────────────────────────────────────────────────

    /**
     * Clears user-scoped state in the order specified by `06-auth-and-multiuser.md` §5:
     * fire-and-forget server revoke → stop scheduler → close + delete DB → delete media
     * dir → clear auth keys while retaining the server URL. Safe to call from any
     * thread; DB mutation happens on IO.
     */
    suspend fun logout(reason: LogoutReason = LogoutReason.UserInitiated) {
        val refresh = refreshTokenCache.value
        val username = usernameCache.value
        val retainedServerUrl = serverUrlCache.value

        if (refresh != null) {
            applicationScope.launch { runCatching { authApi.logout(refresh) } }
        }

        stopReconcilersOnLogout()

        if (username != null) {
            databaseHolder.closeAndDelete(username)
            deleteUserMediaDir(username)
        }
        storage.clearAll()
        persistServerUrl(retainedServerUrl)
        accessTokenCache.value = null
        refreshTokenCache.value = null
        usernameCache.value = null
        isAdminCache.value = false

        surfaceLogoutToast(reason)
    }

    private fun surfaceLogoutToast(reason: LogoutReason) {
        if (reason == LogoutReason.UserInitiated) return
        val messageRes = when (reason) {
            LogoutReason.SessionRevoked -> R.string.auth_signed_out_session_ended
            LogoutReason.RefreshExpired -> R.string.auth_signed_out_sign_in_again
            LogoutReason.TokenReplay -> R.string.auth_signed_out_security_event
            LogoutReason.LegacyToken -> R.string.auth_signed_out_sign_in_again
            LogoutReason.TokenInvalid -> R.string.auth_signed_out_sign_in_again
            LogoutReason.AdminForced -> R.string.auth_signed_out_by_admin
            LogoutReason.UserInitiated -> return
        }
        uiEffects.emit(UiEffect.ToastRes(resId = messageRes, longDuration = true))
    }

    private fun deleteUserMediaDir(username: String) {
        val sanitized = slug(username)
        val dir = File(context.applicationContext.filesDir, "users/$sanitized/media")
        runCatching { dir.deleteRecursively() }
    }

    // ─── Refresh / 401 handshake ─────────────────────────────────────────────

    /**
     * Called by `AuthInterceptor` on a 401. Mutex-serialization lives in the interceptor
     * (it also checks whether another caller already refreshed under the same lock); this
     * method assumes it holds the lock and just makes the refresh-vs-logout decision.
     */
    override suspend fun onAuthExpired(errorCode: String?): String? {
        if (errorCode != null && errorCode != "access_token_expired") {
            logout(logoutReasonFor(errorCode))
            uiEffects.emit(UiEffect.RequireLogin)
            return null
        }
        return refreshOrFail()
    }

    /**
     * Pre-emptively refreshes when the current access token is within
     * [earlyRefreshWindowMs] of expiry. Callers (e.g. `IglooApp.bootstrapPostLogin`) fire
     * this on app launch so the reconciler's first HTTP call doesn't waste a round-trip
     * on a guaranteed-401.
     */
    suspend fun onAppStart(earlyRefreshWindowMs: Long = 5 * 60_000L) {
        if (accessTokenCache.value == null) return
        val expiry = storage.getLong(AuthKeys.ACCESS_EXPIRES_AT_MS) ?: return
        if (expiry - nowMsProvider() > earlyRefreshWindowMs) return
        refreshOrFail()
    }

    /**
     * Single refresh attempt. On success: rotates tokens in cache + storage. Terminal
     * refresh-token rejection logs out; transient server/network failures preserve the
     * local session so offline state remains available.
     */
    private suspend fun refreshOrFail(): String? {
        val refresh = refreshTokenCache.value ?: run {
            logout(LogoutReason.RefreshExpired)
            uiEffects.emit(UiEffect.RequireLogin)
            return null
        }
        val response: RefreshResponse = try {
            authApi.refresh(refresh)
        } catch (e: io.ktor.client.plugins.ClientRequestException) {
            if (e.response.status == io.ktor.http.HttpStatusCode.Unauthorized) {
                logout(LogoutReason.RefreshExpired)
                uiEffects.emit(UiEffect.RequireLogin)
            }
            return null
        } catch (e: io.ktor.client.plugins.ServerResponseException) {
            return null
        } catch (t: Throwable) {
            return null
        }
        persistRefreshTokens(response)
        return response.access_token
    }

    // ─── Persistence helpers ─────────────────────────────────────────────────

    /**
     * Public entry point for non-login server-URL updates (Settings dialog). Normalizes,
     * persists into [AuthStorage] (backing store the HTTP stack reads), and refreshes
     * [serverUrlCache] so new requests use the new base URL on the next call — no
     * app restart required.
     */
    fun updateServerUrl(value: String) {
        persistServerUrl(normalizeServerUrl(value))
    }

    private fun persistServerUrl(url: String) {
        storage.edit { putString(AuthKeys.SERVER_URL, url) }
        serverUrlCache.value = url
    }

    private fun persistLoginTokens(response: LoginResponse) {
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, response.access_token)
            putString(AuthKeys.REFRESH_TOKEN, response.refresh_token)
            putLong(AuthKeys.ACCESS_EXPIRES_AT_MS, response.access_expires_at_ms)
            putLong(AuthKeys.REFRESH_EXPIRES_AT_MS, response.refresh_expires_at_ms)
            putString(AuthKeys.USERNAME, response.username)
            putBoolean(AuthKeys.IS_ADMIN, response.is_admin)
        }
        accessTokenCache.value = response.access_token
        refreshTokenCache.value = response.refresh_token
        usernameCache.value = response.username
        isAdminCache.value = response.is_admin
    }

    private fun persistRefreshTokens(response: RefreshResponse) {
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, response.access_token)
            putString(AuthKeys.REFRESH_TOKEN, response.refresh_token)
            putLong(AuthKeys.ACCESS_EXPIRES_AT_MS, response.access_expires_at_ms)
            putLong(AuthKeys.REFRESH_EXPIRES_AT_MS, response.refresh_expires_at_ms)
        }
        accessTokenCache.value = response.access_token
        refreshTokenCache.value = response.refresh_token
    }

    companion object {
        /**
         * Strip trailing slashes, default to `https` when the user omits the scheme. Matches
         * `06-auth-and-multiuser.md` §4's "normalize server URL" step.
         */
        fun normalizeServerUrl(raw: String): String {
            val trimmed = raw.trim().trimEnd('/')
            if (trimmed.isEmpty()) return ""
            return if (trimmed.startsWith("http://") || trimmed.startsWith("https://")) trimmed
            else "https://$trimmed"
        }

        private fun slug(username: String): String = buildString(username.length) {
            for (c in username) {
                append(
                    if (c.isLetterOrDigit() || c == '.' || c == '_' || c == '-') c.lowercaseChar()
                    else '_',
                )
            }
        }.ifEmpty { "anonymous" }
    }
}
