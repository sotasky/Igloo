// Opt-in Moments media diagnostics. Enable with ?shorts_debug=1 or
// MpaShortsDebug.enable(). Events are kept in-page, POSTed to the server
// debug log, and can be downloaded as JSON.

import { apiFetch } from '../utils.js'

var _state = null
var _events = []
var _maxEvents = 160
var _pending = []
var _flushTimer = null
var _flushing = false
var _sessionID = 'moments-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8)
var _logEndpoint = '/api/logs/moments'
var _serverLog = '~/.local/share/igloo/logs/moments/debug.jsonl'
var _debugTTL = 30 * 60 * 1000
var _bandSampleInterval = 1500
var _lastEventByKey = {}

function debugUntil() {
  try {
    return parseInt(localStorage.getItem('shortsDebugUntil') || '0', 10) || 0
  } catch (_) {
    return 0
  }
}

function wantsDebug() {
  try {
    if (localStorage.getItem('shortsDebug') !== '1') return false
    var until = debugUntil()
    if (!until || until <= Date.now()) {
      setEnabled(false)
      return false
    }
    return true
  } catch (_) {
    return false
  }
}

function syncDebugFlagFromURL() {
  try {
    if (/(?:^|[?&])shorts_debug=1(?:&|$)/.test(window.location.search)) setEnabled(true)
    if (/(?:^|[?&])shorts_debug=0(?:&|$)/.test(window.location.search)) setEnabled(false)
  } catch (_) {}
}

function setEnabled(enabled) {
  try {
    if (enabled) {
      localStorage.setItem('shortsDebug', '1')
      localStorage.setItem('shortsDebugUntil', String(Date.now() + _debugTTL))
    } else {
      localStorage.removeItem('shortsDebug')
      localStorage.removeItem('shortsDebugUntil')
    }
  } catch (_) {}
}

function enabled() {
  return wantsDebug()
}

function rectOf(el) {
  if (!el || typeof el.getBoundingClientRect !== 'function') return null
  var r = el.getBoundingClientRect()
  return {
    x: Math.round(r.x),
    y: Math.round(r.y),
    w: Math.round(r.width),
    h: Math.round(r.height),
    bottom: Math.round(r.bottom)
  }
}

function q(sel, root) {
  return (root || document).querySelector(sel)
}

function styleOf(el) {
  if (!el || typeof getComputedStyle !== 'function') return null
  try {
    var s = getComputedStyle(el)
    return {
      display: s.display,
      visibility: s.visibility,
      opacity: s.opacity,
      pointerEvents: s.pointerEvents
    }
  } catch (_) {
    return null
  }
}

function elementSnapshot(el) {
  if (!el) return { exists: false }
  return {
    exists: true,
    className: String(el.className || ''),
    rect: rectOf(el),
    style: styleOf(el),
    textLen: String(el.textContent || '').trim().length
  }
}

function snapDeltaOf(entry) {
  var container = document.getElementById('shorts-container')
  if (!entry || !entry.el || !container) return null
  if (typeof entry.el.getBoundingClientRect !== 'function' ||
      typeof container.getBoundingClientRect !== 'function') return null
  var itemRect = entry.el.getBoundingClientRect()
  var containerRect = container.getBoundingClientRect()
  return Math.round(itemRect.top - containerRect.top)
}

function visibleGeometry(entry) {
  var container = document.getElementById('shorts-container')
  if (!entry || !entry.el || !container) return null
  if (typeof entry.el.getBoundingClientRect !== 'function' ||
      typeof container.getBoundingClientRect !== 'function') return null
  var itemRect = entry.el.getBoundingClientRect()
  var containerRect = container.getBoundingClientRect()
  var top = Math.max(itemRect.top, containerRect.top)
  var bottom = Math.min(itemRect.bottom, containerRect.bottom)
  var visibleHeight = Math.max(0, bottom - top)
  var itemHeight = Math.max(1, itemRect.height || 0)
  var visibleTopPx = Math.max(0, containerRect.top - itemRect.top)
  var visibleBottomPx = Math.max(0, itemRect.bottom - containerRect.bottom)
  return {
    visibleTopPx: Math.round(visibleTopPx),
    visibleBottomPx: Math.round(visibleBottomPx),
    visibleHeight: Math.round(visibleHeight),
    visibleRatio: Number(Math.min(1, visibleHeight / itemHeight).toFixed(3)),
    missingEdge: visibleTopPx > 2 ? 'top' : (visibleBottomPx > 2 ? 'bottom' : '')
  }
}

