// Feed page ES module entry point.
// Wires all feed modules together and handles event delegation.

import { openMediaOverlay } from './media-overlay.js'
import { initInlineMedia } from './inline-media.js'
import { initTextClamps } from './text-clamp.js'
import { initDates } from './dates.js'
import { handleTranslateAction, getTranslateObserver, queueBackgroundTranslations } from './translate.js'
import { initRetweetersDialog, applyRepostMuteFilter } from './retweeters.js'

// Observe cards for auto-translate. Seen tracking is handled by HTMX attrs on the cards.
function observeTranslateCards(scope, translateObserver) {
  queueBackgroundTranslations(scope)
  if (!translateObserver) return
  var root = scope || document.getElementById('feed-list')
  if (!root) return
  root.querySelectorAll('[data-feed-item][data-tweet-id]').forEach(function (node) {
    if (!(node instanceof Element)) return
    if (node.dataset.feedTranslateObserved === '1') return
    node.dataset.feedTranslateObserved = '1'
    var hasAutoLang = node.querySelectorAll('[data-translate-field][data-lang]')
    var needsObserve = false
    hasAutoLang.forEach(function (c) {
      var lang = (c.getAttribute('data-lang') || '').trim()
      if (lang && c.getAttribute('data-translated') !== '1' && !c.querySelector('[data-original-html], [data-original-text]')) needsObserve = true
    })
    if (needsObserve) translateObserver.observe(node)
  })
}
import { openBookmarkMenu, isBookmarkMenuOpen } from '../bookmark-menu.js'
import {
  cssEscape, apiFetch, showToast, copyText, toFxTwitterUrl, askConfirm,
  getFeedActionIconSvg, syncFeedActionIcons, setSvgContent,
  stateBool, setStateBool, itemRootFromNode, t, tf
} from '../utils.js'

var feedList = document.getElementById('feed-list')
var feedPagination = document.getElementById('feed-pagination')
var pendingForms = new WeakSet()
var feedInitialRankDone = false
var feedThreadReturnKey = 'igloo.feed.threadReturn'
var feedThreadHistoryKey = 'iglooFeedThread'
var feedRouteContent = document.querySelector('[data-feed-route-content]') || feedList
var activeThreadRoute = null
var activeThreadAbort = null
var lastFeedThreadRouteState = null

// ── State helpers ──

function feedReturnURL() {
  return window.location.pathname + window.location.search + window.location.hash
}

function writeFeedThreadReturn(tweetId) {
  if (!tweetId || !window.sessionStorage) return
  try {
    window.sessionStorage.setItem(feedThreadReturnKey, JSON.stringify({
      url: feedReturnURL(),
      scrollY: window.scrollY || 0,
      tweetId: tweetId,
      pending: false,
      at: Date.now()
    }))
  } catch (_) {}
}

function readFeedThreadReturn() {
  if (!window.sessionStorage) return null
  try {
    var raw = window.sessionStorage.getItem(feedThreadReturnKey)
    return raw ? JSON.parse(raw) : null
  } catch (_) {
    return null
  }
}

function writeFeedThreadReturnState(state) {
  if (!state || !window.sessionStorage) return
  try { window.sessionStorage.setItem(feedThreadReturnKey, JSON.stringify(state)) } catch (_) {}
}

function markFeedThreadReturnPending() {
  var state = readFeedThreadReturn()
  if (!state) return
  state.pending = true
  writeFeedThreadReturnState(state)
}

function currentHistoryStatePatch(patch) {
  var current = (window.history && window.history.state && typeof window.history.state === 'object') ? window.history.state : {}
  var next = {}
  Object.keys(current).forEach(function (key) { next[key] = current[key] })
  Object.keys(patch || {}).forEach(function (key) { next[key] = patch[key] })
  return next
}

function rememberFeedHistoryState(tweetId) {
  var state = {
    mode: 'feed',
    url: feedReturnURL(),
    scrollY: window.scrollY || 0,
    tweetId: String(tweetId || '').trim()
  }
  lastFeedThreadRouteState = state
  if (window.history && window.history.replaceState) {
    var patch = {}
    patch[feedThreadHistoryKey] = state
    window.history.replaceState(currentHistoryStatePatch(patch), '', state.url)
  }
  return state
}

function partialThreadURL(href, returnURL) {
  var url = new URL(href, window.location.origin)
  url.searchParams.set('fmt', 'partial')
  url.searchParams.set('return', returnURL || '/feed')
  return url.pathname + url.search
}

function canOpenThreadInFeedRoute(event, link) {
  if (!feedRouteContent || !link) return false
  if (event.defaultPrevented || event.button !== 0) return false
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return false
  var target = link.getAttribute('target')
  if (target && target !== '_self') return false
  return true
}

function setFeedRouteHidden(hidden) {
  if (!feedRouteContent) return
  feedRouteContent.hidden = !!hidden
  if (hidden) {
    feedRouteContent.setAttribute('aria-hidden', 'true')
  } else {
    feedRouteContent.removeAttribute('aria-hidden')
  }
}

function removeActiveThreadRoute() {
  if (activeThreadAbort) {
    try { activeThreadAbort.abort() } catch (_) {}
    activeThreadAbort = null
  }
  if (activeThreadRoute) {
    activeThreadRoute.remove()
    activeThreadRoute = null
  }
}

function insertThreadRoute(route, returnState) {
  if (!feedRouteContent || !route) return false
  removeActiveThreadRoute()
  setFeedRouteHidden(true)
  feedRouteContent.insertAdjacentElement('afterend', route)
  activeThreadRoute = route
  lastFeedThreadRouteState = returnState || lastFeedThreadRouteState
  _feedFocusDirty = true
  _focusedFeedCard = null
  initFeedCards(route)
  initThreadBackLink(route)
  window.scrollTo(0, 0)
  return true
}

function renderLoadingThreadRoute(returnHref) {
  var route = document.createElement('div')
  route.className = 'thread-page-shell thread-page-shell-loading'
  route.setAttribute('data-thread-route', '')
  var back = document.createElement('a')
  back.className = 'thread-back-link'
  back.setAttribute('href', returnHref || '/feed')
  back.setAttribute('data-thread-back-link', '')
  back.textContent = t('thread_back_to_feed', '<- Feed')
  var loading = document.createElement('div')
  loading.className = 'thread-route-loading'
  loading.textContent = t('status_loading', 'Loading...')
  route.appendChild(back)
  route.appendChild(loading)
  return route
}

function parseThreadRouteHTML(html) {
  var template = document.createElement('template')
  template.innerHTML = String(html || '').trim()
  var route = template.content.firstElementChild
  if (!route || !route.matches || !route.matches('[data-thread-route]')) return null
  return route
}

function closeThreadRoute(returnState) {
  var state = returnState || lastFeedThreadRouteState || {}
  removeActiveThreadRoute()
  setFeedRouteHidden(false)
  _feedFocusDirty = true
  _focusedFeedCard = null
  var tweetId = String(state.tweetId || '').trim()
  var target = tweetId && feedList ? feedList.querySelector('[data-feed-item][data-tweet-id="' + cssEscape(tweetId) + '"]') : null
  if (target && typeof target.scrollIntoView === 'function') {
    try { target.scrollIntoView({ behavior: 'auto', block: 'center' }) } catch (_) { target.scrollIntoView() }
    return
  }
  if (typeof state.scrollY === 'number') {
    window.scrollTo(0, state.scrollY)
  }
}

