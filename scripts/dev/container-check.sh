#!/usr/bin/env bash
set -euo pipefail

runtime="${CONTAINER_RUNTIME:-}"
if [[ -z "$runtime" ]]; then
  if command -v docker >/dev/null 2>&1; then
    runtime=docker
  elif command -v podman >/dev/null 2>&1; then
    runtime=podman
  else
    echo "docker or podman is required" >&2
    exit 1
  fi
fi

image="${IGLOO_CONTAINER_CHECK_IMAGE:-igloo:container-check}"
port="${IGLOO_CONTAINER_CHECK_PORT:-5011}"
name="igloo-container-check-$$"
tmp="$(mktemp -d)"

cleanup() {
  "$runtime" rm -f "$name" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

"$runtime" build -t "$image" .
"$runtime" run --rm "$image" test -f /app/locales/app/en.toml
mkdir -p "$tmp/data" "$tmp/config"

"$runtime" run -d --name "$name" \
  -e IGLOO_ENABLED_PLATFORMS=all \
  -v "$tmp/data:/data" \
  -v "$tmp/config:/config" \
  -p "127.0.0.1:${port}:5001" \
  "$image" >/dev/null

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null; then
    break
  fi
  sleep 1
done

curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null
curl -fsS "http://127.0.0.1:${port}/static/style.css" >/dev/null
setup_html="$("$runtime" exec "$name" curl -fsS -c /tmp/igloo-check-cookies.txt "http://127.0.0.1:5001/setup")"
csrf="$(printf '%s\n' "$setup_html" | sed -n 's/.*name="_csrf_token" value="\([^"]*\)".*/\1/p' | head -n1)"
if [[ -z "$csrf" ]]; then
 echo "setup page did not include CSRF token" >&2
 exit 1
fi
status="$("$runtime" exec "$name" curl -fsS -b /tmp/igloo-check-cookies.txt -c /tmp/igloo-check-cookies.txt \
  --data-urlencode "_csrf_token=$csrf" \
  --data-urlencode "username=check" \
  --data-urlencode "password=check-pass" \
  --data-urlencode "password_confirm=check-pass" \
  --data-urlencode "platforms=youtube" \
  -o /dev/null -w '%{http_code}' \
  "http://127.0.0.1:5001/setup")"
if [[ "$status" != "303" ]]; then
  echo "setup POST returned HTTP $status, want 303" >&2
  exit 1
fi

echo "container check ok on http://127.0.0.1:${port}"