function rangesOf(ranges) {
  var out = []
  if (!ranges) return out
  for (var i = 0; i < ranges.length; i += 1) {
    try {
      out.push([Number(ranges.start(i).toFixed(3)), Number(ranges.end(i).toFixed(3))])
    } catch (_) {}
  }
  return out
}

function shortUrl(url) {
  var value = String(url || '')
  if (value.length <= 140) return value
  return value.slice(0, 90) + '...' + value.slice(-40)
}

function sampleBands(video) {
  if (!video || !video.videoWidth || !video.videoHeight) return null
  var now = Date.now()
  var cached = video._shortsDebugBandsCache
  if (cached && now - cached.at < _bandSampleInterval) return cached.bands
  try {
    var canvas = document.createElement('canvas')
    var w = 24
    var h = 24
    canvas.width = w
    canvas.height = h
    var ctx = canvas.getContext('2d', { willReadFrequently: true })
    ctx.drawImage(video, 0, 0, w, h)
    var bands = { top: band(ctx, w, 0, 8), middle: band(ctx, w, 8, 16), bottom: band(ctx, w, 16, 24) }
    video._shortsDebugBandsCache = { at: now, bands: bands }
    return bands
  } catch (err) {
    var errorBands = { error: String(err && err.name || err || 'sample_failed') }
    video._shortsDebugBandsCache = { at: now, bands: errorBands }
    return errorBands
  }
}

function band(ctx, width, startY, endY) {
  var data = ctx.getImageData(0, startY, width, endY - startY).data
  var total = 0
  var dark = 0
  var count = data.length / 4
  for (var i = 0; i < data.length; i += 4) {
    var lum = (data[i] * 0.2126) + (data[i + 1] * 0.7152) + (data[i + 2] * 0.0722)
    total += lum
    if (lum < 8) dark += 1
  }
  return { avg: Math.round(total / Math.max(1, count)), darkPct: Math.round((dark / Math.max(1, count)) * 100) }
}

function chromeSnapshot(entry) {
  var refs = entry && entry.refs
  var root = refs && refs.wrapper ? refs.wrapper : (entry && entry.el)
  var info = refs && refs.info ? refs.info : q('.shorts-info-overlay', root)
  var author = refs && refs.author ? refs.author : q('.shorts-author-name', root)
  var title = refs && refs.title ? refs.title : q('.shorts-video-title', root)
  var actions = refs && refs.actions ? refs.actions : q('.shorts-actions', root)
  var progress = refs && refs.progressContainer ? refs.progressContainer : q('.val-progress-container', root)
  return {
    info: elementSnapshot(info),
    author: elementSnapshot(author),
    title: elementSnapshot(title),
    actions: elementSnapshot(actions),
    progress: elementSnapshot(progress)
  }
}

function currentEntry() {
  if (!_state || _state.currentIndex < 0) return null
  return _state.items && _state.items[_state.currentIndex]
}

