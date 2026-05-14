#!/usr/bin/env bash
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)" || exit 1
cd "$ROOT" || exit 1
. scripts/dev/go-tool-versions.sh

tmp="$(mktemp -d)" || exit 1
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

status=0

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "$1 is required" >&2
    status=127
  fi
}

require_cmd go
require_cmd python3
if [[ "$status" -ne 0 ]]; then
  exit "$status"
fi

go_json="$tmp/go-test.jsonl"
android_log="$tmp/android-test.log"
android_results="$ROOT/android/app/build/test-results/testDevtestUnitTest"

echo "[workflows] checking GitHub Actions pinning..."
if ! scripts/dev/workflow-pin-check.sh; then
  echo "[workflows] action pinning check failed" >&2
  status=1
fi

echo "[static] running repo-specific Go source checks..."
if ! go run ./scripts/dev/staticcheck; then
  echo "[static] repo-specific Go source checks failed" >&2
  status=1
fi

echo "[drift] checking generated outputs..."
if ! scripts/dev/drift-check.sh; then
  echo "[drift] generated output check failed" >&2
  status=1
fi

echo "[go] running tests..."
go test -json ./... | tee "$go_json"
go_status=${PIPESTATUS[0]}
if [[ "$go_status" -ne 0 ]]; then
  echo "[go] tests failed with exit code $go_status" >&2
  status=1
fi

if ! python3 - "$go_json" <<'PY'
import json
import sys

path = sys.argv[1]
skips = []
bad_lines = 0

with open(path, encoding="utf-8") as fh:
    for line_no, line in enumerate(fh, 1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as exc:
            print(f"[go] invalid JSON at line {line_no}: {exc}", file=sys.stderr)
            bad_lines += 1
            continue
        if event.get("Action") == "skip" and event.get("Test"):
            skips.append((event.get("Package", ""), event["Test"]))

if skips:
    print("[go] skipped tests:", file=sys.stderr)
    for package, test in skips:
        print(f"  {package}\t{test}", file=sys.stderr)

if bad_lines or skips:
    sys.exit(1)
PY
then
  status=1
fi

echo "[go] running errcheck..."
go run "github.com/kisielk/errcheck@${ERRCHECK_VERSION}" ./...
errcheck_status=$?
if [[ "$errcheck_status" -ne 0 ]]; then
  echo "[go] errcheck failed with exit code $errcheck_status" >&2
  status=1
fi

echo "[go] running staticcheck..."
go run "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}" ./...
staticcheck_status=$?
if [[ "$staticcheck_status" -ne 0 ]]; then
  echo "[go] staticcheck failed with exit code $staticcheck_status" >&2
  status=1
fi

echo "[go] running govulncheck..."
go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" ./...
govulncheck_status=$?
if [[ "$govulncheck_status" -ne 0 ]]; then
  echo "[go] govulncheck failed with exit code $govulncheck_status" >&2
  status=1
fi

echo "[web] running smoke test..."
if ! scripts/dev/web-smoke.sh; then
  echo "[web] smoke test failed" >&2
  status=1
fi

echo "[android] running JVM unit tests..."
rm -rf "$android_results"
android/test.sh 2>&1 | tee "$android_log"
android_status=${PIPESTATUS[0]}
if [[ "$android_status" -ne 0 ]]; then
  echo "[android] tests failed with exit code $android_status" >&2
  status=1
fi

if ! python3 - "$android_results" <<'PY'
import glob
import os
import sys
import xml.etree.ElementTree as ET

results_dir = sys.argv[1]
paths = sorted(glob.glob(os.path.join(results_dir, "TEST-*.xml")))
if not paths:
    print(f"[android] no test result XML files found in {results_dir}", file=sys.stderr)
    sys.exit(1)

totals = {"tests": 0, "skipped": 0, "failures": 0, "errors": 0}
bad_suites = []

for path in paths:
    suite = ET.parse(path).getroot()
    name = suite.attrib.get("name", os.path.basename(path))
    values = {}
    for key in totals:
        values[key] = int(suite.attrib.get(key, "0"))
        totals[key] += values[key]
    if values["skipped"] or values["failures"] or values["errors"]:
        bad_suites.append((name, values))

print(
    "[android] XML summary: "
    f"{totals['tests']} tests, {totals['skipped']} skipped, "
    f"{totals['failures']} failures, {totals['errors']} errors"
)

if bad_suites:
    print("[android] non-clean suites:", file=sys.stderr)
    for name, values in bad_suites:
        print(
            f"  {name}: skipped={values['skipped']} "
            f"failures={values['failures']} errors={values['errors']}",
            file=sys.stderr,
        )
    sys.exit(1)
PY
then
  status=1
fi

warnings="$(grep -E '^(w:|WARNING:)' "$android_log" || true)"
if [[ -n "$warnings" ]]; then
  echo "[android] warnings from test output:" >&2
  printf '%s\n' "$warnings" >&2
fi

if [[ "$status" -eq 0 ]]; then
  echo "[test-full] all gates passed"
else
  echo "[test-full] one or more gates failed" >&2
fi
exit "$status"
