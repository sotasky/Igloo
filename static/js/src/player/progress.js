import { apiFetch } from '../utils.js'

export function initProgress(video, videoId, root) {
  if (!videoId) return

  const channelPlatform = (root.getAttribute('data-channel-platform') || '').trim().toLowerCase()
  const nextUrl = (root.getAttribute('data-next-url') || '').trim()

  function saveProgress() {
    const pos = Number(video.currentTime || 0)
    const dur = Number(video.duration || 0)
    const completed = video.ended && dur > 0
    const savedPos = completed ? 0 : pos
    if (!completed && pos <= 5) return

    if (channelPlatform === 'tiktok' || channelPlatform === 'instagram') return
    if (dur > 0 && dur < 65) return

    apiFetch('/api/videos/' + encodeURIComponent(videoId) + '/progress', {
      method: 'POST',
      body: JSON.stringify({
        position: savedPos,
        duration: dur,
        updated_at_ms: Date.now(),
		}),
	}).catch(function (err) {
      console.debug('[Progress] save failed', err)
    })
  }

  // URL ?t= param overrides stored resume position
  let urlResumeT = 0
  try {
    const sp = new URLSearchParams(window.location.search)
    urlResumeT = Number(sp.get('t')) || 0
  } catch (_) {}
  const resumePos = urlResumeT > 1 ? urlResumeT : Number(root.getAttribute('data-resume-position') || 0)
  const resumeThreshold = urlResumeT > 1 ? 1 : 5
  if (resumePos > resumeThreshold) {
    video.addEventListener('loadedmetadata', function () {
      const current = Number(video.currentTime || 0)
      if (Math.abs(current - resumePos) > 1) {
        video.currentTime = resumePos
      }
    }, { once: true })
  }

  // Save on pause/exit
  video.addEventListener('pause', saveProgress)
  video.addEventListener('ended', saveProgress)
  window.addEventListener('pagehide', saveProgress)
  window.addEventListener('beforeunload', saveProgress)

  // Autoplay
  const autoplayCurrent = new URLSearchParams(window.location.search).get('autoplay') === '1'
  const autoplayYoutube = channelPlatform === 'youtube'
  if (autoplayCurrent || autoplayYoutube) {
    video.autoplay = true
    video.play().catch(function () {})
  }
  if (autoplayYoutube) {
    video.addEventListener('loadedmetadata', function () {
      video.play().catch(function () {})
    })
  }

  // Autoplay next on ended
  const AUTOPLAY_NEXT_KEY = 'playerAutoplayNextV1'
  video.addEventListener('ended', function () {
    let autoplayNext = false
    try { autoplayNext = localStorage.getItem(AUTOPLAY_NEXT_KEY) === '1' } catch (_) {}
    if (!autoplayNext || !nextUrl) return
    window.location.assign(nextUrl)
  })
	return { saveNow: saveProgress }
}
