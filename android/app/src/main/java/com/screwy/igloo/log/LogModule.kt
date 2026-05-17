package com.screwy.igloo.log

import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.SchedulerLogger
import org.koin.core.qualifier.named
import org.koin.dsl.bind
import org.koin.dsl.module

/**
 * Koin wiring for the logger. Every info/error/debug emit lands in the outbox
 * queue and ships to `POST /api/logs/{server,debug}` on the next drain.
 *
 * `OutboxWriter` is resolved lazily inside the sink binding so Logger can still be
 * constructed before Koin finishes wiring the sync module (test environments).
 */
val iglooLogModule = module {

    single<LogSink> { LogSink { entry -> get<OutboxWriter>().logSink.accept(entry) } }

    single {
        Logger(
            prefs = get(),
            sink = get(),
            scope = get(named("applicationScope")),
        )
    } bind SchedulerLogger::class
}
