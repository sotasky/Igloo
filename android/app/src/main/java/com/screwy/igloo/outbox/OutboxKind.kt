package com.screwy.igloo.outbox

/**
 * Typed mutation + log queue entries.
 *
 * The sealed hierarchy gives the dispatcher a compile-time enumerable kind set — a new
 * mutation is one new subclass + one recipe in `OutboxDispatcher` + one enqueue site.
 * No stringly-typed dispatch.
 *
 * Each kind carries:
 *  - its payload (typed fields) — serialized to JSON via the per-kind recipe's `body`.
 *  - `code`       — the string stored in `outbox.kind`, matched against preserve-local
 *                   and the coalesce key. Kept on a companion so tests reach it without
 *                   instantiating the kind.
 *  - `itemId`/`field` — the two secondary keys the coalesce index consults.
 *                       `null` means "no match on this axis."
 *  - `coalesceKey` — whether an enqueue should DELETE prior pending rows with the same
 *                    (code, itemId, field) before INSERT. `FIFO` means no coalesce.
 *
 * User-state side-table mutations (`Like`, `Bookmark`, `Follow`, `Star`, `Mute`) take
 * an `Action` discriminator so set and clear share one typed row shape.
 */
sealed class OutboxKind {

    abstract val code: String
    abstract val itemId: String?
    open val field: String? = null
    open val coalesceKey: CoalesceKey get() = CoalesceKey.ByKindItemField

    /** All kind codes in one place so string literals stay here and not in recipes. */
    companion object {
        const val CODE_LIKE              = "like"
        const val CODE_BOOKMARK          = "bookmark"
        const val CODE_FOLLOW            = "follow"
        const val CODE_STAR              = "star"
        const val CODE_MUTE              = "mute"
        const val CODE_CHANNEL_SETTING   = "channel_setting"
        const val CODE_SEEN              = "seen"
        const val CODE_MOMENT_VIEW       = "moment_view"
        const val CODE_PROGRESS          = "progress"
        const val CODE_MOMENTS_CURSOR    = "moments_cursor"
        const val CODE_CREATE_CATEGORY   = "create_category"
        const val CODE_BOOKMARK_CATEGORY = "bookmark_category"
        const val CODE_BOOKMARK_ALIAS    = "bookmark_alias"
        const val CODE_LOG               = "log"
        const val CODE_LOG_DEBUG         = "log_debug"
    }

    enum class Action(val wire: String) {
        Set("set"),
        Clear("clear"),
    }

    /**
     * Coalescing rule. `ByKindItemField` is the catch-all shape
     * (matches against `(kind, item_id, field)`). `Fifo` skips coalesce — used for
     * events that stack (seen, log, moment_view, ...). `Singleton` coalesces on kind
     * alone for future global-only mutations.
     */
    enum class CoalesceKey {
        ByKindItemField,
        Fifo,
        Singleton,
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
        val prevRow: BookmarkPreImage? = null,
    ) : OutboxKind() {
        override val code = CODE_BOOKMARK
        override val itemId: String = videoId
    }

    /** Snapshot captured at enqueue time so dispatcher can rollback without re-reading. */
    data class BookmarkPreImage(
        val existed: Boolean,
        val categoryId: Long = 0,
        val customTitle: String? = null,
        val accountHandles: String? = null,
        val mediaIndices: String? = null,
        val bookmarkedAt: Long = 0,
    )

    data class Follow(val channelId: String, val action: Action) : OutboxKind() {
        override val code = CODE_FOLLOW
        override val itemId: String = channelId
    }

    data class Star(val channelId: String, val action: Action) : OutboxKind() {
        override val code = CODE_STAR
        override val itemId: String = channelId
    }

    data class Mute(val handle: String, val action: Action) : OutboxKind() {
        override val code = CODE_MUTE
        override val itemId: String = handle
    }

    // ─── Per-field coalesce ────────────────────────────────────────────────────

    /**
     * Per-channel setting change. `settingField` is one of `media_only`,
     * `include_reposts`, `media_download_limit`, `max_videos`, `download_subtitles`.
     * `value = null` is the inherit-from-global sentinel.
     * `prevValue` captured so rollback restores pre-image without re-reading.
     */
    data class ChannelSetting(
        val channelId: String,
        val settingField: String,
        val value: Long?,
        val prevValue: Long?,
        val prevExisted: Boolean,
    ) : OutboxKind() {
        override val code = CODE_CHANNEL_SETTING
        override val itemId: String = channelId
        override val field: String = settingField
    }

    // ─── Fire-and-forget / FIFO kinds ───────────────────────────────────────────

    data class Seen(val tweetId: String) : OutboxKind() {
        override val code = CODE_SEEN
        override val itemId: String = tweetId
        override val coalesceKey = CoalesceKey.Fifo
    }

    data class MomentView(val videoId: String) : OutboxKind() {
        override val code = CODE_MOMENT_VIEW
        override val itemId: String = videoId
        override val coalesceKey = CoalesceKey.Fifo
    }

    /**
     * Playback progress. Coalesces on `(kind, item_id)` so rapid writes collapse —
     * LWW by server-time is enforced by the later writer overwriting the prior row.
     */
    data class Progress(
        val videoId: String,
        val position: Double,
        val duration: Double,
        val source: String = "android",
    ) : OutboxKind() {
        override val code = CODE_PROGRESS
        override val itemId: String = videoId
    }

    /** One row per moments tab scope. Missing legacy scope maps to `all`. */
    data class MomentsCursor(
        val videoId: String,
        val positionMs: Long,
        val scope: String = "all",
    ) : OutboxKind() {
        override val code = CODE_MOMENTS_CURSOR
        override val itemId: String = scope
    }

    /**
     * Create a new bookmark category. `provisionalId` is the negative local ID used
     * to stitch `bookmarks.category_id`; the server's ACK carries the real positive
     * ID and the dispatcher runs a cascading update.
     */
    data class CreateCategory(
        val name: String,
        val provisionalId: Long,
    ) : OutboxKind() {
        override val code = CODE_CREATE_CATEGORY
        override val itemId: String = provisionalId.toString()
        override val coalesceKey = CoalesceKey.Fifo
    }

    // ─── Log kinds (ride the same queue) ───────────────────────────────────────

    data class Log(
        val level: String,               // "info" | "error"
        val event: String,
        val fields: Map<String, String>,
        val timestampMs: Long,
    ) : OutboxKind() {
        override val code = CODE_LOG
        override val itemId: String? = null
        override val coalesceKey = CoalesceKey.Fifo
    }

    data class LogDebug(
        val event: String,
        val fields: Map<String, String>,
        val timestampMs: Long,
    ) : OutboxKind() {
        override val code = CODE_LOG_DEBUG
        override val itemId: String? = null
        override val coalesceKey = CoalesceKey.Fifo
    }
}
