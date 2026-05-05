package com.screwy.igloo.auth

/**
 * Narrow key-value surface used by [AuthRepo]. Production storage lives outside the
 * per-user Room DB and encrypts its state with Android Keystore; tests wire a plain
 * in-memory implementation so they don't need Keystore scaffolding.
 *
 * Auth storage deliberately lives outside the per-user Room DB so the bootstrap
 * path (resolve server URL -> authenticate -> open DB)
 * works before any DB exists.
 */
interface AuthStorage {
    fun getString(key: String): String?
    fun getLong(key: String): Long?
    fun getBoolean(key: String): Boolean?

    /** Atomic multi-value write so tokens + expiry land together or not at all. */
    fun edit(block: Editor.() -> Unit)

    /** Remove every key this storage owns. */
    fun clearAll()

    interface Editor {
        fun putString(key: String, value: String?)
        fun putLong(key: String, value: Long)
        fun putBoolean(key: String, value: Boolean)
        fun remove(key: String)
    }
}

object AuthKeys {
    const val ACCESS_TOKEN = "auth.access_token"
    const val REFRESH_TOKEN = "auth.refresh_token"
    const val ACCESS_EXPIRES_AT_MS = "auth.access_expires_at_ms"
    const val REFRESH_EXPIRES_AT_MS = "auth.refresh_expires_at_ms"
    const val USERNAME = "auth.username"
    const val IS_ADMIN = "auth.is_admin"
    const val SERVER_URL = "auth.server_url"
}

internal enum class AuthStorageValueType(
    val token: String,
) {
    STRING("s"),
    LONG("l"),
    BOOLEAN("b");

    companion object {
        fun fromToken(token: String): AuthStorageValueType? =
            entries.firstOrNull { it.token == token }
    }
}

internal data class AuthStorageKeySpec(
    val key: String,
    val type: AuthStorageValueType,
)

internal data class AuthStorageEntry(
    val key: String,
    val type: AuthStorageValueType,
    val value: String,
)

internal val authStorageKeySpecs = listOf(
    AuthStorageKeySpec(AuthKeys.ACCESS_TOKEN, AuthStorageValueType.STRING),
    AuthStorageKeySpec(AuthKeys.REFRESH_TOKEN, AuthStorageValueType.STRING),
    AuthStorageKeySpec(AuthKeys.ACCESS_EXPIRES_AT_MS, AuthStorageValueType.LONG),
    AuthStorageKeySpec(AuthKeys.REFRESH_EXPIRES_AT_MS, AuthStorageValueType.LONG),
    AuthStorageKeySpec(AuthKeys.USERNAME, AuthStorageValueType.STRING),
    AuthStorageKeySpec(AuthKeys.IS_ADMIN, AuthStorageValueType.BOOLEAN),
    AuthStorageKeySpec(AuthKeys.SERVER_URL, AuthStorageValueType.STRING),
)