function openThreadRoute(href, tweetId, opts) {
  if (!feedRouteContent || !href) return false
  var options = opts || {}
  var returnState = options.returnState || rememberFeedHistoryState(tweetId)
  var threadState = {
    mode: 'thread',
    url: href,
    tweetId: String(tweetId || '').trim(),
    returnState: returnState
  }
  if (options.push !== false && window.history && window.history.pushState) {
    var patch = {}
    patch[feedThreadHistoryKey] = threadState
    window.history.pushState(currentHistoryStatePatch(patch), '', href)
  }

  insertThreadRoute(renderLoadingThreadRoute(returnState.url), returnState)
  if (activeThreadAbort) {
    try { activeThreadAbort.abort() } catch (_) {}
  }
  activeThreadAbort = window.AbortController ? new AbortController() : null
  var fetchOpts = {
    credentials: 'same-origin',
    headers: { 'X-Requested-With': 'fetch' }
  }
  if (activeThreadAbort) fetchOpts.signal = activeThreadAbort.signal

  fetch(partialThreadURL(href, returnState.url), fetchOpts)
    .then(function (response) {
      if (!response.ok) throw new Error('thread partial failed')
      return response.text()
    })
    .then(function (html) {
      var route = parseThreadRouteHTML(html)
      if (!route) throw new Error('thread partial invalid')
      insertThreadRoute(route, returnState)
    })
    .catch(function (err) {
      if (err && err.name === 'AbortError') return
      window.location.assign(href)
    })
  return true
}

function restoreFeedThreadReturn() {
  if (!feedList) return
  var state = readFeedThreadReturn()
  if (!state || !state.pending) return
  state.pending = false
  writeFeedThreadReturnState(state)
  var tweetId = String(state.tweetId || '').trim()
  var target = tweetId ? feedList.querySelector('[data-feed-item][data-tweet-id="' + cssEscape(tweetId) + '"]') : null
  if (target && typeof target.scrollIntoView === 'function') {
    try { target.scrollIntoView({ behavior: 'auto', block: 'center' }) } catch (_) { target.scrollIntoView() }
    return
  }
  if (typeof state.scrollY === 'number') {
    window.scrollTo(0, state.scrollY)
  }
}

function initThreadBackLink(scope) {
  var root = scope || document
  var link = root.querySelector('[data-thread-back-link]')
  if (!link) return
  var state = readFeedThreadReturn()
  if (state && state.url) link.setAttribute('href', state.url)
  link.addEventListener('click', function (event) {
    if (activeThreadRoute && link.closest('[data-thread-route]') === activeThreadRoute) {
      event.preventDefault()
      var routeState = window.history && window.history.state && window.history.state[feedThreadHistoryKey]
      if (routeState && routeState.mode === 'thread' && window.history && window.history.back) {
        window.history.back()
      } else {
        closeThreadRoute(lastFeedThreadRouteState)
      }
      return
    }
    markFeedThreadReturnPending()
  })
}

function isCurrentChannelPath(channelId) {
  var cid = String(channelId || '').trim()
  if (!cid) return false
  var path = window.location && window.location.pathname ? window.location.pathname : ''
  return path === '/channels/' + cid || path === '/channels/' + encodeURIComponent(cid)
}

function getFeedButton(root, actionName) {
  if (!root) return null
  return root.querySelector('button[data-feed-action-button="' + actionName + '"]')
}

function getFeedActionUiButton(root, actionName) {
  if (!root) return null
  return root.querySelector('[data-feed-action="' + actionName + '"]')
}

function syncFeedActionUiButtons(root) {
  if (!root) return
  var isLiked = stateBool(root, 'liked')
  var isBookmarked = stateBool(root, 'bookmarked')
  var heartBtn = getFeedActionUiButton(root, 'heart')
  var bookmarkBtn = getFeedActionUiButton(root, 'bookmark')
  if (heartBtn) {
    heartBtn.classList.toggle('active', isLiked)
    setSvgContent(heartBtn, getFeedActionIconSvg('heart', isLiked))
    heartBtn.title = isLiked ? t('action_unlike', 'Unlike') : t('action_like', 'Like')
    heartBtn.setAttribute('aria-label', heartBtn.title)
    var tweetId = root.getAttribute('data-tweet-id')
    if (tweetId && (heartBtn.hasAttribute('hx-post') || heartBtn.hasAttribute('hx-delete'))) {
      heartBtn.removeAttribute('hx-post')
      heartBtn.removeAttribute('hx-delete')
      if (isLiked) heartBtn.setAttribute('hx-delete', '/api/feed/like/' + tweetId)
      else heartBtn.setAttribute('hx-post', '/api/feed/like/' + tweetId)
      if (typeof htmx !== 'undefined') htmx.process(heartBtn)
    }
  }
  if (bookmarkBtn) {
    bookmarkBtn.classList.toggle('active', isBookmarked)
    setSvgContent(bookmarkBtn, getFeedActionIconSvg('bookmark', isBookmarked))
    bookmarkBtn.title = isBookmarked ? t('action_unbookmark', 'Unbookmark') : t('action_bookmark', 'Bookmark')
    bookmarkBtn.setAttribute('aria-label', bookmarkBtn.title)
  }
  syncFeedActionIcons(root)
}

function syncOverlayActionsForRoot(root) {
  var _ov = window.FeedMediaOverlay && window.FeedMediaOverlay.element
  if (!root || !_ov) return
  if (String(_ov.getAttribute('data-feed-overlay-quote-tweet-id') || '').trim()) return
  var tweetId = String(root.getAttribute('data-tweet-id') || '').trim()
  if (!tweetId) return
  if (String(_ov.getAttribute('data-feed-overlay-tweet-id') || '').trim() !== tweetId) return
  var isLiked = stateBool(root, 'liked')
  var isBookmarked = stateBool(root, 'bookmarked')
  var heartBtn = _ov.querySelector('[data-feed-overlay-action="heart"]')
  var bookmarkBtn = _ov.querySelector('[data-feed-overlay-action="bookmark"]')
  if (heartBtn) {
    heartBtn.classList.toggle('active', isLiked)
    setSvgContent(heartBtn, getFeedActionIconSvg('heart', isLiked))
    heartBtn.title = isLiked ? t('action_unlike', 'Unlike') : t('action_like', 'Like')
    heartBtn.setAttribute('aria-label', heartBtn.title)
  }
  if (bookmarkBtn) {
    bookmarkBtn.classList.toggle('active', isBookmarked)
    setSvgContent(bookmarkBtn, getFeedActionIconSvg('bookmark', isBookmarked))
    bookmarkBtn.title = isBookmarked ? t('action_unbookmark', 'Unbookmark') : t('action_bookmark', 'Bookmark')
    bookmarkBtn.setAttribute('aria-label', bookmarkBtn.title)
  }
  syncFeedActionIcons(_ov)
}

function syncFeedButtons(root) {
  if (!root) return
  var likeBtn = getFeedButton(root, 'like')
  var bookmarkBtn = getFeedButton(root, 'bookmark')
  var isLiked = stateBool(root, 'liked')
  var isBookmarked = stateBool(root, 'bookmarked')
  if (likeBtn) likeBtn.textContent = isLiked ? t('action_unlike', 'Unlike') : t('action_like', 'Like')
  if (bookmarkBtn) bookmarkBtn.textContent = isBookmarked ? t('action_unbookmark', 'Unbookmark') : t('action_bookmark', 'Bookmark')
  syncFeedActionUiButtons(root)
  syncOverlayActionsForRoot(root)
}

function syncSiblingCards(root) {
  if (!root || !feedList) return
  var hash = String(root.getAttribute('data-content-hash') || '').trim()
  if (!hash) return
  var isLiked = stateBool(root, 'liked')
  var isBookmarked = stateBool(root, 'bookmarked')
  var siblings = feedList.querySelectorAll('[data-feed-item][data-content-hash="' + CSS.escape(hash) + '"]')
  for (var i = 0; i < siblings.length; i++) {
    if (siblings[i] === root) continue
    setStateBool(siblings[i], 'liked', isLiked)
    setStateBool(siblings[i], 'bookmarked', isBookmarked)
    syncFeedButtons(siblings[i])
  }
}

