import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import vm from "node:vm";

const script = fs.readFileSync(
  new URL("./igloo-site-sync.user.js", import.meta.url),
  "utf8",
);

function fakeElement() {
  const classes = new Set();
  const attrs = new Map();
  const element = {
    children: [],
    parentElement: null,
    textContent: "",
    id: "",
    style: {
      setProperty(property, value) {
        this[property] = value;
      },
      removeProperty(property) {
        delete this[property];
      },
    },
    dataset: {},
    classList: {
      add(...names) {
        for (const name of names) classes.add(name);
      },
      remove(...names) {
        for (const name of names) classes.delete(name);
      },
      toggle(name, force) {
        if (force === true) {
          classes.add(name);
          return true;
        }
        if (force === false) {
          classes.delete(name);
          return false;
        }
        if (classes.has(name)) {
          classes.delete(name);
          return false;
        }
        classes.add(name);
        return true;
      },
      contains(name) {
        return classes.has(name);
      },
    },
    appendChild(child) {
      child.parentElement = element;
      element.children.push(child);
      return child;
    },
    insertAdjacentElement() {},
    remove() {
      if (element.parentElement) {
        element.parentElement.children =
          element.parentElement.children.filter((child) => child !== element);
      }
    },
    setAttribute(name, value) {
      attrs.set(name, String(value));
    },
    getAttribute(name) {
      return attrs.get(name) || "";
    },
    addEventListener() {},
    querySelector() {
      return null;
    },
    querySelectorAll() {
      return [];
    },
    closest() {
      return null;
    },
  };
  return element;
}

class TestElement {
  constructor(tagName, attrs = {}, children = []) {
    this.tagName = tagName.toUpperCase();
    this.attrs = { ...attrs };
    this.children = [];
    this.parentElement = null;
    this.dataset = {};
    this.style = {};
    this.textContent = attrs.textContent || "";
    this.src = attrs.src || "";
    for (const child of children) this.appendChild(child);
  }

  appendChild(child) {
    child.parentElement = this;
    this.children.push(child);
    return child;
  }

  getAttribute(name) {
    if (name === "src" && this.src) return this.src;
    return this.attrs[name] || "";
  }

  contains(target) {
    if (this === target) return true;
    return this.children.some((child) => child.contains(target));
  }

  closest(selector) {
    for (let node = this; node; node = node.parentElement) {
      if (matchesSelector(node, selector)) return node;
    }
    return null;
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }

  querySelectorAll(selector) {
    const selectors = selector.split(",").map((part) => part.trim());
    const results = [];
    const visit = (node) => {
      for (const child of node.children) {
        if (selectors.some((part) => matchesSelector(child, part))) {
          results.push(child);
        }
        visit(child);
      }
    };
    visit(this);
    return results;
  }
}

function el(tagName, attrs = {}, children = []) {
  return new TestElement(tagName, attrs, children);
}

function matchesSelector(node, selector) {
  if (selector === "time") return node.tagName === "TIME";
  if (selector === "video") return node.tagName === "VIDEO";
  if (selector === '[data-testid="videoPlayer"]') {
    return node.getAttribute("data-testid") === "videoPlayer";
  }
  if (selector === 'a[href*="/status/"]') {
    return (
      node.tagName === "A" &&
      node.getAttribute("href").includes("/status/")
    );
  }
  if (selector === 'a[href*="/status/"] time') {
    return node.tagName === "TIME" && !!node.closest('a[href*="/status/"]');
  }
  if (selector === 'img[src*="pbs.twimg.com/media"]') {
    return (
      node.tagName === "IMG" &&
      (node.src || node.getAttribute("src")).includes("pbs.twimg.com/media")
    );
  }
  if (selector === '[role="link"]') {
    return node.getAttribute("role") === "link";
  }
  return false;
}

