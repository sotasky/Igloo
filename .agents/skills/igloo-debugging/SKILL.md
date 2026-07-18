---
name: igloo-debugging
description: Use when investigating Igloo failures, regressions, CI failures, flaky Android tests, sync/media gaps, generated-file drift, or unclear server/web/Android behavior before deciding whether to patch product code, tests, workflow, or data.
---

# Igloo Debugging

Use current evidence before theory. Prefer read-only logs, DB queries, MCP traces, source inspection, and exact failing commands over assumptions or broad rewrites.

## Default Flow

1. Identify the failing surface: server, web, Android app, sync/media pipeline, CI, generated assets, or local data.
2. Gather local evidence first:
   - logs and recent errors
   - read-only server SQLite queries
   - Android DB copies or test hooks
   - MCP traces for endpoints, screens, settings, DB tables, workers, and logs
   - current source and generated-output diffs
3. Decide the source of truth. Server storage owns persisted identity, media identity, asset IDs, channel IDs, cursors, and ingest state. Do not add client display fallbacks before tracing why real data is missing.
4. Reproduce with the narrowest command that exercises the failing path.
5. Patch the smallest durable cause, then rerun the narrow proof and the required wider lane for touched files.
6. State any verification boundary plainly, especially when unrelated dirty files, stale generated output, active Android builds, or unavailable device evidence block full confirmation.

## CI Failures

Use the GitHub CI skill for GitHub Actions mechanics, but keep these Igloo-specific checks in mind:

- If `main` is red, there may be no PR. Use branch runs rather than assuming a PR surface exists.
- Android CI runs from `android/` with `./test.sh`; use `just test-android` for normal local verification and the exact CI lane only when reproducing CI behavior.
- Reproduce CI-only Android failures with the exact Gradle lane, usually adding `--rerun-tasks --no-daemon` for cold-run parity.
- Treat first failing Android test names as leads, not proof. Async flakes can move between tests.
- Prefer test-harness fixes for CI-only async timing issues before changing production behavior.
- For hangs, use process evidence such as Gradle worker output or `jstack` to identify the blocked test before guessing.

## Android Debugging

- Respect Android build serialization from `AGENTS.md` before running Gradle, KSP, adb, logcat, install, or debug workflows.
- Prefer the named `just` recipes (`test-android`, `build-android`, `build-android-with-server`, and `restart-and-build-android`) unless reproducing a narrow CI Gradle lane.
- Do not reset app data or preferences as a shortcut.
- For sync/media bugs, treat media completeness and sync convergence as data-pipeline issues before UI polish.
- For Room/schema changes, verify migrations and generated schema expectations instead of using destructive fallback.
- For UI behavior, use code, logs, UI tree, or user-provided screenshots first. Do not run repeated screenshot-based device checks unless the user asks.

## Web And Server Debugging

- For web UI bugs, inspect the live DOM before source when possible: element presence, HTML, visibility, layout box, inline style, and classes.
- If an element is absent, trace the render path. If present but hidden, inspect CSS and runtime mutation.
- For feed item API behavior, trace handler, enrichment, bookmark/follow state, templates, JS callers, and Android callers before tightening shared queries.
- For data repairs, use bounded one-time repair tools only when requested or clearly appropriate. Do not put one-off repairs in normal startup paths.

## Generated Drift And Localization

- The old repeated hardcoded-string automation is retired. Treat localization as scoped repo work unless the user asks for another sweep.
- For catalog/resource drift, run the repo generator/checker rather than editing generated output directly.
- Use `just i18n-check` as the durable gate for shared catalog and Android resource generation changes; use `just i18n-sync` only when the generated output needs to be updated.
- If generated files are stale after templ or catalog edits, regenerate first, then judge the source diff and focused tests.
- The named Go recipes provide the writable `GOCACHE` fallback used in this checkout; preserve an explicitly supplied cache path when one is needed for a narrow raw lane.

## Useful Commands

```bash
sqlite3 "file:$HOME/.local/share/igloo/youtube_downloads.db?mode=ro"
just test-go
just restart
just i18n-check
cd android && ./gradlew :app:testDevtestUnitTest --rerun-tasks --no-daemon
```
