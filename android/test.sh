#!/bin/bash
# Igloo Android — JVM unit tests (no device, no app data touched).
#
# Usage:
#   ./test.sh                          # run all devtest JVM unit tests
#   ./test.sh com.screwy.igloo.data.IglooDatabaseTest  # one class
#   ./test.sh 'com.screwy.igloo.data.*'                 # wildcard

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

source "$SCRIPT_DIR/env.sh"
require_java_home
ensure_android_sdk

export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$SCRIPT_DIR/.gradle-home}"
mkdir -p "$GRADLE_USER_HOME"

test_args=(":app:testDevtestUnitTest")
if [ $# -gt 0 ]; then
    test_args+=("--tests" "$@")
fi

echo "🧪 Running unit tests..."
echo "   JAVA_HOME=$JAVA_HOME"
./gradlew "${test_args[@]}" --no-daemon

echo ""
echo "✅ All tests passed!"