function buildHarness({
  prompts = [],
  followHandles = [],
  localList = [],
  failDownloads = [],
  computedStyles = {},
  pathname = "/home",
  unsafeWindow = {},
  userAgent = "Mozilla/5.0 Chrome/120.0.0.0",
  initialValues = {},
  onRequest = null,
  responseOverrides = {},
  twitterChannels = [
    {
      channel_id: "twitter_alice",
      url: "",
    },
  ],
} = {}) {
  const values = new Map([
    ["igloo_sync_x_downloads", false],
    ["igloo_sync_x_keyboard_shortcuts", false],
    ["xsync_api_base", "http://127.0.0.1:5001"],
    ["xsync_local_list", localList],
    ...Object.entries(initialValues),
  ]);
  const requests = [];
  const requestCalls = [];
  const downloadCalls = [];
  const toasts = [];
  const menu = new Map();
  const promptCalls = [];
  const followButtons = followHandles.map((handle) => {
    const btn = fakeElement();
    btn.dataset.handle = handle;
    return btn;
  });
  const documentElement = fakeElement();
  const body = fakeElement();
  const head = fakeElement();
  const appendBodyChild = body.appendChild;
  body.appendChild = (child) => {
    appendBodyChild(child);
    if (child.id === "x-sync-toast") {
      toasts.push(child.children.map((toastChild) => toastChild.textContent).join(""));
    }
    return child;
  };
  const computedStyleElements = new Map();
  for (const [selector, style] of Object.entries(computedStyles)) {
    const target =
      selector === "body"
        ? body
        : selector === ":root"
          ? documentElement
          : fakeElement();
    target.__computedStyle = style;
    computedStyleElements.set(selector, target);
  }
  function computedStyleFor(element) {
    const style = element?.__computedStyle || {};
    return {
      ...style,
      getPropertyValue(property) {
        return style[property] || "";
      },
    };
  }

  const context = {
    console: {
      log() {},
      warn() {},
      error() {},
    },
    location: {
      hostname: "x.com",
      origin: "https://x.com",
      pathname,
    },
    navigator: {
      userAgent,
    },
    window: {
      addEventListener() {},
      getComputedStyle: computedStyleFor,
    },
    unsafeWindow,
    document: {
      body,
      head,
      documentElement,
      addEventListener() {},
      getElementById(id) {
        return body.children.find((child) => child.id === id) || null;
      },
      querySelector(selector) {
        if (computedStyleElements.has(selector)) {
          return computedStyleElements.get(selector);
        }
        if (selector === "body") return body;
        return null;
      },
      querySelectorAll(selector) {
        if (selector === ".x-sync-btn[data-handle]") return followButtons;
        return [];
      },
      createElement() {
        return fakeElement();
      },
      createElementNS() {
        return fakeElement();
      },
    },
    MutationObserver: class {
      observe() {}
    },
    GM_getValue(key, fallback) {
      return values.has(key) ? values.get(key) : fallback;
    },
    GM_setValue(key, value) {
      values.set(key, value);
    },
    GM_deleteValue(key) {
      values.delete(key);
    },
    GM_registerMenuCommand(name, callback) {
      menu.set(name, callback);
    },
    GM_setClipboard() {},
    GM_download(options) {
      downloadCalls.push({
        url: options.url,
        name: options.name,
        headers: options.headers,
      });
      queueMicrotask(() => {
        if (failDownloads.some((pattern) => options.url.includes(pattern))) {
          options.onerror?.({ error: "forced failure" });
        } else {
          options.onload?.();
        }
      });
    },
    GM_xmlhttpRequest(options) {
      requests.push(options.url);
      requestCalls.push({
        method: options.method,
        url: options.url,
        headers: options.headers || {},
        data: options.data,
      });
      const response = responseFor(options.url, {
        data: options.data,
        headers: options.headers || {},
        responseOverrides,
        twitterChannels,
      });
      onRequest?.(requestCalls[requestCalls.length - 1], values);
      queueMicrotask(() => {
        options.onload({
          status: response.status,
          responseText: response.text,
          responseHeaders: response.headers || "",
          finalUrl: response.finalUrl || options.url,
        });
      });
    },
    prompt(message, fallback) {
      promptCalls.push([message, fallback]);
      return prompts.length ? prompts.shift() : null;
    },
    setTimeout(callback) {
      queueMicrotask(callback);
      return 1;
    },
    clearTimeout() {},
    setInterval() {
      return 1;
    },
    URL,
    queueMicrotask,
  };
  context.globalThis = context;

  return {
    context: vm.createContext(context),
    requests,
    requestCalls,
    values,
    menu,
    promptCalls,
    followButtons,
    downloadCalls,
    toasts,
  };
}

