function formatClock(seconds) {
  const total = Math.max(0, Math.floor(Number(seconds) || 0))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  if (h > 0) return h + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0')
  return m + ':' + String(s).padStart(2, '0')
}

function parsePreviewTrackJson(track, spriteUrl) {
  if (!track || typeof track !== 'object' || track.version !== 1 || !Array.isArray(track.cues)) return []
  const imageUrl = spriteUrl || ''
  return track.cues.map(function (cue) {
    const start = Number(cue.start_ms) / 1000
    const end = Number(cue.end_ms) / 1000
    const coords = [cue.x, cue.y, cue.w, cue.h].map(function (n) { return Number(n) })
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null
    if (coords.length !== 4 || coords.some(function (n) { return !Number.isFinite(n) || n < 0 })) return null
    if (coords[2] <= 0 || coords[3] <= 0 || !imageUrl) return null
    return { start: start, end: end, imageUrl: imageUrl, coords: coords }
  }).filter(Boolean).sort(function (a, b) { return a.start - b.start })
}

function findPreviewCueAtTime(cues, timeSeconds) {
  const list = Array.isArray(cues) ? cues : []
  if (!list.length) return null
  const t = Number(timeSeconds)
  if (!Number.isFinite(t)) return null
  for (let i = 0; i < list.length; i += 1) {
    const cue = list[i]
    if (t >= cue.start && (i === list.length - 1 ? t <= cue.end : t < cue.end)) return cue
  }
  return list[list.length - 1] || null
}

export function initPreviewHover(video, videoId, playerWrapper) {
  if (!playerWrapper || !videoId) return

  let previewOverlay = null
  let previewFrame = null
  let previewImg = null
  let previewTimeEl = null
  let previewCues = []
  let trackLoading = false
  let retryTimer = 0
  let fetchAttempts = 0

  function ensureOverlay() {
    if (previewOverlay) return
    previewOverlay = document.createElement('div')
    previewOverlay.className = 'dashboard-preview-overlay hidden'

    previewFrame = document.createElement('div')
    previewFrame.className = 'dashboard-preview-overlay-frame'
    previewOverlay.appendChild(previewFrame)

    previewTimeEl = document.createElement('div')
    previewTimeEl.className = 'dashboard-preview-overlay-time'
    previewTimeEl.textContent = '0:00'
    previewOverlay.appendChild(previewTimeEl)

    previewImg = document.createElement('img')
    previewImg.className = 'dashboard-preview-overlay-img'
    previewImg.alt = ''
    previewFrame.appendChild(previewImg)

    playerWrapper.appendChild(previewOverlay)
  }

  function hideOverlay() {
    if (previewOverlay) previewOverlay.classList.add('hidden')
  }

  function setOverlay(cue, previewTime, clientX) {
    ensureOverlay()
    if (!previewOverlay || !previewFrame || !previewTimeEl) return
    previewOverlay.classList.remove('hidden')
    previewTimeEl.textContent = formatClock(previewTime)

    const wrapperRect = playerWrapper.getBoundingClientRect()
    const x = Math.max(0, Math.min(wrapperRect.width, Number(clientX) - wrapperRect.left))
    previewOverlay.style.left = x + 'px'
    previewOverlay.style.bottom = '58px'

    if (!cue || !Array.isArray(cue.coords) || cue.coords.length !== 4) {
      previewFrame.style.display = 'none'
      return
    }
    const sx = Number(cue.coords[0]) || 0
    const sy = Number(cue.coords[1]) || 0
    const sw = Math.max(1, Number(cue.coords[2]) || 1)
    const sh = Math.max(1, Number(cue.coords[3]) || 1)
    previewFrame.style.display = ''
    previewFrame.style.width = sw + 'px'
    previewFrame.style.height = sh + 'px'
    previewFrame.style.transform = 'scale(0.72)'
    previewFrame.style.transformOrigin = 'top left'
    previewFrame.style.marginBottom = '-' + Math.round(sh * 0.28) + 'px'
    previewImg.src = cue.imageUrl
    previewImg.style.left = (-sx) + 'px'
    previewImg.style.top = (-sy) + 'px'
  }

  function scheduleRetry() {
    if (retryTimer) return
    const delay = fetchAttempts <= 1 ? 1000 : (fetchAttempts === 2 ? 2000 : 4000)
    retryTimer = window.setTimeout(function () {
      retryTimer = 0
      loadCues()
    }, delay)
  }

  function loadCues() {
    if (trackLoading) return
    trackLoading = true
    fetchAttempts += 1
    const encodedVideoId = encodeURIComponent(videoId)
    const spriteUrl = '/api/media/preview-sprite/' + encodedVideoId
    fetch('/api/media/preview-track-json/' + encodedVideoId, { credentials: 'same-origin' })
      .then(function (resp) {
        if (!resp.ok) {
          const err = new Error('HTTP ' + resp.status)
          err.status = resp.status
          throw err
        }
        return resp.json().then(function (track) {
          previewCues = parsePreviewTrackJson(track, spriteUrl)
        })
      })
      .catch(function () {
        if (!previewCues.length && fetchAttempts < 8) scheduleRetry()
      })
      .finally(function () {
        trackLoading = false
      })
  }

  const timeRange = document.getElementById('main-player-time-range')
  if (!timeRange) return

  ensureOverlay()
  loadCues()

  playerWrapper.addEventListener('mouseleave', hideOverlay)
  timeRange.addEventListener('mouseleave', hideOverlay)
  timeRange.addEventListener('mousemove', function (event) {
    const rect = timeRange.getBoundingClientRect()
    if (!(rect.width > 0)) return
    const dur = Number(video.duration || 0)
    if (!(dur > 0)) return
    const ratio = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width))
    const previewTime = ratio * dur
    const cue = findPreviewCueAtTime(previewCues, previewTime)
    setOverlay(cue, previewTime, event.clientX)
    if (!previewCues.length && !trackLoading) loadCues()
  }, { passive: true })
}
