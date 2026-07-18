#!/bin/bash
# Igloo Android Build & Install
# Single :app module. Builds on host (JDK + Gradle), installs via host adb.
#
# Usage:
#   ./build.sh                              # clean + assembleDebug + install on device
#   ./build.sh compile                      # compileDebugKotlin only (no APK)
#   ./build.sh apk                          # clean + assembleDebug, no install
#   ./build.sh test [ClassName]             # devtest JVM unit tests (optionally filtered)
#   ./build.sh androidTest [Class]          # connectedDevtestAndroidTest (devtest build type)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/env.sh"
require_java_home
ensure_android_sdk

export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$SCRIPT_DIR/.gradle-home}"
mkdir -p "$GRADLE_USER_HOME"

if [ "${IGLOO_ANDROID_SCRIPT_LOCK_HELD:-}" != "1" ]; then
    mkdir -p "$SCRIPT_DIR/.gradle-home"
    export IGLOO_ANDROID_SCRIPT_LOCK_HELD=1
    exec flock "$SCRIPT_DIR/.gradle-home/igloo-android.lock" "$SCRIPT_DIR/$(basename "$0")" "$@"
fi

echo "🚀 Igloo Android"
echo "   JAVA_HOME=$JAVA_HOME"
echo "   ANDROID_HOME=$ANDROID_HOME"
cd "$SCRIPT_DIR"

# ── Subcommands ──

if [ "${1:-}" = "test" ]; then
    shift
    test_args=(":app:testDevtestUnitTest")
    if [ $# -gt 0 ]; then
        test_args+=("--tests" "$@")
    fi
    echo "🧪 Running unit tests..."
    ./gradlew "${test_args[@]}"
    echo "✅ All tests passed!"
    exit 0
fi

if [ "${1:-}" = "compile" ]; then
    echo "🔧 Compiling (no APK, no install)..."
    ./gradlew :app:compileDebugKotlin
    echo "✅ Compile successful!"
    exit 0
fi

if [ "${1:-}" = "apk" ]; then
    echo "📦 Building APK (no install)..."
    ./gradlew clean :app:assembleDebug
    echo "✅ Build successful!"
    exit 0
fi

if [ "${1:-}" = "androidTest" ]; then
    shift
    # devtest build type installs alongside the live com.screwy.igloo APK.
    test_args=(":app:connectedDevtestAndroidTest")
    if [ $# -gt 0 ]; then
        test_args+=("-Pandroid.testInstrumentationRunnerArguments.class=$1")
    fi
    echo "🧪 Running instrumentation tests (target: com.screwy.igloo.devtest)..."
    ./gradlew "${test_args[@]}"
    echo "✅ Instrumentation tests passed!"
    exit 0
fi

# ── Default: Build APK + Install ──

./gradlew clean :app:assembleDebug
echo ""
echo "✅ Build successful!"

apk_path="app/build/outputs/apk/debug/app-debug.apk"
if [ ! -f "$apk_path" ]; then
    echo "❌ APK not found at $apk_path"
    exit 1
fi

if ! command -v adb >/dev/null 2>&1; then
    echo "⚠️  adb not found. Skipping install."
    exit 0
fi

echo "📱 Installing APK..."

# Find wireless device (192.168.1.10x); short retry for adb startup.
adb_serial=""
for attempt in 1 2 3 4 5; do
    adb_serial="$(adb devices | awk '$2=="device" && $1 ~ /^192\.168\.1\.10/ {print $1; exit}')"
    [ -n "$adb_serial" ] && break
    sleep 1
done
if [ -z "$adb_serial" ]; then
    echo "❌ No 192.168.1.10x device found. Aborting install."
    exit 1
fi
echo "   Device: $adb_serial"

install_output=""
if ! install_output="$(adb -s "$adb_serial" install -r --user 0 "$apk_path" 2>&1)"; then
    printf '%s\n' "$install_output"
    if [[ "$install_output" == *"INSTALL_FAILED_UPDATE_INCOMPATIBLE"* ]]; then
        echo "❌ Installed APK has a different signature."
        echo "   Install a build signed with the same key, or approve a manual data-reset install."
        exit 1
    else
        exit 1
    fi
else
    printf '%s\n' "$install_output"
fi
echo "🔄 Relaunching app..."
adb -s "$adb_serial" shell am force-stop --user 0 com.screwy.igloo
adb -s "$adb_serial" shell am start --user 0 -n "com.screwy.igloo/.MainActivity" >/dev/null 2>&1 || true
echo ""
echo "✅ Done! App installed and relaunched."