function responseFor(
  url,
  { data, headers = {}, responseOverrides = {}, twitterChannels } = {},
) {
  const override = responseOverrides[url];
  if (override) {
    return typeof override === "function"
      ? override({ data, headers, twitterChannels })
      : override;
  }
  if (url === "http://127.0.0.1:5001/api/health/live") {
    return {
      status: 400,
      text: "Client sent an HTTP request to an HTTPS server.",
    };
  }
  if (url === "https://localhost:5001/api/health/live") {
    return { status: 200, text: JSON.stringify({ ok: true }) };
  }
  if (url === "https://localhost:5001/api/channels?platform=twitter") {
    return {
      status: 200,
      text: JSON.stringify({
        channels: twitterChannels,
      }),
    };
  }
  if (url === "https://localhost:5001/api/theme.json") {
    return {
      status: 200,
      text: JSON.stringify({
        theme_id: "dracula",
        color_scheme: "dark",
        tokens: {
          theme_id: "dracula",
          color_scheme: "dark",
          dark: true,
          accent: "#50fa7b",
          on_accent: "#11111b",
          rosewater: "#f8f8f2",
          flamingo: "#ffb3d9",
          pink: "#ff79c6",
          mauve: "#bd93f9",
          red: "#ff5555",
          maroon: "#ff6e6e",
          peach: "#ffb86c",
          yellow: "#f1fa8c",
          green: "#50fa7b",
          teal: "#8be9fd",
          sky: "#8be9fd",
          sapphire: "#8be9fd",
          blue: "#6272a4",
          lavender: "#bd93f9",
          text: "#f8f8f2",
          subtext1: "#e6e6e6",
          subtext0: "#cfcfd7",
          overlay2: "#a5adc6",
          overlay1: "#858ba3",
          overlay0: "#6272a4",
          surface2: "#6272a4",
          surface1: "#44475a",
          surface0: "#343746",
          base: "#282a36",
          mantle: "#21222c",
          crust: "#191a21",
        },
      }),
    };
  }
  if (url === "https://localhost:5001/api/subscribe") {
    return {
      status: 201,
      text: JSON.stringify({
        success: true,
        channel_id: "twitter_bob",
      }),
    };
  }
  if (url === "https://localhost:5001/api/unsubscribe/twitter_bob") {
    return { status: 200, text: JSON.stringify({ success: true }) };
  }
  if (url === "https://localhost:5001/api/feed/sources?platform=twitter") {
    return { status: 200, text: JSON.stringify({ sources: [] }) };
  }
  if (url === "https://localhost:5001/api/auth/login") {
    return {
      status: 200,
      text: JSON.stringify({
        access_token: "access-token",
        refresh_token: "refresh-token",
      }),
    };
  }
  if (url === "https://localhost:5001/api/auth/refresh") {
    const body = JSON.parse(data || "{}");
    if (body.refresh_token === "valid-refresh") {
      return {
        status: 200,
        text: JSON.stringify({
          access_token: "rotated-access-token",
          refresh_token: "rotated-refresh-token",
        }),
      };
    }
    return {
      status: 401,
      text: JSON.stringify({
        error_code: "refresh_token_expired",
        error_message: "expired",
      }),
    };
  }
  if (url === "https://localhost:5001/api/stats") {
    return {
      status: 401,
      text: JSON.stringify({
        error_code: "access_token_expired",
      }),
    };
  }
  if (url === "https://localhost:5001/api/tweet-media-dl") {
    if (String(data || "").includes("force-video-fail")) {
      return {
        status: 200,
        text: JSON.stringify({
          success: false,
          error: "yt-dlp failed",
        }),
      };
    }
    if (String(data || "").includes("moved-but-success-false")) {
      return {
        status: 200,
        text: JSON.stringify({
          success: false,
          moved: ["alice label 001.mp4"],
          error: "gallery-dl reported a non-zero exit after writing output",
        }),
      };
    }
    return {
      status: 200,
      text: JSON.stringify({
        success: true,
        moved: ["alice label 001.mp4"],
      }),
    };
  }
  if (url === "https://localhost:5001/api/tweet-media-save") {
    return {
      status: 200,
      text: JSON.stringify({
        success: true,
        moved: ["alice label 001.jpg"],
      }),
    };
  }
  if (url === "https://localhost:5001/api/tweet-media-move") {
    if (String(data || "").includes("force-move-fail")) {
      return {
        status: 200,
        text: JSON.stringify({
          success: false,
          moved: [],
          failed: ["tmp_111_0.mp4"],
        }),
      };
    }
    const ext = String(data || "").includes(".mp4") ? ".mp4" : ".jpg";
    return {
      status: 200,
      text: JSON.stringify({
        success: true,
        moved: ["alice label 001" + ext],
      }),
    };
  }
  if (/^https:\/\/video\.twimg\.com\/.*\.mp4(?:$|[?#])/.test(url)) {
    return {
      status: 200,
      text: "",
      headers: "content-type: video/mp4\r\n",
    };
  }
  return { status: 500, text: JSON.stringify({ error: "unexpected url" }) };
}

async function drainMicrotasks() {
  for (let i = 0; i < 8; i += 1) {
    await new Promise((resolve) => setImmediate(resolve));
  }
}

function tweetApiBodyWithImage() {
  return {
    data: {
      tweetResult: {
        result: {
          __typename: "Tweet",
          rest_id: "333",
          legacy: {
            id_str: "333",
            extended_entities: {
              media: [
                {
                  type: "photo",
                  media_url_https:
                    "https://pbs.twimg.com/media/detail-photo.png",
                },
              ],
            },
          },
        },
      },
    },
  };
}

function tweetApiBodyWithVideo() {
  return {
    data: {
      tweetResult: {
        result: {
          __typename: "Tweet",
          rest_id: "333",
          legacy: {
            id_str: "333",
            extended_entities: {
              media: [
                {
                  type: "video",
                  id_str: "999",
                  video_info: {
                    variants: [
                      {
                        content_type: "video/mp4",
                        bitrate: 256000,
                        url: "https://video.twimg.com/ext_tw_video/999/pu/vid/320x180/low.mp4?tag=12",
                      },
                      {
                        content_type: "video/mp4",
                        bitrate: 832000,
                        url: "https://video.twimg.com/ext_tw_video/999/pu/vid/640x360/high.mp4?tag=12",
                      },
                    ],
                  },
                },
              ],
            },
          },
        },
      },
    },
  };
}

function tweetApiBodyWithQuoteImage() {
  return {
    data: {
      tweetResult: {
        result: {
          __typename: "Tweet",
          rest_id: "111",
          legacy: {
            id_str: "111",
            quoted_status_result: {
              result: {
                __typename: "Tweet",
                rest_id: "222",
                legacy: {
                  id_str: "222",
                  extended_entities: {
                    media: [
                      {
                        type: "photo",
                        media_url_https:
                          "https://pbs.twimg.com/media/quote-api.jpg",
                      },
                    ],
                  },
                },
              },
            },
          },
        },
      },
    },
  };
}

function runScript(harness, { exposeDebug = false } = {}) {
  const source = exposeDebug
    ? script.replace(
        /\}\)\(\);\s*$/,
        `globalThis.__iglooTest = {
  handleUnsave,
  collectTweetMediaItems: typeof collectTweetMediaItems === "function" ? collectTweetMediaItems : undefined,
  downloadMediaItems: typeof downloadMediaItems === "function" ? downloadMediaItems : undefined,
  cacheTweetMediaFromApiResponse: typeof cacheTweetMediaFromApiResponse === "function" ? cacheTweetMediaFromApiResponse : undefined,
  cachedMediaItemsForTweet: typeof cachedMediaItemsForTweet === "function" ? cachedMediaItemsForTweet : undefined,
  shouldShowMediaIndexPicker: typeof shouldShowMediaIndexPicker === "function" ? shouldShowMediaIndexPicker : undefined,
  normalizeSelectedMediaIndices: typeof normalizeSelectedMediaIndices === "function" ? normalizeSelectedMediaIndices : undefined,
};\n})();`,
      )
    : script;
  vm.runInContext(source, harness.context, {
    filename: "igloo-site-sync.user.js",
  });
}

test("uses the HTTPS localhost API when the legacy HTTP default hits a TLS listener", async () => {
  const harness = buildHarness();
  runScript(harness);

  await drainMicrotasks();

  assert.ok(
    harness.requests.includes("https://localhost:5001/api/health/live"),
    `expected HTTPS health probe, got ${harness.requests.join(", ")}`,
  );
  assert.ok(
    harness.requests.includes(
      "https://localhost:5001/api/channels?platform=twitter",
    ),
    `expected channels request over HTTPS, got ${harness.requests.join(", ")}`,
  );
});

test("recognizes followed X accounts from channel_id when the endpoint omits url", async () => {
  const harness = buildHarness({ followHandles: ["alice"] });
  runScript(harness);

  await drainMicrotasks();

  assert.equal(harness.followButtons[0].textContent, "Following");
});

