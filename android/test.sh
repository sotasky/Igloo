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

export JAVA_HOME="${JAVA_HOME:-/usr/lib/jvm/java-17-openjdk}"
if [ ! -x "$JAVA_HOME/bin/java" ]; then
    echo "❌ Java not found at $JAVA_HOME"
    echo "   Set JAVA_HOME or install OpenJDK 17: sudo pacman -S jdk17-openjdk"
    exit 1
fi

export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$SCRIPT_DIR/.gradle-home}"
export PATH="$JAVA_HOME/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"
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
