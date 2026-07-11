#!/usr/bin/env sh
# Igloo install script — checks dependencies, creates directories,
# builds binaries, installs systemd services, and enables them.
#
# Usage:
#   scripts/install.sh              — full install
#   scripts/install.sh --check      — dependency check only
#   scripts/install.sh --no-build   — skip Go build (just dirs + services)
#   scripts/install.sh --allow-test-failures
#                                  — continue install even if go test ./... fails
set -eu

# ── Colors ──────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

ok()   { printf "${GREEN}[ok]${RESET}    %s\n" "$1"; }
warn() { printf "${YELLOW}[warn]${RESET}  %s\n" "$1"; }
fail() { printf "${RED}[miss]${RESET}  %s\n" "$1"; }
info() { printf "${CYAN}[info]${RESET}  %s\n" "$1"; }
step() { printf "\n${BOLD}── %s ──${RESET}\n" "$1"; }

# ── Resolve repo root ──────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Verify we're in the right repo
if [ ! -f "$REPO_DIR/go.mod" ] || ! grep -q 'screwys/igloo' "$REPO_DIR/go.mod"; then
    printf "${RED}error:${RESET} cannot find Igloo repo root (expected go.mod at %s)\n" "$REPO_DIR"
    exit 1
fi

TEMPL_MODULE="github.com/a-h/templ"
TEMPL_CMD="$TEMPL_MODULE/cmd/templ"
TEMPL_VERSION="$(awk -v module="$TEMPL_MODULE" '$1 == module { print $2; exit }' "$REPO_DIR/go.mod")"
if [ -z "$TEMPL_VERSION" ]; then
    printf "${RED}error:${RESET} cannot find %s version in %s\n" "$TEMPL_MODULE" "$REPO_DIR/go.mod"
    exit 1
fi

# ── Defaults ───────────────────────────────────────────────────────
HOME_DIR="$(eval echo ~)"
DEFAULT_DATA_DIR="$HOME_DIR/.local/share/igloo"
DATA_DIR="$DEFAULT_DATA_DIR"
CUSTOM_DATA_DIR=false
if [ -n "${IGLOO_DATA_DIR:-}" ]; then
    DATA_DIR="$IGLOO_DATA_DIR"
    CUSTOM_DATA_DIR=true
fi
MEDIA_DIR="${IGLOO_MEDIA_DIR:-}"
CONFIG_DIR="${IGLOO_CONFIG_DIR:-$HOME_DIR/.config/igloo}"
SYSTEMD_DIR="$HOME_DIR/.config/systemd/user"
SERVER_PORT="${IGLOO_PORT:-5001}"
KAGI_ENV_FILE="${KAGI_ENV_FILE:-$CONFIG_DIR/kagi.env}"

# Add user tool directories to PATH for templ, Homebrew packages, and yt-dlp's
# recommended JavaScript runtime. systemd user services do not inherit the
# interactive shell PATH, so keep this list explicit and reuse it below.
path_prepend_if_dir() {
    if [ -d "$1" ]; then
        case ":$PATH:" in
            *":$1:"*) ;;
            *) PATH="$1:$PATH" ;;
        esac
    fi
}

BREW_PREFIX="${HOMEBREW_PREFIX:-}"
if [ -z "$BREW_PREFIX" ] && command -v brew >/dev/null 2>&1; then
    BREW_PREFIX="$(brew --prefix 2>/dev/null || true)"
fi

path_prepend_if_dir "$HOME_DIR/.deno/bin"
if [ -n "$BREW_PREFIX" ]; then
    path_prepend_if_dir "$BREW_PREFIX/sbin"
    path_prepend_if_dir "$BREW_PREFIX/bin"
fi
path_prepend_if_dir /home/linuxbrew/.linuxbrew/sbin
path_prepend_if_dir /home/linuxbrew/.linuxbrew/bin
path_prepend_if_dir /opt/homebrew/sbin
path_prepend_if_dir /opt/homebrew/bin
path_prepend_if_dir "$HOME_DIR/go/bin"
path_prepend_if_dir "$HOME_DIR/.local/bin"
export PATH

