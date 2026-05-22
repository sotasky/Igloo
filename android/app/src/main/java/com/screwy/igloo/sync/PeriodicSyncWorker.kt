package com.screwy.igloo.sync

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.ListenableWorker
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.screwy.igloo.AppRuntime
import com.screwy.igloo.R
import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.log.Logger
import com.screwy.igloo.perf.PerfProbe
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import org.koin.core.context.GlobalContext
import java.util.concurrent.TimeUnit

internal data class PeriodicSyncDrainStatus(
    val generationId: String?,
    val incompleteImports: Int,
    val pendingAssets: Int,
    val runnerWork: Boolean,
) {
    val remainingWork: Int
        get() = incompleteImports + pendingAssets + if (runnerWork) 1 else 0
}

internal data class PeriodicSyncDrainResult(
    val completed: Boolean,
    val stalled: Boolean,
    val elapsedMs: Long,
    val remainingWork: Int,
    val incompleteImports: Int,
    val pendingAssets: Int,
    val runnerWork: Boolean,
)

internal suspend fun preparePeriodicSyncSession(
    databaseHolder: DatabaseHolder,
    authRepo: AuthRepo,
): Boolean {
    if (databaseHolder.current == null) {
        if (!authRepo.canOpenLocalSessionSync()) return false
        val username = authRepo.usernameSync()?.takeIf { it.isNotBlank() } ?: return false
        databaseHolder.openForUser(username)
    }

    authRepo.onAppStart()
    return databaseHolder.current != null && authRepo.canOpenLocalSessionSync()
}

internal suspend fun awaitSyncDrainOrCap(
    maxRunDurationMs: Long,
    pollIntervalMs: Long,
    startupGraceMs: Long,
    idleStreakRequired: Int,
    nowMs: () -> Long = { System.currentTimeMillis() },
    delayMs: suspend (Long) -> Unit = { delay(it) },
    statusProvider: suspend (Long) -> PeriodicSyncDrainStatus,
): PeriodicSyncDrainResult {
    val startedMs = nowMs()
    val deadline = startedMs + maxRunDurationMs
    var idleStreak = 0
    var stalledStreak = 0
    var sawGeneration = false
    var completed = false
    var last = PeriodicSyncDrainStatus(
        generationId = null,
        incompleteImports = 0,
        pendingAssets = 0,
        runnerWork = false,
    )
    while (nowMs() < deadline) {
        delayMs(pollIntervalMs)
        val now = nowMs()
        last = statusProvider(now)
        if (last.generationId != null) sawGeneration = true
        if (!sawGeneration && now - startedMs < startupGraceMs) {
            idleStreak = 0
            continue
        }
        if (last.remainingWork == 0) {
            stalledStreak = 0
            if (++idleStreak >= idleStreakRequired) {
                completed = true
                break
            }
        } else {
            idleStreak = 0
            if (!last.runnerWork && ++stalledStreak >= idleStreakRequired) {
                return PeriodicSyncDrainResult(
                    completed = false,
                    stalled = true,
                    elapsedMs = nowMs() - startedMs,
                    remainingWork = last.remainingWork,
                    incompleteImports = last.incompleteImports,
                    pendingAssets = last.pendingAssets,
                    runnerWork = last.runnerWork,
                )
            } else if (last.runnerWork) {
                stalledStreak = 0
            }
        }
    }
    return PeriodicSyncDrainResult(
        completed = completed,
        stalled = false,
        elapsedMs = nowMs() - startedMs,
        remainingWork = last.remainingWork,
        incompleteImports = last.incompleteImports,
        pendingAssets = last.pendingAssets,
        runnerWork = last.runnerWork,
    )
}

/**
 * Periodic catch-up trigger.
 *
 * Calls `Scheduler.triggerAll()` and then **stays alive** (promoted to a
 * foreground service via `setForeground`) while Android sync imports the generation
 * and drains verified assets. Without that promotion the worker hits the OS's
 * ~10-minute background-execution cap and the mirror it kicked off can get torn
 * down with the worker process, so backgrounded apps can never catch up after a
 * long sleep.
 *
 * Interval governed by `preferences.sync_interval_minutes` (default 30). Network
 * constraint derived from `sync_wifi_only` — UNMETERED when the pref is true,
 * CONNECTED otherwise. WorkManager enforces a 15-minute floor on periodic workers;
 * smaller values are silently clamped.
 */
