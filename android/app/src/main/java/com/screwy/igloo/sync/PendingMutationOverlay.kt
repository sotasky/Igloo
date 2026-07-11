package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.outbox.applyOptimisticMutation

class PendingMutationOverlay private constructor(
    private val rows: List<OutboxEntity>,
) {
    suspend fun restore(db: IglooDatabase) {
        rows.forEach { applyOptimisticMutation(db, it) }
    }

    companion object {
        fun capture(rows: List<OutboxEntity>) = PendingMutationOverlay(rows)
    }
}
