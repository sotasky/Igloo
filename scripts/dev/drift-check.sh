#!/usr/bin/env bash
set -euo pipefail

path_prepend_if_dir() {
  if [[ -d "$1" ]]; then
    case ":$PATH:" in
      *":$1:"*) ;;
      *) PATH="$1:$PATH" ;;
    esac
  fi
}

BREW_PREFIX="${HOMEBREW_PREFIX:-}"
if [[ -z "$BREW_PREFIX" ]] && command -v brew >/dev/null 2>&1; then
  BREW_PREFIX="$(brew --prefix 2>/dev/null || true)"
fi

path_prepend_if_dir "$HOME/.deno/bin"
if [[ -n "$BREW_PREFIX" ]]; then
  path_prepend_if_dir "$BREW_PREFIX/sbin"
  path_prepend_if_dir "$BREW_PREFIX/bin"
fi
path_prepend_if_dir /home/linuxbrew/.linuxbrew/sbin
path_prepend_if_dir /home/linuxbrew/.linuxbrew/bin
path_prepend_if_dir /opt/homebrew/sbin
path_prepend_if_dir /opt/homebrew/bin
path_prepend_if_dir "$HOME/go/bin"
path_prepend_if_dir "$HOME/.local/bin"
export PATH

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

snapshot_generated_scope() {
  find internal/components static/js static/css -path static/js/dist -prune -o -type f -print0 \
    | sort -z \
    | xargs -0 sha256sum
}

snapshot_generated_scope > "$tmp/before"

echo "[drift] generating templ components..."
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate

echo "[drift] bundling static assets..."
go run ./cmd/igloo-assets

echo "[drift] checking tracked generated files..."
snapshot_generated_scope > "$tmp/after"
diff -u "$tmp/before" "$tmp/after"

echo "[drift] checking ignored JS bundles were produced..."
for asset in feed.js feed.js.map shorts.js shorts.js.map player.js player.js.map; do
  if [[ ! -s "static/js/dist/$asset" ]]; then
    echo "missing generated asset: static/js/dist/$asset" >&2
    exit 1
  fi
done

echo "[drift] generated outputs are fresh"
