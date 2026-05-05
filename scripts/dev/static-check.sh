#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if ! command -v semgrep >/dev/null 2>&1; then
  cat >&2 <<'MSG'
semgrep is not installed.

Install it with your preferred package manager, then rerun:
  scripts/dev/static-check.sh

This script uses the repo-local .semgrep.yml rules and does not require Semgrep
registry access.
MSG
  exit 127
fi

semgrep --config "$ROOT/.semgrep.yml" --error --metrics=off \
  --exclude android/.gradle \
  --exclude android/app/build \
  --exclude static/dist \
  "$ROOT"
