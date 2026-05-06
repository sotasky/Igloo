# Igloo Agent Guide

## Project

- Igloo is a Go/SQLite server with web and Android clients.
- `android/` is the current Android app.
- Runtime/config defaults: native `~/.local/share/igloo/` and `~/.config/igloo/`; container `/data` and `/config`; bundled container assets `/app/static`.

## Evidence

- Start from local evidence: files, DB rows, logs, running DOM, device/app state, then code.
- Inspect the DB read-only when possible:
  `sqlite3 "file:$HOME/.local/share/igloo/igloo.db?mode=ro"`
- Do not fetch public X, YouTube, TikTok, or Instagram pages when stored identifiers or local data can answer the question.
- Check private runtime material only for existence; mask values as `***` if a format check is unavoidable.

## Changes

- Keep changes scoped. Do not mix unrelated cleanup, formatting, generated churn, or private workflow notes into product work.
- Use generic names in tests, docs, examples, comments, and commits.
- Do not clear local state before a network call succeeds.
- Destructive UI actions need product confirmation: Igloo modal on web, Compose `AlertDialog` on Android.
- One-off repair/backfill utilities must not become normal startup behavior.
- Docs-only changes do not need tests unless they alter generated files or documented commands.

## Releases

- Use patch releases for small fixes and minor releases for larger user-visible changes.
- Automatic releases batch every 10 unreleased commits; use `release: minor` in the commit body for larger user-visible batches.
- Release notes should list the exact commits since the previous tag.

## Server And Web

- Feed-item endpoints in `internal/web/` must return the enriched shape callers expect: `feed.EnrichFeedItems(...)`, bookmark state, subscribe/follow URLs, and every field the caller reads.
- Do not narrow a shared query for one caller if another caller needs the data. Add a separate query.
- For web UI bugs, inspect the live DOM before source: element HTML, computed visibility, layout box, inline style, and classes.
- After server, web, static, or component changes that affect the running app, run `scripts/dev/build.sh restart`.
- For Go changes, run `go test ./...`.

## Android

- Android must render normal UI state without live Igloo server access.
- Room mirrors the documented server schema; schema bumps need migrations in `IglooMigrations`.
- User state belongs in thin side tables joined at read time.
- Cursors are opaque. Server-owned identifiers stay server-owned.
- Sync must converge for the retention window, associated assets, bookmarks, likes, and their assets. Partial sync is not success.
- Retention widening triggers replay/backfill; narrowing prunes; bookmarks and likes survive prune.
- Use project scripts: `android/build.sh`, `android/test.sh`, `scripts/dev/build.sh android`, `scripts/dev/build.sh all`.
- Before committing Android changes, run the focused `android/test.sh <ClassFilter>` proof for the touched area. Before pushing Android changes, run full `android/test.sh` unless the user narrows verification.
- Do not reset Android app data or preferences as a debugging shortcut.
