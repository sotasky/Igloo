#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/../.."

export PATH="$HOME/go/bin:$PATH"

templ generate
go run ./scripts/dev/i18n_sync_catalog