function propagateLikeState(tweetId, isLiked) {
  if (!feedList) return
  var contentHash = ''
  var allIds = [tweetId]

  var byId = feedList.querySelectorAll('[data-feed-item][data-tweet-id="' + cssEscape(tweetId) + '"]')
  Array.prototype.forEach.call(byId, function (card) {
    setStateBool(card, 'liked', isLiked)
    syncFeedButtons(card)
    if (!contentHash) contentHash = card.getAttribute('data-content-hash') || ''
  })

  if (contentHash) {
    var byHash = feedList.querySelectorAll('[data-feed-item][data-content-hash="' + cssEscape(contentHash) + '"]')
    Array.prototype.forEach.call(byHash, function (card) {
      var sibId = card.getAttribute('data-tweet-id')
      if (sibId && allIds.indexOf(sibId) < 0) allIds.push(sibId)
      if (sibId === tweetId) return
      setStateBool(card, 'liked', isLiked)
      syncFeedButtons(card)
    })
  }

  for (var q = 0; q < allIds.length; q++) {
    var quoteCards = feedList.querySelectorAll('.feed-quote-card[data-quote-tweet-id="' + cssEscape(allIds[q]) + '"]')
    Array.prototype.forEach.call(quoteCards, function (qc) {
      qc.setAttribute('data-quote-liked', isLiked ? '1' : '0')
    })
  }

  var _ov = window.FeedMediaOverlay && window.FeedMediaOverlay.element
  if (_ov) {
    var overlayTweetId = _ov.getAttribute('data-feed-overlay-tweet-id') || ''
    var overlayQuoteId = _ov.getAttribute('data-feed-overlay-quote-tweet-id') || ''
    var overlayMatch = false
    for (var o = 0; o < allIds.length; o++) {
      if (overlayTweetId === allIds[o] || overlayQuoteId === allIds[o]) { overlayMatch = true; break }
    }
    if (overlayMatch) {
      var overlayHeart = _ov.querySelector('[data-feed-overlay-action="heart"]')
      if (overlayHeart) {
        overlayHeart.classList.toggle('active', isLiked)
        setSvgContent(overlayHeart, getFeedActionIconSvg('heart', isLiked))
        overlayHeart.title = isLiked ? t('action_unlike', 'Unlike') : t('action_like', 'Like')
        overlayHeart.setAttribute('aria-label', overlayHeart.title)
      }
    }
  }
}

function applyLikeState(root, tweetId, isLiked) {
  if (root) {
    setStateBool(root, 'liked', isLiked)
    syncFeedButtons(root)
    syncSiblingCards(root)
  }
  if (tweetId) propagateLikeState(tweetId, isLiked)
}

function resolveFeedRootForActionNode(node) {
  var direct = itemRootFromNode(node)
  if (direct) return direct
  var overlay = node && node.closest ? node.closest('.feed-media-overlay[data-feed-overlay-tweet-id]') : null
  if (!overlay) return null
  var tweetId = String(overlay.getAttribute('data-feed-overlay-tweet-id') || '').trim()
  if (!tweetId || !feedList) return null
  return feedList.querySelector('[data-feed-item][data-tweet-id="' + cssEscape(tweetId) + '"]')
}

// ── Action handlers (JSON API for keyboard shortcuts + overlay) ──

function setFormBusy(form, busy) {
  if (!form) return
  form.querySelectorAll('button').forEach(function (btn) { btn.disabled = !!busy })
}

function feedItemPayloadFromForm(form, tweetId) {
  var fd = new FormData(form)
  return {
    tweet_id: String(tweetId || ''),
    link: String(fd.get('link') || ''),
    canonical_x_link: String(fd.get('canonical_x_link') || ''),
    source_handle: String(fd.get('source_handle') || ''),
    author_handle: String(fd.get('author_handle') || ''),
    title: String(fd.get('title') || ''),
    body_text: String(fd.get('body_text') || '')
  }
}

function runFeedAction(root, actionType, form) {
  var tweetId = root ? String(root.getAttribute('data-tweet-id') || '').trim() : ''
  if (!root || !tweetId) return Promise.reject(new Error('missing tweet id'))

  if (actionType === 'like') {
    var currentlyLiked = stateBool(root, 'liked')
    var method = currentlyLiked ? 'DELETE' : 'POST'
    applyLikeState(root, tweetId, !currentlyLiked)
    return apiFetch('/api/feed/like/' + encodeURIComponent(tweetId), {
      method: method,
      body: method === 'POST' ? JSON.stringify({ item: feedItemPayloadFromForm(form, tweetId) }) : undefined
    }).then(function (payload) {
      var nextLiked = payload && typeof payload.is_liked === 'boolean' ? payload.is_liked : !currentlyLiked
      applyLikeState(root, tweetId, nextLiked)
      if (payload && payload.sync_version && window.SyncPoller) window.SyncPoller.advance(payload.sync_version)
    }).catch(function (err) {
      applyLikeState(root, tweetId, currentlyLiked)
      throw err
    })
  }
  if (actionType === 'bookmark') {
    var currentlyBookmarked = stateBool(root, 'bookmarked')
    var bmethod = currentlyBookmarked ? 'DELETE' : 'POST'
    return apiFetch('/api/bookmark/' + encodeURIComponent(tweetId), {
      method: bmethod,
      body: bmethod === 'POST' ? JSON.stringify({}) : undefined
    }).then(function (payload) {
      var nextBookmarked = payload && typeof payload.bookmarked === 'boolean' ? payload.bookmarked : !currentlyBookmarked
      setStateBool(root, 'bookmarked', nextBookmarked)
      syncFeedButtons(root)
      syncSiblingCards(root)
      if (payload && payload.sync_version && window.SyncPoller) window.SyncPoller.advance(payload.sync_version)
    })
  }
  return Promise.reject(new Error('unsupported feed action'))
}

function findFallbackForm(root, actionType) {
  if (!root) return null
  return root.querySelector('form[data-feed-action-form="' + actionType + '"]')
}

function triggerFeedActionUi(root, actionType, triggerBtn) {
  if (!root) return
  var form = findFallbackForm(root, actionType)
  if (!(form instanceof HTMLFormElement)) return
  if (pendingForms.has(form)) return
  pendingForms.add(form)
  setFormBusy(form, true)
  if (triggerBtn) triggerBtn.disabled = true
  runFeedAction(root, actionType, form)
    .then(function () {
      if (actionType === 'like') showToast(stateBool(root, 'liked') ? t('toast_liked', 'Liked') : t('toast_unliked', 'Unliked'))
      if (actionType === 'bookmark') showToast(stateBool(root, 'bookmarked') ? t('bookmark_saved', 'Bookmarked') : t('bookmark_removed', 'Bookmark removed'))
    })
    .catch(function () { form.submit() })
    .finally(function () {
      pendingForms.delete(form)
      setFormBusy(form, false)
      if (triggerBtn) triggerBtn.disabled = false
    })
}

function handleFeedActionSubmit(form) {
  if (!(form instanceof HTMLFormElement)) return
  var actionType = String(form.getAttribute('data-feed-action-form') || '').trim()
  if (!actionType) return
  var root = itemRootFromNode(form)
  var tweetId = root ? String(root.getAttribute('data-tweet-id') || '').trim() : ''
  if (!root || !tweetId) return
  if (pendingForms.has(form)) return
  pendingForms.add(form)
  setFormBusy(form, true)
  runFeedAction(root, actionType, form).catch(function () {
    form.submit()
  }).finally(function () {
    pendingForms.delete(form)
    setFormBusy(form, false)
  })
}

// Quote overlay direct API actions
function runQuoteOverlayLike(quoteTweetId, btn) {
  if (!quoteTweetId || btn.disabled) return
  var isLiked = btn.classList.contains('active')
  var method = isLiked ? 'DELETE' : 'POST'
  btn.disabled = true
  propagateLikeState(quoteTweetId, !isLiked)
  apiFetch('/api/feed/like/' + encodeURIComponent(quoteTweetId), {
    method: method,
    body: method === 'POST' ? JSON.stringify({ item: { tweet_id: quoteTweetId } }) : undefined
  }).then(function (payload) {
    var nextLiked = payload && typeof payload.is_liked === 'boolean' ? payload.is_liked : !isLiked
    propagateLikeState(quoteTweetId, nextLiked)
    showToast(nextLiked ? t('toast_liked', 'Liked') : t('toast_unliked', 'Unliked'))
    if (payload && payload.sync_version && window.SyncPoller) window.SyncPoller.advance(payload.sync_version)
  }).catch(function () {
    propagateLikeState(quoteTweetId, isLiked)
    showToast(t('logs_status_failed', 'Failed'))
  }).finally(function () { btn.disabled = false })
}

// ── Bookmark menu integration ──

