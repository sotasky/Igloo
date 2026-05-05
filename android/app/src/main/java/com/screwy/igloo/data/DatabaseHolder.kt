package com.screwy.igloo.data

import android.content.Context
import java.io.File

/**
 * Single that wraps the currently-open per-user `IglooDatabase`.
 *
 * Single-user today, multi-user-ready shape. Login calls `openForUser(username)`; logout
 * calls `closeAndDelete(username)`. DAO accessors resolve through `requireCurrent()` so
 * every call site picks up the post-swap instance transparently.
 *
 * Thread-safe: state changes happen under `lock`; reads after the instance is published
 * are plain volatile reads on `current`. Intended use is single-writer (login/logout) +
 * many-readers (DAO resolution).
 */
class DatabaseHolder(
    private val context: Context,
) {

    private val appContext = context.applicationContext
    private val lock = Any()

    @Volatile
    private var currentInstance: IglooDatabase? = null
    @Volatile
    private var currentUsername: String? = null

    val current: IglooDatabase? get() = currentInstance

    /** Currently-opened username, or null when logged out. */
    val username: String? get() = currentUsername

    fun requireCurrent(): IglooDatabase =
        currentInstance
            ?: error("IglooDatabase not opened. Login must precede DAO access; call openForUser() first.")

    /**
     * Open (or re-open) the DB for `username`. If a different user's DB is already open,
     * close it first. Idempotent when the same user is already open.
     */
    fun openForUser(username: String): IglooDatabase = synchronized(lock) {
        val existing = currentInstance
        if (existing != null && currentUsername == username) return existing

        existing?.close()
        val db = IglooDatabase.buildForUser(appContext, username)
        currentInstance = db
        currentUsername = username
        db
    }

    /**
     * Close the current DB instance + delete the underlying file (+ WAL/SHM sidecars).
     * Safe to call when nothing is open — no-op in that case.
     */
    fun closeAndDelete(username: String): Unit = synchronized(lock) {
        val inst = currentInstance
        if (inst != null) {
            inst.close()
        }
        currentInstance = null
        currentUsername = null

        val dbDir = File(appContext.getDatabasePath(IglooDatabase.fileNameFor(username)).parent ?: "")
        if (!dbDir.isDirectory) return
        val base = IglooDatabase.fileNameFor(username)
        // Room uses `<name>`, `<name>-wal`, `<name>-shm` (and sometimes a `-journal` sidecar).
        listOf(base, "$base-wal", "$base-shm", "$base-journal").forEach { name ->
            File(dbDir, name).takeIf { it.exists() }?.delete()
        }
    }

    /** Close without deleting — used at process shutdown. */
    fun closeCurrent(): Unit = synchronized(lock) {
        currentInstance?.close()
        currentInstance = null
        currentUsername = null
    }
}
