package com.screwy.igloo.data

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import java.io.File
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * DatabaseHolder lifecycle — sanitization, idempotent re-open, session detach,
 * swap on username change, close+delete removes the underlying file + WAL/SHM sidecars.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class DatabaseHolderTest {

    private lateinit var ctx: Context
    private lateinit var holder: DatabaseHolder

    @Before fun setUp() {
        ctx = ApplicationProvider.getApplicationContext()
        holder = DatabaseHolder(ctx)
    }

    @After fun tearDown() {
        holder.closeCurrent()
        ctx.deleteDatabase(IglooDatabase.fileNameFor("alice"))
        ctx.deleteDatabase(IglooDatabase.fileNameFor("bob"))
    }

    @Test fun requireCurrent_beforeLogin_throws() {
        assertNull(holder.current)
        try {
            holder.requireCurrent()
            error("expected IllegalStateException")
        } catch (e: IllegalStateException) {
            assertTrue(e.message!!.contains("Login must precede"))
        }
    }

    @Test fun openForUser_isIdempotent() {
        val a = holder.openForUser("alice")
        val b = holder.openForUser("alice")
        assertSame(a, b) // same instance
        assertEquals("alice", holder.username)
        assertEquals("alice", holder.usernameFlow.value)
    }

    @Test fun openForUser_swapsInstanceOnUserChange() {
        val a = holder.openForUser("alice")
        val b = holder.openForUser("bob")
        // Invariants: different instance, holder tracks the new one, username updated.
        // Room's `isOpen` is lazy (only flips true after the first query), so we don't
        // assert on it here.
        assertNotSame(a, b)
        assertSame(b, holder.current)
        assertEquals("bob", holder.username)
        assertEquals("bob", holder.usernameFlow.value)
    }

    @Test fun detachForLogout_hidesSessionButReattachesSameUserDb() {
        val a = holder.openForUser("alice")

        holder.detachForLogout()

        assertSame(a, holder.current)
        assertNull(holder.username)
        assertNull(holder.usernameFlow.value)

        val b = holder.openForUser("alice")

        assertSame(a, b)
        assertEquals("alice", holder.username)
        assertEquals("alice", holder.usernameFlow.value)
    }

    @Test fun closeAndDelete_removesFile() {
        holder.openForUser("alice")
        // Force a table creation round-trip so the file exists on disk.
        val dao = holder.requireCurrent().preferenceDao()
        kotlinx.coroutines.runBlocking { dao.deleteAll() }

        val dbFile = ctx.getDatabasePath(IglooDatabase.fileNameFor("alice"))
        assertTrue("expected DB file at ${dbFile.path}", dbFile.exists())

        holder.closeAndDelete("alice")
        assertNull(holder.current)
        assertNull(holder.username)
        assertNull(holder.usernameFlow.value)
        assertFalse(dbFile.exists())

        // WAL / SHM sidecars gone too
        assertFalse(File(dbFile.parentFile, dbFile.name + "-wal").exists())
        assertFalse(File(dbFile.parentFile, dbFile.name + "-shm").exists())
    }
}
