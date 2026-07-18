---
name: igloo-web-ui-debugging
description: Use when changing or debugging Igloo web UI, static assets, templ components, feed/player/story interactions, hover cards, subtitles, CSS visibility/layout, browser behavior, or server-rendered UI state.
---

# Igloo Web UI Debugging

Inspect the running UI before editing source when a UI symptom is visible or reproducible.

## Flow

1. Identify whether the problem is absence, hidden content, wrong data, wrong layout, stale generated output, or client-side mutation.
2. Inspect the live DOM when possible: element HTML, computed visibility, display, opacity, layout box, classes, inline styles, event handlers, and console errors.
3. If the element is absent, trace the render path through handler, enrichment, templ component, generated output, and JavaScript caller.
4. If the element is present but wrong or hidden, inspect CSS cascade, responsive rules, container dimensions, runtime classes, and media query behavior before changing markup.
5. For feed item surfaces, keep handler responses enriched with bookmark state, follow or subscription URLs, platform/media metadata, and every field the caller reads.
6. For avatars, banners, names, bios, or hover cards, separate presentation bugs from readiness bugs. Patch the UI only when the DB row and cached file already existed before render.
7. Regenerate templ/static assets through `just check-drift` instead of editing generated files directly.

## Common Surfaces

- Feed cards, conversation threads, loaded thread fragments, hover cards, source badges, story tray, moments, fullscreen player, subtitle overlays, and media readiness indicators.
- Server handlers and templates under `internal/web` and `internal/components`.
- Browser JavaScript and CSS under `static`.

## Verification

- After server, web, static, or component changes that affect the running app, run `just restart`.
- For Go handler or template behavior, run focused Go tests and `just test-go` when practical.
- For generated catalog or templ drift, run `just i18n-check` or `just check-drift` and inspect the resulting diff.
- For visual or interaction fixes, give the user the relevant viewport and state to confirm; do not claim visual confirmation yourself.

Useful commands:

```bash
just restart
just test-go-package ./internal/web
just test-go-package ./internal/components
just i18n-check
```
