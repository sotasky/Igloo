package com.screwy.igloo.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import org.koin.core.qualifier.named
import org.koin.android.ext.koin.androidContext
import org.koin.dsl.module

/**
 * Koin wiring for the data layer.
 *
 *  - `IglooDatabase` is the one process-wide local mirror.
 *  - `PreferencesRepo` is a single; needs an application-scoped CoroutineScope to keep
 *    its hot-path caches warm.
 *  - DAO accessors resolve from that fixed Room instance.
 */
val iglooDataModule = module {

    single(named("applicationScope")) { CoroutineScope(SupervisorJob() + Dispatchers.Default) }

    single { IglooDatabase.build(androidContext()) }

    single {
        PreferencesRepo(
            dao = get<IglooDatabase>().preferenceDao(),
            scope = get(named("applicationScope")),
        )
    }

    factory { get<IglooDatabase>().feedItemDao() }
    factory { get<IglooDatabase>().videoDao() }
    factory { get<IglooDatabase>().channelDao() }
    factory { get<IglooDatabase>().channelProfileDao() }
    factory { get<IglooDatabase>().videoCommentDao() }
    factory { get<IglooDatabase>().retweetSourceDao() }
    factory { get<IglooDatabase>().sponsorBlockSegmentDao() }
    factory { get<IglooDatabase>().sponsorBlockCheckedDao() }
    factory { get<IglooDatabase>().feedLikeDao() }
    factory { get<IglooDatabase>().feedRankDao() }
    factory { get<IglooDatabase>().bookmarkDao() }
    factory { get<IglooDatabase>().bookmarkCategoryDao() }
    factory { get<IglooDatabase>().feedSeenDao() }
    factory { get<IglooDatabase>().momentViewDao() }
    factory { get<IglooDatabase>().momentsCursorDao() }
    factory { get<IglooDatabase>().watchHistoryDao() }
    factory { get<IglooDatabase>().mutedChannelDao() }
    factory { get<IglooDatabase>().channelFollowDao() }
    factory { get<IglooDatabase>().channelStarDao() }
    factory { get<IglooDatabase>().channelSettingDao() }
    factory { get<IglooDatabase>().outboxDao() }
    factory { get<IglooDatabase>().preferenceDao() }
    factory { get<IglooDatabase>().androidSyncDao() }

    // Composite read DAOs.
    factory { get<IglooDatabase>().feedReadDao() }
    factory { get<IglooDatabase>().momentReadDao() }
    factory { get<IglooDatabase>().videoReadDao() }
    factory { get<IglooDatabase>().bookmarkReadDao() }
    factory { get<IglooDatabase>().channelReadDao() }
}
