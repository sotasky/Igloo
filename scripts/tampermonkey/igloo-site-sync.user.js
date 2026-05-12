// ==UserScript==
// @name         Igloo Site Sync
// @namespace    local.igloo.site.sync
// @version      8.0.18
// @author       screwys
// @description  Follow X, TikTok, Instagram, and YouTube channels in Igloo; includes the full X media workflow.
// @homepageURL  https://github.com/screwys/Igloo
// @supportURL   https://github.com/screwys/Igloo/issues
// @updateURL    https://raw.githubusercontent.com/screwys/Igloo/main/scripts/tampermonkey/igloo-site-sync.user.js
// @downloadURL  https://raw.githubusercontent.com/screwys/Igloo/main/scripts/tampermonkey/igloo-site-sync.user.js
// @match        https://x.com/*
// @match        https://twitter.com/*
// @match        https://www.tiktok.com/*
// @match        https://www.instagram.com/*
// @match        https://www.youtube.com/*
// @grant        GM_xmlhttpRequest
// @grant        GM_getValue
// @grant        GM_setValue
// @grant        GM_registerMenuCommand
// @grant        GM_notification
// @grant        GM_setClipboard
// @grant        GM_download
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
  const SCRIPT_VERSION = "8.0.18";

  const SETTINGS = {
    apiBase: "xsync_api_base",
    authToken: "xsync_auth_token", // access token (24h)
    authRefresh: "xsync_auth_refresh", // refresh token (90d, rotated on use)
    authUser: "xsync_auth_user",
    authPass: "xsync_auth_pass",
    syncToDashboard: "xsync_sync_to_dashboard",
    saveLocal: "xsync_save_local",
    localList: "xsync_local_list",
    handleAliases: "xdl_handle_aliases",
    xDownloads: "igloo_sync_x_downloads",
    xKeyboardShortcuts: "igloo_sync_x_keyboard_shortcuts",
    xCleanup: "igloo_sync_x_cleanup",
  };

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
  const xDownloadsEnabled = () => !!GM_getValue(SETTINGS.xDownloads, true);
  const xKeyboardShortcutsEnabled = () =>
    !!GM_getValue(SETTINGS.xKeyboardShortcuts, true);
  const xCleanupEnabled = () => !!GM_getValue(SETTINGS.xCleanup, true);
  const pageWindow =
    typeof unsafeWindow !== "undefined" ? unsafeWindow : window;
  const X_MEDIA_CACHE_LIMIT = 500;
  const X_MEDIA_GRAPHQL_PATH_RE =
    /^(?:\/i\/api)?\/graphql\/[^/]+\/(TweetDetail|TweetResultByRestId|UserTweets|UserMedia|HomeTimeline|HomeLatestTimeline|UserTweetsAndReplies|UserHighlightsTweets|UserArticlesTweets|Bookmarks|Likes|CommunitiesExploreTimeline|ListLatestTweetsTimeline|SearchTimeline)$/;
  const cachedXImageMediaByTweetId = new Map();
  const cachedXVideoUrlsByTweetId = new Map();

  function currentPlatform() {
    const host = location.hostname.toLowerCase();
    if (host === "x.com" || host === "twitter.com") return "twitter";
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

  let serverHandleSet = null;
  let serverHandleToChannelId = {};

  const notify = (text) => {
    try {
      GM_notification({ title: "Igloo Sync", text, timeout: 2500 });
    } catch (_) {}
    console.log("[IglooSync]", text);
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

  function rememberCachedVideoUrls(tweetId, urls) {
    const id = String(tweetId || "");
    const nextUrls = Array.from(
      new Set((urls || []).filter(isVideoTwimgMp4Url)),
    );
    if (!id || !nextUrls.length) return false;

    const existing = cachedVideoUrlsForTweet(id);
    const merged = Array.from(new Set([...nextUrls, ...existing]));
    const changed =
      merged.length !== existing.length ||
      merged.some((url, i) => url !== existing[i]);
    cachedXVideoUrlsByTweetId.delete(id);
    cachedXVideoUrlsByTweetId.set(id, merged);
    trimCacheMap(cachedXVideoUrlsByTweetId);
    return changed;
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
      urls.push(...bestMp4VariantUrls(item));
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
    if (typeof body === "string") {
      if (!body.trim()) return 0;
      try {
        json = JSON.parse(body);
      } catch (_) {
        return 0;
      }
    }

    let cached = 0;
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

  function shouldCaptureXApiMediaUrl(url) {
    try {
      const parsed = new URL(url, location.origin);
      return X_MEDIA_GRAPHQL_PATH_RE.test(parsed.pathname);
    } catch (_) {
      return false;
    }
  }

  function fetchInputUrl(input) {
    if (typeof input === "string") return input;
    if (input instanceof URL) return input.href;
    if (input && typeof input.href === "string") return input.href;
    if (input && typeof input.url === "string") return input.url;
    return "";
  }

  function captureXhrMediaResponse(xhr) {
    try {
      if (xhr.status !== 200) return;
      const body =
        typeof xhr.responseText === "string"
          ? xhr.responseText
          : xhr.response;
      cacheTweetMediaFromApiResponse(body);
    } catch (_) {}
  }

  function captureFetchMediaResponse(resp) {
    try {
      const ok =
        typeof resp.ok === "boolean"
          ? resp.ok
          : resp.status >= 200 && resp.status < 300;
      if (!ok || typeof resp.clone !== "function") return;
      resp
        .clone()
        .text()
        .then(cacheTweetMediaFromApiResponse)
        .catch(() => {});
    } catch (_) {}
  }

  function installXApiMediaCapture() {
    if (
      !isXSite() ||
      isXAuthRoute() ||
      !pageWindow ||
      pageWindow.__iglooXMediaCaptureInstalled
    )
      return;
    try {
      pageWindow.__iglooXMediaCaptureInstalled = true;
    } catch (_) {}

    try {
      const XHR = pageWindow.XMLHttpRequest;
      if (XHR?.prototype?.open && !XHR.prototype.open.__iglooPatched) {
        const nativeOpen = XHR.prototype.open;
        const patchedOpen = function (...args) {
          const url = args[1];
          try {
            if (
              shouldCaptureXApiMediaUrl(url) &&
              typeof this.addEventListener === "function"
            ) {
              this.addEventListener("load", () => captureXhrMediaResponse(this));
            }
          } catch (_) {}
          return nativeOpen.apply(this, args);
        };
        patchedOpen.__iglooPatched = true;
        XHR.prototype.open = patchedOpen;
      }
    } catch (err) {
      console.warn("[XDL] could not install XHR media capture:", err);
    }

    try {
      const nativeFetch = pageWindow.fetch;
      if (typeof nativeFetch === "function" && !nativeFetch.__iglooPatched) {
        const patchedFetch = function (...args) {
          const shouldCapture = shouldCaptureXApiMediaUrl(fetchInputUrl(args[0]));
          const promise = nativeFetch.apply(this, args);
          if (!shouldCapture || !promise || typeof promise.then !== "function")
            return promise;
          return promise.then((resp) => {
            captureFetchMediaResponse(resp);
            return resp;
          });
        };
        patchedFetch.__iglooPatched = true;
        pageWindow.fetch = patchedFetch;
      }
    } catch (err) {
      console.warn("[XDL] could not install fetch media capture:", err);
    }
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
    // Preferred: rotate via /api/auth/refresh (no password needed).
    const refresh = getRefresh();
    if (refresh) {
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
      // 401 on refresh = expired/replayed/revoked → drop the stale refresh token
      // and fall through to the username/password fallback below.
      if (r.status === 401) GM_setValue(SETTINGS.authRefresh, "");
    }
    // Fallback: full login with stored username/password (covers first-use + 90d expiry).
    const user = GM_getValue(SETTINGS.authUser, "");
    const pass = GM_getValue(SETTINGS.authPass, "");
    if (!user || !pass) return false;
    const resp = await _rawApiRequest(
      "POST",
      "/api/auth/login",
      { username: user, password: pass },
      false,
    );
    if (resp.ok && storeAuthTokens(resp.json)) {
      console.log("[XSync] token refreshed via login");
      return true;
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
      notify("Token expired — use Tampermonkey menu → Login Dashboard");
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
      const h = String(value || "").trim().replace(/^@+/, "");
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
    const statusLink =
      node.closest && node.closest('a[href*="/status/"]');
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
        const item = {
          kind: "video",
          tweetId: owner.tweetId,
          tweetUrl: owner.url,
          ext: ".mp4",
          domOrder: items.length,
        };
        const cachedUrl = cachedVideoUrlsForTweet(owner.tweetId)[0];
        if (cachedUrl) item.url = cachedUrl;
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
    .x-sync-btn {
      border: 1px solid rgb(83, 100, 113); background: transparent;
      color: #cdd6f4; border-radius: 9999px; font-size: 12px;
      line-height: 16px; font-weight: 700; padding: 4px 10px;
      margin-right: 8px; cursor: pointer; transition: all 0.15s;
    }
    .x-sync-btn:hover { background: rgba(205,214,244,0.1); }
    .x-sync-btn[data-saved="1"] { border-color: rgba(83, 100, 113, 0.5); opacity: 0.55; color: #bac2de; }
    .x-sync-btn[data-saved="1"]:hover { opacity: 0.85; }
    .x-sync-btn:disabled { opacity: 0.65; cursor: wait; }
    .x-source-save-btn {
      position: fixed; right: 24px; bottom: 24px; z-index: 2147483647;
      border: 1px solid rgb(83, 100, 113); background: #15202b;
      color: #cdd6f4; border-radius: 9999px; font-size: 13px;
      line-height: 16px; font-weight: 800; padding: 9px 15px;
      cursor: pointer; transition: all 0.15s;
      box-shadow: 0 4px 20px rgba(0,0,0,0.6);
      font-family: -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;
    }
    .x-source-save-btn:hover { background: rgba(21,32,43,0.92); border-color: rgba(205,214,244,0.6); }
    .x-source-save-btn[data-saved="1"] { opacity: 0.72; color: #bac2de; }
    .x-source-save-btn:disabled { opacity: 0.65; cursor: wait; }

    /* Hide native bookmark button */
    body.igloo-x-cleanup [data-testid="bookmark"],
    body.igloo-x-cleanup [data-testid="removeBookmark"] { display: none !important; }

    /* Override ALL native action button default color → #f38ba8 */
    body.igloo-x-cleanup [data-testid="reply"] div,
    body.igloo-x-cleanup [data-testid="retweet"] div,
    body.igloo-x-cleanup [data-testid="like"] div,
    body.igloo-x-cleanup [aria-label="Share post"] div,
    body.igloo-x-cleanup [aria-label="Share"] div,
    body.igloo-x-cleanup [data-testid="reply"] svg,
    body.igloo-x-cleanup [data-testid="retweet"] svg,
    body.igloo-x-cleanup [data-testid="like"] svg,
    body.igloo-x-cleanup [aria-label="Share post"] svg,
    body.igloo-x-cleanup [aria-label="Share"] svg { color: #f38ba8 !important; fill: #f38ba8 !important; }

    /* Override native like active (pink → #f38ba8) */
    body.igloo-x-cleanup [data-testid="unlike"] div,
    body.igloo-x-cleanup [data-testid="unlike"] svg { color: #f38ba8 !important; }

    /* Override native retweet active (green → Catpucchin Mocha Red */
    body.igloo-x-cleanup [data-testid="unretweet"] div,
    body.igloo-x-cleanup [data-testid="unretweet"] svg { color: #f38ba8 !important; fill: #f38ba8 !important; }

    /* Round hover highlight on native buttons — target all inner divs */
    body.igloo-x-cleanup [data-testid="reply"] div,
    body.igloo-x-cleanup [data-testid="retweet"] div,
    body.igloo-x-cleanup [data-testid="like"] div,
    body.igloo-x-cleanup [data-testid="unlike"] div,
    body.igloo-x-cleanup [data-testid="unretweet"] div,
    body.igloo-x-cleanup [aria-label="Share post"] div,
    body.igloo-x-cleanup [aria-label="Share"] div { border-radius: 9999px !important; }

    /* Override native hover highlight background color */
    body.igloo-x-cleanup [data-testid="reply"]:hover .r-1niwhzg,
    body.igloo-x-cleanup [data-testid="retweet"]:hover .r-1niwhzg,
    body.igloo-x-cleanup [data-testid="like"]:hover .r-1niwhzg,
    body.igloo-x-cleanup [data-testid="unlike"]:hover .r-1niwhzg,
    body.igloo-x-cleanup [data-testid="unretweet"]:hover .r-1niwhzg,
    body.igloo-x-cleanup [aria-label="Share post"]:hover .r-1niwhzg,
    body.igloo-x-cleanup [aria-label="Share"]:hover .r-1niwhzg { background-color: rgba(243,139,168,0.1) !important; }

    /* Composer toolbar buttons */
    body.igloo-x-cleanup button[role="button"][aria-label="Add photos or video"],
    body.igloo-x-cleanup button[role="button"][data-testid="gifSearchButton"],
    body.igloo-x-cleanup button[role="button"][data-testid="grokImgGen"],
    body.igloo-x-cleanup button[role="button"][data-testid="createPollButton"],
    body.igloo-x-cleanup button[role="button"][aria-label="Add emoji"],
    body.igloo-x-cleanup button[role="button"][data-testid="scheduleOption"],
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"],
    body.igloo-x-cleanup button[role="button"][data-testid="contentDisclosureButton"] {
      border-color: transparent !important;
      background-color: transparent !important;
      border-radius: 9999px !important;
    }

    body.igloo-x-cleanup button[role="button"][aria-label="Add photos or video"] div,
    body.igloo-x-cleanup button[role="button"][data-testid="gifSearchButton"] div,
    body.igloo-x-cleanup button[role="button"][data-testid="grokImgGen"] div,
    body.igloo-x-cleanup button[role="button"][data-testid="createPollButton"] div,
    body.igloo-x-cleanup button[role="button"][aria-label="Add emoji"] div,
    body.igloo-x-cleanup button[role="button"][data-testid="scheduleOption"] div,
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"]:not(:disabled) div,
    body.igloo-x-cleanup button[role="button"][data-testid="contentDisclosureButton"] div,
    body.igloo-x-cleanup button[role="button"][aria-label="Add photos or video"] svg,
    body.igloo-x-cleanup button[role="button"][data-testid="gifSearchButton"] svg,
    body.igloo-x-cleanup button[role="button"][data-testid="grokImgGen"] svg,
    body.igloo-x-cleanup button[role="button"][data-testid="createPollButton"] svg,
    body.igloo-x-cleanup button[role="button"][aria-label="Add emoji"] svg,
    body.igloo-x-cleanup button[role="button"][data-testid="scheduleOption"] svg,
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"]:not(:disabled) svg,
    body.igloo-x-cleanup button[role="button"][data-testid="contentDisclosureButton"] svg,
    body.igloo-x-cleanup button[role="button"][aria-label="Add photos or video"] svg *,
    body.igloo-x-cleanup button[role="button"][data-testid="gifSearchButton"] svg *,
    body.igloo-x-cleanup button[role="button"][data-testid="grokImgGen"] svg *,
    body.igloo-x-cleanup button[role="button"][data-testid="createPollButton"] svg *,
    body.igloo-x-cleanup button[role="button"][aria-label="Add emoji"] svg *,
    body.igloo-x-cleanup button[role="button"][data-testid="scheduleOption"] svg *,
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"]:not(:disabled) svg *,
    body.igloo-x-cleanup button[role="button"][data-testid="contentDisclosureButton"] svg * {
      color: #f38ba8 !important;
      fill: #f38ba8 !important;
    }

    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"]:disabled div,
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"]:disabled svg,
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"]:disabled svg * {
      color: #6c7086 !important;
      fill: #6c7086 !important;
    }

    body.igloo-x-cleanup button[role="button"][aria-label="Add photos or video"] span,
    body.igloo-x-cleanup button[role="button"][data-testid="gifSearchButton"] span,
    body.igloo-x-cleanup button[role="button"][data-testid="grokImgGen"] span,
    body.igloo-x-cleanup button[role="button"][data-testid="createPollButton"] span,
    body.igloo-x-cleanup button[role="button"][aria-label="Add emoji"] span,
    body.igloo-x-cleanup button[role="button"][data-testid="scheduleOption"] span,
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"] span,
    body.igloo-x-cleanup button[role="button"][data-testid="contentDisclosureButton"] span {
      border-bottom-color: #f38ba8 !important;
    }

    body.igloo-x-cleanup button[role="button"][aria-label="Add photos or video"]:hover,
    body.igloo-x-cleanup button[role="button"][data-testid="gifSearchButton"]:hover,
    body.igloo-x-cleanup button[role="button"][data-testid="grokImgGen"]:hover,
    body.igloo-x-cleanup button[role="button"][data-testid="createPollButton"]:hover,
    body.igloo-x-cleanup button[role="button"][aria-label="Add emoji"]:hover,
    body.igloo-x-cleanup button[role="button"][data-testid="scheduleOption"]:hover,
    body.igloo-x-cleanup button[role="button"][data-testid="geoButton"]:hover,
    body.igloo-x-cleanup button[role="button"][data-testid="contentDisclosureButton"]:hover {
      background-color: rgba(243,139,168,0.1) !important;
    }

    /* Even spacing for action bar with 6 buttons */
    body.igloo-x-cleanup [role="group"] { justify-content: space-between !important; }

    /* Our action buttons — match native wrapper structure */
    .x-action-wrap {
      display: flex; align-items: center; justify-content: center;
      flex-shrink: 0;
    }
    .x-action-wrap button {
      display: flex; align-items: center; justify-content: center;
      background: transparent; border: none; color: #f38ba8;
      cursor: pointer; border-radius: 9999px; padding: 8px;
      transition: color 0.15s, background-color 0.15s;
    }
    .x-action-wrap button svg { width: 18.75px; height: 18.75px; }
    .x-action-wrap button:hover { background-color: rgba(243,139,168,0.1); }
    .x-action-wrap button:disabled { opacity: 0.5; cursor: wait; }
    .x-action-wrap button.done { color: #f38ba8; }
    .x-action-wrap button.error { color: #f38ba8; }
    .x-action-wrap button.downloaded { color: #f38ba8; }
    .x-action-wrap button.downloaded svg { display: none; }
    .x-action-wrap button.downloaded::after { content: '\u2713'; font-size: 16px; font-weight: 700; }
    .x-action-wrap button.copied { color: #f38ba8; }
    #x-dl-popover {
    position: absolute; background: #1e1e2e;
    border: 1px solid rgba(243,139,168,0.25); border-radius: 16px;
    padding: 20px 22px; z-index: 999998; min-width: 300px; max-width: 340px;
    box-shadow: 0 4px 24px rgba(0,0,0,0.6);
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
    font-size: 14px; color: #cdd6f4;
    }
    #x-dl-popover .xdl-label-text { display:block;margin-bottom:6px;font-size:13px;color:#bac2de;font-weight:500; }
    #x-dl-popover .xdl-preview { font-size:12px;color:#a6adc8;margin-bottom:14px;min-height:18px; }
    #x-dl-popover button.xdl-confirm {
    width:100%;padding:10px;border-radius:9999px;background:#f38ba8;
    color:#1e1e2e;border:none;font-weight:700;cursor:pointer;font-size:14px;transition:background 0.15s;
    }
    #x-dl-popover button.xdl-confirm:hover { background:#eba0ac; }
    .xdl-cat-pill {
      background:transparent;border:1px solid rgba(243,139,168,0.35);
      color:#bac2de;border-radius:9999px;padding:6px 14px;
      font-size:13px;font-weight:600;cursor:pointer;transition:all 0.12s;
    }
    .xdl-cat-pill:hover,.xdl-cat-pill.selected { background:#f38ba8;border-color:#f38ba8;color:#1e1e2e; }
    .xdl-media-idx-row { display:flex;flex-wrap:wrap;gap:5px;margin:4px 0 12px; }
    .xdl-media-idx-btn { width:28px;height:28px;border-radius:6px;border:1px solid rgba(243,139,168,0.35);background:transparent;color:#bac2de;font-size:12px;font-weight:600;cursor:pointer;padding:0;line-height:28px;text-align:center;transition:all 0.12s; }
    .xdl-media-idx-btn:hover { background:rgba(243,139,168,0.18);border-color:#f38ba8; }
    .xdl-media-idx-btn.selected { background:#f38ba8;border-color:#f38ba8;color:#1e1e2e; }

    /* === Native Follow Button Override (HIGH SPECIFICITY) === */
    /* Force transparent background and native grey border */
    body.igloo-x-cleanup #react-root button[role="button"][data-testid$="-follow"] {
      background: transparent !important;
      background-color: transparent !important;
      border: 1px solid rgb(83, 100, 113) !important;
      transition: all 0.15s !important;
    }

    /* Default text color */
    body.igloo-x-cleanup #react-root button[role="button"][data-testid$="-follow"] * {
      color: #cdd6f4 !important;
    }

    /* Hover state (subtle grey/white highlight instead of pink) */
    body.igloo-x-cleanup #react-root button[role="button"][data-testid$="-follow"]:hover {
      background: rgba(205,214,244,0.1) !important;
      background-color: rgba(205,214,244,0.1) !important;
    }

    /* Unfollow / Following state */
    body.igloo-x-cleanup #react-root button[role="button"][data-testid$="-unfollow"] {
      background: transparent !important;
      background-color: transparent !important;
      border: 1px solid rgb(83, 100, 113) !important;
    }

    body.igloo-x-cleanup #react-root button[role="button"][data-testid$="-unfollow"] * {
      color: #bac2de !important;
    }

    body.igloo-x-cleanup #react-root button[role="button"][data-testid$="-unfollow"]:hover {
      background: rgba(205,214,244,0.1) !important;
      background-color: rgba(205,214,244,0.1) !important;
      border-color: rgba(205,214,244,0.3) !important;
    }

    body.igloo-x-cleanup #react-root button[role="button"][data-testid$="-unfollow"]:hover * {
      color: #cdd6f4 !important;
    }
    `;
    document.head.appendChild(style);
  }

  function syncXCleanupClass() {
    if (!document.body) return;
    document.body.classList.toggle(
      "igloo-x-cleanup",
      isXSite() && xCleanupEnabled(),
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
      "font-size:15px;font-weight:700;color:#cdd6f4;margin-bottom:14px;";
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
          "background:#313244;border:1px solid #f38ba8;color:#cdd6f4;border-radius:9999px;padding:6px 14px;font-size:13px;font-weight:600;outline:none;width:" +
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
      "width:100%;box-sizing:border-box;background:#313244;border:1px solid #45475a;color:#cdd6f4;border-radius:8px;padding:10px 12px;font-size:14px;margin-bottom:12px;outline:none;";
    labelInput.addEventListener("focus", () => {
      labelInput.style.borderColor = "#f38ba8";
    });
    labelInput.addEventListener("blur", () => {
      labelInput.style.borderColor = "#45475a";
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
          (idx) =>
            Number.isInteger(idx) && idx >= 0 && idx < mediaCount,
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

  // ── Download: direct browser staging ───────────────────────────────────────
  function downloadImageToStaging(media, stagingName) {
    return new Promise((resolve) => {
      GM_download({
        url: media.url,
        name: stagingName,
        headers: { Referer: "https://x.com/" },
        onload() {
          resolve({ ok: true });
        },
        onerror(err) {
          console.warn("[XDL] GM_download failed:", stagingName, err);
          resolve({ ok: false, error: err });
        },
        ontimeout() {
          resolve({ ok: false, error: "timeout" });
        },
      });
    });
  }

  function directVideoDownloadCandidates(media) {
    if (!media || typeof media !== "object") return [];
    return Array.from(
      new Set([
        ...(isVideoTwimgMp4Url(media.url) ? [media.url] : []),
        ...cachedVideoUrlsForTweet(media.tweetId),
      ]),
    );
  }

  function parseResponseHeaders(rawHeaders) {
    const headers = {};
    String(rawHeaders || "")
      .split(/\r?\n/)
      .forEach((line) => {
        const idx = line.indexOf(":");
        if (idx <= 0) return;
        headers[line.slice(0, idx).trim().toLowerCase()] = line
          .slice(idx + 1)
          .trim();
      });
    return headers;
  }

  function probeDirectMediaUrl(url) {
    return new Promise((resolve) => {
      GM_xmlhttpRequest({
        method: "HEAD",
        url,
        timeout: 15000,
        onload(resp) {
          const headers = parseResponseHeaders(resp.responseHeaders || "");
          const contentType = String(headers["content-type"] || "")
            .toLowerCase();
          const finalUrl = String(resp.finalUrl || url);
          const mediaURL = /(^|\/\/)video\.twimg\.com\//i.test(finalUrl);
          const mp4URL = /\.mp4(?:$|[?#])/i.test(finalUrl);
          const ok =
            resp.status >= 200 &&
            resp.status < 400 &&
            !contentType.includes("text/html") &&
            (contentType.startsWith("video/") ||
              mediaURL ||
              ((contentType === "application/octet-stream" || !contentType) &&
                mp4URL));
          resolve({ ok, status: resp.status, contentType, finalUrl });
        },
        onerror() {
          resolve({ ok: false, error: "network_error" });
        },
        ontimeout() {
          resolve({ ok: false, error: "timeout" });
        },
      });
    });
  }

  function downloadVideoToStaging(media, stagingName) {
    const candidates = directVideoDownloadCandidates(media);
    return new Promise((resolve) => {
      let index = 0;
      async function tryNext() {
        const url = candidates[index++];
        if (!url) {
          resolve({ ok: false, error: "no_cached_video_url" });
          return;
        }
        const probe = await probeDirectMediaUrl(url);
        if (!probe.ok) {
          console.warn("[XDL] direct video probe rejected:", url, probe);
          tryNext();
          return;
        }
        GM_download({
          url,
          name: stagingName,
          onload() {
            resolve({ ok: true, url });
          },
          onerror(err) {
            console.warn("[XDL] direct video download failed:", url, err);
            tryNext();
          },
          ontimeout() {
            console.warn("[XDL] direct video download timed out:", url);
            tryNext();
          },
        });
      }
      tryNext();
    });
  }

  function moveStagedMedia(handle, label, categoryId, stagedFiles) {
    return apiRequest(
      "POST",
      "/api/tweet-media-move",
      {
        handle,
        label,
        category_id: categoryId,
        staged_files: stagedFiles,
      },
      true,
    );
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

  function makeDownloadRunId() {
    return (
      Date.now().toString(36) +
      "_" +
      Math.random().toString(36).slice(2, 8)
    );
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
      const runId = makeDownloadRunId();
      for (let i = 0; i < mediaItems.length; i++) {
        const media = mediaItems[i];
        const staged = {
          staging_name: "tmp_" + tweetId + "_" + runId + "_" + i + media.ext,
          ext: media.ext,
        };
        if (media.kind === "video") {
          const directResp = await downloadVideoToStaging(
            media,
            staged.staging_name,
          );
          if (!directResp.ok) {
            failed.push(media.tweetId || staged.staging_name);
            continue;
          }
          const moveResp = await moveStagedMedia(handle, label, categoryId, [
            staged,
          ]);
          if (moveResp.ok && moveResp.json && moveResp.json.success) {
            moved.push(...(moveResp.json.moved || []));
          } else {
            failed.push(media.tweetId || staged.staging_name);
          }
          continue;
        }

        const dlResp = await downloadImageToStaging(media, staged.staging_name);
        if (!dlResp.ok) {
          failed.push(staged.staging_name);
          continue;
        }
        const moveResp = await moveStagedMedia(handle, label, categoryId, [
          staged,
        ]);
        if (moveResp.ok && moveResp.json && moveResp.json.success) {
          moved.push(...(moveResp.json.moved || []));
        } else {
          failed.push(staged.staging_name);
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

    if (dlCategories === null) {
      notify("Categories loading, try again");
      return;
    }

    showDlPopover(
      dlBtn,
      handle,
      info.tweetId,
      mediaItems.length || 1,
      async (catId, lbl, effectiveHandle, selectedIndices) => {
        setDlButtonState(dlBtn, "loading");

        function onSuccess() {
          markDownloaded(info.tweetId);
          setDlButtonState(dlBtn, "done");
          dlBtn.classList.add("downloaded");
        }

        const selectedMedia = selectedIndices
          ? mediaItems.filter((_, i) => selectedIndices.includes(i))
          : mediaItems;
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
              showToast("Download failed: move error");
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
      "position:fixed;bottom:28px;left:50%;transform:translateX(-50%);background:#1e1e2e;color:#cdd6f4;border:1px solid rgba(108,112,134,0.55);border-radius:8px;padding:11px 18px;font-size:14px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;z-index:999999;display:flex;align-items:center;gap:6px;box-shadow:0 4px 20px rgba(0,0,0,0.5);opacity:1;transition:opacity 0.3s ease;white-space:nowrap;";

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
        "text-decoration:underline;cursor:pointer;color:#f38ba8;margin-left:2px;";
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
    if (!xCleanupEnabled()) return;
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
        if (wrap && wrap.style.display !== "none") wrap.style.display = "none";
      }
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
      "Dashboard API base URL",
      getApiBase() || DEFAULT_API_BASE,
    );
    if (next === null) return false;
    saveApiBaseSetting(next);
    if (notifyOnSave) notify("Saved API URL");
    return true;
  }

  function registerMenu() {
    GM_registerMenuCommand("Set API URL", () => {
      promptForApiBase(true);
    });

    GM_registerMenuCommand("Login Dashboard (Store Token)", async () => {
      if (!promptForApiBase(false)) return;
      const username = prompt(
        "Dashboard username",
        GM_getValue(SETTINGS.authUser, ""),
      );
      if (!username) return;
      const password = prompt("Dashboard password");
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
      GM_setValue(SETTINGS.authPass, password);
      notify(`Saved token for ${username}`);
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

    GM_registerMenuCommand("Test Dashboard Connection", async () => {
      const resp = await apiRequest("GET", "/api/stats", null, true);
      notify(
        resp.ok
          ? "Dashboard connection OK"
          : `Connection failed (${resp.status || resp.error || "error"})`,
      );
    });

    GM_registerMenuCommand("Toggle Server Sync", () => {
      const next = !syncToDashboardEnabled();
      GM_setValue(SETTINGS.syncToDashboard, next);
      notify(`Server sync ${next ? "enabled" : "disabled"}`);
    });

    GM_registerMenuCommand("Toggle Local Follow", () => {
      const next = !saveLocalEnabled();
      GM_setValue(SETTINGS.saveLocal, next);
      notify(`Local follow ${next ? "enabled" : "disabled"}`);
    });

    GM_registerMenuCommand("Toggle X Download Buttons", () => {
      const next = !xDownloadsEnabled();
      GM_setValue(SETTINGS.xDownloads, next);
      if (next) {
        fetchDlCategories();
        fetchDlLabels();
        loadHandleAliasesFromApi();
        mountDlButtons();
      } else {
        clearDlButtons();
      }
      notify(`X download buttons ${next ? "enabled" : "disabled"}`);
    });

    GM_registerMenuCommand("Toggle Keyboard Shortcuts", () => {
      const next = !xKeyboardShortcutsEnabled();
      GM_setValue(SETTINGS.xKeyboardShortcuts, next);
      notify(`Keyboard shortcuts ${next ? "enabled" : "disabled"}`);
    });

    GM_registerMenuCommand("Toggle X Cleanup/Theme Overrides", () => {
      const next = !xCleanupEnabled();
      GM_setValue(SETTINGS.xCleanup, next);
      syncXCleanupClass();
      if (next) hideNativeButtons();
      notify(`X cleanup/theme overrides ${next ? "enabled" : "disabled"}`);
    });

    GM_registerMenuCommand("Copy Local Subs JSON", () => {
      const list = GM_getValue(SETTINGS.localList, []);
      GM_setClipboard(JSON.stringify(list, null, 2), "text");
      notify(`Copied ${list.length} local subs`);
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
    btn.title = saved
      ? "Remove from Igloo subscriptions"
      : "Follow in Igloo";
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
    btn.title = info.name
      ? `Follow ${info.name} in Igloo`
      : btn.title;
    anchor.insertAdjacentElement("afterend", btn);
    currentYouTubeKey = stateKey;
  }

  function setImportantStyle(el, property, value) {
    if (el && el.style && typeof el.style.setProperty === "function") {
      el.style.setProperty(property, value, "important");
    }
  }

  function applyXComposerToolbarTheme() {
    if (!isXSite() || !xCleanupEnabled()) return;
    for (const btn of document.querySelectorAll(
      X_COMPOSER_TOOLBAR_BUTTON_SELECTOR,
    )) {
      const color =
        btn.disabled || btn.getAttribute("aria-disabled") === "true"
          ? "#6c7086"
          : "#f38ba8";
      setImportantStyle(btn, "background-color", "transparent");
      setImportantStyle(btn, "border-color", "transparent");
      for (
        let node = btn.parentElement;
        node && node !== document.body && node.getAttribute("data-testid") !== "toolBar";
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
    if (platform === "twitter") {
      syncXCleanupClass();
      applyXComposerToolbarTheme();
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

  installXApiMediaCapture();
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", startIglooSync, {
      once: true,
    });
  } else {
    startIglooSync();
  }
})();
