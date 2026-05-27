package com.screwy.igloo.sync

import android.app.Application
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.screwy.igloo.AppRuntime
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.log.Logger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import org.koin.core.context.GlobalContext

class SyncBootReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action ?: return
        if (action !in SUPPORTED_ACTIONS) return

        val pending = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.Default).launch {
            try {
                val application = context.applicationContext as? Application ?: return@launch
                if (!AppRuntime.prepareLocalSession(application)) return@launch
                val koin = GlobalContext.get()
                val prefs: PreferencesRepo = koin.get()
                PeriodicSyncWorker.enqueue(application, prefs)
                PeriodicSyncWorker.enqueueCatchup(application, prefs)
                koin.get<Logger>().info(
                    event = "sync_bootstrap_enqueued",
                    fields = mapOf("reason" to action.substringAfterLast('.')),
                )
            } finally {
                pending.finish()
            }
        }
    }

    private companion object {
        val SUPPORTED_ACTIONS = setOf(
            Intent.ACTION_BOOT_COMPLETED,
            Intent.ACTION_MY_PACKAGE_REPLACED,
        )
    }
}
