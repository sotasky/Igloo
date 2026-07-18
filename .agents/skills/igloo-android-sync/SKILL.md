---
name: igloo-android-sync
description: Use when changing or debugging Igloo Android sync, Room mirrors, retention/prune behavior, importer output, sync cursors, generated Android schemas, asset convergence, bookmarks, likes, or offline Android data behavior.
---

# Igloo Android Sync

Start from the server contract, then prove the Android mirror converges without live server access.

## Flow

1. Identify the owned surface: server sync API, import/export path, Android importer, Room schema, DAO query, repository state, UI reader, or asset download.
2. Trace the data shape from server storage to Android read model. Treat server-owned identifiers, cursors, content hashes, asset IDs, and sync generation values as opaque client inputs.
3. Preserve offline behavior. Android must render normal state from Room and side tables after sync; do not add live-server fallbacks for missing mirrored data.
4. Keep user state in thin side tables joined at read time. Bookmarks, likes, hidden state, and retention pruning must survive content prune unless product behavior says otherwise.
5. For schema changes, update Room entities, migrations in `IglooMigrations`, generated schema expectations, and any server contract goldens in the same pass.
6. For retention widening, replay or backfill enough data to converge. For narrowing, prune bounded server and Android state without losing bookmarks or likes.
7. Patch the smallest durable cause, then run the narrow proof and the required Android lane.

## Evidence To Gather

- Server sync endpoint request, response, generation, cursor, and golden fixture behavior.
- Relevant server DB rows, preferably read-only.
- Android Room entity, migration, DAO, importer, repository, and UI reader paths.
- Asset rows and downloaded-file expectations for avatars, banners, videos, thumbnails, and story media.
- Test output XML or exact Gradle failure names for Android tests.

## Verification

- For touched Go sync/API code, run the focused Go tests first, then `just test-go` when practical.
- For touched Android sync or Room code, run `just test-android <ClassFilter>` for the focused class.
- Before completing Android behavior changes, run `just build-android`; if device install is unavailable, report that plainly. Use `just test` for a full-suite claim rather than duplicating a full Android test unless Android-only output is needed for debugging.
- For schema or contract drift, run `just check-schema` instead of editing generated output blindly.

Useful commands:

```bash
just test-go-package ./internal/web
just test-go-package ./internal/db
just test-android com.screwy.igloo.data.IglooDatabaseTest
just test-android
just build-android
just test
```
