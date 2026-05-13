#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
go run ./scripts/dev/asset_inventory_reconcile "$@"
