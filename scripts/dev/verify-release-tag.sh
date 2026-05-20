#!/usr/bin/env bash
set -euo pipefail

exec .github/scripts/verify-signed-release-tag.sh "$@"