function snapshot(entry, eventName, extra) {
  entry = entry || currentEntry()
  if (!entry || !entry.refs) return null
  var video = entry.refs.video
  var wrapper = entry.refs.wrapper
  var poster = entry.refs.poster
  var container = document.getElementById('shorts-container')
  var info = entry.refs.info || q('.shorts-info-overlay', wrapper || entry.el)
  var author = entry.refs.author || q('.shorts-author-name', wrapper || entry.el)
  var title = entry.refs.title || q('.shorts-video-title', wrapper || entry.el)
  var actions = entry.refs.actions || q('.shorts-actions', wrapper || entry.el)
  var progress = entry.refs.progressContainer || q('.val-progress-container', wrapper || entry.el)
  var videoStyle = video ? getComputedStyle(video) : null
  var wrapperStyle = wrapper ? getComputedStyle(wrapper) : null
  var posterStyle = poster ? getComputedStyle(poster) : null
  var visible = visibleGeometry(entry) || {}
  var videoFrameBands = sampleBands(video)
  return {
    t: Math.round(performance.now()),
    timestampMs: Date.now(),
    sessionId: _sessionID,
    event: eventName || 'snapshot',
    id: entry.data && entry.data.id,
    isSkeletonCard: !!(entry.data && entry.data.isSkeleton),
    index: _state ? _state.items.indexOf(entry) : -1,
    currentIndex: _state ? _state.currentIndex : -1,
    isCurrent: _state ? _state.items[_state.currentIndex] === entry : false,
    tab: _state && _state.currentTab,
    wrapperClass: wrapper && wrapper.className,
    itemClass: entry.el && entry.el.className,
    containerRect: rectOf(container),
    itemRect: rectOf(entry.el),
    snapDelta: snapDeltaOf(entry),
    visibleTopPx: visible.visibleTopPx,
    visibleBottomPx: visible.visibleBottomPx,
    visibleRatio: visible.visibleRatio,
    visible: visible,
    wrapperRect: rectOf(wrapper),
    videoRect: rectOf(video),
    posterRect: rectOf(poster),
    infoRect: rectOf(info),
    authorRect: rectOf(author),
    titleRect: rectOf(title),
    actionsRect: rectOf(actions),
    progressRect: rectOf(progress),
    chrome: chromeSnapshot(entry),
    containerScroll: container ? {
      top: Math.round(container.scrollTop || 0),
      height: Math.round(container.scrollHeight || 0),
      clientHeight: Math.round(container.clientHeight || 0)
    } : null,
    wrapperOverflow: wrapperStyle && wrapperStyle.overflow,
    wrapperRadius: wrapperStyle && wrapperStyle.borderRadius,
    wrapperFit: wrapperStyle && wrapperStyle.objectFit,
    videoDisplay: videoStyle && videoStyle.display,
    videoFit: videoStyle && videoStyle.objectFit,
    videoOpacity: videoStyle && videoStyle.opacity,
    posterDisplay: posterStyle && posterStyle.display,
    video: video ? {
      readyState: video.readyState,
      networkState: video.networkState,
      paused: video.paused,
      ended: video.ended,
      preload: video.preload,
      currentTime: Number((video.currentTime || 0).toFixed(3)),
      duration: Number((video.duration || 0).toFixed(3)),
      width: video.videoWidth,
      height: video.videoHeight,
      buffered: rangesOf(video.buffered),
      seekable: rangesOf(video.seekable),
      src: shortUrl(video.currentSrc || video.src),
      poster: shortUrl(video.poster)
    } : null,
    videoFrameBands: videoFrameBands,
    bands: videoFrameBands,
    extra: extra || null
  }
}

export function recordShortsDebugEvent(entry, eventName, extra) {
  if (!enabled()) return
  if (!eventAllowed(entry, eventName, extra)) return
  var row = snapshot(entry, eventName, extra)
  if (!row) return
  _events.push(row)
  if (_events.length > _maxEvents) _events.shift()
  _pending.push(row)
  if (_pending.length > _maxEvents) _pending = _pending.slice(-_maxEvents)
  scheduleFlush()
}

function eventAllowed(entry, eventName, extra) {
  var interval = eventMinInterval(eventName)
  if (!interval) return true
  var id = entry && entry.data && entry.data.id
  var phase = extra && extra.phase ? ':' + extra.phase : ''
  var key = [id || 'current', eventName || 'snapshot', phase].join('|')
  var now = Date.now()
  var last = _lastEventByKey[key] || 0
  if (now - last < interval) return false
  _lastEventByKey[key] = now
  return true
}

