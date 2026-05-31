import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import vm from 'node:vm';

class FakeClassList {
  constructor() {
    this.values = new Set();
  }

  add(name) {
    this.values.add(name);
  }

  remove(name) {
    this.values.delete(name);
  }

  toggle(name, force) {
    const on = force === undefined ? !this.values.has(name) : !!force;
    if (on) this.add(name);
    else this.remove(name);
    return on;
  }
}

class FakeElement {
  constructor(tagName, attrs = {}) {
    this.tagName = String(tagName || 'div').toUpperCase();
    this.attrs = new Map();
    this.children = [];
    this.parentNode = null;
    this.style = {};
    this.classList = new FakeClassList();
    this.scrollCalls = [];
    this.scrollTop = 0;
    this.scrollHeight = 0;
    this.rect = null;
    for (const [key, value] of Object.entries(attrs)) {
      this.setAttribute(key, value);
    }
  }

  setAttribute(name, value) {
    this.attrs.set(String(name), String(value));
  }

  getAttribute(name) {
    return this.attrs.has(String(name)) ? this.attrs.get(String(name)) : null;
  }

  appendChild(child) {
    if (child && child.tagName === '#FRAGMENT') {
      while (child.children.length) this.appendChild(child.children.shift());
      return child;
    }
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  insertBefore(child, before) {
    if (child && child.tagName === '#FRAGMENT') {
      const moving = child.children.splice(0);
      for (const node of moving) this.insertBefore(node, before);
      return child;
    }
    child.parentNode = this;
    const index = this.children.indexOf(before);
    if (index < 0) this.children.push(child);
    else this.children.splice(index, 0, child);
    return child;
  }

  removeChild(child) {
    const index = this.children.indexOf(child);
    if (index >= 0) this.children.splice(index, 1);
    child.parentNode = null;
    return child;
  }

  replaceChildren(...children) {
    this.children.forEach((child) => {
      child.parentNode = null;
    });
    this.children = [];
    children.forEach((child) => this.appendChild(child));
  }

  contains(node) {
    if (node === this) return true;
    for (const child of this.children) {
      if (child === node || (child.contains && child.contains(node))) return true;
    }
    return false;
  }

  get firstChild() {
    return this.children[0] || null;
  }

  get scrollHeight() {
    return this.children.length * 100;
  }

  set scrollHeight(_value) {}

  querySelectorAll(selector) {
    const found = [];
    const wantedTag = String(selector || '').trim().toLowerCase();
    function visit(node) {
      for (const child of node.children) {
        if (!wantedTag || child.tagName.toLowerCase() === wantedTag) found.push(child);
        visit(child);
      }
    }
    visit(this);
    return found;
  }

  scrollIntoView(options) {
    this.scrollCalls.push(options || {});
  }

  focus() {}

  get offsetHeight() {
    return this.getBoundingClientRect().height;
  }

  getBoundingClientRect() {
    return this.rect || { top: 0, left: 0, width: 100, height: 100, bottom: 100, right: 100 };
  }
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

async function flush() {
  await new Promise((resolve) => setImmediate(resolve));
}

async function loadOverlay() {
  const src = await readFile(new URL('./src/shorts/overlay.js', import.meta.url), 'utf8');
  const runnable = src
    .replace(/^import .*$/gm, '')
    .replace(/\bexport\s+/g, '') +
    '\nObject.assign(globalThis, { initOverlay, scrollToIndex, activateIndex, openOverlayAtIndex, goNext, goPrev, ensureContainerScrollBehavior });';

  const timers = [];
  const calls = {
    startedSlideshows: [],
    slideshowIndexes: [],
    pausedExcept: [],
  };

  const context = vm.createContext({
    console,
    window: {},
    document: { createElement: (tag) => new FakeElement(tag) },
    requestAnimationFrame: (fn) => setImmediate(fn),
    cancelAnimationFrame: clearImmediate,
    setTimeout(fn) {
      timers.push(fn);
      return timers.length;
    },
    clearTimeout() {},
    IntersectionObserver: class {
      observe() {}
      disconnect() {}
    },
    performance: { now: () => Date.now() },
    localStorage: { setItem() {}, getItem() { return ''; } },
    pauseAllShorts(exceptId) { calls.pausedExcept.push(exceptId || ''); },
    setSlideshowIndex(entryArg, index) {
      calls.slideshowIndexes.push({ id: entryArg && entryArg.data && entryArg.data.id, index });
      if (entryArg && entryArg.refs && entryArg.refs.slideshow) entryArg.refs.slideshow.index = index;
    },
    startSlideshowPlayback(entryArg) {
      calls.startedSlideshows.push(entryArg && entryArg.data && entryArg.data.id);
      if (entryArg && entryArg.refs && entryArg.refs.slideshow) entryArg.refs.slideshow.playing = true;
    },
    t(_key, fallback) { return fallback; },
    tf(_key, fallback) { return fallback; },
    recordShortsDebugEvent() {},
  });
  context.__calls = calls;
  context.__runTimers = () => {
    const pending = timers.splice(0);
    for (const fn of pending) fn();
  };
  vm.runInContext(runnable, context, { filename: 'overlay.js' });
  return context;
}

function card(id, skeleton = false) {
  return new FakeElement('a', {
    'data-video-id': id,
    'data-shorts-card-skeleton': skeleton ? '1' : '0',
  });
}

function fakeVideo(currentTime = 0) {
  return {
    currentTime,
    readyState: 4,
    paused: true,
    preload: 'metadata',
    playCalls: 0,
    loadCalls: 0,
    play() {
      this.playCalls += 1;
      this.paused = false;
      return Promise.resolve();
    },
    pause() {
      this.paused = true;
    },
    load() {
      this.loadCalls += 1;
    },
  };
}

function entry(id, opts = {}) {
  const wrapper = new FakeElement('div');
  const refs = { wrapper };
  if (opts.video) refs.video = fakeVideo(opts.currentTime || 0);
  if (opts.slideshow) {
    refs.slideshow = {
      index: opts.slideIndex || 0,
      count: 3,
      timer: 9,
      audio: { currentTime: opts.audioTime || 0, pause() {}, play() { return Promise.resolve(); } },
    };
  }
  return {
    el: new FakeElement('section', { 'data-video-id': id }),
    data: { id, page: 1, sortAtMs: 1 },
    refs,
  };
}

function initBasicOverlay(overlay, options = {}) {
  const sourceContainer = new FakeElement('div');
  const cards = Array.from({ length: options.count || 6 }, (_unused, index) => card('v' + index, options.skeleton === index));
  cards.forEach((el) => sourceContainer.appendChild(el));
  const shortsContainer = new FakeElement('div');
  const persisted = [];
  const state = {
    cards,
    items: new Array(cards.length).fill(null),
    byId: new Map(),
    cardIndexById: new Map(cards.map((el, index) => [String(el.getAttribute('data-video-id')), index])),
    currentIndex: -1,
    overlayOpen: false,
    storyMode: false,
    renderedStart: -1,
    renderedEnd: -1,
    overlayHydrated: false,
    hydrateCardElement(el) {
      if (options.hydrateCardElement) return options.hydrateCardElement(el);
      el.setAttribute('data-shorts-card-skeleton', '0');
      return Promise.resolve(el);
    },
    isSkeletonCard(el) {
      return el && el.getAttribute('data-shorts-card-skeleton') === '1';
    },
  };

  overlay.initOverlay(state, {
    shortsContainer,
    gridShell: new FakeElement('div'),
    layout: new FakeElement('div'),
    upToDateOverlay: null,
    sourceContainer,
    sourceCardSelector: 'a',
    doc: {
      body: new FakeElement('body'),
      createDocumentFragment: () => new FakeElement('#fragment'),
    },
  }, {
    closeBookmarkMenu() {},
    ensureGridThumbnails() {},
    updateTopControls() {},
    updateCurrentActionButtons() {},
    setLastViewedShortId(id) { persisted.push({ type: 'id', id }); },
    setLastViewedShortResume(id, index) { persisted.push({ type: 'resume', id, index }); },
    markShortViewed(id) { persisted.push({ type: 'viewed', id }); },
    getShortsInfiniteController() { return null; },
    parseCardData(el) {
      const id = String(el.getAttribute('data-video-id') || '');
      if (!id || el.getAttribute('data-shorts-card-skeleton') === '1') return null;
      return { id, page: 1, sortAtMs: 1 };
    },
    makeShortItem(data) {
      if (options.makeShortItem) return options.makeShortItem(data);
      return entry(data.id, options.entryOptions && options.entryOptions[data.id]);
    },
    iconSvg() { return ''; },
  });
  return { state, cards, sourceContainer, shortsContainer, persisted };
}

test('late skeleton hydration does not override a newer navigation target', async () => {
  const overlay = await loadOverlay();
  const hydration = deferred();
  const sourceContainer = new FakeElement('div');
  const cards = [card('current'), card('late', true)];
  cards.forEach((el) => sourceContainer.appendChild(el));
  const current = entry('current');
  const late = entry('late');
  const state = {
    cards,
    items: [current, null],
    byId: new Map([['current', current]]),
    cardIndexById: new Map([['current', 0], ['late', 1]]),
    currentIndex: 0,
    overlayOpen: true,
    storyMode: false,
    renderedStart: 0,
    renderedEnd: 0,
    overlayHydrated: true,
    hydrateCardElement(el) {
      return hydration.promise.then(() => {
        el.setAttribute('data-shorts-card-skeleton', '0');
      });
    },
    isSkeletonCard(el) {
      return el && el.getAttribute('data-shorts-card-skeleton') === '1';
    },
  };

  overlay.initOverlay(state, {
    shortsContainer: new FakeElement('div'),
    gridShell: new FakeElement('div'),
    layout: new FakeElement('div'),
    upToDateOverlay: null,
    sourceContainer,
    sourceCardSelector: 'a',
    doc: {
      body: new FakeElement('body'),
      createDocumentFragment: () => new FakeElement('#fragment'),
    },
  }, {
    closeBookmarkMenu() {},
    ensureGridThumbnails() {},
    updateTopControls() {},
    updateCurrentActionButtons() {},
    setLastViewedShortId() {},
    setLastViewedShortResume() {},
    markShortViewed() {},
    getShortsInfiniteController() { return null; },
    parseCardData(el) {
      const id = String(el.getAttribute('data-video-id') || '');
      if (!id || el.getAttribute('data-shorts-card-skeleton') === '1') return null;
      return { id, page: 1, sortAtMs: 1 };
    },
    makeShortItem(data) {
      return data.id === 'late' ? late : entry(data.id);
    },
    iconSvg() { return ''; },
  });

  assert.equal(overlay.scrollToIndex(1, 'smooth'), true);
  assert.equal(overlay.scrollToIndex(0, 'smooth'), true);
  hydration.resolve();
  await flush();
  await flush();

  assert.equal(state.currentIndex, 0);
  assert.equal(late.el.scrollCalls.length, 0);
});

test('vertical deck does not let late skeleton hydration navigate away from the current item', async () => {
  const overlay = await loadOverlay();
  const hydration = deferred();
  const { state } = initBasicOverlay(overlay, {
    skeleton: 2,
    hydrateCardElement(el) {
      return hydration.promise.then(() => {
        el.setAttribute('data-shorts-card-skeleton', '0');
        return el;
      });
    },
  });

  overlay.openOverlayAtIndex(1, true);
  assert.equal(state.currentIndex, 1);

  assert.equal(overlay.scrollToIndex(2, 'smooth'), true);
  assert.equal(state.currentIndex, 1);

  hydration.resolve();
  await flush();
  await flush();

  assert.equal(state.currentIndex, 1);
});

test('vertical deck transition ignores additional navigation until landing', async () => {
  const overlay = await loadOverlay();
  const { state } = initBasicOverlay(overlay);

  overlay.openOverlayAtIndex(1, true);
  assert.equal(state.currentIndex, 1);

  assert.equal(overlay.scrollToIndex(2, 'smooth'), true);
  assert.equal(state.currentIndex, 2);
  assert.equal(overlay.scrollToIndex(3, 'smooth'), true);

  assert.equal(state.currentIndex, 2);
  assert.equal(state.deck && state.deck.targetIndex, 2);
});

test('vertical deck starts target playback from zero immediately and persists only after landing', async () => {
  const overlay = await loadOverlay();
  const target = entry('v2', { video: true, currentTime: 17 });
  const { state, persisted } = initBasicOverlay(overlay, {
    makeShortItem(data) {
      return data.id === 'v2' ? target : entry(data.id, { video: true });
    },
  });

  overlay.openOverlayAtIndex(1, true);
  persisted.splice(0);

  assert.equal(overlay.scrollToIndex(2, 'smooth'), true);
  assert.equal(target.refs.video.currentTime, 0);
  assert.equal(target.refs.video.playCalls, 1);
  assert.equal(persisted.length, 0);

  overlay.__runTimers();

  assert.deepEqual(persisted.map((row) => [row.type, row.id, row.index]), [
    ['viewed', 'v2', undefined],
    ['id', 'v2', undefined],
    ['resume', 'v2', 2],
  ]);
});

test('vertical deck resets slideshow state when a rendered item becomes active', async () => {
  const overlay = await loadOverlay();
  const target = entry('v2', { slideshow: true, slideIndex: 2, audioTime: 11 });
  const { state } = initBasicOverlay(overlay, {
    makeShortItem(data) {
      return data.id === 'v2' ? target : entry(data.id);
    },
  });

  overlay.openOverlayAtIndex(1, true);
  assert.equal(overlay.scrollToIndex(2, 'smooth'), true);

  assert.equal(state.currentIndex, 2);
  assert.equal(target.refs.slideshow.index, 0);
  assert.equal(target.refs.slideshow.audio.currentTime, 0);
  assert.deepEqual(overlay.__calls.slideshowIndexes.at(-1), { id: 'v2', index: 0 });
  assert.equal(overlay.__calls.startedSlideshows.at(-1), 'v2');
});

test('vertical deck activation builds nearby entries without mutating scrollTop', async () => {
  const overlay = await loadOverlay();
  const sourceContainer = new FakeElement('div');
  const cards = Array.from({ length: 30 }, (_unused, index) => card('v' + index));
  cards.forEach((el) => sourceContainer.appendChild(el));

  const shortsContainer = new FakeElement('div');
  shortsContainer.scrollTop = 400;
  const items = new Array(cards.length).fill(null);
  for (let i = 10; i <= 20; i += 1) {
    items[i] = entry('v' + i);
    shortsContainer.appendChild(items[i].el);
  }

  const state = {
    cards,
    items,
    byId: new Map(items.filter(Boolean).map((it) => [it.data.id, it])),
    cardIndexById: new Map(cards.map((el, index) => [String(el.getAttribute('data-video-id')), index])),
    currentIndex: 13,
    overlayOpen: true,
    storyMode: false,
    renderedStart: 10,
    renderedEnd: 20,
    overlayHydrated: true,
  };

  overlay.initOverlay(state, {
    shortsContainer,
    gridShell: new FakeElement('div'),
    layout: new FakeElement('div'),
    upToDateOverlay: null,
    sourceContainer,
    sourceCardSelector: 'a',
    doc: {
      body: new FakeElement('body'),
      createDocumentFragment: () => new FakeElement('#fragment'),
    },
  }, {
    closeBookmarkMenu() {},
    ensureGridThumbnails() {},
    updateTopControls() {},
    updateCurrentActionButtons() {},
    setLastViewedShortId() {},
    setLastViewedShortResume() {},
    markShortViewed() {},
    getShortsInfiniteController() { return null; },
    parseCardData(el) {
      const id = String(el.getAttribute('data-video-id') || '');
      return id ? { id, page: 1, sortAtMs: 1 } : null;
    },
    makeShortItem(data) {
      return entry(data.id);
    },
    iconSvg() { return ''; },
  });

  overlay.activateIndex(12, { force: true, play: false, persist: false });

  assert.equal(state.currentIndex, 12);
  assert.equal(shortsContainer.scrollTop, 400);
  assert.equal(state.items[2].data.id, 'v2');
  assert.equal(state.items[22].data.id, 'v22');
  assert.match(state.items[12].el.style.transform, /translate3d\(0, 0%, 0\)/);
});
