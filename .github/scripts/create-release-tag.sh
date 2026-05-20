#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: .github/scripts/create-release-tag.sh [--push] <patch|minor|major> SUMMARY

Prepares release metadata, commits it, and creates a signed annotated release
tag. SUMMARY becomes the first paragraph of the GitHub release notes.

Examples:
  .github/scripts/create-release-tag.sh --push minor "Add Android offline sync"
USAGE
}

push_release=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --push)
      push_release=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    -*)
      echo "unknown option: $1" >&2
      usage
      exit 1
      ;;
    *)
      break
      ;;
  esac
done

bump="${1:-}"
summary="${2:-}"

if [[ -z "$bump" || -z "$summary" ]]; then
  usage
  exit 1
fi

case "$bump" in
  patch|minor|major)
    ;;
  *)
    echo "bump must be patch, minor, or major" >&2
    exit 1
    ;;
esac

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "working tree must be clean before creating a release tag" >&2
  exit 1
fi

notes_file="$(mktemp)"
release_output="$(mktemp)"
cleanup() {
  rm -f "$notes_file" "$release_output"
}
trap cleanup EXIT

node scripts/dev/release.mjs prepare "$bump" \
  --notes "$notes_file" \
  --description "$summary" | tee "$release_output"

version="$(awk -F= '$1 == "version" { print $2 }' "$release_output")"
tag="$(awk -F= '$1 == "tag" { print $2 }' "$release_output")"

if [[ -z "$version" || -z "$tag" ]]; then
  echo "release helper did not report version and tag" >&2
  exit 1
fi

git add android/app/build.gradle.kts
git diff --cached --quiet && {
  echo "release metadata did not change" >&2
  exit 1
}
git commit -S -m "chore(release): bump to $version"
git tag -s "$tag" -F "$notes_file"
git show "$tag" --no-patch

if [[ "$push_release" == "1" ]]; then
  git push --atomic origin HEAD:main "refs/tags/$tag"
fi
