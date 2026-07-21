import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  CINEMA_COMPACT_LEFT_SIDEBAR_WIDTH,
  CINEMA_HIDE_LEFT_SIDEBAR_WIDTH,
  CINEMA_MIN_PLAYER_WIDTH,
  PLAYER_MAIN_HORIZONTAL_PADDING,
  PLAYER_SIDEBAR_WIDTH,
  cinemaSidebarDefaultMode,
  initCinemaView,
  shouldAutoEnableCinema,
} from "../../static/js/src/player/cinema.js";

const css = readFileSync(new URL("../../static/style.css", import.meta.url), "utf8");
const playerTemplate = readFileSync(new URL("../../internal/components/player.templ", import.meta.url), "utf8");
const playerIndex = readFileSync(new URL("../../static/js/src/player/index.js", import.meta.url), "utf8");
const siteBase = readFileSync(new URL("../../static/js/site_base.js", import.meta.url), "utf8");
const modalsTemplate = readFileSync(new URL("../../internal/components/modals.templ", import.meta.url), "utf8");

const layoutAtVideoWidth = (videoWidth) => (
  videoWidth + PLAYER_SIDEBAR_WIDTH + PLAYER_MAIN_HORIZONTAL_PADDING
);

test("cinema view recommends itself below a 720px video column", () => {
  assert.equal(CINEMA_MIN_PLAYER_WIDTH, 720);
  assert.equal(shouldAutoEnableCinema(layoutAtVideoWidth(719), false), true);
  assert.equal(shouldAutoEnableCinema(layoutAtVideoWidth(720), false), false);
});

test("cinema leaves the left sidebar full, compacts it, then hides it by available width", () => {
  assert.equal(cinemaSidebarDefaultMode(CINEMA_COMPACT_LEFT_SIDEBAR_WIDTH), null);
  assert.equal(cinemaSidebarDefaultMode(CINEMA_COMPACT_LEFT_SIDEBAR_WIDTH - 1), "compact");
  assert.equal(cinemaSidebarDefaultMode(CINEMA_HIDE_LEFT_SIDEBAR_WIDTH - 1), "hidden");
});

test("cinema keeps a stacked next-in-line rail visible", () => {
  const rootClasses = new Set();
  const buttonClasses = new Set();
  const buttonListeners = new Map();
  const attributes = new Map();
  const root = {
    classList: {
      contains: (name) => rootClasses.has(name),
      toggle(name, enabled) { if (enabled) rootClasses.add(name); else rootClasses.delete(name); },
    },
    getBoundingClientRect() { return { width: CINEMA_HIDE_LEFT_SIDEBAR_WIDTH - 1 }; },
    querySelector() { return { setAttribute(name, value) { attributes.set(name, value); } }; },
  };
  const button = {
    classList: {
      toggle(name, enabled) { if (enabled) buttonClasses.add(name); else buttonClasses.delete(name); },
    },
    addEventListener(name, listener) { buttonListeners.set(name, listener); },
    setAttribute(name, value) { attributes.set(`button:${name}`, value); },
  };
  const mediaQuery = { matches: true, addEventListener() {} };
  globalThis.window = { matchMedia: () => mediaQuery, ResizeObserver: class { constructor() {} observe() {} } };

  initCinemaView({ root, button });
  buttonListeners.get("click")();
  assert.equal(rootClasses.has("cinema-view"), true);
  assert.equal(rootClasses.has("cinema-hides-player-sidebar"), false);
  assert.equal(attributes.get("aria-hidden"), "false");
  assert.equal(buttonClasses.has("active"), false);

  delete globalThis.window;
});

test("manual cinema can hide the left sidebar at a tight desktop width", () => {
  const rootClasses = new Set();
  const buttonListeners = new Map();
  const cinemaEvents = [];
  let layoutWidth = 1200;
  const root = {
    classList: {
      contains: (name) => rootClasses.has(name),
      toggle(name, enabled) { if (enabled) rootClasses.add(name); else rootClasses.delete(name); },
    },
    getBoundingClientRect() { return { width: layoutWidth }; },
    querySelector() { return { setAttribute() {} }; },
    dispatchEvent(event) { cinemaEvents.push(event); },
  };
  const button = {
    classList: { toggle() {} },
    addEventListener(name, listener) { buttonListeners.set(name, listener); },
    setAttribute() {},
  };
  const mediaQuery = { matches: false, addEventListener() {} };
  const originalCustomEvent = globalThis.CustomEvent;
  globalThis.CustomEvent = class {
    constructor(type, init) { this.type = type; this.detail = init.detail; }
  };
  globalThis.window = { matchMedia: () => mediaQuery, ResizeObserver: class { constructor() {} observe() {} } };

  initCinemaView({ root, button });
  layoutWidth = CINEMA_HIDE_LEFT_SIDEBAR_WIDTH - 1;
  buttonListeners.get("click")();
  assert.equal(cinemaEvents.at(-1).detail.defaultSidebarMode, "hidden");
  assert.equal(cinemaEvents.at(-1).detail.forceSidebarMode, true);

  if (originalCustomEvent === undefined) delete globalThis.CustomEvent;
  else globalThis.CustomEvent = originalCustomEvent;
  delete globalThis.window;
});