function onBookmarkStateChange(root) {
  syncFeedButtons(root)
  syncSiblingCards(root)
  if (typeof root._onBookmarkChange === 'function') root._onBookmarkChange()
}

function openFeedBookmarkMenu(anchorEl, root) {
  var tweetId = String(root.getAttribute('data-tweet-id') || '').trim()
  var bodyText = ((root.querySelector('.feed-body-text') || {}).textContent || '')
    + ' ' + ((root.querySelector('.feed-quote-text') || {}).textContent || '')
  var title = String((root.querySelector('.feed-body-text') || {}).textContent || '').trim()
    || String((root.querySelector('.feed-text') || {}).textContent || '').trim()
    || String((root.querySelector('.feed-summary') || {}).textContent || '').trim()
    || String(root.getAttribute('data-feed-author') || '').trim()
  openBookmarkMenu(anchorEl, root, {
    tweetId: tweetId,
    bodyText: bodyText,
    titleFallback: title,
    onStateChange: onBookmarkStateChange,
  })
}

// ── Retweet muting (localStorage-based) ──

var retweetMuteStorageKey = 'feedMutedRetweetChannels'
var legacyRetweetMuteStorageKey = 'mpa-feed-retweet-muted:v1'

function getRetweetMutedChannels() {
  var raw = ''
  try {
    raw = localStorage.getItem(retweetMuteStorageKey) || ''
    if (!raw) raw = localStorage.getItem(legacyRetweetMuteStorageKey) || ''
  } catch (_) { raw = '' }
  if (!raw) return new Set()
  try {
    var parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return new Set()
    return new Set(parsed.map(function (v) { return String(v || '').trim() }).filter(Boolean))
  } catch (_) { return new Set() }
}

function saveRetweetMutedChannels(set) {
  try {
    localStorage.setItem(retweetMuteStorageKey, JSON.stringify(Array.from(set || [])))
    localStorage.setItem(legacyRetweetMuteStorageKey, JSON.stringify(Array.from(set || [])))
  } catch (_) {}
}

function isRetweetMutedChannel(channelId) {
  return getRetweetMutedChannels().has(String(channelId || '').trim())
}

function setRetweetMutedChannel(channelId, muted) {
  var cid = String(channelId || '').trim()
  if (!cid) return
  var set = getRetweetMutedChannels()
  if (muted) set.add(cid); else set.delete(cid)
  saveRetweetMutedChannels(set)
}

function updateRetweetMenuLabels(scope) {
  (scope || document).querySelectorAll('[data-feed-menu-action="retweets_off"]').forEach(function (btn) {
    var channelId = String(btn.getAttribute('data-feed-channel-id') || '').trim()
    btn.textContent = isRetweetMutedChannel(channelId)
      ? t('feed_turn_on_retweets', 'Turn on retweets')
      : t('feed_turn_off_retweets', 'Turn off retweets')
  })
}

function applyRetweetMuteFilter(scope) {
  var muteSet = getRetweetMutedChannels()
  ;(scope || document).querySelectorAll('[data-feed-item]').forEach(function (item) {
    var hide = false

    var isRetweet = String(item.getAttribute('data-feed-is-retweet') || '') === '1'
    var sourceChannelId = String(item.getAttribute('data-source-channel-id') || '').trim()
    var authorChannelId = String(item.getAttribute('data-feed-author-channel-id') || item.getAttribute('data-channel-id') || '').trim()
    var quoteTweetId = String(item.getAttribute('data-feed-quote-tweet-id') || '').trim()
    var quoteAuthorChannelId = String(item.getAttribute('data-feed-quote-author-channel-id') || '').trim()

    // Pure RT path
    if (isRetweet && sourceChannelId && sourceChannelId !== authorChannelId && muteSet.has(sourceChannelId)) {
      var rtsRaw = item.getAttribute('data-feed-retweeters') || '[]'
      var rts = []
      try { rts = JSON.parse(rtsRaw) || [] } catch (_) { rts = [] }
      var allMuted = true
      if (rts.length === 0) {
        // No multi-retweeter info — single source. Already-known: source is muted.
        allMuted = true
      } else {
        for (var i = 0; i < rts.length; i++) {
          if (!muteSet.has(rts[i])) { allMuted = false; break }
        }
      }
      if (allMuted) hide = true
    }

    // Quote path
    if (!hide && quoteTweetId && authorChannelId && quoteAuthorChannelId && authorChannelId !== quoteAuthorChannelId && muteSet.has(authorChannelId)) {
      hide = true
    }

    item.classList.toggle('feed-card-muted-retweet', !!hide)
    item.style.display = hide ? 'none' : ''
  })
  updateRetweetMenuLabels(scope || document)
}

// ── Follow/unfollow ──

function syncFollowButtons(channelId, following) {
  var cid = String(channelId || '').trim()
  if (!cid) return
  if (window.MpaSiteBase && typeof window.MpaSiteBase.syncChannelFollowState === 'function') {
    window.MpaSiteBase.syncChannelFollowState(cid, following)
    return
  }
  document.querySelectorAll('[data-feed-follow-toggle][data-feed-channel-id]').forEach(function (btn) {
    if (String(btn.getAttribute('data-feed-channel-id') || '').trim() !== cid) return
    btn.setAttribute('data-following', following ? '1' : '0')
    btn.classList.toggle('following', !!following)
    btn.textContent = following ? t('action_following', 'Following') : t('action_follow', 'Follow')
  })
  document.querySelectorAll('[data-feed-menu-action="unfollow"][data-feed-channel-id]').forEach(function (btn) {
    if (String(btn.getAttribute('data-feed-channel-id') || '').trim() !== cid) return
    btn.style.display = following ? '' : 'none'
  })
}

function unfollowChannel(channelId, label) {
  var cid = String(channelId || '').trim()
  if (!cid) return Promise.resolve(false)
  return askConfirm({
    title: t('confirm_unfollow_channel_title', 'Unfollow Channel'),
    body: tf('confirm_unfollow_channel_delete_media_body', 'Unfollow "%1$s" and delete local media? This cannot be undone.', String(label || cid)),
    confirmLabel: t('action_unfollow', 'Unfollow'),
    cancelLabel: t('action_cancel', 'Cancel'),
    danger: true
  }).then(function (confirmed) {
    if (!confirmed) return false
    syncFollowButtons(cid, false)
    return apiFetch('/api/unsubscribe/' + encodeURIComponent(cid) + '?delete_files=true', { method: 'DELETE' })
      .then(function (payload) {
        showToast((payload && payload.message) || tf('toast_unfollowed_channel', 'Unfollowed %1$s', String(label || cid)))
        if (payload && payload.sync_version && window.SyncPoller) window.SyncPoller.advance(payload.sync_version)
        return true
      })
      .catch(function (err) {
        syncFollowButtons(cid, true)
        showToast((err && err.payload && err.payload.error) ? err.payload.error : t('error_unfollow_failed', 'Failed to unfollow'))
        return false
      })
  })
}

function followChannel(channelId, handle, label) {
  var cid = String(channelId || '').trim()
  var cleanHandle = String(handle || '').trim().replace(/^@+/, '')
  if (!cid || !cleanHandle) return Promise.resolve(false)
  return apiFetch('/api/subscribe', {
    method: 'POST',
    body: JSON.stringify({ url: 'https://x.com/' + cleanHandle })
  }).then(function (payload) {
    if (payload && payload.success === false) throw Object.assign(new Error('subscribe failed'), { payload: payload })
    syncFollowButtons(cid, true)
    showToast(tf('toast_followed_channel', 'Followed %1$s', String(label || cleanHandle)))
    if (payload && payload.sync_version && window.SyncPoller) window.SyncPoller.advance(payload.sync_version)
    return true
  }).catch(function (err) {
    showToast((err && err.payload && err.payload.error) ? err.payload.error : t('error_follow_failed', 'Failed to follow'))
    return false
  })
}

function handleFeedFollowToggle(btn) {
  if (!btn || btn.disabled) return
  var channelId = String(btn.getAttribute('data-feed-channel-id') || '').trim()
  var handle = String(btn.getAttribute('data-feed-handle') || '').trim().replace(/^@+/, '')
  var label = String(btn.getAttribute('data-feed-label') || handle || channelId || 'account').trim()
  if (!channelId || !handle) return
  var following = String(btn.getAttribute('data-following') || '') === '1'
  btn.disabled = true
  var op = following ? unfollowChannel(channelId, label) : followChannel(channelId, handle, label)
  Promise.resolve(op).finally(function () { btn.disabled = false })
}