function eventMinInterval(eventName) {
  switch (eventName) {
    case 'chrome:snapshot':
      return 1000
    case 'video:timeupdate':
      return 1000
    case 'intersect:candidate':
    case 'deck:transition-start':
      return 500
    default:
      return 0
  }
}

function scheduleFlush() {
  if (_flushTimer) return
  _flushTimer = setTimeout(function () {
    _flushTimer = null
    flush()
  }, 750)
}

function flush() {
  if (_flushing || !_pending.length) return Promise.resolve({ written: 0 })
  _flushing = true
  var batch = _pending.splice(0, 80)
  return apiFetch(_logEndpoint, {
    method: 'POST',
    body: JSON.stringify({
      device_id: 'web-moments',
      entries: batch.map(function (row) {
        return {
          level: 'debug',
          event: 'moments_video_debug',
          timestamp_ms: row.timestampMs || Date.now(),
          fields: row
        }
      })
    })
  }).catch(function () {
    _pending = batch.concat(_pending).slice(-_maxEvents)
    return { written: 0, error: true }
  }).finally(function () {
    _flushing = false
    if (_pending.length) scheduleFlush()
  })
}

function payload() {
  return {
    sessionId: _sessionID,
    generatedAtMs: Date.now(),
    serverLog: _serverLog,
    current: snapshot(currentEntry(), 'manual:current'),
    recent: _events.slice()
  }
}

function downloadPayload() {
  var body = JSON.stringify(payload(), null, 2)
  var blob = new Blob([body], { type: 'application/json' })
  var a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = 'igloo-moments-debug-' + _sessionID + '.json'
  document.body.appendChild(a)
  a.click()
  setTimeout(function () {
    URL.revokeObjectURL(a.href)
    a.remove()
  }, 0)
  return body
}

export function attachShortVideoDebug(entry) {
  var video = entry && entry.refs && entry.refs.video
  if (!video || video._shortsDebugAttached) return
  video._shortsDebugAttached = true
  ;['loadstart', 'loadedmetadata', 'loadeddata', 'canplay', 'playing', 'waiting', 'stalled', 'suspend', 'resize', 'timeupdate', 'error'].forEach(function (name) {
    video.addEventListener(name, function () {
      if (name === 'timeupdate' && video.currentTime > 1) return
      recordShortsDebugEvent(entry, 'video:' + name)
    })
  })
  if (typeof video.requestVideoFrameCallback === 'function') {
    video.requestVideoFrameCallback(function (_now, meta) {
      recordShortsDebugEvent(entry, 'video:first-frame', {
        mediaTime: meta && meta.mediaTime,
        presentedFrames: meta && meta.presentedFrames,
        width: meta && meta.width,
        height: meta && meta.height
      })
    })
  }
}

export function initShortsDebug(stateRef) {
  _state = stateRef
  syncDebugFlagFromURL()
  window.MpaShortsDebug = {
    enable: function () { setEnabled(true); recordShortsDebugEvent(currentEntry(), 'debug:enabled'); return this.current() },
    disable: function () { setEnabled(false); return flush() },
    enabled: enabled,
    status: function () {
      var isEnabled = enabled()
      var until = debugUntil()
      return {
        enabled: isEnabled,
        sessionId: _sessionID,
        events: _events.length,
        pending: _pending.length,
        endpoint: _logEndpoint,
        serverLog: _serverLog,
        expiresAtMs: until || 0,
        expiresInMs: until ? Math.max(0, until - Date.now()) : 0
      }
    },
    current: function () { return snapshot(currentEntry(), 'manual:current') },
    recent: function () { return _events.slice() },
    payload: payload,
    flush: flush,
    download: function () { return flush().then(downloadPayload) },
    clear: function () { _events = []; return true },
    mark: function (label) { recordShortsDebugEvent(currentEntry(), 'debug:marker', { label: String(label || 'manual') }); return this.current() },
    copy: function () {
      var body = JSON.stringify(payload(), null, 2)
      if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(body)
      return Promise.resolve(body)
    }
  }
  if (enabled()) recordShortsDebugEvent(currentEntry(), 'debug:init')
}
