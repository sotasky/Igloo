package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Upsert
import com.screwy.igloo.data.entity.PreferenceEntity
import kotlinx.coroutines.flow.Flow

/**
 * Raw key-value DAO for the `preferences` table.
 * PreferencesRepo (data/PreferencesRepo.kt) wraps this with typed accessors +
 * Flow/StateFlow caches for hot-path gating.
 */
@Dao
interface PreferenceDao {

    @Query("SELECT * FROM preferences WHERE `key` = :key")
    fun flowByKey(key: String): Flow<PreferenceEntity?>

    @Query("SELECT value FROM preferences WHERE `key` = :key")
    suspend fun getValue(key: String): String?

    @Upsert
    suspend fun upsert(row: PreferenceEntity)

    /** Suspend helper: write a string value + bump `updated_at`. */
    suspend fun put(key: String, value: String?, nowMs: Long) {
        upsert(PreferenceEntity(key = key, value = value, updatedAt = nowMs))
    }

    @Query("DELETE FROM preferences WHERE `key` = :key")
    suspend fun delete(key: String)

    @Query("DELETE FROM preferences")
    suspend fun deleteAll()

    @Query("SELECT * FROM preferences")
    fun allFlow(): Flow<List<PreferenceEntity>>
}
