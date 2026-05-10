#!/bin/bash
# Shared Android build environment helpers.

set -euo pipefail

required_java_major=26
required_android_compile_sdk=36
required_android_build_tools=36.0.0

java_major_version() {
    local java_bin="$1"
    local version_line version

    version_line="$("$java_bin" -version 2>&1 | sed -n '1p')"
    version="$(printf '%s\n' "$version_line" | sed -n 's/.*version "\([^"]*\)".*/\1/p')"
    if [[ "$version" == 1.* ]]; then
        printf '%s\n' "${version#1.}" | cut -d. -f1
    else
        printf '%s\n' "$version" | cut -d. -f1
    fi
}

java_home_from_bin() {
    local java_bin="$1"
    local resolved

    resolved="$(readlink -f "$java_bin" 2>/dev/null || realpath "$java_bin" 2>/dev/null || printf '%s\n' "$java_bin")"
    dirname "$(dirname "$resolved")"
}

candidate_java_homes() {
    if [ -n "${JAVA_HOME:-}" ]; then
        printf '%s\n' "$JAVA_HOME"
    fi

    if command -v java >/dev/null 2>&1; then
        java_home_from_bin "$(command -v java)"
    fi

    printf '%s\n' \
        "$HOME/.sdkman/candidates/java/current" \
        "$HOME/.sdkman/candidates/java/${required_java_major}"* \
        "$HOME/.local/share/jdks/java-${required_java_major}-openjdk" \
        "$HOME/.local/share/jdks/jdk-${required_java_major}" \
        "/usr/lib/jvm/java-${required_java_major}-openjdk" \
        "/usr/lib/jvm/java-latest-openjdk" \
        "/usr/lib/jvm/jdk-${required_java_major}" \
        "/usr/lib/jvm/jdk-${required_java_major}-openjdk" \
        "/opt/homebrew/opt/openjdk@${required_java_major}" \
        "/opt/homebrew/opt/openjdk@${required_java_major}/libexec/openjdk.jdk/Contents/Home" \
        "/usr/local/opt/openjdk@${required_java_major}" \
        "/usr/local/opt/openjdk@${required_java_major}/libexec/openjdk.jdk/Contents/Home"
}

is_required_java_home() {
    local candidate="$1"
    local java_bin="$candidate/bin/java"
    local javac_bin="$candidate/bin/javac"
    local major

    [ -x "$java_bin" ] || return 1
    [ -x "$javac_bin" ] || return 1
    major="$(java_major_version "$java_bin")"
    [ "$major" = "$required_java_major" ]
}

require_java_home() {
    local candidate
    local original_java_home="${JAVA_HOME:-}"

    while IFS= read -r candidate; do
        [ -n "$candidate" ] || continue
        if is_required_java_home "$candidate"; then
            export JAVA_HOME="$candidate"
            export PATH="$JAVA_HOME/bin:$PATH"
            return 0
        fi
    done < <(candidate_java_homes | awk 'NF && !seen[$0]++')

    if [ -n "$original_java_home" ]; then
        echo "❌ JAVA_HOME is set, but it is not a Java ${required_java_major} JDK: $original_java_home"
    else
        echo "❌ Java ${required_java_major} JDK not found."
    fi
    echo "   Set JAVA_HOME to a Java ${required_java_major} JDK, put Java ${required_java_major} on PATH,"
    echo "   or install it with your platform package manager."
    echo "   Examples:"
    echo "     Arch/CachyOS: sudo pacman -S jdk-openjdk"
    echo "     Fedora:       rpm-ostree install java-latest-openjdk-devel"
    echo "     Homebrew:     brew install openjdk@${required_java_major}"
    return 1
}

candidate_android_homes() {
    if [ -n "${ANDROID_HOME:-}" ]; then
        printf '%s\n' "$ANDROID_HOME"
    fi

    if [ -n "${ANDROID_SDK_ROOT:-}" ]; then
        printf '%s\n' "$ANDROID_SDK_ROOT"
    fi

    printf '%s\n' \
        "$HOME/Android/Sdk" \
        "$HOME/Library/Android/sdk" \
        "$HOME/.android/sdk" \
        "/opt/android-sdk" \
        "/usr/local/share/android-sdk" \
        "/usr/lib/android-sdk"
}

select_android_home() {
    local candidate fallback=""

    while IFS= read -r candidate; do
        [ -n "$candidate" ] || continue
        if [ -d "$candidate" ]; then
            printf '%s\n' "$candidate"
            return 0
        fi
        if [ -z "$fallback" ]; then
            fallback="$candidate"
        fi
    done < <(candidate_android_homes | awk 'NF && !seen[$0]++')

    printf '%s\n' "${fallback:-$HOME/Android/Sdk}"
}

sha1_file() {
    local file="$1"

    if command -v sha1sum >/dev/null 2>&1; then
        sha1sum "$file" | awk '{print $1}'
        return 0
    fi

    if command -v shasum >/dev/null 2>&1; then
        shasum -a 1 "$file" | awk '{print $1}'
        return 0
    fi

    echo "❌ sha1sum or shasum is required to verify Android command-line tools."
    return 1
}

require_command() {
    local command_name="$1"
    local help_text="$2"

    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "❌ Missing required command: $command_name"
        echo "   $help_text"
        return 1
    fi
}

android_cmdline_tools_package() {
    case "$(uname -s)" in
        Linux)
            printf '%s %s\n' \
                "commandlinetools-linux-14742923_latest.zip" \
                "48833c34b761c10cb20bcd16582129395d121b27"
            ;;
        Darwin)
            printf '%s %s\n' \
                "commandlinetools-mac-14742923_latest.zip" \
                "cc27cca4b84bfdbc7df17e3d0a01d0c640d8ee71"
            ;;
        *)
            echo "❌ Unsupported platform for Android SDK bootstrap: $(uname -s)"
            return 1
            ;;
    esac
}

