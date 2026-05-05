package com.screwy.igloo.feed

import com.screwy.igloo.data.dao.FeedReadDao
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow

/**
 * Attach conversation context to reply leaves. Standalone rows that also appear
 * as an ancestor of another reply in this same list are dropped because they
 * will render inline inside that reply's stack.
 */
suspend fun attachThreadChains(
    dao: FeedReadDao,
    rows: List<FeedRow>,
): List<ThreadedFeedRow> {
    if (rows.isEmpty()) return emptyList()

    val chainsByLeaf = mutableMapOf<String, List<FeedRow>>()
    val ancestorIds = mutableSetOf<String>()

    for (row in rows) {
        val item = row.item
        if (!item.isReply || item.replyToStatus.isNullOrBlank()) continue

        val chain = dao.getThreadChain(item.tweetId)
        if (chain.size <= 1) continue

        val ancestors = chain.dropLast(1)
        chainsByLeaf[item.tweetId] = ancestors
        ancestorIds += ancestors.map { it.item.tweetId }
    }

    return rows.mapNotNull { row ->
        val id = row.item.tweetId
        if (id in ancestorIds) return@mapNotNull null
        ThreadedFeedRow(row = row, chain = chainsByLeaf[id].orEmpty())
    }
}
