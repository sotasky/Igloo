import { t } from '../utils.js'

// Reposter + tagged-accounts UI — extracted from site_base.js.
//
// Responsibilities:
//   - initRepostLines: page-load mute filter for inline reposter links
//   - expand chip handler for "+N more"
//   - initRetweetersDialog: tagged-accounts modal only (repost multi-button
//     no longer exists in markup)

var retweetMuteStorageKey = 'feedMutedRetweetChannels'

function getMutedChannels() {
  try {
    var raw = localStorage.getItem(retweetMuteStorageKey) || '[]'
    return new Set((JSON.parse(raw) || []).map(function (v) { return String(v || '').trim() }).filter(Boolean))
  } catch (_) { return new Set() }
}

// Hide muted reposter links inside every .feed-repost-line found in scope.
// If every link in a line is hidden (and no self-RT is visible), hide the
// whole line.
export function applyRepostMuteFilter(scope) {
  var muted = getMutedChannels()
  var root = scope || document
  root.querySelectorAll('[data-feed-repost-line]').forEach(function (line) {
    var article = line.closest('[data-feed-item]')
    var authorHandle = String((article && article.getAttribute('data-author-handle')) || '').toLowerCase().replace(/^@/, '')
    var links = line.querySelectorAll('.feed-repost-link[data-repost-channel-id]')
    var anyVisible = false
    links.forEach(function (a) {
      var ch = String(a.getAttribute('data-repost-channel-id') || '').trim()
      var handle = String(a.getAttribute('data-repost-handle') || '').toLowerCase()
      var isSelf = handle && handle === authorHandle
      var hide = !isSelf && ch && muted.has(ch)
      a.style.display = hide ? 'none' : ''
      var sep = a.nextElementSibling
      if (sep && sep.classList.contains('feed-repost-sep')) {
        sep.style.display = hide ? 'none' : ''
      }
      if (!hide) anyVisible = true
    })
    // Inert spans (.feed-repost-link-inert) and the "+N more" button don't
    // participate in mute-filtering; they always count as visible content.
    if (links.length === 0 || line.querySelector('.feed-repost-link-inert') || line.querySelector('[data-feed-repost-more]')) {
      anyVisible = true
    }
    line.style.display = anyVisible ? '' : 'none'
  })
}

function expandRepostLine(btn) {
  var line = btn.closest('[data-feed-repost-line]')
  if (!line) return
  var hidden = line.querySelector('[data-feed-repost-hidden]')
  if (hidden) hidden.removeAttribute('hidden')
  // The ", " separator before the button is no longer needed — the expanded
  // wrapper already carries its own leading separator.
  var precedingSep = btn.previousElementSibling
  if (precedingSep && precedingSep.classList.contains('feed-repost-sep')) {
    precedingSep.remove()
  }
  btn.remove()
  // Re-apply mute filter so newly revealed links respect mutes for visual
  // consistency (the hidden block may contain muted channels).
  applyRepostMuteFilter(line.parentNode || document)
}

// Global click delegation for the "+N more" chip.
document.addEventListener('click', function (e) {
  var btn = e.target && e.target.closest && e.target.closest('[data-feed-repost-more]')
  if (!btn) return
  e.preventDefault()
  expandRepostLine(btn)
})

// ─── Tagged-accounts dialog (unchanged behavior) ───

export function initRetweetersDialog() {
  var dialog = document.getElementById('retweeters-dialog')
  if (!dialog) return
  var list = dialog.querySelector('.retweeters-list')
  var titleEl = dialog.querySelector('.retweeters-dialog-header h3')
  var closeBtn = dialog.querySelector('.retweeters-dialog-close')

  function buildRetweeterItem(rt) {
    var handle = (rt.handle || '').replace(/^@/, '')
    var name = rt.display_name || handle
    var channelId = rt.channel_id || ('twitter_' + handle)
    var avatarUrl = rt.avatar_url || ''

    var li = document.createElement('li')
    var a = document.createElement('a')
    a.href = '/channels/' + encodeURIComponent(channelId)

    if (avatarUrl) {
      var img = document.createElement('img')
      img.className = 'retweeter-avatar'
      img.src = avatarUrl
      img.alt = name.charAt(0).toUpperCase()
      img.loading = 'lazy'
      img.onerror = function () { this.style.display = 'none'; var fb = this.nextElementSibling; if (fb && fb.classList.contains('retweeter-avatar-fb')) fb.style.display = 'inline-flex' }
      var fb = document.createElement('span')
      fb.className = 'retweeter-avatar-fb'
      fb.style.display = 'none'
      fb.textContent = name.charAt(0).toUpperCase() || '?'
      a.appendChild(img)
      a.appendChild(fb)
    } else {
      var fb2 = document.createElement('span')
      fb2.className = 'retweeter-avatar-fb'
      fb2.textContent = name.charAt(0).toUpperCase() || '?'
      a.appendChild(fb2)
    }

    var nameSpan = document.createElement('span')
    nameSpan.className = 'retweeter-name'
    nameSpan.textContent = name

    var handleSpan = document.createElement('span')
    handleSpan.className = 'retweeter-handle'
    handleSpan.textContent = '@' + handle

    a.appendChild(nameSpan)
    a.appendChild(handleSpan)
    li.appendChild(a)
    return li
  }

  function openDialog(btn, entries, title) {
    while (list.firstChild) list.removeChild(list.firstChild)
    if (titleEl) titleEl.textContent = title
    entries.forEach(function (rt) { list.appendChild(buildRetweeterItem(rt)) })

    var rect = btn.getBoundingClientRect()
    dialog.style.margin = '0'
    dialog.style.position = 'fixed'
    var top = rect.bottom + 4
    var left = rect.left
    if (top + 360 > window.innerHeight) top = rect.top - 360 - 4
    if (left + 340 > window.innerWidth) left = window.innerWidth - 340 - 8
    if (left < 8) left = 8
    dialog.style.top = top + 'px'
    dialog.style.left = left + 'px'
    dialog.showModal()
  }

  document.addEventListener('click', function (e) {
    var tagBtn = e.target.closest('.feed-tagged-multi')
    if (tagBtn) {
      e.preventDefault()
      var tagged
      try { tagged = JSON.parse(tagBtn.getAttribute('data-tagged')) } catch (_) { return }
      if (!tagged || !tagged.length) return
      openDialog(tagBtn, tagged, t('feed_tagged_in_this_post', 'Tagged in this post'))
    }
  })

  if (closeBtn) {
    closeBtn.addEventListener('click', function () { dialog.close() })
  }
  dialog.addEventListener('click', function (e) {
    if (e.target === dialog) dialog.close()
  })
}
