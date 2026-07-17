import assert from "node:assert/strict";
import test from "node:test";

import {
  CINEMA_MIN_PLAYER_WIDTH,
  PLAYER_MAIN_HORIZONTAL_PADDING,
  PLAYER_SIDEBAR_WIDTH,
  initCinemaView,
  shouldAutoEnableCinema,
} from "../../static/js/src/player/cinema.js";

const layoutAtVideoWidth = (videoWidth) => (
  videoWidth + PLAYER_SIDEBAR_WIDTH + PLAYER_MAIN_HORIZONTAL_PADDING
);

test("cinema view starts when the normal video column is narrower than 720px", () => {
  assert.equal(CINEMA_MIN_PLAYER_WIDTH, 720);
  assert.equal(shouldAutoEnableCinema(layoutAtVideoWidth(719), false), true);
  assert.equal(shouldAutoEnableCinema(layoutAtVideoWidth(720), false), false);
});

test("cinema view does not auto-hide a sidebar that is already stacked below the player", () => {
  assert.equal(shouldAutoEnableCinema(layoutAtVideoWidth(500), true), false);
});

test("a manual cinema choice holds until the available-width recommendation changes", () => {
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

  initCinemaView({ root, button });
  assert.equal(classes.has("cinema-view"), true);

  buttonListeners.get("click")();
  assert.equal(classes.has("cinema-view"), false);
  resize();
  assert.equal(classes.has("cinema-view"), false);

  layoutWidth = layoutAtVideoWidth(720);
  resize();
  assert.equal(classes.has("cinema-view"), false);

  layoutWidth = layoutAtVideoWidth(719);
  resize();
  assert.equal(classes.has("cinema-view"), true);
  assert.equal(attributes.get("button:aria-pressed"), "true");
  assert.equal(attributes.get("sidebar:aria-hidden"), "true");

  delete globalThis.window;
});
