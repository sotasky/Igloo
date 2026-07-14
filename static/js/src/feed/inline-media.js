// Inline media module — extracted from feed_page.js
// IntersectionObserver-based autoplay/pause for inline feed videos.

import { makeDraggableSeekbar, attachSeekTooltip } from '../utils.js'

let observer = null
let preloadObserver = null

function preloadVideo(video) {
  if (!(video instanceof HTMLVideoElement)) return
  if (video.dataset.feedPreloadStarted === '1') return
  video.dataset.feedPreloadStarted = '1'
  if (video.preload === 'none') video.preload = 'metadata'
  try { video.load() } catch (_) { }
}

function ensurePreloadObserver() {
  if (preloadObserver) return preloadObserver
  preloadObserver = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      const video = entry.target
      if (!(video instanceof HTMLVideoElement)) return
      if (!entry.isIntersecting) return
      preloadVideo(video)
      preloadObserver.unobserve(video)
    })
  }, { rootMargin: '900px 0px 900px 0px', threshold: [0] })
  return preloadObserver
}

function ensureObserver() {
  if (observer) return observer
  observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      const video = entry.target
      if (!(video instanceof HTMLVideoElement)) return
      if (entry.isIntersecting && entry.intersectionRatio >= 0.55) {
        video.muted = true
        preloadVideo(video)
        video.play().catch(function () { })
      } else {
        try { video.pause() } catch (_) { }
      }
    })
  }, { threshold: [0.35, 0.55, 0.8] })
  return observer
}

function bindVideo(video) {
  if (!(video instanceof HTMLVideoElement)) return false
  if (video.dataset.feedBound === '1') return false
  video.dataset.feedBound = '1'
  const wrap = video.closest('.feed-media-wrap')
  const progress = wrap && wrap.querySelector ? wrap.querySelector('[data-feed-progress]') : null
  const fill = wrap && wrap.querySelector ? wrap.querySelector('[data-feed-progress-fill]') : null

  video.addEventListener('timeupdate', function () {
    if (!fill) return
    const dur = Number(video.duration || 0)
    const cur = Number(video.currentTime || 0)
    const pct = dur > 0 ? Math.max(0, Math.min(100, (cur / dur) * 100)) : 0
    fill.style.width = pct + '%'
  })

  makeDraggableSeekbar(progress, fill, video)
  attachSeekTooltip(progress, video)

  ensurePreloadObserver().observe(video)
  ensureObserver().observe(video)
  return true
}

export function initInlineMedia(container) {
  const scope = container || document
  const videos = Array.from(scope.querySelectorAll('video[data-feed-inline-video]'))
  const newlyBound = videos.filter(bindVideo)
  newlyBound.slice(0, 3).forEach(preloadVideo)
}

// Global bridge for initFeedCards and other callers
window.FeedInlineMedia = { init: initInlineMedia }
