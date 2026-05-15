package com.screwy.igloo.feed

import com.screwy.igloo.data.dao.FeedReadDao
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow

/**
 * Attach conversation context to reply leaves. Standalone rows that also appear
 * as an ancestor of another reply in this same list are dropped because they
 * will render inline inside that reply's stack. Sibling reply branches that
 * share the same oldest ancestor collapse to the first feed-ranked leaf, so the
 * feed renders one thread capsule per conversation root instead of one card per
 * branch.
 */
suspend fun attachThreadChains(
    dao: FeedReadDao,
    rows: List<FeedRow>,
): List<ThreadedFeedRow> {
    if (rows.isEmpty()) return emptyList()

    val chainsByLeaf = mutableMapOf<String, List<FeedRow>>()
    val rootIdsByLeaf = mutableMapOf<String, String>()
    val ancestorIds = mutableSetOf<String>()

    for (row in rows) {
        val item = row.item
        if (!item.isReply || item.replyToStatus.isNullOrBlank()) continue

        val chain = dao.getThreadChain(item.tweetId)
        if (chain.size <= 1) continue

        val ancestors = chain.dropLast(1)
        chainsByLeaf[item.tweetId] = ancestors
        rootIdsByLeaf[item.tweetId] = ancestors.firstOrNull()?.item?.tweetId ?: item.tweetId
        ancestorIds += ancestors.map { it.item.tweetId }
    }

    val emittedThreadRootIds = mutableSetOf<String>()
    return rows.mapNotNull { row ->
        val id = row.item.tweetId
        if (id in ancestorIds) return@mapNotNull null
        val chain = chainsByLeaf[id].orEmpty()
        if (chain.isNotEmpty()) {
            val rootId = rootIdsByLeaf[id] ?: chain.first().item.tweetId
            if (!emittedThreadRootIds.add(rootId)) return@mapNotNull null
        }
        ThreadedFeedRow(row = row, chain = chain)
    }
}
