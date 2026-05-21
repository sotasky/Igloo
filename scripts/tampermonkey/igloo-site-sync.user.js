// ==UserScript==
// @name         Igloo Site Sync
// @namespace    local.igloo.site.sync
// @version      8.0.34
// @author       screwys
// @description  Follow X, TikTok, Instagram, and YouTube channels in Igloo; includes the full X media workflow.
// @homepageURL  https://github.com/screwys/Igloo
// @supportURL   https://github.com/screwys/Igloo/issues
// @updateURL    https://raw.githubusercontent.com/screwys/Igloo/main/scripts/tampermonkey/igloo-site-sync.user.js
// @downloadURL  https://raw.githubusercontent.com/screwys/Igloo/main/scripts/tampermonkey/igloo-site-sync.user.js
// @match        https://x.com/*
// @match        https://x.co/*
// @match        https://twitter.com/*
// @match        https://api.x.com/*
// @match        https://api.twitter.com/*
// @match        https://www.tiktok.com/*
// @match        https://www.instagram.com/*
// @match        https://www.youtube.com/*
// @grant        GM_xmlhttpRequest
// @grant        GM_getValue
// @grant        GM_setValue
// @grant        GM_deleteValue
// @grant        GM_registerMenuCommand
// @grant        GM_setClipboard
// @grant        unsafeWindow
// @connect      localhost
// @connect      127.0.0.1
// @connect      localhost:8443
// @connect      127.0.0.1:8443
// @connect      *
// @connect      pbs.twimg.com
// @connect      video.twimg.com
// @connect      *.twimg.com
// @run-at       document-start
// ==/UserScript==

