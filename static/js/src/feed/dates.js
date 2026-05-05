// Dates module — extracted from feed_page.js
// Hydrates relative/absolute dates on feed cards.

import { formatRelative, formatAbsolute } from '../utils.js'

export function initDates(container) {
  const scope = container || document
  scope.querySelectorAll('.feed-date-inline[data-feed-date-raw], .feed-quote-date[data-feed-date-raw]').forEach(function (el) {
    if (!el) return
    const raw = String(el.getAttribute('data-feed-date-raw') || '').trim()
    if (!raw) return
    const rel = formatRelative(raw)
    const abs = formatAbsolute(raw)
    el.setAttribute('data-date-relative', rel)
    el.setAttribute('data-date-absolute', abs)
    el.textContent = '\u00b7 ' + (rel || raw)
    el.title = abs || raw
    if (el.dataset.feedDateBound === '1') return
    el.dataset.feedDateBound = '1'
    el.addEventListener('mouseenter', function () {
      const absolute = String(el.getAttribute('data-date-absolute') || '').trim()
      if (absolute) el.textContent = '\u00b7 ' + absolute
    })
    el.addEventListener('mouseleave', function () {
      const relative = String(el.getAttribute('data-date-relative') || '').trim()
      const fallback = String(el.getAttribute('data-feed-date-raw') || '').trim()
      el.textContent = '\u00b7 ' + (relative || fallback)
    })
  })
}

document.addEventListener('igloo:i18n:changed', function () {
  initDates(document)
})

window.FeedDates = { init: initDates }
