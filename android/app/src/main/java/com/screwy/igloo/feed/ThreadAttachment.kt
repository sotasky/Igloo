package com.screwy.igloo.feed

import com.screwy.igloo.data.dao.FeedReadDao
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow

/**
 * Attach server-owned conversation context to feed leaves. Standalone rows that
 * also appear as an ancestor of another row in this same list are dropped
 * because they will render inline inside that row's stack. Sibling reply
 * branches that share the same oldest ancestor collapse to the first feed-ranked
 * leaf, so the feed renders one thread capsule per conversation root instead of
 * one card per branch.
 */
suspend fun attachThreadChains(
    dao: FeedReadDao,
    rows: List<FeedRow>,
): List<ThreadedFeedRow> {
    if (rows.isEmpty()) return emptyList()

    val contextsByLeaf = dao.getThreadContexts(rows.map { it.item.tweetId })
        .groupBy { it.leafTweetId }
        .mapValues { (_, contexts) -> contexts.sortedBy { it.ancestorOrder } }
    if (contextsByLeaf.isEmpty()) {
        return rows.map { ThreadedFeedRow(row = it, chain = emptyList()) }
    }

    val ancestorRowsById = dao.getFeedRowsByTweetIds(
        contextsByLeaf.values
            .flatten()
            .map { it.ancestorTweetId }
            .distinct(),
    ).associateBy { it.item.tweetId }

    val chainsByLeaf = mutableMapOf<String, List<FeedRow>>()
    val rootIdsByLeaf = mutableMapOf<String, String>()
    val ancestorIds = mutableSetOf<String>()

    for ((leafTweetId, contexts) in contextsByLeaf) {
        if (contexts.isEmpty()) continue
        val chain = contexts.mapNotNull { ancestorRowsById[it.ancestorTweetId] }
        if (chain.size != contexts.size) continue
        chainsByLeaf[leafTweetId] = chain
        rootIdsByLeaf[leafTweetId] = contexts.firstOrNull()?.rootTweetId?.takeIf { it.isNotBlank() }
            ?: chain.firstOrNull()?.item?.tweetId
            ?: leafTweetId
        ancestorIds += chain.map { it.item.tweetId }
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
