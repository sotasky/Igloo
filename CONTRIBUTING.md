# Contributing

Thank you for your interest! I'm looking forward to any kind of feedbacks.

## Native Development

For native development, clone the repo and run:

```bash
scripts/install.sh --check
```

This checks for dependencies. You need Go 1.26 or newer, the `templ` generator, `yt-dlp`,
`gallery-dl`, `ffmpeg`, SQLite, and such.
For server/web development:

```bash
scripts/dev/build.sh          # build Go server and assets
scripts/dev/build.sh restart  # build and restart the local server
go test ./...
```

Then open the local server and finish setup in the browser:

```text
http://127.0.0.1:5001
```

Native installs use these folders by default:

| Path | Contents |
|---|---|
| `~/.local/share/igloo/` | Database, media, thumbnails, logs |
| `~/.config/igloo/` | Auth files, config, platform session files |

More development scripts are listed at
[`scripts/dev/README.md`](scripts/dev/README.md).

## Android

For Android work, keep the local server
schema and the Room mirror in sync.

```bash
android/test.sh <ClassFilter>  # focused JVM test while iterating
android/test.sh                # full JVM unit test suite
android/build.sh               # build, install, and relaunch on a connected device (first device with 192.168.1 in adb devices, convenience for me, you can change that)
android/build.sh apk           # build APK without installing
```

## Configuration

| Variable | Purpose |
|---|---|
| `IGLOO_PORT` | HTTP port, default `5001` |
| `IGLOO_DATA_DIR` | Data directory override |
| `IGLOO_CONFIG_DIR` | Config directory override |
| `IGLOO_REPO_DIR` | Repo/static root override for native installs |
| `IGLOO_ENABLED_PLATFORMS` | Enabled platforms, such as `youtube,tiktok,instagram,twitter`, or `all` |