SERVICE_PATH=""
service_path_append() {
    case ":$SERVICE_PATH:" in
        *":$1:"*) ;;
        *) SERVICE_PATH="${SERVICE_PATH:+$SERVICE_PATH:}$1" ;;
    esac
}
service_path_append "$HOME_DIR/.local/bin"
service_path_append "$HOME_DIR/go/bin"
service_path_append "$HOME_DIR/.deno/bin"
if [ -n "$BREW_PREFIX" ]; then
    service_path_append "$BREW_PREFIX/bin"
    service_path_append "$BREW_PREFIX/sbin"
fi
service_path_append /home/linuxbrew/.linuxbrew/bin
service_path_append /home/linuxbrew/.linuxbrew/sbin
service_path_append /opt/homebrew/bin
service_path_append /opt/homebrew/sbin
service_path_append /usr/local/bin
service_path_append /usr/bin
service_path_append /bin

CHECK_ONLY=false
SKIP_BUILD=false
ALLOW_TEST_FAILURES=false
for arg in "$@"; do
    case "$arg" in
        --check)               CHECK_ONLY=true ;;
        --no-build)            SKIP_BUILD=true ;;
        --allow-test-failures) ALLOW_TEST_FAILURES=true ;;
    esac
done

# ── Dependency check ──────────────────────────────────────────────
step "Checking dependencies"

MISSING=""
OPTIONAL_MISSING=""

check_required() {
    if command -v "$1" >/dev/null 2>&1; then
        ver=""
        case "$1" in
            go)         ver=" $(go version | sed 's/go version //')" ;;
            yt-dlp)     ver=" $(yt-dlp --version 2>/dev/null)" ;;
            gallery-dl) ver=" $(gallery-dl --version 2>/dev/null)" ;;
            ffmpeg)     ver=" $(ffmpeg -version 2>/dev/null | head -1 | sed 's/ffmpeg version //' | cut -d' ' -f1)" ;;
            nginx)      ver=" $(nginx -v 2>&1 | sed 's/nginx version: //')" ;;
            podman)     ver=" $(podman --version 2>/dev/null | sed 's/podman version //')" ;;
            templ)      ver=" $(templ version 2>/dev/null || echo '?')" ;;
            sqlite3)    ver=" $(sqlite3 --version 2>/dev/null | cut -d' ' -f1)" ;;
        esac
        ok "$1$ver"
    else
        fail "$1 — $2"
        MISSING="$MISSING $1"
    fi
}

check_optional() {
    if command -v "$1" >/dev/null 2>&1; then
        ok "$1 (optional)"
    else
        warn "$1 — $2 (optional)"
        OPTIONAL_MISSING="$OPTIONAL_MISSING $1"
    fi
}

# Required
check_required go        "Go compiler — install from https://go.dev/dl/"
if [ "$CHECK_ONLY" = false ] && command -v go >/dev/null 2>&1 && ! command -v templ >/dev/null 2>&1; then
    info "installing templ $TEMPL_VERSION with go install..."
    go install "$TEMPL_CMD@$TEMPL_VERSION"
fi
check_required templ     "templ code generator — go install $TEMPL_CMD@$TEMPL_VERSION"
check_required yt-dlp    "video downloader — brew install yt-dlp, pip install yt-dlp, or install your distro package"
check_required gallery-dl "image downloader — brew install gallery-dl, pip install gallery-dl, or install your distro package"
check_required ffmpeg    "media processing — brew install ffmpeg or install your distro package"
check_required nginx     "reverse proxy — install nginx with brew or your distro package manager"
check_required sqlite3   "database CLI — brew install sqlite or install your distro package"
check_required ss        "socket statistics — install iproute/iproute2 with your distro package manager"
check_required git       "version control"
check_required deno      "JavaScript runtime for yt-dlp YouTube challenge solving — brew install deno or install your distro package"

