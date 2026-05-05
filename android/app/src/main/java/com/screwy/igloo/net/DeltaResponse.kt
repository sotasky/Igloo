package com.screwy.igloo.net

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject

/**
 * Wire envelope for every bundle-delta endpoint: feed, shorts, videos, and
 * channels. `primary` and `attachments` stay as `JsonObject` because each stream
 * has distinct content shapes; the inbound reconciler deserializes them by
 * `primary_kind`.
 */
@Serializable
data class DeltaResponse(
    val bundles: List<BundleEnvelope> = emptyList(),
    val next_marker: String = "",
    val end_of_stream: Boolean = false,
    val ok: Boolean = true,
    val sync_version: Long? = null,
    val sync_stream: String? = null,
    val server_time_ms: Long? = null,
)

@Serializable
data class BundleEnvelope(
    val primary_kind: String,
    val primary: JsonObject,
    val attachments: JsonObject? = null,
)
