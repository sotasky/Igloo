package com.screwy.igloo.outbox

import java.util.UUID

sealed class OutboxKind {

    abstract val code: String
    abstract val itemId: String?
    open val field: String? = null
    open val coalesceKey: CoalesceKey
        get() = CoalesceKey.ByKindItemField

    /** All kind codes in one place so string literals stay here and not in recipes. */
    companion object {
        const val CODE_LIKE = "like"
        const val CODE_BOOKMARK = "bookmark"
        const val CODE_FOLLOW = "follow"
        const val CODE_STAR = "star"
        const val CODE_MUTE = "mute"
        const val CODE_CHANNEL_SETTING = "channel_setting"
        const val CODE_SEEN = "seen"
        const val CODE_MOMENT_VIEW = "moment_view"
        const val CODE_PROGRESS = "progress"
        const val CODE_MOMENTS_CURSOR = "moments_cursor"
        const val CODE_CREATE_CATEGORY = "create_category"
        const val CODE_LOG = "log"
        const val CODE_LOG_DEBUG = "log_debug"
    }

    enum class Action(val wire: String) {
        Set("set"),
        Clear("clear"),
    }

    enum class CoalesceKey {
        ByKindItemField,
        Fifo,
    }

    // ─── Toggle kinds — (kind, item_id) coalesce, `Set`/`Clear` action ─────────

    data class Like(val tweetId: String, val action: Action) : OutboxKind() {
        override val code = CODE_LIKE
        override val itemId: String = tweetId
    }

    data class Bookmark(
        val videoId: String,
        val action: Action,
        val categoryId: Long? = null,
        val customTitle: String? = null,
        val accountHandles: String? = null,
        val mediaIndices: String? = null,
    ) : OutboxKind() {
        override val code = CODE_BOOKMARK
        override val itemId: String = videoId
    }

    data class Follow(val channelId: String, val action: Action) : OutboxKind() {
        override val code = CODE_FOLLOW
        override val itemId: String = channelId
    }

    data class Star(val channelId: String, val action: Action) : OutboxKind() {
        override val code = CODE_STAR
        override val itemId: String = channelId
    }

    data class Mute(val channelId: String, val action: Action) : OutboxKind() {
        override val code = CODE_MUTE
        override val itemId: String = channelId
    }

    // ─── Per-field coalesce ────────────────────────────────────────────────────

    /** Per-channel setting change. `value = null` means inherit the global value. */
    data class ChannelSetting(val channelId: String, val settingField: String, val value: Long?) :
        OutboxKind() {
        override val code = CODE_CHANNEL_SETTING
        override val itemId: String = channelId
        override val field: String = settingField
    }

    // ─── Fire-and-forget / FIFO kinds ───────────────────────────────────────────

    data class Seen(val tweetId: String) : OutboxKind() {
        override val code = CODE_SEEN
        override val itemId: String = tweetId
    }

    data class MomentView(val videoId: String) : OutboxKind() {
        override val code = CODE_MOMENT_VIEW
        override val itemId: String = videoId
    }

    /**
     * Playback progress. Coalesces on `(kind, item_id)` so rapid writes collapse — LWW by
     * server-time is enforced by the later writer overwriting the prior row.
     */
    data class Progress(val videoId: String, val position: Double, val duration: Double) :
        OutboxKind() {
        override val code = CODE_PROGRESS
        override val itemId: String = videoId
    }

    /** One row per moments tab scope. Missing legacy scope maps to `all`. */
    data class MomentsCursor(
        val videoId: String,
        val positionMs: Long,
        val scope: String = "all",
        val sortAtMs: Long? = null,
    ) : OutboxKind() {
        override val code = CODE_MOMENTS_CURSOR
        override val itemId: String = scope
    }

    /**
     * Create a new bookmark category. `provisionalId` is the negative local ID used to stitch
     * `bookmarks.category_id`; the server's ACK carries the real positive ID and the dispatcher
     * runs a cascading update.
     */
    data class CreateCategory(
        val name: String,
        val provisionalId: Long,
        val requestId: String = UUID.randomUUID().toString(),
    ) : OutboxKind() {
        override val code = CODE_CREATE_CATEGORY
        override val itemId: String = provisionalId.toString()
        override val coalesceKey = CoalesceKey.Fifo
    }

    // ─── Log kinds (ride the same queue) ───────────────────────────────────────

    data class Log(
        val level: String, // "info" | "error"
        val event: String,
        val fields: Map<String, String>,
        val timestampMs: Long,
    ) : OutboxKind() {
        override val code = CODE_LOG
        override val itemId: String? = null
        override val coalesceKey = CoalesceKey.Fifo
    }

    data class LogDebug(val event: String, val fields: Map<String, String>, val timestampMs: Long) :
        OutboxKind() {
        override val code = CODE_LOG_DEBUG
        override val itemId: String? = null
        override val coalesceKey = CoalesceKey.Fifo
    }
}
