import { apiFetch, showToast, copyText, escapeHtml, askConfirm, formatRelative, formatAbsolute, t, tf, toFxTwitterUrl } from '../utils.js'
import { openBookmarkMenu, closeBookmarkMenu, isBookmarkMenuOpen } from '../bookmark-menu.js'
import { initSponsorBlock } from './sponsorblock.js'
import { initPreviewHover } from './preview.js'
import { initProgress } from './progress.js'

const doc = document
const root = doc.getElementById('player-root')
const video = doc.getElementById('mpa-player')
if (root && video) {
  const videoId = (root.getAttribute('data-video-id') || '').trim()
  const nextUrl = (root.getAttribute('data-next-url') || '').trim()
  const channelId = (root.getAttribute('data-channel-id') || '').trim()
  const channelPlatform = (root.getAttribute('data-channel-platform') || '').trim().toLowerCase()
  const playerWrapper = root.querySelector('.player-wrapper')

  const shareBtn = doc.getElementById('player-share-btn')
  const bookmarkBtn = doc.getElementById('player-bookmark-btn')
  const autoplayNextBtn = doc.getElementById('player-autoplay-next-btn')
  const seekBack10Btn = doc.getElementById('player-seek-back-10-btn')
  const seekForward10Btn = doc.getElementById('player-seek-forward-10-btn')
  const speedMenuWrap = doc.getElementById('player-speed-menu-wrap')
  const speedMenuBtn = doc.getElementById('player-speed-menu-btn')
  const speedMenu = doc.getElementById('player-speed-menu')
  const fullscreenBtn = doc.getElementById('player-fullscreen-btn')
  // Note: no custom subtitle menu in the template — media-chrome's
  // built-in <media-captions-button> handles CC when tracks exist.
  const deleteBtn = doc.getElementById('player-delete-btn')
  const channelUnsubBtn = doc.getElementById('player-channel-unsub-btn')
  const channelSubBtn = doc.getElementById('player-channel-sub-btn')

  const descCard = doc.getElementById('player-description-card')
  const descText = doc.getElementById('player-description-text')
  const descFade = doc.getElementById('player-description-fade')
  const descToggle = doc.getElementById('player-description-toggle')
  const playerDateEl = doc.getElementById('player-video-date')

  const AUTOPLAY_NEXT_KEY = 'playerAutoplayNextV1'
  const YOUTUBE_DEFAULT_RATE_KEY = 'youtube_default_playback_rate'
  const YOUTUBE_LEGACY_RATE_KEY = 'youtube_playback_rate'
  let autoplayNext = false

  // --- Helpers ---

  function q(sel, rootEl) {
    return (rootEl || doc).querySelector(sel)
  }

  function formatCompactNumber(n) {
    if (n == null) return ''
    n = Number(n)
    if (isNaN(n)) return ''
    if (n < 1000) return String(n)
    if (n < 1000000) {
      var k = n / 1000
      return (k >= 100 ? Math.round(k) : k.toFixed(1).replace(/\.0$/, '')) + 'K'
    }
    var m = n / 1000000
    return (m >= 100 ? Math.round(m) : m.toFixed(1).replace(/\.0$/, '')) + 'M'
  }

  function parseTimestampToken(token) {
    const s = String(token || '').trim()
    if (!s) return null
    const parts = s.split(':').map(function (n) { return parseInt(n, 10) })
    if (!parts.length || parts.some(function (n) { return !Number.isFinite(n) })) return null
    if (parts.length === 2) return Math.max(0, parts[0] * 60 + parts[1])
    if (parts.length === 3) return Math.max(0, parts[0] * 3600 + parts[1] * 60 + parts[2])
    return null
  }

  function urlSafeHtml(text) {
    return escapeHtml(text).replace(/\n/g, '<br>')
  }

  // renderRichText: timestamps become seek buttons, URLs become links.
  // All dynamic values are passed through escapeHtml — safe for innerHTML.
  function renderRichText(text) {
    const raw = String(text || '')
    if (!raw) return ''
    const timestampRe = /\b(?:(\d{1,2}):)?(\d{1,2}):(\d{2})\b/g
    const urlRe = /\bhttps?:\/\/[^\s<]+/gi
    const matches = []
    let m
    while ((m = timestampRe.exec(raw))) {
      const token = m[0]
      const seconds = parseTimestampToken(token)
      if (seconds == null) continue
      matches.push({
        start: m.index,
        end: m.index + token.length,
        html: '<button type="button" class="inline-seek-link" data-seek-seconds="' + seconds + '">' + escapeHtml(token) + '</button>',
      })
    }
    while ((m = urlRe.exec(raw))) {
      const token = m[0]
      matches.push({
        start: m.index,
        end: m.index + token.length,
        html: '<a class="inline-rich-link" href="' + escapeHtml(token) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(token) + '</a>',
      })
    }
    matches.sort(function (a, b) {
      if (a.start !== b.start) return a.start - b.start
      return (b.end - b.start) - (a.end - a.start)
    })
    const filtered = []
    let cursor = -1
    matches.forEach(function (item) {
      if (item.start < cursor) return
      filtered.push(item)
      cursor = item.end
    })
    let out = ''
    let pos = 0
    filtered.forEach(function (item) {
      out += urlSafeHtml(raw.slice(pos, item.start))
      out += item.html
      pos = item.end
    })
    out += urlSafeHtml(raw.slice(pos))
    return out
  }

  function seekTo(seconds) {
    const target = Math.max(0, Number(seconds) || 0)
    try {
      video.currentTime = target
      video.play().catch(function () {})
    } catch (_) {}
  }

  function scrollToPlayerTop() {
    const target = playerWrapper || root
    if (!target || typeof target.scrollIntoView !== 'function') return
    try {
      target.scrollIntoView({ behavior: 'smooth', block: 'start' })
    } catch (_) {
      target.scrollIntoView()
    }
  }

  function seekRelative(deltaSeconds) {
    const delta = Number(deltaSeconds) || 0
    const dur = Number(video.duration || 0)
    const cur = Number(video.currentTime || 0)
    let next = Math.max(0, cur + delta)
    if (dur > 0) next = Math.min(dur, next)
    seekTo(next)
  }

  // --- Popup menus ---

  function closePopupMenu(menu, btn) {
    if (!menu) return
    menu.classList.add('hidden')
    if (btn) btn.setAttribute('aria-expanded', 'false')
  }

  function openPopupMenu(menu, btn) {
    if (!menu) return
    menu.classList.remove('hidden')
    if (btn) btn.setAttribute('aria-expanded', 'true')
  }

  function closeAllPlayerMenus(except) {
    if (speedMenu && speedMenu !== except) closePopupMenu(speedMenu, speedMenuBtn)
  }

  function setupPlayerControlsVisibility() {
    var controller = doc.getElementById('main-media-controller')
    if (!controller || !playerWrapper) return null

    var pointerInside = false
    var keyboardNavigation = false
    var hideTimer = 0
    var readyAttr = 'data-player-controls-ready'
    var visibleAttr = 'data-player-controls-visible'

    function menuOpen() {
      return !!(speedMenu && !speedMenu.classList.contains('hidden'))
    }

    function dispatchVisibilityChange(visible) {
      controller.dispatchEvent(new CustomEvent('playercontrolsvisibilitychange', {
        bubbles: true,
        detail: { visible: visible },
      }))
    }

    function setVisible(visible) {
      var current = controller.getAttribute(visibleAttr) === '1'
      if (current === visible && controller.hasAttribute(readyAttr)) return
      controller.setAttribute(readyAttr, '1')
      if (visible) {
        controller.setAttribute(visibleAttr, '1')
        controller.removeAttribute('userinactive')
      } else {
        controller.setAttribute(visibleAttr, '0')
        controller.setAttribute('userinactive', '')
      }
      dispatchVisibilityChange(visible)
    }

    function clearHideTimer() {
      if (!hideTimer) return
      window.clearTimeout(hideTimer)
      hideTimer = 0
    }

    function scheduleHide() {
      clearHideTimer()
      hideTimer = window.setTimeout(function () {
        hideTimer = 0
        if (!pointerInside && !menuOpen()) setVisible(false)
      }, 80)
    }

    playerWrapper.addEventListener('pointerenter', function () {
      pointerInside = true
      keyboardNavigation = false
      clearHideTimer()
      setVisible(true)
    })
    playerWrapper.addEventListener('pointermove', function () {
      pointerInside = true
      keyboardNavigation = false
      clearHideTimer()
      setVisible(true)
    }, { passive: true })
    playerWrapper.addEventListener('pointerleave', function () {
      pointerInside = false
      scheduleHide()
    })
    playerWrapper.addEventListener('pointerdown', function () {
      keyboardNavigation = false
    }, true)
    playerWrapper.addEventListener('focusin', function () {
      if (keyboardNavigation) setVisible(true)
    })
    playerWrapper.addEventListener('focusout', function () {
      if (!pointerInside) scheduleHide()
    })
    if (speedMenu) {
      speedMenu.addEventListener('pointerenter', function () {
        pointerInside = true
        clearHideTimer()
        setVisible(true)
      })
      speedMenu.addEventListener('pointerleave', function () {
        pointerInside = false
        scheduleHide()
      })
    }
    doc.addEventListener('keydown', function (event) {
      if (event.key === 'Tab') keyboardNavigation = true
    }, true)

    setVisible(false)
    return {
      isVisible: function () { return controller.getAttribute(visibleAttr) === '1' },
    }
  }

  function controllerControlsVisible(controller) {
    if (!controller) return false
    if (controller.hasAttribute('data-player-controls-ready')) {
      return controller.getAttribute('data-player-controls-visible') === '1'
    }
    return !controller.hasAttribute('userinactive')
  }

  // --- Bookmark button state ---

  function updateBookmarkBtn(bookmarked) {
    if (!bookmarkBtn) return
    bookmarkBtn.classList.toggle('active', !!bookmarked)
    root.setAttribute('data-bookmarked', bookmarked ? '1' : '0')
    const svg = q('svg', bookmarkBtn)
    if (svg) svg.setAttribute('fill', bookmarked ? 'currentColor' : 'none')
    bookmarkBtn.title = bookmarked ? t('action_remove_bookmark', 'Remove bookmark') : t('action_bookmark', 'Bookmark')
    bookmarkBtn.setAttribute('aria-label', bookmarkBtn.title)
  }

  // --- Autoplay next ---

  function loadAutoplayNextPref() {
    try {
      autoplayNext = localStorage.getItem(AUTOPLAY_NEXT_KEY) === '1'
    } catch (_) {
      autoplayNext = false
    }
    if (autoplayNextBtn) {
      autoplayNextBtn.classList.toggle('active', autoplayNext)
      autoplayNextBtn.title = tf('player_autoplay_next_state', 'Auto-play next: %1$s', autoplayNext ? t('state_on', 'ON') : t('state_off', 'OFF'))
      autoplayNextBtn.setAttribute('aria-label', autoplayNextBtn.title)
    }
  }

  function saveAutoplayNextPref() {
    try { localStorage.setItem(AUTOPLAY_NEXT_KEY, autoplayNext ? '1' : '0') } catch (_) {}
  }

  // --- Speed menu ---

  function formatRateLabel(rate) {
    const n = Number(rate) || 1
    if (Math.abs(n - Math.round(n)) < 0.0001) return Math.round(n) + 'x'
    return n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '') + 'x'
  }

  function parsePlaybackRateValue(value) {
    const n = Number(value)
    if (!Number.isFinite(n) || n <= 0) return null
    return Math.max(0.25, Math.min(3, n))
  }

  function loadPreferredPlaybackRate() {
    let raw = ''
    try { raw = localStorage.getItem(YOUTUBE_DEFAULT_RATE_KEY) || localStorage.getItem(YOUTUBE_LEGACY_RATE_KEY) || '' } catch (_) { raw = '' }
    return parsePlaybackRateValue(raw) || 1
  }

  function persistPlaybackRate(rate) {
    const parsed = parsePlaybackRateValue(rate)
    if (!parsed) return
    const value = String(parsed)
    try { localStorage.setItem(YOUTUBE_DEFAULT_RATE_KEY, value) } catch (_) {}
    try { localStorage.setItem(YOUTUBE_LEGACY_RATE_KEY, value) } catch (_) {}
  }

  function applyPreferredPlaybackRate() {
    const preferred = loadPreferredPlaybackRate()
    try { video.defaultPlaybackRate = preferred } catch (_) {}
    try { video.playbackRate = preferred } catch (_) {}
    return preferred
  }

  function renderSpeedMenu() {
    if (!speedMenu) return
    const rates = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3]
    const current = Number(video.playbackRate || 1)
    speedMenu.textContent = ''
    rates.forEach(function (rate) {
      const active = Math.abs(current - rate) < 0.001
      const btn = doc.createElement('button')
      btn.type = 'button'
      btn.className = 'mc-speed-option' + (active ? ' is-active' : '')
      btn.setAttribute('role', 'menuitemradio')
      btn.setAttribute('aria-checked', active ? 'true' : 'false')
      btn.setAttribute('data-rate', String(rate))
      btn.textContent = formatRateLabel(rate)
      speedMenu.appendChild(btn)
    })
    if (speedMenuBtn) {
      speedMenuBtn.textContent = formatRateLabel(current)
      speedMenuBtn.title = tf('player_playback_speed_value', 'Playback speed (%1$s)', formatRateLabel(current))
      speedMenuBtn.setAttribute('aria-label', speedMenuBtn.title)
    }
  }

  function setupSpeedMenu() {
    if (!speedMenuWrap || !speedMenuBtn || !speedMenu) return
    applyPreferredPlaybackRate()
    renderSpeedMenu()
    // Portal to body to escape overflow:hidden in media-controller.
    // Must add inline styles since CSS ancestor selectors no longer match.
    doc.body.appendChild(speedMenu)
    speedMenu.style.cssText = 'position:fixed; z-index:99999; margin:0; display:grid; gap:0.12rem; min-width:92px; max-height:min(50vh,320px); overflow-y:auto; padding:0.3rem; border-radius:10px; border:1px solid var(--border-color); background:var(--bg-glass); box-shadow:var(--shadow); backdrop-filter:var(--glass-blur);'
    function repositionSpeedMenu() {
      var rect = speedMenuBtn.getBoundingClientRect()
      speedMenu.style.bottom = (window.innerHeight - rect.top + 6) + 'px'
      speedMenu.style.right = (window.innerWidth - rect.right) + 'px'
      speedMenu.style.left = 'auto'
      speedMenu.style.top = 'auto'
    }
    speedMenuBtn.addEventListener('click', function (event) {
      event.preventDefault()
      event.stopImmediatePropagation()
      closeAllPlayerMenus(speedMenu)
      var isHidden = speedMenu.classList.contains('hidden')
      if (isHidden) {
        repositionSpeedMenu()
        openPopupMenu(speedMenu, speedMenuBtn)
      } else {
        closePopupMenu(speedMenu, speedMenuBtn)
      }
    })
    speedMenu.addEventListener('click', function (event) {
      const btn = event.target && event.target.closest ? event.target.closest('[data-rate]') : null
      if (!btn) return
      event.preventDefault()
      const rate = Number(btn.getAttribute('data-rate') || 1)
      if (!Number.isFinite(rate) || rate <= 0) return
      try { video.playbackRate = rate } catch (_) {}
      try { video.defaultPlaybackRate = rate } catch (_) {}
      persistPlaybackRate(rate)
      renderSpeedMenu()
      closePopupMenu(speedMenu, speedMenuBtn)
      showToast(tf('player_speed_set_to', 'Speed %1$s', formatRateLabel(rate)))
    })
    video.addEventListener('ratechange', function () {
      const current = parsePlaybackRateValue(video.playbackRate)
      if (current) persistPlaybackRate(current)
      renderSpeedMenu()
    })
    video.addEventListener('loadedmetadata', function () {
      const current = applyPreferredPlaybackRate()
      if (current) renderSpeedMenu()
    })
  }

  // --- Seek buttons ---

  function setupSeekButtons() {
    if (seekBack10Btn) {
      seekBack10Btn.addEventListener('click', function () { seekRelative(-10) })
    }
    if (seekForward10Btn) {
      seekForward10Btn.addEventListener('click', function () { seekRelative(10) })
    }
  }

  // --- Fullscreen ---

  var playerLayout = root.closest('.player-layout') || root

  function setFullscreenMode(mode) {
    var immersive = mode === 'immersive'
    playerLayout.classList.toggle('fullscreen-immersive', immersive)
    playerLayout.classList.toggle('fullscreen-browse', !immersive)
  }

  function isPlayerLayoutFullscreen() {
    var fsEl = doc.fullscreenElement || doc.webkitFullscreenElement
    return fsEl === playerLayout
  }

  function toggleFullscreen() {
    var isFs = doc.fullscreenElement || doc.webkitFullscreenElement
    if (!isFs) {
      playerLayout.classList.add('fullscreen-immersive')
      playerLayout.classList.remove('fullscreen-browse')
      playerLayout.scrollTop = 0
      window.scrollTo(0, 0)
      var fsReq = playerLayout.requestFullscreen || playerLayout.webkitRequestFullscreen
      if (fsReq) {
        fsReq.call(playerLayout).catch(function () {
          playerLayout.classList.remove('fullscreen-immersive')
        })
      }
      return
    }
    var fsExit = doc.exitFullscreen || doc.webkitExitFullscreen
    if (fsExit) fsExit.call(doc).catch(function () {})
  }

  function normalizeWheelDeltaY(e) {
    if (typeof e.deltaY === 'number') return e.deltaY
    if (typeof e.wheelDelta === 'number') return -e.wheelDelta
    if (typeof e.detail === 'number') return e.detail * 40
    return 0
  }

  function setupFullscreenButton() {
    if (!fullscreenBtn) return
    fullscreenBtn.addEventListener('click', toggleFullscreen)

    if (video) {
      video.addEventListener('dblclick', function (e) {
        e.preventDefault()
        toggleFullscreen()
      })
    }

    function onFullscreenChange() {
      if (isPlayerLayoutFullscreen()) {
        setFullscreenMode('immersive')
        playerLayout.scrollTop = 0
        if (speedMenu && speedMenu.parentNode !== playerLayout) playerLayout.appendChild(speedMenu)
      } else if (!doc.fullscreenElement && !doc.webkitFullscreenElement) {
        playerLayout.classList.remove('fullscreen-immersive', 'fullscreen-browse')
        playerLayout.scrollTop = 0
        if (speedMenu && speedMenu.parentNode !== doc.body) doc.body.appendChild(speedMenu)
      }
    }
    doc.addEventListener('fullscreenchange', onFullscreenChange)
    doc.addEventListener('webkitfullscreenchange', onFullscreenChange)

    function handleFullscreenWheel(e) {
      if (!isPlayerLayoutFullscreen()) return
      var deltaY = normalizeWheelDeltaY(e)
      if (Math.abs(deltaY) < 0.01) return

      if (playerLayout.classList.contains('fullscreen-immersive')) {
        if (deltaY <= 0) return
        e.preventDefault()
        e.stopImmediatePropagation()
        e.stopPropagation()
        setFullscreenMode('browse')
        requestAnimationFrame(function () {
          playerLayout.scrollTop = 0
        })
        return
      }

      if (deltaY < 0 && (playerLayout.scrollTop || 0) <= 0) {
        e.preventDefault()
        e.stopImmediatePropagation()
        e.stopPropagation()
        setFullscreenMode('immersive')
        return
      }

      e.preventDefault()
      e.stopImmediatePropagation()
      e.stopPropagation()
      playerLayout.scrollTop = Math.max(0, (playerLayout.scrollTop || 0) + deltaY)
    }

    doc.addEventListener('wheel', handleFullscreenWheel, { passive: false, capture: true })
    doc.addEventListener('mousewheel', handleFullscreenWheel, { passive: false, capture: true })
    doc.addEventListener('DOMMouseScroll', handleFullscreenWheel, { passive: false, capture: true })
  }

  // --- Player actions ---

  function setupPlayerActions() {
    loadAutoplayNextPref()
    updateBookmarkBtn(String(root.getAttribute('data-bookmarked') || '') === '1')

    if (shareBtn) {
      shareBtn.addEventListener('click', function () {
        let url = String(root.getAttribute('data-original-url') || '').trim()
        if (!url) url = window.location.href

        url = toFxTwitterUrl(url)

        copyText(url).then(function () {
          showToast(t('toast_link_copied', 'Link copied'))
        }).catch(function () {
          showToast(t('error_copy_link_failed', 'Failed to copy link'))
        })
      })
    }

    if (bookmarkBtn && videoId) {
      bookmarkBtn.addEventListener('click', function () {
        if (isBookmarkMenuOpen()) { closeBookmarkMenu(); return }
        var idOpts = {}
        if (channelPlatform === 'twitter') idOpts.tweetId = videoId
        else if (channelPlatform === 'tiktok') idOpts.tiktokId = videoId
        else if (channelPlatform === 'instagram') idOpts.instagramId = videoId
        else idOpts.youtubeId = videoId
        var desc = String((descText && descText.textContent) || '').trim()
        openBookmarkMenu(bookmarkBtn, root, Object.assign(idOpts, {
          bodyText: desc,
          titleFallback: desc,
          onStateChange: function (_r, isBm) { updateBookmarkBtn(isBm) },
        }))
      })
    }

    if (deleteBtn && videoId) {
      deleteBtn.addEventListener('click', doDeleteVideo)
    }

    if (autoplayNextBtn) {
      autoplayNextBtn.addEventListener('click', function () {
        autoplayNext = !autoplayNext
        saveAutoplayNextPref()
        loadAutoplayNextPref()
        showToast(tf('player_autoplay_next_state', 'Auto-play next: %1$s', autoplayNext ? t('state_on', 'ON') : t('state_off', 'OFF')))
      })
    }
  }

  function doDeleteVideo() {
    if (!deleteBtn || !videoId || deleteBtn.disabled) return
    var run = function () {
      deleteBtn.disabled = true
      apiFetch('/api/videos/' + encodeURIComponent(videoId), { method: 'DELETE' })
        .then(function (payload) {
          if (payload && payload.success === false) throw new Error('delete failed')
          showToast(t('player_video_deleted', 'Video deleted'))
          if (nextUrl) {
            window.location.assign(nextUrl.replace(/\?autoplay=1$/, ''))
          } else if (channelId) {
            window.location.assign('/channels/' + encodeURIComponent(channelId))
          } else {
            window.location.assign('/videos')
          }
        })
        .catch(function () {
          showToast(t('player_delete_failed', 'Delete failed'))
        })
        .finally(function () {
          deleteBtn.disabled = false
        })
    }
    askConfirm({
      title: t('player_delete_video_title', 'Delete video'),
      body: t('player_delete_video_body', 'Delete this video?'),
      confirmLabel: t('action_delete', 'Delete'),
      cancelLabel: t('action_cancel', 'Cancel'),
      danger: true,
    })
      .then(function (confirmed) { if (confirmed) run() })
  }

  // --- Channel inline actions ---

  function setupChannelInlineActions() {
    // Star button is HTMX-driven via PlayerStarButton templ component.
    if (channelSubBtn && channelId) {
      channelSubBtn.addEventListener('click', function () {
        if (channelSubBtn.disabled) return
        channelSubBtn.disabled = true
        apiFetch('/api/channels/' + encodeURIComponent(channelId) + '/subscribe', { method: 'POST' })
          .then(function () {
            showToast(t('player_subscribed_to_channel', 'Subscribed to channel'))
            var svg = channelSubBtn.querySelector('svg')
            if (svg) {
              while (svg.firstChild) svg.removeChild(svg.firstChild)
              var ns = 'http://www.w3.org/2000/svg'
              var l1 = doc.createElementNS(ns, 'line')
              l1.setAttribute('x1', '18'); l1.setAttribute('y1', '6')
              l1.setAttribute('x2', '6'); l1.setAttribute('y2', '18')
              var l2 = doc.createElementNS(ns, 'line')
              l2.setAttribute('x1', '6'); l2.setAttribute('y1', '6')
              l2.setAttribute('x2', '18'); l2.setAttribute('y2', '18')
              svg.appendChild(l1)
              svg.appendChild(l2)
            }
            channelSubBtn.title = t('action_unsubscribe_channel', 'Unsubscribe channel')
            channelSubBtn.setAttribute('aria-label', channelSubBtn.title)
          })
          .catch(function () {
            showToast(t('player_subscribe_failed', 'Failed to subscribe'))
          })
          .finally(function () {
            channelSubBtn.disabled = false
          })
      })
    }

    if (channelUnsubBtn && channelId) {
      channelUnsubBtn.addEventListener('click', function () {
        if (channelUnsubBtn.disabled) return
        var channelLink = root.querySelector('.channel-link')
        var displayName = (channelLink && channelLink.textContent || '').trim() || channelId
        var doUnsub = function () {
          channelUnsubBtn.disabled = true
          apiFetch('/api/unsubscribe/' + encodeURIComponent(channelId) + '?delete_files=true', { method: 'DELETE' })
            .then(function (payload) {
              showToast((payload && payload.message) || t('player_channel_unsubscribed', 'Channel unsubscribed'))
              window.location.assign('/channels')
            })
            .catch(function (err) {
              const msg = (err && err.payload && err.payload.error) ? err.payload.error : t('player_unsubscribe_failed', 'Failed to unsubscribe')
              showToast(msg)
            })
            .finally(function () {
              channelUnsubBtn.disabled = false
            })
        }
        askConfirm({
          title: t('action_unsubscribe', 'Unsubscribe'),
          body: tf('player_unsubscribe_channel_confirm_body', 'Remove channel and files for %1$s?', displayName),
          confirmLabel: t('action_remove', 'Remove'),
          cancelLabel: t('action_cancel', 'Cancel'),
          danger: true,
        }).then(function (confirmed) {
          if (confirmed) doUnsub()
        })
      })
    }
  }

  // --- UI: date hover, description, seek links, video stats ---

  function setupPlayerDateHover() {
    if (!playerDateEl) return
    const raw = String(playerDateEl.getAttribute('data-video-date') || '').trim()
    if (!raw) return
    const rel = formatRelative(raw)
    const abs = formatAbsolute(raw)
    playerDateEl.textContent = rel || raw
    playerDateEl.title = abs || raw
    playerDateEl.addEventListener('mouseenter', function () {
      playerDateEl.textContent = abs || raw
    })
    playerDateEl.addEventListener('mouseleave', function () {
      playerDateEl.textContent = rel || raw
    })
  }

  function setupDescriptionBox() {
    if (!descCard || !descText) return
    const raw = descText.textContent || ''
    const clean = String(raw || '').trim()
    if (!clean) {
      descCard.classList.add('is-empty')
      return
    }
    // Description text is server-rendered plain text; renderRichText escapes it
    descText.innerHTML = renderRichText(clean)

    function updateToggleVisibility() {
      const shouldToggle = descText.scrollHeight - descText.clientHeight > 6
      descCard.classList.toggle('has-toggle', shouldToggle)
      if (descToggle) descToggle.classList.toggle('hidden', !shouldToggle)
      if (descFade) descFade.classList.toggle('hidden', !shouldToggle || descText.classList.contains('expanded'))
    }

    if (descToggle) {
      descToggle.addEventListener('click', function () {
        const expanded = descText.classList.toggle('expanded')
        if (descFade) descFade.classList.toggle('hidden', expanded)
        descToggle.textContent = expanded ? t('action_show_less', 'Show less') : t('action_show_more', 'Show more')
      })
    }
    requestAnimationFrame(updateToggleVisibility)
    window.addEventListener('resize', updateToggleVisibility, { passive: true })
  }

  function bindSeekLinks(rootEl) {
    if (!rootEl) return
    rootEl.addEventListener('click', function (event) {
      const btn = event.target && event.target.closest ? event.target.closest('.inline-seek-link') : null
      if (btn) {
        event.preventDefault()
        event.stopPropagation()
        seekTo(btn.getAttribute('data-seek-seconds'))
        scrollToPlayerTop()
      }
    })
  }

  // --- Init ---

  function init() {
    bindSeekLinks(doc)
    setupPlayerActions()
    setupSeekButtons()
    setupFullscreenButton()
    setupSpeedMenu()
    setupPlayerControlsVisibility()
    setupChannelInlineActions()
    setupPlayerDateHover()

    // Populate video stats from data attributes
    doc.querySelectorAll('.player-stat').forEach(function (el) {
      var count = el.getAttribute('data-count')
      var valEl = el.querySelector('.player-stat-value')
      if (valEl && count) valEl.textContent = formatCompactNumber(count)
    })

    setupDescriptionBox()

    // CC toggle — fetch tracks, inject <track>, show button if any exist
    var ccBtn = doc.getElementById('player-cc-btn')
    if (ccBtn && videoId) {
      var ccOn = false
      apiFetch('/api/videos/' + encodeURIComponent(videoId) + '/subtitles')
        .then(function (payload) {
          var tracks = Array.isArray(payload && payload.tracks) ? payload.tracks : []
          if (!tracks.length) return
          var track = tracks.find(function (candidate) { return !(candidate && candidate.is_auto) }) || tracks[0]
          var controller = doc.getElementById('main-media-controller')
          var subtitleOverlay = null
          var subtitleCues = []
          var subtitleTrackUrl = '/api/media/subtitle/' + encodeURIComponent(videoId) + '?track=' + encodeURIComponent(track.track_id || '')
          function readSubtitleOffsetPx(name, fallback) {
            if (!playerWrapper) return fallback
            var raw = window.getComputedStyle(playerWrapper).getPropertyValue(name)
            var value = parseFloat(raw)
            return Number.isFinite(value) ? value : fallback
          }
          function readSubtitleOffset(name, fallback) {
            if (!playerWrapper) return fallback
            var raw = window.getComputedStyle(playerWrapper).getPropertyValue(name).trim()
            var value = parseFloat(raw)
            if (!Number.isFinite(value)) return fallback
            if (raw.endsWith('%')) {
              var rect = playerWrapper.getBoundingClientRect()
              return rect && rect.height > 0 ? rect.height * value / 100 : fallback
            }
            return value
          }
          function controlsSubtitleOffsetPx(isFs) {
            var fallback = readSubtitleOffset(isFs ? '--player-subtitles-offset-fullscreen-controls' : '--player-subtitles-offset-controls', isFs ? 104 : 72)
            if (!controller || !playerWrapper) return fallback
            var bar = controller.querySelector('media-control-bar.dashboard-media-control-bar, media-control-bar, .dashboard-media-control-bar')
            if (!bar || typeof bar.getBoundingClientRect !== 'function') return fallback
            var wrapperRect = playerWrapper.getBoundingClientRect()
            var barRect = bar.getBoundingClientRect()
            if (!(wrapperRect && wrapperRect.height > 0 && barRect && barRect.height > 0)) return fallback
            var gap = readSubtitleOffsetPx(isFs ? '--player-subtitles-controls-gap-fullscreen' : '--player-subtitles-controls-gap', isFs ? 12 : 6)
            var measured = wrapperRect.bottom - barRect.top + gap
            if (!Number.isFinite(measured) || measured <= 0) return fallback
            return Math.max(0, Math.min(wrapperRect.height, measured))
          }
          function subtitleOffsetPx() {
            var isFs = !!(doc.fullscreenElement || doc.webkitFullscreenElement)
            var controlsVisible = controllerControlsVisible(controller)
            if (controlsVisible) return controlsSubtitleOffsetPx(isFs)
            if (isFs) return readSubtitleOffset('--player-subtitles-offset-fullscreen-idle', 52)
            return readSubtitleOffset('--player-subtitles-offset-idle', 36)
          }
          function ensureSubtitleOverlay() {
            if (subtitleOverlay) return subtitleOverlay
            subtitleOverlay = doc.createElement('div')
            subtitleOverlay.className = 'player-subtitle-overlay hidden'
            subtitleOverlay.setAttribute('aria-hidden', 'true')
            playerWrapper.appendChild(subtitleOverlay)
            return subtitleOverlay
          }
          function parseVttTimestamp(raw) {
            var value = String(raw || '').trim().split(/\s+/)[0]
            if (!value) return Number.NaN
            var parts = value.split(':')
            if (parts.length < 2 || parts.length > 3) return Number.NaN
            var secondsPart = parts.pop().replace(',', '.')
            var minutesPart = parts.pop()
            var hoursPart = parts.length ? parts.pop() : '0'
            var hours = Number(hoursPart)
            var minutes = Number(minutesPart)
            var seconds = Number(secondsPart)
            if (!Number.isFinite(hours) || !Number.isFinite(minutes) || !Number.isFinite(seconds)) return Number.NaN
            return hours * 3600 + minutes * 60 + seconds
          }
          function parseVtt(text) {
            var lines = String(text || '').replace(/\r/g, '').split('\n')
            var cues = []
            for (var i = 0; i < lines.length; i++) {
              var line = lines[i].trim()
              if (!line) continue
              if (line === 'WEBVTT' || line.indexOf('Kind:') === 0 || line.indexOf('Language:') === 0) continue
              if (line.indexOf('-->') < 0) continue
              var parts = line.split('-->')
              var start = parseVttTimestamp(parts[0])
              var end = parseVttTimestamp(parts[1])
              if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) continue
              var cueLines = []
              for (i = i + 1; i < lines.length; i++) {
                var cueLine = lines[i]
                if (!cueLine.trim()) break
                cueLines.push(cueLine)
              }
              if (cueLines.length) cues.push({ start: start, end: end, text: cueLines.join('\n') })
            }
            return cues
          }
          function subtitleTextHtml(text) {
            return escapeHtml(sanitizeVttCueText(text)).replace(/\r?\n/g, '<br>')
          }
          function sanitizeVttCueText(text) {
            return decodeVttEntities(
              String(text || '')
                .replace(/<(?:\d{1,2}:)?\d{2}:\d{2}[.,]\d{3}>/g, '')
                .replace(/<\/?(?:c(?:\.[^>\s]+)*|v(?:\s+[^>]*)?|lang(?:\s+[^>]*)?|b|i|u|ruby|rt)>/g, '')
                .replace(/<[^>]+>/g, '')
            ).trim()
          }
          function decodeVttEntities(text) {
            return String(text || '')
              .replace(/&nbsp;|&#160;|&#x0*a0;/gi, ' ')
              .replace(/&amp;/gi, '&')
              .replace(/&lt;/gi, '<')
              .replace(/&gt;/gi, '>')
              .replace(/&quot;/gi, '"')
              .replace(/&apos;|&#39;/gi, "'")
          }
          function updateSubtitleOverlayPosition() {
            var overlay = ensureSubtitleOverlay()
            overlay.style.bottom = subtitleOffsetPx() + 'px'
          }
          function activeSubtitleCues() {
            var time = Number(video.currentTime || 0)
            if (!Number.isFinite(time)) return []
            return subtitleCues.filter(function (cue) {
              return time >= cue.start && time < cue.end
            })
          }
          function renderSubtitleOverlay() {
            var overlay = ensureSubtitleOverlay()
            updateSubtitleOverlayPosition()
            var activeCues = ccOn ? activeSubtitleCues() : []
            if (!activeCues.length) {
              overlay.classList.add('hidden')
              overlay.replaceChildren()
              return
            }
            var html = []
            for (var i = 0; i < activeCues.length; i++) {
              var cue = activeCues[i]
              html.push('<div class="player-subtitle-cue">' + subtitleTextHtml(cue && cue.text) + '</div>')
            }
            overlay.innerHTML = html.join('')
            overlay.classList.remove('hidden')
          }
          if (controller) {
            new MutationObserver(function () {
              renderSubtitleOverlay()
            }).observe(controller, { attributes: true, attributeFilter: ['userinactive', 'data-player-controls-visible'] })
            controller.addEventListener('playercontrolsvisibilitychange', function () {
              requestAnimationFrame(renderSubtitleOverlay)
            })
          }
          video.addEventListener('timeupdate', renderSubtitleOverlay, { passive: true })
          video.addEventListener('seeked', renderSubtitleOverlay)
          video.addEventListener('play', renderSubtitleOverlay)
          video.addEventListener('pause', renderSubtitleOverlay)
          video.addEventListener('loadedmetadata', renderSubtitleOverlay)
          window.addEventListener('resize', function () { renderSubtitleOverlay() }, { passive: true })
          doc.addEventListener('fullscreenchange', function () { requestAnimationFrame(renderSubtitleOverlay) })
          doc.addEventListener('webkitfullscreenchange', function () { requestAnimationFrame(renderSubtitleOverlay) })
          if (playerLayout) {
            playerLayout.addEventListener('scroll', function () { requestAnimationFrame(renderSubtitleOverlay) }, { passive: true })
          }

          ccBtn.classList.remove('hidden')

          // Auto-enable manual subtitle tracks. Auto-generated captions stay
          // available through the CC button but do not appear by default.
          if (!(track && track.is_auto)) {
            ccOn = true
            ccBtn.classList.add('active')
            ccBtn.title = t('player_subtitles_on', 'Subtitles (On)')
          }

          fetch(subtitleTrackUrl, { credentials: 'same-origin' })
            .then(function (resp) {
              if (!resp.ok) throw new Error('subtitle fetch failed')
              return resp.text()
            })
            .then(function (text) {
              subtitleCues = parseVtt(text)
              renderSubtitleOverlay()
            })
            .catch(function () {
              subtitleCues = []
              renderSubtitleOverlay()
            })

          ccBtn.addEventListener('click', function () {
            ccOn = !ccOn
            ccBtn.classList.toggle('active', ccOn)
            ccBtn.title = ccOn ? t('player_subtitles_on', 'Subtitles (On)') : t('player_subtitles', 'Subtitles')
            renderSubtitleOverlay()
          })
        })
        .catch(function () {})
    }

    // Module inits
    initSponsorBlock(video, root)
    initPreviewHover(video, videoId, playerWrapper)
    initProgress(video, videoId, root)

    // Global click: close popup menus
    doc.addEventListener('click', function (event) {
      var path = typeof event.composedPath === 'function' ? event.composedPath() : [event.target]
      var inSpeed = speedMenuWrap && (path.indexOf(speedMenuWrap) >= 0 || (speedMenu && path.indexOf(speedMenu) >= 0))
      if (!inSpeed) closePopupMenu(speedMenu, speedMenuBtn)
    })

    // Keyboard shortcuts — capture phase so we fire BEFORE media-chrome
    doc.addEventListener('keydown', function (event) {
      if (event.key === 'Escape') { closeAllPlayerMenus(null); return }
      if (event.ctrlKey || event.metaKey || event.altKey) return
      var activeEl = doc.activeElement
      if (activeEl && (activeEl.tagName === 'INPUT' || activeEl.tagName === 'TEXTAREA' || activeEl.isContentEditable)) return

      var sc = window.cfShortcuts
      if (sc.match('player.fullscreen', event.key)) {
        event.preventDefault()
        event.stopImmediatePropagation()
        toggleFullscreen()
      } else if (event.key === 'ArrowLeft') {
        event.preventDefault()
        event.stopImmediatePropagation()
        seekRelative(-5)
      } else if (event.key === 'ArrowRight') {
        event.preventDefault()
        event.stopImmediatePropagation()
        seekRelative(5)
      } else if (event.key === 'ArrowUp') {
        event.preventDefault()
        event.stopImmediatePropagation()
        var vol = Math.min(1, (video.volume || 0) + 0.05)
        video.volume = vol
        video.muted = false
      } else if (event.key === 'ArrowDown') {
        event.preventDefault()
        event.stopImmediatePropagation()
        var vol = Math.max(0, (video.volume || 0) - 0.05)
        video.volume = vol
      } else if (sc.match('player.bookmark', event.key) && bookmarkBtn) {
        event.preventDefault()
        bookmarkBtn.click()
      } else if (sc.match('player.share', event.key) && shareBtn) {
        event.preventDefault()
        shareBtn.click()
      } else if (sc.match('player.autoplay', event.key) && autoplayNextBtn) {
        event.preventDefault()
        autoplayNextBtn.click()
      } else if (event.key === 'd' || event.key === 'D') {
        if (deleteBtn && videoId && !deleteBtn.disabled) {
          event.preventDefault()
          doDeleteVideo()
        }
      }
    }, true)
  }

  init()
}
