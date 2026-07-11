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
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.ListenableWorker
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.screwy.igloo.AppRuntime
import com.screwy.igloo.R
import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.media.ForegroundPromoter
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import org.koin.core.context.GlobalContext

class PeriodicSyncWorker(appContext: Context, params: WorkerParameters) :
    CoroutineWorker(appContext, params) {

    override suspend fun doWork(): ListenableWorker.Result {
        return try {
            val prepared = AppRuntime.prepareLocalSession(applicationContext as Application)
            if (!prepared) return ListenableWorker.Result.success()

            val koin = GlobalContext.get()
            val authRepo: AuthRepo = koin.get()
            authRepo.onAppStart()
            if (!authRepo.hasSessionSync()) return ListenableWorker.Result.success()

            val prefs: PreferencesRepo = koin.get()
            if (!prefs.syncEnabled().first()) {
                cancel(applicationContext)
                return ListenableWorker.Result.success()
            }

            setForeground(getForegroundInfo())
            koin.get<ForegroundPromoter>().acquireExternalForegroundLease().use {
                koin.get<SyncCoordinator>().pass()
            }
            ListenableWorker.Result.success()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            ListenableWorker.Result.retry()
        }
    }

    override suspend fun getForegroundInfo(): ForegroundInfo {
        ensureChannel()
        val notification =
            NotificationCompat.Builder(applicationContext, CHANNEL_ID)
                .setContentTitle(
                    applicationContext.getString(R.string.notification_background_sync_title)
                )
                .setSmallIcon(android.R.drawable.stat_sys_download)
                .setProgress(0, 0, true)
                .setOngoing(true)
                .setSilent(true)
                .build()
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ForegroundInfo(
                NOTIFICATION_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
            )
        } else {
            ForegroundInfo(NOTIFICATION_ID, notification)
        }
    }

    private fun ensureChannel() {
        val manager =
            applicationContext.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (manager.getNotificationChannel(CHANNEL_ID) != null) return
        manager.createNotificationChannel(
            NotificationChannel(
                    CHANNEL_ID,
                    applicationContext.getString(
                        R.string.notification_background_sync_channel_name
                    ),
                    NotificationManager.IMPORTANCE_LOW,
                )
                .apply {
                    description =
                        applicationContext.getString(
                            R.string.notification_background_sync_channel_description
                        )
                    enableVibration(false)
                    setSound(null, null)
                }
        )
    }

    companion object {
        const val WORK_NAME = "igloo_periodic_sync"
        const val CATCHUP_WORK_NAME = "igloo_sync_catchup"
        const val MIN_INTERVAL_MINUTES = 15L

        private const val NOTIFICATION_ID = 1002
        private const val CHANNEL_ID = "igloo_background_sync"

        suspend fun enqueue(
            context: Context,
            prefs: PreferencesRepo,
            policy: ExistingPeriodicWorkPolicy = ExistingPeriodicWorkPolicy.UPDATE,
        ) {
            if (!prefs.syncEnabled().first()) {
                cancel(context)
                return
            }
            val interval =
                prefs.syncIntervalMinutes().first().toLong().coerceAtLeast(MIN_INTERVAL_MINUTES)
            val request =
                PeriodicWorkRequestBuilder<PeriodicSyncWorker>(interval, TimeUnit.MINUTES)
                    .setConstraints(constraintsFor(prefs.syncWifiOnly().first()))
                    .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(WORK_NAME, policy, request)
        }

        suspend fun enqueueCatchup(context: Context, prefs: PreferencesRepo) {
            if (!prefs.syncEnabled().first()) {
                cancel(context)
                return
            }
            val request =
                OneTimeWorkRequestBuilder<PeriodicSyncWorker>()
                    .setConstraints(constraintsFor(prefs.syncWifiOnly().first()))
                    .build()
            WorkManager.getInstance(context)
                .enqueueUniqueWork(CATCHUP_WORK_NAME, ExistingWorkPolicy.REPLACE, request)
        }

        fun cancel(context: Context) {
            WorkManager.getInstance(context).apply {
                cancelUniqueWork(WORK_NAME)
                cancelUniqueWork(CATCHUP_WORK_NAME)
            }
        }

        private fun constraintsFor(wifiOnly: Boolean): Constraints =
            Constraints.Builder()
                .setRequiredNetworkType(
                    if (wifiOnly) NetworkType.UNMETERED else NetworkType.CONNECTED
                )
                .build()
    }
}

interface PeriodicSyncScheduler {
    suspend fun applyPreferences()

    suspend fun enqueueCatchup()
}

internal class WorkManagerPeriodicSyncScheduler(
    private val context: Context,
    private val prefs: PreferencesRepo,
) : PeriodicSyncScheduler {
    override suspend fun applyPreferences() {
        PeriodicSyncWorker.enqueue(context, prefs, ExistingPeriodicWorkPolicy.CANCEL_AND_REENQUEUE)
        PeriodicSyncWorker.enqueueCatchup(context, prefs)
    }

    override suspend fun enqueueCatchup() {
        PeriodicSyncWorker.enqueueCatchup(context, prefs)
    }
}
