package com.screwy.igloo.auth

/**
 * What triggered [AuthRepo.logout].
 * Non-`UserInitiated` reasons surface a toast so the user knows they didn't tap Logout.
 */
enum class LogoutReason {
    UserInitiated,
    SessionRevoked,
    RefreshExpired,
    TokenReplay,
    LegacyToken,
    TokenInvalid,
    AdminForced,
}

/** Map a server `error_code` on the 401 envelope onto a [LogoutReason]. */
fun logoutReasonFor(errorCode: String?): LogoutReason = when (errorCode) {
    "session_revoked" -> LogoutReason.SessionRevoked
    "refresh_token_replayed" -> LogoutReason.TokenReplay
    "refresh_token_expired" -> LogoutReason.RefreshExpired
    "refresh_token_invalid" -> LogoutReason.RefreshExpired
    "legacy_token_invalid" -> LogoutReason.LegacyToken
    "access_token_invalid" -> LogoutReason.TokenInvalid
    else -> LogoutReason.RefreshExpired
}
