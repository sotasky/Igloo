import { showToast, t, tf } from '../utils.js'

const CATEGORY_KEYS = {
  sponsor: 'sponsorblock_category_sponsor',
  selfpromo: 'sponsorblock_category_selfpromo',
  interaction: 'sponsorblock_category_interaction',
  intro: 'sponsorblock_category_intro',
  outro: 'sponsorblock_category_outro',
  preview: 'sponsorblock_category_preview',
  filler: 'sponsorblock_category_filler',
  music_offtopic: 'sponsorblock_category_music_offtopic',
}

const CATEGORY_FALLBACKS = {
  sponsor: 'Sponsor', selfpromo: 'Self-promo', interaction: 'Interaction reminder',
  intro: 'Intro', outro: 'Outro', preview: 'Preview', filler: 'Filler', music_offtopic: 'Non-music',
}

const CATEGORY_COLORS = {
  sponsor: '#00d400', selfpromo: '#ffff00', interaction: '#cc00ff',
  intro: '#00ffff', outro: '#0202ed', preview: '#008fd6',
  filler: '#7300ff', music_offtopic: '#ff9900',
}

function categoryLabel(category) {
  var key = CATEGORY_KEYS[category]
  var fallback = CATEGORY_FALLBACKS[category] || t('sponsorblock_segment_fallback', 'Segment')
  return key ? t(key, fallback) : fallback
}

export function initSponsorBlock(video, root) {
  const channelPlatform = (root.getAttribute('data-channel-platform') || '').trim().toLowerCase()
  const videoId = (root.getAttribute('data-video-id') || '').trim()
  const sbRaw = (root.getAttribute('data-sponsorblock-categories') || '').trim()
  if (!sbRaw || channelPlatform !== 'youtube' || !videoId) return

  // Parse "sponsor:silent,selfpromo:ask,interaction:silent" into {category: mode}
  const sbModes = {}
  const apiCats = []
  sbRaw.split(',').forEach(function (entry) {
    const parts = entry.trim().split(':')
    const cat = parts[0]
    const mode = parts[1] || 'silent'
    if (cat && mode !== 'off') {
      sbModes[cat] = mode
      apiCats.push(cat)
    }
  })
  if (!apiCats.length) return

  let segments = []
  let skipBtn = null
  let currentSeg = null
  let activeSeg = null
  let didSeek = false
  let playbackStarted = false
  let internalSeek = false

  // Track user-initiated seeks only. Ignore:
  //   - seeks before first 'playing' (resume position applied during load)
  //   - seeks we trigger ourselves (adjacent segments would otherwise show a Skip button
  //     instead of chaining silent skips)
  video.addEventListener('playing', function () { playbackStarted = true }, { once: true })
  video.addEventListener('seeking', function () {
    if (internalSeek) { internalSeek = false; return }
    if (playbackStarted) didSeek = true
  })

  function seekPast(seg) {
    internalSeek = true
    video.currentTime = seg.end
  }

  // Create Skip button overlay
  const wrapper = root.querySelector('.player-wrapper')
  if (wrapper) {
    skipBtn = document.createElement('button')
    skipBtn.className = 'sb-skip-btn hidden'
    skipBtn.type = 'button'
    skipBtn.textContent = t('action_skip', 'Skip')
    skipBtn.addEventListener('click', function () {
      if (currentSeg) {
        seekPast(currentSeg)
        activeSeg = null
      }
      skipBtn.classList.add('hidden')
      currentSeg = null
    })
    wrapper.appendChild(skipBtn)
  }

  function showSkipBtn(seg) {
    if (!skipBtn) return
    currentSeg = seg
    skipBtn.classList.remove('hidden')
  }

  function hideSkipBtn() {
    if (!skipBtn) return
    skipBtn.classList.add('hidden')
    currentSeg = null
  }

  function findSegment(t) {
    for (let i = 0; i < segments.length; i++) {
      const seg = segments[i]
      if (t >= seg.start && t < seg.end - 0.3) return seg
    }
    return null
  }

  function renderOverlay() {
    const dur = video.duration
    if (!dur || dur <= 0) {
      video.addEventListener('durationchange', function handler() {
        video.removeEventListener('durationchange', handler)
        renderOverlay()
      })
      return
    }

    const timeRange = document.getElementById('main-player-time-range')
    if (!timeRange) return

    if (!segments.length) {
      timeRange.style.removeProperty('--media-range-track-background')
      return
    }

    const base = 'rgba(255, 255, 255, 0.14)'
    const sorted = segments.slice().sort(function (a, b) { return a.start - b.start })

    function buildGradient(fillColor) {
      const stops = []
      let cursor = 0
      sorted.forEach(function (seg) {
        const startPct = (seg.start / dur) * 100
        const endPct = Math.min((seg.end / dur) * 100, 100)
        const color = CATEGORY_COLORS[seg.category] || '#888'
        if (startPct > cursor) {
          stops.push(fillColor + ' ' + cursor + '%')
          stops.push(fillColor + ' ' + startPct + '%')
        }
        stops.push(color + ' ' + startPct + '%')
        stops.push(color + ' ' + endPct + '%')
        cursor = endPct
      })
      if (cursor < 100) {
        stops.push(fillColor + ' ' + cursor + '%')
        stops.push(fillColor + ' 100%')
      }
      return 'linear-gradient(to right, ' + stops.join(', ') + ')'
    }

    timeRange.style.setProperty('--media-range-track-background', buildGradient(base))
  }

  video.addEventListener('timeupdate', function () {
    if (!segments.length) return
    const t = video.currentTime
    const seg = findSegment(t)
    const wasSeek = didSeek
    didSeek = false

    if (!seg) {
      activeSeg = null
      if (currentSeg) hideSkipBtn()
      return
    }

    if (seg === activeSeg) return

    activeSeg = seg

    if (seg.mode === 'ask') {
      showSkipBtn(seg)
    } else if (seg.mode === 'silent') {
      if (wasSeek) {
        showSkipBtn(seg)
      } else {
        seekPast(seg)
        activeSeg = null
        hideSkipBtn()
        showToast(tf('sponsorblock_segment_skipped', '%1$s skipped', categoryLabel(seg.category)))
      }
    }
  })

  // Fetch segments
  fetch('/api/videos/' + encodeURIComponent(videoId) + '/segments')
    .then(function (r) { return r.ok ? r.json() : Promise.reject() })
    .then(function (data) {
      const allSegs = data.segments || []
      segments = allSegs.filter(function (s) {
        return sbModes[s.category]
      }).map(function (s) {
        return { start: s.start, end: s.end, mode: sbModes[s.category], category: s.category }
      })
      renderOverlay()
    })
    .catch(function () {})
}
