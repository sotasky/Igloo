package com.screwy.igloo.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import org.koin.core.qualifier.named
import org.koin.dsl.module

/**
 * Koin wiring for the data layer.
 *
 *  - `DatabaseHolder` is a single; holds the current per-user DB instance.
 *  - `PreferencesRepo` is a single; needs an application-scoped CoroutineScope to keep
 *    its hot-path caches warm.
 *  - DAO accessors are factories that resolve the current DB from the holder at each
 *    call, so login/logout swaps propagate transparently to callers.
 *
 *  Consumers inject DAOs directly — they never hold a IglooDatabase reference. Accessing
 *  a DAO before login throws via `requireCurrent()` (bug if it happens; caller paths are
 *  all gated behind the login screen).
 */
val iglooDataModule = module {

    single(named("applicationScope")) { CoroutineScope(SupervisorJob() + Dispatchers.Default) }

    single { DatabaseHolder(get()) }

    single {
        PreferencesRepo(
            dao = get<DatabaseHolder>().requireCurrent().preferenceDao(),
            scope = get(named("applicationScope")),
        )
    }

    // Per-entity DAOs — factory shape so login/logout is transparent.
    factory { get<DatabaseHolder>().requireCurrent().feedItemDao() }
    factory { get<DatabaseHolder>().requireCurrent().videoDao() }
    factory { get<DatabaseHolder>().requireCurrent().channelDao() }
    factory { get<DatabaseHolder>().requireCurrent().channelProfileDao() }
    factory { get<DatabaseHolder>().requireCurrent().videoCommentDao() }
    factory { get<DatabaseHolder>().requireCurrent().retweetSourceDao() }
    factory { get<DatabaseHolder>().requireCurrent().sponsorBlockSegmentDao() }
    factory { get<DatabaseHolder>().requireCurrent().sponsorBlockCheckedDao() }
    factory { get<DatabaseHolder>().requireCurrent().feedLikeDao() }
    factory { get<DatabaseHolder>().requireCurrent().feedRankDao() }
    factory { get<DatabaseHolder>().requireCurrent().bookmarkDao() }
    factory { get<DatabaseHolder>().requireCurrent().bookmarkCategoryDao() }
    factory { get<DatabaseHolder>().requireCurrent().bookmarkLabelDao() }
    factory { get<DatabaseHolder>().requireCurrent().feedSeenDao() }
    factory { get<DatabaseHolder>().requireCurrent().momentViewDao() }
    factory { get<DatabaseHolder>().requireCurrent().watchHistoryDao() }
    factory { get<DatabaseHolder>().requireCurrent().mutedAccountDao() }
    factory { get<DatabaseHolder>().requireCurrent().channelFollowDao() }
    factory { get<DatabaseHolder>().requireCurrent().channelStarDao() }
    factory { get<DatabaseHolder>().requireCurrent().channelSettingDao() }
    factory { get<DatabaseHolder>().requireCurrent().outboxDao() }
    factory { get<DatabaseHolder>().requireCurrent().preferenceDao() }
    factory { get<DatabaseHolder>().requireCurrent().cursorDao() }
    factory { get<DatabaseHolder>().requireCurrent().mediaInventoryDao() }
    factory { get<DatabaseHolder>().requireCurrent().androidSyncDao() }

    // Composite read DAOs.
    factory { get<DatabaseHolder>().requireCurrent().feedReadDao() }
    factory { get<DatabaseHolder>().requireCurrent().momentReadDao() }
    factory { get<DatabaseHolder>().requireCurrent().videoReadDao() }
    factory { get<DatabaseHolder>().requireCurrent().bookmarkReadDao() }
    factory { get<DatabaseHolder>().requireCurrent().channelReadDao() }
}
