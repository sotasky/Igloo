import { apiFetch, t } from '../utils.js'

export function initSubtitles(video, videoId, subtitlesBtn, subtitlesMenu, subtitlesMenuWrap, closeAllPlayerMenus, togglePopupMenu, closePopupMenu) {
  if (!videoId) return

  let currentTrackId = 'off'

  function trackSrc(trackId) {
    const qid = String(trackId || '').trim()
    const base = '/api/media/subtitle/' + encodeURIComponent(videoId)
    if (!qid) return base
    return base + '?track=' + encodeURIComponent(qid)
  }

  function ensureTrackElement(track) {
    const trackId = String(track && track.track_id || '').trim()
    if (!trackId) return null
    const esc = (window.CSS && typeof window.CSS.escape === 'function')
      ? window.CSS.escape(trackId)
      : trackId.replace(/["\\]/g, '\\$&')
    let el = video.querySelector('track[data-track-id="' + esc + '"]')
    if (el) return el
    el = document.createElement('track')
    el.setAttribute('data-track-id', trackId)
    el.kind = String(track.kind || 'subtitles')
    el.label = String(track.label || t('player_subtitles', 'Subtitles'))
    el.srclang = String(track.srclang || 'en')
    el.src = trackSrc(trackId)
    video.appendChild(el)
    return el
  }

  function applySelection(trackId) {
    currentTrackId = String(trackId || 'off')
    const textTracks = video.textTracks || []
    for (let i = 0; i < textTracks.length; i += 1) {
      try { textTracks[i].mode = 'disabled' } catch (_) {}
    }
    if (currentTrackId !== 'off') {
      let targetIndex = -1
      const trackEls = video.querySelectorAll('track')
      for (let i = 0; i < trackEls.length; i += 1) {
        if (String(trackEls[i].getAttribute('data-track-id') || '') === currentTrackId) {
          targetIndex = i
          break
        }
      }
      if (targetIndex >= 0 && textTracks[targetIndex]) {
        try { textTracks[targetIndex].mode = 'showing' } catch (_) {}
      }
    }
    renderMenu(currentTrackId)
  }

  let cachedTracks = []

  function renderMenu(activeTrackId, tracks) {
    if (!subtitlesMenu || !subtitlesBtn) return
    const list = Array.isArray(tracks) ? tracks : cachedTracks
    if (Array.isArray(tracks)) cachedTracks = tracks.slice()
    const active = String(activeTrackId || currentTrackId || 'off')

    // Build menu buttons via DOM to avoid innerHTML
    subtitlesMenu.textContent = ''

    function addOption(label, value) {
      const isActive = value === active
      const btn = document.createElement('button')
      btn.type = 'button'
      btn.className = 'mc-speed-option' + (isActive ? ' is-active' : '')
      btn.setAttribute('role', 'menuitemradio')
      btn.setAttribute('aria-checked', isActive ? 'true' : 'false')
      btn.setAttribute('data-subtitle-track', value)
      btn.textContent = label
      subtitlesMenu.appendChild(btn)
    }

    addOption(t('option_off', 'Off'), 'off')
    list.forEach(function (track) {
      const id = String(track && track.track_id || '').trim()
      if (!id) return
      const label = String(track.label || track.srclang || t('player_subtitles', 'Subtitles'))
      const suffix = track.is_auto ? ' (auto)' : ''
      addOption(label + suffix, id)
    })

    subtitlesBtn.title = active === 'off' ? t('player_subtitles_off', 'Subtitles (Off)') : t('player_subtitles_on', 'Subtitles (On)')
    subtitlesBtn.setAttribute('aria-label', subtitlesBtn.title)
    subtitlesBtn.classList.toggle('active', active !== 'off')
    if (subtitlesMenuWrap) subtitlesMenuWrap.classList.toggle('hidden', list.length === 0)
  }

  const hasExternalUi = !!(subtitlesMenuWrap && subtitlesBtn && subtitlesMenu)
  if (hasExternalUi) {
    renderMenu('off', [])
    subtitlesBtn.addEventListener('click', function (event) {
      event.preventDefault()
      closeAllPlayerMenus(subtitlesMenu)
      togglePopupMenu(subtitlesMenu, subtitlesBtn)
    })
    subtitlesMenu.addEventListener('click', function (event) {
      const btn = event.target && event.target.closest ? event.target.closest('[data-subtitle-track]') : null
      if (!btn) return
      event.preventDefault()
      const trackId = String(btn.getAttribute('data-subtitle-track') || 'off')
      applySelection(trackId)
      closePopupMenu(subtitlesMenu, subtitlesBtn)
    })
  }

  apiFetch('/api/videos/' + encodeURIComponent(videoId) + '/subtitles')
    .then(function (payload) {
      const tracks = Array.isArray(payload && payload.tracks) ? payload.tracks : []
      tracks.forEach(function (track) { ensureTrackElement(track) })
      currentTrackId = 'off'
      if (hasExternalUi) {
        renderMenu('off', tracks)
      }
      applySelection('off')
    })
    .catch(function () {
      if (hasExternalUi && subtitlesMenuWrap) subtitlesMenuWrap.classList.add('hidden')
    })
}