test("menu is limited and login does not persist the password", async () => {
  const harness = buildHarness({
    prompts: ["https://localhost:5001", "admin", "secret"],
    initialValues: {
      xsync_auth_pass: "legacy-secret",
    },
  });
  runScript(harness);

  assert.deepEqual([...harness.menu.keys()], [
    "Log in to server",
    "Test connection",
    "Toggle button overrides",
    "Toggle theme",
  ]);
  assert.equal(harness.menu.has("Set Dashboard Bearer Token"), false);
  assert.equal(harness.values.has("xsync_auth_pass"), false);
  const login = harness.menu.get("Log in to server");
  assert.equal(typeof login, "function");

  await login();
  await drainMicrotasks();

  assert.deepEqual(
    harness.promptCalls.map(([message]) => message),
    ["Server API base URL", "Server username", "Server password"],
  );
  assert.equal(harness.values.get("xsync_api_base"), "https://localhost:5001");
  assert.equal(harness.values.get("xsync_auth_token"), "access-token");
  assert.equal(harness.values.get("xsync_auth_refresh"), "refresh-token");
  assert.equal(harness.values.get("xsync_auth_user"), "admin");
  assert.equal(harness.values.has("xsync_auth_pass"), false);
  assert.ok(
    harness.requests.includes("https://localhost:5001/api/auth/login"),
    `expected login request over configured HTTPS base, got ${harness.requests.join(", ")}`,
  );
});

test("expired refresh token asks for login without password fallback", async () => {
  const harness = buildHarness({
    initialValues: {
      xsync_auth_token: "expired-access",
      xsync_auth_refresh: "expired-refresh",
      xsync_auth_user: "admin",
      xsync_auth_pass: "legacy-secret",
    },
  });
  runScript(harness);

  const testConnection = harness.menu.get("Test connection");
  await testConnection();
  await drainMicrotasks();

  assert.ok(
    harness.requests.includes("https://localhost:5001/api/auth/refresh"),
    `expected refresh request, got ${harness.requests.join(", ")}`,
  );
  assert.equal(
    harness.requests.includes("https://localhost:5001/api/auth/login"),
    false,
  );
  assert.equal(harness.values.get("xsync_auth_token"), "");
  assert.equal(harness.values.get("xsync_auth_refresh"), "");
  assert.equal(harness.values.has("xsync_auth_pass"), false);
  assert.ok(
    harness.toasts.some((message) =>
      message.includes("Log in to server"),
    ),
  );
});

test("failed refresh does not clear tokens replaced by a newer login", async () => {
  const freshAccess = "fresh-access";
  const freshRefresh = "fresh-refresh";
  const harness = buildHarness({
    initialValues: {
      xsync_auth_token: "expired-access",
      xsync_auth_refresh: "expired-refresh",
    },
    onRequest(call, values) {
      if (call.url === "https://localhost:5001/api/auth/refresh") {
        values.set("xsync_auth_token", freshAccess);
        values.set("xsync_auth_refresh", freshRefresh);
      }
    },
    responseOverrides: {
      "https://localhost:5001/api/stats": ({ headers }) => {
        if (headers.Authorization === `Bearer ${freshAccess}`) {
          return { status: 200, text: JSON.stringify({ ok: true }) };
        }
        return {
          status: 401,
          text: JSON.stringify({
            error_code: "access_token_expired",
            error_message: "token expired",
          }),
        };
      },
    },
  });
  runScript(harness);

  const testConnection = harness.menu.get("Test connection");
  await testConnection();
  await drainMicrotasks();

  assert.ok(
    harness.requests.includes("https://localhost:5001/api/auth/refresh"),
    `expected refresh request, got ${harness.requests.join(", ")}`,
  );
  assert.equal(harness.values.get("xsync_auth_token"), freshAccess);
  assert.equal(harness.values.get("xsync_auth_refresh"), freshRefresh);
  assert.ok(
    harness.toasts.some((message) => message.includes("Server connection OK")),
    `expected successful retry toast, got ${harness.toasts.join(", ")}`,
  );
});

test("unauthenticated API response does not claim token expiry", async () => {
  const harness = buildHarness({
    responseOverrides: {
      "https://localhost:5001/api/stats": {
        status: 401,
        text: JSON.stringify({
          error_code: "unauthenticated",
          error_message: "Authentication required",
        }),
      },
    },
  });
  runScript(harness);

  const testConnection = harness.menu.get("Test connection");
  await testConnection();
  await drainMicrotasks();

  assert.equal(
    harness.requests.includes("https://localhost:5001/api/auth/refresh"),
    false,
  );
  assert.equal(
    harness.toasts.some((message) => message.includes("Token expired")),
    false,
  );
  assert.ok(
    harness.toasts.some((message) => message.includes("Not logged in")),
    `expected login guidance, got ${harness.toasts.join(", ")}`,
  );
});

test("stays idle on X auth routes", async () => {
  class FakeXMLHttpRequest {
    open() {}
  }
  const nativeOpen = FakeXMLHttpRequest.prototype.open;
  const nativeFetch = function fetch() {};
  const harness = buildHarness({
    pathname: "/login",
    unsafeWindow: {
      XMLHttpRequest: FakeXMLHttpRequest,
      fetch: nativeFetch,
    },
  });

  runScript(harness);
  await drainMicrotasks();

  assert.equal(harness.requests.length, 0);
  assert.equal(FakeXMLHttpRequest.prototype.open, nativeOpen);
  assert.equal(harness.context.unsafeWindow.fetch, nativeFetch);
  assert.equal(typeof harness.menu.get("Log in to server"), "function");
});

test("does not patch X page globals in Firefox", async () => {
  class FakeXMLHttpRequest {
    open() {}
  }
  const nativeOpen = FakeXMLHttpRequest.prototype.open;
  const nativeFetch = function fetch() {};
  const unsafeWindow = {
    XMLHttpRequest: FakeXMLHttpRequest,
    fetch: nativeFetch,
  };
  const harness = buildHarness({
    unsafeWindow,
    userAgent: "Mozilla/5.0 Firefox/126.0",
  });

  runScript(harness);
  await drainMicrotasks();

  assert.equal(FakeXMLHttpRequest.prototype.open, nativeOpen);
  assert.equal(unsafeWindow.fetch, nativeFetch);
  assert.equal(unsafeWindow.__iglooXMediaCaptureInstalled, undefined);
});