// ── Three-dot menu ──

function closeAllFeedMenus(exceptMenu) {
  document.querySelectorAll('[data-feed-menu].open').forEach(function (menu) {
    if (exceptMenu && menu === exceptMenu) return
    menu.classList.remove('open')
  })
}

function toggleFeedMenu(menuRoot) {
  if (!menuRoot) return
  var willOpen = !menuRoot.classList.contains('open')
  closeAllFeedMenus(menuRoot)
  menuRoot.classList.toggle('open', willOpen)
  updateRetweetMenuLabels(menuRoot)
}

function handleFeedMenuAction(btn) {
  if (!btn) return
  var action = String(btn.getAttribute('data-feed-menu-action') || '').trim()
  var channelId = String(btn.getAttribute('data-feed-channel-id') || '').trim()
  var label = String(btn.getAttribute('data-feed-label') || channelId || 'account').trim()
  if (!action) return
  var menuRoot = btn.closest('[data-feed-menu]')
  var finish = function () { if (menuRoot) menuRoot.classList.remove('open') }

  if (action === 'mute') {
    if (btn.hasAttribute('hx-post')) { finish(); return }
    var muteHandle = String(btn.getAttribute('data-mute-handle') || '').trim()
    if (!muteHandle) { finish(); return }
    btn.disabled = true
    apiFetch('/api/feed/mute/' + encodeURIComponent(muteHandle), { method: 'POST' })
      .then(function () {
        var lower = muteHandle.toLowerCase()
        document.querySelectorAll('[data-feed-item]').forEach(function (card) {
          var a = (card.getAttribute('data-author-handle') || '').toLowerCase()
          var qa = (card.getAttribute('data-quote-author-handle') || '').toLowerCase()
          if (a === lower || qa === lower) card.remove()
        })
        showToast(tf('toast_muted_account', 'Muted @%1$s', muteHandle))
      })
      .catch(function () { showToast(t('error_mute_account_failed', 'Failed to mute account')) })
      .finally(function () { btn.disabled = false; finish() })
    return
  }

  if (!channelId) return

  if (action === 'retweets_off') {
    var nextMuted = !isRetweetMutedChannel(channelId)
    setRetweetMutedChannel(channelId, nextMuted)
    applyRetweetMuteFilter(feedList || document)
    applyRepostMuteFilter(feedList || document)
    updateRetweetMenuLabels(document)
    var _ov = window.FeedMediaOverlay && window.FeedMediaOverlay.element
    if (nextMuted && _ov) {
      var overlayTweetId = String(_ov.getAttribute('data-feed-overlay-tweet-id') || '').trim()
      var overlayRoot = overlayTweetId && feedList
        ? feedList.querySelector('[data-feed-item][data-tweet-id="' + cssEscape(overlayTweetId) + '"]')
        : null
      var ovIsRT = String(overlayRoot && overlayRoot.getAttribute('data-feed-is-retweet') || '') === '1'
      var ovSrc = String(overlayRoot && (overlayRoot.getAttribute('data-source-channel-id') || overlayRoot.getAttribute('data-channel-id')) || '').trim()
      var ovAuth = String(overlayRoot && overlayRoot.getAttribute('data-feed-author-channel-id') || '').trim()
      var ovQuote = String(overlayRoot && overlayRoot.getAttribute('data-feed-quote-tweet-id') || '').trim()
      var ovQuoteAuth = String(overlayRoot && overlayRoot.getAttribute('data-feed-quote-author-channel-id') || '').trim()
      var hideOverlay = false
      if (ovIsRT && ovSrc === channelId) hideOverlay = true
      if (ovQuote && ovAuth === channelId && ovAuth !== ovQuoteAuth) hideOverlay = true
      if (overlayRoot && hideOverlay) {
        if (window.FeedMediaOverlay) window.FeedMediaOverlay.close()
      }
    }
    apiFetch('/api/channels/' + encodeURIComponent(channelId) + '/settings', {
      method: 'POST',
      body: JSON.stringify({ include_reposts: !nextMuted })
    }).catch(function () { /* localStorage still applied; server sync best-effort */ })
    showToast(nextMuted
      ? t('toast_retweets_disabled_for_account', 'Retweets disabled for this account')
      : t('toast_retweets_enabled_for_account', 'Retweets enabled for this account'))
    finish(); return
  }

  if (action === 'media_only') {
    if (window.MpaSiteBase && typeof window.MpaSiteBase.openChannelSettingsModal === 'function') {
      window.MpaSiteBase.openChannelSettingsModal({ channelId: channelId, channelName: label, platform: 'twitter' })
    } else { showToast(t('error_channel_settings_unavailable', 'Channel settings unavailable')) }
    finish(); return
  }

  if (action === 'refresh') {
    btn.disabled = true
    apiFetch('/api/channels/' + encodeURIComponent(channelId) + '/refresh', {
      method: 'POST', body: JSON.stringify({})
    }).then(function (payload) {
      showToast((payload && payload.message) || tf('toast_refreshed_channel', 'Refreshed %1$s', label))
      if (isCurrentChannelPath(channelId)) window.location.reload()
    }).catch(function (err) {
      showToast((err && err.payload && err.payload.error) ? err.payload.error : t('error_refresh_failed', 'Refresh failed'))
    }).finally(function () { btn.disabled = false; finish() })
    return
  }

  if (action === 'unfollow') {
    btn.disabled = true
    unfollowChannel(channelId, label).finally(function () { btn.disabled = false; finish() })
    return
  }

  finish()
}

// ── Card initialization ──

function removeEmptyStateIfNeeded() {
  if (!feedList) return
  var emptyDiv = feedList.querySelector('.empty-state')
  if (emptyDiv && feedList.querySelector('[data-feed-item]')) emptyDiv.remove()
}

function initFeedCards(scope) {
  var container = scope || feedList
  if (!container) return
  initTextClamps(container)
  initDates(container)
  initInlineMedia(container)
  container.querySelectorAll('[data-feed-item]').forEach(function (item) {
    syncFeedButtons(item)
    item.setAttribute('data-fi', '1')
  })
  applyRetweetMuteFilter(container)
  applyRepostMuteFilter(container)
  observeTranslateCards(container, getTranslateObserver())
  if (!feedInitialRankDone && (!scope || scope === feedList)) {
    feedInitialRankDone = true
    // Server snapshot now handles diversity + jitter; no client-side rerank needed.
  }
}

// ── Event delegation: form submit ──

document.addEventListener('submit', function (event) {
  var form = event.target
  if (!(form instanceof HTMLFormElement)) return
  if (!form.hasAttribute('data-feed-action-form')) return
  event.preventDefault()
  handleFeedActionSubmit(form)
})

// ── Event delegation: clicks ──

