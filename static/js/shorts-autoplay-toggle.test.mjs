import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import vm from 'node:vm'

async function loadPlayback(videos) {
  const src = await readFile(new URL('./src/shorts/playback.js', import.meta.url), 'utf8')
  const runnable = src
    .replace(/\bexport\s+/g, '') +
    '\nObject.assign(globalThis, { initPlayback, syncRenderedShortVideoLoop });'

  const selectors = []
  const context = vm.createContext({
    document: {
      querySelectorAll(selector) {
        selectors.push(selector)
        return selector === '#shorts-container video' ? videos : []
      },
    },
  })
  vm.runInContext(runnable, context, { filename: 'playback.js' })
  context.__selectors = selectors
  return context
}

test('autoplay toggle synchronizes loop state on rendered shorts videos', async () => {
  const videos = [{ loop: false }, { loop: false }]
  const playback = await loadPlayback(videos)
  const state = { storyMode: false, autoPlayNext: false }

  playback.initPlayback(state, function () {})
  playback.syncRenderedShortVideoLoop()
  assert.deepEqual(videos.map((video) => video.loop), [true, true])

  state.autoPlayNext = true
  playback.syncRenderedShortVideoLoop()
  assert.deepEqual(videos.map((video) => video.loop), [false, false])

  state.autoPlayNext = false
  state.storyMode = true
  playback.syncRenderedShortVideoLoop()
  assert.deepEqual(videos.map((video) => video.loop), [false, false])
  assert.deepEqual(playback.__selectors, [
    '#shorts-container video',
    '#shorts-container video',
    '#shorts-container video',
  ])
})
