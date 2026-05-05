---
name: igloo-release-hygiene
description: Use when preparing Igloo for public release by cleaning scripts, docs, tests, examples, configs, or agent-facing repo files.
---

# Igloo Release Hygiene

- Keep private workflow files, local absolute paths, generated artifacts, real usernames, real handles, and real platform IDs out of tracked repo content.
- Keep optional tooling recoverable with portable examples or docs instead of committing machine-specific config.
- Do not keep one-off backfills as part of the public surface. If a repair is still relevant, make it automatic, bounded, tested, or document it as maintainer-only.
- Public repo instructions should help contributors and coding agents understand Igloo itself. Put personal workflow preferences in user-level agent config instead.
- Prefer small, current docs over archives of old plans.
