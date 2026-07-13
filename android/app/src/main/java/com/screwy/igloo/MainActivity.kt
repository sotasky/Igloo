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
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.iglooDataModule
import com.screwy.igloo.feature.iglooFeatureModule
import com.screwy.igloo.i18n.AppLanguageStore
import com.screwy.igloo.i18n.AppLocaleProvider
import com.screwy.igloo.log.Logger
import com.screwy.igloo.log.iglooLogModule
import com.screwy.igloo.media.iglooMediaModule
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.iglooNetModule
import com.screwy.igloo.sync.SyncCoordinator
import com.screwy.igloo.sync.iglooSyncModule
import com.screwy.igloo.ui.iglooUiModule
import com.screwy.igloo.ui.nav.AppNavHost
import com.screwy.igloo.ui.theme.IglooTheme
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import org.koin.android.ext.android.inject
import org.koin.android.ext.koin.androidContext
import org.koin.core.context.GlobalContext
import org.koin.core.context.startKoin
import org.koin.core.qualifier.named

/**
 * Activity entrypoint. Resolves theme preferences from the fixed local database and gates signed-in
 * routes on auth state.
 */
class MainActivity : ComponentActivity() {

    private val authRepo: AuthRepo by inject()
    private val languageStore: AppLanguageStore by inject()

    override fun onCreate(savedInstanceState: Bundle?) {
        AppRuntime.ensureStarted(application)
        super.onCreate(savedInstanceState)
        setContent {
            val languageTag by languageStore.languageTag.collectAsStateWithLifecycle()
            val username by authRepo.usernameFlow.collectAsStateWithLifecycle()
            AppLocaleProvider(languageTag = languageTag) {
                if (username != null && authRepo.hasSessionSync()) {
                    LoggedInContent()
                } else {
                    IglooTheme { AppNavHost() }
                }
            }
        }
    }
}

@androidx.compose.runtime.Composable
private fun LoggedInContent() {
    val prefs: PreferencesRepo = org.koin.compose.koinInject()
    val themeId by
        prefs
            .themeId()
            .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.THEME_ID)
    val accentHex by
        prefs
            .themeAccentHex()
            .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.THEME_ACCENT_HEX)

    IglooTheme(themeId = themeId, accentHex = accentHex) { AppNavHost() }
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
        if (GlobalContext.getOrNull() == null) {
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

        return GlobalContext.get().get<AuthRepo>().hasSessionSync()
    }

    fun bootstrapPostLogin() {
        val koin = GlobalContext.get()
        koin.get<Reachability>().start()

        if (appStartLogged) return
        appStartLogged = true

        koin.get<Logger>().info(event = "app_start", fields = emptyMap())

        val scheduler: SyncCoordinator = koin.get()
        val authRepo: AuthRepo = koin.get()
        val scope: CoroutineScope = koin.get(named("applicationScope"))
        scope.launch {
            authRepo.onAppStart()
            if (authRepo.hasSessionSync()) {
                scheduler.start()
            }
        }
    }

    fun onLogout() {
        appStartLogged = false
    }

    private fun configureImageLoader() {
        SingletonImageLoader.setSafe { context -> GlobalContext.get().get<ImageLoader>() }
    }
}
