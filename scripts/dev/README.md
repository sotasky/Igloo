# Development Scripts

`scripts/dev/` these are development tools that are not used in runtime, or included in images.

Web assets are bundled by the Go asset builder:

```text
cmd/igloo-assets
```

## Common Commands

| Command | Purpose |
|---|---|
| `build.sh` | Build the Go server and web assets. |
| `build.sh restart` | Build and restart the local server. |
| `build.sh android` | Build the server, then build/install the Android APK. |
| `build.sh all` | Build, restart the server, then build/install Android. |
| `doctor.sh` | Run Igloo Doctor against the local data/config paths. |
| `test-full.sh` | Run workflow pinning, repo-specific Go source checks, generated drift, Go, errcheck, staticcheck, govulncheck, web smoke, and Android test gates. |
| `workflow-pin-check.sh` | Verify GitHub Actions workflow dependencies are pinned to commit SHAs. |
| `web-smoke.sh` | Start a throwaway Igloo server and exercise liveness, setup, login, and the authenticated home redirect. |
| `container-check.sh` | Build and check the container image. |

Maintained diagnostics and repair tools are Go subcommands:

```text
go run ./cmd/igloo-mcp doctor
go run ./cmd/igloo-dev android-sync-maintenance -dry-run
go run ./cmd/igloo-dev asset-inventory-reconcile -limit 1000
go run ./cmd/igloo-dev lifecycle-audit
go run ./cmd/igloo-dev persistence-audit
go run ./cmd/igloo-dev query-audit
go run ./cmd/igloo-dev sqlite-repack
```

The supported browser userscript lives at:

```text
scripts/tampermonkey/igloo-site-sync.user.js
```

Most other files in this directory are maintainer diagnostics or repair helpers,
not first-run instructions for a normal install.
