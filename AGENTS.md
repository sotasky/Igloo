# Igloo Agent Guide

## Project

- Igloo is a Go/SQLite server with web and Android clients.
- `android/` is the current Android app.
- Runtime/config defaults: native `~/.local/share/igloo/` and `~/.config/igloo/`; container `/igloo/data` and `/igloo/config`; bundled container assets `/app/static`.

## Evidence

- Start from local evidence: files, DB rows, logs, running DOM, device/app state, then code.
- When the Igloo MCP is available, prefer its read-only tools for first-pass
  orientation and runtime evidence: `doctor_status`, `server_query`,
  `db_schema`, `list_logs`, `read_log`, `recent_errors`, `pipeline_status`,
  `android_sync_status`, `identity_media_status`, `trace_endpoint`,
  `trace_page`, `trace_screen`, `trace_data_flow`, and `get_context`. Use raw
  shell commands when MCP is unavailable, missing the needed view, or a result
  needs independent verification.
- Inspect the DB read-only when possible:
  `sqlite3 "file:$HOME/.local/share/igloo/igloo.db?mode=ro"`
- Do not fetch public X, YouTube, TikTok, or Instagram pages when stored identifiers or local data can answer the question.
- Check private runtime material only for existence; mask values as `***` if a format check is unavoidable.

For profile/avatar/banner readiness bugs, prove the data timeline before changing UI:

- When did the content row enter Igloo (`feed_items`, `videos`, source tables)?
- Which stored author, quote, mention, coauthor, or source identity should have created a `channel_profiles` row?
- When did `channel_profiles.fetched_at` change, and when did the avatar/banner file appear on disk?
- Which ingest, seed, profile-worker, or backfill step should have fetched it before the user hovered or opened the page?

If profile media only becomes ready after hover/page render, treat that as a pipeline bug until proven otherwise.

## Coding Rules

- Keep changes scoped. Do not mix unrelated cleanup, formatting, generated churn, or private workflow notes into product work.
- For narrow behavioral fixes, change the owner path directly. Do not preserve
  questionable code by adding parallel recovery layers, compatibility paths,
  startup sweeps, broad backfills, or extra abstractions around it. When the
  diff grows beyond the shape of the bug, stop and reduce the concept count
  before continuing.
- Use generic names in tests, docs, examples, comments, and commits.
  Do not commit real handles, usernames, channel IDs, post IDs, or local data
  values from bug reports or runtime state. Preserve the shape of the case with
  generic equivalents instead, such as `_sample_handle` for a leading-underscore
  X handle.
- Do not clear local state before a network call succeeds.
- Destructive UI actions need product confirmation: Igloo modal on web, Compose `AlertDialog` on Android.
- One-off repair/backfill utilities must not become normal startup behavior.
- Fix root causes, not display-only symptoms.
- If multiple causes are found, fix all in the same pass unless the user narrows the scope.
- Do not invent client-side fallbacks for server-owned identity or ingest-time data before tracing why the real data is missing.
- Do not patch render-time retry, hover-card fetch, or local media serving as the first fix for missing identity/media. First trace why the ingest/profile pipeline failed to prepare that identity when the relevant content was stored.
- Keep status updates factual: what is fixed, what is still broken, and what is being worked on next.

For Go code, protect the success path. Do not allocate rollback journals, diagnostic collections, or per-item bookkeeping on the happy path just to make rare failures easier to unwind. If the affected work can be enumerated again safely, let the error path recompute it and clean up there. Keep explicit rollback state only when side effects are non-idempotent, external, ordered in a way that cannot be rebuilt, or otherwise impossible to reconstruct.

## Test Gates

- Use focused tests while developing or proving a narrow change, but use
  `scripts/dev/test-full.sh` for full-suite completion claims.
- For full-suite verification, do not treat raw `go test ./...` or Android
  `BUILD SUCCESSFUL` output as enough. Check for skipped tests and ignored
  errors explicitly.
- Run `scripts/dev/test-full.sh` for full-suite verification. It runs Go tests
  with JSON output, fails/reports real skipped Go tests, runs
  `go run github.com/kisielk/errcheck@latest ./...`, runs `android/test.sh`,
  inspects Android XML for failures/errors/skips, and reports Kotlin/JVM
  warnings from the Android test output.
