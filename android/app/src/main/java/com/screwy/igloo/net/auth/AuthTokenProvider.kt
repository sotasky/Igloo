package com.screwy.igloo.net.auth

/**
 * Narrow sync-read surface for the bearer-token cache. The `AuthInterceptor` calls
 * `bearerTokenSync()` on every request bound for the Igloo server; `AuthRepo` backs
 * this with an in-memory mirror of auth storage so the hot path does not touch disk.
 *
 * `NoAuthTokenProvider` is the default before `AuthRepo` overrides the Koin binding —
 * it returns `null` for every read and never refreshes.
 */
interface AuthTokenProvider {
    fun bearerTokenSync(): String?

    /**
     * Called by `AuthInterceptor` on a 401 response, while the interceptor holds its
     * refresh mutex. `errorCode` is the `error_code` field on the 401 envelope (or
     * `null` when absent / unparseable).
     *
     * Contract:
     *  - Return a fresh access token → interceptor retries the original request with it.
     *  - Return `null` → interceptor propagates the original 401 to the caller. In this
     *    case the implementation has already decided the auth state is terminal and has
     *    fired `logout` + `UiEffect.RequireLogin`.
     *
     * Implementations branch internally on `errorCode`:
     *   `access_token_expired` (or unknown / missing) → rotate tokens via
     *   `/api/auth/refresh`; terminal codes (`session_revoked`, `refresh_token_*`,
     *   `legacy_token_invalid`, `access_token_invalid`) → logout immediately.
     */
    suspend fun onAuthExpired(errorCode: String?): String? = null
}

/** Default used before login/auth wiring is available. */
object NoAuthTokenProvider : AuthTokenProvider {
    override fun bearerTokenSync(): String? = null
}
