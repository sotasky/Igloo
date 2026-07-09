// Global bookmark menu module — popover for saving/moving/removing bookmarks.
// Usable from feed, videos, shorts, or any page. Dispatches callbacks for
// page-specific state sync (e.g. icon updates, sibling propagation).

import { apiFetch, showToast, stateBool, setStateBool, t, tf } from './utils.js'

var bookmarkMenu = null
var bookmarkCategories = []
var bookmarkLabels = []
var bookmarkAccountOptions = []
var bookmarkAccountOptionsLoaded = false

// In-memory alias cache + bookmarked handles set
var aliasCache = {}
var bookmarkedHandlesSet = new Set()
var aliasCacheLoaded = false

function getLastBookmarkCategoryId() {
  try { return String(localStorage.getItem('bookmarkLastCategoryIdV1') || '').trim() || null }
  catch (_) { return null }
}

function setLastBookmarkCategoryId(categoryId) {
  var clean = String(categoryId || '').trim()
  if (!clean) return
  try { localStorage.setItem('bookmarkLastCategoryIdV1', clean) } catch (_) {}
}

async function loadBookmarkAliasesFromApi() {
  if (aliasCacheLoaded) return aliasCache
  try {
    var resp = await fetch('/api/bookmark-aliases?include_handles=1')
    var data = await resp.json()
    aliasCache = {}
    var aliases = data.aliases || data
    for (var i = 0; i < aliases.length; i++) {
      aliasCache[aliases[i].original_handle.toLowerCase()] = aliases[i].display_alias
    }
    if (data.bookmarked_handles) {
      bookmarkedHandlesSet = new Set(data.bookmarked_handles.map(function (h) { return h.toLowerCase() }))
    }
  } catch (_) {}
  aliasCacheLoaded = true
  return aliasCache
}

function resolveBookmarkAlias(handle) {
  if (!handle || !aliasCache) return handle
  return aliasCache[handle.toLowerCase()] || handle
}

async function saveBookmarkAliasToApi(original, aliased) {
  aliasCache[original.toLowerCase()] = aliased
  try {
    await fetch('/api/bookmark-aliases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ aliases: [{ original_handle: original, display_alias: aliased }] })
    })
  } catch (_) {}
}

var _bmOutsideHandler = null, _bmEscHandler = null

export function closeBookmarkMenu() {
  if (!bookmarkMenu) return
  var menu = bookmarkMenu
  bookmarkMenu = null
  if (menu._accountPicker && menu._accountPicker.parentNode) menu._accountPicker.remove()
  if (menu.parentNode) menu.remove()
  if (_bmOutsideHandler) { document.removeEventListener('mousedown', _bmOutsideHandler); _bmOutsideHandler = null }
  if (_bmEscHandler) { document.removeEventListener('keydown', _bmEscHandler); _bmEscHandler = null }
}

export function isBookmarkMenuOpen() {
  return !!bookmarkMenu
}

function loadBookmarkCategories() {
  return apiFetch('/api/bookmark-categories')
    .then(function (data) {
      var categories = Array.isArray(data) ? data : (data && data.categories) || []
      if (Array.isArray(categories)) bookmarkCategories = categories.slice()
      return bookmarkCategories
    })
    .catch(function () { return bookmarkCategories })
}

function loadBookmarkLabels() {
  return apiFetch('/api/bookmark-labels')
    .then(function (data) {
      var labels = Array.isArray(data) ? data : (data && data.labels) || []
      if (Array.isArray(labels)) bookmarkLabels = labels.slice()
      return bookmarkLabels
    })
    .catch(function () { return bookmarkLabels })
}

function loadBookmarkAccountOptions() {
  if (bookmarkAccountOptionsLoaded) return Promise.resolve(bookmarkAccountOptions)
  return apiFetch('/api/bookmark-account-options')
    .then(function (data) {
      var accounts = Array.isArray(data) ? data : (data && data.accounts) || []
      bookmarkAccountOptions = accounts
        .map(function (account) {
          return {
            handle: String(account.handle || '').trim().replace(/^@+/, ''),
            label: String(account.label || '').trim(),
            platform: String(account.platform || '').trim()
          }
        })
        .filter(function (account) { return account.handle })
      bookmarkAccountOptionsLoaded = true
      return bookmarkAccountOptions
    })
    .catch(function () {
      bookmarkAccountOptionsLoaded = true
      return bookmarkAccountOptions
    })
}

function normalizeAccountKey(handle) {
  return String(handle || '').trim().replace(/^@+/, '').toLowerCase()
}

function accountOptionSearchText(account) {
  return [account.handle, account.label, account.platform].join(' ').toLowerCase()
}

function parseAccountHandles(value) {
  if (!value) return []
  var handles = Array.isArray(value) ? value : String(value || '').split(',')
  var seen = new Set()
  var out = []
  handles.forEach(function (handle) {
    var clean = String(handle || '').trim().replace(/^@+/, '')
    var key = normalizeAccountKey(clean)
    if (!key || seen.has(key)) return
    seen.add(key)
    out.push(clean)
  })
  return out
}

