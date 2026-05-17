package com.screwy.igloo.testutil

import com.screwy.igloo.sync.SchedulerActions
import com.screwy.igloo.sync.SyncStream

class FakeSchedulerActions : SchedulerActions {
    var triggerAllCount = 0
        private set
    val triggeredStreams = mutableListOf<SyncStream>()

    override fun triggerAll() {
        triggerAllCount += 1
    }

    override fun triggerStream(stream: SyncStream) {
        triggeredStreams += stream
    }
}