test("uses follow wording for visible subscription labels", () => {
  assert.doesNotMatch(
    script,
    /Save source|Saved source|Toggle Local Save|Local save/,
  );
});

test("does not keep a password-backed auth fallback", () => {
  assert.doesNotMatch(script, /authPass/);
  assert.doesNotMatch(script, /SETTINGS\.[A-Za-z0-9_]*Pass/);
  assert.doesNotMatch(script, /refreshed via login/);
});

test("declares Tampermonkey update metadata", () => {
  assert.match(script, /^\/\/ @author\s+screwys$/m);
  assert.match(script, /^\/\/ @homepageURL\s+https:\/\/github\.com\/screwys\/Igloo$/m);
  assert.match(script, /^\/\/ @supportURL\s+https:\/\/github\.com\/screwys\/Igloo\/issues$/m);
  assert.match(
    script,
    /^\/\/ @updateURL\s+https:\/\/raw\.githubusercontent\.com\/screwys\/Igloo\/main\/scripts\/tampermonkey\/igloo-site-sync\.user\.js$/m,
  );
  assert.match(
    script,
    /^\/\/ @downloadURL\s+https:\/\/raw\.githubusercontent\.com\/screwys\/Igloo\/main\/scripts\/tampermonkey\/igloo-site-sync\.user\.js$/m,
  );
  assert.doesNotMatch(script, /^\/\/ @grant\s+GM_notification$/m);
  assert.doesNotMatch(script, /\bGM_notification\b/);
});

test("themes current X composer toolbar buttons", () => {
  for (const selector of [
    'button[role="button"][aria-label="Add photos or video"]',
    'button[role="button"][data-testid="gifSearchButton"]',
    'button[role="button"][data-testid="grokImgGen"]',
    'button[role="button"][data-testid="createPollButton"]',
    'button[role="button"][aria-label="Add emoji"]',
    'button[role="button"][data-testid="scheduleOption"]',
    'button[role="button"][data-testid="geoButton"]',
    'button[role="button"][data-testid="contentDisclosureButton"]',
  ]) {
    assert.ok(script.includes(selector), `missing selector ${selector}`);
  }
  assert.match(script, /svg \*/);
  assert.match(script, /applyXComposerToolbarTheme/);
  assert.match(script, /setProperty\(property, value, "important"\)/);
  assert.match(script, /setImportantStyle\(node, "filter", "none"\)/);
  assert.match(script, /border-bottom-color:\s*var\(--igloo-x-accent\)\s*!important/);
  assert.match(script, /setImportantStyle\(svgEl, "color", color\)/);
  assert.match(script, /var\(--igloo-x-accent\)/);
  assert.match(
    script,
    /data-testid="unretweet"[\s\S]+color: var\(--igloo-x-accent\) !important; fill: var\(--igloo-x-accent\) !important;/,
  );
  assert.match(
    script,
    /data-testid="unlike"[\s\S]+color: var\(--igloo-x-accent\) !important; fill: var\(--igloo-x-accent\) !important;/,
  );
  assert.match(
    script,
    /\[data-testid\$="-follow"\][\s\S]+color: var\(--igloo-x-on-accent\) !important;/,
  );
});

test("covers current Catppuccin Twitter class selectors", () => {
  const expected = [
    "PageContainer",
    "ResponsiveLayout--Night",
    "app-info",
    "auth",
    "block-btn",
    "button",
    "caret-inner",
    "caret-outer",
    "draftjs-styles_0",
    "dropdown-caret",
    "email-report-btn",
    "follow-button",
    "footer",
    "list-btn",
    "list-explanation",
    "logo",
    "mute-btn",
    "notice",
    "oauth",
    "permissions",
    "public-DraftEditorPlaceholder-root",
    "r-11gmi9o",
    "r-11mg6pl",
    "r-11z020y",
    "r-12d83nn",
    "r-14wv3jr",
    "r-15azkrj",
    "r-15ce4ve",
    "r-19130f6",
    "r-1aihyag",
    "r-1blqq69",
    "r-1bnu78o",
    "r-1bwzh9t",
    "r-1ccsd61",
    "r-1cuuowz",
    "r-1cvl2hr",
    "r-1dgebii",
    "r-1eltapf",
    "r-1fkb3t2",
    "r-1hdo0pc",
    "r-1igl3o0",
    "r-1kqtdi0",
    "r-1krxqcr",
    "r-1kwlb9n",
    "r-1m3jxhj",
    "r-1nao33i",
    "r-1pbtemp",
    "r-1peqgm7",
    "r-1pr99xn",
    "r-1roi411",
    "r-1tbvlxk",
    "r-1uusn97",
    "r-1vtznih",
    "r-1wyyjkm",
    "r-1x669os",
    "r-1xc7w19",
    "r-2sztyj",
    "r-3gvs5h",
    "r-4nw3r4",
    "r-5zmot",
    "r-6wtuen",
    "r-9cip40",
    "r-9l7dzd",
    "r-b5kvu3",
    "r-cqee49",
    "r-drfeu3",
    "r-eff69c",
    "r-eok2q2",
    "r-g2wdr4",
    "r-gu4em3",
    "r-h7o7i8",
    "r-jc7xae",
    "r-jwli3a",
    "r-kemksi",
    "r-l5o3uw",
    "r-l8tqsx",
    "r-loe9s5",
    "r-n94g0g",
    "r-o6sn0f",
    "r-oybae9",
    "r-pjtv4k",
    "r-qazpri",
    "r-qo02w8",
    "r-qqmkd0",
    "r-r18ze4",
    "r-rgqbpe",
    "r-s224ru",
    "r-uuique",
    "r-vhj8yc",
    "r-vkub15",
    "r-xzxzvz",
    "r-yuvema",
    "r-z32n2g",
    "r-z9i421",
    "redirect-btn",
    "selected",
    "submit-btn",
    "unfollow",
    "unfollow-btn",
    "user-menu",
  ];
  for (const className of expected) {
    assert.match(script, new RegExp(`\\.${className}(?![A-Za-z0-9_-])`));
  }
});