test("cinema view does not auto-hide a sidebar that is already stacked below the player", () => {
  assert.equal(shouldAutoEnableCinema(layoutAtVideoWidth(500), true), false);
});

test("a manual cinema choice survives sidebar width changes", () => {
  const classes = new Set();
  const attributes = new Map();
  const buttonListeners = new Map();
  let layoutWidth = layoutAtVideoWidth(719);
  let resize;

  const classList = {
    contains: (name) => classes.has(name),
    toggle(name, enabled) {
      if (enabled) classes.add(name);
      else classes.delete(name);
    },
  };
  const sidebar = {
    setAttribute(name, value) { attributes.set(`sidebar:${name}`, value); },
  };
  const button = {
    classList,
    addEventListener(name, listener) { buttonListeners.set(name, listener); },
    setAttribute(name, value) { attributes.set(`button:${name}`, value); },
  };
  const root = {
    classList,
    getBoundingClientRect() { return { width: layoutWidth }; },
    querySelector(selector) { return selector === ".player-sidebar" ? sidebar : null; },
  };
  const mediaQuery = {
    matches: false,
    addEventListener() {},
  };
  globalThis.window = {
    matchMedia: () => mediaQuery,
    ResizeObserver: class {
      constructor(callback) { resize = callback; }
      observe() {}
    },
  };

  const cinemaView = initCinemaView({ root, button });
  assert.equal(classes.has("cinema-view"), true);

  buttonListeners.get("click")();
  assert.equal(classes.has("cinema-view"), false);
  resize();
  assert.equal(classes.has("cinema-view"), false);

  layoutWidth = layoutAtVideoWidth(720);
  resize();
  assert.equal(classes.has("cinema-view"), false);

  buttonListeners.get("click")();
  assert.equal(classes.has("cinema-view"), true);

  assert.equal(cinemaView.suspendForFullscreen(), true);
  assert.equal(classes.has("cinema-view"), false);
  resize();
  assert.equal(classes.has("cinema-view"), false);
  cinemaView.restoreAfterFullscreen(true);
  assert.equal(classes.has("cinema-view"), true);

  layoutWidth = layoutAtVideoWidth(719);
  resize();
  assert.equal(classes.has("cinema-view"), true);
  assert.equal(attributes.get("button:aria-pressed"), "true");
  assert.equal(attributes.get("sidebar:aria-hidden"), "true");

  delete globalThis.window;
});