class PeriodicSyncWorker(
    appContext: Context,
    params: WorkerParameters,
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): ListenableWorker.Result {
        return try {
            PerfProbe.timedSuspend(event = "workmanager_catchup_do_work") {
                AppRuntime.ensureStarted(applicationContext as Application)
            }
            val koin = GlobalContext.get()
            val databaseHolder: DatabaseHolder = koin.get()
            val authRepo: AuthRepo = koin.get()
            val prepared = PerfProbe.timedSuspend(event = "workmanager_prepare_session") {
                preparePeriodicSyncSession(databaseHolder, authRepo)
            }
            if (!prepared) {
                PerfProbe.log(event = "workmanager_catchup_done") { mapOf("prepared" to false) }
                return ListenableWorker.Result.success()
            }

            val prefs: PreferencesRepo = koin.get()
            val scheduler: Scheduler = koin.get()
            val logger: Logger = koin.get()
            val syncDao: AndroidSyncDao = koin.get()
            val androidSync: AndroidSyncMirror = koin.get()
            if (!prefs.syncEnabled().first()) {
                cancel(applicationContext)
                logger.info(event = "periodic_sync_disabled", fields = emptyMap())
                PerfProbe.log(event = "workmanager_catchup_done") {
                    mapOf("prepared" to true, "enabled" to false)
                }
                return ListenableWorker.Result.success()
            }

            // Promote to foreground BEFORE kicking the scheduler so the
            // foreground-service token covers the entire drain — including the
            // MediaForegroundService start that ForegroundPromoter would
            // otherwise block on under Android 12+ background-start rules.
            runCatching {
                PerfProbe.timedSuspend(event = "workmanager_set_foreground") {
                    setForeground(getForegroundInfo())
                }
            }
                .onFailure { error ->
                    logger.info(
                        event = "periodic_sync_foreground_failed",
                        fields = mapOf(
                            "class" to (error::class.simpleName ?: "Exception"),
                            "error" to (error.message ?: error::class.simpleName.orEmpty()),
                        ),
                    )
                }

            PerfProbe.timed(event = "workmanager_scheduler_trigger") {
                scheduler.start()
                scheduler.triggerAll()
            }
            logger.info(event = "periodic_sync_triggered", fields = emptyMap())

            val drain = PerfProbe.timedSuspend(event = "workmanager_await_drain") {
                awaitDrainOrCap(syncDao, androidSync, logger)
            }
            PerfProbe.log(
                event = "workmanager_catchup_done",
            ) {
                mapOf(
                    "prepared" to true,
                    "completed" to drain.completed,
                    "stalled" to drain.stalled,
                    "elapsed_ms" to drain.elapsedMs,
                    "remaining_work" to drain.remainingWork,
                )
            }
            // Periodic work has already made its bounded best effort by this point.
            // Returning retry hands scheduling to WorkManager backoff, which can push
            // the next run well past the user configured sync interval.
            ListenableWorker.Result.success()
        } catch (e: Exception) {
            ListenableWorker.Result.retry()
        }
    }

    private suspend fun awaitDrainOrCap(
        syncDao: AndroidSyncDao,
        androidSync: AndroidSyncMirror,
        logger: Logger,
    ): PeriodicSyncDrainResult {
        val result = awaitSyncDrainOrCap(
            maxRunDurationMs = MAX_RUN_DURATION_MS,
            pollIntervalMs = POLL_INTERVAL_MS,
            startupGraceMs = SYNC_STARTUP_GRACE_MS,
            idleStreakRequired = IDLE_STREAK_REQUIRED,
        ) { nowMs ->
            val generationId = syncDao.latestGenerationId()
            val incompleteImports = syncDao.countLatestIncompleteImports(ANDROID_SYNC_ITEM_IMPORTER_VERSION)
            val runnerWork = androidSync.hasPendingOrActiveWork()
            val syncAssetWork = if (generationId == null) {
                0
            } else {
                syncDao.countPending(generationId)
            }
            PeriodicSyncDrainStatus(
                generationId = generationId,
                incompleteImports = incompleteImports,
                pendingAssets = syncAssetWork,
                runnerWork = runnerWork,
            )
        }
        logger.info(
            event = "periodic_sync_drain_done",
            fields = mapOf(
                "completed" to result.completed.toString(),
                "stalled" to result.stalled.toString(),
                "elapsed_ms" to result.elapsedMs.toString(),
                "remaining_sync_pending_assets" to result.pendingAssets.toString(),
                "remaining_sync_incomplete_imports" to result.incompleteImports.toString(),
                "remaining_sync_runner_work" to result.runnerWork.toString(),
                "remaining_sync_total_work" to result.remainingWork.toString(),
            ),
        )
        return result
    }

    override suspend fun getForegroundInfo(): ForegroundInfo {
        ensureChannel()
        val notification = NotificationCompat.Builder(applicationContext, CHANNEL_ID)
            .setContentTitle(applicationContext.getString(R.string.notification_background_sync_title))
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setProgress(0, 0, /* indeterminate */ true)
            .setOngoing(true)
            .setSilent(true)
            .build()
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ForegroundInfo(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            ForegroundInfo(NOTIFICATION_ID, notification)
        }
    }

    private fun ensureChannel() {
        val mgr = applicationContext.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (mgr.getNotificationChannel(CHANNEL_ID) != null) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            applicationContext.getString(R.string.notification_background_sync_channel_name),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = applicationContext.getString(R.string.notification_background_sync_channel_description)
            enableVibration(false)
            setSound(null, null)
        }
        mgr.createNotificationChannel(channel)
    }

    companion object {
        /** Stable name so re-enqueues replace the prior request. */
        const val WORK_NAME = "igloo_periodic_sync"

        /** WorkManager clamps to a 15-minute floor. */
        const val MIN_INTERVAL_MINUTES = 15L

        /** Foreground notification — separate ID from MediaForegroundService (1001). */
        private const val NOTIFICATION_ID = 1002
        private const val CHANNEL_ID = "igloo_background_sync"

        /** Cap one run at ~50 minutes — well under Android's 6-hour FGS-time-out. */
        private const val MAX_RUN_DURATION_MS = 50L * 60_000L

        /** Poll the queue every 5 seconds to decide whether to keep holding the FGS. */
        private const val POLL_INTERVAL_MS = 5_000L

        /** Two consecutive idle polls before we tear down — handles brief refill gaps. */
        private const val IDLE_STREAK_REQUIRED = 2

        /** Give a freshly triggered process time to fetch/import its first Sync generation. */
        private const val SYNC_STARTUP_GRACE_MS = 60_000L

        suspend fun enqueue(context: Context, prefs: PreferencesRepo) {
            if (!prefs.syncEnabled().first()) {
                cancel(context)
                return
            }
            val intervalMin = prefs.syncIntervalMinutes().first().toLong().coerceAtLeast(MIN_INTERVAL_MINUTES)
            val wifiOnly = prefs.syncWifiOnly().first()
            val constraints = Constraints.Builder()
                .setRequiredNetworkType(if (wifiOnly) NetworkType.UNMETERED else NetworkType.CONNECTED)
                .build()
            val request = PeriodicWorkRequestBuilder<PeriodicSyncWorker>(intervalMin, TimeUnit.MINUTES)
                .setConstraints(constraints)
                .build()
            WorkManager.getInstance(context)
                .enqueueUniquePeriodicWork(WORK_NAME, ExistingPeriodicWorkPolicy.UPDATE, request)
        }

        fun cancel(context: Context) {
            WorkManager.getInstance(context).cancelUniqueWork(WORK_NAME)
        }
    }
}

interface PeriodicSyncScheduler {
    suspend fun applyPreferences()
}

internal class WorkManagerPeriodicSyncScheduler(
    private val context: Context,
    private val prefs: PreferencesRepo,
) : PeriodicSyncScheduler {
    override suspend fun applyPreferences() {
        PeriodicSyncWorker.enqueue(context, prefs)
    }
}
