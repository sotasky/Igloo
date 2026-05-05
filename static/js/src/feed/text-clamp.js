// Text clamp module — extracted from feed_page.js
// Handles "Read more" / "Show less" toggle for long post text.

import { t } from '../utils.js'

export function initTextClamps(container) {
  const scope = container || document
  scope.querySelectorAll('[data-feed-text-container]').forEach(function (el) {
    if (!el || el.dataset.feedTextInit === '1') return
    el.dataset.feedTextInit = '1'
    const textEl = el.querySelector('[data-feed-text-clamp]')
    const toggleBtn = el.querySelector('[data-feed-text-toggle]')
    if (!textEl || !toggleBtn) return
    const clampLines = Math.max(2, parseInt(textEl.getAttribute('data-feed-clamp-lines') || '6', 10) || 6)
    function applyClamp() {
      textEl.style.display = '-webkit-box'
      textEl.style.webkitBoxOrient = 'vertical'
      textEl.style.webkitLineClamp = String(clampLines)
      textEl.style.lineClamp = String(clampLines)
    }
    function removeClamp() {
      textEl.style.display = 'block'
      textEl.style.webkitLineClamp = 'unset'
      textEl.style.lineClamp = 'unset'
    }
    function decideClamp() {
      // Measure natural height while element is still in default block layout.
      // -webkit-box layout reports inflated scrollHeight for short content
      // (line-height/emoji metric quirks), causing false-positive "Read more".
      const cs = getComputedStyle(textEl)
      const fontSize = parseFloat(cs.fontSize) || 16
      let lineHeight
      if (cs.lineHeight && cs.lineHeight.endsWith('px')) {
        lineHeight = parseFloat(cs.lineHeight)
      } else {
        // Firefox returns unitless numbers (e.g. "1.3") and both browsers
        // return "normal" when unset — resolve against font-size here.
        const num = parseFloat(cs.lineHeight)
        const multiplier = isFinite(num) && num > 0 ? num : 1.2
        lineHeight = fontSize * multiplier
      }
      if (!isFinite(lineHeight) || lineHeight <= 0) lineHeight = fontSize * 1.2
      const naturalHeight = textEl.scrollHeight
      const maxClampedHeight = lineHeight * clampLines
      if (naturalHeight > maxClampedHeight + 2) {
        applyClamp()
        toggleBtn.classList.remove('hidden')
      } else {
        toggleBtn.classList.add('hidden')
      }
    }
    toggleBtn.addEventListener('click', function () {
      const expanded = textEl.classList.toggle('expanded')
      if (expanded) {
        removeClamp()
        toggleBtn.textContent = t('action_show_less', 'Show less')
      } else {
        applyClamp()
        toggleBtn.textContent = t('action_read_more', 'Read more')
      }
    })
    requestAnimationFrame(decideClamp)
  })
}

window.FeedTextClamp = { init: initTextClamps }