# Optional
check_optional kagi      "kagi-cli for translate/search — install from https://github.com/nicholasgasior/kagi-cli"
check_optional adb       "Android debug bridge (for APK install)"
check_optional java      "Java 17+ (for Android builds)"

if [ -n "$MISSING" ]; then
    printf "\n${RED}${BOLD}Missing required dependencies:${RESET}${RED}%s${RESET}\n" "$MISSING"
    printf "Install them and re-run this script.\n"
    if [ "$CHECK_ONLY" = true ]; then
        exit 1
    fi
    printf "\nContinue anyway? [y/N] "
    read -r ans
    case "$ans" in
        [yY]*) ;;
        *)     exit 1 ;;
    esac
fi

if [ "$CHECK_ONLY" = true ]; then
    if [ -n "$OPTIONAL_MISSING" ]; then
        printf "\n${YELLOW}Optional missing:%s${RESET}\n" "$OPTIONAL_MISSING"
    fi
    printf "\n${GREEN}Dependency check complete.${RESET}\n"
    exit 0
fi

# ── Create directories ─────────────────────────────────────────────
step "Creating directories"

create_dir() {
    if [ -d "$1" ]; then
        ok "$1 (exists)"
    else
        mkdir -p "$1"
        ok "$1 (created)"
    fi
}

# Data directories
STATE_ROOT_MARKER="$DATA_DIR/.igloo-state-root"
if [ "$CUSTOM_DATA_DIR" = true ]; then
    if [ ! -d "$DATA_DIR" ]; then
        printf "${RED}error:${RESET} IGLOO_DATA_DIR is unavailable: %s\n" "$DATA_DIR"
        exit 1
    fi
    if [ ! -f "$STATE_ROOT_MARKER" ] || [ -L "$STATE_ROOT_MARKER" ]; then
        printf "${RED}error:${RESET} IGLOO_DATA_DIR is missing %s\n" "$STATE_ROOT_MARKER"
        exit 1
    fi
    ok "$DATA_DIR (state root verified)"
else
    create_dir "$DATA_DIR"
    if [ -L "$STATE_ROOT_MARKER" ] || { [ -e "$STATE_ROOT_MARKER" ] && [ ! -f "$STATE_ROOT_MARKER" ]; }; then
        printf "${RED}error:${RESET} invalid state root marker: %s\n" "$STATE_ROOT_MARKER"
        exit 1
    fi
    if [ ! -f "$STATE_ROOT_MARKER" ]; then
        : > "$STATE_ROOT_MARKER"
        chmod 0644 "$STATE_ROOT_MARKER"
    fi
fi
create_dir "$DATA_DIR/logs"
create_dir "$DATA_DIR/logs/server"
create_dir "$DATA_DIR/logs/android"
create_dir "$DATA_DIR/thumbnails"
create_dir "$DATA_DIR/thumbnails/generated"
create_dir "$DATA_DIR/thumbnails/previews"
create_dir "$DATA_DIR/tmp"
if [ -n "$MEDIA_DIR" ]; then
    if [ ! -d "$MEDIA_DIR" ]; then
        printf "${RED}error:${RESET} IGLOO_MEDIA_DIR must already be mounted: %s\n" "$MEDIA_DIR"
        exit 1
    fi
    if [ ! -f "$MEDIA_DIR/.igloo-media-root" ] || [ -L "$MEDIA_DIR/.igloo-media-root" ]; then
        printf "${RED}error:${RESET} IGLOO_MEDIA_DIR is missing %s/.igloo-media-root\n" "$MEDIA_DIR"
        exit 1
    fi
    ok "$MEDIA_DIR (external media root verified)"
else
    create_dir "$DATA_DIR/media"
fi

# Config directories
create_dir "$CONFIG_DIR"
create_dir "$CONFIG_DIR/cookies"

# Systemd
create_dir "$SYSTEMD_DIR"

# Binary output
create_dir "$REPO_DIR/bin"

# nginx state
create_dir "$HOME_DIR/.local/state/igloo-nginx"
create_dir "$HOME_DIR/.local/state/igloo-nginx/logs"

