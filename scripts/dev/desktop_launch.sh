#!/usr/bin/env bash
# Launch a build.sh command from a desktop entry without opening a terminal.
# Usage: desktop_launch.sh <build.sh arg>
set -euo pipefail

ARG="${1:?usage: desktop_launch.sh <restart|android|full|all>}"
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
SCRIPT="$SCRIPT_DIR/build.sh"
LOG_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/igloo"
LOG_FILE="$LOG_DIR/desktop-${ARG}.log"

mkdir -p "$LOG_DIR"

cat >"$LOG_FILE" <<EOF
[$(date --iso-8601=seconds)] starting: $SCRIPT $ARG
EOF

notify-send "Igloo $ARG started" "Logging to $LOG_FILE"

setsid bash -lc '
    script="$1"
    arg="$2"
    log_file="$3"

    "$script" "$arg" >>"$log_file" 2>&1
    status=$?

    if [ "$status" -eq 0 ]; then
        notify-send "Igloo $arg finished" "Build completed successfully"
    else
        notify-send "Igloo $arg failed" "See $log_file"
    fi

    exit "$status"
' bash "$SCRIPT" "$ARG" "$LOG_FILE" >/dev/null 2>&1 &