test("cinema hides only the right player sidebar", () => {
  assert.match(
    css,
    /\.player-layout\.cinema-view\.cinema-hides-player-sidebar:not\(\.fullscreen-browse\):not\(\.fullscreen-immersive\)\s*\{[\s\S]*?grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(0,\s*0px\);/,
  );
  assert.match(css, /\.sidebar-resize-handle\s*\{[\s\S]*?display:\s*block;/);
  assert.doesNotMatch(css, /cinema-left-sidebar-(?:compact|hidden)/);
  assert.match(
    playerIndex,
    /initCinemaView\(\{[\s\S]*?onCinemaRequested/,
  );
  assert.match(
    readFileSync(new URL("../../static/js/src/player/cinema.js", import.meta.url), "utf8"),
    /defaultSidebarMode:\s*enabled \? defaultSidebarMode : null/,
  );
  assert.match(siteBase, /defaultSidebarMode[\s\S]*?setSidebarWidth\(SIDEBAR_COMPACT_WIDTH, false, false\)/);
  assert.match(siteBase, /setSidebarHidden\(cinemaSidebarDefaultMode === 'hidden'\)/);
  assert.match(siteBase, /forceSidebarMode[\s\S]*?cinemaSidebarDefaultForced/);
  assert.doesNotMatch(
    readFileSync(new URL("../../static/js/src/player/cinema.js", import.meta.url), "utf8"),
    /button\.classList\.toggle\('active'/,
  );
});

test("fullscreen browse suspends cinema layout changes", () => {
  assert.match(
    css,
    /\.player-layout\.cinema-view\.cinema-hides-player-sidebar:not\(\.fullscreen-browse\):not\(\.fullscreen-immersive\)\s*\{[\s\S]*?grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(0,\s*0px\);/,
  );
});

test("requesting cinema from fullscreen exits into cinema view", () => {
  assert.match(
    playerIndex,
    /onCinemaRequested:\s*function \(enabled\)\s*\{[\s\S]*?isPlayerLayoutFullscreen\(\)[\s\S]*?cinemaOnFullscreenExit = enabled[\s\S]*?toggleFullscreen\(\)[\s\S]*?return true/,
  );
  assert.match(playerIndex, /cinemaView\.suspendForFullscreen\(\)/);
  assert.match(playerIndex, /cinemaView\.restoreAfterFullscreen\(/);
});

test("the player header search fills the available right sidebar width", () => {
  assert.match(css, /--player-sidebar-width:\s*320px;/);
  assert.match(css, /\.player-layout\s*\{[\s\S]*?grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(0,\s*var\(--player-sidebar-width\)\);/);
  assert.match(
    css,
    /\.main:has\(> #player-root\) > \.floating-header\s*\{[\s\S]*?width:\s*calc\(var\(--player-sidebar-width\) - 2rem\);/,
  );
  assert.match(
    css,
    /\.main:has\(> #player-root\) > \.floating-header \.header-search\s*\{[\s\S]*?flex:\s*1 1 auto;[\s\S]*?min-width:\s*0;[\s\S]*?width:\s*auto;/,
  );
});

test("the player reserves a top lane for floating navigation controls", () => {
  assert.match(
    css,
    /\.player-main\s*\{[\s\S]*?padding:\s*3\.75rem\s+1\.5rem\s+1\.5rem;/,
  );
});

test("cinema view uses a plain rectangle icon", () => {
  assert.match(playerTemplate, /class="player-cinema-rectangle-icon"/);
  assert.match(playerTemplate, /<rect x="3" y="7" width="18" height="10" rx="1"><\/rect>/);
  assert.doesNotMatch(playerTemplate, /m8 10-2 2 2 2M16 10l2 2-2 2/);
});

test("fullscreen uses a corner-bracket icon", () => {
  assert.match(playerTemplate, /class="player-fullscreen-corners-icon"/);
  assert.doesNotMatch(playerTemplate, />&#x2922;<\/button>/);
});

test("player controls use a shared square hit area and icon size", () => {
  assert.match(css, /--player-control-size:\s*36px;/);
  assert.match(css, /--player-control-icon-size:\s*18px;/);
  assert.match(
    css,
    /media-play-button,[\s\S]*?\.mc-custom-btn\s*\{[\s\S]*?width:\s*var\(--player-control-size\);[\s\S]*?height:\s*var\(--player-control-size\);[\s\S]*?padding:\s*0;/,
  );
  assert.match(
    css,
    /\.mc-custom-btn svg\s*\{[\s\S]*?width:\s*var\(--player-control-icon-size\);[\s\S]*?height:\s*var\(--player-control-icon-size\);/,
  );
});

test("player controls respond to the player width instead of viewport width", () => {
  assert.match(css, /\.dashboard-media-controller\s*\{[\s\S]*?container-type:\s*inline-size;/);
  assert.match(css, /@container \(max-width:\s*760px\)[\s\S]*?media-volume-range[\s\S]*?flex:\s*0 0 52px;/);
  assert.match(css, /@container \(max-width:\s*620px\)[\s\S]*?media-volume-range,[\s\S]*?\.mc-separator[\s\S]*?display:\s*none;/);
  assert.match(css, /@container \(max-width:\s*440px\)[\s\S]*?media-time-display[\s\S]*?display:\s*none;/);
});

test("cinema's visual spacing belongs to its button, not its icon", () => {
  assert.match(
    css,
    /#player-cinema-btn\s*\{[\s\S]*?margin-inline-start:\s*6px;/,
  );
  assert.doesNotMatch(
    css,
    /#player-cinema-btn svg\s*\{[\s\S]*?transform:\s*translateX/,
  );
});

test("cinema view has a configurable default C shortcut", () => {
  assert.match(siteBase, /'player\.cinema':\s*'c'/);
  assert.match(
    playerIndex,
    /sc\.match\('player\.cinema', event\.key\) && cinemaBtn[\s\S]*?cinemaBtn\.click\(\)/,
  );
  assert.match(modalsTemplate, /data-sc="player\.cinema"/);
});
