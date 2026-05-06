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

ensure_java_home() {
    export JAVA_HOME="${JAVA_HOME:-/usr/lib/jvm/java-26-openjdk}"
    [ -x "$JAVA_HOME/bin/java" ]
}

if ! ensure_java_home; then
    echo "❌ Java not found at $JAVA_HOME"
    echo "   Set JAVA_HOME or install OpenJDK 26: sudo pacman -S jdk-openjdk"
    exit 1
fi

export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$SCRIPT_DIR/.gradle-home}"
export PATH="$JAVA_HOME/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"
mkdir -p "$GRADLE_USER_HOME"

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
    ./gradlew "${test_args[@]}" --no-daemon
    echo "✅ All tests passed!"
    exit 0
fi

if [ "${1:-}" = "compile" ]; then
    echo "🔧 Compiling (no APK, no install)..."
    ./gradlew :app:compileDebugKotlin --no-daemon
    echo "✅ Compile successful!"
    exit 0
fi

if [ "${1:-}" = "apk" ]; then
    echo "📦 Building APK (no install)..."
    ./gradlew clean :app:assembleDebug --no-daemon
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
    ./gradlew "${test_args[@]}" --no-daemon
    echo "✅ Instrumentation tests passed!"
    exit 0
fi

# ── Default: Build APK + Install ──

./gradlew clean :app:assembleDebug --no-daemon
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

adb -s "$adb_serial" install -r --user 0 "$apk_path"
while IFS= read -r user_id; do
    [ "$user_id" = "0" ] && continue
    if adb -s "$adb_serial" shell pm list packages --user "$user_id" 2>/dev/null | grep -qx "package:com.screwy.igloo"; then
        echo "🧹 Removing secondary-profile install for user $user_id..."
        adb -s "$adb_serial" shell pm uninstall --user "$user_id" com.screwy.igloo >/dev/null || true
    fi
done <<EOF
$(adb -s "$adb_serial" shell pm list users | sed -n 's/.*UserInfo{\([0-9][0-9]*\):.*/\1/p')
EOF
echo "🔄 Relaunching app..."
adb -s "$adb_serial" shell am force-stop --user 0 com.screwy.igloo
adb -s "$adb_serial" shell am start --user 0 -n "com.screwy.igloo/.MainActivity" >/dev/null 2>&1 || true
echo ""
echo "✅ Done! App installed and relaunched."
