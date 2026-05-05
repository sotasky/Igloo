package com.screwy.igloo.sync

import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.outbox.OutboxKind

/**
 * Preserve-local filter.
 *
 * When a delta delivers a side-table row (e.g., `feed_likes`), we skip the write if a
 * matching pending outbox entry exists — the user clicked like, we optimistically
 * updated locally, the server hasn't yet seen the mutation, and the delta is showing
 * us the pre-mutation view. Applying it would clobber the optimistic write until the
 * outbox drains.
 *
 * Main-row upserts (feed_items, videos, channels) bypass this guard because
 * content is server-authoritative.
 *
 * Lookups run against `idx_outbox_coalesce` and are cheap. Each guard instance is
 * single-use, scoped to one bundle-ingest call; its `blocked` map caches lookups
 * within the call so the same `(kind, item_id)` isn't queried twice in one pass.
 */
class PreserveLocalGuard(private val outboxDao: OutboxDao) {

    private val cache = HashMap<Key, Boolean>()

    private data class Key(val kind: String, val itemId: String?, val field: String?)

    /**
     * `true` → a pending outbox row claims this side-table slot; delta write must skip.
     * `false` → server's view is authoritative (no pending local mutation).
     */
    suspend fun isPending(kind: String, itemId: String?, field: String? = null): Boolean {
        val key = Key(kind, itemId, field)
        cache[key]?.let { return it }
        val hit = outboxDao.hasPending(kind, itemId, field)
        cache[key] = hit
        return hit
    }

    // Convenience accessors.

    suspend fun likePending(tweetId: String) = isPending(OutboxKind.CODE_LIKE, tweetId)
    suspend fun bookmarkPending(videoId: String) = isPending(OutboxKind.CODE_BOOKMARK, videoId)
    suspend fun seenPending(tweetId: String) = isPending(OutboxKind.CODE_SEEN, tweetId)
    suspend fun followPending(channelId: String) = isPending(OutboxKind.CODE_FOLLOW, channelId)
    suspend fun starPending(channelId: String) = isPending(OutboxKind.CODE_STAR, channelId)
    suspend fun mutePending(handle: String) = isPending(OutboxKind.CODE_MUTE, handle)
    suspend fun momentViewPending(videoId: String) = isPending(OutboxKind.CODE_MOMENT_VIEW, videoId)
    suspend fun progressPending(videoId: String) = isPending(OutboxKind.CODE_PROGRESS, videoId)
    suspend fun channelSettingPending(channelId: String, field: String) =
        isPending(OutboxKind.CODE_CHANNEL_SETTING, channelId, field)
}