function loadStoredBookmarkAccountHandles(itemId) {
  if (!itemId) return Promise.resolve([])
  return apiFetch('/api/bookmark/' + encodeURIComponent(itemId))
    .then(function (data) { return parseAccountHandles(data && data.account_handles) })
    .catch(function () { return [] })
}

function platformLabelFallback(platform) {
  if (platform === 'twitter') return t('bookmark_platform_x_post', 'X post')
  if (platform === 'tiktok') return t('bookmark_platform_tiktok_post', 'TikTok post')
  if (platform === 'instagram') return t('bookmark_platform_instagram_post', 'Instagram post')
  if (platform === 'youtube') return t('bookmark_platform_youtube_video', 'YouTube video')
  return ''
}

function removeBookmark(itemId, root) {
  if (!itemId) return Promise.resolve(false)
  return apiFetch('/api/bookmark/' + encodeURIComponent(itemId), { method: 'DELETE' })
    .then(function (result) {
      if (!result || !result.success) return false
      if (root) {
        setStateBool(root, 'bookmarked', false)
        root.setAttribute('data-bookmark-category-id', '')
      }
      return true
    })
    .catch(function () { return false })
}

// openBookmarkMenu opens the bookmark popover anchored to anchorEl.
// root: the item element (used for data-bookmarked, data-author-handle, etc.).
// opts.tweetId / opts.tiktokId / opts.youtubeId: platform-specific item ID
//   — exactly one should be set, and determines opts.platform implicitly.
// opts.bodyText: post description/caption used for @mention account suggestions
//   (optional; omit to skip mention scanning).
// opts.titleFallback: placeholder for the label input (falls back to a
//   platform-appropriate default like "X post" / "TikTok post").
// opts.onStateChange(root, isBookmarked, category): called after bookmark state changes.
export async function openBookmarkMenu(anchorEl, root, opts) {
  if (!anchorEl || !root || !opts) return
  var platform = opts.tweetId ? 'twitter' : opts.tiktokId ? 'tiktok' : opts.instagramId ? 'instagram' : opts.youtubeId ? 'youtube' : ''
  var itemId = String(opts.tweetId || opts.tiktokId || opts.instagramId || opts.youtubeId || '').trim()
  if (!itemId || !platform) return
  var onStateChange = opts.onStateChange || function () {}
  var bodyText = String(opts.bodyText || '')
  var titleFallback = String(opts.titleFallback || platformLabelFallback(platform) || '').trim()

  await loadBookmarkCategories()
  await Promise.all([loadBookmarkAliasesFromApi(), loadBookmarkLabels(), loadBookmarkAccountOptions()])

  // One-time migration from localStorage to API
  var legacyAliases = localStorage.getItem('bookmarkAliasesV1')
  if (legacyAliases) {
    try {
      var parsed = JSON.parse(legacyAliases)
      var aliasList = Object.entries(parsed).map(function (kv) {
        return { original_handle: kv[0], display_alias: kv[1] }
      })
      if (aliasList.length) {
        fetch('/api/bookmark-aliases', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ aliases: aliasList })
        }).then(function (r) { if (r.ok) localStorage.removeItem('bookmarkAliasesV1') })
        for (var i = 0; i < aliasList.length; i++) {
          aliasCache[aliasList[i].original_handle] = aliasList[i].display_alias
        }
      }
    } catch (_) {}
  }

  closeBookmarkMenu()
  var isBookmarked = stateBool(root, 'bookmarked')
  var currentCategoryId = String(root.getAttribute('data-bookmark-category-id') || getLastBookmarkCategoryId() || '')
  var authorHandle = String(root.getAttribute('data-author-handle') || '').trim()
  var sourceHandle = platform === 'twitter' ? String(root.getAttribute('data-source-handle') || '').trim() : ''
  var storedAccountHandles = isBookmarked ? await loadStoredBookmarkAccountHandles(itemId) : []

  // Build accounts list
  var accounts = []
  if (authorHandle) accounts.push({ handle: authorHandle, selected: false })
  if (platform === 'twitter') {
    var isRetweet = String(root.getAttribute('data-feed-is-retweet') || '') === '1'
    if (isRetweet && sourceHandle && sourceHandle.toLowerCase() !== authorHandle.toLowerCase()) {
      accounts.push({ handle: sourceHandle, selected: false })
    }
    var quoteAuthorHandle = (root.getAttribute('data-quote-author-handle') || '').trim()
    if (quoteAuthorHandle && !accounts.some(function (a) { return a.handle.toLowerCase() === quoteAuthorHandle.toLowerCase() })) {
      accounts.push({ handle: quoteAuthorHandle, selected: false })
    }
  }
  var existingHandles = new Set(accounts.map(function (a) { return normalizeAccountKey(a.handle) }))
  var mentionRe = /(^|[^A-Za-z0-9_.])@([A-Za-z0-9_](?:[A-Za-z0-9_.]{2,28}[A-Za-z0-9_])?)/g
  var mentionMatch
  while ((mentionMatch = mentionRe.exec(bodyText)) !== null) {
    var mHandle = String(mentionMatch[2] || '').trim()
    if (!existingHandles.has(normalizeAccountKey(mHandle))) {
      accounts.push({ handle: mHandle, selected: false })
      existingHandles.add(normalizeAccountKey(mHandle))
    }
  }

  // Tagged accounts/collaborators: add and auto-select
  var hasTagged = false
  try {
    var taggedRaw = root.getAttribute('data-tagged-accounts')
    if (taggedRaw) {
      var tagged = JSON.parse(taggedRaw)
      for (var ti = 0; ti < tagged.length; ti++) {
        var taggedAccount = tagged[ti] || {}
        var th = String(taggedAccount.retweeter_handle || taggedAccount.handle || taggedAccount.username || '').trim()
        if (th && !existingHandles.has(normalizeAccountKey(th))) {
          accounts.push({ handle: th, selected: true })
          existingHandles.add(normalizeAccountKey(th))
          hasTagged = true
        } else if (th) {
          var existing = accounts.find(function (a) { return normalizeAccountKey(a.handle) === normalizeAccountKey(th) })
          if (existing) { existing.selected = true; hasTagged = true }
        }
      }
    }
  } catch (_) {}

  var hasStoredAccountHandles = false
  storedAccountHandles.forEach(function (handle) {
    var key = normalizeAccountKey(handle)
    if (!key) return
    var existing = accounts.find(function (a) { return normalizeAccountKey(a.handle) === key })
    if (existing) {
      existing.selected = true
    } else {
      accounts.push({ handle: handle, selected: true })
      existingHandles.add(key)
    }
    hasStoredAccountHandles = true
  })

  // Smart pre-selection
  var channelKey = (sourceHandle || authorHandle || '').toLowerCase()
  var preSelected = hasTagged || hasStoredAccountHandles
  if (!preSelected) {
    try {
      var prefs = JSON.parse(localStorage.getItem('bookmarkAccountPrefsV1') || '{}')
      var savedSelection = prefs[channelKey]
      if (savedSelection && Array.isArray(savedSelection) && savedSelection.length) {
        var savedSet = new Set(savedSelection.map(function (s) { return s.toLowerCase() }))
        for (var si = 0; si < accounts.length; si++) {
          if (savedSet.has(accounts[si].handle.toLowerCase())) { accounts[si].selected = true; preSelected = true; break }
        }
      }
    } catch (_) {}
  }
  if (!preSelected) {
    var resolved = accounts.map(function (a) { return resolveBookmarkAlias(a.handle).toLowerCase() })
    for (var ri = 0; ri < accounts.length; ri++) {
      if (bookmarkedHandlesSet.has(accounts[ri].handle.toLowerCase()) || bookmarkedHandlesSet.has(resolved[ri])) {
        accounts[ri].selected = true; preSelected = true; break
      }
    }
  }
  if (!preSelected && accounts.length) accounts[0].selected = true

  // Build popover DOM
  var popover = document.createElement('div')
  popover.className = 'bookmark-popover'
  var titleDiv = document.createElement('div')
  titleDiv.className = 'bookmark-popover-title'
  titleDiv.textContent = isBookmarked ? t('bookmark_move_to', 'Move bookmark to\u2026') : t('bookmark_save_to', 'Save bookmark to\u2026')
  popover.appendChild(titleDiv)
  var bodyDiv = document.createElement('div')
  bodyDiv.className = 'bookmark-popover-body'
  popover.appendChild(bodyDiv)
  document.body.appendChild(popover)
  bookmarkMenu = popover

  // Position near anchor
  var anchorRect = anchorEl.getBoundingClientRect()
  var scrollX = window.scrollX || window.pageXOffset
  var scrollY = window.scrollY || window.pageYOffset
  var popW = 480
  var top = anchorRect.bottom + scrollY + 8
  var left = anchorRect.left + scrollX - popW / 2 + anchorRect.width / 2
  if (left < 8) left = 8
  if (left + popW > document.documentElement.scrollWidth - 8) left = document.documentElement.scrollWidth - popW - 8
  popover.style.top = top + 'px'
  popover.style.left = left + 'px'

  requestAnimationFrame(function () {
    var popRect = popover.getBoundingClientRect()
    var overflow = popRect.bottom - window.innerHeight + 12
    if (overflow > 0) window.scrollBy({ top: overflow, behavior: 'smooth' })
  })

  setTimeout(function () {
    _bmOutsideHandler = function (e) {
      var picker = popover._accountPicker
      var pickerToggle = popover._accountPickerToggle
      if (picker && !picker.classList.contains('hidden') &&
          !picker.contains(e.target) &&
          !(pickerToggle && pickerToggle.contains(e.target))) {
        picker.classList.add('hidden')
      }
      if (!popover.contains(e.target) && !(picker && picker.contains(e.target)) && e.target !== anchorEl) closeBookmarkMenu()
    }
    _bmEscHandler = function (e) { if (e.key === 'Escape') closeBookmarkMenu() }
    document.addEventListener('mousedown', _bmOutsideHandler)
    document.addEventListener('keydown', _bmEscHandler)
  }, 80)

  var body = bodyDiv

  function renderSheet() {
    var categories = Array.isArray(bookmarkCategories) ? bookmarkCategories : []
    if (!categories.some(function (c) { return String(c.id) === String(currentCategoryId) })) {
      var lastCategoryId = String(getLastBookmarkCategoryId() || '')
      currentCategoryId = categories.some(function (c) { return String(c.id) === lastCategoryId })
        ? lastCategoryId
        : String((categories[0] && categories[0].id) || '')
    }

    body.textContent = ''

    // Category pills
    var catList = document.createElement('div')
    catList.className = 'bookmark-sheet-list'
    categories.forEach(function (cat) {
      var catId = String(cat.id)
      var btn = document.createElement('button')
      btn.className = 'bookmark-sheet-option' + (currentCategoryId === catId ? ' selected' : '')
      btn.type = 'button'
      btn.dataset.bookmarkCategory = catId
      btn.textContent = cat.name || tf('bookmark_category_default', 'Category %1$s', catId)
      catList.appendChild(btn)
    })
    body.appendChild(catList)

    // Account pills
    if (accounts.length > 0 || bookmarkAccountOptions.length > 0) {
      var accountLabel = document.createElement('div')
      accountLabel.className = 'bookmark-sheet-field-label'
      accountLabel.textContent = t('bookmark_account', 'Account')
      body.appendChild(accountLabel)

      var accountWrap = document.createElement('div')
      accountWrap.className = 'bookmark-sheet-account-wrap'

      var pillRow = document.createElement('div')
      pillRow.className = 'bookmark-sheet-account-row'
      pillRow.style.display = 'flex'
      pillRow.style.flexWrap = 'wrap'
      pillRow.style.gap = '6px'

      function createAccountPill(acc) {
          var resolvedName = resolveBookmarkAlias(acc.handle)
          var pill = document.createElement('button')
          pill.type = 'button'
          pill.className = 'bookmark-sheet-account-pill' + (acc.selected ? ' active' : '')
          pill.textContent = resolvedName
          pill.dataset.originalHandle = acc.handle
          pill.dataset.selected = acc.selected ? '1' : '0'

          pill.addEventListener('click', function () {
            var isSel = pill.dataset.selected === '1'
            pill.dataset.selected = isSel ? '0' : '1'
            pill.classList.toggle('active', !isSel)
          })

          pill.addEventListener('dblclick', function (e) {
            e.preventDefault()
            var input = document.createElement('input')
            input.type = 'text'
            input.className = 'bookmark-sheet-account-input'
            input.value = pill.textContent
            input.style.width = Math.max(60, pill.offsetWidth) + 'px'
            pill.replaceWith(input)
            input.focus({ preventScroll: true })
            input.select()
            var finish = function () {
              var newAlias = input.value.trim()
              if (newAlias && newAlias !== acc.handle) {
                saveBookmarkAliasToApi(acc.handle, newAlias)
                pill.textContent = newAlias
              } else {
                pill.textContent = resolveBookmarkAlias(acc.handle)
              }
              input.replaceWith(pill)
            }
            input.addEventListener('blur', finish)
            input.addEventListener('keydown', function (ke) { if (ke.key === 'Enter') { ke.preventDefault(); finish() } })
          })

          pillRow.appendChild(pill)
          return pill
      }

      for (var ai = 0; ai < accounts.length; ai++) {
        createAccountPill(accounts[ai])
      }
      var accountAddBtn = document.createElement('button')
      accountAddBtn.className = 'bookmark-sheet-account-add-btn'
      accountAddBtn.type = 'button'
      accountAddBtn.textContent = '+'
      accountAddBtn.setAttribute('aria-label', t('bookmark_add_account', 'Add account'))
      pillRow.appendChild(accountAddBtn)
      popover._accountPickerToggle = accountAddBtn
      accountWrap.appendChild(pillRow)

      var accountPicker = document.createElement('div')
      accountPicker.className = 'bookmark-sheet-account-picker hidden'
      var accountSearch = document.createElement('input')
      accountSearch.type = 'text'
      accountSearch.className = 'bookmark-sheet-input bookmark-sheet-account-search'
      accountSearch.placeholder = t('bookmark_filter_accounts', 'Filter accounts')
      accountSearch.setAttribute('autocomplete', 'off')
      accountPicker.appendChild(accountSearch)
      var accountResults = document.createElement('div')
      accountResults.className = 'bookmark-sheet-account-results'
      accountPicker.appendChild(accountResults)
      document.body.appendChild(accountPicker)
      popover._accountPicker = accountPicker
      body.appendChild(accountWrap)

      function positionAccountPicker() {
        var rect = accountAddBtn.getBoundingClientRect()
        var pickerRect = accountPicker.getBoundingClientRect()
        var margin = 8
        var left = rect.right + margin
        if (left + pickerRect.width > window.innerWidth - margin) {
          left = Math.max(margin, rect.left - pickerRect.width - margin)
        }
        var top = rect.top
        if (top + pickerRect.height > window.innerHeight - margin) {
          top = Math.max(margin, window.innerHeight - pickerRect.height - margin)
        }
        accountPicker.style.left = left + 'px'
        accountPicker.style.top = top + 'px'
      }

      function renderAccountResults() {
        var existing = new Set(accounts.map(function (a) { return normalizeAccountKey(a.handle) }))
        var query = normalizeAccountKey(accountSearch.value)
        var matches = bookmarkAccountOptions
          .filter(function (account) {
            return !existing.has(normalizeAccountKey(account.handle)) &&
              (!query || accountOptionSearchText(account).indexOf(query) !== -1)
          })
          .slice(0, 8)
        accountResults.textContent = ''
        if (!matches.length) {
          var empty = document.createElement('div')
          empty.className = 'bookmark-sheet-account-empty'
          empty.textContent = t('bookmark_no_accounts_found', 'No accounts found')
          accountResults.appendChild(empty)
          return
        }
        matches.forEach(function (account) {
          var row = document.createElement('button')
          row.type = 'button'
          row.className = 'bookmark-sheet-account-result'
          var handleSpan = document.createElement('span')
          handleSpan.className = 'bookmark-sheet-account-result-handle'
          handleSpan.textContent = account.handle
          row.appendChild(handleSpan)
          var label = account.label && normalizeAccountKey(account.label) !== normalizeAccountKey(account.handle)
            ? account.label
            : account.platform
          if (label) {
            var labelSpan = document.createElement('span')
            labelSpan.className = 'bookmark-sheet-account-result-label'
            labelSpan.textContent = label
            row.appendChild(labelSpan)
          }
          row.addEventListener('click', function () {
            var acc = { handle: account.handle, selected: true }
            accounts.push(acc)
            pillRow.insertBefore(createAccountPill(acc), accountAddBtn)
            accountSearch.value = ''
            accountPicker.classList.add('hidden')
          })
          accountResults.appendChild(row)
        })
      }

      accountAddBtn.addEventListener('click', function () {
        accountPicker.classList.toggle('hidden')
        renderAccountResults()
        if (!accountPicker.classList.contains('hidden')) {
          requestAnimationFrame(function () {
            positionAccountPicker()
            try { accountSearch.focus({ preventScroll: true }) } catch (_) {}
          })
        }
      })
      accountSearch.addEventListener('input', function () {
        renderAccountResults()
        positionAccountPicker()
      })
    }

    // Label input with suggestions
    var labelLabel = document.createElement('div')
    labelLabel.className = 'bookmark-sheet-field-label'
    labelLabel.textContent = t('bookmark_label', 'Label')
    body.appendChild(labelLabel)
    var labelWrap = document.createElement('div')
    labelWrap.style.position = 'relative'
    labelWrap.style.marginBottom = '10px'
    var titleInput = document.createElement('input')
    titleInput.className = 'bookmark-sheet-input bookmark-sheet-title-input'
    titleInput.type = 'text'
    titleInput.setAttribute('data-bookmark-custom-title', '')
    titleInput.setAttribute('autocomplete', 'off')
    titleInput.maxLength = 180
    titleInput.placeholder = titleFallback || t('bookmark_label_optional', 'label (optional)')
    titleInput.value = ''
    titleInput.style.width = '100%'
    titleInput.style.boxSizing = 'border-box'
    labelWrap.appendChild(titleInput)
    var suggestBox = document.createElement('div')
    suggestBox.className = 'bookmark-label-suggestions'
    suggestBox.style.display = 'none'
    labelWrap.appendChild(suggestBox)
    body.appendChild(labelWrap)

    var _labelActiveIdx = -1
    function updateLabelSuggestions() {
      var q = (titleInput.value || '').trim().toLowerCase()
      suggestBox.textContent = ''
      if (!q) { suggestBox.style.display = 'none'; return }
      var filtered = bookmarkLabels.filter(function (l) {
        return l.toLowerCase().indexOf(q) !== -1 && l.toLowerCase() !== q
      })
      filtered.sort(function (a, b) {
        var aStarts = a.toLowerCase().startsWith(q) ? 0 : 1
        var bStarts = b.toLowerCase().startsWith(q) ? 0 : 1
        return aStarts - bStarts
      })
      filtered = filtered.slice(0, 6)
      if (!filtered.length) { suggestBox.style.display = 'none'; return }
      function chooseLabelSuggestion(lbl, e) {
        if (e) {
          e.preventDefault()
          e.stopPropagation()
        }
        titleInput.value = lbl
        suggestBox.style.display = 'none'
        _labelActiveIdx = -1
        try { titleInput.focus({ preventScroll: true }) } catch (_) {}
      }
      filtered.forEach(function (lbl) {
        var item = document.createElement('div')
        item.className = 'bookmark-label-suggestion-item'
        var labelSpan = document.createElement('span')
        labelSpan.textContent = lbl
        item.appendChild(labelSpan)
        item.addEventListener('mousedown', function (e) { chooseLabelSuggestion(lbl, e) })
        var removeBtn = document.createElement('button')
        removeBtn.className = 'bookmark-label-remove-btn'
        removeBtn.type = 'button'
        removeBtn.textContent = '\u00d7'
        removeBtn.title = t('bookmark_delete_label', 'Delete label')
        removeBtn.addEventListener('mousedown', function (e) {
          e.preventDefault()
          e.stopPropagation()
          apiFetch('/api/bookmark-labels/' + encodeURIComponent(lbl), { method: 'DELETE' })
            .then(function () {
              bookmarkLabels = bookmarkLabels.filter(function (l) { return l !== lbl })
              item.remove()
              if (!suggestBox.children.length) suggestBox.style.display = 'none'
              showToast(t('bookmark_label_deleted', 'Label deleted'))
            })
            .catch(function () { showToast(t('bookmark_delete_label_failed', 'Failed to delete label')) })
        })
        item.appendChild(removeBtn)
        suggestBox.appendChild(item)
      })
      suggestBox.style.display = ''
    }

    function moveLabelActive(delta) {
      var items = suggestBox.querySelectorAll('.bookmark-label-suggestion-item')
      if (!items.length) return
      items.forEach(function (el) { el.classList.remove('active') })
      _labelActiveIdx += delta
      if (_labelActiveIdx < -1) _labelActiveIdx = items.length - 1
      if (_labelActiveIdx >= items.length) _labelActiveIdx = -1
      if (_labelActiveIdx >= 0) items[_labelActiveIdx].classList.add('active')
    }

    titleInput.addEventListener('input', function () { _labelActiveIdx = -1; updateLabelSuggestions() })
    titleInput.addEventListener('focus', function () { _labelActiveIdx = -1; updateLabelSuggestions() })
    titleInput.addEventListener('blur', function () { setTimeout(function () { suggestBox.style.display = 'none'; _labelActiveIdx = -1 }, 150) })

    // Save button
    var saveBtnEl = document.createElement('button')
    saveBtnEl.className = 'bookmark-sheet-save-btn'
    saveBtnEl.type = 'button'
    saveBtnEl.setAttribute('data-bookmark-save', '')
    saveBtnEl.textContent = (isBookmarked ? t('action_save', 'Save') : t('action_bookmark', 'Bookmark')) + ' \u21b5'
    saveBtnEl.style.width = '100%'
    saveBtnEl.style.marginBottom = '8px'
    body.appendChild(saveBtnEl)

    // Remove bookmark button
    if (isBookmarked) {
      var dangerBtn = document.createElement('button')
      dangerBtn.className = 'bookmark-sheet-danger'
      dangerBtn.setAttribute('data-bookmark-remove', '')
      dangerBtn.textContent = t('action_remove_bookmark', 'Remove bookmark')
      body.appendChild(dangerBtn)
    }

    // Add category
    var addDiv = document.createElement('div')
    addDiv.className = 'bookmark-sheet-add'
    var addInputEl = document.createElement('input')
    addInputEl.className = 'bookmark-sheet-input'
    addInputEl.type = 'text'
    addInputEl.placeholder = t('bookmark_add_category', 'Add category')
    addInputEl.maxLength = 64
    addDiv.appendChild(addInputEl)
    var addBtnEl = document.createElement('button')
    addBtnEl.className = 'bookmark-sheet-add-btn'
    addBtnEl.type = 'button'
    addBtnEl.setAttribute('data-bookmark-add', '')
    addBtnEl.textContent = '+'
    addDiv.appendChild(addBtnEl)
    body.appendChild(addDiv)

    // Media index selector
    var mainMediaCount = parseInt(root.getAttribute('data-media-count') || '0', 10) || 0
    var quoteMediaCount = platform === 'twitter'
      ? (parseInt(root.getAttribute('data-quote-media-count') || '0', 10) || 0)
      : 0
    var totalMedia = mainMediaCount + quoteMediaCount
    if (totalMedia > 1) {
      var mediaLabel = document.createElement('div')
      mediaLabel.className = 'bookmark-sheet-field-label'
      mediaLabel.textContent = t('bookmark_pick_media_to_download', 'Pick # to download') + (quoteMediaCount ? '  ' + t('bookmark_quote_first', '(quote first)') : '')
      mediaLabel.style.marginTop = '8px'
      body.appendChild(mediaLabel)
      var mediaRow = document.createElement('div')
      mediaRow.className = 'bookmark-media-idx-row'
      var displayOrder = []
      for (var mi2 = 0; mi2 < mainMediaCount; mi2++) displayOrder.push(mi2)
      for (var qi = 0; qi < quoteMediaCount; qi++) displayOrder.push(mainMediaCount + qi)
      for (var di = 0; di < displayOrder.length; di++) {
        ;(function (displayIdx, downloadIdx) {
          var mBtn = document.createElement('button')
          mBtn.type = 'button'
          mBtn.className = 'bookmark-media-idx-btn active'
          mBtn.textContent = String(displayIdx + 1)
          mBtn.dataset.mediaIdx = String(downloadIdx)
          mBtn.dataset.selected = '1'
          if (quoteMediaCount && displayIdx === mainMediaCount) mBtn.style.marginLeft = '8px'
          mBtn.addEventListener('click', function () {
            var sel = mBtn.dataset.selected === '1'
            mBtn.dataset.selected = sel ? '0' : '1'
            mBtn.classList.toggle('active', !sel)
          })
          mediaRow.appendChild(mBtn)
        })(di, displayOrder[di])
      }
      body.appendChild(mediaRow)
    }

    // Status area
    var statusDiv = document.createElement('div')
    statusDiv.className = 'bookmark-sheet-status'
    statusDiv.setAttribute('data-bookmark-status', '')
    body.appendChild(statusDiv)

    function setStatus(text) { statusDiv.textContent = String(text || '') }

    function saveCurrentBookmark() {
      setStatus('')
      var selectedPills = body.querySelectorAll('.bookmark-sheet-account-pill[data-selected="1"]')
      var accountHandles = Array.from(selectedPills).map(function (p) { return p.textContent.trim() }).filter(Boolean)
      var selOriginals = Array.from(selectedPills)
        .map(function (p) { return p.dataset.originalHandle }).filter(Boolean)
      var categoryIdNum = Number(currentCategoryId) || null
      var bookmarkBody = { category_id: categoryIdNum }
      var custom = String((titleInput && titleInput.value) || '').trim()
      if (custom) bookmarkBody.custom_title = custom
      if (accountHandles.length) bookmarkBody.account_handles = accountHandles
      var mediaIdxBtns = body.querySelectorAll('.bookmark-media-idx-btn')
      if (mediaIdxBtns.length) {
        var selIndices = []
        mediaIdxBtns.forEach(function (b) { if (b.dataset.selected === '1') selIndices.push(parseInt(b.dataset.mediaIdx, 10)) })
        if (selIndices.length < mediaIdxBtns.length) bookmarkBody.media_indices = selIndices
      }

      // Resolve category name locally so we can apply UI state before the
      // server responds. Server uses the same fallback (first category when
      // category_id is null), so optimistic state matches the eventual reply.
      var localCategory = null
      var matchId = categoryIdNum
      if (matchId == null && bookmarkCategories.length) matchId = bookmarkCategories[0].id
      for (var bci = 0; bci < bookmarkCategories.length; bci++) {
        if (Number(bookmarkCategories[bci].id) === Number(matchId)) {
          localCategory = bookmarkCategories[bci]; break
        }
      }
      var localCategoryName = localCategory ? localCategory.name : ''
      var resolvedCategoryId = localCategory ? localCategory.id : matchId

      // Snapshot prior state for revert on failure.
      var priorBookmarked = stateBool(root, 'bookmarked')
      var priorCategoryAttr = root.getAttribute('data-bookmark-category-id') || ''
      var priorPrefs = null
      if (channelKey) {
        try { priorPrefs = localStorage.getItem('bookmarkAccountPrefsV1') } catch (_) {}
      }

      // Apply optimistic state + close menu before the network roundtrip.
      setStateBool(root, 'bookmarked', true)
      root.setAttribute('data-bookmark-category-id', String(resolvedCategoryId || ''))
      onStateChange(root, true, { id: resolvedCategoryId, name: localCategoryName })
      setLastBookmarkCategoryId(resolvedCategoryId)
      if (channelKey) {
        try {
          var prefs = JSON.parse(localStorage.getItem('bookmarkAccountPrefsV1') || '{}')
          prefs[channelKey] = selOriginals
          localStorage.setItem('bookmarkAccountPrefsV1', JSON.stringify(prefs))
        } catch (_) {}
      }
      closeBookmarkMenu()
      showToast(localCategoryName ? tf('bookmark_saved_to', 'Bookmarked to %1$s', localCategoryName) : t('bookmark_saved', 'Bookmarked'))

      var revert = function () {
        setStateBool(root, 'bookmarked', priorBookmarked)
        root.setAttribute('data-bookmark-category-id', priorCategoryAttr)
        onStateChange(root, priorBookmarked, priorBookmarked ? { id: Number(priorCategoryAttr) || null, name: '' } : null)
        if (channelKey) {
          try {
            if (priorPrefs == null) localStorage.removeItem('bookmarkAccountPrefsV1')
            else localStorage.setItem('bookmarkAccountPrefsV1', priorPrefs)
          } catch (_) {}
        }
        showToast(t('bookmark_save_failed', 'Failed to save bookmark'))
      }

      return apiFetch('/api/bookmark/' + encodeURIComponent(itemId), {
        method: 'POST',
        body: JSON.stringify(bookmarkBody)
      }).then(function (result) {
        if (!result || !result.success) { revert(); return false }
        if (result.sync_version && window.SyncPoller) window.SyncPoller.advance(result.sync_version)
        return true
      }).catch(function () { revert(); return false })
    }

    function selectPill(catId) {
      currentCategoryId = String(catId || currentCategoryId || '')
      setLastBookmarkCategoryId(currentCategoryId)
      Array.prototype.forEach.call(body.querySelectorAll('[data-bookmark-category]'), function (p) {
        p.classList.toggle('selected', p.getAttribute('data-bookmark-category') === currentCategoryId)
      })
    }

    function cyclePill(dir) {
      var pills = body.querySelectorAll('[data-bookmark-category]')
      if (!pills.length) return
      var curIdx = -1
      for (var j = 0; j < pills.length; j++) {
        if (pills[j].classList.contains('selected')) { curIdx = j; break }
      }
      var nextIdx = (curIdx + dir + pills.length) % pills.length
      selectPill(pills[nextIdx].getAttribute('data-bookmark-category'))
    }

    function sheetKeydown(e) {
      if (e.key === 'Tab') { e.preventDefault(); e.stopPropagation(); cyclePill(e.shiftKey ? -1 : 1) }
      var num = parseInt(e.key, 10)
      if (num >= 1 && num <= 9) {
        var mediaIdxBtns2 = body.querySelectorAll('.bookmark-media-idx-btn')
        if (mediaIdxBtns2.length > 0) {
          var idx = num - 1
          if (idx < mediaIdxBtns2.length) { e.preventDefault(); mediaIdxBtns2[idx].click() }
        } else {
          var accountPills = body.querySelectorAll('.bookmark-sheet-account-pill')
          var idx2 = num - 1
          if (idx2 < accountPills.length) {
            e.preventDefault()
            var pill = accountPills[idx2]
            var wasSel = pill.dataset.selected === '1'
            pill.dataset.selected = wasSel ? '0' : '1'
            pill.classList.toggle('active', !wasSel)
          }
        }
      }
    }

    titleInput.addEventListener('keydown', function (e) {
      sheetKeydown(e)
      if (suggestBox.style.display !== 'none') {
        if (e.key === 'ArrowDown') { e.preventDefault(); moveLabelActive(1) }
        else if (e.key === 'ArrowUp') { e.preventDefault(); moveLabelActive(-1) }
        else if (e.key === 'Enter' && _labelActiveIdx >= 0) {
          e.preventDefault()
          var items = suggestBox.querySelectorAll('.bookmark-label-suggestion-item')
          if (_labelActiveIdx < items.length) {
            var activeSpan = items[_labelActiveIdx].querySelector('span')
            titleInput.value = activeSpan ? activeSpan.textContent : items[_labelActiveIdx].firstChild.textContent
            suggestBox.style.display = 'none'
            _labelActiveIdx = -1
          }
          return
        }
      }
      if (e.key === 'Enter') { e.preventDefault(); saveCurrentBookmark() }
    })

    Array.prototype.forEach.call(body.querySelectorAll('[data-bookmark-category]'), function (btn) {
      btn.addEventListener('click', function () {
        selectPill(btn.getAttribute('data-bookmark-category'))
      })
    })

    var removeBtnEl = body.querySelector('[data-bookmark-remove]')
    if (removeBtnEl) {
      removeBtnEl.addEventListener('click', function () {
        removeBookmark(itemId, root).then(function (ok) {
          if (!ok) { setStatus(t('bookmark_remove_failed', 'Failed to remove bookmark.')); return }
          onStateChange(root, false, null)
          closeBookmarkMenu()
          showToast(t('bookmark_removed', 'Bookmark removed'))
        })
      })
    }

    saveBtnEl.addEventListener('click', saveCurrentBookmark)

    function submitCreate() {
      var name = String((addInputEl && addInputEl.value) || '').trim()
      if (!name) { setStatus(t('bookmark_enter_category', 'Enter a category name.')); return }
      apiFetch('/api/bookmark-categories', {
        method: 'POST',
        body: JSON.stringify({ name: name })
      }).then(function (result) {
        if (!result || !result.success || !result.category || !result.category.id) {
          setStatus((result && result.error) ? result.error : t('bookmark_create_category_failed', 'Failed to create category.'))
          return
        }
        loadBookmarkCategories().then(function () {
          currentCategoryId = String(result.category.id)
          setLastBookmarkCategoryId(currentCategoryId)
          saveCurrentBookmark()
        })
      }).catch(function (err) {
        var msg = err && err.payload && err.payload.error ? err.payload.error : t('bookmark_create_category_failed', 'Failed to create category.')
        setStatus(msg)
      })
    }

    addBtnEl.addEventListener('click', submitCreate)
    addInputEl.addEventListener('keydown', function (e) {
      sheetKeydown(e)
      if (e.key === 'Enter') { e.preventDefault(); submitCreate() }
    })

    requestAnimationFrame(function () {
      try { titleInput.focus({ preventScroll: true }) } catch (_) {}
    })
  }

  renderSheet()
}
