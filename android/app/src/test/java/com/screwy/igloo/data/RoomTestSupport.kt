package com.screwy.igloo.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider

/**
 * In-memory `IglooDatabase` for JVM unit tests. Run via Robolectric so Android's
 * SQLiteOpenHelper (which Room relies on) loads successfully.
 *
 * Each call creates a fresh database — tests share no state.
 */
object RoomTestSupport {
    fun freshDb(): IglooDatabase {
        val ctx: Context = ApplicationProvider.getApplicationContext()
        return Room.inMemoryDatabaseBuilder(ctx, IglooDatabase::class.java)
            .allowMainThreadQueries()
            .build()
    }
}