document.addEventListener('click', function (event) {
  var threadOpen = event.target && event.target.closest ? event.target.closest('[data-feed-thread-open]') : null
  if (threadOpen) {
    var tweetId = String(threadOpen.getAttribute('data-thread-tweet-id') || '').trim()
    var threadHref = threadOpen.getAttribute('href')
    if (tweetId) writeFeedThreadReturn(tweetId)
    if (threadHref && canOpenThreadInFeedRoute(event, threadOpen)) {
      event.preventDefault()
      event.stopPropagation()
      if (openThreadRoute(threadHref, tweetId)) return
    }
    return
  }

  // Three-dot menu toggle
  var menuToggle = event.target && event.target.closest ? event.target.closest('[data-feed-menu-toggle]') : null
  if (menuToggle) {
    event.preventDefault(); event.stopPropagation()
    var menuRoot = menuToggle.closest('[data-feed-menu]')
    if (menuRoot) toggleFeedMenu(menuRoot)
    return
  }

  // Menu action
  var menuAction = event.target && event.target.closest ? event.target.closest('[data-feed-menu-action]') : null
  if (menuAction) {
    event.preventDefault(); event.stopPropagation()
    handleFeedMenuAction(menuAction)
    return
  }

  // Follow toggle (skip HTMX-handled buttons)
  var followBtn = event.target && event.target.closest ? event.target.closest('[data-feed-follow-toggle]') : null
  if (followBtn) {
    if (followBtn.hasAttribute('hx-post') || followBtn.hasAttribute('hx-delete')) return
    event.preventDefault(); event.stopPropagation()
    handleFeedFollowToggle(followBtn)
    return
  }

  // Close menus when clicking outside
  if (!(event.target && event.target.closest && event.target.closest('[data-feed-menu]'))) {
    closeAllFeedMenus()
  }

  // Translation
  var translateBtn = event.target && event.target.closest ? event.target.closest('.feed-translate-btn[data-feed-action="translate"]') : null
  if (translateBtn) {
    event.preventDefault(); event.stopPropagation()
    var tCard = translateBtn.closest('[data-feed-item]')
    if (tCard) handleTranslateAction(tCard, translateBtn)
    return
  }

  // Feed action buttons (heart, bookmark, share)
  var actionBtn = event.target && event.target.closest ? event.target.closest('.feed-action-btn[data-feed-action]') : null
  if (actionBtn) {
    var action = String(actionBtn.getAttribute('data-feed-action') || '').trim()
    if (action && action !== 'openx') {
      event.preventDefault(); event.stopPropagation()
      var root = resolveFeedRootForActionNode(actionBtn)
      if (!root) return
      if (action === 'share') {
        var link = String(root.getAttribute('data-feed-link') || '').trim()
        var shareLink = toFxTwitterUrl(link) || link
        copyText(shareLink).then(function () { showToast(t('toast_link_copied', 'Link copied')) }).catch(function () { showToast(t('error_copy_link_failed', 'Failed to copy link')) })
        var shareTweetId = String(root.getAttribute('data-tweet-id') || '').trim()
        if (shareTweetId) {
	          apiFetch('/api/feed/interaction', {
	            method: 'POST',
	            body: JSON.stringify({
	              action: 'share',
	              item: {
	                tweet_id: shareTweetId,
	                source_handle: String(root.getAttribute('data-source-handle') || '').trim(),
	                author_handle: String(root.getAttribute('data-author-handle') || '').trim()
	              }
	            })
	          }).catch(function () {})
        }
        return
      }
      if (action === 'heart') {
        if (actionBtn.hasAttribute('hx-post') || actionBtn.hasAttribute('hx-delete')) return
        triggerFeedActionUi(root, 'like', actionBtn)
        return
      }
      if (action === 'bookmark') {
        openFeedBookmarkMenu(actionBtn, root)
        return
      }
    }
  }

  // Overlay action buttons
  var overlayActionBtn = event.target && event.target.closest ? event.target.closest('[data-feed-overlay-action]') : null
  if (overlayActionBtn) {
    var overlayAction = String(overlayActionBtn.getAttribute('data-feed-overlay-action') || '').trim()
    if (overlayAction && overlayAction !== 'openx') {
      event.preventDefault(); event.stopPropagation()
      var overlayEl = overlayActionBtn.closest('.feed-media-overlay')
      var overlayQuoteTweetId = overlayEl ? String(overlayEl.getAttribute('data-feed-overlay-quote-tweet-id') || '').trim() : ''

      if (overlayQuoteTweetId) {
        if (overlayAction === 'share') {
          var qCard = feedList && feedList.querySelector('.feed-quote-card[data-quote-tweet-id="' + cssEscape(overlayQuoteTweetId) + '"]')
          var qLink = qCard ? String(qCard.getAttribute('data-quote-link') || '').trim() : ''
          var qShareLink = toFxTwitterUrl(qLink) || qLink
          if (qShareLink) copyText(qShareLink).then(function () { showToast(t('toast_link_copied', 'Link copied')) }).catch(function () { showToast(t('error_copy_link_failed', 'Failed to copy link')) })
          return
        }
        if (overlayAction === 'heart') {
          runQuoteOverlayLike(overlayQuoteTweetId, overlayActionBtn)
          return
        }
        if (overlayAction === 'bookmark') {
          var qCard2 = feedList && feedList.querySelector('.feed-quote-card[data-quote-tweet-id="' + cssEscape(overlayQuoteTweetId) + '"]')
          var parentRoot = resolveFeedRootForActionNode(overlayActionBtn)
          var syntheticRoot = document.createElement('div')
          syntheticRoot.setAttribute('data-tweet-id', overlayQuoteTweetId)
          syntheticRoot.setAttribute('data-bookmarked', qCard2 ? (qCard2.getAttribute('data-quote-bookmarked') || '0') : '0')
          syntheticRoot.setAttribute('data-bookmark-category-id', '')
          var qAuthor = parentRoot ? (parentRoot.getAttribute('data-quote-author-handle') || '') : ''
          syntheticRoot.setAttribute('data-author-handle', qAuthor)
          syntheticRoot.setAttribute('data-source-handle', qAuthor)
          syntheticRoot.setAttribute('data-feed-is-retweet', '0')
          syntheticRoot.setAttribute('data-feed-item', '')
          var qBodyEl = qCard2 && qCard2.querySelector('.feed-quote-text')
          if (qBodyEl) {
            var synthBody = document.createElement('p')
            synthBody.className = 'feed-body-text'
            synthBody.textContent = qBodyEl.textContent
            syntheticRoot.appendChild(synthBody)
          }
          var _overlayBtnRef = overlayActionBtn
          var _qCardRef = qCard2
          syntheticRoot._onBookmarkChange = function () {
            var nextBm = syntheticRoot.getAttribute('data-bookmarked') === '1'
            if (_qCardRef) _qCardRef.setAttribute('data-quote-bookmarked', nextBm ? '1' : '0')
            _overlayBtnRef.classList.toggle('active', nextBm)
            setSvgContent(_overlayBtnRef, getFeedActionIconSvg('bookmark', nextBm))
            _overlayBtnRef.title = nextBm ? t('action_unbookmark', 'Unbookmark') : t('action_bookmark', 'Bookmark')
            _overlayBtnRef.setAttribute('aria-label', _overlayBtnRef.title)
          }
          openFeedBookmarkMenu(_overlayBtnRef, syntheticRoot)
          return
        }
        return
      }

      var oRoot = resolveFeedRootForActionNode(overlayActionBtn)
      if (!oRoot) return
      if (overlayAction === 'share') {
        var oLink = String(oRoot.getAttribute('data-feed-link') || '').trim()
        var oShareLink = toFxTwitterUrl(oLink) || oLink
        copyText(oShareLink).then(function () { showToast(t('toast_link_copied', 'Link copied')) }).catch(function () { showToast(t('error_copy_link_failed', 'Failed to copy link')) })
        return
      }
      if (overlayAction === 'heart') {
        triggerFeedActionUi(oRoot, 'like', overlayActionBtn)
        return
      }
      if (overlayAction === 'bookmark') {
        openFeedBookmarkMenu(overlayActionBtn, oRoot)
        return
      }
    }
  }
})

window.addEventListener('popstate', function (event) {
  var routeState = event.state && event.state[feedThreadHistoryKey]
  if (routeState && routeState.mode === 'thread' && feedRouteContent) {
    openThreadRoute(routeState.url, routeState.tweetId, {
      push: false,
      returnState: routeState.returnState || lastFeedThreadRouteState
    })
    return
  }
  if (activeThreadRoute) {
    closeThreadRoute(routeState && routeState.mode === 'feed' ? routeState : lastFeedThreadRouteState)
  }
})

// ── Media overlay click delegation ──

