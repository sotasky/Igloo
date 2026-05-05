#!/usr/bin/env bash
# Install user-local desktop launchers that point at this checkout.
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
app_dir="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$app_dir"

write_launcher() {
    local name="$1"
    local comment="$2"
    local icon="$3"
    local arg="$4"
    local path="$app_dir/$5"

    if [ -L "$path" ]; then
        rm -f "$path"
    fi

    cat >"$path" <<EOF
[Desktop Entry]
Name=$name
Comment=$comment
Icon=$icon
Type=Application
Exec=$repo_root/scripts/dev/desktop_launch.sh $arg
Categories=Development;
Terminal=false
EOF
    chmod 0644 "$path"
    echo "installed $path"
}

write_launcher "Igloo Restart" "Build Go + restart server" "system-restart" "restart" "igloo-restart.desktop"
write_launcher "Igloo Full Restart" "Build Go + daemon-reload + rsshub + restart" "system-restart" "full" "igloo-full.desktop"
write_launcher "Igloo Android Build" "Build Go + build/install Android APK" "phone" "android" "igloo-android.desktop"

if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "$app_dir" >/dev/null 2>&1 || true
fi
