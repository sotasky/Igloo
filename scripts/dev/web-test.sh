#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "$1 is required" >&2
    exit 127
  fi
}

require_cmd curl
require_cmd go
require_cmd python3

tmp="$(mktemp -d)"
server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" >/dev/null 2>&1; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

port="$(
  python3 - <<'PY'
import socket

with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
base_url="http://127.0.0.1:${port}"
server_log="$tmp/server.log"

IGLOO_DATA_DIR="$tmp/data" \
IGLOO_CONFIG_DIR="$tmp/config" \
IGLOO_REPO_DIR="$ROOT" \
IGLOO_PORT="$port" \
IGLOO_ENABLED_PLATFORMS=all \
  go run ./cmd/igloo >"$server_log" 2>&1 &
server_pid="$!"

for _ in $(seq 1 60); do
  if curl -fsS "$base_url/api/health/live" >"$tmp/live.json" 2>/dev/null; then
    break
  fi
  if ! kill -0 "$server_pid" >/dev/null 2>&1; then
    cat "$server_log" >&2
    echo "igloo server exited before liveness probe passed" >&2
    exit 1
  fi
  sleep 1
done

if ! grep -q '"status":"live"' "$tmp/live.json"; then
  cat "$tmp/live.json" >&2
  echo "unexpected liveness response" >&2
  exit 1
fi

cookie_jar="$tmp/cookies.txt"
setup_html="$tmp/setup.html"
curl -fsS -c "$cookie_jar" "$base_url/setup" -o "$setup_html"
grep -q 'name="_csrf_token"' "$setup_html"
grep -q 'name="username"' "$setup_html"
grep -q 'name="platforms"' "$setup_html"

csrf="$(
  python3 - "$setup_html" <<'PY'
import html
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
match = re.search(r'name="_csrf_token" value="([^"]+)"', text)
if not match:
    raise SystemExit("missing csrf token")
print(html.unescape(match.group(1)))
PY
)"

post_headers="$tmp/setup-post.headers"
curl -sS -D "$post_headers" -o /dev/null -b "$cookie_jar" -c "$cookie_jar" \
  -X POST "$base_url/setup" \
  --data-urlencode "_csrf_token=$csrf" \
  --data-urlencode "username=smoke_admin" \
  --data-urlencode "password=smoke_password_123" \
  --data-urlencode "password_confirm=smoke_password_123" \
  --data-urlencode "platforms=youtube"

grep -q '^HTTP/.* 303 ' "$post_headers"
grep -qi '^location: /' "$post_headers"
test -s "$tmp/config/auth_users.json"

home_html="$tmp/home.html"
curl -fsSL -b "$cookie_jar" -c "$cookie_jar" "$base_url/" -o "$home_html"
grep -qi '<html' "$home_html"

echo "web test passed at $base_url"
