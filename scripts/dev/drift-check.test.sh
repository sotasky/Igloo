#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin" "$TMP/home"

cat >"$TMP/bin/templ" <<'SH'
#!/usr/bin/env bash
echo "templ $*" >>"$DRIFT_TEST_LOG"
if [[ "${1:-}" == "version" ]]; then
  echo "v0.0.0"
  exit 0
fi
echo "drift-check used PATH templ instead of the pinned generator" >&2
exit 42
SH
chmod +x "$TMP/bin/templ"

cat >"$TMP/bin/go" <<'SH'
#!/usr/bin/env bash
echo "go $*" >>"$DRIFT_TEST_LOG"
case "$*" in
  "run github.com/a-h/templ/cmd/templ@v0.3.1020 generate") exit 0 ;;
  "run ./cmd/igloo-assets") exit 0 ;;
esac
echo "unexpected go invocation: $*" >&2
exit 43
SH
chmod +x "$TMP/bin/go"

cat >"$TMP/bin/git" <<'SH'
#!/usr/bin/env bash
echo "git $*" >>"$DRIFT_TEST_LOG"
if [[ "$*" == "diff --exit-code -- internal/components static/js static/css" ]]; then
  exit 0
fi
exec /usr/bin/git "$@"
SH
chmod +x "$TMP/bin/git"

export DRIFT_TEST_LOG="$TMP/drift.log"
export HOME="$TMP/home"
export PATH="$TMP/bin:/usr/bin:/bin"

"$ROOT/scripts/dev/drift-check.sh" >"$TMP/out" 2>"$TMP/err"

if grep -q '^templ generate' "$DRIFT_TEST_LOG"; then
  echo "drift-check executed templ from PATH" >&2
  cat "$DRIFT_TEST_LOG" >&2
  exit 1
fi

if ! grep -q '^go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate$' "$DRIFT_TEST_LOG"; then
  echo "drift-check did not invoke the pinned templ generator" >&2
  cat "$DRIFT_TEST_LOG" >&2
  exit 1
fi
