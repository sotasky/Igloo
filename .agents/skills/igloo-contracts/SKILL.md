---
name: igloo-contracts
description: Use when changing Igloo server, web, Android sync, or shared API/data contracts.
---

# Igloo Contracts

- The Go server and SQLite database are the source of truth for persisted state, channel identity, media identity, asset IDs, and cursors.
- Web and Android are both active clients. Web often establishes product behavior first; Android should preserve behavior that fits offline sync.
- A successful like/bookmark is a durability signal for content the user actually saw. If persisted feed/body/media/video data is missing afterward, treat that as a capture or persistence contract bug. Do not infer that the item was empty just because the action payload, feed row, or bookmark row is sparse.
- Endpoints returning feed items must provide the enriched shape callers read: feed enrichment, bookmark state, follow or subscription URL resolution, and platform/media metadata.
- Android should mirror required server schema columns, keep local user state in side tables, treat cursors as opaque, and sync until end-of-stream, cancellation, or a real failure.
- Missing identity, media, or ingest data should be fixed at the server/storage boundary before adding renderer fallbacks.
- For failures, regressions, CI triage, or unclear evidence, use the Igloo debugging workflow before changing contracts.
- Use generic fixture names, handles, and IDs in tests, docs, and examples.
