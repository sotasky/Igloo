#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: .github/scripts/prepare-release.sh <patch|minor|major> [NOTES_FILE] [SUMMARY]

Updates Igloo release metadata and writes release notes using SUMMARY as the
first release-notes paragraph.
USAGE
}

bump="${1:-}"
notes_file="${2:-release-notes.md}"
summary="${3:-}"

case "$bump" in
  patch|minor|major)
    ;;
  -h|--help|"")
    usage
    exit 0
    ;;
  *)
    echo "bump must be patch, minor, or major" >&2
    exit 1
    ;;
esac

node scripts/dev/release.mjs prepare "$bump" \
  --notes "$notes_file" \
  --description "$summary"
