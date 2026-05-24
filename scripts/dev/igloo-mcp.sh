#!/usr/bin/env sh
# Build and run the Igloo MCP server from the current checkout.
set -eu

ROOT="${IGLOO_PROJECT_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)}"
cd "$ROOT"

mkdir -p bin
go build -o bin/igloo-mcp ./cmd/igloo-mcp
exec ./bin/igloo-mcp "$@"
