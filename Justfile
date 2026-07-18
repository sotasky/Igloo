set default-list

# Build the server binary and generated web assets without restarting it.
build:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/build.sh

# Build the server and restart the local service.
restart:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/build.sh restart

# Build the server, reload its systemd unit, and restart the local service.
restart-daemon:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/build.sh full

# Build server assets and install/relaunch the Android app on a connected device.
build-android-with-server:
    just build
    just build-android

# Build and restart the server, then install/relaunch the Android app.
restart-and-build-android:
    just restart
    just build-android

# Run every repository gate; stale generated files may be regenerated before the check.
test:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/test-full.sh

# Run the Go test suite only.
test-go:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" go test ./...

# Run Go tests for one package, for example: just test-go-package ./internal/web
test-go-package package:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" go test {{ quote(package) }}

# Run Android JVM tests, optionally for one class: just test-android com.example.Test
test-android filter="":
    if [ -n "{{ filter }}" ]; then android/test.sh {{ quote(filter) }}; else android/test.sh; fi

# Build, install, and relaunch the Android app on a connected device.
build-android:
    #!/usr/bin/env bash
    set -euo pipefail
    output="$(mktemp)"
    trap 'rm -f "$output"' EXIT
    if android/build.sh >"$output" 2>&1; then status=0; else status=$?; fi
    cat "$output"
    if (( status != 0 )); then exit "$status"; fi
    if grep -Fq 'Skipping install.' "$output"; then
        printf '%s\n' 'android/build.sh did not install or relaunch the app because adb is unavailable.' >&2
        exit 1
    fi

# Build the Android APK without installing it.
android-apk:
    android/build.sh apk

# Compile Android Kotlin without assembling or installing an APK.
android-compile:
    android/build.sh compile

# Run the throwaway-server web test.
test-web:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/web-test.sh

# Regenerate templ and bundled assets, then fail if tracked generated output changed.
check-drift:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/drift-check.sh

# Validate the SQLite schema and Android Room mirror contract.
check-schema:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/schema-check.sh

# Build an image and exercise its basic container runtime contract.
check-container:
    scripts/dev/container-check.sh

# Verify that the shared catalog and generated Android resources are current.
i18n-check:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" go test ./scripts/dev/i18n_sync_catalog -run TestGeneratedCatalogOutputsAreCurrent -count=1

# Regenerate the shared catalog and Android string resources.
i18n-sync:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" go run ./scripts/dev/i18n_sync_catalog

# Report runtime paths, cache state, and local health through the Igloo doctor.
doctor:
    GOCACHE="${GOCACHE:-/tmp/igloo-go-cache}" scripts/dev/doctor.sh

# Check the working-tree diff for whitespace errors.
diff-check:
    git diff --check

# Create, publish, and dispatch a signed release after an explicit request with a user-written summary.
release bump summary:
    .github/scripts/create-release-tag.sh --push {{ quote(bump) }} {{ quote(summary) }}

# Create a local signed release tag without publishing it.
release-local bump summary:
    .github/scripts/create-release-tag.sh {{ quote(bump) }} {{ quote(summary) }}
