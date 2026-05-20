#!/usr/bin/env bash
set -euo pipefail

expected_fingerprint="05DC1C810BD2BC8D1BBD1397AAAC9B802753EA1A"
public_key_path="${1:-.github/release-gpg.pub}"
release_ref_name="${GITHUB_REF_NAME:-}"

if [[ ! "$release_ref_name" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release ref must be a vX.Y.Z tag" >&2
  exit 1
fi

if [[ ! -f "$public_key_path" ]]; then
  echo "release public key not found: $public_key_path" >&2
  exit 1
fi

gpg_home="$(mktemp -d "${RUNNER_TEMP:-/tmp}/igloo-release-gpg.XXXXXX")"
export GNUPGHOME="$gpg_home"
cleanup_gpg_home() {
  gpgconf --kill all >/dev/null 2>&1 || true
  rm -rf "$gpg_home"
}
trap cleanup_gpg_home EXIT

gpg --batch --import "$public_key_path" >/dev/null
actual_fingerprint="$(
  gpg --batch --with-colons --fingerprint "$expected_fingerprint" |
    awk -F: '$1 == "fpr" { print $10; exit }'
)"
if [[ "$actual_fingerprint" != "$expected_fingerprint" ]]; then
  echo "release public key fingerprint mismatch" >&2
  exit 1
fi
printf '%s:6:\n' "$expected_fingerprint" | gpg --batch --import-ownertrust >/dev/null

git fetch --force origin \
  "refs/heads/main:refs/remotes/origin/main" \
  "refs/tags/${release_ref_name}:refs/tags/${release_ref_name}"

tag_type="$(git cat-file -t "refs/tags/${release_ref_name}" 2>/dev/null || true)"
if [[ "$tag_type" != "tag" ]]; then
  echo "release ref must be an annotated signed tag" >&2
  exit 1
fi

git tag -v "$release_ref_name"

tag_target="$(git rev-list -n1 "refs/tags/${release_ref_name}")"
head_commit="$(git rev-parse HEAD)"
if [[ "$tag_target" != "$head_commit" ]]; then
  echo "checked-out commit does not match release tag target" >&2
  exit 1
fi

if ! git merge-base --is-ancestor "$tag_target" origin/main; then
  echo "release tag target must be reachable from origin/main" >&2
  exit 1
fi