# ── Optional Kagi auth for systemd ─────────────────────────────────
extract_kagi_session_token() {
    kagi_config="$HOME_DIR/.kagi.toml"
    [ -f "$kagi_config" ] || return 1
    awk '
        /^[[:space:]]*\[auth\][[:space:]]*$/ { in_auth=1; next }
        /^[[:space:]]*\[/ { in_auth=0 }
        in_auth && $0 ~ /^[[:space:]]*session_token[[:space:]]*=/ {
            val=$0
            sub(/^[^=]*=/, "", val)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
            if (substr(val, 1, 1) == "\"" && substr(val, length(val), 1) == "\"") {
                val=substr(val, 2, length(val)-2)
            }
            print val
            exit
        }
    ' "$kagi_config"
}

write_kagi_env_file() {
    command -v kagi >/dev/null 2>&1 || return 0
    if [ -f "$KAGI_ENV_FILE" ]; then
        chmod 600 "$KAGI_ENV_FILE" 2>/dev/null || warn "could not chmod 600 $KAGI_ENV_FILE"
        ok "$KAGI_ENV_FILE (exists)"
        return 0
    fi

    token="${KAGI_SESSION_TOKEN:-}"
    source="environment"
    if [ -z "$token" ]; then
        token="$(extract_kagi_session_token || true)"
        source="$HOME_DIR/.kagi.toml"
    fi

    if [ -n "$token" ]; then
        old_umask="$(umask)"
        umask 077
        printf "KAGI_SESSION_TOKEN=%s\n" "$token" > "$KAGI_ENV_FILE"
        umask "$old_umask"
        ok "$KAGI_ENV_FILE (created from $source)"
    else
        warn "Kagi CLI found, but no session token was found; create $KAGI_ENV_FILE with KAGI_SESSION_TOKEN=..."
    fi
}

write_kagi_env_file

# ── Build Go binaries ──────────────────────────────────────────────
if [ "$SKIP_BUILD" = false ]; then
    step "Building Go binaries"

    export PATH="$HOME_DIR/go/bin:$PATH"
    cd "$REPO_DIR"

    info "templ generate..."
    templ generate

    info "bundling web assets..."
    go run ./cmd/igloo-assets
    ok "static/js/dist"

    info "go build bin/igloo..."
    go build -o bin/igloo ./cmd/igloo/
    ok "bin/igloo"

    info "go build bin/igloo-mcp..."
    go build -o bin/igloo-mcp ./cmd/igloo-mcp/
    ok "bin/igloo-mcp"

    info "go build bin/igloo-import..."
    go build -o bin/igloo-import ./cmd/import/
    ok "bin/igloo-import"

    info "running tests..."
    if go test ./...; then
        ok "go test ./..."
    elif [ "$ALLOW_TEST_FAILURES" = true ]; then
        warn "some tests failed; continuing because --allow-test-failures was passed"
    else
        printf "${RED}error:${RESET} go test ./... failed; re-run with --allow-test-failures to continue anyway\n"
        exit 1
    fi
else
    info "skipping build (--no-build)"
fi

# ── Install systemd services ──────────────────────────────────────
step "Installing systemd services"

prepare_service_file() {
    UNIT_FILE="$SYSTEMD_DIR/$1"
    if [ ! -L "$UNIT_FILE" ]; then
        return 0
    fi

    target="$(readlink "$UNIT_FILE")"
    case "$target" in
        /*) target_path="$target" ;;
        *)  target_path="$(dirname "$UNIT_FILE")/$target" ;;
    esac
    target_dir="$(dirname "$target_path")"
    if [ ! -d "$target_dir" ]; then
        warn "$UNIT_FILE points to missing $target_dir; replacing symlink"
        rm "$UNIT_FILE"
    fi
}

# igloo.service
prepare_service_file igloo.service
STORAGE_MOUNTS="$DATA_DIR"
MEDIA_ENV_LINE=""
if [ -n "$MEDIA_DIR" ]; then
    STORAGE_MOUNTS="$STORAGE_MOUNTS $MEDIA_DIR"
    MEDIA_ENV_LINE="Environment=IGLOO_MEDIA_DIR=$MEDIA_DIR"
fi
cat > "$UNIT_FILE" <<EOF
[Unit]
Description=Igloo server
Wants=network-online.target
After=network-online.target
RequiresMountsFor=$STORAGE_MOUNTS

[Service]
Type=simple
WorkingDirectory=$REPO_DIR

Environment=IGLOO_CONFIG_DIR=$CONFIG_DIR
Environment=IGLOO_DATA_DIR=$DATA_DIR
$MEDIA_ENV_LINE
Environment=IGLOO_REPO_DIR=$REPO_DIR
Environment=IGLOO_PORT=$SERVER_PORT
EnvironmentFile=-$KAGI_ENV_FILE
Environment=PATH=$SERVICE_PATH

ExecStart=$REPO_DIR/bin/igloo

Restart=on-failure
RestartSec=5
TimeoutStopSec=15

[Install]
WantedBy=default.target
EOF
ok "$UNIT_FILE"

# igloo-nginx.service
prepare_service_file igloo-nginx.service
cat > "$UNIT_FILE" <<EOF
[Unit]
Description=Igloo nginx reverse proxy
After=igloo.service
Wants=igloo.service
RequiresMountsFor=$STORAGE_MOUNTS

[Service]
Type=forking
PIDFile=$HOME_DIR/.local/state/igloo-nginx/nginx.pid
ExecStartPre=/usr/bin/nginx -t -c $CONFIG_DIR/nginx.conf
ExecStart=/usr/bin/nginx -c $CONFIG_DIR/nginx.conf
ExecReload=/usr/bin/nginx -s reload -c $CONFIG_DIR/nginx.conf
ExecStop=/usr/bin/nginx -s stop -c $CONFIG_DIR/nginx.conf

Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
ok "$UNIT_FILE"

# ── Enable services ────────────────────────────────────────────────
step "Enabling systemd services"

systemctl --user daemon-reload
ok "daemon-reload"

systemctl --user enable igloo.service
ok "igloo.service enabled"

systemctl --user enable igloo-nginx.service
ok "igloo-nginx.service enabled"

# Lingering lets user services start at boot without login
if ! loginctl show-user "$(whoami)" 2>/dev/null | grep -q "Linger=yes"; then
    info "enabling lingering for $(whoami) (services start at boot)"
    loginctl enable-linger "$(whoami)" 2>/dev/null || warn "could not enable-linger — services won't auto-start at boot"
fi

# ── Config file hints ──────────────────────────────────────────────
step "Post-install checklist"

if [ ! -f "$CONFIG_DIR/nginx.conf" ]; then
    warn "nginx.conf not found at $CONFIG_DIR/nginx.conf"
    info "  copy and edit the template: see ops/nginx/README.md"
fi

if [ ! -f "$CONFIG_DIR/auth_users.json" ]; then
    warn "auth_users.json not found — open Igloo setup to create the first admin"
fi

if [ ! -f "$CONFIG_DIR/server.crt" ] || [ ! -f "$CONFIG_DIR/server.key" ]; then
    info "no TLS certs found — igloo will run HTTP-only on :$SERVER_PORT"
    info "  place server.crt + server.key in $CONFIG_DIR for HTTPS"
fi

# ── Summary ────────────────────────────────────────────────────────
step "Done"
printf "\n"
info "repo:     $REPO_DIR"
info "data:     $DATA_DIR"
info "config:   $CONFIG_DIR"
info "services: $SYSTEMD_DIR/{igloo,igloo-nginx}.service"
info "kagi env: $KAGI_ENV_FILE"
printf "\n"
info "start everything:"
printf "  systemctl --user start igloo igloo-nginx\n"
printf "\n"
info "or use the build script:"
printf "  scripts/dev/build.sh full\n"
printf "\n"