test("single X theme toggle enables all theme CSS with Igloo theme colors", async () => {
  const harness = buildHarness({
    initialValues: {
      igloo_sync_x_cleanup: false,
      igloo_sync_x_theme_source: "catppuccin",
      igloo_sync_x_theme_flavor: "macchiato",
      igloo_sync_x_theme_accent: "lavender",
    },
  });
  runScript(harness);

  harness.menu.get("Toggle theme")();
  await drainMicrotasks();

  assert.equal(harness.values.get("igloo_sync_x_cleanup"), true);
  assert.equal(
    harness.context.document.body.classList.contains("igloo-theme-overrides"),
    true,
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-accent"],
    "#50fa7b",
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-base"],
    "#282a36",
  );
});

test("enabled X theme loads Igloo theme colors without sampling X", async () => {
  const harness = buildHarness({
    computedStyles: {
      '[data-testid="primaryColumn"]': {
        "background-color": "rgb(12, 18, 24)",
      },
      body: {
        color: "rgb(220, 230, 240)",
      },
      '[data-testid="tweetButtonInline"]': {
        "background-color": "rgb(10, 200, 180)",
      },
      '[style*="border-color"]': {
        "border-color": "rgb(55, 65, 75)",
      },
      '[style*="color: rgb(113, 118, 123)"]': {
        color: "rgb(130, 140, 150)",
      },
      '[data-testid="SearchBox_Search_Input"]': {
        "background-color": "rgb(18, 24, 30)",
      },
    },
    initialValues: {
      igloo_sync_x_cleanup: true,
      igloo_sync_x_theme_flavor: "macchiato",
      igloo_sync_x_theme_accent: "lavender",
    },
  });
  runScript(harness);
  await drainMicrotasks();

  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-accent"],
    "#50fa7b",
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-base"],
    "#282a36",
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-text"],
    "#f8f8f2",
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-control-accent"],
    "#50fa7b",
  );
  assert.equal(
    harness.context.document.documentElement.style["color-scheme"],
    "dark",
  );
  assert.equal(
    harness.context.document.documentElement.getAttribute(
      "data-igloo-x-theme-source",
    ),
    "igloo",
  );
  const themeRequest = harness.requestCalls.find((call) =>
    call.url.endsWith("/api/theme.json"),
  );
  assert.equal(themeRequest?.headers.Authorization, undefined);
});

test("X theme fetch sends stored auth token when available", async () => {
  const harness = buildHarness({
    initialValues: {
      igloo_sync_x_cleanup: true,
      xsync_auth_token: "theme-access-token",
    },
  });
  runScript(harness);
  await drainMicrotasks();

  const themeRequest = harness.requestCalls.find((call) =>
    call.url.endsWith("/api/theme.json"),
  );
  assert.equal(
    themeRequest?.headers.Authorization,
    "Bearer theme-access-token",
  );
});

test("disabled X theme does not fetch or apply theme overrides", async () => {
  const harness = buildHarness({
    initialValues: {
      igloo_sync_x_cleanup: false,
      igloo_sync_x_theme_flavor: "macchiato",
      igloo_sync_x_theme_accent: "lavender",
    },
  });
  runScript(harness);
  await drainMicrotasks();

  assert.equal(
    harness.requestCalls.some((call) => call.url.endsWith("/api/theme.json")),
    false,
  );
  assert.equal(
    harness.context.document.body.classList.contains("igloo-theme-overrides"),
    false,
  );
  assert.equal(
    harness.context.document.documentElement.getAttribute(
      "data-igloo-x-theme-source",
    ),
    "disabled",
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-base"],
    undefined,
  );
  assert.equal(
    harness.context.document.documentElement.style["color-scheme"],
    "light dark",
  );
});

test("turning X theme off removes cached Igloo theme application", async () => {
  const harness = buildHarness({
    initialValues: {
      igloo_sync_x_cleanup: false,
    },
  });
  runScript(harness);

  harness.menu.get("Toggle theme")();
  await drainMicrotasks();
  assert.equal(
    harness.context.document.body.classList.contains("igloo-theme-overrides"),
    true,
  );
  assert.equal(
    harness.context.document.documentElement.getAttribute(
      "data-igloo-x-theme-source",
    ),
    "igloo",
  );

  harness.menu.get("Toggle theme")();
  await drainMicrotasks();

  assert.equal(
    harness.context.document.body.classList.contains("igloo-theme-overrides"),
    false,
  );
  assert.equal(
    harness.context.document.documentElement.getAttribute(
      "data-igloo-x-theme-source",
    ),
    "disabled",
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-base"],
    undefined,
  );
  assert.equal(
    harness.context.document.documentElement.style["--igloo-x-control-base"],
    undefined,
  );
});

