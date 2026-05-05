package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Upsert
import com.screwy.igloo.data.entity.CursorEntity
import kotlinx.coroutines.flow.Flow

/**
 * Per-stream sync markers. Read + written by the inbound reconciler (02-sync.md);
 * never touched by the UI layer. Cursor values are opaque — client only echoes
 * whatever the server last emitted.
 */
@Dao
interface CursorDao {

    @Upsert
    suspend fun upsert(row: CursorEntity)

    suspend fun upsert(stream: String, cursor: String, nowMs: Long) {
        upsert(CursorEntity(stream = stream, cursor = cursor, updatedAt = nowMs))
    }

    @Query("SELECT * FROM cursors WHERE stream = :stream")
    suspend fun get(stream: String): CursorEntity?

    @Query("SELECT * FROM cursors WHERE stream = :stream")
    fun flowByStream(stream: String): Flow<CursorEntity?>

    @Query("SELECT * FROM cursors")
    fun allFlow(): Flow<List<CursorEntity>>

    @Query("DELETE FROM cursors WHERE stream = :stream")
    suspend fun delete(stream: String)

    /** Used on logout + on schema-version mismatch to force full re-sync. */
    @Query("DELETE FROM cursors")
    suspend fun deleteAll()
}
