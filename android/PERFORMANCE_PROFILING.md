# Android Performance Profiling Probes

This instrumentation is scoped to the Android performance findings verified on
2026-05-16. It avoids product behavior changes and is gated by the Android log
tag `IglooPerf`: when that tag is not `DEBUG`, probe timers, counters, trace
sections, collector tracking, and Room query/invalidation summaries return early.

## Enable Log Probes

Trace sections are visible during Perfetto captures. Query, invalidation, and
collector logs are opt-in:

```bash
adb -s "$SERIAL" shell setprop log.tag.IglooPerf DEBUG
adb -s "$SERIAL" logcat -c
adb -s "$SERIAL" shell am force-stop com.screwy.igloo
adb -s "$SERIAL" shell am start -n com.screwy.igloo/.MainActivity
adb -s "$SERIAL" logcat -v threadtime -s IglooPerf
```

Set the property before the app opens its Room database; the query callback is
installed at DB build time.

Disable probes for normal app use:

```bash
adb -s "$SERIAL" shell setprop log.tag.IglooPerf INFO
adb -s "$SERIAL" shell am force-stop com.screwy.igloo
adb -s "$SERIAL" shell am start -n com.screwy.igloo/.MainActivity
```

## Probe Coverage

- Finding 5: media resolver collector starts/ends/emits, summarized Room query
  counts, and summarized Room invalidation logs for media and sync tables.
- Finding 6: native feed model build, adapter submit/bind, viewport change,
  near-visible warming, seen enqueue, and action toggle timings.
- Finding 8: Moments and MediaViewer player build/prepare/clear/release counts
  plus pager page/scroll-state logs for rapid swipe captures.
- Finding 9: long-form playback state, UI state matrix, progress/subtitle
  polling collectors, subtitle parse timing, seek-preview decode/parse timing,
  and scrub start/end logs.
- Finding 10: per-prune-statement timings with affected rows, generation prune
  timings, and orphan asset file walk counts/duration.
- Finding 11: full-list Room emissions and downstream map timings for Videos,
  Moments grid/player, and Bookmarks, with summarized Room invalidation logs.
- Finding 12: cold startup, post-login bootstrap, foreground trigger, mutation
  delta, scheduler trigger, and WorkManager catch-up timings.

## Evidence Folder

Runtime captures for this session belong under:

```text
.agents/android-performance-report-2026-05-16/profiling-run-2026-05-16/
```

Keep each run focused: one flow per Perfetto/gfxinfo/logcat capture, with the
adb serial, app build, exact flow, and caveats recorded next to the artifacts.
