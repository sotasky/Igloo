package com.screwy.igloo.logs

/**
 * Categorical pill shown on every log card. Derived from an event's `subsystem` field
 * if present, else an event-name prefix rule. Extend cautiously — each new subsystem
 * becomes a user-visible filter chip.
 */
enum class Subsystem(val label: String) {
    App("App"),
    Sync("Sync"),
    Outbox("Outbox"),
    Media("Media"),
    Other("Other");

    companion object {
        fun fromString(value: String): Subsystem = when (value.lowercase()) {
            "app" -> App
            "sync" -> Sync
            "outbox" -> Outbox
            "media" -> Media
            else -> Other
        }
    }
}

/**
 * Pure derivation used at parse time. Explicit `subsystem` field wins; otherwise we
 * match the event-code prefix. The prefix table covers every event currently emitted
 * by Logger call sites in the codebase (grep for `event = "…"` literals).
 */
fun deriveSubsystem(event: String, fields: Map<String, String>): Subsystem {
    fields["subsystem"]?.let { return Subsystem.fromString(it) }
    return when {
        event.startsWith("outbox_")              -> Subsystem.Outbox
        event.startsWith("android_sync_")        -> Subsystem.Sync
        event.startsWith("sync_")                -> Subsystem.Sync
        event.startsWith("periodic_sync_")       -> Subsystem.Sync
        event.startsWith("media_")               -> Subsystem.Media
        event.startsWith("foreground_")          -> Subsystem.Media
        event.startsWith("cache_")               -> Subsystem.Media
        event.startsWith("android_cache_")       -> Subsystem.Media
        event == "app_start"                     -> Subsystem.App
        else -> Subsystem.Other
    }
}