- Treat new or high-signal production `errcheck` findings as blockers. If
  existing findings remain, report them plainly with the reason they were not
  fixed.
- For CI-fix work, commit and push the verified fix unless the user explicitly
  asks not to, publishing is unavailable, or the repository state makes a safe
  push impossible. Report the exact blocker when a verified fix cannot be
  published.

## Git Workflow

- Igloo normally publishes directly from the current branch. For "push",
  "lets push", "ship it", or similar requests, commit the intended changes on
  the current branch and push that branch. If the current branch is `main`,
  push directly to `origin/main`.
- Do not create a feature branch, PR, or review branch for Igloo unless the
  user explicitly asks for one, the current branch is not the intended target,
  or repository state makes a direct push unsafe.
- If `origin/main` moved before a direct-main push, fetch and rebase or
  fast-forward the current work onto `origin/main`, then push.
- These Igloo rules override generic GitHub branch/PR publishing defaults.

## Releases

- Releases are manually dispatched from the release workflow with an explicit patch, minor, or major bump.
- Use `.github/scripts/prepare-release.sh` for release preparation and `.github/scripts/create-release-tag.sh` for local signed release commits/tags.
- Release scripts take the user-written summary as input and put it first in the generated notes, then a `Changelog` section with commits since the previous tag.
- Release commits and tags are GPG-signed with `RELEASE_GPG_PRIVATE_KEY` and `RELEASE_GPG_PASSPHRASE`; optional `RELEASE_GIT_USER_NAME` and `RELEASE_GIT_USER_EMAIL` repository variables set the non-secret commit identity.
- Release artifact workflows verify tags against `.github/release-gpg.pub` before accessing release secrets or publishing assets.
- Release APKs and container images publish GitHub artifact attestations; container images are also signed keylessly with cosign.
- Release notes should list the exact commits since the previous tag.

## Server And Web

- Feed-item endpoints in `internal/web/` must return the enriched shape callers expect: `feed.EnrichFeedItems(...)`, bookmark state, subscribe/follow URLs, and every field the caller reads.
- Do not narrow a shared query for one caller if another caller needs the data. Add a separate query.
- For web UI bugs, inspect the live DOM before source: element HTML, computed visibility, layout box, inline style, and classes.
- For missing avatars, banners, names, bios, or hover profile cards, separate presentation bugs from readiness bugs. A presentation fix is valid only when the DB row and cached file already existed before render; otherwise fix the source path: parser, ingest batch, identity seed, profile refresh candidate query, worker queue/backfill, or failed download retry.
- After server, web, static, or component changes that affect the running app, run `scripts/dev/build.sh restart`.
- For Go changes, run `go test ./...`; for full-suite claims, use the
  stricter `scripts/dev/test-full.sh` gate above.

## Android

- Android must render normal UI state without live Igloo server access.
- Room mirrors the documented server schema; schema bumps need migrations in `IglooMigrations`.
- User state belongs in thin side tables joined at read time.
- Cursors are opaque. Server-owned identifiers stay server-owned.
- Sync must converge for the retention window, associated assets, bookmarks, likes, and their assets. Partial sync is not success.
- Retention widening triggers replay/backfill; narrowing prunes; bookmarks and likes survive prune.
- Use project scripts: `android/build.sh`, `android/test.sh`, `scripts/dev/build.sh android`, `scripts/dev/build.sh all`.
- Before committing Android changes, run the focused `android/test.sh <ClassFilter>` proof for the touched area.
- Treat Android JVM final-field mutation warnings as test failures. Replace
  concrete-class mocks with fakes/interfaces rather than adding JVM flags to
  silence the warning.
- Do not run a separate full `android/test.sh` after `scripts/dev/test-full.sh`
  just to duplicate full-suite proof; run it separately when debugging Android
  failures or when Android-only output is needed.
 If any file under `android/` changes, `android/build.sh` is the required final
  Android proof before final response or commit. It builds, installs, and
  relaunches the app on the device. Do not treat `android/test.sh`, a focused
  Gradle test, or `BUILD SUCCESSFUL` from compilation as a substitute. If
  `android/build.sh` cannot run because no device or Android tool is available,
  say that explicitly in the final response.