document.addEventListener('click', function (event) {
  var expandBtn = event.target && event.target.closest ? event.target.closest('[data-feed-video-expand]') : null
  if (expandBtn) {
    event.preventDefault(); event.stopPropagation()
    var wrap = expandBtn.closest('[data-feed-media]')
    if (wrap) openMediaOverlay(wrap, wrap)
    return
  }

  var mediaTrigger = event.target && event.target.closest ? event.target.closest('[data-feed-media]') : null
  if (!mediaTrigger) return
  if (event.target.closest && event.target.closest('.feed-media-overlay')) return

  event.preventDefault(); event.stopPropagation()
  var mediaKind = String(mediaTrigger.getAttribute('data-feed-media-kind') || '').trim().toLowerCase()
  if (mediaKind === 'video') {
    var isInGrid = mediaTrigger.closest && mediaTrigger.closest('.feed-media-wrap-grid')
    if (isInGrid) { openMediaOverlay(mediaTrigger, mediaTrigger); return }
    if (event.detail >= 2) { openMediaOverlay(mediaTrigger, mediaTrigger); return }
    var inlineVideo = mediaTrigger.querySelector('video')
    if (inlineVideo && inlineVideo.muted) {
      inlineVideo.muted = false
      inlineVideo.play().catch(function () {})
      mediaTrigger.setAttribute('data-feed-video-unmuted', '1')
      return
    }
    if (inlineVideo) {
      if (inlineVideo.paused) inlineVideo.play().catch(function () {})
      else inlineVideo.pause()
    }
    return
  }
  openMediaOverlay(mediaTrigger, mediaTrigger)
})

// ── Keyboard: Enter/Space on focused media opens overlay ──

document.addEventListener('keydown', function (event) {
  var mediaTrigger = event.target && event.target.closest ? event.target.closest('[data-feed-media]') : null
  if (!mediaTrigger) return
  if (event.key !== 'Enter' && event.key !== ' ') return
  event.preventDefault()
  openMediaOverlay(mediaTrigger, mediaTrigger)
})

// ── Keyboard shortcuts (l=like, b=bookmark, s=share, t=translate) ──

var _focusedFeedCard = null
var _feedFocusDirty = true

function currentFeedCardScope() {
  return activeThreadRoute || feedList
}

function getFocusedFeedCard() {
  var scope = currentFeedCardScope()
  if (!scope) return null
  if (!_feedFocusDirty && _focusedFeedCard && document.contains(_focusedFeedCard)) return _focusedFeedCard
  var cards = scope.querySelectorAll('[data-feed-item]')
  var best = null, bestScore = -Infinity
  var midY = window.innerHeight / 2
  for (var i = 0; i < cards.length; i++) {
    var r = cards[i].getBoundingClientRect()
    if (r.bottom < 0 || r.top > window.innerHeight) continue
    var center = (r.top + r.bottom) / 2
    var score = -Math.abs(center - midY)
    if (score > bestScore) { bestScore = score; best = cards[i] }
  }
  _focusedFeedCard = best; _feedFocusDirty = false
  return best
}

function visibleFeedCards() {
  var scope = currentFeedCardScope()
  if (!scope) return []
  var cards = scope.querySelectorAll('[data-feed-item]')
  var visible = []
  for (var i = 0; i < cards.length; i++) {
    var card = cards[i]
    var style = window.getComputedStyle ? window.getComputedStyle(card) : null
    if (style && (style.display === 'none' || style.visibility === 'hidden')) continue
    var r = card.getBoundingClientRect()
    if (r.width <= 0 || r.height <= 0) continue
    visible.push(card)
  }
  return visible
}

function scrollFeedCardBy(delta) {
  var cards = visibleFeedCards()
  if (!cards.length) return false
  var current = getFocusedFeedCard()
  var index = current ? cards.indexOf(current) : -1
  if (index < 0) {
    index = delta > 0 ? -1 : cards.length
  }
  var nextIndex = Math.max(0, Math.min(cards.length - 1, index + delta))
  var next = cards[nextIndex]
  if (!next || next === current) return false
  _focusedFeedCard = next
  _feedFocusDirty = false
  try {
    next.scrollIntoView({ behavior: 'smooth', block: 'start' })
  } catch (_) {
    next.scrollIntoView()
  }
  return true
}

document.addEventListener('scroll', function () { _feedFocusDirty = true; _focusedFeedCard = null }, { passive: true, capture: true })

document.addEventListener('keydown', function (event) {
  var tag = (event.target.tagName || '').toLowerCase()
  if (tag === 'input' || tag === 'textarea' || (event.target && event.target.isContentEditable)) return
  if (event.ctrlKey || event.altKey || event.metaKey) return

  var sc = window.cfShortcuts
  if (!sc) return

  var overlayOpen = !!(window.FeedMediaOverlay && window.FeedMediaOverlay.element)
  if (overlayOpen) {
    var ov = window.FeedMediaOverlay.element
    if (sc.match('feed.media', event.key)) {
      event.preventDefault(); event.stopPropagation()
      window.FeedMediaOverlay.close()
    } else if (sc.match('feed.like', event.key)) {
      event.preventDefault(); event.stopPropagation()
      var ovHeart = ov.querySelector('[data-feed-overlay-action="heart"]')
      if (ovHeart) ovHeart.click()
    } else if (sc.match('feed.bookmark', event.key)) {
      if (isBookmarkMenuOpen()) return
      event.preventDefault(); event.stopPropagation()
      var ovBm = ov.querySelector('[data-feed-overlay-action="bookmark"]')
      if (ovBm) ovBm.click()
    } else if (sc.match('feed.share', event.key)) {
      event.preventDefault(); event.stopPropagation()
      var ovShare = ov.querySelector('[data-feed-overlay-action="share"]')
      if (ovShare) ovShare.click()
    }
    return
  }
  if (isBookmarkMenuOpen()) return

  if (event.key === 'j' || event.key === 'J') {
    if (scrollFeedCardBy(1)) { event.preventDefault(); event.stopPropagation() }
    return
  } else if (event.key === 'k' || event.key === 'K') {
    if (scrollFeedCardBy(-1)) { event.preventDefault(); event.stopPropagation() }
    return
  }

  var card = getFocusedFeedCard()
  if (!card) return

  if (sc.match('feed.like', event.key)) {
    event.preventDefault(); event.stopPropagation()
    triggerFeedActionUi(card, 'like', getFeedActionUiButton(card, 'heart'))
  } else if (sc.match('feed.bookmark', event.key)) {
    event.preventDefault(); event.stopPropagation()
    openFeedBookmarkMenu(getFeedActionUiButton(card, 'bookmark'), card)
  } else if (sc.match('feed.share', event.key)) {
    event.preventDefault(); event.stopPropagation()
    var sLink = String(card.getAttribute('data-feed-link') || '').trim()
    var sShareLink = toFxTwitterUrl(sLink) || sLink
    copyText(sShareLink).then(function () { showToast(t('toast_link_copied', 'Link copied')) }).catch(function () { showToast(t('error_copy_link_failed', 'Failed to copy link')) })
  } else if (sc.match('feed.translate', event.key)) {
    event.preventDefault(); event.stopPropagation()
    var tBtn = card.querySelector('.feed-translate-btn[data-feed-action="translate"]')
    if (tBtn) handleTranslateAction(card, tBtn)
  } else if (sc.match('feed.media', event.key)) {
    event.preventDefault(); event.stopPropagation()
    var mediaTrigger = card.querySelector('[data-feed-media]')
    if (mediaTrigger) openMediaOverlay(mediaTrigger, mediaTrigger)
  }
}, true)

// ── Channel page follow button ──

document.addEventListener('click', function (e) {
  var btn = e.target.closest('.js-follow-from-channel')
  if (!btn || btn.disabled) return
  var handle = btn.getAttribute('data-handle')
  var channelId = btn.getAttribute('data-channel-id')
  if (!handle) return
  var isFollowing = btn.getAttribute('data-following') === '1'
  btn.disabled = true
  var op = isFollowing ? unfollowChannel(channelId, handle) : followChannel(channelId, handle, handle)
  op.then(function (ok) { if (ok) window.location.reload(); else btn.disabled = false })
})

function syncToggleValue(value, keys) {
  value = value || {}
  for (var i = 0; i < keys.length; i++) {
    var raw = value[keys[i]]
    if (typeof raw === 'boolean') return raw
    if (typeof raw === 'number') return raw !== 0
    if (typeof raw === 'string') {
      var s = raw.toLowerCase()
      if (s === 'true' || s === '1' || s === 'set') return true
      if (s === 'false' || s === '0' || s === 'clear') return false
    }
  }
  var action = String(value.action || '').toLowerCase()
  if (action === 'set') return true
  if (action === 'clear') return false
  return null
}

