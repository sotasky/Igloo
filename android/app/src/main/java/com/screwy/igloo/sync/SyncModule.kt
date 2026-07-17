package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.outbox.OutboxDispatcher
import com.screwy.igloo.outbox.OutboxDrain
import com.screwy.igloo.outbox.OutboxWriter
import kotlinx.coroutines.flow.first
import org.koin.android.ext.koin.androidContext
import org.koin.core.qualifier.named
import org.koin.dsl.bind
import org.koin.dsl.module

val iglooSyncModule = module {

    single<PeriodicSyncScheduler> {
        WorkManagerPeriodicSyncScheduler(
            context = androidContext(),
            prefs = get(),
        )
    }

    single {
        OutboxWriter(
            db = get<IglooDatabase>(),
            prefs = get(),
            scope = get(named("applicationScope")),
            onDrainRequested = { get<SyncCoordinator>().trigger() },
        )
    }

    single {
        OutboxDispatcher(
            api = get(),
            db = get<IglooDatabase>(),
            uiEffects = get(),
        )
    }

    single {
        OutboxDrain(
            outboxDao = get(),
            dispatcher = get(),
            db = get<IglooDatabase>(),
            prefs = get(),
            reachability = get(),
            logger = get(),
        )
    }

    single {
        val prefs = get<PreferencesRepo>()
        AndroidSyncMirror(
            db = get<IglooDatabase>(),
            dao = get(),
            api = get(),
            client = get(),
            baseUrlProvider = get(),
            reachability = get(),
            foregroundPromoter = get(),
            mediaRoot = get(named("mediaRoot")),
            logger = get(),
            retentionProvider = {
                AndroidSyncRetentionRequest(
                    feedDays = prefs.retentionDaysFeed().first(),
                    youtubeDays = prefs.retentionDaysYoutube().first(),
                    momentsDays = prefs.retentionDaysMoments().first(),
                    storyHours = prefs.storiesWindowHours().first(),
                )
            },
            serverNowMsProvider = {
                System.currentTimeMillis() + prefs.serverTimeOffsetMsSync()
            },
        )
    }

    single {
        SyncCoordinator(
            scope = get(named("applicationScope")),
            outbox = get(),
            mirror = get(),
            prefs = get(),
            reachability = get(),
            foregroundFlow = get<com.screwy.igloo.net.ForegroundLifecycleFlow>().flow,
            logger = get(),
        )
    }

    single {
        OfflineVideoDownloads(
            db = get<IglooDatabase>(),
            syncDao = get(),
            downloads = get(),
            mediaRoot = get(named("mediaRoot")),
            nowMsProvider = System::currentTimeMillis,
            syncTrigger = get<SyncCoordinator>()::trigger,
        )
    } bind OfflineVideoActions::class
}