test("custom X controls use control theme variables instead of page theme variables", () => {
  assert.match(
    script,
    /\.x-action-wrap button \{[\s\S]*color: var\(--igloo-x-control-accent\)/,
  );
  assert.match(
    script,
    /#x-dl-popover \{[\s\S]*background: var\(--igloo-x-control-base\)/,
  );
  assert.match(
    script,
    /\[data-testid\$="-follow"\] \* \{[\s\S]*color: var\(--igloo-x-control-text\) !important;/,
  );
  assert.match(
    script,
    /labelInput\.style\.borderColor = "var\(--igloo-x-control-accent\)";/,
  );
  assert.match(script, /body\.igloo-theme-overrides \[data-testid="cellInnerDiv"\]/);
  assert.match(script, /body\.igloo-theme-overrides \[aria-label\^="Timeline:"\]/);
  assert.match(script, /body\.igloo-theme-overrides header\[role="banner"\]/);
  assert.match(script, /body\.igloo-theme-overrides \[data-testid="sidebarColumn"\]/);
});

test("ghost-resubscribed X handles can be unfollowed immediately", async () => {
  const harness = buildHarness({
    localList: [{ handle: "bob", url: "https://x.com/bob" }],
    twitterChannels: [],
  });
  runScript(harness, { exposeDebug: true });

  await drainMicrotasks();

  assert.ok(
    harness.requestCalls.some(
      (call) =>
        call.method === "POST" &&
        call.url === "https://localhost:5001/api/subscribe",
    ),
    `expected ghost re-subscribe, got ${harness.requestCalls
      .map((call) => `${call.method} ${call.url}`)
      .join(", ")}`,
  );

  await harness.context.__iglooTest.handleUnsave("bob", null);
  await drainMicrotasks();

  assert.ok(
    harness.requestCalls.some(
      (call) =>
        call.method === "DELETE" &&
        call.url === "https://localhost:5001/api/unsubscribe/twitter_bob",
    ),
    `expected immediate unfollow DELETE, got ${harness.requestCalls
      .map((call) => `${call.method} ${call.url}`)
      .join(", ")}`,
  );
  assert.ok(
    harness.toasts.includes("Removed @bob"),
    `expected unfollow toast, got ${harness.toasts.join(", ")}`,
  );
});

test("collects parent and quote media in parent-first order", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const article = el("article", {}, [
    el("a", { href: "/parent/status/111" }, [el("time")]),
    el("a", { href: "/parent/status/111/photo/1" }, [
      el("img", {
        src: "https://pbs.twimg.com/media/main-one?format=jpg&name=small",
      }),
    ]),
    el("a", { href: "/parent/status/111/photo/2" }, [
      el("img", {
        src: "https://pbs.twimg.com/media/main-two?format=jpg&name=small",
      }),
    ]),
    el("div", { role: "link" }, [
      el("a", { href: "/quote/status/222" }, [el("time")]),
      el("a", { href: "/quote/status/222/photo/1" }, [
        el("img", {
          src: "https://pbs.twimg.com/media/quote-one?format=jpg&name=small",
        }),
      ]),
      el("a", { href: "/quote/status/222/photo/2" }, [
        el("img", {
          src: "https://pbs.twimg.com/media/quote-two?format=jpg&name=small",
        }),
      ]),
    ]),
  ]);

  const items = JSON.parse(
    JSON.stringify(harness.context.__iglooTest.collectTweetMediaItems(article)),
  );

  assert.deepEqual(
    items.map((item) => [item.kind, item.tweetId, item.url]),
    [
      [
        "image",
        "111",
        "https://pbs.twimg.com/media/main-one?format=jpg&name=orig",
      ],
      [
        "image",
        "111",
        "https://pbs.twimg.com/media/main-two?format=jpg&name=orig",
      ],
      [
        "image",
        "222",
        "https://pbs.twimg.com/media/quote-one?format=jpg&name=orig",
      ],
      [
        "image",
        "222",
        "https://pbs.twimg.com/media/quote-two?format=jpg&name=orig",
      ],
    ],
  );
});

test("uses cached X API image media when the overlay article has no rendered image", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const cached = harness.context.__iglooTest.cacheTweetMediaFromApiResponse(
    tweetApiBodyWithImage(),
  );
  const article = el("article", {}, [
    el("a", { href: "/sample_handle/status/333" }, [el("time")]),
  ]);

  const items = JSON.parse(
    JSON.stringify(harness.context.__iglooTest.collectTweetMediaItems(article)),
  );

  assert.equal(cached, 1);
  assert.deepEqual(items, [
    {
      kind: "image",
      url: "https://pbs.twimg.com/media/detail-photo?format=png&name=orig",
      ext: ".png",
      tweetId: "333",
      tweetUrl: "https://x.com/sample_handle/status/333",
      index: 0,
    },
  ]);
});

test("attaches cached X API video URL to the selected video item", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const cached = harness.context.__iglooTest.cacheTweetMediaFromApiResponse(
    tweetApiBodyWithVideo(),
  );
  const article = el("article", {}, [
    el("a", { href: "/sample_handle/status/333" }, [el("time")]),
    el("div", { "data-testid": "videoPlayer" }, [
      el("video", {
        src: "https://video.twimg.com/ext_tw_video/999/pu/vid/320x180/low.mp4?tag=12",
      }),
    ]),
  ]);

  const items = JSON.parse(
    JSON.stringify(harness.context.__iglooTest.collectTweetMediaItems(article)),
  );

  assert.equal(cached, 1);
  assert.deepEqual(items, [
    {
      kind: "video",
      tweetId: "333",
      tweetUrl: "https://x.com/sample_handle/status/333",
      mediaId: "999",
      url: "https://video.twimg.com/ext_tw_video/999/pu/vid/640x360/high.mp4?tag=12",
      ext: ".mp4",
      index: 0,
    },
  ]);
});

test("uses cached X API quote image media when the quote card image is absent", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  harness.context.__iglooTest.cacheTweetMediaFromApiResponse(
    tweetApiBodyWithQuoteImage(),
  );
  const article = el("article", {}, [
    el("a", { href: "/parent/status/111" }, [el("time")]),
    el("div", { role: "link" }, [
      el("a", { href: "/quote/status/222" }, [el("time")]),
    ]),
  ]);

  const items = JSON.parse(
    JSON.stringify(harness.context.__iglooTest.collectTweetMediaItems(article)),
  );

  assert.deepEqual(items, [
    {
      kind: "image",
      url: "https://pbs.twimg.com/media/quote-api?format=jpg&name=orig",
      ext: ".jpg",
      tweetId: "222",
      tweetUrl: "https://x.com/quote/status/222",
      index: 0,
    },
  ]);
});

