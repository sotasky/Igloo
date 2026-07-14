export function initFeedSeenTracking(root, send) {
  if (!root || root.getAttribute('data-seen-tracked') !== '1') return null
  if (typeof IntersectionObserver !== 'function' || typeof send !== 'function') return null

  var pending = new Set()
  var timer = 0
  var inFlight = false

  function markSeen(ids) {
    var marked = new Set(ids)
    root.querySelectorAll('[data-feed-item][data-tweet-id]').forEach(function (card) {
      if (!marked.has(String(card.getAttribute('data-tweet-id') || '').trim())) return
      card.setAttribute('data-feed-seen', '1')
      card.classList.add('feed-card-seen')
    })
  }

  function schedule(delay) {
    if (timer || inFlight || pending.size === 0) return
    timer = setTimeout(flush, delay == null ? 120 : delay)
  }

  function flush() {
    if (timer) clearTimeout(timer)
    timer = 0
    if (inFlight || pending.size === 0) return
    var ids = Array.from(pending).slice(0, 500)
    ids.forEach(function (id) { pending.delete(id) })
    inFlight = true
    Promise.resolve()
      .then(function () { return send(ids) })
      .then(function () { markSeen(ids) })
      .catch(function () {})
      .finally(function () {
        inFlight = false
        if (pending.size > 0) schedule(0)
      })
  }

  var observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (!entry.isIntersecting) return
      var card = entry.target
      observer.unobserve(card)
      var tweetId = String(card.getAttribute('data-tweet-id') || '').trim()
      if (tweetId) pending.add(tweetId)
    })
    schedule(pending.size >= 500 ? 0 : 120)
  }, { threshold: 0.15 })

  function observe() {
    root.querySelectorAll('[data-feed-item][data-tweet-id][data-feed-seen="0"]').forEach(function (card) {
      if (card.getAttribute('data-feed-seen-observed') === '1') return
      card.setAttribute('data-feed-seen-observed', '1')
      observer.observe(card)
    })
  }

  observe()
  return { observe: observe, flush: flush }
}
