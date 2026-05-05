package com.screwy.igloo.net

/**
 * Breaks the `HttpClient -> Reachability -> HealthApi -> HttpClient` construction cycle.
 *
 * The shared client reports successful Igloo responses and transport failures into this
 * bridge, and the concrete [Reachability] state machine binds itself after Koin finishes
 * constructing the net graph.
 */
class ReachabilitySignals {
    @Volatile
    private var reachability: Reachability? = null

    fun bind(target: Reachability) {
        reachability = target
    }

    fun markOnline() {
        reachability?.markOnline()
    }

    fun downgrade() {
        reachability?.downgrade()
    }
}
