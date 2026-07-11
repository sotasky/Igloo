package com.screwy.igloo.net

/**
 * Sync host-resolver lambda used by the HTTP client. Backed by `AuthRepo.serverHostSync()`
 * in production so the HTTP client can be resolved before login.
 *
 * Kept as a thin wrapper (and not inlined into `AuthRepo`) so call-sites that don't need
 * the full auth surface still get a narrow host-only dependency.
 */
class IglooHostProvider(
    private val hostSource: () -> String,
) {
    /** Lowercase host (no port). Empty string if the server URL isn't parseable. */
    fun hostSync(): String = hostSource()
}
