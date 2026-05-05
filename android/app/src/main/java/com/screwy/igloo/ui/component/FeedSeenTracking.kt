package com.screwy.igloo.ui.component

/**
 * Pure collector for the feed seen pipeline. It adds each id once, then emits
 * and clears the pending batch on [flush].
 */
internal class SeenBatcher(private val emit: (List<String>) -> Unit) {
    private val pending = mutableListOf<String>()
    private val seenOnce = mutableSetOf<String>()

    fun add(id: String) {
        if (seenOnce.add(id)) pending.add(id)
    }

    fun flush() {
        if (pending.isEmpty()) return
        val snapshot = pending.toList()
        pending.clear()
        emit(snapshot)
    }

    internal fun pendingSize(): Int = pending.size
}

/**
 * Tracks rows that actually moved above the viewport due to user scroll. The
 * previous row snapshot is used so local action mutations do not snowball seen
 * writes when the list updates.
 */
internal class PassedFeedRowsTracker(
    private val batcher: SeenBatcher,
) {
    private var previousRowIds: List<String> = emptyList()
    private var previousFirstVisibleIndex: Int? = null

    fun onViewportChanged(rowIds: List<String>, firstVisibleIndex: Int) {
        val previousIndex = previousFirstVisibleIndex
        val currentIndex = firstVisibleIndex.coerceAtLeast(0)

        if (previousIndex != null && currentIndex > previousIndex) {
            val upperBound = currentIndex.coerceAtMost(previousRowIds.size)
            for (index in previousIndex until upperBound) {
                previousRowIds[index]
                    .takeIf { it.isNotBlank() }
                    ?.let(batcher::add)
            }
        }

        previousRowIds = rowIds
        previousFirstVisibleIndex = currentIndex
    }
}
