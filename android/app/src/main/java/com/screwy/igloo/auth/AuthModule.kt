package com.screwy.igloo.auth

import com.screwy.igloo.AppRuntime
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.ServerDiscovery
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.sync.Scheduler
import org.koin.core.module.dsl.viewModel
import org.koin.core.qualifier.named
import org.koin.dsl.bind
import org.koin.dsl.module

/**
 * Koin wiring for the `auth/` package. Load `iglooAuthModule` AFTER `iglooNetModule`;
 * the overrides here re-bind:
 *
 *  - `AuthTokenProvider` — `AuthRepo` reads the bearer token from its
 *    DB-independent auth cache and handles the 401 refresh handshake.
 *  - `ServerBaseUrlProvider` / `IglooHostProvider` — URL / host source lambdas now
 *    point at `AuthRepo.serverUrlSync` / `serverHostSync`. That's the pre-login
 *    bootstrap source of truth (DB-independent) so the HTTP client is resolvable
 *    before login lands.
 *
 * `prefsUpdater` mirrors the server URL into Room `preferences` after login so the
 * Settings screen sees the same URL the auth storage has; failures are non-fatal —
 * auth storage is authoritative.
 */
val iglooAuthModule = module {

    single<AuthStorage> { KeystoreAuthStorage.createMigrating(get()) }

    single {
        AuthRepo(
            context = get(),
            storage = get(),
            databaseHolder = get(),
            uiEffects = get(),
            applicationScope = get(named("applicationScope")),
            authApiProvider = { get() },
            // Scheduler depends on the per-user DB via InboundReconciler; resolving it
            // eagerly at AuthRepo ctor time would crash pre-login. Wrap as a lambda so
            // resolution happens on logout, by which point the DB is open.
            stopReconcilersOnLogout = {
                AppRuntime.onLogout()
                get<Scheduler>().stopAll()
            },
            prefsUpdater = { url -> get<PreferencesRepo>().setServerUrl(url) },
            onPostLoginBootstrap = { AppRuntime.bootstrapPostLogin() },
        )
    } bind AccountSessionActions::class

    single<AuthTokenProvider> { get<AuthRepo>() }

    single { ServerBaseUrlProvider(urlSource = { get<AuthRepo>().serverUrlSync() }) }

    single { IglooHostProvider(hostSource = { get<AuthRepo>().serverHostSync() }) }

    viewModel { (onSuccess: () -> Unit) ->
        LoginViewModel(authRepo = get(), serverDiscovery = get<ServerDiscovery>(), onLoginSuccess = onSuccess)
    }
}
