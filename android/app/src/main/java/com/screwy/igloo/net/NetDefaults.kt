package com.screwy.igloo.net

import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/** HTTP timeout defaults and the shared public request fingerprint. */
object NetDefaults {

    /**
     * Keep public-origin media/image requests inside a broad browser-shaped crowd.
     *
     * Firefox ESR 140.10.0 is current as of 2026-04-21; the UA exposes the ESR major
     * version because Firefox user agents do not include the ESR patch train.
     */
    const val PUBLIC_BROWSER_USER_AGENT =
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0"

    const val USER_AGENT = PUBLIC_BROWSER_USER_AGENT

    /** Default envelope timeout. */
    val DEFAULT_REQUEST_TIMEOUT: Duration = 15.seconds
    val DEFAULT_CONNECT_TIMEOUT: Duration = 10.seconds
    val DEFAULT_SOCKET_TIMEOUT: Duration = 15.seconds

    /** Health probe timeout. */
    val PROBE_REQUEST_TIMEOUT: Duration = 5.seconds

    /** Optional per-host overrides. Prefer the shared public UA unless a host forces a different one. */
    val UA_OVERRIDES: Map<String, String> = emptyMap()
}
