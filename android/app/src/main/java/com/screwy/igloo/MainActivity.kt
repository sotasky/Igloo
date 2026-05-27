package com.screwy.igloo

import android.app.Application
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.ImageLoader
import coil3.SingletonImageLoader
import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.auth.iglooAuthModule
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.iglooDataModule
import com.screwy.igloo.feature.iglooFeatureModule
import com.screwy.igloo.i18n.AppLanguageStore
import com.screwy.igloo.i18n.AppLocaleProvider
import com.screwy.igloo.log.Logger
import com.screwy.igloo.log.iglooLogModule
import com.screwy.igloo.media.iglooMediaModule
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.perf.PerfProbe
import com.screwy.igloo.net.iglooNetModule
import com.screwy.igloo.sync.PeriodicSyncWorker
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.sync.iglooSyncModule
import com.screwy.igloo.ui.nav.AppNavHost
import com.screwy.igloo.ui.iglooUiModule
import com.screwy.igloo.ui.theme.IglooTheme
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import org.koin.android.ext.android.inject
import org.koin.android.ext.koin.androidContext
import org.koin.core.context.GlobalContext
import org.koin.core.context.startKoin
import org.koin.core.qualifier.named

/**
 * Activity entrypoint. Resolves the user's theme preferences reactively once a
 * per-user Room DB is open — any pref write (e.g. from the settings/theme picker)
 * recomposes the UI with the new palette without a restart. Before login, the DB
 * is closed and prefs can't be resolved; we render the login route under the
 * default theme until post-login bootstrap opens the DB and starts reconcilers.
 */
class MainActivity : ComponentActivity() {

    private val databaseHolder: DatabaseHolder by inject()
    private val languageStore: AppLanguageStore by inject()

    override fun onCreate(savedInstanceState: Bundle?) {
        PerfProbe.begin("app_on_create")
        try {
            AppRuntime.ensureStarted(application)
            super.onCreate(savedInstanceState)
            setContent {
                val languageTag by languageStore.languageTag.collectAsStateWithLifecycle()
                val username by databaseHolder.usernameFlow.collectAsStateWithLifecycle()
                AppLocaleProvider(languageTag = languageTag) {
                    if (username != null) {
                        LoggedInContent()
                    } else {
                        IglooTheme {
                            AppNavHost()
                        }
                    }
                }
            }
        } finally {
            PerfProbe.end()
        }
    }
}

@androidx.compose.runtime.Composable
private fun LoggedInContent() {
    val prefs: PreferencesRepo = org.koin.compose.koinInject()
    val themeId by prefs.themeId()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.THEME_ID)
    val accentHex by prefs.themeAccentHex()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.THEME_ACCENT_HEX)

    IglooTheme(
        themeId = themeId,
        accentHex = accentHex,
    ) {
        AppNavHost()
    }
}

object AppRuntime {
    @Volatile private var appStartLogged = false

    fun ensureStarted(application: Application) {
        if (prepareLocalSession(application)) {
            bootstrapPostLogin()
        }
    }

    @Synchronized
    fun prepareLocalSession(application: Application): Boolean {
        PerfProbe.timed(event = "app_runtime_ensure_started") {
            if (GlobalContext.getOrNull() == null) {
                PerfProbe.timed(event = "app_runtime_start_koin") {
                    startKoin {
                        androidContext(application)
                        modules(
                            iglooDataModule,
                            iglooNetModule,
                            iglooLogModule,
                            iglooUiModule,
                            iglooMediaModule,
                            iglooSyncModule,
                            iglooAuthModule,
                            iglooFeatureModule,
                        )
                    }
                    configureImageLoader()
                }
            }

            val koin = GlobalContext.get()
            val databaseHolder: DatabaseHolder = koin.get()
            if (databaseHolder.current == null) {
                PerfProbe.timed(event = "app_runtime_open_local_session") {
                    runCatching {
                        val authRepo: AuthRepo = koin.get()
                        if (authRepo.canOpenLocalSessionSync()) {
                            authRepo.usernameSync()?.takeIf { it.isNotBlank() }?.let { username ->
                                databaseHolder.openForUser(username)
                            }
                        }
                    }
                }
            }

            return databaseHolder.current != null
        }
    }

    fun bootstrapPostLogin() {
        PerfProbe.timed(event = "app_runtime_bootstrap_post_login") {
            val koin = GlobalContext.get()
            koin.get<Reachability>().start()

            if (appStartLogged) return@timed
            appStartLogged = true

            koin.get<Logger>().info(event = "app_start", fields = emptyMap())

            val scheduler: Scheduler = koin.get()
            val prefs: PreferencesRepo = koin.get()
            val authRepo: AuthRepo = koin.get()
            val scope: CoroutineScope = koin.get(named("applicationScope"))
            scope.launch {
                PerfProbe.timedSuspend(event = "app_runtime_post_login_async") {
                    authRepo.onAppStart()
                    if (authRepo.canOpenLocalSessionSync()) {
                        scheduler.start()
                        PeriodicSyncWorker.enqueue(koin.get<Application>(), prefs)
                        PeriodicSyncWorker.enqueueCatchup(koin.get<Application>(), prefs)
                    }
                }
            }
        }
    }

    fun onLogout() {
        appStartLogged = false
    }

    private fun configureImageLoader() {
        SingletonImageLoader.setSafe { context ->
            GlobalContext.get().get<ImageLoader>()
        }
    }
}
