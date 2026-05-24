# Development Scripts

`scripts/dev/` these are development tools that are not used in runtime, or included in images.

Web assets are bundled by the Go asset builder:

```text
cmd/igloo-assets
```

## Commands

| Command | Purpose |
|---|---|
| `build.sh` | Build the server and web assets |
| `build.sh restart` | build and restart the local server |
| `build.sh android` | build the server, then build/install the Android APK |
| `build.sh all` | build, restart the server, then build/install Android |
| `doctor.sh` | for local data/config paths |
| `igloo-mcp.sh` | build and run the local MCP server |
| `test-full.sh` | self-explanatory |
| `web-test.sh` | start a throwaway server and and do web tests |
| `container-check.sh` | build and check the container |

## Cli commands

```text
go run ./cmd/igloo-mcp doctor
go run ./cmd/igloo-dev android-sync-maintenance -dry-run
go run ./cmd/igloo-dev asset-inventory-reconcile -limit 1000
go run ./cmd/igloo-dev lifecycle-audit
go run ./cmd/igloo-dev persistence-audit
go run ./cmd/igloo-dev query-audit
go run ./cmd/igloo-dev sqlite-repack
```
