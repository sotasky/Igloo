package com.screwy.igloo.net

import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.data.PreferencesRepo
import androidx.lifecycle.eventFlow
import io.ktor.http.HttpStatusCode
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.onStart
import org.koin.android.ext.koin.androidContext
import org.koin.core.qualifier.named
import org.koin.dsl.module

/**
 * Koin wiring for `net/`.
 *
 *  - Shared `HttpClient` with the igloo interceptor stack.
 *  - One API class per domain — thin Ktor wrappers over the endpoint surface.
 *  - `Reachability` state machine + `baseUrlProvider` supply wrapped in singles.
 *
 * `iglooAuthModule` provides `AuthTokenProvider` and server URL bindings using
 * Koin's last-definition-wins resolution.
 *
 * URL / host resolvers source from the `serverUrlSource` lambda (filled in by
 * `iglooAuthModule` to point at `AuthRepo.serverUrlSync`), which is backed by
 * DB-independent auth storage — the HTTP client is therefore constructible pre-login.
 * Pre-auth the lambda defaults to `PreferencesRepo.Defaults.SERVER_URL`.
 */
val iglooNetModule = module {

    single { ReachabilitySignals() }

    single { LanServerNetworkBinder(context = androidContext(), hostProvider = get()) }

    // ServerBaseUrlProvider, IglooHostProvider and AuthTokenProvider are declared in
    // `iglooAuthModule` — they read from `AuthRepo`'s DB-independent auth cache so
    // the HTTP client stays DB-independent and the auth layer is the single source of
    // truth for server URL + bearer token.

    single {
        // Passing DB-backed prefs as a lambda keeps the HTTP client constructible
        // pre-login: `PreferencesRepo` transits `DatabaseHolder.requireCurrent()`,
        // which throws until a user logs in. Once login opens the DB it resolves
        // normally; the shared envelope parser only applies the server-time side effect.
        // Stream cursors stay owned by inbound `next_marker` loops.
        val holder: DatabaseHolder = get()
        val prefsProvider: () -> PreferencesRepo? = {
            if (holder.current != null) runCatching { get<PreferencesRepo>() }.getOrNull() else null
        }
        buildIglooClient(
            prefsProvider = prefsProvider,
            tokenProvider = get(),
            hostProvider = get(),
            beforeIglooRequest = get<LanServerNetworkBinder>()::bindForCurrentServerIfNeeded,
            onReachable = get<ReachabilitySignals>()::markOnline,
            onTransportFailure = get<ReachabilitySignals>()::downgrade,
        )
    }

    // ─── API surface ────────────────────────────────────────────────────────
    single { HealthApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { AuthApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { FeedApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { VideoApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { ShortsApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { ChannelsApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { AndroidSyncApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { OutboxApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }
    single { MutationDeltaApi(client = get(), baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl) }

    // ─── Reachability ───────────────────────────────────────────────────────
    single {
        Reachability(
            scope = get(named("applicationScope")),
            probe = {
                runCatching {
                    val resp = get<HealthApi>().health()
                    resp.status == HttpStatusCode.OK
                }.getOrDefault(false)
            },
            foregroundFlow = get<ForegroundLifecycleFlow>().flow,
        ).also { get<ReachabilitySignals>().bind(it) }
    }

    single { ForegroundLifecycleFlow(scope = get(named("applicationScope"))) }
}

/**
 * Sync-cached base URL for every `*Api`. Reads from a lambda so `AuthRepo` can wire its
 * auth-storage-backed `serverUrlSync` without pulling in a Koin dep.
 */
class ServerBaseUrlProvider(
    private val urlSource: () -> String,
) {
    fun baseUrl(): String = urlSource().trimEnd('/')
}

/**
 * ProcessLifecycleOwner-backed `Flow<Boolean>` where `true` means the app is
 * foregrounded. `Reachability` subscribes to drive its 30s offline probe loop.
 *
 * Kept as a separate class so tests can swap a plain `Flow<Boolean>` in without
 * pulling `androidx.lifecycle.ProcessLifecycleOwner` into the JVM test path.
 */
class ForegroundLifecycleFlow(@Suppress("UNUSED_PARAMETER") scope: CoroutineScope) {

    val flow: kotlinx.coroutines.flow.Flow<Boolean> = buildForegroundFlow()

    private fun buildForegroundFlow(): kotlinx.coroutines.flow.Flow<Boolean> {
        val lifecycle = androidx.lifecycle.ProcessLifecycleOwner.get().lifecycle
        return lifecycle.eventFlow
            .map { event ->
                when (event) {
                    androidx.lifecycle.Lifecycle.Event.ON_START -> true
                    androidx.lifecycle.Lifecycle.Event.ON_STOP -> false
                    else -> null
                }
            }
            .filterNotNull()
            .onStart {
                emit(lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.STARTED))
            }
            .distinctUntilChanged()
    }
}
