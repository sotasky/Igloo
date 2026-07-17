package com.screwy.igloo.media

import android.content.Context
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.sync.SyncCoordinator
import kotlinx.coroutines.flow.map
import org.koin.android.ext.koin.androidContext
import org.koin.core.qualifier.named
import org.koin.dsl.bind
import org.koin.dsl.module

/** Let the first media request establish reachability; only a confirmed outage blocks it. */
internal fun allowsRemoteMediaFallback(state: Reachability.State): Boolean =
    state !is Reachability.State.Offline

/**
 * Koin wiring for the media layer. Android sync owns media mirroring; this
 * module keeps the UI resolvers, storage accounting, and foreground promotion
 * used by the Android sync downloader.
 *
 * Named qualifier "mediaRoot" avoids ambiguity with any other `java.io.File` binding.
 */
val iglooMediaModule = module {
    single(named("mediaRoot")) {
        val ctx: Context = androidContext()
        java.io.File(ctx.filesDir, "media").apply { mkdirs() }
    }

    single {
        ForegroundPromoter(
            context = androidContext(),
            logger = get(),
        )
    }

    single {
        MediaResolversImpl(
            syncDao = get(),
            baseUrlProvider = get<ServerBaseUrlProvider>()::baseUrl,
            prefs = get(),
            remoteFallbackAllowed = get<Reachability>().state.map(::allowsRemoteMediaFallback),
        )
    } bind MediaResolvers::class

    single {
        CacheOps(
            syncDao = get(),
            mediaRoot = get(named("mediaRoot")),
            logger = get(),
            syncTrigger = get<SyncCoordinator>()::trigger,
        )
    } bind CacheActions::class
}
