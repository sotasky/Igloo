#!/usr/bin/env sh
# Igloo install script — checks dependencies, creates directories,
# builds binaries, installs systemd services, and enables them.
#
# Usage:
#   scripts/install.sh              — full install
#   scripts/install.sh --check      — dependency check only
#   scripts/install.sh --no-build   — skip Go build (just dirs + services)
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

# ── Defaults ───────────────────────────────────────────────────────
HOME_DIR="$(eval echo ~)"
DATA_DIR="${IGLOO_DATA_DIR:-$HOME_DIR/.local/share/igloo}"
CONFIG_DIR="${IGLOO_CONFIG_DIR:-$HOME_DIR/.config/igloo}"
SYSTEMD_DIR="$HOME_DIR/.config/systemd/user"
SERVER_PORT="${IGLOO_PORT:-5001}"
RSSHUB_PORT="${RSSHUB_PORT:-1200}"
RSSHUB_BASE="${RSSHUB_BASE:-http://127.0.0.1:$RSSHUB_PORT}"
RSSHUB_IMAGE="${RSSHUB_IMAGE:-ghcr.io/diygod/rsshub:chromium-bundled}"
RSSHUB_CONTAINER="${RSSHUB_CONTAINER:-rsshub}"
RSSHUB_ENV_FILE="${RSSHUB_ENV_FILE:-$CONFIG_DIR/rsshub.env}"
PODMAN_BIN="$(command -v podman 2>/dev/null || echo /usr/bin/podman)"

# Add Go bin to PATH for templ and other Go-installed tools
export PATH="$HOME_DIR/go/bin:$HOME_DIR/.local/bin:$PATH"

CHECK_ONLY=false
SKIP_BUILD=false
for arg in "$@"; do
    case "$arg" in
        --check)    CHECK_ONLY=true ;;
        --no-build) SKIP_BUILD=true ;;
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
check_required templ     "templ code generator — go install github.com/a-h/templ/cmd/templ@latest"
check_required yt-dlp    "video downloader — pip install yt-dlp or pacman -S yt-dlp"
check_required gallery-dl "image downloader — pip install gallery-dl or pacman -S gallery-dl"
check_required ffmpeg    "media processing — pacman -S ffmpeg"
check_required nginx     "reverse proxy — pacman -S nginx"
check_required podman    "container runtime (for RSSHub) — pacman -S podman"
check_required sqlite3   "database CLI — pacman -S sqlite"
check_required ss        "socket statistics — pacman -S iproute2"
check_required git       "version control"

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
create_dir "$DATA_DIR"
create_dir "$DATA_DIR/logs"
create_dir "$DATA_DIR/logs/server"
create_dir "$DATA_DIR/logs/android"
create_dir "$DATA_DIR/thumbnails"
create_dir "$DATA_DIR/thumbnails/generated"
create_dir "$DATA_DIR/thumbnails/previews"
create_dir "$DATA_DIR/tmp"

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

# ── Build Go binaries ──────────────────────────────────────────────
if [ "$SKIP_BUILD" = false ]; then
    step "Building Go binaries"

    export PATH="$HOME_DIR/go/bin:$PATH"
    cd "$REPO_DIR"

    info "templ generate..."
    templ generate

    info "go build bin/igloo..."
    go build -o bin/igloo ./cmd/igloo/
    ok "bin/igloo"

    info "go build bin/igloo-mcp..."
    go build -o bin/igloo-mcp ./cmd/igloo-mcp/
    ok "bin/igloo-mcp"

    info "running tests..."
    if go test ./... >/dev/null 2>&1; then
        ok "go test ./..."
    else
        warn "some tests failed — run 'go test ./... -v' to investigate"
    fi
else
    info "skipping build (--no-build)"
fi

# ── Install systemd services ──────────────────────────────────────
step "Installing systemd services"

# igloo.service
cat > "$SYSTEMD_DIR/igloo.service" <<EOF
[Unit]
Description=Igloo server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
WorkingDirectory=$REPO_DIR

Environment=IGLOO_CONFIG_DIR=$CONFIG_DIR
Environment=IGLOO_DATA_DIR=$DATA_DIR
Environment=IGLOO_REPO_DIR=$REPO_DIR
Environment=IGLOO_PORT=$SERVER_PORT
Environment=RSSHUB_BASE=$RSSHUB_BASE
Environment=PATH=$HOME_DIR/.local/bin:$HOME_DIR/go/bin:/usr/local/bin:/usr/bin:/bin

ExecStart=$REPO_DIR/bin/igloo

Restart=on-failure
RestartSec=5
TimeoutStopSec=15

[Install]
WantedBy=default.target
EOF
ok "igloo.service"

# rsshub.service
cat > "$SYSTEMD_DIR/rsshub.service" <<EOF
[Unit]
Description=RSSHub feed service
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
Environment=PORT=$RSSHUB_PORT
EnvironmentFile=-$RSSHUB_ENV_FILE
ExecStartPre=-$PODMAN_BIN rm -f $RSSHUB_CONTAINER
ExecStart=$PODMAN_BIN run --name $RSSHUB_CONTAINER --replace --pull=always --env-file $RSSHUB_ENV_FILE -p 127.0.0.1:$RSSHUB_PORT:1200 $RSSHUB_IMAGE
ExecStop=$PODMAN_BIN stop -t 10 $RSSHUB_CONTAINER
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF
ok "rsshub.service"

# igloo-nginx.service
cat > "$SYSTEMD_DIR/igloo-nginx.service" <<EOF
[Unit]
Description=Igloo nginx reverse proxy
After=igloo.service
Wants=igloo.service

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
ok "igloo-nginx.service"

# ── Enable services ────────────────────────────────────────────────
step "Enabling systemd services"

systemctl --user daemon-reload
ok "daemon-reload"

systemctl --user enable igloo.service
ok "igloo.service enabled"

systemctl --user enable rsshub.service
ok "rsshub.service enabled"

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

if [ ! -f "$RSSHUB_ENV_FILE" ]; then
    warn "rsshub.env not found at $RSSHUB_ENV_FILE"
    info "  create it with RSSHub environment variables (can be empty)"
    touch "$RSSHUB_ENV_FILE"
    ok "created empty $RSSHUB_ENV_FILE"
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
info "services: $SYSTEMD_DIR/{igloo,rsshub,igloo-nginx}.service"
printf "\n"
info "start everything:"
printf "  systemctl --user start rsshub igloo igloo-nginx\n"
printf "\n"
info "or use the build script:"
printf "  scripts/dev/build.sh full\n"
printf "\n"
