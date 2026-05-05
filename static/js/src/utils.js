// Shared utilities for ES module bundles.

export function escapeHtml(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

export function cssEscape(value) {
  var text = String(value == null ? '' : value)
  if (window.CSS && typeof window.CSS.escape === 'function') return window.CSS.escape(text)
  return text.replace(/["\\]/g, '\\$&')
}

export function jsLinkify(text) {
  if (!text) return ''
  var s = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
  s = s.replace(/(https?:\/\/[^\s<>\[\]()"']+)/gi,
    '<a href="$1" class="feed-inline-link" target="_blank" rel="noopener">$1</a>')
  // @mention — skip when preceded by a word char (email local part) or
  // followed by `@` or `.tld` (email domain part).
  s = s.replace(/(<a\b[^>]*>[\s\S]*?<\/a>)|(?<!\w)@([A-Za-z0-9_]{1,15})(?![A-Za-z0-9_])/g, function (m, anchor, mention, offset, full) {
    if (anchor) return anchor
    var after = full.slice(offset + m.length)
    if (after.charAt(0) === '@' || /^\.[A-Za-z]{2,12}\b/.test(after)) return m
    return '<a href="/channels/twitter_' + mention.toLowerCase() + '" class="feed-inline-link">@' + mention + '</a>'
  })
  return s.replace(/\n/g, '<br>\n')
}

export function toFxTwitterUrl(rawUrl) {
  var value = String(rawUrl || '').trim()
  if (!value) return ''
  if (!useEmbedFriendlyShareLinks()) return value
  try {
    var parsed = new URL(value, window.location.origin)
    var host = String(parsed.hostname || '').toLowerCase()
    if (host === 'x.com' || host === 'www.x.com' || host.endsWith('.x.com') ||
        host === 'twitter.com' || host === 'www.twitter.com' || host.endsWith('.twitter.com')) {
      parsed.hostname = 'fxtwitter.com'
    }
    if (host === 'tiktok.com' || host === 'www.tiktok.com' || host.endsWith('.tiktok.com')) {
      parsed.hostname = 'tnktok.com'
    }
    return parsed.toString()
  } catch (_) { return value }
}

export function useEmbedFriendlyShareLinks() {
  var cfg = window.IglooPreferences || {}
  var value = cfg.shareEmbedFriendlyLinks
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value !== 0
  if (typeof value === 'string') {
    var s = value.toLowerCase()
    return s === 'true' || s === '1' || s === 'yes' || s === 'on'
  }
  return false
}

var csrfToken = (document.querySelector('meta[name="csrf-token"]') || {}).content || ''

export function t(key, fallback) {
  var cfg = window.IglooI18n || {}
  var messages = cfg.messages || {}
  var value = messages[key]
  return value == null || value === '' ? String(fallback || key || '') : String(value)
}

export function tf(key, fallback) {
  var out = t(key, fallback)
  for (var i = 2; i < arguments.length; i++) {
    var idx = i - 1
    var value = String(arguments[i])
    out = out
      .replace(new RegExp('%' + idx + '\\$d', 'g'), value)
      .replace(new RegExp('%' + idx + '\\$s', 'g'), value)
  }
  return out
}

export function apiFetch(url, options) {
  var opts = Object.assign({ credentials: 'same-origin', headers: {} }, options || {})
  opts.headers = Object.assign({}, opts.headers || {})
  if (csrfToken) opts.headers['X-CSRF-Token'] = csrfToken
  if (opts.body != null && !opts.headers['Content-Type']) opts.headers['Content-Type'] = 'application/json'
  return fetch(url, opts).then(function (response) {
    return response.json().catch(function () { return null }).then(function (payload) {
      if (response.status === 401) {
        window.location.href = '/login'
        throw new Error(t('error_unauthorized', 'Unauthorized'))
      }
      if (!response.ok) {
        var err = new Error(tf('error_http_status', 'HTTP %1$d', response.status))
        err.payload = payload
        throw err
      }
      return payload
    })
  })
}

export function showToast(message) {
  if (window.MpaSiteBase && typeof window.MpaSiteBase.showToast === 'function') {
    window.MpaSiteBase.showToast(message)
    return
  }
  console.log(String(message || ''))
}

export function copyText(text) {
  if (window.MpaSiteBase && typeof window.MpaSiteBase.copyText === 'function') {
    return window.MpaSiteBase.copyText(text)
  }
  return navigator.clipboard
    ? navigator.clipboard.writeText(text)
    : Promise.reject(new Error(t('error_clipboard_unavailable', 'Clipboard unavailable')))
}

export function askConfirm(opts) {
  if (window.MpaSiteBase && typeof window.MpaSiteBase.askConfirm === 'function') {
    return window.MpaSiteBase.askConfirm(opts)
  }
  var body = String((opts && opts.body) || (opts && opts.title) || t('confirm_default_title', 'Confirm'))
  return Promise.resolve(window.confirm(body))
}

// Static SVG icon markup — getFeedActionIconSvg returns hardcoded SVG strings
// with no user input. Used by syncFeedActionIcons and state sync functions.
export function getFeedActionIconSvg(kind, active) {
  if (kind === 'share') return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8"/><polyline points="16 6 12 2 8 6"/><line x1="12" y1="2" x2="12" y2="15"/></svg>'
  if (kind === 'heart') {
    var fill = active ? 'currentColor' : 'none'
    return '<svg width="20" height="20" viewBox="0 0 24 24" fill="' + fill + '" stroke="currentColor" stroke-width="2"><path d="M12 21s-6.7-4.35-9.2-8.14C.69 9.63 1.22 5.5 4.56 4.02 6.8 3.03 9.2 3.69 10.72 5.55L12 7.12l1.28-1.57c1.52-1.86 3.92-2.52 6.16-1.53 3.34 1.48 3.87 5.61 1.76 8.84C18.7 16.65 12 21 12 21z"/></svg>'
  }
  if (kind === 'bookmark') {
    var bfill = active ? 'currentColor' : 'none'
    return '<svg width="20" height="20" viewBox="0 0 24 24" fill="' + bfill + '" stroke="currentColor" stroke-width="2"><path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/></svg>'
  }
  if (kind === 'link' || kind === 'xlogo') return '<svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor"><path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-4.714-6.231-5.401 6.231H2.746l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>'
  return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l2.12-2.12a5 5 0 0 0-7.07-7.07L11.38 5.5"/><path d="M14 11a5 5 0 0 0-7.54-.54L4.34 12.58a5 5 0 0 0 7.07 7.07l1.41-1.41"/></svg>'
}

// setSvgContent safely sets static SVG content on an element using a template.
// All SVG comes from getFeedActionIconSvg — no user input.
export function setSvgContent(el, svgString) {
  el.replaceChildren()
  var tmp = document.createElement('template')
  tmp.innerHTML = svgString // nosec: static SVG from getFeedActionIconSvg
  el.appendChild(tmp.content)
}

// makeDraggableSeekbar — click + hold-and-drag seeking for a progress bar.
// bar: the progress container element, fill: the fill element, video: the HTMLVideoElement.
export function makeDraggableSeekbar(bar, fill, video) {
  if (!bar || !video) return
  var dragging = false
  var wasPlaying = false

  function seek(clientX) {
    var dur = Number(video.duration || 0)
    if (!(dur > 0)) return
    var rect = bar.getBoundingClientRect()
    if (!(rect.width > 0)) return
    var x = Math.max(0, Math.min(rect.width, clientX - rect.left))
    var pct = x / rect.width
    video.currentTime = pct * dur
    if (fill) fill.style.width = (pct * 100) + '%'
  }

  function onMove(e) {
    if (!dragging) return
    e.preventDefault()
    var clientX = e.touches ? e.touches[0].clientX : e.clientX
    seek(clientX)
  }

  function onUp() {
    if (!dragging) return
    dragging = false
    if (wasPlaying) {
      if (video.seeking) {
        video.addEventListener('seeked', function resume() {
          video.removeEventListener('seeked', resume)
          video.play().catch(function () {})
        })
      } else {
        video.play().catch(function () {})
      }
    }
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    document.removeEventListener('touchmove', onMove)
    document.removeEventListener('touchend', onUp)
  }

  function onDown(e) {
    e.preventDefault()
    e.stopPropagation()
    dragging = true
    wasPlaying = !video.paused
    video.pause()
    var clientX = e.touches ? e.touches[0].clientX : e.clientX
    seek(clientX)
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    document.addEventListener('touchmove', onMove, { passive: false })
    document.addEventListener('touchend', onUp)
  }

  bar.addEventListener('mousedown', onDown)
  bar.addEventListener('touchstart', onDown, { passive: false })
  bar.addEventListener('click', function (e) { e.stopPropagation() })
}

export function syncFeedActionIcons(scope) {
  var root = scope || document
  root.querySelectorAll('.feed-action-btn[data-feed-action="share"], .feed-action-btn[data-feed-overlay-action="share"]').forEach(function (btn) {
    setSvgContent(btn, getFeedActionIconSvg('share'))
  })
  root.querySelectorAll('.feed-action-btn[data-feed-action="openx"], .feed-action-btn[data-feed-overlay-action="openx"]').forEach(function (btn) {
    setSvgContent(btn, getFeedActionIconSvg('link'))
  })
}

export function stateBool(root, key) {
  if (!root) return false
  return String(root.dataset[key] || '') === '1'
}

export function setStateBool(root, key, value) {
  if (!root) return
  root.dataset[key] = value ? '1' : '0'
}

export function itemRootFromNode(node) {
  return node && node.closest ? node.closest('[data-feed-item]') : null
}

// parseAppDate normalises server timestamp values to a Date. Accepts:
//   - a Number (unix-millis — the post-2026 server wire shape)
//   - a numeric string holding unix-millis
//   - legacy "YYYY-MM-DD HH:MM:SS" strings
//   - anything else Date.parse accepts
// Returns null when no signal is parseable.
export function parseAppDate(raw) {
  if (raw === null || raw === undefined || raw === '' || raw === 0) return null
  if (typeof raw === 'number' && Number.isFinite(raw)) {
    return raw > 0 ? new Date(raw) : null
  }
  var text = String(raw).trim()
  if (!text) return null
  if (/^-?\d+$/.test(text)) {
    var n = Number(text)
    return n > 0 ? new Date(n) : null
  }
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/.test(text)) {
    var clean = text.replace(/\s+[+-]\d{4}\s+\w+$/, '')
    var t = Date.parse(clean.replace(' ', 'T') + 'Z')
    if (Number.isFinite(t)) return new Date(t)
  }
  var t2 = Date.parse(text)
  if (Number.isFinite(t2)) return new Date(t2)
  return null
}

// formatRelative returns "5m ago" / "3h ago" / "2d ago" etc.
export function formatRelative(raw) {
  var d = parseAppDate(raw)
  if (!d) return String(raw || '')
  var sec = Math.round((Date.now() - d.getTime()) / 1000)
  var abs = Math.abs(sec)
  var future = sec < 0
  if (abs < 60) return tf(future ? 'time_seconds_from_now' : 'time_seconds_ago', future ? '%1$ds from now' : '%1$ds ago', abs)
  var min = Math.round(abs / 60)
  if (min < 60) return tf(future ? 'time_minutes_from_now' : 'time_minutes_ago', future ? '%1$dm from now' : '%1$dm ago', min)
  var hrs = Math.round(min / 60)
  if (hrs < 24) return tf(future ? 'time_hours_from_now' : 'time_hours_ago', future ? '%1$dh from now' : '%1$dh ago', hrs)
  var days = Math.round(hrs / 24)
  if (days < 30) return tf(future ? 'time_days_from_now' : 'time_days_ago', future ? '%1$dd from now' : '%1$dd ago', days)
  var weeks = Math.round(days / 7)
  if (weeks < 8) return tf(future ? 'time_weeks_from_now' : 'time_weeks_ago', future ? '%1$dw from now' : '%1$dw ago', weeks)
  var months = Math.round(days / 30)
  if (months < 12) return tf(future ? 'time_months_from_now' : 'time_months_ago', future ? '%1$dmo from now' : '%1$dmo ago', months)
  return tf(future ? 'time_years_from_now' : 'time_years_ago', future ? '%1$dy from now' : '%1$dy ago', Math.round(days / 365))
}

// formatAbsolute returns the locale-formatted full date-time.
export function formatAbsolute(raw) {
  var d = parseAppDate(raw)
  if (!d) return String(raw || '')
  try { return d.toLocaleString() } catch (_) { return String(raw || '') }
}

// formatVideoTime returns m:ss for a duration in seconds.
function formatVideoTime(s) {
  if (!isFinite(s) || s < 0) return '0:00'
  var m = Math.floor(s / 60)
  var sec = Math.floor(s % 60)
  return m + ':' + (sec < 10 ? '0' : '') + sec
}

// attachSeekTooltip wires a hover tooltip onto a seekbar element. The tooltip
// shows m:ss at the cursor's position and follows the mouse. Idempotent —
// safe to call multiple times on the same bar.
export function attachSeekTooltip(progress, video) {
  if (!progress || !video) return
  if (progress.dataset.tooltipBound === '1') return
  progress.dataset.tooltipBound = '1'

  var tip = document.createElement('div')
  tip.className = 'feed-video-progress-tooltip'
  tip.style.display = 'none'
  progress.appendChild(tip)

  function update(clientX) {
    var dur = Number(video.duration || 0)
    if (!(dur > 0)) { tip.style.display = 'none'; return }
    var rect = progress.getBoundingClientRect()
    if (!(rect.width > 0)) return
    var x = Math.max(0, Math.min(rect.width, clientX - rect.left))
    var pct = x / rect.width
    tip.textContent = formatVideoTime(pct * dur)
    tip.style.left = x + 'px'
    tip.style.display = 'block'
  }

  progress.addEventListener('mouseenter', function (e) { update(e.clientX) })
  progress.addEventListener('mousemove', function (e) { update(e.clientX) })
  progress.addEventListener('mouseleave', function () { tip.style.display = 'none' })
}
