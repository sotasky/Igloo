#!/usr/bin/env sh
set -eu

echo "[igloo] stopping service..."
systemctl --user stop igloo.service 2>/dev/null || true

# Kill anything still holding port 5001
PID=$(ss -tlnp | grep ':5001 ' | sed -n 's/.*pid=\([0-9]*\).*/\1/p')
if [ -n "$PID" ]; then
  echo "[igloo] killing leftover pid $PID on :5001"
  kill "$PID" 2>/dev/null || true
  sleep 1
fi

echo "[igloo] starting service..."
systemctl --user start igloo.service

sleep 2
if systemctl --user is-active --quiet igloo.service; then
  echo "[igloo] running"
else
  echo "[igloo] FAILED — check: journalctl --user -u igloo.service -n 30"
  exit 1
fi
