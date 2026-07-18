# Development Scripts

`scripts/dev/` these are development tools that are not used in runtime, or included in images.

Web assets are bundled by the Go asset builder:

```text
cmd/igloo-assets
```

## Commands

| Command | Purpose |
|---|---|
| `just build` | Build the server and web assets |
| `just restart` | build and restart the local server |
| `just build-android-with-server` | build the server, then build/install the Android APK |
| `just restart-and-build-android` | build, restart the server, then build/install Android |
| `just doctor` | for local data/config paths |
| `igloo-mcp.sh` | build and run the local MCP server |
| `just test` | self-explanatory |
| `just test-web` | start a throwaway server and and do web tests |
| `just check-container` | build and check the container |

## Cli commands

```text
go run ./cmd/igloo-mcp doctor
go run ./cmd/igloo-dev lifecycle-audit
go run ./cmd/igloo-dev persistence-audit
go run ./cmd/igloo-dev query-audit
go run ./cmd/igloo-dev sqlite-repack
```
