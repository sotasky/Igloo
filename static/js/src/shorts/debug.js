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

function wantsDebug() {
  try {
    return localStorage.getItem('shortsDebug') === '1'
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
    if (enabled) localStorage.setItem('shortsDebug', '1')
    else localStorage.removeItem('shortsDebug')
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
  try {
    var canvas = document.createElement('canvas')
    var w = 24
    var h = 24
    canvas.width = w
    canvas.height = h
    var ctx = canvas.getContext('2d', { willReadFrequently: true })
    ctx.drawImage(video, 0, 0, w, h)
    var bands = { top: band(ctx, w, 0, 8), middle: band(ctx, w, 8, 16), bottom: band(ctx, w, 16, 24) }
    return bands
  } catch (err) {
    return { error: String(err && err.name || err || 'sample_failed') }
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
  var videoStyle = video ? getComputedStyle(video) : null
  var wrapperStyle = wrapper ? getComputedStyle(wrapper) : null
  var posterStyle = poster ? getComputedStyle(poster) : null
  return {
    t: Math.round(performance.now()),
    timestampMs: Date.now(),
    sessionId: _sessionID,
    event: eventName || 'snapshot',
    id: entry.data && entry.data.id,
    index: _state ? _state.items.indexOf(entry) : -1,
    currentIndex: _state ? _state.currentIndex : -1,
    isCurrent: _state ? _state.items[_state.currentIndex] === entry : false,
    tab: _state && _state.currentTab,
    wrapperClass: wrapper && wrapper.className,
    itemClass: entry.el && entry.el.className,
    wrapperRect: rectOf(wrapper),
    videoRect: rectOf(video),
    posterRect: rectOf(poster),
    wrapperFit: wrapperStyle && wrapperStyle.objectFit,
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
    bands: sampleBands(video),
    extra: extra || null
  }
}

export function recordShortsDebugEvent(entry, eventName, extra) {
  if (!enabled()) return
  var row = snapshot(entry, eventName, extra)
  if (!row) return
  _events.push(row)
  if (_events.length > _maxEvents) _events.shift()
  _pending.push(row)
  if (_pending.length > _maxEvents) _pending = _pending.slice(-_maxEvents)
  scheduleFlush()
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
      return {
        enabled: enabled(),
        sessionId: _sessionID,
        events: _events.length,
        pending: _pending.length,
        endpoint: _logEndpoint,
        serverLog: _serverLog
      }
    },
    current: function () { return snapshot(currentEntry(), 'manual:current') },
    recent: function () { return _events.slice() },
    payload: payload,
    flush: flush,
    download: function () { return flush().then(downloadPayload) },
    clear: function () { _events = []; return true },
    mark: function (label) { recordShortsDebugEvent(currentEntry(), 'mark:' + String(label || 'manual')); return this.current() },
    copy: function () {
      var body = JSON.stringify(payload(), null, 2)
      if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(body)
      return Promise.resolve(body)
    }
  }
  if (enabled()) recordShortsDebugEvent(currentEntry(), 'debug:init')
}