test("uses the quote tweet URL for quote-only videos", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const article = el("article", {}, [
    el("a", { href: "/parent/status/111" }, [el("time")]),
    el("div", { role: "link" }, [
      el("a", { href: "/quote/status/222" }, [el("time")]),
      el("div", { "data-testid": "videoPlayer" }, [el("video")]),
    ]),
  ]);

  const items = JSON.parse(
    JSON.stringify(harness.context.__iglooTest.collectTweetMediaItems(article)),
  );

  assert.deepEqual(items, [
    {
      kind: "video",
      tweetId: "222",
      tweetUrl: "https://x.com/quote/status/222",
      mediaId: "",
      ext: ".mp4",
      index: 0,
    },
  ]);
});

test("prefers the quote author permalink over X generic i-status video links", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const article = el("article", {}, [
    el("a", { href: "/parent/status/111" }, [el("time")]),
    el("div", { role: "link" }, [
      el("a", { href: "/quote/status/222" }, [el("time")]),
      el("a", { href: "/i/status/222" }, [
        el("div", { "data-testid": "videoPlayer" }, [el("video")]),
      ]),
    ]),
  ]);

  const items = JSON.parse(
    JSON.stringify(harness.context.__iglooTest.collectTweetMediaItems(article)),
  );

  assert.deepEqual(items, [
    {
      kind: "video",
      tweetId: "222",
      tweetUrl: "https://x.com/quote/status/222",
      mediaId: "",
      ext: ".mp4",
      index: 0,
    },
  ]);
});

test("shows the media picker even for a single media item", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  assert.equal(harness.context.__iglooTest.shouldShowMediaIndexPicker(1), true);
});

test("treats no selected media buttons as the default all-media selection", () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  assert.equal(
    harness.context.__iglooTest.normalizeSelectedMediaIndices([], 4),
    null,
  );
});

test("downloads videos through the server backend", async () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const handle = "sample_handle";
  let result = null;
  harness.context.__iglooTest.downloadMediaItems(
    "111",
    handle,
    [
      {
        kind: "video",
        tweetId: "222",
        tweetUrl: "https://x.com/sample_quote/status/222",
        mediaId: "999",
        url: "https://video.twimg.com/ext_tw_video/999/pu/vid/640x360/high.mp4?tag=12",
        ext: ".mp4",
        index: 0,
      },
    ],
    1,
    "label",
    (resp) => {
      result = JSON.parse(JSON.stringify(resp));
    },
  );

  await drainMicrotasks();

  assert.deepEqual(result?.json?.moved, ["alice label 001.mp4"]);
  assert.deepEqual(harness.downloadCalls, []);
  const call = harness.requestCalls.find(
    (item) => item.url === "https://localhost:5001/api/tweet-media-dl",
  );
  assert.ok(call, "expected server video download request");
  assert.deepEqual(JSON.parse(call.data), {
    tweet_url: "https://x.com/sample_quote/status/222",
    media_url:
      "https://video.twimg.com/ext_tw_video/999/pu/vid/640x360/high.mp4?tag=12",
    media_id: "999",
    media_index: 0,
    handle,
    label: "label",
    category_id: 1,
  });
});

test("reports server video download failures", async () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const handle = "sample_handle";
  let result = null;
  harness.context.__iglooTest.downloadMediaItems(
    "111",
    handle,
    [
      {
        kind: "video",
        tweetId: "222",
        tweetUrl: "https://x.com/sample_quote/status/222",
        ext: ".mp4",
        index: 0,
      },
    ],
    1,
    "force-video-fail",
    (resp) => {
      result = JSON.parse(JSON.stringify(resp));
    },
  );

  await drainMicrotasks();

  assert.equal(result?.json?.success, false);
  assert.deepEqual(result?.json?.moved, []);
  assert.deepEqual(result?.json?.failed, ["tweet 222: yt-dlp failed"]);
});

test("accepts server video downloads when the response contains moved files", async () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  let result = null;
  harness.context.__iglooTest.downloadMediaItems(
    "111",
    "sample_handle",
    [
      {
        kind: "video",
        tweetId: "222",
        tweetUrl: "https://x.com/sample_quote/status/222",
        ext: ".mp4",
        index: 0,
      },
    ],
    1,
    "moved-but-success-false",
    (resp) => {
      result = JSON.parse(JSON.stringify(resp));
    },
  );

  await drainMicrotasks();

  assert.equal(result?.json?.success, true);
  assert.deepEqual(result?.json?.moved, ["alice label 001.mp4"]);
  assert.deepEqual(result?.json?.failed, []);
});

test("saves images through the server backend", async () => {
  const harness = buildHarness();
  runScript(harness, { exposeDebug: true });

  const handle = "sample_handle";
  let result = null;
  harness.context.__iglooTest.downloadMediaItems(
    "111",
    handle,
    [
      {
        kind: "image",
        tweetId: "111",
        tweetUrl: "https://x.com/sample_handle/status/111",
        url: "https://pbs.twimg.com/media/main-one?format=jpg&name=orig",
        ext: ".jpg",
        index: 0,
      },
    ],
    1,
    "label",
    (resp) => {
      result = JSON.parse(JSON.stringify(resp));
    },
  );

  await drainMicrotasks();

  assert.deepEqual(result?.json?.moved, ["alice label 001.jpg"]);
  assert.deepEqual(harness.downloadCalls, []);
  const call = harness.requestCalls.find(
    (item) => item.url === "https://localhost:5001/api/tweet-media-save",
  );
  assert.ok(call, "expected server image save request");
  assert.deepEqual(JSON.parse(call.data), {
    urls: ["https://pbs.twimg.com/media/main-one?format=jpg&name=orig"],
    handle,
    label: "label",
    category_id: 1,
  });
  assert.equal(
    harness.requestCalls.some(
      (item) => item.url === "https://localhost:5001/api/tweet-media-move",
    ),
    false,
  );
});
