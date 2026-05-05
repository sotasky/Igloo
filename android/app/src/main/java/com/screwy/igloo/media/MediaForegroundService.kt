package com.screwy.igloo.media

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import com.screwy.igloo.R

/**
 * Foreground service promoted to when a media drain is in-flight.
 *
 * Promotion is triggered by [ForegroundPromoter.startDownloading]; de-promotion by
 * [ForegroundPromoter.finishedBatch] when the in-flight set empties.
 *
 * Large backfills include thousands of thumbnails and avatars, so the whole drain
 * can be promoted when Android allows foreground-service starts.
 */
class MediaForegroundService : Service() {

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf(startId)
            return START_NOT_STICKY
        }

        ensureNotificationChannel()

        val notification = NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.notification_downloading_media_title))
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setProgress(0, 0, /* indeterminate */ true)
            .setOngoing(true)
            .setSilent(true)
            .build()

        try {
            startForeground(NOTIFICATION_ID, notification)
        } catch (e: Exception) {
            Log.i(TAG, "Foreground media promotion denied; continuing without foreground service", e)
            stopSelf(startId)
            return START_NOT_STICKY
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun ensureNotificationChannel() {
        val manager = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        if (manager.getNotificationChannel(CHANNEL_ID) == null) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                getString(R.string.notification_media_download_channel_name),
                NotificationManager.IMPORTANCE_LOW,
            ).apply {
                description = getString(R.string.notification_media_download_channel_description)
                enableVibration(false)
                setSound(null, null)
            }
            manager.createNotificationChannel(channel)
        }
    }

    companion object {
        const val ACTION_STOP = "com.screwy.igloo.media.STOP_FOREGROUND"
        const val CHANNEL_ID = "igloo_media_downloads"
        private const val TAG = "MediaForegroundService"
        private const val NOTIFICATION_ID = 1001
    }
}
