import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import vm from 'node:vm'

class FakeVideo {
  constructor() {
    this.dataset = {}
    this.preload = 'none'
    this.loadCount = 0
  }

  addEventListener() {}
  closest() { return null }
  load() { this.loadCount += 1 }
}

async function loadInlineMedia() {
  const source = await readFile(new URL('./src/feed/inline-media.js', import.meta.url), 'utf8')
  const runnable = source
    .replace(/^import .*$/m, 'const makeDraggableSeekbar = () => {}; const attachSeekTooltip = () => {};')
    .replace(/\bexport\s+/g, '') +
    '\nObject.assign(globalThis, { initInlineMedia });'

  class FakeObserver {
    observe() {}
    unobserve() {}
  }

  const window = {}
  const context = vm.createContext({
    Array,
    HTMLVideoElement: FakeVideo,
    IntersectionObserver: FakeObserver,
    document: {},
    window,
  })
  vm.runInContext(runnable, context, { filename: 'inline-media.js' })
  return context
}

test('each appended feed batch preloads its first three new videos', async () => {
  const media = await loadInlineMedia()
  const existing = Array.from({ length: 4 }, () => new FakeVideo())
  const appended = Array.from({ length: 4 }, () => new FakeVideo())
  let videos = existing
  const feed = {
    querySelectorAll() { return videos },
  }

  media.initInlineMedia(feed)
  assert.deepEqual(existing.map((video) => video.loadCount), [1, 1, 1, 0])

  videos = existing.concat(appended)
  media.initInlineMedia(feed)

  assert.deepEqual(existing.map((video) => video.loadCount), [1, 1, 1, 0])
  assert.deepEqual(appended.map((video) => video.loadCount), [1, 1, 1, 0])
})
