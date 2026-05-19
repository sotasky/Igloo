package com.screwy.igloo.logs

/**
 * Seeded translations from machine event codes to human sentences. Additions welcome —
 * every event produced by a `Logger.info/error/debug` call that a user might see is
 * fair game.
 *
 * Lookup is a plain Map; missing keys → caller renders the raw event code as the
 * message body (LogRowCard).
 */
object EventDictionary {

    operator fun get(event: String): EventTemplate? = templates[event]

    private val templates: Map<String, EventTemplate> = mapOf(
        // ─── App lifecycle ────────────────────────────────────────────
        "app_start" to EventTemplate("App started"),

        // ─── Sync ─────────────────────────────────────────────────────
        "periodic_sync_triggered" to EventTemplate("Periodic sync triggered"),
        "mutation_delta_page_applied" to EventTemplate(
            "Applied {count} mutations from server",
            expandFields = listOf("request_marker", "next_marker", "truncated"),
        ),
        "mutation_delta_skipped_offline" to EventTemplate("Skipped mutation delta — device is offline"),
        "mutation_delta_sync_failed" to EventTemplate(
            "Mutation delta sync failed",
            expandFields = listOf("error", "stack"),
        ),
        "mutation_delta_unknown_type" to EventTemplate("Unknown mutation type — ignored"),
        "inbound_pass_start"            to EventTemplate("Inbound sync pass starting"),
        "inbound_pass_done"             to EventTemplate("Inbound sync pass complete"),
        "inbound_pass_skipped_offline"  to EventTemplate("Inbound pass skipped — device is offline"),
        "inbound_pass_aborted_offline"  to EventTemplate("Inbound pass aborted — went offline mid-run"),
        "inbound_reconciler_unhandled"  to EventTemplate(
            "Inbound reconciler crashed",
            expandFields = listOf("class", "error", "stack"),
        ),
        "stream_page_applied"           to EventTemplate("Applied stream page ({count} items)"),
        "stream_fetch_exception"        to EventTemplate(
            "Stream fetch failed",
            expandFields = listOf("class", "error"),
        ),
        "stream_fetch_response_error"   to EventTemplate(
            "Stream fetch returned error ({status})",
            expandFields = listOf("status"),
        ),
        "stream_fetch_retry"            to EventTemplate("Stream fetch retry scheduled"),
        "stream_marker_stalled"         to EventTemplate("Stream marker stalled — bailed out"),
        "stream_parse_failed"           to EventTemplate("Stream entry failed to parse"),
        "stream_all_parses_failed"      to EventTemplate("Every stream entry failed to parse"),
        "bundle_parse_failure"          to EventTemplate("Bundle payload parse failed"),
        "bundle_unknown_kind"           to EventTemplate("Bundle had unknown kind — ignored"),
        "retention_replay_reset"        to EventTemplate("Retention replay reset"),
        "retention_prune_refresh"       to EventTemplate("Retention prune refresh"),
        "android_sync_orphan_asset_files_pruned" to EventTemplate("Pruned orphan Android sync media files"),

        // ─── Outbox ───────────────────────────────────────────────────
        "outbox_drain_skipped_offline" to EventTemplate("Skipped drain — device is offline"),
        "outbox_drain_unhandled"       to EventTemplate(
            "Outbox drain crashed",
            expandFields = listOf("class", "error", "stack"),
        ),
        "outbox_row_auth_refresh" to EventTemplate(
            "Refreshed auth before posting row #{id}",
            expandFields = listOf("kind"),
        ),
        "outbox_row_retry" to EventTemplate(
            "Retrying outbox row #{id}",
            expandFields = listOf("kind", "attempt", "error"),
        ),
        "outbox_row_dead" to EventTemplate(
            "Outbox row #{id} gave up — marked dead",
            expandFields = listOf("kind", "error"),
        ),
        "outbox_ttl_gc_dead"           to EventTemplate("Dropped {count} dead outbox rows past TTL"),
        "outbox_debug_backlog_dropped" to EventTemplate("Dropped {count} old debug rows from outbox"),

        // ─── Media ────────────────────────────────────────────────────
        "cache_cleared" to EventTemplate("Cleared {bucket|human} cache"),
        "media_foreground_service_start"         to EventTemplate("Media foreground service started"),
        "media_foreground_service_stop"          to EventTemplate("Media foreground service stopped"),
        "media_foreground_service_start_skipped" to EventTemplate("Foreground service skipped — nothing pending"),
        "moments_player_prepare_slot"            to EventTemplate(
            "Moments player prepared slot {slot} for {video_id}",
            expandFields = listOf("page", "stream_kind", "current_media_id", "player_state", "player_position_ms"),
        ),
        "moments_player_keep_active_source"      to EventTemplate(
            "Moments player kept active source for {video_id}",
            expandFields = listOf("page", "slot", "stream_kind", "current_media_id", "player_state"),
        ),
        "moments_player_clear_missing_stream"    to EventTemplate(
            "Moments player cleared slot {slot} because stream is missing",
            expandFields = listOf("page", "video_id", "current_media_id", "player_state"),
        ),
        "moments_player_current_stream_missing"  to EventTemplate(
            "Current Moments page has no stream yet",
            expandFields = listOf("page", "slot", "video_id", "player_state"),
        ),
        "moments_player_current_slot_mismatch"   to EventTemplate(
            "Current Moments slot points at a different video",
            expandFields = listOf("page", "slot", "video_id", "current_media_id", "player_state"),
        ),
        "moments_player_surface_reject_recycled_media" to EventTemplate(
            "Moments player rejected a recycled video surface",
            expandFields = listOf("reason", "expected_media_id", "current_media_id", "player_state", "player_position_ms"),
        ),
        "moments_player_surface_waiting"         to EventTemplate(
            "Moments player surface is waiting for video readiness",
            expandFields = listOf("reason", "expected_media_id", "player_state", "video_width", "video_height", "wait_duration_ms"),
        ),
        "moments_player_surface_ready"           to EventTemplate(
            "Moments player surface became ready",
            expandFields = listOf("reason", "expected_media_id", "player_state", "video_width", "video_height", "wait_duration_ms"),
        ),
        "moments_player_first_frame"             to EventTemplate(
            "Moments player rendered first frame",
            expandFields = listOf("expected_media_id", "player_state", "video_width", "video_height", "wait_duration_ms"),
        ),
        "moments_player_visible_wait_start"     to EventTemplate(
            "Moments player visible wait started",
            expandFields = listOf("page", "video_id", "stream_kind", "current_media_id", "player_state", "player_position_ms"),
        ),
        "moments_player_visible_ready"          to EventTemplate(
            "Moments player became ready after visible wait",
            expandFields = listOf("page", "video_id", "visible_wait_ms", "current_media_id", "player_state", "player_position_ms", "rendered_frame_count"),
        ),
        "moments_player_visible_first_frame"    to EventTemplate(
            "Moments player rendered first frame after visible wait",
            expandFields = listOf("page", "video_id", "visible_wait_ms", "current_media_id", "player_state", "player_position_ms", "rendered_frame_count"),
        ),
        "moments_player_visible_playing"        to EventTemplate(
            "Moments player started playback after visible wait",
            expandFields = listOf("page", "video_id", "visible_wait_ms", "current_media_id", "player_state", "player_position_ms", "surface_player_is_playing"),
        ),
        "moments_player_visible_playback_advanced" to EventTemplate(
            "Moments player playback advanced after visible wait",
            expandFields = listOf("page", "video_id", "visible_wait_ms", "current_media_id", "player_state", "player_position_ms", "surface_player_position_ms", "rendered_frame_count"),
        ),
        "moments_player_visible_delay_detected" to EventTemplate(
            "Moments player is still waiting for visible playback",
            expandFields = listOf("page", "video_id", "visible_wait_ms", "threshold_ms", "current_media_id", "player_state", "player_position_ms", "rendered_frame_count"),
        ),
        "moments_player_video_freeze_detected" to EventTemplate(
            "Moments player video frames stalled while playback advanced",
            expandFields = listOf("page", "video_id", "current_media_id", "player_state", "player_position_ms", "frame_count", "last_frame_age_ms", "position_since_last_frame_ms"),
        ),
    )
}
