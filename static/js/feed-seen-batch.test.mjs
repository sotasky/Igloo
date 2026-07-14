import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import vm from 'node:vm'

class FakeCard {
  constructor(tweetId) {
    this.attributes = new Map([
      ['data-feed-seen', '0'],
      ['data-tweet-id', tweetId],
    ])
    this.classes = new Set()
    this.classList = { add: (name) => this.classes.add(name) }
  }

  getAttribute(name) { return this.attributes.get(name) || null }
  setAttribute(name, value) { this.attributes.set(name, String(value)) }
}

class FakeRoot {
  constructor(cards) {
    this.cards = cards
  }

  getAttribute(name) { return name === 'data-seen-tracked' ? '1' : null }
  querySelectorAll(selector) {
    if (selector.includes('data-feed-seen="0"')) {
      return this.cards.filter((card) => card.getAttribute('data-feed-seen') === '0')
    }
    return this.cards
  }
}

class FakeObserver {
  constructor(callback) {
    this.callback = callback
    this.observed = new Set()
    FakeObserver.instance = this
  }

  observe(card) { this.observed.add(card) }
  unobserve(card) { this.observed.delete(card) }
  intersect(cards) {
    this.callback(cards.map((target) => ({ target, isIntersecting: true })))
  }
}

async function loadSeenTracking() {
  const source = await readFile(new URL('./src/feed/seen.js', import.meta.url), 'utf8')
  const runnable = source.replace(/\bexport\s+/g, '') +
    '\nObject.assign(globalThis, { initFeedSeenTracking });'
  const context = vm.createContext({
    Array,
    IntersectionObserver: FakeObserver,
    Promise,
    Set,
    clearTimeout,
    setTimeout,
  })
  vm.runInContext(runnable, context, { filename: 'seen.js' })
  return context.initFeedSeenTracking
}

test('visible feed cards share one seen request', async () => {
  const initFeedSeenTracking = await loadSeenTracking()
  const cards = [new FakeCard('post_a'), new FakeCard('post_b'), new FakeCard('post_a')]
  const root = new FakeRoot(cards)
  const requests = []
  initFeedSeenTracking(root, async (ids) => { requests.push(Array.from(ids)) })

  FakeObserver.instance.intersect(cards)
  await new Promise((resolve) => setTimeout(resolve, 160))

  assert.deepEqual(requests, [['post_a', 'post_b']])
  for (const card of cards) {
    assert.equal(card.getAttribute('data-feed-seen'), '1')
    assert.equal(card.classes.has('feed-card-seen'), true)
  }
})
