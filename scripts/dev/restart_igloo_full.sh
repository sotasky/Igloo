#!/usr/bin/env sh
set -eu

echo "[igloo-full] daemon-reload..."
systemctl --user daemon-reload

echo "[igloo-full] restarting rsshub..."
systemctl --user restart rsshub.service 2>/dev/null || true
sleep 2

echo "[igloo-full] stopping igloo..."
systemctl --user stop igloo.service 2>/dev/null || true

# Kill anything still holding port 5001
PID=$(ss -tlnp | grep ':5001 ' | sed -n 's/.*pid=\([0-9]*\).*/\1/p')
if [ -n "$PID" ]; then
  echo "[igloo-full] killing leftover pid $PID on :5001"
  kill "$PID" 2>/dev/null || true
  sleep 1
fi

echo "[igloo-full] starting igloo..."
systemctl --user start igloo.service

sleep 2
if systemctl --user is-active --quiet igloo.service; then
  echo "[igloo-full] running"
else
  echo "[igloo-full] FAILED — check: journalctl --user -u igloo.service -n 30"
  exit 1
fi
