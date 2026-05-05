#!/usr/bin/env sh
# Build Go binary, optionally restart server and/or build Android.
# Usage:
#   build.sh              — build Go only
#   build.sh restart      — build Go + restart server
#   build.sh android      — build Go + build/install Android APK
#   build.sh all          — build Go + restart + Android
#   build.sh full         — build Go + daemon-reload + rsshub + restart
set -eu

export PATH="$HOME/go/bin:$PATH"

cd "$(dirname "$0")/../.."

# ── Templ ──
echo "[templ] generating..."
templ generate
echo "[templ] ok"

# ── JS ──
echo "[esbuild] bundling..."
node scripts/dev/esbuild.mjs
echo "[esbuild] ok"

# ── Go ──
echo "[go] building..."
go build -o bin/igloo ./cmd/igloo/
go build -o bin/igloo-mcp ./cmd/igloo-mcp/
echo "[go] ok"

case "${1:-}" in
  mcp)
    echo "[mcp] building..."
    go build -o bin/igloo-mcp ./cmd/igloo-mcp/
    echo "[mcp] ok"
    exit 0
    ;;
  restart)
    scripts/dev/restart_igloo.sh
    ;;
  android)
    echo "[android] building..."
    android/build.sh
    ;;
  all)
    scripts/dev/restart_igloo.sh
    echo "[android] building..."
    android/build.sh
    ;;
  full)
    scripts/dev/restart_igloo_full.sh
    ;;
  "")
    ;;
  *)
    echo "usage: build.sh [restart|android|all|full]"
    exit 1
    ;;
esac
