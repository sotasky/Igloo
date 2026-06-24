# Igloo 

<p align="center">
  <img alt="Igloo" src="static/logo.svg" width="320">
</p>

<p align="center">
  <img alt="License: GPLv3" src="https://img.shields.io/badge/license-GPLv3-blue">
  <img alt="Go" src="https://img.shields.io/badge/Go-server-00ADD8?logo=go&logoColor=white">
  <img alt="Kotlin" src="https://img.shields.io/badge/Kotlin-Android-7F52FF?logo=kotlin&logoColor=white">
</p>

![Igloo web app](static/screenshots/igloo.png)

Igloo is an opinionated self-hosted personal social inbox for X, YouTube, TikTok and Instagram written in [Go](https://go.dev/). It pulls content from accounts you can import or freely add, creates an all-in-one site for you to browse their content or configure the server, and optionally lets you access these from an Android app, which is fully usable even when disconnected from the server.  It is not meant to be a complete front-end replacement for these services, it intentionally stays out of any interaction with these platforms, such as posting or commenting. The published image is built with Nix to keep it small, about 200~ MiB compressed and 500~ MiB local image size. You can also build the image yourself, or just install it natively. [Jump to installation](#install)

Any interaction you do on the client, stays in your machine which includes likes, follows or bookmarks. You don't need to log in to your accounts on these platforms, but that can also affect what media the server can fetch, since it uses [yt-dlp](https://github.com/yt-dlp/yt-dlp) and [gallery-dl](https://github.com/mikf/gallery-dl) to download media, you can only go as far as these packages let you go without cookies. On the web UI, you can upload one or more cookie files or set the browser with cookies to automatically enable cookies (supported out of the box on native installations on Linux, you would need to mount your browser folder if you want to use it with the image).

![Cookie settings](static/screenshots/cookies.png)

You can import your NewPipe subscriptions directly, or add any subscriptions one by one through the site by an account/post link, or you can use the provided [Tampermonkey script](https://github.com/screwys/Igloo/raw/refs/heads/main/scripts/tampermonkey/igloo-site-sync.user.js) which adds a button to each website that lets you import the account on screen to the server.

Once you import a few subscriptions, you can expand your subscriptions list through Igloo alone. You can enable reposts to get content from accounts you are not directly following, and once they appear on your feed you can follow them, more about this at the next part.

## Features

- Highly optimized, fast to navigate web UI for browsing platforms and managing the server
- Discovery through Igloo alone: follow accounts from posts, reposts, stories
  profile cards, and even handles in descriptions!
- With a redirect extension such as [LibRedirect](https://addons.mozilla.org/en-US/firefox/addon/libredirect/), you can redirect all YouTube videos to the server. Set your instance URL to
  `SERVER_URL/temp`, and YouTube watch links can land on
  `/temp/watch?v=...` for local temporary download and playback. You can also pin the temporary downloads to make them, well, not temporary.
- Both web and app are made to be themeable, you can select from the ready themes, or bring your own custom CSS. 
- 
<img src="static/screenshots/themes.webp" alt="Theme controls" width="100%">
    
- **Feed**: one timeline across followed accounts, with opt-in offline algorithm
  based on interactions and recency. It can show reply chains, lets you be able
  to mute, turn off retweets for an individual account. It also comes with retweet deduplication, a problem in RSS feeds for X, it groups retweets and lists everyone who retweeted in one line. It can also be configured to automatically
  translate tweets, either in bulk or lazily with DeepL, Google Translate,
  kagi-cli or API/self-hosted node. [langdetect](https://github.com/taruti/langdetect) is included to reduce API usage.

<p align="center">
  <img src="static/screenshots/feed.webp" alt="Feed" width="760">
</p>

- **Bookmarks**: This is a powerful feature that lets you create categories and
  optionally set a per-category saving path. This is designed to be featureful
  and convenient, to solve my frustrations with time I spend on saving multiple
  files to different folders. You can save images/videos/gifs with a simple
  standardized format: `[account handle] + [optional label] + [automatically
  assigned number].ext` (file extension) to your pre-configured folder. Accounts
  can be renamed and rename persists, which is handy for artists with multiple
  accounts. As an example, if there is a category named `nature` and we want to
  save photographs of Mt. Everest, what normally would take us `right-click` +
  `save` + type `everest 1`, repeat this thrice more to save all 4 would now take:
  press <kbd>b</kbd> + write <kbd>e</kbd> (`everest` gets auto suggested, press
  <kbd>down</kbd> key) + <kbd>enter</kbd> to select + <kbd>enter</kbd> to save.
  This also takes previously saved posts into account, and handles all media
  formats (`account everest 001.jpg` + `account everest 002.mp4`), and saves
  media in quote too, therefore we can go up to 8. There are
  also hotkeys to quickly select which images to download as well.

- **Moments**: vertical video player for short videos, image slideshows, TikTok
  reposts, and Instagram reposts; also stories, that can be accessed
  from tap on user avatar, and a stories sidebar.

<img src="static/screenshots/moments.png" alt="Moments" width="100%">

- **Videos**: YouTube subtitles, speed control, SponsorBlock, DeArrow, comments,
  preview thumbnails, and synced watch position between clients.
  
  <img src="static/screenshots/video_player.webp" alt="Video Player" width="100%">
 
- **YouTube search**: search and queue downloads from the web UI. Search results
  open Igloo's temp watch page, download the video locally, and then play it.

<img src="static/screenshots/search.webp" alt="YouTube search" width="100%">


### Android App
<p align="center">
  <img src="static/screenshots/android.png" height="800" alt="Android">
  &nbsp;&nbsp;
  <img src="static/screenshots/android_2.png" height="800" alt="Android">
</p>
<p align="center">
  <img src="static/screenshots/android_3.png" height="800" alt="Android">
  &nbsp;&nbsp;
  <img src="static/screenshots/android_4.png" height="800" alt="Android">
</p>

- Designed to be able to run detached from the server with synced items, same feature set with the web where it makes sense
- If server and app are on the same network, you can even use the app without
  internet permission!
- Syncs feed rows, bookmarked and liked items, media assets, playback progress,
  follows, likes, bookmarks, and per-channel settings.
- Queues user actions locally and sends them to the server on the next sync
- You can select how many days you want to keep the new data on the app per platform, you can turn off local storage too

## Install

```bash
IGLOO_VERSION=vX.Y.Z
docker pull "ghcr.io/screwys/igloo:${IGLOO_VERSION}"
docker run -d --name igloo --restart unless-stopped \
  --user "$(id -u):$(id -g)" \
  -p 5001:5001 \
  -v "YOUR_DIRECTORY:/igloo" \
  "ghcr.io/screwys/igloo:${IGLOO_VERSION}"
```

Use `-p 127.0.0.1:5001:5001` instead if you only want same-machine browser access.
If you serve Igloo only over HTTPS, set `IGLOO_SESSION_COOKIE_SECURE=true`.

You can use `latest` or a release tag such as `vX.Y.Z`. The `--user` flag keeps bind-mounted files owned by your current user. By default, this will create `data` and `config` inside `YOUR_DIRECTORY`. You can configure bookmarks through web; use `/igloo/bookmarks/<folder>` to keep
them under the same folder or reuse one folder for multiple categories. To keep
bookmark archives elsewhere, add `-v "YOUR_BOOKMARKS_DIRECTORY:/bookmarks"` and
use `/bookmarks/<folder>`; make that folder writable by your user with
`sudo chown -R "$(id -u):$(id -g)" YOUR_BOOKMARKS_DIRECTORY`.

Without `--user`, the image runs as the unprivileged user
`10001:10001`; so you would need to make mounted folders writable too.

To build the image locally instead:

```bash
git clone https://github.com/screwys/igloo
cd igloo
mkdir -p igloo
IGLOO_UID="$(id -u)" IGLOO_GID="$(id -g)" docker compose up -d --build
```

Then open Igloo and create the first admin account in the setup screen:

```text
http://<server-ip>:5001
```

By default, data is at:

```text
./igloo/data
./igloo/config
```

## Back Ups

You can enable automatic backups, and can include bookmarks inside as well, these do not store sensitive files. You can later import a single file or the whole .zip to merge/replace the database. There is also a manual full export option, but it includes .env/cookie files which lets you set up the server and make it continue from where it left on another machine just by running `install.sh`, because I am lazy :) 

## Platforms

You can run the server with any combination of the supported platforms and can enable which platforms you want to use during first installation on the web UI. To do that from the cli with the image, add this to the `docker run` command:

```bash
-e IGLOO_ENABLED_PLATFORMS=youtube,tiktok,instagram,twitter
```

## Browser Userscript

The supported
[userscript](https://github.com/screwys/Igloo/raw/refs/heads/main/scripts/tampermonkey/igloo-site-sync.user.js) adds Igloo save/sync actions on X, TikTok, Instagram, and YouTube.

![Tampermonkey import button](static/screenshots/tampermonkey.png)

To contribute, please check [CONTRIBUTING.md](CONTRIBUTING.md). You can also contribute by providing translations.

## Translations

Currently there are only English and Turkish language options. To add a new language (or if you hate my tone), copy `locales/app/en.toml` and translate values while keeping keys unchanged.

## Privacy

See [PRIVACY.md](PRIVACY.md).


## License

[LICENSE](LICENSE)