download_android_cmdline_tools() {
    local sdkmanager_bin="$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"
    local package_info tools_zip tools_sha1 tools_url tmp_dir actual_sha

    [ -x "$sdkmanager_bin" ] && return 0

    package_info="$(android_cmdline_tools_package)" || return 1
    read -r tools_zip tools_sha1 <<<"$package_info"
    tools_url="https://dl.google.com/android/repository/$tools_zip"

    require_command curl "Install curl, then rerun this script."
    require_command unzip "Install unzip, then rerun this script."

    echo "📦 Android SDK command-line tools not found. Downloading..."
    tmp_dir="$(mktemp -d)"
    mkdir -p "$ANDROID_HOME/cmdline-tools"

    curl -fL --retry 3 --retry-delay 2 -o "$tmp_dir/$tools_zip" "$tools_url"
    actual_sha="$(sha1_file "$tmp_dir/$tools_zip")"
    if [ "$actual_sha" != "$tools_sha1" ]; then
        echo "❌ Android command-line tools checksum mismatch."
        echo "   expected: $tools_sha1"
        echo "   actual:   $actual_sha"
        rm -rf "$tmp_dir"
        return 1
    fi

    unzip -q "$tmp_dir/$tools_zip" -d "$tmp_dir"
    rm -rf "$ANDROID_HOME/cmdline-tools/latest"
    mv "$tmp_dir/cmdline-tools" "$ANDROID_HOME/cmdline-tools/latest"
    rm -rf "$tmp_dir"
}

run_sdkmanager_with_yes() {
    local sdkmanager_bin="$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"
    local status

    set +e
    yes | "$sdkmanager_bin" "$@"
    status="${PIPESTATUS[1]}"
    set -e
    return "$status"
}

ensure_android_sdk() {
    local missing_packages=()

    export ANDROID_HOME="$(select_android_home)"
    export ANDROID_SDK_ROOT="$ANDROID_HOME"
    export PATH="$JAVA_HOME/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"

    download_android_cmdline_tools

    [ -d "$ANDROID_HOME/platforms/android-$required_android_compile_sdk" ] ||
        missing_packages+=("platforms;android-$required_android_compile_sdk")
    [ -d "$ANDROID_HOME/build-tools/$required_android_build_tools" ] ||
        missing_packages+=("build-tools;$required_android_build_tools")
    [ -x "$ANDROID_HOME/platform-tools/adb" ] ||
        missing_packages+=("platform-tools")

    if [ "${#missing_packages[@]}" -eq 0 ]; then
        return 0
    fi

    echo "📦 Installing Android SDK packages: ${missing_packages[*]}"
    run_sdkmanager_with_yes --licenses >/dev/null
    run_sdkmanager_with_yes "${missing_packages[@]}"
}
