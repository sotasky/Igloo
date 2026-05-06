package com.screwy.igloo.ui

import coil3.ImageLoader
import coil3.annotation.ExperimentalCoilApi
import coil3.memory.MemoryCache
import coil3.network.ktor3.KtorNetworkFetcherFactory
import coil3.request.CachePolicy
import coil3.size.Precision
import com.screwy.igloo.i18n.AppLanguageStore
import io.ktor.client.HttpClient
import org.koin.android.ext.koin.androidContext
import org.koin.dsl.module

/** Koin wiring for the `ui/` package's non-composable plumbing. */
@OptIn(ExperimentalCoilApi::class)
val iglooUiModule = module {
    single { UiEffects() }
    single { AppLanguageStore(androidContext()) }
    single {
        ImageLoader.Builder(androidContext())
            .components {
                add(KtorNetworkFetcherFactory(get<HttpClient>()))
            }
            .memoryCache {
                MemoryCache.Builder()
                    .maxSizePercent(androidContext(), 0.35)
                    .build()
            }
            .memoryCachePolicy(CachePolicy.ENABLED)
            .diskCachePolicy(CachePolicy.ENABLED)
            .networkCachePolicy(CachePolicy.ENABLED)
            .precision(Precision.INEXACT)
            .build()
    }
}
