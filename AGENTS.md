# Igloo Agent Guide

## What Igloo Is

Igloo is a personal, single-user system with three clients:

- Server: the source of truth. It is Go plus SQLite and runs on the deployment host.
- Web: the primary client. New product behavior usually starts here.
- Android: the synced offline-first client. It should preserve real user actions while working without the Igloo server.

Web behavior matters. Do not treat Android as the only product truth. Android is not second-class either: it should support actions such as follow, unfollow, like, bookmark, playback progress, and per-channel settings when those actions fit the mobile/offline model.

The app in `android/` is the current Android client in this repo.

## Important Paths

- Native runtime data defaults to `~/.local/share/igloo/`.
- Native config defaults to `~/.config/igloo/`.
- Container runtime data is mounted at `/data`.
- Container config is mounted at `/config`.
- Bundled static assets live under `/app/static` in the container image.

## Evidence First

For server and web work, start from current local evidence:

1. filesystem, DB, and logs
2. current code

For Android work, start from the current app, device-visible behavior, logs, DB
state, and code. Web and Android are both active clients.

Use logs and read-only DB inspection before calling the server API. Use web search only after local evidence is not enough. Avoid fetching Twitter/X, YouTube, TikTok, or Instagram pages when local identifiers or stored data can answer the question.

Read-only DB commands:

```bash
sqlite3 "file:$HOME/.local/share/igloo/igloo.db?mode=ro"
```

For the Android DB, inspect pulled current-app DB copies or test hooks.

If evidence conflicts, keep reading. State which source is authoritative for the task.

## Local Change Rules

- Check private runtime material only for existence. If a format check is unavoidable, mask values as `***`.
- Before git operations that discard local edits or rewrite history, run `git status`, list affected unstaged changes, and get confirmation.
- Use the project scripts for local service rebuilds: `scripts/dev/build.sh restart`, or `scripts/dev/build.sh all` when Android files changed.
- Do not make one-off repair, backfill, or migration utilities part of normal startup paths. If a repair is current product behavior, implement it in the backend with tests.
- Use generic names in tests, comments, docs, and commit messages. Do not expose real usernames, handles, or IDs in repo content.
- Do not clear local state before a network call succeeds.
- Destructive UI actions must use product-owned confirmation dialogs: the Igloo confirmation modal on web, and Compose `AlertDialog` on Android.
- Android must not require live Igloo server access to render normal UI state.

## Contribution Hygiene

- Keep changes scoped to the requested behavior and nearby code.
- Do not mix unrelated formatting, cleanup, or generated-output churn into feature or bug-fix changes.
- The old repeated Igloo i18n hardcoded-string automation is retired; treat localization work as normal scoped repo work unless the user asks for another sweep.
- Keep public docs, tests, comments, and examples generic. Avoid personal paths, private workflow notes, real account names, and real platform IDs.
- Prefer portable examples for optional tooling such as MCP clients, reverse proxies, and container runtimes.

## Server And Web

Feed-item endpoints in `internal/web/` must return the enriched shape their callers expect. Account for:

- `feed.EnrichFeedItems(...)`
- bookmark state
- subscribe/follow URL resolution where needed
- every field the caller reads

Do not tighten a shared query for one caller if that removes data another caller needs. Add a separate query instead.

For web UI bugs, inspect the live DOM before reading source. Confirm the page, element HTML, computed visibility, layout box, inline style, and classes. If an element is present but hidden, look for CSS or runtime mutation. If it is absent, inspect the server render path.

After server, web, static, or component changes that affect the running app, run:

```bash
scripts/dev/build.sh restart
```

## Android

Keep these invariants:

- Room mirrors the server schema column-for-column where the Android docs require it.
- User state lives in thin side tables joined at read time.
- Room schema bumps need real migrations in `IglooMigrations`. Destructive fallback is a last resort, not a shortcut for column adds.
- Cursors are opaque.
- The server owns identifiers such as asset IDs, channel IDs, avatar identity, and media identity.
- Media sync should drain to completion. Transient download failures are not success states.
- The retention window is the product boundary for local content. Do not add arbitrary client caps.

Android sync should converge to:

- every content row inside the active retention window
- every associated asset for that content
- all bookmarked items and their assets
- all liked items and their assets

Transport pagination is fine. Product-level partial sync is not. Delta and manifest loops should continue until `end_of_stream: true`, cancellation, or a real failure. If `next_marker` stops advancing while `end_of_stream` is false, log it as a protocol bug instead of adding a cap.

Retention widening must trigger replay or backfill. Retention narrowing should prune. Bookmarks and likes are protection rules and must survive prune regardless of age.

Sync and media completeness come before UI polish when a real data pipeline issue is found.

Use project scripts for Android work:

```bash
android/build.sh
android/test.sh
scripts/dev/build.sh android
scripts/dev/build.sh all
```

`android/test.sh` is the CI-equivalent JVM unit-test lane and runs the `devtest` variant. Before committing Android changes, run the focused `android/test.sh <ClassFilter>` proof for the touched area; before pushing Android changes, run full `android/test.sh` unless the user explicitly narrows verification.

Do not reset Android app data or user preferences as a debugging shortcut. If a build or install command appears to reset preferences, record the exact command and evidence before changing scripts.

## Coding Rules

- Fix root causes, not display-only symptoms.
- If multiple causes are found, fix all in the same pass unless the user narrows the scope.
- Do not invent client-side fallbacks for server-owned identity or ingest-time data before tracing why the real data is missing.
- Keep status updates factual: what is fixed, what is still broken, and what is being worked on next.

For Go code, protect the success path. Do not allocate rollback journals, diagnostic collections, or per-item bookkeeping on the happy path just to make rare failures easier to unwind. If the affected work can be enumerated again safely, let the error path recompute it and clean up there. Keep explicit rollback state only when side effects are non-idempotent, external, ordered in a way that cannot be rebuilt, or otherwise impossible to reconstruct.

## Build And Test

Run the smallest command that proves the change, then the required project-level command for the touched area.

Common commands:

```bash
go test ./...
scripts/dev/build.sh
scripts/dev/build.sh restart
scripts/dev/build.sh android
scripts/dev/build.sh all
scripts/dev/build.sh full
android/build.sh
android/test.sh
android/test.sh com.screwy.igloo.data.IglooDatabaseTest
android/test.sh 'com.screwy.igloo.data.*'
```

For Go changes, run `go test ./...`.

For Android changes, run the relevant Android test or build command for the scope. If Android files changed and the running app must be updated, use `scripts/dev/build.sh all`.

Docs-only changes do not need tests unless they alter generated files or documented commands.
