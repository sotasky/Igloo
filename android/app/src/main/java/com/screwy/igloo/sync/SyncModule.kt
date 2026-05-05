package com.screwy.igloo.sync

import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.outbox.OutboxDispatcher
import com.screwy.igloo.outbox.OutboxDrain
import com.screwy.igloo.outbox.OutboxWriter
import kotlinx.coroutines.flow.first
import org.koin.core.qualifier.named
import org.koin.dsl.module

/**
 * Koin wiring for the scheduler, outbox, inbound reconciler, and media mirror.
 *
 *  - `OutboxWriter` / `OutboxDispatcher` / `OutboxDrain` live in `outbox/`.
 *  - `InboundReconciler` + `Scheduler` live here.
 *  - Android sync owns media mirroring.
 *
 * Room DAOs come from the `DatabaseHolder.requireCurrent()` factory bindings in
 * `iglooDataModule` so sync services stay current across login/logout swaps.
 */
val iglooSyncModule = module {

    single {
        OutboxWriter(
            db = get<DatabaseHolder>().requireCurrent(),
            prefs = get(),
            scope = get(named("applicationScope")),
        )
    }

    single {
        OutboxDispatcher(
            api = get(),
            db = get<DatabaseHolder>().requireCurrent(),
            authTokens = get(),
            logger = get(),
            uiEffects = get(),
        )
    }

    single {
        OutboxDrain(
            outboxDao = get(),
            dispatcher = get(),
            db = get<DatabaseHolder>().requireCurrent(),
            prefs = get(),
            reachability = get(),
            logger = get(),
        )
    }

    single {
        MutationDeltaSync(
            db = get<DatabaseHolder>().requireCurrent(),
            prefs = get(),
            cursorDao = get(),
            outboxDao = get(),
            api = get(),
            reachability = get(),
            logger = get(),
        )
    }

    single {
        InboundReconciler(
            db = get<DatabaseHolder>().requireCurrent(),
            prefs = get(),
            cursorDao = get(),
            outboxDao = get(),
            feedApi = get(),
            videoApi = get(),
            shortsApi = get(),
            channelsApi = get(),
            rankRefreshTrigger = { get<AndroidSyncMirror>().trigger() },
            reachability = get(),
            logger = get(),
        )
    }

    single {
        val prefs = get<PreferencesRepo>()
        AndroidSyncMirror(
            db = get<DatabaseHolder>().requireCurrent(),
            dao = get(),
            outboxDao = get(),
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
        )
    }

    single {
        SyncReplayTrigger {
            get<InboundReconciler>().trigger()
        }
    }

    single {
        val androidSync = get<AndroidSyncMirror>()
        RetentionReplayCoordinator(
            scope = get(named("applicationScope")),
            prefs = get(),
            cursorDao = get(),
            replayTrigger = get(),
            syncTrigger = { androidSync.trigger() },
            logger = get(),
        )
    }

    single {
        Scheduler(
            scope = get(named("applicationScope")),
            inbound = get(),
            outbox = get(),
            androidSync = get(),
            retentionReplay = get(),
            reachability = get(),
            foregroundFlow = get<com.screwy.igloo.net.ForegroundLifecycleFlow>().flow,
            writer = get(),
            mutationDelta = get(),
            logger = get(),
        )
    }
}