(function () {
  "use strict";
  const SCRIPT_VERSION = "8.0.34";

  const SETTINGS = {
    apiBase: "xsync_api_base",
    authToken: "xsync_auth_token", // access token (24h)
    authRefresh: "xsync_auth_refresh", // refresh token (90d, rotated on use)
    authUser: "xsync_auth_user",
    syncToDashboard: "xsync_sync_to_dashboard",
    saveLocal: "xsync_save_local",
    localList: "xsync_local_list",
    handleAliases: "xdl_handle_aliases",
    buttonOverrides: "igloo_sync_button_overrides",
    xDownloads: "igloo_sync_x_downloads",
    xKeyboardShortcuts: "igloo_sync_x_keyboard_shortcuts",
    xCleanup: "igloo_sync_x_cleanup",
  };
  const LEGACY_AUTH_PASSWORD_KEY = "xsync_auth_pass";

  const DEFAULT_API_BASE = "https://localhost:5001";
  const DEFAULT_API_BASE_CANDIDATES = [
    DEFAULT_API_BASE,
    "https://127.0.0.1:5001",
    "https://localhost:8443",
    "https://127.0.0.1:8443",
    "http://127.0.0.1:5001",
    "http://localhost:5001",
  ];
  const BUTTON_SCAN_DEBOUNCE_MS = 350;
  const RESERVED_PATHS = new Set([
    "home",
    "explore",
    "notifications",
    "messages",
    "i",
    "search",
    "settings",
    "compose",
    "tos",
    "privacy",
    "about",
    "help",
    "hashtag",
    "lists",
    "communities",
    "premium",
    "verified",
    "jobs",
  ]);
  const X_COMPOSER_TOOLBAR_BUTTON_SELECTOR = [
    'button[role="button"][aria-label="Add photos or video"]',
    'button[role="button"][data-testid="gifSearchButton"]',
    'button[role="button"][data-testid="grokImgGen"]',
    'button[role="button"][data-testid="createPollButton"]',
    'button[role="button"][aria-label="Add emoji"]',
    'button[role="button"][data-testid="scheduleOption"]',
    'button[role="button"][data-testid="geoButton"]',
    'button[role="button"][data-testid="contentDisclosureButton"]',
  ].join(",");

  function hexToRgb(value) {
    const hex = String(value || "")
      .trim()
      .replace(/^#/, "");
    if (!/^[0-9a-f]{6}$/i.test(hex)) return null;
    return {
      r: parseInt(hex.slice(0, 2), 16),
      g: parseInt(hex.slice(2, 4), 16),
      b: parseInt(hex.slice(4, 6), 16),
    };
  }

  function cssRgbToRgb(value) {
    const match = String(value || "").match(
      /rgba?\(\s*([0-9.]+)[,\s]+([0-9.]+)[,\s]+([0-9.]+)/i,
    );
    if (!match) return null;
    return {
      r: Number(match[1]),
      g: Number(match[2]),
      b: Number(match[3]),
    };
  }

  function colorLuma(value) {
    const rgb = String(value || "")
      .trim()
      .startsWith("#")
      ? hexToRgb(value)
      : cssRgbToRgb(value);
    if (!rgb) return 0;
    return (0.2126 * rgb.r + 0.7152 * rgb.g + 0.0722 * rgb.b) / 255;
  }

  function readableColorForBackground(value) {
    return colorLuma(value) > 0.58 ? "#0f1419" : "#ffffff";
  }

  function isUsableCssColor(value) {
    const color = String(value || "").trim();
    return (
      color &&
      color !== "transparent" &&
      color !== "rgba(0, 0, 0, 0)" &&
      color !== "rgba(0 0 0 / 0)"
    );
  }

  let _iglooThemePalette = null;
  let _iglooThemeFetch = null;
  let _iglooThemeFetchedAt = 0;
  const IGLOO_THEME_CACHE_MS = 30000;

  function prefersLightColorScheme() {
    try {
      return (
        typeof window.matchMedia === "function" &&
        window.matchMedia("(prefers-color-scheme: light)").matches
      );
    } catch (_) {
      return false;
    }
  }

  function themeToken(tokens, key, fallback) {
    const value = tokens && tokens[key];
    return isUsableCssColor(value) ? String(value).trim() : fallback;
  }

  function selectIglooThemeTokens(snapshot) {
    if (!snapshot || typeof snapshot !== "object") return null;
    if (prefersLightColorScheme() && snapshot.light_tokens) {
      return snapshot.light_tokens;
    }
    if (!prefersLightColorScheme() && snapshot.dark_tokens) {
      return snapshot.dark_tokens;
    }
    return snapshot.tokens || null;
  }

  function paletteFromIglooTheme(snapshot) {
    const tokens = selectIglooThemeTokens(snapshot);
    if (!tokens) return null;
    const accent = themeToken(tokens, "accent", "");
    const base = themeToken(tokens, "base", "");
    const text = themeToken(tokens, "text", "");
    if (!accent || !base || !text) return null;
    const surface1 = themeToken(tokens, "surface1", base);
    const dark =
      tokens.dark === true ||
      (tokens.dark !== false && colorLuma(base) <= 0.58);
    return {
      source: "igloo",
      flavor: String(tokens.theme_id || snapshot.theme_id || "igloo"),
      dark,
      colorScheme: String(tokens.color_scheme || snapshot.color_scheme || ""),
      accent,
      onAccent: themeToken(
        tokens,
        "on_accent",
        readableColorForBackground(accent),
      ),
      base,
      mantle: themeToken(tokens, "mantle", base),
      crust: themeToken(tokens, "crust", base),
      surface0: themeToken(tokens, "surface0", base),
      surface1,
      surface2: themeToken(tokens, "surface2", surface1),
      overlay0: themeToken(tokens, "overlay0", text),
      overlay1: themeToken(tokens, "overlay1", text),
      overlay2: themeToken(tokens, "overlay2", text),
      subtext0: themeToken(tokens, "subtext0", text),
      subtext1: themeToken(tokens, "subtext1", text),
      text,
      red: themeToken(tokens, "red", accent),
      maroon: themeToken(tokens, "maroon", accent),
      green: themeToken(tokens, "green", accent),
      yellow: themeToken(tokens, "yellow", accent),
      pink: themeToken(tokens, "pink", accent),
      mauve: themeToken(tokens, "mauve", accent),
      peach: themeToken(tokens, "peach", accent),
      blue: themeToken(tokens, "blue", accent),
      sapphire: themeToken(tokens, "sapphire", accent),
      sky: themeToken(tokens, "sky", accent),
      lavender: themeToken(tokens, "lavender", accent),
    };
  }

  function setIglooThemeSnapshot(snapshot) {
    const palette = paletteFromIglooTheme(snapshot);
    if (!palette) return false;
    _iglooThemePalette = palette;
    _iglooThemeFetchedAt = Date.now();
    return true;
  }

  async function fetchIglooThemePalette(force = false) {
    if (
      !force &&
      _iglooThemePalette &&
      Date.now() - _iglooThemeFetchedAt < IGLOO_THEME_CACHE_MS
    ) {
      return true;
    }
    if (_iglooThemeFetch) return _iglooThemeFetch;
    _iglooThemeFetch = apiRequest("GET", "/api/theme.json", null, true)
      .then((resp) => {
        if (!resp.ok || !setIglooThemeSnapshot(resp.json)) return false;
        return true;
      })
      .finally(() => {
        _iglooThemeFetch = null;
      });
    return _iglooThemeFetch;
  }

  function xThemeOverridesActive() {
    return isXSite() && themeOverridesEnabled() && !!_iglooThemePalette;
  }

  const X_THEME_VAR_KEYS = [
    "accent",
    "on-accent",
    "base",
    "mantle",
    "crust",
    "surface0",
    "surface1",
    "surface2",
    "overlay0",
    "overlay1",
    "overlay2",
    "subtext0",
    "subtext1",
    "text",
    "red",
    "maroon",
    "green",
    "yellow",
    "pink",
    "mauve",
    "peach",
    "blue",
    "sapphire",
    "sky",
    "lavender",
    "border",
  ];

  function xThemeVarsForPalette(palette) {
    return {
      accent: palette.accent,
      "on-accent": palette.onAccent,
      base: palette.base,
      mantle: palette.mantle,
      crust: palette.crust,
      surface0: palette.surface0,
      surface1: palette.surface1,
      surface2: palette.surface2,
      overlay0: palette.overlay0,
      overlay1: palette.overlay1,
      overlay2: palette.overlay2,
      subtext0: palette.subtext0,
      subtext1: palette.subtext1,
      text: palette.text,
      red: palette.red,
      maroon: palette.maroon,
      green: palette.green,
      yellow: palette.yellow,
      pink: palette.pink,
      mauve: palette.mauve,
      peach: palette.peach,
      blue: palette.blue,
      sapphire: palette.sapphire,
      sky: palette.sky,
      lavender: palette.lavender,
      border: palette.surface1,
    };
  }

  function setXThemeVars(root, prefix, palette) {
    for (const [key, value] of Object.entries(xThemeVarsForPalette(palette))) {
      root.style.setProperty(`--igloo-x-${prefix}${key}`, value);
    }
  }

  function clearXThemeVars(root, prefix = "") {
    for (const key of X_THEME_VAR_KEYS) {
      root.style.removeProperty(`--igloo-x-${prefix}${key}`);
    }
  }

  function applyXThemeSettings() {
    if (!isXSite()) return;
    const root = document.documentElement;
    if (!root || !root.style) return;
    const themeEnabled = themeOverridesEnabled();
    const palette = themeEnabled ? _iglooThemePalette : null;
    if (!palette) {
      clearXThemeVars(root);
      clearXThemeVars(root, "control-");
      root.style.setProperty("color-scheme", "light dark");
      const state = themeEnabled ? "unavailable" : "disabled";
      root.setAttribute("data-igloo-x-theme-source", state);
      root.setAttribute("data-igloo-x-theme-flavor", state);
      root.setAttribute("data-igloo-x-control-source", state);
      root.setAttribute("data-igloo-x-control-flavor", state);
      return;
    }
    const controlPalette = palette;
    setXThemeVars(root, "", palette);
    setXThemeVars(root, "control-", controlPalette);
    root.style.setProperty(
      "color-scheme",
      themeEnabled ? (palette.dark ? "dark" : "light") : "light dark",
    );
    root.setAttribute("data-igloo-x-theme-source", palette.source || "site");
    root.setAttribute("data-igloo-x-theme-flavor", palette.flavor || "site");
    root.setAttribute(
      "data-igloo-x-control-source",
      controlPalette.source || "site",
    );
    root.setAttribute(
      "data-igloo-x-control-flavor",
      controlPalette.flavor || "site",
    );
  }

  function normalizeApiBase(value) {
    return String(value || "")
      .trim()
      .replace(/\/+$/, "");
  }

  const getStoredApiBase = () =>
    normalizeApiBase(GM_getValue(SETTINGS.apiBase, ""));
  const getApiBase = () => getStoredApiBase() || DEFAULT_API_BASE;

  function isLocalApiBase(base) {
    try {
      const u = new URL(base);
      const host = u.hostname.toLowerCase();
      return (
        (u.protocol === "http:" || u.protocol === "https:") &&
        (host === "localhost" || host === "127.0.0.1") &&
        (u.port === "5001" || u.port === "8443")
      );
    } catch (_) {
      return false;
    }
  }

  function apiBaseCandidates() {
    const configured = getApiBase();
    const candidates = [configured];
    if (isLocalApiBase(configured)) {
      candidates.push(...DEFAULT_API_BASE_CANDIDATES);
    }
    return Array.from(
      new Set(candidates.map(normalizeApiBase).filter(Boolean)),
    );
  }

  function shouldTryNextApiBase(resp) {
    return (
      resp &&
      (resp.error === "network_error" ||
        resp.error === "timeout" ||
        (resp.status === 400 &&
          /HTTP request to an HTTPS server/i.test(resp.text || "")))
    );
  }
  const getToken = () => (GM_getValue(SETTINGS.authToken, "") || "").trim();
  const getRefresh = () => (GM_getValue(SETTINGS.authRefresh, "") || "").trim();
  const syncToDashboardEnabled = () =>
    !!GM_getValue(SETTINGS.syncToDashboard, true);
  const saveLocalEnabled = () => !!GM_getValue(SETTINGS.saveLocal, true);
  const buttonOverridesEnabled = () => {
    const stored = GM_getValue(SETTINGS.buttonOverrides, null);
    if (stored !== null && stored !== undefined) return !!stored;
    return !!(
      GM_getValue(SETTINGS.xDownloads, true) ||
      GM_getValue(SETTINGS.xKeyboardShortcuts, true)
    );
  };
  const xDownloadsEnabled = () => buttonOverridesEnabled();
  const xKeyboardShortcutsEnabled = () => buttonOverridesEnabled();
  const themeOverridesEnabled = () => !!GM_getValue(SETTINGS.xCleanup, true);
  const pageWindow =
    typeof unsafeWindow !== "undefined" ? unsafeWindow : window;
  const X_MEDIA_CACHE_LIMIT = 500;
  const cachedXImageMediaByTweetId = new Map();
  const cachedXVideoUrlsByTweetId = new Map();
  const cachedXVideoUrlsByMediaId = new Map();

  function currentPlatform() {
    const host = location.hostname.toLowerCase();
    if (
      host === "x.com" ||
      host === "x.co" ||
      host === "twitter.com" ||
      host === "api.x.com" ||
      host === "api.twitter.com"
    )
      return "twitter";
    if (host === "www.tiktok.com" || host === "tiktok.com") return "tiktok";
    if (host === "www.instagram.com" || host === "instagram.com")
      return "instagram";
    if (host === "www.youtube.com" || host === "youtube.com") return "youtube";
    return "";
  }

  function isXSite() {
    return currentPlatform() === "twitter";
  }

  function isXAuthRoute() {
    if (!isXSite()) return false;
    const host = location.hostname.toLowerCase();
    if (host === "api.x.com" || host === "api.twitter.com") return true;
    const path = location.pathname || "/";
    return (
      path === "/login" ||
      path === "/logout" ||
      path === "/account/access" ||
      path.startsWith("/i/flow/") ||
      path.startsWith("/account/")
    );
  }

  // Store login values from a /api/auth/login or /api/auth/refresh response.
  function storeAuthTokens(json) {
    if (!json) return false;
    const access = String(json.access_token || json.token || "");
    if (!access) return false;
    GM_setValue(SETTINGS.authToken, access);
    if (json.refresh_token)
      GM_setValue(SETTINGS.authRefresh, String(json.refresh_token));
    return true;
  }

  function forgetLegacyDashboardPassword() {
    try {
      if (typeof GM_deleteValue === "function") {
        GM_deleteValue(LEGACY_AUTH_PASSWORD_KEY);
        return;
      }
    } catch (_) {}
    GM_setValue(LEGACY_AUTH_PASSWORD_KEY, "");
  }

  let serverHandleSet = null;
  let serverHandleToChannelId = {};

  const notify = (text) => {
    console.log("[IglooSync]", text);
    const display = () => {
      if (!document.body) return false;
      showToast(text);
      return true;
    };
    if (display()) return;
    window.addEventListener("DOMContentLoaded", display, { once: true });
  };

  function isLikelyHandle(value) {
    return /^[A-Za-z0-9_]{1,15}$/.test(value || "");
  }

  function parseHandleFromHref(href) {
    if (!href) return null;
    try {
      const parts = new URL(href, location.origin).pathname
        .split("/")
        .filter(Boolean);
      if (!parts.length) return null;
      const maybe = parts[0];
      if (RESERVED_PATHS.has(maybe.toLowerCase()) || !isLikelyHandle(maybe))
        return null;
      return maybe;
    } catch (_) {
      return null;
    }
  }

  function parseTweetInfoFromHref(href) {
    if (!href) return null;
    const m = String(href).match(/\/([A-Za-z0-9_]+)\/status\/(\d+)/);
    if (!m) return null;
    return {
      url: "https://x.com/" + m[1] + "/status/" + m[2],
      tweetId: m[2],
      handle: m[1],
    };
  }

  function isVideoTwimgMp4Url(url) {
    return /^https:\/\/video\.twimg\.com\/.+\.mp4(?:$|[?#])/i.test(
      String(url || ""),
    );
  }

  function trimCacheMap(map) {
    while (map.size > X_MEDIA_CACHE_LIMIT) {
      const oldest = map.keys().next().value;
      map.delete(oldest);
    }
  }

  function videoMediaIdFromUrl(rawUrl) {
    if (!rawUrl) return "";
    try {
      const url = new URL(rawUrl, location.origin);
      const match = url.pathname.match(
        /\/(?:amplify_video|amplify_video_thumb|ext_tw_video|ext_tw_video_thumb|tweet_video|tweet_video_thumb)\/(\d+)(?:\/|$)/,
      );
      return match ? match[1] : "";
    } catch (_) {
      return "";
    }
  }

  function videoQualityScore(rawUrl) {
    const match = String(rawUrl || "").match(/\/(\d+)x(\d+)\//);
    if (!match) return 0;
    return Number(match[1] || 0) * Number(match[2] || 0);
  }

  function imageExtFromUrl(rawUrl) {
    try {
      const url = new URL(rawUrl, location.origin);
      const format = String(url.searchParams.get("format") || "")
        .trim()
        .toLowerCase();
      if (/^[a-z0-9]+$/.test(format)) return "." + format;
      const pathExt = (url.pathname.match(/\.([A-Za-z0-9]+)$/) || [])[1];
      if (pathExt) return "." + pathExt.toLowerCase();
    } catch (_) {}
    return ".jpg";
  }

  function imageMediaFromXUrl(rawUrl) {
    const src = String(rawUrl || "");
    if (!src.includes("pbs.twimg.com/media")) return null;
    let url;
    try {
      url = new URL(src, location.origin);
    } catch (_) {
      return null;
    }
    const ext = imageExtFromUrl(url.href);
    const format = ext.slice(1) || "jpg";
    const base = url.origin + url.pathname.replace(/\.[A-Za-z0-9]+$/, "");
    return {
      kind: "image",
      url: base + "?format=" + format + "&name=orig",
      ext,
    };
  }

  function cachedVideoUrlsForTweet(tweetId) {
    return cachedXVideoUrlsByTweetId.get(String(tweetId || "")) || [];
  }

  function cachedVideoUrlsForMediaId(mediaId) {
    return cachedXVideoUrlsByMediaId.get(String(mediaId || "")) || [];
  }

  function normalizeVideoUrls(urls) {
    return Array.from(new Set((urls || []).filter(isVideoTwimgMp4Url))).sort(
      (a, b) => videoQualityScore(b) - videoQualityScore(a),
    );
  }

  function rememberCachedVideoUrlsForMediaId(mediaId, urls) {
    const id = String(mediaId || "");
    const nextUrls = normalizeVideoUrls(urls);
    if (!id || !nextUrls.length) return false;

    const existing = cachedVideoUrlsForMediaId(id);
    const merged = normalizeVideoUrls([...nextUrls, ...existing]);
    const changed =
      merged.length !== existing.length ||
      merged.some((url, i) => url !== existing[i]);
    cachedXVideoUrlsByMediaId.delete(id);
    cachedXVideoUrlsByMediaId.set(id, merged);
    trimCacheMap(cachedXVideoUrlsByMediaId);
    return changed;
  }

  function rememberCachedVideoUrls(tweetId, urls) {
    const id = String(tweetId || "");
    const nextUrls = normalizeVideoUrls(urls);
    if (!id || !nextUrls.length) return false;

    const existing = cachedVideoUrlsForTweet(id);
    const merged = normalizeVideoUrls([...nextUrls, ...existing]);
    const changed =
      merged.length !== existing.length ||
      merged.some((url, i) => url !== existing[i]);
    cachedXVideoUrlsByTweetId.delete(id);
    cachedXVideoUrlsByTweetId.set(id, merged);
    trimCacheMap(cachedXVideoUrlsByTweetId);
    return changed;
  }

  function extractVideoUrlsFromText(text) {
    const urls = [];
    const seen = new Set();
    const normalized = String(text || "")
      .replace(/\\u0026/g, "&")
      .replace(/\\\//g, "/")
      .replace(/%3A/gi, ":")
      .replace(/%2F/gi, "/")
      .replace(/%3F/gi, "?")
      .replace(/%3D/gi, "=")
      .replace(/%26/gi, "&")
      .replace(/&amp;/g, "&");
    const re =
      /https?:\/\/video\.twimg\.com\/[^"'\\\s<>]+?\.mp4(?:\?[^"'\\\s<>]*)?/gi;
    let match;
    while ((match = re.exec(normalized))) {
      let url = match[0];
      try {
        url = new URL(url).href;
      } catch (_) {
        continue;
      }
      if (!isVideoTwimgMp4Url(url) || seen.has(url)) continue;
      seen.add(url);
      urls.push(url);
    }
    return urls;
  }

  function cacheVideoUrlsByMediaIdFromText(text) {
    let cached = 0;
    extractVideoUrlsFromText(text).forEach((url) => {
      const mediaId = videoMediaIdFromUrl(url);
      if (rememberCachedVideoUrlsForMediaId(mediaId, [url])) cached += 1;
    });
    return cached;
  }

  function cachedImageMediaForTweet(tweetId) {
    return cachedXImageMediaByTweetId.get(String(tweetId || "")) || [];
  }

  function rememberCachedImageMedia(tweetId, media) {
    const id = String(tweetId || "");
    const next = [];
    const seen = new Set();
    (media || []).forEach((item) => {
      if (!item || item.kind !== "image" || !item.url) return;
      const key = item.url.split("?")[0];
      if (seen.has(key)) return;
      seen.add(key);
      next.push(item);
    });
    if (!id || !next.length) return false;

    const existing = cachedImageMediaForTweet(id);
    const changed =
      next.length !== existing.length ||
      next.some(
        (item, i) =>
          item.url !== existing[i]?.url || item.ext !== existing[i]?.ext,
      );
    cachedXImageMediaByTweetId.delete(id);
    cachedXImageMediaByTweetId.set(id, next);
    trimCacheMap(cachedXImageMediaByTweetId);
    return changed;
  }

  function bestMp4VariantUrls(media) {
    const variants = media?.video_info?.variants;
    if (!Array.isArray(variants)) return [];
    return variants
      .filter(
        (variant) =>
          variant &&
          String(variant.content_type || "").toLowerCase() === "video/mp4" &&
          isVideoTwimgMp4Url(variant.url),
      )
      .slice()
      .sort((a, b) => Number(b.bitrate || 0) - Number(a.bitrate || 0))
      .map((variant) => variant.url);
  }

  function cacheTweetVideoUrls(tweet) {
    const legacy = tweet?.legacy;
    const tweetId = String(tweet?.rest_id || legacy?.id_str || "");
    const media =
      legacy?.extended_entities?.media || legacy?.entities?.media || [];
    if (!tweetId || !Array.isArray(media)) return false;

    const urls = [];
    media.forEach((item) => {
      const mediaType = String(item?.type || "");
      const unavailable =
        String(item?.ext_media_availability?.status || "") === "Unavailable";
      if (
        unavailable ||
        (mediaType !== "video" && mediaType !== "animated_gif")
      ) {
        return;
      }
      const variantUrls = bestMp4VariantUrls(item);
      urls.push(...variantUrls);
      rememberCachedVideoUrlsForMediaId(item?.id_str || item?.id, variantUrls);
    });
    return rememberCachedVideoUrls(tweetId, urls);
  }

  function cacheTweetImageMedia(tweet) {
    const legacy = tweet?.legacy;
    const tweetId = String(tweet?.rest_id || legacy?.id_str || "");
    const media =
      legacy?.extended_entities?.media || legacy?.entities?.media || [];
    if (!tweetId || !Array.isArray(media)) return false;

    const images = [];
    media.forEach((item) => {
      const mediaType = String(item?.type || "");
      const unavailable =
        String(item?.ext_media_availability?.status || "") === "Unavailable";
      if (unavailable || mediaType !== "photo") return;
      const image = imageMediaFromXUrl(item?.media_url_https);
      if (image) images.push(image);
    });
    return rememberCachedImageMedia(tweetId, images);
  }

  function cacheTweetMediaFromApiResponse(body) {
    let json = body;
    let cached = 0;
    if (typeof body === "string") {
      if (!body.trim()) return 0;
      cached += cacheVideoUrlsByMediaIdFromText(body);
      try {
        json = JSON.parse(body);
      } catch (_) {
        return cached;
      }
    }

    const seen = new Set();
    function visit(value) {
      if (!value || typeof value !== "object" || seen.has(value)) return;
      seen.add(value);
      if (value.legacy) {
        const cachedImages = cacheTweetImageMedia(value);
        const cachedVideos = cacheTweetVideoUrls(value);
        if (cachedImages || cachedVideos) cached += 1;
      }
      if (Array.isArray(value)) {
        value.forEach(visit);
        return;
      }
      Object.keys(value).forEach((key) => visit(value[key]));
    }
    visit(json);
    return cached;
  }

  function videoMediaIdFromElement(node) {
    const candidates = [];
    function add(value) {
      if (value) candidates.push(value);
    }
    if (node) {
      add(node.currentSrc);
      add(node.src);
      add(node.poster);
      add(node.getAttribute && node.getAttribute("src"));
      add(node.getAttribute && node.getAttribute("poster"));
    }
    const root =
      (node?.closest &&
        node.closest(
          '[data-testid="videoPlayer"], [data-testid="videoComponent"], [data-testid="tweetPhoto"]',
        )) ||
      node;
    if (root?.querySelectorAll) {
      root.querySelectorAll("video, source, img").forEach((el) => {
        add(el.currentSrc);
        add(el.src);
        add(el.poster);
        add(el.getAttribute("src"));
        add(el.getAttribute("poster"));
      });
    }
    for (const candidate of candidates) {
      const mediaId = videoMediaIdFromUrl(candidate);
      if (mediaId) return mediaId;
    }
    return "";
  }

  function extractHandleFromArticle(article) {
    if (!article) return null;
    const unb = article.querySelector("[data-testid='User-Name']");
    if (unb)
      for (const a of unb.querySelectorAll("a[href]")) {
        const h = parseHandleFromHref(a.getAttribute("href"));
        if (h) return h;
      }
    for (const a of article.querySelectorAll("a[href]")) {
      if ((a.getAttribute("href") || "").includes("/status/")) continue;
      const h = parseHandleFromHref(a.getAttribute("href"));
      if (h) return h;
    }
    return null;
  }

  function extractTweetUrl(article) {
    // Prefer the permalink <a> wrapping <time> — this is always the tweet's
    // own link, not a quoted tweet's.  Fixes copy-link on QRT detail pages.
    const timeLink = article.querySelector('a[href*="/status/"] time');
    if (timeLink) {
      const a = timeLink.closest("a");
      const href = (a && a.getAttribute("href")) || "";
      const info = parseTweetInfoFromHref(href);
      if (info) return info;
    }
    for (const a of article.querySelectorAll('a[href*="/status/"]')) {
      const href = a.getAttribute("href") || "";
      const info = parseTweetInfoFromHref(href);
      if (info) return info;
    }
    return parseTweetInfoFromHref(location.href);
  }

  // ── Local list helpers ──────────────────────────────────────────────────────
  function isInLocalList(handle) {
    const list = GM_getValue(SETTINGS.localList, []);
    return list.some(
      (item) => String(item.handle).toLowerCase() === handle.toLowerCase(),
    );
  }

  function saveLocal(handle) {
    if (!saveLocalEnabled()) return false;
    if (isInLocalList(handle)) return false;
    const list = GM_getValue(SETTINGS.localList, []);
    list.push({
      handle,
      url: `https://x.com/${handle}`,
      added_at: new Date().toISOString(),
      source: "xsync",
    });
    GM_setValue(SETTINGS.localList, list);
    return true;
  }

  function removeLocal(handle) {
    const list = GM_getValue(SETTINGS.localList, []);
    GM_setValue(
      SETTINGS.localList,
      list.filter(
        (item) => String(item.handle).toLowerCase() !== handle.toLowerCase(),
      ),
    );
  }

  // ── API request helper ──────────────────────────────────────────────────────
  let _refreshingToken = null;
  let _resolvedApiBase = "";
  let _resolvingApiBase = null;

  function _requestToApiBase(apiBase, method, path, body, withAuth = true) {
    return new Promise((resolve) => {
      if (!apiBase) {
        resolve({ ok: false, error: "no_api_base" });
        return;
      }
      const headers = { "Content-Type": "application/json" };
      const token = getToken();
      if (withAuth && token) headers.Authorization = `Bearer ${token}`;
      GM_xmlhttpRequest({
        method,
        url: `${apiBase}${path}`,
        headers,
        data: body ? JSON.stringify(body) : undefined,
        timeout: 120000,
        onload: (resp) => {
          let json = null;
          try {
            json = resp.responseText ? JSON.parse(resp.responseText) : null;
          } catch (_) {}
          resolve({
            ok: resp.status >= 200 && resp.status < 300,
            status: resp.status,
            json,
            text: resp.responseText || "",
            apiBase,
          });
        },
        onerror: () => resolve({ ok: false, error: "network_error", apiBase }),
        ontimeout: () => resolve({ ok: false, error: "timeout", apiBase }),
      });
    });
  }

  async function _resolveApiBase() {
    const candidates = apiBaseCandidates();
    if (_resolvedApiBase && candidates.includes(_resolvedApiBase)) {
      return _resolvedApiBase;
    }
    if (candidates.length <= 1) return candidates[0] || "";

    for (const base of candidates) {
      const probe = await _requestToApiBase(
        base,
        "GET",
        "/api/health/live",
        null,
        false,
      );
      if (probe.ok) {
        _resolvedApiBase = base;
        if (getStoredApiBase() !== base) {
          GM_setValue(SETTINGS.apiBase, base);
        }
        return base;
      }
      if (!shouldTryNextApiBase(probe)) break;
    }
    return candidates[0] || "";
  }

  async function getResolvedApiBase() {
    if (!_resolvingApiBase) {
      _resolvingApiBase = _resolveApiBase().finally(() => {
        _resolvingApiBase = null;
      });
    }
    return _resolvingApiBase;
  }

  async function _rawApiRequest(method, path, body, withAuth = true) {
    const apiBase = await getResolvedApiBase();
    return _requestToApiBase(apiBase, method, path, body, withAuth);
  }

  async function _refreshToken() {
    const refresh = getRefresh();
    if (!refresh) return false;
    const r = await _rawApiRequest(
      "POST",
      "/api/auth/refresh",
      { refresh_token: refresh },
      false,
    );
    if (r.ok && storeAuthTokens(r.json)) {
      console.log("[XSync] token rotated");
      return true;
    }
    if (r.status === 401) {
      GM_setValue(SETTINGS.authRefresh, "");
      GM_setValue(SETTINGS.authToken, "");
    }
    return false;
  }

  async function apiRequest(method, path, body, withAuth = true) {
    const resp = await _rawApiRequest(method, path, body, withAuth);
    // Detect expired token: 303 redirect to /login, or 401
    if (
      withAuth &&
      (resp.status === 303 ||
        resp.status === 401 ||
        (resp.ok && resp.json === null && resp.text.includes("<!DOCTYPE")))
    ) {
      if (!_refreshingToken) {
        _refreshingToken = _refreshToken().then((ok) => {
          _refreshingToken = null;
          return ok;
        });
      }
      const refreshed = await _refreshingToken;
      if (refreshed) return _rawApiRequest(method, path, body, withAuth);
      notify("Token expired — use Tampermonkey menu → Log in to server");
    }
    return resp;
  }

  // ── Server handles (for Follow button) ─────────────────────────────────────
  async function fetchServerHandles() {
    const resp = await apiRequest(
      "GET",
      "/api/channels?platform=twitter",
      null,
      true,
    );
    if (!resp.ok) return;
    // Server-side-changes #10: list now wrapped as {channels:[...]}. Fall back
    // to the bare array for older deployments.
    const channels =
      resp.json &&
      (Array.isArray(resp.json.channels)
        ? resp.json.channels
        : Array.isArray(resp.json)
          ? resp.json
          : null);
    if (!channels) return;
    const set = new Set();
    const map = {};
    for (const ch of channels) {
      const channelId = String(ch.channel_id || ch.id || "");
      for (const h of twitterChannelHandleKeys(ch)) {
        set.add(h);
        map[h] = channelId;
      }
    }
    serverHandleSet = set;
    serverHandleToChannelId = map;
    console.log(`[XSync] loaded ${set.size} server handles`);

    // Re-subscribe ghost-followed handles: local follow state absent from server.
    // These happen when syncToServer() failed (e.g. expired token) during a
    // previous session. Safe to retry — server returns 409 if already present.
    if (syncToDashboardEnabled()) {
      const localList = GM_getValue(SETTINGS.localList, []);
      const ghosts = localList.filter((item) => {
        const h = String(item.handle || "").toLowerCase();
        return h && !set.has(h);
      });
      if (ghosts.length) {
        console.log(
          `[XSync] re-subscribing ${ghosts.length} ghost-followed handle(s)`,
        );
        for (const item of ghosts) {
          const r = await apiRequest(
            "POST",
            "/api/subscribe",
            { url: `https://x.com/${item.handle}` },
            true,
          );
          if (r.ok || r.status === 409) {
            const handle = item.handle.toLowerCase();
            const channelId = String(r.json?.channel_id || r.json?.id || "");
            serverHandleSet.add(handle);
            if (channelId) serverHandleToChannelId[handle] = channelId;
            console.log(`[XSync] ghost synced: ${item.handle}`);
          } else {
            console.warn(
              `[XSync] ghost sync failed: ${item.handle} (${r.status})`,
            );
          }
        }
      }
    }

    refreshButtonStates();
  }

  function isSaved(handle) {
    return (
      (serverHandleSet && serverHandleSet.has(handle.toLowerCase())) ||
      isInLocalList(handle)
    );
  }

  function twitterChannelHandleKeys(ch) {
    const keys = [];
    const add = (value) => {
      const h = String(value || "")
        .trim()
        .replace(/^@+/, "");
      if (isLikelyHandle(h)) keys.push(h.toLowerCase());
    };
    const channelId = String(ch.channel_id || ch.id || "").trim();
    const lowerChannelId = channelId.toLowerCase();
    if (lowerChannelId.startsWith("twitter_")) {
      add(channelId.slice("twitter_".length));
    }
    if (lowerChannelId.startsWith("x_")) {
      add(channelId.slice("x_".length));
    }
    add(ch.handle);
    add(ch.source_id);
    try {
      const parts = new URL(String(ch.url || "")).pathname
        .split("/")
        .filter(Boolean);
      if (parts.length > 0) add(parts[0]);
    } catch (_) {}
    return Array.from(new Set(keys));
  }

  // ── DOM media extraction ───────────────────────────────────────────────────
  function findQuoteMediaScopes(article, parentTweetId) {
    const scopes = [];
    const seen = new Set();
    article.querySelectorAll('a[href*="/status/"]').forEach((link) => {
      const info = parseTweetInfoFromHref(link.getAttribute("href") || "");
      if (!info || info.tweetId === parentTweetId || seen.has(info.tweetId))
        return;
      seen.add(info.tweetId);
      const root =
        (link.closest && link.closest('[role="link"]')) ||
        link.parentElement ||
        link;
      scopes.push({ ...info, root });
    });
    return scopes;
  }

  function mediaOwnerForElement(node, article, parentInfo, quoteScopes) {
    const statusLink = node.closest && node.closest('a[href*="/status/"]');
    let genericStatusOwner = null;
    if (statusLink) {
      const owner = parseTweetInfoFromHref(
        statusLink.getAttribute("href") || "",
      );
      if (owner && owner.handle !== "i") return owner;
      genericStatusOwner = owner;
    }
    for (const scope of quoteScopes) {
      if (
        scope.root &&
        scope.root !== article &&
        typeof scope.root.contains === "function" &&
        scope.root.contains(node)
      ) {
        return scope;
      }
    }
    return genericStatusOwner || parentInfo;
  }

  function imageMediaFromElement(img) {
    const src = img.src || img.getAttribute("src") || "";
    return imageMediaFromXUrl(src);
  }

  function cachedMediaItemsForTweet(owner) {
    if (!owner || !owner.tweetId) return [];
    const tweetId = String(owner.tweetId);
    const images = cachedImageMediaForTweet(tweetId).map((media) => ({
      ...media,
      tweetId,
      tweetUrl: owner.url,
    }));
    const videoUrl = cachedVideoUrlsForTweet(tweetId)[0];
    const videos = videoUrl
      ? [
          {
            kind: "video",
            url: videoUrl,
            tweetId,
            tweetUrl: owner.url,
            ext: ".mp4",
          },
        ]
      : [];
    return [...images, ...videos];
  }

  function collectTweetMediaItems(article) {
    const parentInfo = extractTweetUrl(article);
    if (!parentInfo) return [];

    const quoteScopes = findQuoteMediaScopes(article, parentInfo.tweetId);
    const ownerOrder = new Map([[parentInfo.tweetId, 0]]);
    quoteScopes.forEach((scope, i) => ownerOrder.set(scope.tweetId, i + 1));

    const items = [];
    const seenImages = new Set();
    article
      .querySelectorAll('img[src*="pbs.twimg.com/media"]')
      .forEach((img) => {
        const media = imageMediaFromElement(img);
        if (!media) return;
        const owner = mediaOwnerForElement(
          img,
          article,
          parentInfo,
          quoteScopes,
        );
        const imageKey = owner.tweetId + "|" + media.url.split("?")[0];
        if (seenImages.has(imageKey)) return;
        seenImages.add(imageKey);
        items.push({
          ...media,
          tweetId: owner.tweetId,
          tweetUrl: owner.url,
          domOrder: items.length,
        });
      });

    const seenVideos = new Set();
    article
      .querySelectorAll('video, [data-testid="videoPlayer"]')
      .forEach((node) => {
        const owner = mediaOwnerForElement(
          node,
          article,
          parentInfo,
          quoteScopes,
        );
        if (seenVideos.has(owner.tweetId)) return;
        seenVideos.add(owner.tweetId);
        const mediaId = videoMediaIdFromElement(node);
        const videoUrl =
          (mediaId && cachedVideoUrlsForMediaId(mediaId)[0]) ||
          cachedVideoUrlsForTweet(owner.tweetId)[0] ||
          "";
        const item = {
          kind: "video",
          tweetId: owner.tweetId,
          tweetUrl: owner.url,
          mediaId,
          ext: ".mp4",
          domOrder: items.length,
        };
        if (videoUrl) item.url = videoUrl;
        items.push(item);
      });

    function appendCachedMedia(owner) {
      cachedMediaItemsForTweet(owner).forEach((media) => {
        if (media.kind === "image") {
          const imageKey = owner.tweetId + "|" + media.url.split("?")[0];
          if (seenImages.has(imageKey)) return;
          seenImages.add(imageKey);
        } else if (media.kind === "video") {
          if (seenVideos.has(owner.tweetId)) return;
          seenVideos.add(owner.tweetId);
        } else {
          return;
        }
        items.push({ ...media, domOrder: items.length });
      });
    }
    appendCachedMedia(parentInfo);
    quoteScopes.forEach(appendCachedMedia);

    items.sort((a, b) => {
      const ownerA = ownerOrder.has(a.tweetId)
        ? ownerOrder.get(a.tweetId)
        : Number.MAX_SAFE_INTEGER;
      const ownerB = ownerOrder.has(b.tweetId)
        ? ownerOrder.get(b.tweetId)
        : Number.MAX_SAFE_INTEGER;
      return ownerA - ownerB || a.domOrder - b.domOrder;
    });
    return items.map(({ domOrder, ...item }, index) => ({ ...item, index }));
  }

  // ── DL categories + labels ──────────────────────────────────────────────────
  let dlCategories = null;
  let dlLabels = [];

  async function fetchDlCategories() {
    const resp = await apiRequest(
      "GET",
      "/api/bookmark-categories",
      null,
      true,
    );
    // Server-side-changes #10: list now wrapped as {categories:[...]}. Fall
    // back to the bare array for older deployments.
    const cats =
      resp.ok && resp.json
        ? Array.isArray(resp.json.categories)
          ? resp.json.categories
          : Array.isArray(resp.json)
            ? resp.json
            : null
        : null;
    dlCategories = cats ? cats.filter((c) => c.archive_path) : [];
  }

  async function fetchDlLabels() {
    const resp = await apiRequest("GET", "/api/bookmark-labels", null, true);
    if (!resp.ok || !resp.json) return;
    if (Array.isArray(resp.json.labels)) dlLabels = resp.json.labels;
    else if (Array.isArray(resp.json)) dlLabels = resp.json;
  }

  async function getDlNextIndex(handle, label, categoryId) {
    const params = new URLSearchParams({
      handle,
      label,
      category_id: String(categoryId),
    });
    const resp = await apiRequest(
      "GET",
      "/api/tweet-media-next-index?" + params.toString(),
      null,
      true,
    );
    return resp.ok && resp.json && resp.json.next_index
      ? resp.json.next_index
      : 1;
  }

  function _dlLastCategory() {
    return parseInt(GM_getValue("xdl_last_category", "1"), 10) || 1;
  }

  // ── Download history ────────────────────────────────────────────────────
  const DL_HISTORY_KEY = "xdl_history";

  function getDlHistory() {
    return GM_getValue(DL_HISTORY_KEY, {});
  }

  function markDownloaded(tweetId) {
    const h = getDlHistory();
    h[tweetId] = Date.now();
    GM_setValue(DL_HISTORY_KEY, h);
  }

  function isDownloaded(tweetId) {
    return !!getDlHistory()[tweetId];
  }

  // ── Handle aliases (johndoe2 → johndoe1) — server-backed ──────────────────
  let _handleAliasCache = {};
  let _handleAliasCacheLoaded = false;
  let _bookmarkedHandlesSet = new Set();

  async function loadHandleAliasesFromApi() {
    if (_handleAliasCacheLoaded) return;
    try {
      const resp = await apiRequest(
        "GET",
        "/api/bookmark-aliases?include_handles=1",
        null,
        true,
      );
      if (resp.ok && resp.json) {
        _handleAliasCache = {};
        const aliases = resp.json.aliases || resp.json;
        if (Array.isArray(aliases)) {
          for (const a of aliases)
            _handleAliasCache[a.original_handle.toLowerCase()] =
              a.display_alias;
        }
        if (resp.json.bookmarked_handles) {
          _bookmarkedHandlesSet = new Set(
            resp.json.bookmarked_handles.map((h) => h.toLowerCase()),
          );
        }
      }
    } catch (_) {}
    _handleAliasCacheLoaded = true;
    // One-time migration from GM storage to server
    const legacy = GM_getValue(SETTINGS.handleAliases, null);
    if (legacy && typeof legacy === "object" && Object.keys(legacy).length) {
      const aliasList = Object.entries(legacy).map(([k, v]) => ({
        original_handle: k,
        display_alias: v,
      }));
      // Merge into cache
      for (const a of aliasList)
        _handleAliasCache[a.original_handle.toLowerCase()] = a.display_alias;
      apiRequest(
        "POST",
        "/api/bookmark-aliases",
        { aliases: aliasList },
        true,
      ).then((r) => {
        if (r.ok) GM_setValue(SETTINGS.handleAliases, {});
      });
    }
  }

  function resolveHandleAlias(handle) {
    if (!handle) return handle;
    return _handleAliasCache[handle.toLowerCase()] || handle;
  }

  function saveHandleAlias(originalHandle, aliasedTo) {
    if (!originalHandle || !aliasedTo) return;
    const origLower = originalHandle.toLowerCase();
    const aliasLower = aliasedTo.toLowerCase();
    if (origLower === aliasLower) return;
    _handleAliasCache[origLower] = aliasedTo;
    apiRequest(
      "POST",
      "/api/bookmark-aliases",
      {
        aliases: [
          { original_handle: originalHandle, display_alias: aliasedTo },
        ],
      },
      true,
    );
  }

  // ── Styles ─────────────────────────────────────────────────────────────────
  function ensureButtonStyles() {
    if (document.getElementById("x-sync-style")) return;
    const style = document.createElement("style");
    style.id = "x-sync-style";
    style.textContent = `
    :root {
      --igloo-x-accent: rgb(29, 155, 240);
      --igloo-x-on-accent: #ffffff;
      --igloo-x-base: rgb(0, 0, 0);
      --igloo-x-mantle: rgb(0, 0, 0);
      --igloo-x-crust: rgb(0, 0, 0);
      --igloo-x-surface0: rgb(22, 24, 28);
      --igloo-x-surface1: rgb(47, 51, 54);
      --igloo-x-surface2: rgb(83, 100, 113);
      --igloo-x-overlay0: rgb(113, 118, 123);
      --igloo-x-overlay1: rgb(113, 118, 123);
      --igloo-x-overlay2: rgb(113, 118, 123);
      --igloo-x-subtext0: rgb(113, 118, 123);
      --igloo-x-subtext1: rgb(113, 118, 123);
      --igloo-x-text: rgb(231, 233, 234);
      --igloo-x-red: rgb(249, 24, 128);
      --igloo-x-maroon: rgb(244, 33, 46);
      --igloo-x-green: rgb(0, 186, 124);
      --igloo-x-yellow: rgb(255, 212, 0);
      --igloo-x-pink: rgb(250, 68, 152);
      --igloo-x-mauve: rgb(120, 86, 255);
      --igloo-x-peach: rgb(255, 122, 0);
      --igloo-x-blue: var(--igloo-x-accent);
      --igloo-x-sapphire: var(--igloo-x-accent);
      --igloo-x-sky: var(--igloo-x-accent);
      --igloo-x-lavender: var(--igloo-x-accent);
      --igloo-x-border: rgb(83, 100, 113);
      --igloo-x-control-accent: rgb(29, 155, 240);
      --igloo-x-control-on-accent: #ffffff;
      --igloo-x-control-base: transparent;
      --igloo-x-control-mantle: Canvas;
      --igloo-x-control-crust: Canvas;
      --igloo-x-control-surface0: color-mix(in srgb, currentColor 8%, transparent);
      --igloo-x-control-surface1: color-mix(in srgb, currentColor 24%, transparent);
      --igloo-x-control-surface2: color-mix(in srgb, currentColor 35%, transparent);
      --igloo-x-control-overlay0: currentColor;
      --igloo-x-control-overlay1: currentColor;
      --igloo-x-control-overlay2: currentColor;
      --igloo-x-control-subtext0: currentColor;
      --igloo-x-control-subtext1: currentColor;
      --igloo-x-control-text: currentColor;
      --igloo-x-control-red: rgb(244, 33, 46);
      --igloo-x-control-maroon: rgb(244, 33, 46);
      --igloo-x-control-green: rgb(0, 186, 124);
      --igloo-x-control-yellow: rgb(255, 212, 0);
      --igloo-x-control-pink: rgb(249, 24, 128);
      --igloo-x-control-mauve: rgb(120, 86, 255);
      --igloo-x-control-peach: rgb(255, 122, 0);
      --igloo-x-control-blue: rgb(29, 155, 240);
      --igloo-x-control-sapphire: rgb(29, 155, 240);
      --igloo-x-control-sky: rgb(29, 155, 240);
      --igloo-x-control-lavender: rgb(29, 155, 240);
      --igloo-x-control-border: color-mix(in srgb, currentColor 35%, transparent);
    }
    html[data-igloo-x-theme-source="igloo"] {
      background: var(--igloo-x-mantle) !important;
    }
    .x-sync-btn {
      border: 1px solid var(--igloo-x-control-border); background: transparent;
      color: var(--igloo-x-control-text); border-radius: 9999px; font-size: 12px;
      line-height: 16px; font-weight: 700; padding: 4px 10px;
      margin-right: 8px; cursor: pointer; transition: all 0.15s;
    }
    .x-sync-btn:hover { background: color-mix(in srgb, var(--igloo-x-control-text) 10%, transparent); }
    .x-sync-btn[data-saved="1"] { border-color: color-mix(in srgb, var(--igloo-x-control-border) 55%, transparent); opacity: 0.55; color: var(--igloo-x-control-subtext1); }
    .x-sync-btn[data-saved="1"]:hover { opacity: 0.85; }
    .x-sync-btn:disabled { opacity: 0.65; cursor: wait; }
    .x-source-save-btn {
      position: fixed; right: 24px; bottom: 24px; z-index: 2147483647;
      border: 1px solid var(--igloo-x-control-border); background: var(--igloo-x-control-mantle);
      color: var(--igloo-x-control-text); border-radius: 9999px; font-size: 13px;
      line-height: 16px; font-weight: 800; padding: 9px 15px;
      cursor: pointer; transition: all 0.15s;
      box-shadow: 0 4px 20px rgba(0,0,0,0.6);
      font-family: -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;
    }
    .x-source-save-btn:hover { background: color-mix(in srgb, var(--igloo-x-control-mantle) 92%, var(--igloo-x-control-text)); border-color: color-mix(in srgb, var(--igloo-x-control-text) 60%, transparent); }
    .x-source-save-btn[data-saved="1"] { opacity: 0.72; color: var(--igloo-x-control-subtext1); }
    .x-source-save-btn:disabled { opacity: 0.65; cursor: wait; }

    /* Hide native bookmark button */
    body.igloo-button-overrides [data-testid="bookmark"],
    body.igloo-button-overrides [data-testid="removeBookmark"] { display: none !important; }

    /* X theme selectors adapted from Catppuccin userstyles' MIT-licensed Twitter theme. */
    body.igloo-theme-overrides {
      --border-color: var(--igloo-x-surface0);
      --color: var(--igloo-x-overlay1);
      --color-emphasis: var(--igloo-x-text);
      --hover-bg-color: var(--igloo-x-surface0);
      --cpft-text-primary: var(--igloo-x-text);
      background-color: var(--igloo-x-base) !important;
      color: var(--igloo-x-text);
    }
    body.igloo-theme-overrides,
    body.igloo-theme-overrides #react-root,
    body.igloo-theme-overrides #react-root > div,
    body.igloo-theme-overrides #react-root > div > div,
    body.igloo-theme-overrides header[role="banner"],
    body.igloo-theme-overrides header[role="banner"] nav,
    body.igloo-theme-overrides main,
    body.igloo-theme-overrides [role="main"],
    body.igloo-theme-overrides [data-testid="sidebarColumn"],
    body.igloo-theme-overrides .PageContainer,
    body.igloo-theme-overrides #placeholder,
    body.igloo-theme-overrides [data-testid="primaryColumn"],
    body.igloo-theme-overrides [data-testid="cellInnerDiv"],
    body.igloo-theme-overrides [data-testid="tweet"],
    body.igloo-theme-overrides article[data-testid="tweet"],
    body.igloo-theme-overrides [aria-label^="Timeline:"],
    body.igloo-theme-overrides .r-kemksi {
      background-color: var(--igloo-x-base) !important;
      color: var(--igloo-x-text);
    }
    body.igloo-theme-overrides #ScriptLoadFailure span,
    body.igloo-theme-overrides .r-1nao33i,
    body.igloo-theme-overrides .r-jwli3a,
    body.igloo-theme-overrides a[aria-label^="Translated from"][aria-label$="by Google"] svg path {
      color: var(--igloo-x-text) !important;
      fill: var(--igloo-x-text) !important;
    }
    body.igloo-theme-overrides [style*="scrollbar-color: rgb(62, 65, 68) rgb(22, 24, 28)"] {
      scrollbar-color: var(--igloo-x-accent) transparent !important;
      scrollbar-width: thin;
    }
    body.igloo-theme-overrides .r-qo02w8,
    body.igloo-theme-overrides .r-15ce4ve {
      box-shadow: rgba(0, 0, 0, 0.4) 0 0 15px, rgba(0, 0, 0, 0.35) 0 0 3px 1px !important;
    }
    body.igloo-theme-overrides .r-1tbvlxk {
      filter: drop-shadow(rgba(0, 0, 0, 0.5) 1px -1px 1px) !important;
    }
    body.igloo-theme-overrides .r-1uusn97 {
      box-shadow: rgba(0, 0, 0, 0.4) 0 0 5px, rgba(0, 0, 0, 0.35) 0 1px 4px 1px !important;
    }
    body.igloo-theme-overrides .r-5zmot {
      background-color: color-mix(in srgb, var(--igloo-x-base) 75%, transparent) !important;
    }
    body.igloo-theme-overrides .r-cqee49 {
      color: var(--igloo-x-base) !important;
    }
    body.igloo-theme-overrides .r-1hdo0pc,
    body.igloo-theme-overrides .r-pjtv4k {
      background-color: color-mix(in srgb, var(--igloo-x-text) 10%, transparent) !important;
    }
    body.igloo-theme-overrides .r-11gmi9o {
      background-color: color-mix(in srgb, var(--igloo-x-text) 20%, transparent) !important;
    }
    body.igloo-theme-overrides .r-1cuuowz {
      background-color: color-mix(in srgb, var(--igloo-x-text) 3%, transparent) !important;
    }
    body.igloo-theme-overrides .r-1kqtdi0,
    body.igloo-theme-overrides .r-1roi411,
    body.igloo-theme-overrides [stroke="#2F3336" i],
    body.igloo-theme-overrides [style*="border-color: rgb(51, 54, 57)"] {
      border-color: var(--igloo-x-surface0) !important;
      stroke: var(--igloo-x-surface2) !important;
    }
    body.igloo-theme-overrides .r-1igl3o0 {
      border-bottom-color: var(--igloo-x-surface0) !important;
    }
    body.igloo-theme-overrides .r-2sztyj {
      border-top-color: var(--igloo-x-surface0) !important;
    }
    body.igloo-theme-overrides .r-1aihyag {
      border-right-color: var(--igloo-x-surface0) !important;
    }
    body.igloo-theme-overrides .r-1wyyjkm {
      border-left-color: var(--igloo-x-subtext0) !important;
    }
    body.igloo-theme-overrides [style*="border-color: rgb(83, 100, 113)"] {
      border-color: var(--igloo-x-surface1) !important;
    }
    body.igloo-theme-overrides .r-1ccsd61,
    body.igloo-theme-overrides .r-xzxzvz {
      border-color: var(--igloo-x-surface2) !important;
    }
    body.igloo-theme-overrides .r-1xc7w19 {
      border-color: var(--igloo-x-base) !important;
    }
    body.igloo-theme-overrides .r-gu4em3,
    body.igloo-theme-overrides .r-1bnu78o,
    body.igloo-theme-overrides .r-z32n2g,
    body.igloo-theme-overrides .r-1m3jxhj,
    body.igloo-theme-overrides .r-1pr99xn,
    body.igloo-theme-overrides .r-1fkb3t2 {
      background-color: var(--igloo-x-surface0) !important;
    }
    body.igloo-theme-overrides .r-g2wdr4 {
      background-color: var(--igloo-x-mantle) !important;
    }
    body.igloo-theme-overrides .r-14wv3jr {
      border-color: var(--igloo-x-mantle) !important;
    }
    body.igloo-theme-overrides .r-1bwzh9t,
    body.igloo-theme-overrides .r-qazpri {
      color: var(--igloo-x-overlay1) !important;
    }
    body.igloo-theme-overrides .r-l5o3uw,
    body.igloo-theme-overrides [style*="background-color: rgb(29, 155, 240)"] {
      background-color: var(--igloo-x-accent) !important;
    }
    body.igloo-theme-overrides .r-1vtznih {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 88%, #000) !important;
    }
    body.igloo-theme-overrides .r-yuvema {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 82%, #000) !important;
    }
    body.igloo-theme-overrides .r-eff69c {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 95%, #000) !important;
    }
    body.igloo-theme-overrides .r-eff69c [style*="color: rgb(255, 255, 255)"] {
      color: var(--igloo-x-on-accent) !important;
    }
    body.igloo-theme-overrides .r-1peqgm7,
    body.igloo-theme-overrides .r-rgqbpe,
    body.igloo-theme-overrides [style*="background-color: rgb(2, 17, 61)"] {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 12%, transparent) !important;
    }
    body.igloo-theme-overrides .r-11z020y {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 12%, transparent) !important;
    }
    body.igloo-theme-overrides .r-r18ze4 {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 20%, transparent) !important;
    }
    body.igloo-theme-overrides .r-l5o3uw .r-jwli3a,
    body.igloo-theme-overrides .r-1vtznih .r-jwli3a,
    body.igloo-theme-overrides .r-yuvema .r-jwli3a,
    body.igloo-theme-overrides [style*="background-color: rgb(29, 155, 240)"] [style*="color: rgb(255, 255, 255)"],
    body.igloo-theme-overrides [style*="background-color: rgb(239, 243, 244)"] [style*="color: rgb(15, 20, 25)"],
    body.igloo-theme-overrides [data-testid$="-follow"] [style*="color: rgb(15, 20, 25)"] {
      color: var(--igloo-x-on-accent) !important;
    }
    body.igloo-theme-overrides .r-jc7xae {
      background-color: color-mix(in srgb, var(--igloo-x-text) 96%, #000) !important;
    }
    body.igloo-theme-overrides .r-6wtuen {
      background-color: color-mix(in srgb, var(--igloo-x-text) 92%, #000) !important;
    }
    body.igloo-theme-overrides .r-1eltapf {
      background-color: color-mix(in srgb, var(--igloo-x-sapphire) 10%, transparent) !important;
    }
    body.igloo-theme-overrides .r-eok2q2 {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 60%, transparent) !important;
    }
    body.igloo-theme-overrides .r-9cip40 {
      box-shadow: var(--igloo-x-accent) 0 0 0 1px !important;
    }
    @keyframes r-1wvy3k1 {
      0% { box-shadow: color-mix(in srgb, var(--igloo-x-mauve) 40%, transparent) 0; }
      100% { box-shadow: color-mix(in srgb, var(--igloo-x-mauve) 0%, transparent) 0; }
    }
    body.igloo-theme-overrides [style="background-image: linear-gradient(61.63deg, rgb(45, 66, 255) -15.05%, rgb(156, 99, 250) 104.96%);"] {
      background-image: linear-gradient(61.63deg, var(--igloo-x-blue) -15.05%, var(--igloo-x-mauve) 104.96%) !important;
    }
    body.igloo-theme-overrides .r-1blqq69 {
      border-color: var(--igloo-x-mauve) !important;
    }
    body.igloo-theme-overrides .r-11mg6pl {
      border-color: var(--igloo-x-on-accent) !important;
    }
    body.igloo-theme-overrides .draftjs-styles_0 .public-DraftEditorPlaceholder-root {
      color: var(--igloo-x-overlay0) !important;
    }
    body.igloo-theme-overrides .r-s224ru {
      background-color: var(--igloo-x-green) !important;
    }
    body.igloo-theme-overrides .r-h7o7i8,
    body.igloo-theme-overrides .r-15azkrj {
      background-color: color-mix(in srgb, var(--igloo-x-green) 10%, transparent) !important;
    }
    body.igloo-theme-overrides .r-1x669os {
      background-color: color-mix(in srgb, var(--igloo-x-green) 20%, transparent) !important;
    }
    body.igloo-theme-overrides .r-4nw3r4,
    body.igloo-theme-overrides .r-1dgebii,
    body.igloo-theme-overrides [style*="background-color: rgb(244, 33, 46)"] {
      background-color: var(--igloo-x-red) !important;
    }
    body.igloo-theme-overrides .r-b5kvu3 {
      border-color: var(--igloo-x-red) !important;
    }
    body.igloo-theme-overrides .r-qqmkd0,
    body.igloo-theme-overrides .r-1krxqcr {
      background-color: color-mix(in srgb, var(--igloo-x-red) 10%, transparent) !important;
    }
    body.igloo-theme-overrides .r-1kwlb9n {
      background-color: color-mix(in srgb, var(--igloo-x-red) 12%, transparent) !important;
    }
    body.igloo-theme-overrides .r-uuique {
      background-color: color-mix(in srgb, var(--igloo-x-red) 20%, transparent) !important;
    }
    body.igloo-theme-overrides .r-12d83nn {
      background-color: color-mix(in srgb, var(--igloo-x-red) 88%, #000) !important;
    }
    body.igloo-theme-overrides .r-oybae9 {
      background-color: color-mix(in srgb, var(--igloo-x-red) 82%, #000) !important;
    }
    body.igloo-theme-overrides .r-n94g0g {
      background-color: color-mix(in srgb, var(--igloo-x-text) 30%, transparent) !important;
    }
    body.igloo-theme-overrides .r-z9i421 {
      background-color: color-mix(in srgb, var(--igloo-x-text) 27%, transparent) !important;
    }
    body.igloo-theme-overrides .r-19130f6 {
      background-color: var(--igloo-x-crust) !important;
    }
    body.igloo-theme-overrides .r-l8tqsx {
      background-color: color-mix(in srgb, var(--igloo-x-text) 10%, transparent) !important;
    }
    body.igloo-theme-overrides .r-3gvs5h {
      background-color: var(--igloo-x-overlay1) !important;
    }
    body.igloo-theme-overrides .r-vkub15,
    body.igloo-theme-overrides .r-9l7dzd,
    body.igloo-theme-overrides [fill="rgb(249,22,127)"],
    body.igloo-theme-overrides [fill="rgb(222,45,108)"],
    body.igloo-theme-overrides g[clip-path="url(#__lottie_element_562)"] path,
    body.igloo-theme-overrides [style="color: rgb(249, 24, 128);"] [viewBox="0 0 24 24"] path {
      color: var(--igloo-x-red) !important;
      fill: var(--igloo-x-red) !important;
    }
    body.igloo-theme-overrides .r-1cvl2hr,
    body.igloo-theme-overrides [stroke="#1D9BF0" i],
    body.igloo-theme-overrides [style*="stroke: rgb(29, 155, 240)"] {
      color: var(--igloo-x-accent) !important;
      stroke: var(--igloo-x-accent) !important;
    }
    body.igloo-theme-overrides .r-o6sn0f {
      color: var(--igloo-x-green) !important;
    }
    body.igloo-theme-overrides [stroke="#FFD400" i],
    body.igloo-theme-overrides [stop-color="#f4e72a" i],
    body.igloo-theme-overrides [stop-color="#cd8105" i],
    body.igloo-theme-overrides [stop-color="#cb7b00" i],
    body.igloo-theme-overrides [stop-color="#f4ec26" i],
    body.igloo-theme-overrides [stop-color="#f9e87f" i],
    body.igloo-theme-overrides [stop-color="#e2b719" i] {
      stroke: var(--igloo-x-yellow) !important;
      stop-color: var(--igloo-x-yellow) !important;
    }
    body.igloo-theme-overrides [fill="#829AAB" i],
    body.igloo-theme-overrides [data-testid="card.wrapper"] [d="M21.04 1.54L17.5 5.09c-.04-.02-.08-.03-.13-.04L14.3 3H9.7l-3 2H5C3.62 5 2.5 6.12 2.5 7.5v11c0 .46.12.88.34 1.25l-1.3 1.29 1.42 1.42 19.5-19.5-1.42-1.42zM13.7 5l2.33 1.56-2 1.99C13.44 8.2 12.74 8 12 8c-2.21 0-4 1.79-4 4 0 .74.2 1.44.55 2.03L4.5 18.09V7.5c0-.28.22-.5.5-.5h2.3l3-2h3.4zM12 10c.18 0 .35.02.52.07l-2.45 2.45c-.05-.17-.07-.34-.07-.52 0-1.1.9-2 2-2zm7 11H7.24l2-2H19c.28 0 .5-.22.5-.5V9h2v9.5c0 1.38-1.12 2.5-2.5 2.5z"] {
      color: var(--igloo-x-overlay0) !important;
      fill: var(--igloo-x-overlay2) !important;
    }
    body.igloo-theme-overrides [fill="#1DA1F2" i],
    body.igloo-theme-overrides [fill="#78C6EE" i] {
      fill: var(--igloo-x-sky) !important;
    }
    body.igloo-theme-overrides [fill="#d18800" i] {
      fill: var(--igloo-x-yellow) !important;
    }
    body.igloo-theme-overrides [style*="https://abs.twimg.com/responsive-web/client-web/background-premiumplus-web"] {
      background-image: none !important;
      background-color: var(--igloo-x-surface0) !important;
    }
    body.igloo-theme-overrides [style*="border-color: rgb(103, 7, 15)"] {
      border-color: color-mix(in srgb, var(--igloo-x-red) 50%, transparent) !important;
    }
    body.igloo-theme-overrides [style*="border-color: rgb(29, 155, 240)"],
    body.igloo-theme-overrides .r-vhj8yc {
      border-color: var(--igloo-x-accent) !important;
    }
    body.igloo-theme-overrides .r-1pbtemp {
      border-right-color: var(--igloo-x-accent) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides [style*="color: rgb(239, 243, 244)"]:not([style*="background-color: rgb(239, 243, 244)"]),
    body.igloo-theme-overrides [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]) {
      color: var(--igloo-x-text) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]) input::placeholder {
      color: var(--igloo-x-subtext1) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(113, 118, 123)"]:not([style*="background-color: rgb(113, 118, 123)"]),
    body.igloo-theme-overrides [style*="color: rgb(182, 185, 188)"]:not([style*="background-color: rgb(182, 185, 188)"]) {
      color: var(--igloo-x-overlay1) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(0, 186, 124)"]:not([style*="background-color: rgb(0, 186, 124)"]) {
      color: var(--igloo-x-green) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(249, 24, 128)"]:not([style*="background-color: rgb(249, 24, 128)"]),
    body.igloo-theme-overrides [style*="color: rgb(244, 33, 46)"]:not([style*="background-color: rgb(244, 33, 46)"]) {
      color: var(--igloo-x-red) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(250, 68, 152)"]:not([style*="background-color: rgb(250, 68, 152)"]) {
      color: var(--igloo-x-pink) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(255, 212, 0)"]:not([style*="background-color: rgb(255, 212, 0)"]) {
      color: var(--igloo-x-yellow) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(120, 86, 255)"]:not([style*="background-color: rgb(120, 86, 255)"]) {
      color: var(--igloo-x-mauve) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(255, 122, 0)"]:not([style*="background-color: rgb(255, 122, 0)"]) {
      color: var(--igloo-x-peach) !important;
    }
    body.igloo-theme-overrides [style*="color: rgb(29, 155, 240)"]:not([style*="background-color: rgb(29, 155, 240)"]) {
      color: var(--igloo-x-accent) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgb(142, 205, 248)"] {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 88%, #fff) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgba(255, 255, 255, 0.25)"] {
      background-color: color-mix(in srgb, var(--igloo-x-text) 25%, transparent) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgb(147, 147, 147)"] {
      background-color: var(--igloo-x-overlay0) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgb(147, 147, 147)"] + [style*="background-color: rgb(250, 250, 250)"] {
      background-color: var(--igloo-x-text) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgb(239, 243, 244)"] {
      background-color: var(--igloo-x-text) !important;
    }
    body.igloo-theme-overrides [data-testid$="-follow"] {
      background: var(--igloo-x-text) !important;
      background-color: var(--igloo-x-text) !important;
      border-color: var(--igloo-x-text) !important;
    }
    body.igloo-theme-overrides [data-testid$="-follow"],
    body.igloo-theme-overrides [data-testid$="-follow"] *,
    body.igloo-theme-overrides [data-testid$="-follow"][style*="color: rgb(15, 20, 25)"],
    body.igloo-theme-overrides [data-testid$="-follow"] [style*="color: rgb(15, 20, 25)"] {
      color: var(--igloo-x-on-accent) !important;
    }
    body.igloo-theme-overrides [data-testid$="-unfollow"] {
      background: transparent !important;
      background-color: transparent !important;
      border-color: var(--igloo-x-surface1) !important;
    }
    body.igloo-theme-overrides [data-testid$="-unfollow"],
    body.igloo-theme-overrides [data-testid$="-unfollow"] * {
      color: var(--igloo-x-text) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgb(0, 0, 0)"],
    body.igloo-theme-overrides [style*="background-color: #000"] {
      background-color: var(--igloo-x-base) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgba(15, 20, 25, 0.75)"] {
      background-color: color-mix(in srgb, var(--igloo-x-crust) 75%, transparent) !important;
    }
    body.igloo-theme-overrides [style*="background-color: rgba(15, 20, 25, 0.75)"] [style*="color: rgb(255, 255, 255)"] svg {
      color: var(--igloo-x-text) !important;
    }
    body.igloo-theme-overrides .r-l5o3uw [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-l5o3uw[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-1vtznih [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-1vtznih[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-4nw3r4 [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-4nw3r4[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-12d83nn [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-12d83nn[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-oybae9 [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-oybae9[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-yuvema [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-yuvema[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-3gvs5h [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-3gvs5h[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides [style="background-image: linear-gradient(61.63deg, rgb(45, 66, 255) -15.05%, rgb(156, 99, 250) 104.96%);"] [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides [style="background-image: linear-gradient(61.63deg, rgb(45, 66, 255) -15.05%, rgb(156, 99, 250) 104.96%);"][style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-l5o3uw [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-l5o3uw[style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-1vtznih [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-1vtznih[style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-4nw3r4 [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-4nw3r4[style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-12d83nn [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-12d83nn[style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-oybae9 [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-oybae9[style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-yuvema [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-yuvema[style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-3gvs5h [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-3gvs5h[style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides [style="background-image: linear-gradient(61.63deg, rgb(45, 66, 255) -15.05%, rgb(156, 99, 250) 104.96%);"] [style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides [style="background-image: linear-gradient(61.63deg, rgb(45, 66, 255) -15.05%, rgb(156, 99, 250) 104.96%);"][style*="color: rgb(231, 233, 234)"]:not([style*="background-color: rgb(231, 233, 234)"]),
    body.igloo-theme-overrides .r-l5o3uw [color="white"],
    body.igloo-theme-overrides .r-1vtznih [color="white"],
    body.igloo-theme-overrides .r-4nw3r4 [color="white"],
    body.igloo-theme-overrides .r-12d83nn [color="white"],
    body.igloo-theme-overrides .r-oybae9 [color="white"],
    body.igloo-theme-overrides .r-yuvema [color="white"],
    body.igloo-theme-overrides .r-3gvs5h [color="white"],
    body.igloo-theme-overrides [style="background-image: linear-gradient(61.63deg, rgb(45, 66, 255) -15.05%, rgb(156, 99, 250) 104.96%);"] [color="white"] {
      color: var(--igloo-x-on-accent) !important;
    }
    body.igloo-theme-overrides [data-testid="videoComponent"]:not(.r-4nw3r4) [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides [data-testid="videoComponent"]:not(.r-4nw3r4)[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides [data-testid="videoComponent"]:not(.r-4nw3r4) .r-jwli3a,
    body.igloo-theme-overrides .r-loe9s5 [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-loe9s5[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-loe9s5 .r-jwli3a,
    body.igloo-theme-overrides .r-drfeu3:has([style="background-color: rgba(255, 255, 255, 0.25); border-color: rgba(0, 0, 0, 0); backdrop-filter: blur(4px);"]) [style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-drfeu3:has([style="background-color: rgba(255, 255, 255, 0.25); border-color: rgba(0, 0, 0, 0); backdrop-filter: blur(4px);"])[style*="color: rgb(255, 255, 255)"]:not([style*="background-color: rgb(255, 255, 255)"]),
    body.igloo-theme-overrides .r-drfeu3:has([style="background-color: rgba(255, 255, 255, 0.25); border-color: rgba(0, 0, 0, 0); backdrop-filter: blur(4px);"]) .r-jwli3a {
      color: #fff !important;
    }
    body.igloo-theme-overrides #header {
      color: var(--igloo-x-subtext0);
      background: var(--igloo-x-base);
      border-bottom-color: var(--igloo-x-surface1);
    }
    body.igloo-theme-overrides #header .logo a {
      border-bottom-color: transparent;
    }
    body.igloo-theme-overrides #session a,
    body.igloo-theme-overrides #session input,
    body.igloo-theme-overrides #session button {
      background: var(--igloo-x-surface0);
      color: var(--igloo-x-subtext0);
    }
    body.igloo-theme-overrides #session h2 img,
    body.igloo-theme-overrides .oauth #bd,
    body.igloo-theme-overrides .list-btn {
      border-color: var(--igloo-x-surface1);
    }
    body.igloo-theme-overrides .footer {
      background: var(--igloo-x-mantle);
      border-top-color: var(--igloo-x-surface1);
    }
    body.igloo-theme-overrides .auth h2,
    body.igloo-theme-overrides .notice,
    body.igloo-theme-overrides .notice p,
    body.igloo-theme-overrides h2 {
      color: var(--igloo-x-subtext1);
    }
    body.igloo-theme-overrides .notice.error {
      background: color-mix(in srgb, var(--igloo-x-red) 20%, transparent);
      border-color: color-mix(in srgb, var(--igloo-x-red) 25%, transparent);
    }
    body.igloo-theme-overrides .app-info h3 img {
      border-color: var(--igloo-x-base);
    }
    body.igloo-theme-overrides .permissions.allow strong {
      color: var(--igloo-x-green);
    }
    body.igloo-theme-overrides .button {
      background: var(--igloo-x-overlay0);
      color: var(--igloo-x-text);
      border-color: var(--igloo-x-surface1);
    }
    body.igloo-theme-overrides .button:hover {
      background: color-mix(in srgb, var(--igloo-x-surface2) 88%, #000);
      color: var(--igloo-x-text);
      border-color: var(--igloo-x-surface1);
    }
    body.igloo-theme-overrides .button.selected,
    body.igloo-theme-overrides .follow-button .unfollow .button,
    body.igloo-theme-overrides .submit-btn {
      background-color: var(--igloo-x-accent);
      color: var(--igloo-x-on-accent);
      border-color: color-mix(in srgb, var(--igloo-x-accent) 88%, #000);
    }
    body.igloo-theme-overrides .button.selected:hover,
    body.igloo-theme-overrides .follow-button .unfollow .button:hover,
    body.igloo-theme-overrides .submit-btn:hover,
    body.igloo-theme-overrides .redirect-btn:hover {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 88%, #000);
    }
    body.igloo-theme-overrides .button.selected .app-info,
    body.igloo-theme-overrides .button.selected #bd h3,
    body.igloo-theme-overrides .follow-button .unfollow .button .app-info,
    body.igloo-theme-overrides .follow-button .unfollow .button #bd h3,
    body.igloo-theme-overrides .list-explanation {
      color: var(--igloo-x-subtext0);
    }
    body.igloo-theme-overrides .button.selected #ft,
    body.igloo-theme-overrides .follow-button .unfollow .button #ft {
      color: var(--igloo-x-overlay0);
    }
    body.igloo-theme-overrides .ResponsiveLayout--Night .PageContainer {
      background-color: var(--igloo-x-base);
    }
    body.igloo-theme-overrides .ResponsiveLayout--Night .list-btn:first-of-type {
      border-top-color: var(--igloo-x-mantle);
    }
    body.igloo-theme-overrides .ResponsiveLayout--Night .list-btn:hover {
      background-color: var(--igloo-x-mantle);
    }
    body.igloo-theme-overrides .block-btn {
      color: var(--igloo-x-maroon);
      border-color: var(--igloo-x-maroon);
    }
    body.igloo-theme-overrides .mute-btn,
    body.igloo-theme-overrides .unfollow-btn,
    body.igloo-theme-overrides .email-report-btn {
      color: var(--igloo-x-accent);
      border-color: var(--igloo-x-accent);
    }
    body.igloo-theme-overrides .list-btn:first-of-type {
      border-top-color: var(--igloo-x-surface1);
    }
    body.igloo-theme-overrides .list-btn:hover,
    body.igloo-theme-overrides .js #session .user-menu {
      background-color: var(--igloo-x-surface0);
    }
    body.igloo-theme-overrides #session .user-menu a:focus,
    body.igloo-theme-overrides #session .user-menu a:hover,
    body.igloo-theme-overrides #session .user-menu button:focus,
    body.igloo-theme-overrides #session .user-menu button:hover,
    body.igloo-theme-overrides #session .user-menu input:focus,
    body.igloo-theme-overrides #session .user-menu input:hover {
      color: var(--igloo-x-on-accent);
      background-color: var(--igloo-x-accent);
    }
    body.igloo-theme-overrides .dropdown-caret .caret-outer,
    body.igloo-theme-overrides .dropdown-caret .caret-inner {
      border-bottom-color: var(--igloo-x-surface0);
    }

    /* Preserve the original Igloo action-button treatment, backed by theme vars. */
    body.igloo-theme-overrides [data-testid="reply"] div,
    body.igloo-theme-overrides [data-testid="retweet"] div,
    body.igloo-theme-overrides [data-testid="like"] div,
    body.igloo-theme-overrides [data-testid="unlike"] div,
    body.igloo-theme-overrides [data-testid="unretweet"] div,
    body.igloo-theme-overrides [aria-label="Share post"] div,
    body.igloo-theme-overrides [aria-label="Share"] div,
    body.igloo-theme-overrides [data-testid="reply"] svg,
    body.igloo-theme-overrides [data-testid="retweet"] svg,
    body.igloo-theme-overrides [data-testid="like"] svg,
    body.igloo-theme-overrides [data-testid="unlike"] svg,
    body.igloo-theme-overrides [data-testid="unretweet"] svg,
    body.igloo-theme-overrides [aria-label="Share post"] svg,
    body.igloo-theme-overrides [aria-label="Share"] svg { color: var(--igloo-x-accent) !important; fill: var(--igloo-x-accent) !important; }

    body.igloo-theme-overrides [data-testid="reply"] div,
    body.igloo-theme-overrides [data-testid="retweet"] div,
    body.igloo-theme-overrides [data-testid="like"] div,
    body.igloo-theme-overrides [data-testid="unlike"] div,
    body.igloo-theme-overrides [data-testid="unretweet"] div,
    body.igloo-theme-overrides [aria-label="Share post"] div,
    body.igloo-theme-overrides [aria-label="Share"] div { border-radius: 9999px !important; }

    body.igloo-theme-overrides [data-testid="reply"]:hover .r-1niwhzg,
    body.igloo-theme-overrides [data-testid="retweet"]:hover .r-1niwhzg,
    body.igloo-theme-overrides [data-testid="like"]:hover .r-1niwhzg,
    body.igloo-theme-overrides [data-testid="unlike"]:hover .r-1niwhzg,
    body.igloo-theme-overrides [data-testid="unretweet"]:hover .r-1niwhzg,
    body.igloo-theme-overrides [aria-label="Share post"]:hover .r-1niwhzg,
    body.igloo-theme-overrides [aria-label="Share"]:hover .r-1niwhzg { background-color: color-mix(in srgb, var(--igloo-x-accent) 10%, transparent) !important; }

    /* Composer toolbar buttons */
    body.igloo-theme-overrides button[role="button"][aria-label="Add photos or video"],
    body.igloo-theme-overrides button[role="button"][data-testid="gifSearchButton"],
    body.igloo-theme-overrides button[role="button"][data-testid="grokImgGen"],
    body.igloo-theme-overrides button[role="button"][data-testid="createPollButton"],
    body.igloo-theme-overrides button[role="button"][aria-label="Add emoji"],
    body.igloo-theme-overrides button[role="button"][data-testid="scheduleOption"],
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"],
    body.igloo-theme-overrides button[role="button"][data-testid="contentDisclosureButton"] {
      border-color: transparent !important;
      background-color: transparent !important;
      border-radius: 9999px !important;
    }

    body.igloo-theme-overrides button[role="button"][aria-label="Add photos or video"] div,
    body.igloo-theme-overrides button[role="button"][data-testid="gifSearchButton"] div,
    body.igloo-theme-overrides button[role="button"][data-testid="grokImgGen"] div,
    body.igloo-theme-overrides button[role="button"][data-testid="createPollButton"] div,
    body.igloo-theme-overrides button[role="button"][aria-label="Add emoji"] div,
    body.igloo-theme-overrides button[role="button"][data-testid="scheduleOption"] div,
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"]:not(:disabled) div,
    body.igloo-theme-overrides button[role="button"][data-testid="contentDisclosureButton"] div,
    body.igloo-theme-overrides button[role="button"][aria-label="Add photos or video"] svg,
    body.igloo-theme-overrides button[role="button"][data-testid="gifSearchButton"] svg,
    body.igloo-theme-overrides button[role="button"][data-testid="grokImgGen"] svg,
    body.igloo-theme-overrides button[role="button"][data-testid="createPollButton"] svg,
    body.igloo-theme-overrides button[role="button"][aria-label="Add emoji"] svg,
    body.igloo-theme-overrides button[role="button"][data-testid="scheduleOption"] svg,
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"]:not(:disabled) svg,
    body.igloo-theme-overrides button[role="button"][data-testid="contentDisclosureButton"] svg,
    body.igloo-theme-overrides button[role="button"][aria-label="Add photos or video"] svg *,
    body.igloo-theme-overrides button[role="button"][data-testid="gifSearchButton"] svg *,
    body.igloo-theme-overrides button[role="button"][data-testid="grokImgGen"] svg *,
    body.igloo-theme-overrides button[role="button"][data-testid="createPollButton"] svg *,
    body.igloo-theme-overrides button[role="button"][aria-label="Add emoji"] svg *,
    body.igloo-theme-overrides button[role="button"][data-testid="scheduleOption"] svg *,
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"]:not(:disabled) svg *,
    body.igloo-theme-overrides button[role="button"][data-testid="contentDisclosureButton"] svg * {
      color: var(--igloo-x-accent) !important;
      fill: var(--igloo-x-accent) !important;
    }

    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"]:disabled div,
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"]:disabled svg,
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"]:disabled svg * {
      color: var(--igloo-x-overlay0) !important;
      fill: var(--igloo-x-overlay0) !important;
    }

    body.igloo-theme-overrides button[role="button"][aria-label="Add photos or video"] span,
    body.igloo-theme-overrides button[role="button"][data-testid="gifSearchButton"] span,
    body.igloo-theme-overrides button[role="button"][data-testid="grokImgGen"] span,
    body.igloo-theme-overrides button[role="button"][data-testid="createPollButton"] span,
    body.igloo-theme-overrides button[role="button"][aria-label="Add emoji"] span,
    body.igloo-theme-overrides button[role="button"][data-testid="scheduleOption"] span,
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"] span,
    body.igloo-theme-overrides button[role="button"][data-testid="contentDisclosureButton"] span {
      border-bottom-color: var(--igloo-x-accent) !important;
    }

    body.igloo-theme-overrides button[role="button"][aria-label="Add photos or video"]:hover,
    body.igloo-theme-overrides button[role="button"][data-testid="gifSearchButton"]:hover,
    body.igloo-theme-overrides button[role="button"][data-testid="grokImgGen"]:hover,
    body.igloo-theme-overrides button[role="button"][data-testid="createPollButton"]:hover,
    body.igloo-theme-overrides button[role="button"][aria-label="Add emoji"]:hover,
    body.igloo-theme-overrides button[role="button"][data-testid="scheduleOption"]:hover,
    body.igloo-theme-overrides button[role="button"][data-testid="geoButton"]:hover,
    body.igloo-theme-overrides button[role="button"][data-testid="contentDisclosureButton"]:hover {
      background-color: color-mix(in srgb, var(--igloo-x-accent) 10%, transparent) !important;
    }

    /* Even spacing for action bar with 6 buttons */
    body.igloo-button-overrides [role="group"] { justify-content: space-between !important; }

    /* Our action buttons — match native wrapper structure */
    .x-action-wrap {
      display: flex; align-items: center; justify-content: center;
      flex-shrink: 0;
    }
    .x-action-wrap button {
      display: flex; align-items: center; justify-content: center;
      background: transparent; border: none; color: var(--igloo-x-control-accent);
      cursor: pointer; border-radius: 9999px; padding: 8px;
      transition: color 0.15s, background-color 0.15s;
    }
    .x-action-wrap button svg { width: 18.75px; height: 18.75px; }
    .x-action-wrap button:hover { background-color: color-mix(in srgb, var(--igloo-x-control-accent) 10%, transparent); }
    .x-action-wrap button:disabled { opacity: 0.5; cursor: wait; }
    .x-action-wrap button.done { color: var(--igloo-x-control-accent); }
    .x-action-wrap button.error { color: var(--igloo-x-control-red); }
    .x-action-wrap button.downloaded { color: var(--igloo-x-control-green); }
    .x-action-wrap button.downloaded svg { display: none; }
    .x-action-wrap button.downloaded::after { content: '\u2713'; font-size: 16px; font-weight: 700; }
    .x-action-wrap button.copied { color: var(--igloo-x-control-accent); }
    #x-dl-popover {
    position: absolute; background: var(--igloo-x-control-base);
    border: 1px solid color-mix(in srgb, var(--igloo-x-control-accent) 25%, transparent); border-radius: 16px;
    padding: 20px 22px; z-index: 999998; min-width: 300px; max-width: 340px;
    box-shadow: 0 4px 24px rgba(0,0,0,0.6);
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
    font-size: 14px; color: var(--igloo-x-control-text);
    }
    #x-dl-popover .xdl-label-text { display:block;margin-bottom:6px;font-size:13px;color:var(--igloo-x-control-subtext1);font-weight:500; }
    #x-dl-popover .xdl-preview { font-size:12px;color:var(--igloo-x-control-subtext0);margin-bottom:14px;min-height:18px; }
    #x-dl-popover button.xdl-confirm {
    width:100%;padding:10px;border-radius:9999px;background:var(--igloo-x-control-accent);
    color:var(--igloo-x-control-on-accent);border:none;font-weight:700;cursor:pointer;font-size:14px;transition:background 0.15s;
    }
    #x-dl-popover button.xdl-confirm:hover { background:color-mix(in srgb, var(--igloo-x-control-accent) 88%, #000); }
    .xdl-cat-pill {
      background:transparent;border:1px solid color-mix(in srgb, var(--igloo-x-control-accent) 35%, transparent);
      color:var(--igloo-x-control-subtext1);border-radius:9999px;padding:6px 14px;
      font-size:13px;font-weight:600;cursor:pointer;transition:all 0.12s;
    }
    .xdl-cat-pill:hover,.xdl-cat-pill.selected { background:var(--igloo-x-control-accent);border-color:var(--igloo-x-control-accent);color:var(--igloo-x-control-on-accent); }
    .xdl-media-idx-row { display:flex;flex-wrap:wrap;gap:5px;margin:4px 0 12px; }
    .xdl-media-idx-btn { width:28px;height:28px;border-radius:6px;border:1px solid color-mix(in srgb, var(--igloo-x-control-accent) 35%, transparent);background:transparent;color:var(--igloo-x-control-subtext1);font-size:12px;font-weight:600;cursor:pointer;padding:0;line-height:28px;text-align:center;transition:all 0.12s; }
    .xdl-media-idx-btn:hover { background:color-mix(in srgb, var(--igloo-x-control-accent) 18%, transparent);border-color:var(--igloo-x-control-accent); }
    .xdl-media-idx-btn.selected { background:var(--igloo-x-control-accent);border-color:var(--igloo-x-control-accent);color:var(--igloo-x-control-on-accent); }

    /* === Native Follow Button Override (HIGH SPECIFICITY) === */
    /* Force transparent background and native grey border */
    body.igloo-button-overrides #react-root button[role="button"][data-testid$="-follow"] {
      background: transparent !important;
      background-color: transparent !important;
      border: 1px solid var(--igloo-x-control-border) !important;
      transition: all 0.15s !important;
    }

    /* Default text color */
    body.igloo-button-overrides #react-root button[role="button"][data-testid$="-follow"] * {
      color: var(--igloo-x-control-text) !important;
    }

    /* Hover state (subtle grey/white highlight instead of pink) */
    body.igloo-button-overrides #react-root button[role="button"][data-testid$="-follow"]:hover {
      background: color-mix(in srgb, var(--igloo-x-control-text) 10%, transparent) !important;
      background-color: color-mix(in srgb, var(--igloo-x-control-text) 10%, transparent) !important;
    }

    /* Unfollow / Following state */
    body.igloo-button-overrides #react-root button[role="button"][data-testid$="-unfollow"] {
      background: transparent !important;
      background-color: transparent !important;
      border: 1px solid var(--igloo-x-control-border) !important;
    }

    body.igloo-button-overrides #react-root button[role="button"][data-testid$="-unfollow"] * {
      color: var(--igloo-x-control-subtext1) !important;
    }

    body.igloo-button-overrides #react-root button[role="button"][data-testid$="-unfollow"]:hover {
      background: color-mix(in srgb, var(--igloo-x-control-text) 10%, transparent) !important;
      background-color: color-mix(in srgb, var(--igloo-x-control-text) 10%, transparent) !important;
      border-color: color-mix(in srgb, var(--igloo-x-control-text) 30%, transparent) !important;
    }

    body.igloo-button-overrides #react-root button[role="button"][data-testid$="-unfollow"]:hover * {
      color: var(--igloo-x-control-text) !important;
    }
    `;
    document.head.appendChild(style);
  }

  function syncXOverrideClasses() {
    if (!document.body) return;
    document.body.classList.toggle(
      "igloo-button-overrides",
      isXSite() && buttonOverridesEnabled(),
    );
    document.body.classList.toggle(
      "igloo-theme-overrides",
      xThemeOverridesActive(),
    );
  }

  // ── Popover ────────────────────────────────────────────────────────────────
  function showDlPopover(
    anchorBtn,
    handle,
    tweetId,
    mediaCount,
    onConfirm,
    article,
  ) {
    document.getElementById("x-dl-popover")?.remove();
    const popover = document.createElement("div");
    popover.id = "x-dl-popover";

    const hdr = document.createElement("div");
    hdr.style.cssText =
      "font-size:15px;font-weight:700;color:var(--igloo-x-control-text);margin-bottom:14px;";
    hdr.textContent =
      mediaCount > 0
        ? "Download " + mediaCount + " file" + (mediaCount > 1 ? "s" : "")
        : "Download media";
    popover.appendChild(hdr);

    const cats = dlCategories || [];
    let selectedCatId = cats.find((c) => c.id === _dlLastCategory())
      ? _dlLastCategory()
      : (cats[0] && cats[0].id) || 1;

    let catRow = null;
    if (cats.length) {
      catRow = document.createElement("div");
      catRow.style.cssText =
        "display:flex;flex-wrap:wrap;gap:8px;margin-bottom:16px;";
      cats.forEach((c) => {
        const pill = document.createElement("button");
        pill.type = "button";
        pill.className =
          "xdl-cat-pill" + (c.id === selectedCatId ? " selected" : "");
        pill.textContent = c.name;
        pill.addEventListener("click", () => {
          catRow
            .querySelectorAll(".xdl-cat-pill")
            .forEach((p) => p.classList.remove("selected"));
          pill.classList.add("selected");
          selectedCatId = c.id;
          updatePreview();
        });
        catRow.appendChild(pill);
      });
      popover.appendChild(catRow);
    }

    // ── Account pills (multi-select, double-click to edit alias) ──
    const originalHandle = handle || "";
    // Gather accounts: author, quote author, @mentions
    const acctList = [];
    const acctSeen = new Set();
    function addAcct(h, sel) {
      if (!h || acctSeen.has(h.toLowerCase())) return;
      acctSeen.add(h.toLowerCase());
      acctList.push({ handle: h, selected: sel });
    }
    addAcct(originalHandle, true);
    if (article) {
      // Retweeter — role="link" element whose text contains "repost"
      for (const rl of article.querySelectorAll('[role="link"][href]')) {
        if (/repost/i.test(rl.textContent)) {
          const rh = parseHandleFromHref(rl.getAttribute("href"));
          if (rh) {
            addAcct(rh, false);
            break;
          }
        }
      }
      // All User-Name blocks — each has @handle spans. Captures author, quote author, etc.
      article.querySelectorAll('[data-testid="User-Name"]').forEach((unb) => {
        for (const a of unb.querySelectorAll("a[href]")) {
          const h = parseHandleFromHref(a.getAttribute("href"));
          if (h) {
            addAcct(h, false);
            break;
          }
        }
        // Also check for @handle text (quote cards may not have <a> wrappers)
        const atMatch = (unb.textContent || "").match(/@(\w{4,15})/);
        if (atMatch && isLikelyHandle(atMatch[1])) addAcct(atMatch[1], false);
      });
      // @mention links in tweet text
      article
        .querySelectorAll('[data-testid="tweetText"] a[href]')
        .forEach((a) => {
          const href = a.getAttribute("href") || "";
          if (href.startsWith("/")) {
            const mh = parseHandleFromHref(href);
            if (mh && mh.length >= 4) addAcct(mh, false);
          }
        });
    }
    // Smart pre-selection: pick first handle with existing bookmarks, fall back to author
    function applySmartSelection() {
      let picked = false;
      acctList.forEach((a) => {
        a.selected = false;
      });
      if (_handleAliasCacheLoaded && _bookmarkedHandlesSet.size) {
        for (const acc of acctList) {
          const resolved = resolveHandleAlias(acc.handle).toLowerCase();
          if (
            _bookmarkedHandlesSet.has(acc.handle.toLowerCase()) ||
            _bookmarkedHandlesSet.has(resolved)
          ) {
            acc.selected = true;
            picked = true;
            break;
          }
        }
      }
      if (!picked && acctList.length) acctList[0].selected = true;
    }
    applySmartSelection();

    const acctLabel = document.createElement("span");
    acctLabel.className = "xdl-label-text";
    acctLabel.textContent = "Account";
    popover.appendChild(acctLabel);

    const acctRow = document.createElement("div");
    acctRow.style.cssText =
      "display:flex;flex-wrap:wrap;gap:6px;margin-bottom:12px;align-items:center;";

    function makePill(acc) {
      const resolved = resolveHandleAlias(acc.handle);
      const pill = document.createElement("button");
      pill.type = "button";
      pill.className = "xdl-cat-pill" + (acc.selected ? " selected" : "");
      pill.textContent = resolved;
      pill.dataset.originalHandle = acc.handle;
      pill.dataset.selected = acc.selected ? "1" : "0";
      pill.addEventListener("click", () => {
        const isSel = pill.dataset.selected === "1";
        pill.dataset.selected = isSel ? "0" : "1";
        pill.classList.toggle("selected", !isSel);
        updatePreview();
      });
      pill.addEventListener("dblclick", (e) => {
        e.preventDefault();
        e.stopPropagation();
        const inp = document.createElement("input");
        inp.type = "text";
        inp.value = pill.textContent;
        inp.style.cssText =
          "background:var(--igloo-x-control-surface0);border:1px solid var(--igloo-x-control-accent);color:var(--igloo-x-control-text);border-radius:9999px;padding:6px 14px;font-size:13px;font-weight:600;outline:none;width:" +
          Math.max(80, pill.offsetWidth + 10) +
          "px;";
        pill.replaceWith(inp);
        inp.focus();
        inp.select();
        const finish = () => {
          const val = inp.value.trim();
          if (val && val.toLowerCase() !== acc.handle.toLowerCase()) {
            saveHandleAlias(acc.handle, val);
            pill.textContent = val;
          } else {
            pill.textContent = resolveHandleAlias(acc.handle);
          }
          inp.replaceWith(pill);
          updatePreview();
        };
        inp.addEventListener("blur", finish);
        inp.addEventListener("keydown", (ke) => {
          if (ke.key === "Enter") {
            ke.preventDefault();
            finish();
          }
        });
      });
      return pill;
    }

    for (const acc of acctList) acctRow.appendChild(makePill(acc));
    popover.appendChild(acctRow);

    const lblSpan = document.createElement("span");
    lblSpan.className = "xdl-label-text";
    lblSpan.textContent = "Label";
    popover.appendChild(lblSpan);

    const labelInput = document.createElement("input");
    labelInput.type = "text";
    labelInput.setAttribute("autocomplete", "off");
    labelInput.placeholder = "label (empty = tweet ID)";
    labelInput.style.cssText =
      "width:100%;box-sizing:border-box;background:var(--igloo-x-control-surface0);border:1px solid var(--igloo-x-control-surface1);color:var(--igloo-x-control-text);border-radius:8px;padding:10px 12px;font-size:14px;margin-bottom:12px;outline:none;";
    labelInput.addEventListener("focus", () => {
      labelInput.style.borderColor = "var(--igloo-x-control-accent)";
    });
    labelInput.addEventListener("blur", () => {
      labelInput.style.borderColor = "var(--igloo-x-control-surface1)";
    });
    const dlListId = "xdl-labels-" + tweetId;
    labelInput.setAttribute("list", dlListId);
    const dlDatalist = document.createElement("datalist");
    dlDatalist.id = dlListId;
    dlLabels.forEach((lbl) => {
      const o = document.createElement("option");
      o.value = lbl;
      dlDatalist.appendChild(o);
    });
    popover.appendChild(labelInput);
    popover.appendChild(dlDatalist);

    // Media index selector — numbered buttons, all selected by default
    if (shouldShowMediaIndexPicker(mediaCount)) {
      const mediaLbl = document.createElement("span");
      mediaLbl.className = "xdl-label-text";
      mediaLbl.textContent = "Pick # to download";
      popover.appendChild(mediaLbl);
      const mediaRow = document.createElement("div");
      mediaRow.className = "xdl-media-idx-row";
      for (let i = 0; i < mediaCount; i++) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "xdl-media-idx-btn selected";
        btn.textContent = String(i + 1);
        btn.dataset.mediaIdx = String(i);
        btn.dataset.selected = "1";
        btn.addEventListener("click", () => {
          const sel = btn.dataset.selected === "1";
          btn.dataset.selected = sel ? "0" : "1";
          btn.classList.toggle("selected", !sel);
          updatePreview();
        });
        mediaRow.appendChild(btn);
      }
      popover.appendChild(mediaRow);
    }

    const preview = document.createElement("div");
    preview.className = "xdl-preview";
    preview.textContent = "\u2026";
    popover.appendChild(preview);

    const confirmBtn = document.createElement("button");
    confirmBtn.className = "xdl-confirm";
    confirmBtn.textContent = "Download \u21b5";
    popover.appendChild(confirmBtn);

    document.body.appendChild(popover);
    const rect = anchorBtn.getBoundingClientRect();
    const scrollY = window.scrollY || window.pageYOffset;
    const scrollX = window.scrollX || window.pageXOffset;
    const popH = popover.offsetHeight || 300;
    // Prefer above button; if cut off at top, flip to below
    let popTop = rect.top + scrollY - popH - 8;
    if (popTop < scrollY + 6) popTop = rect.bottom + scrollY + 8;
    popover.style.top = popTop + "px";
    popover.style.left =
      Math.min(
        rect.left + scrollX,
        document.documentElement.scrollWidth - 350,
      ) + "px";
    setTimeout(() => labelInput.focus(), 50);

    function getEffectiveHandle() {
      // Join all selected pills' text (resolved aliases)
      const selected = Array.from(
        acctRow.querySelectorAll('.xdl-cat-pill[data-selected="1"]'),
      );
      const joined = selected
        .map((p) => p.textContent.trim())
        .filter(Boolean)
        .join(" ");
      return joined || handle || "user";
    }

    function getSelectedMediaCount() {
      const btns = popover.querySelectorAll(".xdl-media-idx-btn");
      if (!btns.length) return mediaCount;
      let n = 0;
      btns.forEach((b) => {
        if (b.dataset.selected === "1") n++;
      });
      return n || mediaCount;
    }

    let previewTimer = null,
      previewGen = 0;
    async function updatePreview() {
      const gen = ++previewGen;
      const lbl = labelInput.value.trim();
      const effectiveHandle = getEffectiveHandle();
      const effectiveLbl = lbl || tweetId || "media";
      const selCount = getSelectedMediaCount();
      const nextIdx = await getDlNextIndex(
        effectiveHandle,
        effectiveLbl,
        selectedCatId,
      );
      if (gen !== previewGen) return; // stale response, skip
      const pad = (n) => String(n).padStart(3, "0");
      const name = effectiveHandle + " " + effectiveLbl;
      const suffix = lbl ? "" : " (tweet ID)";
      if (selCount <= 1) {
        preview.textContent =
          "Saving as: " + name + " " + pad(nextIdx) + suffix;
      } else {
        preview.textContent =
          "Saving as: " +
          name +
          " " +
          pad(nextIdx) +
          " \u2026 " +
          pad(nextIdx + selCount - 1) +
          suffix;
      }
    }
    labelInput.addEventListener("input", () => {
      clearTimeout(previewTimer);
      previewTimer = setTimeout(updatePreview, 400);
    });
    updatePreview();

    let _outside = null,
      _esc = null;
    function dismiss() {
      popover.remove();
      if (_outside) document.removeEventListener("mousedown", _outside);
      if (_esc) document.removeEventListener("keydown", _esc);
    }
    function doConfirm() {
      const lbl = labelInput.value.trim() || tweetId || "media";
      const effectiveHandle = getEffectiveHandle();
      const mediaBtns = popover.querySelectorAll(".xdl-media-idx-btn");
      let selectedIndices = null;
      if (mediaBtns.length) {
        selectedIndices = [];
        mediaBtns.forEach((b) => {
          if (b.dataset.selected === "1")
            selectedIndices.push(parseInt(b.dataset.mediaIdx, 10));
        });
        selectedIndices = normalizeSelectedMediaIndices(
          selectedIndices,
          mediaBtns.length,
        );
      }
      GM_setValue("xdl_last_category", selectedCatId);
      dismiss();
      onConfirm(selectedCatId, lbl, effectiveHandle, selectedIndices);
    }
    confirmBtn.addEventListener("click", doConfirm);

    function selectCatByOffset(offset) {
      if (!cats.length || !catRow) return;
      const curIdx = cats.findIndex((c) => c.id === selectedCatId);
      const nextIdx = (curIdx + offset + cats.length) % cats.length;
      selectedCatId = cats[nextIdx].id;
      catRow
        .querySelectorAll(".xdl-cat-pill")
        .forEach((p, i) => p.classList.toggle("selected", i === nextIdx));
      updatePreview();
    }

    function handleInputKeydown(e) {
      if (e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        doConfirm();
      } else if (e.key === "Tab" && !e.shiftKey) {
        e.preventDefault();
        e.stopPropagation();
        selectCatByOffset(1);
      } else if (e.key === "Tab" && e.shiftKey) {
        e.preventDefault();
        e.stopPropagation();
        selectCatByOffset(-1);
      }
    }
    labelInput.addEventListener("keydown", handleInputKeydown);
    setTimeout(() => {
      _outside = (e) => {
        if (!popover.contains(e.target) && e.target !== anchorBtn) dismiss();
      };
      _esc = (e) => {
        if (e.key === "Escape") dismiss();
      };
      document.addEventListener("mousedown", _outside);
      document.addEventListener("keydown", _esc);
    }, 80);
  }

  function shouldShowMediaIndexPicker(mediaCount) {
    return mediaCount >= 1;
  }

  function normalizeSelectedMediaIndices(selectedIndices, mediaCount) {
    if (!Array.isArray(selectedIndices) || mediaCount <= 0) return null;
    const valid = Array.from(
      new Set(
        selectedIndices.filter(
          (idx) => Number.isInteger(idx) && idx >= 0 && idx < mediaCount,
        ),
      ),
    ).sort((a, b) => a - b);
    if (!valid.length || valid.length === mediaCount) return null;
    return valid;
  }

  // ── DL button state ────────────────────────────────────────────────────────
  function setDlButtonState(btn, state) {
    if (!btn) return;
    btn.disabled = false;
    btn.classList.remove("done", "error");
    if (!btn._svgIcon && btn.querySelector("svg"))
      btn._svgIcon = btn.querySelector("svg");
    function reset() {
      btn.classList.remove("done", "error");
      btn.textContent = "";
      if (btn._svgIcon) btn.appendChild(btn._svgIcon);
    }
    if (state === "loading") {
      btn.textContent = "\u2026";
      btn.disabled = true;
    } else if (state === "done") {
      btn.textContent = "\u2713";
      btn.classList.add("done");
      setTimeout(reset, 2000);
    } else if (state === "error") {
      btn.textContent = "\u2717";
      btn.classList.add("error");
      setTimeout(reset, 2000);
    }
  }

  // ── Download: server-backed archive writes ────────────────────────────────
  // Keep X media downloads out of page globals and browser staging. The local
  // Igloo server owns archive writes via yt-dlp/image download endpoints.
  function saveImageViaServer(media, handle, label, categoryId) {
    return apiRequest(
      "POST",
      "/api/tweet-media-save",
      {
        urls: [media.url],
        handle,
        label,
        category_id: categoryId,
      },
      true,
    );
  }

  function downloadTweetMediaViaServer(media, handle, label, categoryId) {
    const body = {
      tweet_url: media.tweetUrl,
      handle,
      label,
      category_id: categoryId,
    };
    if (media.url) body.media_url = media.url;
    if (media.mediaId) body.media_id = media.mediaId;
    if (Number.isInteger(media.index)) body.media_index = media.index;
    return apiRequest("POST", "/api/tweet-media-dl", body, true);
  }

  function mediaDownloadResult(moved, failed) {
    const success = moved.length > 0 && failed.length === 0;
    return {
      ok: moved.length > 0,
      json: {
        success,
        partial: moved.length > 0 && failed.length > 0,
        moved,
        failed,
      },
    };
  }

  function normalizeMediaDownloadResponse(resp) {
    const json = resp?.json || {};
    const moved = Array.isArray(json.moved) ? json.moved : [];
    const failed = Array.isArray(json.failed) ? json.failed : [];
    const hasMoved = moved.length > 0;
    const failedByServer =
      resp?.ok === false || json.success === false || failed.length > 0;
    return {
      moved,
      failed,
      success: hasMoved && failed.length === 0,
      partial: hasMoved && failedByServer,
    };
  }

  function downloadErrorText(err) {
    if (!err) return "download failed";
    if (typeof err === "string") return err;
    if (err.error) return String(err.error);
    if (err.message) return String(err.message);
    try {
      return JSON.stringify(err);
    } catch (_) {
      return "download failed";
    }
  }

  function mediaFailureLabel(media, err) {
    const owner = media?.tweetId ? "tweet " + media.tweetId : "media";
    return owner + ": " + downloadErrorText(err);
  }

  function downloadMediaItems(
    tweetId,
    handle,
    mediaItems,
    categoryId,
    label,
    onComplete,
  ) {
    if (!mediaItems.length) {
      onComplete({ ok: false });
      return;
    }

    (async () => {
      const moved = [];
      const failed = [];
      for (let i = 0; i < mediaItems.length; i++) {
        const media = mediaItems[i];
        const resp =
          media.kind === "video"
            ? await downloadTweetMediaViaServer(
                media,
                handle,
                label,
                categoryId,
              )
            : await saveImageViaServer(media, handle, label, categoryId);
        const normalized = normalizeMediaDownloadResponse(resp);
        if (normalized.success || normalized.partial) {
          moved.push(...normalized.moved);
          failed.push(...normalized.failed);
        } else {
          failed.push(mediaFailureLabel(media, resp.json || resp));
        }
      }
      onComplete(mediaDownloadResult(moved, failed));
    })();
  }

  async function handleDlClick(dlBtn, article) {
    const info = extractTweetUrl(article);
    if (!info) {
      notify("Could not find tweet URL");
      return;
    }

    const handle = extractHandleFromArticle(article) || info.handle;
    const mediaItems = collectTweetMediaItems(article);
    const downloadableItems = mediaItems.length
      ? mediaItems
      : [
          {
            kind: "video",
            tweetId: info.tweetId,
            tweetUrl: info.url,
            ext: ".mp4",
            index: 0,
          },
        ];

    if (dlCategories === null) {
      notify("Categories loading, try again");
      return;
    }

    showDlPopover(
      dlBtn,
      handle,
      info.tweetId,
      downloadableItems.length,
      async (catId, lbl, effectiveHandle, selectedIndices) => {
        setDlButtonState(dlBtn, "loading");

        function onSuccess() {
          markDownloaded(info.tweetId);
          setDlButtonState(dlBtn, "done");
          dlBtn.classList.add("downloaded");
        }

        const selectedMedia = selectedIndices
          ? downloadableItems.filter((_, i) => selectedIndices.includes(i))
          : downloadableItems;
        if (selectedMedia.length) {
          downloadMediaItems(
            info.tweetId,
            effectiveHandle,
            selectedMedia,
            catId,
            lbl,
            async (resp) => {
              const ok = resp.ok && resp.json && resp.json.success;
              const partial = resp.ok && resp.json && resp.json.partial;
              const movedCount = resp.json?.moved?.length || 0;
              if (ok) {
                onSuccess();
                showToast("Downloaded " + movedCount + " file(s): " + lbl);
                return;
              }
              if (partial) {
                onSuccess();
                showToast(
                  "Downloaded " +
                    movedCount +
                    " file(s), " +
                    (resp.json?.failed?.length || 0) +
                    " failed: " +
                    lbl,
                );
                return;
              }
              setDlButtonState(dlBtn, "error");
              const firstFailure = (resp.json?.failed || [])[0];
              showToast(
                "Download failed" + (firstFailure ? ": " + firstFailure : ""),
              );
            },
          );
        } else {
          setDlButtonState(dlBtn, "error");
          showToast("No media found or cached for this post");
        }
      },
      article,
    );
  }

  // ── Toast ──────────────────────────────────────────────────────────────────
  function showToast(message, actionLabel, actionCallback, duration = 3000) {
    document.getElementById("x-sync-toast")?.remove();
    const toast = document.createElement("div");
    toast.id = "x-sync-toast";
    toast.style.cssText =
      "position:fixed;bottom:28px;left:50%;transform:translateX(-50%);background:var(--igloo-x-control-base);color:var(--igloo-x-control-text);border:1px solid color-mix(in srgb, var(--igloo-x-control-overlay0) 55%, transparent);border-radius:8px;padding:11px 18px;font-size:14px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;z-index:999999;display:flex;align-items:center;gap:6px;box-shadow:0 4px 20px rgba(0,0,0,0.5);opacity:1;transition:opacity 0.3s ease;white-space:nowrap;";

    const text = document.createElement("span");
    text.textContent = message;
    toast.appendChild(text);

    let done = false;
    function dismiss(withAction) {
      if (done) return;
      done = true;
      if (withAction && actionCallback) actionCallback();
      toast.style.opacity = "0";
      setTimeout(() => toast.remove(), 320);
    }

    if (actionLabel && actionCallback) {
      const action = document.createElement("span");
      action.textContent = actionLabel;
      action.style.cssText =
        "text-decoration:underline;cursor:pointer;color:var(--igloo-x-control-accent);margin-left:2px;";
      action.addEventListener("click", () => dismiss(true));
      toast.appendChild(action);
    }

    document.body.appendChild(toast);
    setTimeout(() => dismiss(false), duration);
  }

  function showSaveToast(handle, undoCallback) {
    showToast(`You followed @${handle}.\u00a0`, "Undo", undoCallback);
  }

  function setCustomButtonState(btn, saved) {
    if (!btn) return;
    btn.textContent = saved ? "Following" : "Follow";
    btn.dataset.saved = saved ? "1" : "0";
  }

  // ── Follow / unfollow ──────────────────────────────────────────────────────
  async function syncToServer(handle) {
    if (!syncToDashboardEnabled()) return { skipped: true };
    const resp = await apiRequest(
      "POST",
      "/api/subscribe",
      { url: `https://x.com/${handle}` },
      true,
    );
    if (resp.ok) return { ok: true, body: resp.json };
    return { ok: false, ...resp };
  }

  async function handleUnsave(handle, triggerBtn) {
    if (triggerBtn) {
      triggerBtn.disabled = true;
      triggerBtn.textContent = "Removing...";
    }

    const channelId = serverHandleToChannelId[handle.toLowerCase()];
    if (channelId) {
      const resp = await apiRequest(
        "DELETE",
        `/api/unsubscribe/${channelId}`,
        null,
        true,
      );
      if (!resp.ok)
        console.warn(`[XSync] server unfollow failed handle=${handle}`, resp);
    }
    if (serverHandleSet) serverHandleSet.delete(handle.toLowerCase());
    delete serverHandleToChannelId[handle.toLowerCase()];
    removeLocal(handle);

    if (triggerBtn) {
      triggerBtn.disabled = false;
      setCustomButtonState(triggerBtn, false);
    }
    notify(`Removed @${handle}`);
  }

  async function handleFollowClick(handle, triggerBtn) {
    if (!handle) return;
    if (isSaved(handle)) {
      return handleUnsave(handle, triggerBtn);
    }

    if (triggerBtn) {
      triggerBtn.disabled = true;
      triggerBtn.textContent = "Following...";
    }

    if (serverHandleSet) serverHandleSet.add(handle.toLowerCase());
    saveLocal(handle);
    syncToServer(handle).then((save) => {
      if (!save.ok && !save.skipped)
        console.warn(`[XSync] server follow failed handle=${handle}`);
    });

    showSaveToast(handle, () => {
      if (serverHandleSet) serverHandleSet.delete(handle.toLowerCase());
      removeLocal(handle);
      if (triggerBtn) setCustomButtonState(triggerBtn, false);
    });

    if (triggerBtn) {
      triggerBtn.disabled = false;
      setCustomButtonState(triggerBtn, true);
    }
  }

  let xFeedSourceMap = new Map();

  function parseXFeedSourceFromLocation() {
    const m = location.pathname.match(/^\/i\/(lists|communities)\/([^/?#]+)/);
    if (!m) return null;
    const type = m[1] === "lists" ? "list" : "community";
    const id = decodeURIComponent(m[2]);
    const sourceId =
      type === "list" ? `twitter_list_${id}` : `twitter_community_${id}`;
    return {
      sourceId,
      type,
      id,
      url: `https://x.com/i/${m[1]}/${encodeURIComponent(id)}`,
      label: type === "list" ? `X List ${id}` : `X Community ${id}`,
    };
  }

  async function fetchXFeedSources() {
    const resp = await apiRequest(
      "GET",
      "/api/feed/sources?platform=twitter",
      null,
      true,
    );
    if (!resp.ok) return;
    const sources =
      resp.json && Array.isArray(resp.json.sources) ? resp.json.sources : [];
    const map = new Map();
    for (const source of sources) {
      if (source && source.source_id) map.set(String(source.source_id), source);
    }
    xFeedSourceMap = map;
    updateXSourceButton();
  }

  function setXSourceButtonState(btn, saved) {
    if (!btn) return;
    btn.textContent = saved ? "Following source" : "Follow source";
    btn.dataset.saved = saved ? "1" : "0";
    btn.title = saved
      ? "Remove X source from Igloo"
      : "Follow X source in Igloo";
  }

  async function saveXFeedSource(info, btn) {
    if (!syncToDashboardEnabled()) {
      showToast("Server sync is disabled");
      return;
    }
    btn.disabled = true;
    btn.textContent = "Saving...";
    const resp = await apiRequest(
      "POST",
      "/api/feed/sources",
      { url: info.url, label: info.label },
      true,
    );
    if (resp.ok) {
      xFeedSourceMap.set(
        info.sourceId,
        resp.json && resp.json.source
          ? resp.json.source
          : { source_id: info.sourceId },
      );
      btn.disabled = false;
      setXSourceButtonState(btn, true);
      showToast(
        "Followed source in Igloo",
        "Undo",
        () => removeXFeedSource(info, btn),
        4000,
      );
      return;
    }
    btn.disabled = false;
    setXSourceButtonState(btn, false);
    showToast(`Follow source failed (${resp.status || resp.error || "error"})`);
  }

  async function removeXFeedSource(info, btn) {
    btn.disabled = true;
    btn.textContent = "Removing...";
    const resp = await apiRequest(
      "DELETE",
      `/api/feed/sources/${encodeURIComponent(info.sourceId)}`,
      null,
      true,
    );
    if (resp.ok) {
      xFeedSourceMap.delete(info.sourceId);
      btn.disabled = false;
      setXSourceButtonState(btn, false);
      showToast("Removed source from Igloo");
      return;
    }
    btn.disabled = false;
    setXSourceButtonState(btn, true);
    showToast(`Remove source failed (${resp.status || resp.error || "error"})`);
  }

  function updateXSourceButton() {
    const btn = document.getElementById("igloo-x-source-save-btn");
    if (!btn) return;
    const info = parseXFeedSourceFromLocation();
    if (!info) {
      btn.remove();
      return;
    }
    btn.dataset.sourceId = info.sourceId;
    btn.dataset.url = info.url;
    setXSourceButtonState(btn, xFeedSourceMap.has(info.sourceId));
  }

  function mountXFeedSourceButton() {
    ensureButtonStyles();
    const info = parseXFeedSourceFromLocation();
    if (!info) {
      document.getElementById("igloo-x-source-save-btn")?.remove();
      return;
    }
    let btn = document.getElementById("igloo-x-source-save-btn");
    if (!btn) {
      btn = document.createElement("button");
      btn.id = "igloo-x-source-save-btn";
      btn.type = "button";
      btn.className = "x-source-save-btn";
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        const current = parseXFeedSourceFromLocation();
        if (!current || btn.disabled) return;
        if (btn.dataset.saved === "1") removeXFeedSource(current, btn);
        else saveXFeedSource(current, btn);
      });
      document.body.appendChild(btn);
    }
    updateXSourceButton();
  }

  function refreshButtonStates() {
    for (const btn of document.querySelectorAll(".x-sync-btn[data-handle]")) {
      const handle = String(btn.dataset.handle || "").trim();
      if (handle) setCustomButtonState(btn, isSaved(handle));
    }
  }

  // ── Mount buttons ─────────────────────────────────────────────────────────
  function mountFeedButtons() {
    ensureButtonStyles();
    for (const caret of document.querySelectorAll(
      "article [data-testid='caret']",
    )) {
      const row = caret.parentElement;
      if (!row || row.querySelector(".x-sync-btn")) continue;
      const article = caret.closest("article");
      if (!article) continue;
      const handle = extractHandleFromArticle(article);
      if (!handle) continue;

      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "x-sync-btn";
      btn.dataset.handle = handle;
      setCustomButtonState(btn, isSaved(handle));
      btn.title = `Follow @${handle} in Igloo`;
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        handleFollowClick(handle, btn);
      });
      row.insertBefore(btn, caret);
    }
  }

  function hideNativeButtons() {
    if (!buttonOverridesEnabled()) return;
    const selectors = [
      '[data-testid="bookmark"]',
      '[data-testid="removeBookmark"]',
    ];
    for (const sel of selectors) {
      for (const el of document.querySelectorAll(`[role="group"] ${sel}`)) {
        const actionBar = el.closest('[role="group"]');
        if (!actionBar) continue;
        let wrap = el;
        while (wrap && wrap.parentElement !== actionBar)
          wrap = wrap.parentElement;
        if (
          wrap &&
          wrap.style.display !== "none" &&
          !wrap.dataset.iglooHiddenNativeButton
        ) {
          wrap.dataset.iglooPreviousDisplay = wrap.style.display || "";
          wrap.dataset.iglooHiddenNativeButton = "1";
          wrap.style.display = "none";
        }
      }
    }
  }

  function restoreNativeButtons() {
    for (const wrap of document.querySelectorAll(
      "[data-igloo-hidden-native-button]",
    )) {
      wrap.style.display = wrap.dataset.iglooPreviousDisplay || "";
      delete wrap.dataset.iglooHiddenNativeButton;
      delete wrap.dataset.iglooPreviousDisplay;
    }
  }

  function clearButtonOverrides() {
    document.getElementById("x-dl-popover")?.remove();
    for (const selector of [
      ".x-sync-btn",
      "#igloo-x-source-save-btn",
      ".x-action-wrap",
      ".igloo-cross-save-btn",
    ]) {
      for (const el of document.querySelectorAll(selector)) el.remove();
    }
  }

  function clearDlButtons() {
    document.getElementById("x-dl-popover")?.remove();
    for (const wrap of document.querySelectorAll(".x-action-wrap"))
      wrap.remove();
  }

  function mountDlButtons() {
    if (!xDownloadsEnabled()) {
      clearDlButtons();
      return;
    }
    ensureButtonStyles();
    for (const article of document.querySelectorAll(
      'article[data-testid="tweet"]',
    )) {
      const info = extractTweetUrl(article);
      if (!info) continue;

      const actionBar = article.querySelector('[role="group"]');
      if (
        !actionBar ||
        actionBar.querySelector(
          '.x-dl-action-btn[data-tweet="' + info.tweetId + '"]',
        )
      )
        continue;

      const svgNS = "http://www.w3.org/2000/svg";

      // Download button
      const dlWrap = document.createElement("div");
      dlWrap.className = "x-action-wrap";
      const dlBtn = document.createElement("button");
      dlBtn.type = "button";
      dlBtn.className = "x-dl-action-btn";
      dlBtn.dataset.tweet = info.tweetId;
      dlBtn.title = "Download media";

      const dlSvg = document.createElementNS(svgNS, "svg");
      dlSvg.setAttribute("viewBox", "0 0 24 24");
      dlSvg.setAttribute("fill", "none");
      dlSvg.setAttribute("stroke", "currentColor");
      dlSvg.setAttribute("stroke-width", "2");
      dlSvg.setAttribute("stroke-linecap", "round");
      dlSvg.setAttribute("stroke-linejoin", "round");
      const p1 = document.createElementNS(svgNS, "path");
      p1.setAttribute("d", "M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4");
      const p2 = document.createElementNS(svgNS, "polyline");
      p2.setAttribute("points", "7 10 12 15 17 10");
      const p3 = document.createElementNS(svgNS, "line");
      p3.setAttribute("x1", "12");
      p3.setAttribute("y1", "15");
      p3.setAttribute("x2", "12");
      p3.setAttribute("y2", "3");
      dlSvg.appendChild(p1);
      dlSvg.appendChild(p2);
      dlSvg.appendChild(p3);
      dlBtn.appendChild(dlSvg);

      if (isDownloaded(info.tweetId)) {
        dlBtn.classList.add("downloaded");
      }

      dlBtn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        handleDlClick(dlBtn, article);
      });

      dlWrap.appendChild(dlBtn);
      actionBar.appendChild(dlWrap);

      // Share button (copies fxtwitter link)
      if (!actionBar.querySelector(".x-share-btn")) {
        const shareWrap = document.createElement("div");
        shareWrap.className = "x-action-wrap";
        const shareBtn = document.createElement("button");
        shareBtn.type = "button";
        shareBtn.className = "x-share-btn";
        shareBtn.title = "Copy fxtwitter link";

        const shareSvg = document.createElementNS(svgNS, "svg");
        shareSvg.setAttribute("viewBox", "0 0 24 24");
        shareSvg.setAttribute("fill", "none");
        shareSvg.setAttribute("stroke", "currentColor");
        shareSvg.setAttribute("stroke-width", "1.75");
        shareSvg.setAttribute("stroke-linecap", "round");
        shareSvg.setAttribute("stroke-linejoin", "round");
        // Link/chain icon — fits the "share as link" concept
        const sp1 = document.createElementNS(svgNS, "path");
        sp1.setAttribute(
          "d",
          "M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71",
        );
        const sp2 = document.createElementNS(svgNS, "path");
        sp2.setAttribute(
          "d",
          "M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71",
        );
        shareSvg.appendChild(sp1);
        shareSvg.appendChild(sp2);
        shareBtn.appendChild(shareSvg);

        shareBtn.addEventListener("click", (e) => {
          e.preventDefault();
          e.stopPropagation();
          const fxUrl = info.url.replace(
            "https://x.com",
            "https://fxtwitter.com",
          );
          GM_setClipboard(fxUrl, "text");
          shareBtn.classList.add("copied");
          setTimeout(() => shareBtn.classList.remove("copied"), 1500);
          showToast("Link copied to clipboard");
        });

        shareWrap.appendChild(shareBtn);
        actionBar.appendChild(shareWrap);
      }
    }
  }

  // ── Menu commands ─────────────────────────────────────────────────────────
  function saveApiBaseSetting(value) {
    GM_setValue(SETTINGS.apiBase, normalizeApiBase(value));
    _resolvedApiBase = "";
    _resolvingApiBase = null;
  }

  function promptForApiBase(notifyOnSave) {
    const next = prompt(
      "Server API base URL",
      getApiBase() || DEFAULT_API_BASE,
    );
    if (next === null) return false;
    saveApiBaseSetting(next);
    if (notifyOnSave) notify("Saved server URL");
    return true;
  }

  function refreshXThemeStyles(options = {}) {
    if (!isXSite()) return;
    ensureButtonStyles();
    if (!themeOverridesEnabled()) {
      syncXOverrideClasses();
      applyXThemeSettings();
      clearXComposerToolbarTheme();
      return;
    }
    fetchIglooThemePalette(!!options.force).then((ok) => {
      applyXThemeSettings();
      syncXOverrideClasses();
      applyXComposerToolbarTheme();
      if (!ok && options.notifyFailure) {
        notify("Theme unavailable: could not read Igloo web theme");
      }
    });
    applyXThemeSettings();
    syncXOverrideClasses();
    applyXComposerToolbarTheme();
  }

  function registerMenu() {
    GM_registerMenuCommand("Log in to server", async () => {
      if (!promptForApiBase(false)) return;
      const username = prompt(
        "Server username",
        GM_getValue(SETTINGS.authUser, ""),
      );
      if (!username) return;
      const password = prompt("Server password");
      if (!password) return;
      const resp = await _rawApiRequest(
        "POST",
        "/api/auth/login",
        { username, password },
        false,
      );
      if (!resp.ok || !storeAuthTokens(resp.json)) {
        notify(
          "Login failed: " +
            (resp.json?.error_message ||
              resp.json?.error_code ||
              resp.error ||
              resp.status ||
              "unknown"),
        );
        return;
      }
      GM_setValue(SETTINGS.authUser, username);
      GM_setValue(SETTINGS.syncToDashboard, true);
      GM_setValue(SETTINGS.saveLocal, true);
      forgetLegacyDashboardPassword();
      notify(`Logged in to server as ${username}`);
      if (isXSite()) {
        fetchServerHandles();
        if (xDownloadsEnabled()) {
          fetchDlCategories();
          fetchDlLabels();
          loadHandleAliasesFromApi();
        }
      } else if (currentPlatform() === "tiktok") {
        fetchPlatformChannels("tiktok");
      } else if (currentPlatform() === "instagram") {
        fetchPlatformChannels("instagram");
      } else if (currentPlatform() === "youtube") {
        fetchPlatformChannels("youtube");
      }
    });

    GM_registerMenuCommand("Test connection", async () => {
      const resp = await apiRequest("GET", "/api/stats", null, true);
      notify(
        resp.ok
          ? "Server connection OK"
          : `Connection failed (${resp.status || resp.error || "error"})`,
      );
    });

    GM_registerMenuCommand("Toggle button overrides", () => {
      const next = !buttonOverridesEnabled();
      GM_setValue(SETTINGS.buttonOverrides, next);
      GM_setValue(SETTINGS.xDownloads, next);
      GM_setValue(SETTINGS.xKeyboardShortcuts, next);
      syncXOverrideClasses();
      if (next) {
        if (isXSite()) {
          fetchDlCategories();
          fetchDlLabels();
          loadHandleAliasesFromApi();
        }
        runCurrentPlatformScan();
      } else {
        clearButtonOverrides();
        restoreNativeButtons();
      }
      notify(`Button overrides ${next ? "enabled" : "disabled"}`);
    });

    GM_registerMenuCommand("Toggle theme", () => {
      const next = !themeOverridesEnabled();
      GM_setValue(SETTINGS.xCleanup, next);
      refreshXThemeStyles({ force: next, notifyFailure: next });
      notify(`Theme ${next ? "enabled" : "disabled"}`);
    });
  }

  // ── Keyboard shortcuts ───────────────────────────────────────────────────
  let _focusedArticleCache = null;
  let _focusCacheDirty = true;

  function getFocusedArticle() {
    if (
      !_focusCacheDirty &&
      _focusedArticleCache &&
      document.contains(_focusedArticleCache)
    ) {
      return _focusedArticleCache;
    }
    const articles = document.querySelectorAll('article[data-testid="tweet"]');
    let best = null,
      bestScore = -Infinity;
    const midY = window.innerHeight / 2;
    for (const a of articles) {
      const r = a.getBoundingClientRect();
      if (r.bottom < 0 || r.top > window.innerHeight) continue;
      const center = (r.top + r.bottom) / 2;
      const score = -Math.abs(center - midY);
      if (score > bestScore) {
        bestScore = score;
        best = a;
      }
    }
    _focusedArticleCache = best;
    _focusCacheDirty = false;
    return best;
  }

  function invalidateFocusCache() {
    _focusCacheDirty = true;
    _focusedArticleCache = null;
  }
  document.addEventListener("scroll", invalidateFocusCache, {
    passive: true,
    capture: true,
  });

  function clickArticleAction(article, testId) {
    if (!article) return;
    const btn = article.querySelector(`[data-testid="${testId}"]`);
    if (btn) btn.click();
  }

  function clickVisibleButton(selectors) {
    for (const selector of selectors) {
      const btn = document.querySelector(selector);
      if (!btn || btn.disabled) continue;
      const style = window.getComputedStyle(btn);
      const rect = btn.getBoundingClientRect();
      if (
        style.display === "none" ||
        style.visibility === "hidden" ||
        rect.width <= 0 ||
        rect.height <= 0
      )
        continue;
      btn.click();
      return true;
    }
    return false;
  }

  function clickCurrentFollowShortcut() {
    if (isXSite()) {
      const article = getFocusedArticle();
      const followBtn = article?.querySelector(".x-sync-btn[data-handle]");
      if (!followBtn || followBtn.disabled) return false;
      followBtn.click();
      return true;
    }
    const platform = currentPlatform();
    const selectorsByPlatform = {
      tiktok: ["#igloo-tiktok-save-btn", "#igloo-tiktok-save-fab"],
      instagram: [
        "#igloo-instagram-media-save-btn",
        "#igloo-instagram-save-btn",
        "#igloo-instagram-save-fab",
      ],
      youtube: ["#igloo-youtube-save-btn"],
    };
    return clickVisibleButton(selectorsByPlatform[platform] || []);
  }

  document.addEventListener(
    "keydown",
    (e) => {
      if (!xKeyboardShortcutsEnabled()) return;
      // Skip if user is typing in an input/textarea/contentEditable or popover is open
      const tag = (e.target.tagName || "").toLowerCase();
      if (tag === "input" || tag === "textarea" || e.target.isContentEditable)
        return;
      if (document.getElementById("x-dl-popover")) return;
      if (e.ctrlKey || e.altKey || e.metaKey) return;

      const key = e.key.toLowerCase();
      if (key === "f") {
        if (clickCurrentFollowShortcut()) {
          e.preventDefault();
          e.stopPropagation();
        }
        return;
      }

      const article = getFocusedArticle();
      if (!article) return;

      if (key === "l") {
        e.preventDefault();
        e.stopPropagation();
        const unlike = article.querySelector('[data-testid="unlike"]');
        if (unlike) unlike.click();
        else clickArticleAction(article, "like");
      } else if (key === "r") {
        e.preventDefault();
        e.stopPropagation();
        clickArticleAction(article, "retweet");
      } else if (key === "b") {
        e.preventDefault();
        e.stopPropagation();
        const dlBtn = article.querySelector(".x-dl-action-btn");
        if (dlBtn) dlBtn.click();
      } else if (key === "s") {
        e.preventDefault();
        e.stopPropagation();
        const shareBtn = article.querySelector(".x-share-btn");
        if (shareBtn) shareBtn.click();
      }
    },
    true,
  );

  // ── TikTok / YouTube adapters ─────────────────────────────────────────────
  const platformChannelMaps = {
    tiktok: new Map(),
    instagram: new Map(),
    youtube: new Map(),
  };
  let lastTikTokPath = "";
  let lastInstagramPath = "";
  let currentYouTubeKey = "";

  function ensureCrossSiteStyles() {
    if (document.getElementById("igloo-cross-site-style")) return;
    const style = document.createElement("style");
    style.id = "igloo-cross-site-style";
    style.textContent = `
    .igloo-cross-save-btn {
      border: 1px solid rgb(83, 100, 113);
      background: transparent;
      color: #cdd6f4;
      border-radius: 9999px;
      font-size: 13px;
      line-height: 16px;
      font-weight: 700;
      padding: 7px 13px;
      margin-left: 8px;
      cursor: pointer;
      transition: all 0.15s;
      white-space: nowrap;
      font-family: -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;
    }
    .igloo-cross-save-btn:hover { background: rgba(205,214,244,0.1); }
    .igloo-cross-save-btn[data-saved="1"] { border-color: rgba(83, 100, 113, 0.5); opacity: 0.65; color: #bac2de; }
    .igloo-cross-save-btn:disabled { opacity: 0.65; cursor: wait; }
    #igloo-tiktok-save-fab, #igloo-instagram-save-fab {
      position: fixed;
      right: 24px;
      bottom: 24px;
      z-index: 2147483647;
      background: #15202b;
      color: #C3735F;
      border-color: rgba(195,115,95,0.6);
      box-shadow: 0 4px 20px rgba(0,0,0,0.6);
    }
    #igloo-youtube-save-btn {
      height: 36px;
      padding: 0 14px;
      font-size: 14px;
    }
    `;
    document.head.appendChild(style);
  }

  function normalizePlatformHandle(value) {
    return String(value || "")
      .trim()
      .replace(/^@+/, "")
      .toLowerCase();
  }

  function normalizeYouTubeChannelID(value) {
    return String(value || "")
      .trim()
      .replace(/^youtube_/, "");
  }

  function platformChannelKeys(ch, platform) {
    const keys = [];
    const channelID = String(ch.channel_id || ch.id || "").trim();
    const rawURL = String(ch.url || "").trim();
    if (channelID) {
      keys.push(channelID.toLowerCase());
      if (platform === "tiktok" && channelID.startsWith("tiktok_")) {
        keys.push(channelID.slice("tiktok_".length).toLowerCase());
      }
      if (platform === "instagram" && channelID.startsWith("instagram_")) {
        keys.push(channelID.slice("instagram_".length).toLowerCase());
      }
      if (platform === "youtube") {
        keys.push(normalizeYouTubeChannelID(channelID).toLowerCase());
      }
    }
    try {
      const parsed = new URL(rawURL);
      const parts = parsed.pathname.split("/").filter(Boolean);
      if (platform === "tiktok" && parts[0] && parts[0].startsWith("@")) {
        keys.push(normalizePlatformHandle(parts[0]));
      }
      if (platform === "instagram" && parts[0]) {
        keys.push(normalizePlatformHandle(parts[0]));
      }
      if (platform === "youtube") {
        if (parts[0] === "channel" && parts[1])
          keys.push(normalizeYouTubeChannelID(parts[1]).toLowerCase());
        if (parts[0] && parts[0].startsWith("@"))
          keys.push(normalizePlatformHandle(parts[0]));
      }
    } catch (_) {}
    return Array.from(new Set(keys.filter(Boolean)));
  }

  async function fetchPlatformChannels(platform) {
    const resp = await apiRequest(
      "GET",
      `/api/channels?platform=${encodeURIComponent(platform)}`,
      null,
      true,
    );
    if (!resp.ok) return;
    const channels =
      resp.json &&
      (Array.isArray(resp.json.channels)
        ? resp.json.channels
        : Array.isArray(resp.json)
          ? resp.json
          : null);
    if (!channels) return;
    const map = new Map();
    for (const ch of channels) {
      for (const key of platformChannelKeys(ch, platform)) {
        map.set(key, String(ch.channel_id || ch.id || ""));
      }
    }
    platformChannelMaps[platform] = map;
    refreshCrossSiteButtons();
  }

  function isPlatformSaved(platform, key) {
    return !!(
      key &&
      platformChannelMaps[platform] &&
      platformChannelMaps[platform].has(String(key).toLowerCase())
    );
  }

  function platformChannelID(platform, key) {
    return (
      key &&
      platformChannelMaps[platform] &&
      platformChannelMaps[platform].get(String(key).toLowerCase())
    );
  }

  function setCrossButtonState(btn, saved) {
    btn.textContent = saved ? "Following" : "Follow";
    btn.dataset.saved = saved ? "1" : "0";
    btn.title = saved ? "Remove from Igloo subscriptions" : "Follow in Igloo";
  }

  function makeCrossSaveButton(id, platform, key, url) {
    const btn = document.createElement("button");
    if (id) btn.id = id;
    btn.type = "button";
    btn.className = "igloo-cross-save-btn";
    btn.dataset.platform = platform;
    btn.dataset.key = key;
    btn.dataset.url = url;
    setCrossButtonState(btn, isPlatformSaved(platform, key));
    btn.addEventListener("click", handleCrossSaveClick);
    return btn;
  }

  function refreshCrossSiteButtons() {
    for (const btn of document.querySelectorAll(
      ".igloo-cross-save-btn[data-platform]",
    )) {
      const platform = btn.dataset.platform;
      const key = btn.dataset.key;
      if (platform && key)
        setCrossButtonState(btn, isPlatformSaved(platform, key));
    }
  }

  async function handleCrossSaveClick(e) {
    e.preventDefault();
    e.stopPropagation();
    const btn = e.currentTarget;
    const platform = btn.dataset.platform;
    const key = btn.dataset.key;
    const url = btn.dataset.url;
    if (!platform || !key || !url || btn.disabled) return;
    if (btn.dataset.saved === "1") await unsaveCrossChannel(platform, key, btn);
    else await saveCrossChannel(platform, key, url, btn);
  }

  async function saveCrossChannel(platform, key, url, btn) {
    if (!syncToDashboardEnabled()) {
      showToast("Server sync is disabled");
      return;
    }
    btn.disabled = true;
    btn.textContent = "Following...";
    const resp = await apiRequest(
      "POST",
      "/api/subscribe",
      { url, platform },
      true,
    );
    if (resp.ok || resp.status === 409) {
      const channelID =
        (resp.json && resp.json.channel_id) ||
        platformChannelID(platform, key) ||
        "";
      if (channelID)
        platformChannelMaps[platform].set(String(key).toLowerCase(), channelID);
      btn.disabled = false;
      setCrossButtonState(btn, true);
      showToast(
        "Followed in Igloo",
        "Undo",
        () => unsaveCrossChannel(platform, key, btn),
        4000,
      );
      fetchPlatformChannels(platform);
      return;
    }
    btn.disabled = false;
    setCrossButtonState(btn, false);
    showToast(`Follow failed (${resp.status || resp.error || "error"})`);
    console.warn("[IglooSync] follow failed", resp);
  }

  async function unsaveCrossChannel(platform, key, btn) {
    const channelID = platformChannelID(platform, key);
    if (!channelID) {
      showToast("Channel id not known yet");
      return;
    }
    if (btn) {
      btn.disabled = true;
      btn.textContent = "Removing...";
    }
    const resp = await apiRequest(
      "DELETE",
      `/api/unsubscribe/${encodeURIComponent(channelID)}`,
      null,
      true,
    );
    if (resp.ok) {
      platformChannelMaps[platform].delete(String(key).toLowerCase());
      if (btn) {
        btn.disabled = false;
        setCrossButtonState(btn, false);
      }
      showToast("Removed from Igloo");
      fetchPlatformChannels(platform);
      return;
    }
    if (btn) {
      btn.disabled = false;
      setCrossButtonState(btn, true);
    }
    showToast(`Remove failed (${resp.status || resp.error || "error"})`);
  }

  function getTikTokHandleFromPath() {
    const m = location.pathname.match(/\/@([^/?#]+)/);
    return m ? normalizePlatformHandle(m[1]) : "";
  }

  function getVisibleTikTokHandle() {
    const pathHandle = getTikTokHandleFromPath();
    if (pathHandle) return pathHandle;
    const cy = window.innerHeight / 2;
    let best = "";
    let bestDist = Infinity;
    for (const a of document.querySelectorAll('a[href*="/@"]')) {
      const href = a.href || a.getAttribute("href") || "";
      const m = href.match(/\/@([^/?#]+)/);
      if (!m) continue;
      const rect = a.getBoundingClientRect();
      if (
        !rect.width ||
        !rect.height ||
        rect.bottom < 0 ||
        rect.top > window.innerHeight
      )
        continue;
      if ((rect.left + rect.right) / 2 < 240) continue;
      const dist = Math.abs((rect.top + rect.bottom) / 2 - cy);
      if (dist < bestDist) {
        bestDist = dist;
        best = normalizePlatformHandle(m[1]);
      }
    }
    return best;
  }

  function ensureTikTokFab() {
    if (document.getElementById("igloo-tiktok-save-fab")) return;
    const btn = makeCrossSaveButton("igloo-tiktok-save-fab", "tiktok", "", "");
    document.body.appendChild(btn);
  }

  function mountTikTokProfileButton() {
    const handle = getTikTokHandleFromPath();
    if (!handle) return;
    const followBtn = document.querySelector('[data-e2e="follow-button"]');
    if (!followBtn) return;
    const existing = document.getElementById("igloo-tiktok-save-btn");
    if (existing && existing.dataset.key === handle) return;
    existing?.remove();
    const btn = makeCrossSaveButton(
      "igloo-tiktok-save-btn",
      "tiktok",
      handle,
      `https://www.tiktok.com/@${handle}`,
    );
    btn.style.height = "36px";
    btn.style.borderRadius = "4px";
    followBtn.insertAdjacentElement("afterend", btn);
  }

  function updateTikTokFab() {
    const btn = document.getElementById("igloo-tiktok-save-fab");
    if (!btn) return;
    const handle = getVisibleTikTokHandle();
    if (!handle) {
      btn.style.display = "none";
      return;
    }
    btn.dataset.key = handle;
    btn.dataset.url = `https://www.tiktok.com/@${handle}`;
    setCrossButtonState(btn, isPlatformSaved("tiktok", handle));
    btn.style.display = "inline-flex";
  }

  function mountTikTokButtons() {
    ensureCrossSiteStyles();
    ensureTikTokFab();
    const changed = lastTikTokPath !== location.pathname;
    lastTikTokPath = location.pathname;
    if (changed) document.getElementById("igloo-tiktok-save-btn")?.remove();
    mountTikTokProfileButton();
    updateTikTokFab();
  }

  const instagramReservedPaths = new Set([
    "p",
    "reel",
    "reels",
    "stories",
    "explore",
    "accounts",
    "direct",
    "about",
    "developer",
    "legal",
    "privacy",
    "terms",
  ]);

  function getInstagramHandleFromPath() {
    const parts = location.pathname.split("/").filter(Boolean);
    if (!parts.length) return "";
    if (parts[0] === "stories" && parts[1])
      return normalizePlatformHandle(parts[1]);
    if (instagramReservedPaths.has(parts[0])) return "";
    return normalizePlatformHandle(parts[0]);
  }

  function instagramRouteKind() {
    const first = location.pathname.split("/").filter(Boolean)[0] || "";
    if (first === "p" || first === "reel" || first === "reels") return first;
    return "";
  }

  function instagramHandleFromHref(href) {
    try {
      const parsed = new URL(href, location.origin);
      if (parsed.hostname && !/(^|\.)instagram\.com$/i.test(parsed.hostname))
        return "";
      const parts = parsed.pathname.split("/").filter(Boolean);
      if (
        instagramReservedPaths.has(parts[0]) ||
        !(parts.length === 1 || (parts.length === 2 && parts[1] === "reels"))
      )
        return "";
      const handle = normalizePlatformHandle(parts[0]);
      return /^[a-z0-9_.]{1,64}$/.test(handle) ? handle : "";
    } catch (_) {
      return "";
    }
  }

  function visibleInstagramOwnerLinks(root) {
    if (!root) return [];
    const links = [];
    for (const a of root.querySelectorAll(
      'a[href^="/"], a[href^="https://www.instagram.com/"], a[href^="https://instagram.com/"]',
    )) {
      const handle = instagramHandleFromHref(
        a.getAttribute("href") || a.href || "",
      );
      if (!handle) continue;
      const rect = a.getBoundingClientRect();
      if (
        !rect.width ||
        !rect.height ||
        rect.bottom < 0 ||
        rect.top > window.innerHeight ||
        rect.right < 0 ||
        rect.left > window.innerWidth
      )
        continue;
      links.push({ handle, link: a, rect });
    }
    links.sort((a, b) => {
      const top = Math.abs(a.rect.top - b.rect.top);
      if (top > 8) return a.rect.top - b.rect.top;
      return a.rect.left - b.rect.left;
    });
    return links;
  }

  function currentInstagramMediaOwner() {
    if (!instagramRouteKind()) return null;
    const roots = [
      document.querySelector('div[role="dialog"] article'),
      document.querySelector('div[role="dialog"]'),
      ...document.querySelectorAll("article"),
      document.querySelector("main"),
    ].filter(Boolean);
    for (const root of roots) {
      const links = visibleInstagramOwnerLinks(root);
      if (links.length) return links[0];
    }
    return null;
  }

  function getVisibleInstagramHandle() {
    const pathHandle = getInstagramHandleFromPath();
    if (pathHandle) return pathHandle;
    const mediaOwner = currentInstagramMediaOwner();
    if (mediaOwner && mediaOwner.handle) return mediaOwner.handle;
    const cy = window.innerHeight / 2;
    let best = "";
    let bestDist = Infinity;
    for (const a of document.querySelectorAll('a[href^="/"]')) {
      const href = a.getAttribute("href") || "";
      const parts = href.split("?")[0].split("/").filter(Boolean);
      if (parts.length !== 1 || instagramReservedPaths.has(parts[0])) continue;
      const handle = normalizePlatformHandle(parts[0]);
      if (!handle || !/^[a-z0-9_.]{1,64}$/.test(handle)) continue;
      const rect = a.getBoundingClientRect();
      if (
        !rect.width ||
        !rect.height ||
        rect.bottom < 0 ||
        rect.top > window.innerHeight
      )
        continue;
      const dist = Math.abs((rect.top + rect.bottom) / 2 - cy);
      if (dist < bestDist) {
        bestDist = dist;
        best = handle;
      }
    }
    return best;
  }

  function ensureInstagramFab() {
    if (document.getElementById("igloo-instagram-save-fab")) return;
    const btn = makeCrossSaveButton(
      "igloo-instagram-save-fab",
      "instagram",
      "",
      "",
    );
    document.body.appendChild(btn);
  }

  function mountInstagramProfileButton() {
    const handle = getInstagramHandleFromPath();
    if (!handle) return;
    const header = document.querySelector("header");
    if (!header) return;
    const existing = document.getElementById("igloo-instagram-save-btn");
    if (existing && existing.dataset.key === handle) return;
    existing?.remove();
    const btn = makeCrossSaveButton(
      "igloo-instagram-save-btn",
      "instagram",
      handle,
      `https://www.instagram.com/${handle}/`,
    );
    const anchor = header.querySelector('button, a[role="button"]') || header;
    anchor.insertAdjacentElement(
      anchor === header ? "beforeend" : "afterend",
      btn,
    );
  }

  function mountInstagramMediaButton() {
    const owner = currentInstagramMediaOwner();
    if (!owner || !owner.handle || !owner.link) return;
    const existing = document.getElementById("igloo-instagram-media-save-btn");
    if (existing && existing.dataset.key === owner.handle) return;
    existing?.remove();
    const btn = makeCrossSaveButton(
      "igloo-instagram-media-save-btn",
      "instagram",
      owner.handle,
      `https://www.instagram.com/${owner.handle}/`,
    );
    btn.style.marginLeft = "10px";
    owner.link.insertAdjacentElement("afterend", btn);
  }

  function updateInstagramFab() {
    const btn = document.getElementById("igloo-instagram-save-fab");
    if (!btn) return;
    const handle = getVisibleInstagramHandle();
    if (!handle) {
      btn.style.display = "none";
      return;
    }
    btn.dataset.key = handle;
    btn.dataset.url = `https://www.instagram.com/${handle}/`;
    setCrossButtonState(btn, isPlatformSaved("instagram", handle));
    btn.style.display = "inline-flex";
  }

  function mountInstagramButtons() {
    ensureCrossSiteStyles();
    ensureInstagramFab();
    const changed = lastInstagramPath !== location.pathname;
    lastInstagramPath = location.pathname;
    if (changed) {
      document.getElementById("igloo-instagram-save-btn")?.remove();
      document.getElementById("igloo-instagram-media-save-btn")?.remove();
      const fab = document.getElementById("igloo-instagram-save-fab");
      if (fab) {
        fab.dataset.key = "";
        fab.dataset.url = "";
        fab.style.display = "none";
      }
    }
    updateInstagramFab();
  }

  function extractYouTubeChannelFromPage() {
    let channelID = "";
    let name = "";
    let url = "";
    const player =
      pageWindow.ytInitialPlayerResponse || window.ytInitialPlayerResponse;
    if (player && player.videoDetails) {
      channelID = normalizeYouTubeChannelID(
        player.videoDetails.channelId || "",
      );
      name = String(player.videoDetails.author || "").trim();
    }
    if (!channelID) {
      const meta = document.querySelector(
        'meta[itemprop="channelId"][content]',
      );
      channelID = normalizeYouTubeChannelID(
        meta && meta.getAttribute("content"),
      );
    }
    if (!channelID) {
      const m = location.pathname.match(/\/channel\/(UC[A-Za-z0-9_-]{8,})/);
      if (m) channelID = m[1];
    }
    const ownerLink = document.querySelector(
      'ytd-video-owner-renderer a[href^="/@"], ytd-video-owner-renderer a[href^="/channel/"], #owner a[href^="/@"], #owner a[href^="/channel/"], ytd-channel-name a[href^="/@"], ytd-channel-name a[href^="/channel/"]',
    );
    if (ownerLink) {
      url = new URL(ownerLink.getAttribute("href"), location.origin).toString();
      if (!name) name = (ownerLink.textContent || "").trim();
    }
    if (!url && channelID) url = `https://www.youtube.com/channel/${channelID}`;
    const handle = url.match(/youtube\.com\/@([^/?#]+)/i);
    const key = channelID || (handle ? normalizePlatformHandle(handle[1]) : "");
    return { channelID, key: key.toLowerCase(), name, url };
  }

  function findYouTubeSubscribeAnchor() {
    return document.querySelector(
      "ytd-video-owner-renderer #subscribe-button, ytd-watch-metadata #subscribe-button, #owner #subscribe-button, ytd-subscribe-button-renderer",
    );
  }

  function mountYouTubeButton() {
    ensureCrossSiteStyles();
    const info = extractYouTubeChannelFromPage();
    const anchor = findYouTubeSubscribeAnchor();
    if (!anchor || !info.key || !info.url) return;
    const existing = document.getElementById("igloo-youtube-save-btn");
    const stateKey = `${info.key}|${info.url}`;
    if (existing && currentYouTubeKey === stateKey) {
      setCrossButtonState(existing, isPlatformSaved("youtube", info.key));
      return;
    }
    existing?.remove();
    const subscribeURL = info.channelID
      ? `https://www.youtube.com/channel/${info.channelID}`
      : info.url;
    const btn = makeCrossSaveButton(
      "igloo-youtube-save-btn",
      "youtube",
      info.key,
      subscribeURL,
    );
    btn.title = info.name ? `Follow ${info.name} in Igloo` : btn.title;
    anchor.insertAdjacentElement("afterend", btn);
    currentYouTubeKey = stateKey;
  }

  function setImportantStyle(el, property, value) {
    if (el && el.style && typeof el.style.setProperty === "function") {
      el.style.setProperty(property, value, "important");
    }
  }

  function clearImportantStyle(el, property) {
    if (el && el.style && typeof el.style.removeProperty === "function") {
      el.style.removeProperty(property);
    }
  }

  function clearXComposerToolbarTheme() {
    if (!isXSite()) return;
    for (const btn of document.querySelectorAll(
      X_COMPOSER_TOOLBAR_BUTTON_SELECTOR,
    )) {
      clearImportantStyle(btn, "background-color");
      clearImportantStyle(btn, "border-color");
      for (
        let node = btn.parentElement;
        node &&
        node !== document.body &&
        node.getAttribute("data-testid") !== "toolBar";
        node = node.parentElement
      ) {
        clearImportantStyle(node, "filter");
      }
      for (const svgEl of btn.querySelectorAll("svg, svg *")) {
        clearImportantStyle(svgEl, "color");
        clearImportantStyle(svgEl, "fill");
      }
      for (const underline of btn.querySelectorAll("span")) {
        clearImportantStyle(underline, "border-bottom-color");
      }
    }
  }

  function applyXComposerToolbarTheme() {
    if (!isXSite()) return;
    if (!xThemeOverridesActive()) {
      clearXComposerToolbarTheme();
      return;
    }
    for (const btn of document.querySelectorAll(
      X_COMPOSER_TOOLBAR_BUTTON_SELECTOR,
    )) {
      const color =
        btn.disabled || btn.getAttribute("aria-disabled") === "true"
          ? "var(--igloo-x-overlay0)"
          : "var(--igloo-x-accent)";
      setImportantStyle(btn, "background-color", "transparent");
      setImportantStyle(btn, "border-color", "transparent");
      for (
        let node = btn.parentElement;
        node &&
        node !== document.body &&
        node.getAttribute("data-testid") !== "toolBar";
        node = node.parentElement
      ) {
        setImportantStyle(node, "filter", "none");
      }
      for (const svgEl of btn.querySelectorAll("svg, svg *")) {
        setImportantStyle(svgEl, "color", color);
        setImportantStyle(svgEl, "fill", color);
      }
      for (const underline of btn.querySelectorAll("span")) {
        setImportantStyle(underline, "border-bottom-color", color);
      }
    }
  }

  function runCurrentPlatformScan() {
    const platform = currentPlatform();
    syncXOverrideClasses();
    if (platform === "twitter") {
      refreshXThemeStyles();
    }
    if (!buttonOverridesEnabled()) {
      clearButtonOverrides();
      restoreNativeButtons();
      return;
    }
    if (platform === "twitter") {
      mountXFeedSourceButton();
      mountFeedButtons();
      mountDlButtons();
      hideNativeButtons();
    } else if (platform === "tiktok") {
      mountTikTokButtons();
    } else if (platform === "instagram") {
      mountInstagramButtons();
    } else if (platform === "youtube") {
      mountYouTubeButton();
    }
  }

  // ── Init ──────────────────────────────────────────────────────────────────
  let scanTimer = null;
  const observer = new MutationObserver(() => {
    invalidateFocusCache();
    if (scanTimer) clearTimeout(scanTimer);
    scanTimer = setTimeout(runCurrentPlatformScan, BUTTON_SCAN_DEBOUNCE_MS);
  });

  function startIglooSync() {
    forgetLegacyDashboardPassword();
    registerMenu();
    if (isXAuthRoute()) {
      console.log(`[IglooSync] loaded v${SCRIPT_VERSION} (auth route idle)`);
      return;
    }
    runCurrentPlatformScan();
    if (isXSite()) {
      fetchServerHandles();
      fetchXFeedSources();
      if (xDownloadsEnabled()) {
        fetchDlCategories();
        fetchDlLabels();
        loadHandleAliasesFromApi();
      }
    } else if (currentPlatform() === "tiktok") {
      fetchPlatformChannels("tiktok");
    } else if (currentPlatform() === "instagram") {
      fetchPlatformChannels("instagram");
    } else if (currentPlatform() === "youtube") {
      fetchPlatformChannels("youtube");
      window.addEventListener("yt-navigate-finish", () => {
        currentYouTubeKey = "";
        runCurrentPlatformScan();
      });
    }
    const observeRoot = document.documentElement || document.body;
    if (observeRoot) {
      observer.observe(observeRoot, {
        childList: true,
        subtree: true,
      });
    }
    setInterval(runCurrentPlatformScan, 1000);
    console.log(`[IglooSync] loaded v${SCRIPT_VERSION}`);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", startIglooSync, {
      once: true,
    });
  } else {
    startIglooSync();
  }
})();