// ── SyncPoller integration ──

if (window.SyncPoller) {
  window.SyncPoller.on('like', function (itemId, value) {
    var liked = syncToggleValue(value, ['liked'])
    if (liked !== null) propagateLikeState(itemId, liked)
  })
  window.SyncPoller.on('bookmark', function (itemId, value) {
    var root = feedList && feedList.querySelector('[data-feed-item][data-tweet-id="' + cssEscape(itemId) + '"]')
    if (!root) return
    var bookmarked = syncToggleValue(value, ['bookmarked'])
    if (bookmarked === null) return
    setStateBool(root, 'bookmarked', bookmarked)
    syncFeedButtons(root)
  })
  window.SyncPoller.on('follow', function (itemId, value) {
    var followed = syncToggleValue(value, ['followed', 'subscribed'])
    if (followed !== null) syncFollowButtons(itemId, followed)
  })
  window.SyncPoller.on('subscribe', function (itemId) { syncFollowButtons(itemId, true) })
  window.SyncPoller.on('unsubscribe', function (itemId) { syncFollowButtons(itemId, false) })
}

// ── HTMX event handlers ──

// Pre-load sentinel: observe it 2500px before it enters the viewport so the
// next page request is in-flight well before the user reaches the bottom.
// Use a custom event instead of HTMX's built-in "revealed" trigger so the
// observer cannot race with HTMX's own visibility handler and issue a duplicate
// request for the same sentinel.
function observeSentinelEarly() {
  var sentinel = feedList && feedList.querySelector('.feed-scroll-sentinel')
  if (!sentinel || sentinel._earlyObserved) return
  sentinel._earlyObserved = true
  var observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (entry.isIntersecting) {
        observer.disconnect()
        htmx.trigger(entry.target, 'feed-preload')
      }
    })
  }, { rootMargin: '0px 0px 2500px 0px' })
  observer.observe(sentinel)
}

// After infinite scroll sentinel swap, initialize only newly added cards.
// Passing feedList to initFeedCards on every append would re-run syncFeedButtons
// on all accumulated items (O(n) per page). Instead we mark items after init
// and only process the unmarked ones.
function initNewFeedCards() {
  if (!feedList) return
  var newItems = feedList.querySelectorAll('[data-feed-item]:not([data-fi])')
  if (!newItems.length) return
  initTextClamps(feedList)
  initDates(feedList)
  initInlineMedia(feedList)
  newItems.forEach(function (item) {
    syncFeedButtons(item)
    item.setAttribute('data-fi', '1')
  })
  applyRetweetMuteFilter(feedList)
  applyRepostMuteFilter(feedList)
  observeTranslateCards(feedList, getTranslateObserver())
}

if (feedList) {
  feedList.addEventListener('htmx:afterSettle', function (e) {
    var trigger = e.detail && e.detail.requestConfig && e.detail.requestConfig.elt
    if (trigger && trigger.classList && trigger.classList.contains('feed-scroll-sentinel')) {
      initNewFeedCards()
      removeEmptyStateIfNeeded()
      observeSentinelEarly()
    }
  })
}

// Custom confirm dialog for HTMX mute buttons
document.body.addEventListener('htmx:confirm', function (e) {
  var btn = e.target
  if (!btn || !btn.hasAttribute('data-feed-mute-btn')) return
  e.preventDefault()
  var muteLabel = btn.textContent.trim()
  askConfirm({
    title: muteLabel,
    body: t('feed_mute_confirm_body', 'Posts from this account will be hidden from your feed.'),
    confirmLabel: t('action_mute', 'Mute'),
    cancelLabel: t('action_cancel', 'Cancel'),
    danger: true
  }).then(function (confirmed) {
    if (confirmed) e.detail.issueRequest()
  })
})

// After mute via HX-Trigger, remove all cards by that author
document.body.addEventListener('accountMuted', function (e) {
  var handle = e.detail && e.detail.handle
  if (!handle) return
  var lower = handle.toLowerCase()
  document.querySelectorAll('[data-feed-item]').forEach(function (card) {
    var author = (card.getAttribute('data-author-handle') || '').toLowerCase()
    var quoteAuthor = (card.getAttribute('data-quote-author-handle') || '').toLowerCase()
    if (author === lower || quoteAuthor === lower) card.remove()
  })
  showToast(tf('toast_muted_account', 'Muted @%1$s', handle))
})

document.body.addEventListener('channelRefreshComplete', function (e) {
  var channelId = e.detail && e.detail.channelId
  if (isCurrentChannelPath(channelId)) window.location.reload()
})

// After star toggle via HX-Trigger, sync all star buttons
document.body.addEventListener('starChanged', function (e) {
  var channelId = e.detail && e.detail.channelId
  var starred = !!(e.detail && e.detail.starred)
  if (!channelId) return
  document.querySelectorAll('.feed-star-btn[data-id="' + CSS.escape(channelId) + '"]').forEach(function (btn) {
    btn.classList.toggle('active', starred)
    btn.textContent = starred ? '\u2605' : '\u2606'
    btn.title = starred ? t('action_unfavourite_account', 'Unfavourite account') : t('action_favourite_account', 'Favourite account')
  })
})

// After follow via HX-Trigger, update buttons in place (do not remove —
// hero + feed items both need to keep their button and flip to "Following").
document.body.addEventListener('followChanged', function (e) {
  var channelId = e.detail && e.detail.channelId
  if (!channelId) return
  syncFollowButtons(channelId, true)
})

document.body.addEventListener('htmx:beforeSend', function (e) {
  var elt = e.detail && e.detail.elt
  if (!elt || elt.getAttribute('data-feed-action') !== 'heart') return
  var card = elt.closest('[data-feed-item]')
  if (!card) return
  var tid = card.getAttribute('data-tweet-id')
  if (!tid) return
  var nextLiked = elt.hasAttribute('hx-post') ? true : elt.hasAttribute('hx-delete') ? false : null
  if (nextLiked === null) return
  elt.setAttribute('data-feed-like-before', stateBool(card, 'liked') ? '1' : '0')
  applyLikeState(card, tid, nextLiked)
})

function rollbackHTMXLikeState(e) {
  var elt = e.detail && e.detail.elt
  if (!elt || elt.getAttribute('data-feed-action') !== 'heart') return
  var previous = elt.getAttribute('data-feed-like-before')
  if (previous !== '0' && previous !== '1') return
  elt.removeAttribute('data-feed-like-before')
  var card = elt.closest('[data-feed-item]')
  var tid = card && card.getAttribute('data-tweet-id')
  if (!card || !tid) return
  applyLikeState(card, tid, previous === '1')
}

document.body.addEventListener('htmx:responseError', rollbackHTMXLikeState)
document.body.addEventListener('htmx:sendError', rollbackHTMXLikeState)

// After like button HTMX swap, propagate state
document.body.addEventListener('htmx:afterSwap', function (e) {
  var elt = e.detail.elt
  if (!elt) return
  if (elt.getAttribute('data-feed-action') === 'heart') {
    var card = elt.closest('[data-feed-item]')
    if (!card) return
    var tid = card.getAttribute('data-tweet-id')
    var liked = elt.classList.contains('active')
    propagateLikeState(tid, liked)
  }
})

// ── Hide old pagination when HTMX sentinel exists ──

if (feedPagination && document.querySelector('.feed-scroll-sentinel')) {
  feedPagination.style.display = 'none'
}

// ── Init ──

initFeedCards(feedList)
removeEmptyStateIfNeeded()
observeSentinelEarly()
initRetweetersDialog()
restoreFeedThreadReturn()
initThreadBackLink()

// Boot-time sync: push any channels that are muted in localStorage but not
// yet persisted on the server (channels toggled before the API call existed).
;(function syncRetweetMutesToServer() {
  var muted = Array.from(getRetweetMutedChannels())
  muted.forEach(function(channelId) {
    apiFetch('/api/channels/' + encodeURIComponent(channelId) + '/settings', {
      method: 'POST',
      body: JSON.stringify({ include_reposts: false })
    }).catch(function() {})
  })
})()
