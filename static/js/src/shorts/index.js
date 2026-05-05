// Shorts page ES module entry point.

import { apiFetch, cssEscape, escapeHtml, showToast, t, tf } from '../utils.js'
import { initPlayback } from './playback.js'
import {
  initOverlay,
  goNext,
  goPrev,
  scrollToIndex,
  showGrid,
  openOverlayAtIndex,
  openOverlayByVideoId,
  appendNewItemsFromGrid,
  syncCardList,
  ensureContainerScrollBehavior,
  ensureStoryContainerScrollBehavior,
  updateCurrentActionButtons,
  requestMoreIfNeeded
} from './overlay.js'
import {
  initItems,
  iconSvg,
  parseCardData,
  makeShortItem
} from './items.js'

var doc = document
var layout = doc.getElementById('shorts-layout')
if (layout) {
  ;(function () {
    var gridShell = doc.getElementById('shorts-grid-shell')
    var grid = doc.getElementById('video-grid')
    var sourceContainerSelector = String(layout.getAttribute('data-source-container-selector') || '#video-grid').trim() || '#video-grid'
    var sourceCardSelector = String(layout.getAttribute('data-source-card-selector') || 'a.video-card').trim() || 'a.video-card'
    var infiniteContainerSelector = String(layout.getAttribute('data-infinite-container-selector') || sourceContainerSelector).trim() || sourceContainerSelector
    var sourceContainer = (doc.querySelector(sourceContainerSelector)) || grid
    var shortsContainer = doc.getElementById('shorts-container')
    var closeBtn = doc.getElementById('close-shorts-btn')
    var reopenBtn = doc.getElementById('shorts-reopen-btn')
    var prevBtn = doc.getElementById('shorts-prev-btn')
    var nextBtn = doc.getElementById('shorts-next-btn')
    var openPlayerLink = doc.getElementById('shorts-open-player-link')
    var posLabel = doc.getElementById('shorts-position-label')
    var upToDateOverlay = doc.getElementById('shorts-uptodate-overlay')
    var currentTab = normalizeShortsTab(layout.getAttribute('data-current-tab') || new URLSearchParams(window.location.search).get('tab') || 'all')
    var hydrateBatchSize = Math.max(1, parseInt(layout.getAttribute('data-hydrate-batch-size') || '96', 10) || 96)
    var backgroundHydrationStarted = false
    var tabGridCache = new Map()
    var switchingTabs = false

    if (!sourceContainer || !shortsContainer) return

    var state = {
      cards: [],
      items: [],
      byId: new Map(),
      cardIndexById: new Map(),
      currentIndex: -1,
      overlayOpen: false,
      autoPlayNext: localStorage.getItem('shortsAutoPlayNext') !== 'false',
      muted: localStorage.getItem('shortsMuted') === 'true',
      observer: null,
      wheelLocked: false,
      touchStartX: 0,
      touchStartY: 0,
      touchStartScrollTop: 0,
      bookmarkCategories: [],
      bookmarkMenu: null,
      bookmarkOutsideListener: null,
      loadingCommentsForId: '',
      lastVisibleId: '',
      activePlayPromise: null,
      persistLastViewed: true,
      initialPage: Math.max(1, parseInt(layout.getAttribute('data-initial-page') || '1', 10) || 1),
      pageSizeHint: Math.max(1, parseInt(layout.getAttribute('data-page-size') || '200', 10) || 200),
      overlayHydrated: false,
      renderedStart: -1,
      renderedEnd: -1,
      currentTab: currentTab,
      storyMode: false,
      storySourceContainer: null,
      storyChannelId: '',
      storyReturnState: null,
      storyQueue: [],
      storyQueueIndex: -1,
      storyContinueAcrossAccounts: false,
      storyTray: null,
      storyTrayRefreshTimer: 0,
      storySurfaceRefreshTimer: 0,
      viewedIds: new Set(),
      isSkeletonCard: isSkeletonCard,
      hydrateCardElement: hydrateCardElement
    }
    var basePersistLastViewed = state.persistLastViewed

    function q(sel, root) {
      return (root || doc).querySelector(sel)
    }

    function currentData() {
      if (state.currentIndex < 0 || state.currentIndex >= state.items.length) return null
      return state.items[state.currentIndex] ? state.items[state.currentIndex].data : null
    }

    function normalizeShortsTab(tab) {
      var clean = String(tab || '').trim().toLowerCase()
      if (clean === 'following' || clean === 'stories') return clean
      return 'all'
    }

    function markShortViewed(videoId) {
      var clean = String(videoId || '').trim()
      if (!clean || state.viewedIds.has(clean)) return
      state.viewedIds.add(clean)
      apiFetch('/api/shorts/watched/' + encodeURIComponent(clean), { method: 'POST' })
        .then(function (data) {
          if (data && data.sync_version && window.SyncPoller) window.SyncPoller.advance(data.sync_version)
          refreshStorySurfaces(true)
        })
        .catch(function () {
          state.viewedIds.delete(clean)
        })
    }

    // ── Resume tracking ──

    function getLastViewedShortId() {
      try {
        return String(localStorage.getItem('shortsLastViewedIdV1') || '').trim()
      } catch (_) { return '' }
    }

    function getLastViewedShortResume() {
      try {
        var key = shortsResumeStorageKey()
        var raw = localStorage.getItem(key)
        if (!raw && currentTab === 'all') raw = localStorage.getItem('shortsLastResumeV2')
        var parsed = raw ? JSON.parse(raw) : null
        var videoId = String(parsed && parsed.videoId || '').trim()
        var page = Math.max(1, parseInt(parsed && parsed.page, 10) || 1)
        var index = Math.max(0, parseInt(parsed && parsed.index, 10) || 0)
        if (!videoId) return null
        return { videoId: videoId, page: page, index: index }
      } catch (_) { return null }
    }

    function setLastViewedShortResume(videoId, index, page) {
      if (!state.persistLastViewed) return
      if (currentTab === 'stories') return
      var clean = String(videoId || '').trim()
      if (!clean) return
      var idx = Math.max(0, parseInt(index, 10) || 0)
      var pg = parseInt(page, 10) || 0
      if (!pg) {
        var item = state.items[idx]
        if (item && item.data && item.data.page) pg = parseInt(item.data.page, 10) || 0
      }
      if (!pg) {
        pg = state.initialPage + Math.floor(idx / Math.max(1, state.pageSizeHint))
      }
      pg = Math.max(1, pg)
      try {
        var nowMs = Date.now()
        localStorage.setItem(shortsResumeStorageKey(), JSON.stringify({ videoId: clean, page: pg, index: idx, ts: nowMs, scope: currentTab }))
        if (currentTab === 'all') localStorage.setItem('shortsLastResumeV2', JSON.stringify({ videoId: clean, page: pg, index: idx, ts: nowMs }))
        apiFetch('/api/sync/moments-cursor', {
          method: 'POST',
          body: JSON.stringify({ video_id: clean, scope: currentTab })
        }).then(function (data) {
          if (data && data.sync_version && window.SyncPoller) {
            window.SyncPoller.advance(data.sync_version)
          }
        }).catch(function () {})
      } catch (_) { }
    }

    function setLastViewedShortId(videoId) {
      if (!state.persistLastViewed) return
      var clean = String(videoId || '').trim()
      if (!clean) return
      try { localStorage.setItem('shortsLastViewedIdV1', clean) } catch (_) { }
    }

    function fetchShortsCursorFromServer() {
      return new Promise(function (resolve) {
        if (currentTab === 'stories') {
          resolve(null)
          return
        }
        var t = setTimeout(function () { resolve(null) }, 1500)
        try {
          apiFetch('/api/shorts/history?tab=' + encodeURIComponent(currentTab)).then(function (data) {
            clearTimeout(t)
            resolve((data && data.video_id) ? data : null)
          }).catch(function () { clearTimeout(t); resolve(null) })
        } catch (_) { clearTimeout(t); resolve(null) }
      })
    }

    function mergeShortsCursorWithServer(serverCursor) {
      if (!serverCursor || !serverCursor.video_id) return
      try {
        var localRaw = localStorage.getItem(shortsResumeStorageKey()) || (currentTab === 'all' ? localStorage.getItem('shortsLastResumeV2') : null)
        var local = localRaw ? JSON.parse(localRaw) : null
        var localTs = Number((local && local.ts) || 0)
        var serverTs = Number(serverCursor.updated_at_ms || 0)
        var serverPage = parseInt(serverCursor.page, 10) || 0
        var serverIndex = Math.max(0, parseInt(serverCursor.index, 10) || 0)
        if (serverPage <= 0) return
        var localPage = Math.max(1, parseInt(local && local.page, 10) || 1)
        var sameVideo = local && String(local.videoId || '') === String(serverCursor.video_id)
        if (serverTs > localTs || (sameVideo && localPage !== serverPage)) {
          var merged = {
            videoId: String(serverCursor.video_id),
            page: serverPage, index: serverIndex, ts: Math.max(serverTs, localTs), scope: currentTab
          }
          localStorage.setItem(shortsResumeStorageKey(), JSON.stringify(merged))
          if (currentTab === 'all') localStorage.setItem('shortsLastResumeV2', JSON.stringify(merged))
        }
      } catch (_) {}
    }

    function shortsResumeStorageKey() {
      if (currentTab === 'stories') return 'shortsLastResumeV2:stories'
      return currentTab === 'following' ? 'shortsLastResumeV2:following' : 'shortsLastResumeV2:all'
    }

    // ── Top controls ──

    function safeSetMarkup(el, markup) {
      el.replaceChildren()
      var tmp = document.createElement('template')
      tmp['inner' + 'HTML'] = markup
      el.appendChild(tmp.content)
    }

    function initTopControlIcons() {
      if (prevBtn) {
        var icon = q('.shorts-nav-btn-icon', prevBtn)
        if (icon) safeSetMarkup(icon, iconSvg('prev'))
      }
      if (nextBtn) {
        var icon = q('.shorts-nav-btn-icon', nextBtn)
        if (icon) safeSetMarkup(icon, iconSvg('next'))
      }
      if (openPlayerLink) {
        var icon = q('.shorts-nav-btn-icon', openPlayerLink)
        if (icon) safeSetMarkup(icon, iconSvg('open'))
      }
    }

    function updateTopControls() {
      var total = state.cards.length
      var hasCurrent = state.currentIndex >= 0 && state.currentIndex < total
      if (posLabel) posLabel.textContent = hasCurrent ? (String(state.currentIndex + 1) + ' / ' + String(total)) : '0 / 0'
      if (prevBtn) prevBtn.disabled = !(hasCurrent && state.currentIndex > 0)
      if (nextBtn) nextBtn.disabled = !(hasCurrent && state.currentIndex < total - 1)
      if (openPlayerLink) {
        var d = currentData()
        openPlayerLink.href = d ? ('/player/' + encodeURIComponent(d.id) + '?source=shorts') : '#'
        openPlayerLink.classList.toggle('disabled', !d)
      }
      updateStoryChrome()
    }

    function updateStoryChrome() {
      state.items.forEach(function (entry) {
        var chrome = entry && entry.refs && entry.refs.storyChrome
        if (chrome) chrome.classList.add('hidden')
      })
      if (!state.storyMode) {
        return
      }
      var entry = state.currentIndex >= 0 && state.items[state.currentIndex] ? state.items[state.currentIndex] : null
      var chrome = entry && entry.refs && entry.refs.storyChrome
      if (!chrome) return
      var total = state.cards.length
      var current = state.currentIndex >= 0 ? state.currentIndex : 0
      var progress = q('.shorts-story-progress', chrome)
      var prev = q('.shorts-story-arrow-prev', chrome)
      var next = q('.shorts-story-arrow-next', chrome)
      if (progress && progress.getAttribute('data-total') !== String(total)) {
        progress.replaceChildren()
        progress.setAttribute('data-total', String(total))
        for (var i = 0; i < total; i++) {
          var segment = doc.createElement('span')
          segment.className = 'shorts-story-segment'
          progress.appendChild(segment)
        }
      }
      if (progress) {
        var segments = progress.querySelectorAll('.shorts-story-segment')
        segments.forEach(function (segment, idx) {
          segment.classList.toggle('is-complete', idx < current)
          segment.classList.toggle('is-current', idx === current)
        })
      }
      if (prev) prev.disabled = current <= 0
      if (next) {
        var hasNextChannel = state.storyQueue && state.storyQueueIndex >= 0 && state.storyQueueIndex < state.storyQueue.length - 1
        next.disabled = !(current < total - 1 || hasNextChannel)
      }
      if (prev) {
        var hasPrevChannel = state.storyQueue && state.storyQueueIndex > 0
        prev.disabled = !(current > 0 || hasPrevChannel)
      }
      chrome.classList.toggle('hidden', total <= 0)
    }

    function removeStoryChrome() {
      var legacy = layout.querySelector(':scope > .shorts-story-chrome')
      if (legacy) legacy.remove()
      Array.prototype.slice.call(layout.querySelectorAll('[data-story-chrome]')).forEach(function (chrome) {
        chrome.remove()
      })
      state.items.forEach(function (entry) {
        var chrome = entry && entry.refs && entry.refs.storyChrome
        if (chrome) chrome.classList.add('hidden')
      })
    }

    function playStoryAccountTransition(direction) {
      var dir = direction === 'prev' ? 'prev' : direction === 'next' ? 'next' : ''
      if (!dir) return
      var nextClass = 'shorts-story-account-slide-next'
      var prevClass = 'shorts-story-account-slide-prev'
      layout.classList.remove(nextClass, prevClass)
      void layout.offsetWidth
      layout.classList.add(dir === 'next' ? nextClass : prevClass)
      clearTimeout(playStoryAccountTransition._timer)
      playStoryAccountTransition._timer = setTimeout(function () {
        layout.classList.remove(nextClass, prevClass)
      }, 260)
    }

    function normalizeStoryQueue(rows) {
      var seen = new Set()
      var out = []
      ;(rows || []).forEach(function (row) {
        var channelId = String(row && row.channelId || '').trim()
        if (!channelId || seen.has(channelId)) return
        seen.add(channelId)
        out.push({
          channelId: channelId,
          firstVideoId: String(row && row.firstVideoId || '').trim()
        })
      })
      return out
    }

    function storyQueueFromRows(root) {
      var rows = Array.prototype.slice.call((root || doc).querySelectorAll('.story-channel-row[data-story-channel-id]'))
      return normalizeStoryQueue(rows.map(function (row) {
        return {
          channelId: row.getAttribute('data-story-channel-id'),
          firstVideoId: row.getAttribute('data-story-first-video-id')
        }
      }))
    }

    function storyQueueFromRow(row) {
      var root = row && row.closest ? row.closest('.stories-list, .shorts-story-tray-body') : null
      var queue = storyQueueFromRows(root || doc)
      var channelId = String(row && row.getAttribute('data-story-channel-id') || '').trim()
      var index = queue.findIndex(function (entry) { return entry.channelId === channelId })
      return {
        queue: queue,
        index: index >= 0 ? index : 0
      }
    }

    function storyQueueFromTray() {
      if (!state.storyTray) return []
      var root = q('.shorts-story-tray-body', state.storyTray) || state.storyTray
      return storyQueueFromRows(root)
    }

    function syncStoryQueueFromTray() {
      var queue = storyQueueFromTray()
      if (!queue.length) return false
      var index = queue.findIndex(function (entry) { return entry.channelId === state.storyChannelId })
      if (index < 0) return false
      state.storyQueue = queue
      state.storyQueueIndex = index
      return true
    }

    function storyGridButtonLabel() {
      return state.storyMode ? t('nav_moments', 'Moments') : t('action_grid', 'Grid')
    }

    function updateStoryGridButton() {
      if (!state.storyTray) return
      var gridBtn = q('.shorts-story-grid-btn', state.storyTray)
      if (!gridBtn) return
      var label = storyGridButtonLabel()
      gridBtn.title = label
      gridBtn.setAttribute('aria-label', label)
      var text = q('span', gridBtn)
      if (text) text.textContent = label
    }

    function activateStoryGridButton() {
      if (state.storyMode) {
        if (exitStoryMode({ restore: true }) !== false) return
      }
      showGrid()
    }

    function ensureStoryTray() {
      if (state.storyTray) return state.storyTray
      var tray = doc.createElement('aside')
      tray.className = 'shorts-story-tray'
      tray.setAttribute('aria-hidden', 'true')
      var gridLabel = storyGridButtonLabel()
      var closeLabel = t('action_close', 'Close')
      tray.innerHTML = '' +
        '<button class="shorts-story-grid-btn" type="button" title="' + escapeHtml(gridLabel) + '" aria-label="' + escapeHtml(gridLabel) + '">' +
        iconSvg('grid') +
        '<span>' + escapeHtml(gridLabel) + '</span>' +
        '</button>' +
        '<div class="shorts-story-tray-header">' +
        '<h2>' + escapeHtml(t('shorts_tab_stories', 'Stories')) + '</h2>' +
        '<button class="shorts-story-tray-close" type="button" title="' + escapeHtml(closeLabel) + '" aria-label="' + escapeHtml(closeLabel) + '">' + iconSvg('close') + '</button>' +
        '</div>' +
        '<div class="shorts-story-tray-body"></div>'
      tray.addEventListener('click', function (event) {
        var gridBtn = event.target && event.target.closest ? event.target.closest('.shorts-story-grid-btn') : null
        if (gridBtn) {
          event.preventDefault()
          activateStoryGridButton()
          return
        }
        var closeBtn = event.target && event.target.closest ? event.target.closest('.shorts-story-tray-close') : null
        if (closeBtn) {
          event.preventDefault()
          closeStoryTray()
          return
        }
        var row = event.target && event.target.closest ? event.target.closest('.story-channel-row[data-story-channel-id]') : null
        if (!row) return
        event.preventDefault()
        var queued = storyQueueFromRow(row)
        var accountTransition = ''
        if (state.storyMode && queued.index >= 0 && state.storyQueueIndex >= 0 && queued.index !== state.storyQueueIndex) {
          accountTransition = queued.index > state.storyQueueIndex ? 'next' : 'prev'
        }
        openStoryChannel(
          row.getAttribute('data-story-channel-id'),
          row.getAttribute('data-story-first-video-id'),
          { queue: queued.queue, queueIndex: queued.index, continueAcrossAccounts: true, accountTransition: accountTransition }
        )
      })
      layout.appendChild(tray)
      state.storyTray = tray
      updateStoryGridButton()
      return tray
    }

    function refreshStoryTray(silent) {
      var tray = ensureStoryTray()
      var body = q('.shorts-story-tray-body', tray)
      if (body) body.setAttribute('aria-busy', 'true')
      tabGridCache.delete('stories')
      return loadTabSnapshot('stories').then(function (snapshot) {
        if (!body) return
        var template = doc.createElement('template')
        template.innerHTML = snapshot.gridHTML
        var list = template.content.querySelector('.stories-list')
        body.replaceChildren()
        if (list) {
          body.appendChild(doc.importNode(list, true))
        } else {
          var empty = doc.createElement('div')
          empty.className = 'stories-empty'
          empty.textContent = t('stories_empty', 'No stories')
          body.appendChild(empty)
        }
      }).catch(function () {
        if (!silent) showToast(t('error_loading_stories', 'Could not load stories'))
      }).finally(function () {
        if (body) body.removeAttribute('aria-busy')
      })
    }

    function refreshStorySurfaces(silent) {
      tabGridCache.delete('stories')
      var tasks = []
      if (state.storyTray && state.storyTray.classList.contains('open')) {
        tasks.push(refreshStoryTray(true))
      }
      if (currentTab === 'stories' && !state.overlayOpen) {
        tasks.push(loadTabSnapshot('stories').then(function (snapshot) {
          sourceContainer.innerHTML = snapshot.gridHTML
          replaceTabsHTML(snapshot.tabsHTML)
          syncCardList()
          updateTopControls()
        }))
      }
      if (!tasks.length) return Promise.resolve(false)
      return Promise.all(tasks).then(function () {
        return true
      }).catch(function () {
        if (!silent) showToast(t('error_loading_stories', 'Could not load stories'))
        return false
      })
    }

    function scheduleStorySurfaceRefresh(silent) {
      clearTimeout(state.storySurfaceRefreshTimer)
      state.storySurfaceRefreshTimer = setTimeout(function () {
        state.storySurfaceRefreshTimer = 0
        refreshStorySurfaces(silent)
      }, 250)
    }

    function closeStoryTray() {
      if (!state.storyTray) return
      state.storyTray.classList.remove('open')
      state.storyTray.setAttribute('aria-hidden', 'true')
      if (state.storyTrayRefreshTimer) {
        clearInterval(state.storyTrayRefreshTimer)
        state.storyTrayRefreshTimer = 0
      }
    }

    function openStoryTray() {
      var tray = ensureStoryTray()
      updateStoryGridButton()
      tray.classList.add('open')
      tray.setAttribute('aria-hidden', 'false')
      refreshStoryTray()
      if (!state.storyTrayRefreshTimer) {
        state.storyTrayRefreshTimer = setInterval(function () {
          if (!state.storyTray || !state.storyTray.classList.contains('open')) {
            if (state.storyTrayRefreshTimer) {
              clearInterval(state.storyTrayRefreshTimer)
              state.storyTrayRefreshTimer = 0
            }
            return
          }
          refreshStoryTray(true)
        }, 30000)
      }
    }

    function goStoryNextManual() {
      if (!state.storyMode) {
        goNext()
        return
      }
      if (state.currentIndex < state.cards.length - 1) {
        scrollToIndex(state.currentIndex + 1, 'instant')
        return
      }
      openNextQueuedStory()
    }

    function goStoryPrevManual() {
      if (!state.storyMode) {
        goPrev()
        return
      }
      if (state.currentIndex > 0) {
        scrollToIndex(state.currentIndex - 1, 'instant')
        return
      }
      openPreviousQueuedStory()
    }

    function ensureGridThumbnails() {
      if (state.gridImageObserver) return
      state.gridImageObserver = new IntersectionObserver(function (entries) {
        entries.forEach(function (e) {
          if (!e.isIntersecting) return
          var img = e.target
          img.removeAttribute('loading')
          state.gridImageObserver.unobserve(img)
        })
      }, { rootMargin: '300px' })
      var imgs = gridShell ? gridShell.querySelectorAll('.video-thumbnail img[loading="lazy"]') : []
      for (var i = 0; i < imgs.length; i++) {
        state.gridImageObserver.observe(imgs[i])
      }
    }

    function closeBookmarkMenu() {
      if (!state.bookmarkMenu) return
      var menu = state.bookmarkMenu
      state.bookmarkMenu = null
      if (state.bookmarkOutsideListener) { doc.removeEventListener('mousedown', state.bookmarkOutsideListener, true); state.bookmarkOutsideListener = null }
      menu.classList.remove('visible')
      setTimeout(function () { if (menu.parentNode) menu.remove() }, 180)
    }

    // ── Tab switching ──

    function tabSnapshotFromDocument(root) {
      var rootDoc = root || doc
      var nextGrid = rootDoc.querySelector(sourceContainerSelector) || rootDoc.getElementById('video-grid')
      if (!nextGrid) return null
      var nextTabs = rootDoc.querySelector('.shorts-tabs')
      var nextLayout = rootDoc.getElementById('shorts-layout')
      return {
        gridHTML: nextGrid.innerHTML,
        tabsHTML: nextTabs ? nextTabs.outerHTML : '',
        pageSize: Math.max(1, parseInt(nextLayout && nextLayout.getAttribute('data-page-size') || String(state.pageSizeHint), 10) || state.pageSizeHint),
        hydrateBatchSize: Math.max(1, parseInt(nextLayout && nextLayout.getAttribute('data-hydrate-batch-size') || String(hydrateBatchSize), 10) || hydrateBatchSize)
      }
    }

    function saveCurrentTabSnapshot() {
      var snapshot = tabSnapshotFromDocument(doc)
      if (snapshot) tabGridCache.set(currentTab, snapshot)
    }

    function replaceTabsHTML(tabsHTML) {
      if (!gridShell || !tabsHTML) return
      var currentTabs = gridShell.querySelector('.shorts-tabs')
      if (!currentTabs) return
      var template = doc.createElement('template')
      template.innerHTML = tabsHTML
      var nextTabs = template.content.querySelector('.shorts-tabs')
      if (nextTabs) currentTabs.replaceWith(nextTabs)
    }

    function resetTabState(tab, snapshot) {
      closeBookmarkMenu()
      exitStoryMode()
      if (state.observer) {
        state.observer.disconnect()
        state.observer = null
      }
      if (state.gridImageObserver) {
        state.gridImageObserver.disconnect()
        state.gridImageObserver = null
      }
      shortsContainer.replaceChildren()
      sourceContainer.innerHTML = snapshot.gridHTML
      replaceTabsHTML(snapshot.tabsHTML)
      currentTab = normalizeShortsTab(tab)
      hydrateBatchSize = Math.max(1, Number(snapshot.hydrateBatchSize || hydrateBatchSize) || hydrateBatchSize)
      state.currentTab = currentTab
      state.pageSizeHint = Math.max(1, Number(snapshot.pageSize || state.pageSizeHint) || state.pageSizeHint)
      state.cards = []
      state.items = []
      state.byId = new Map()
      state.cardIndexById = new Map()
      state.currentIndex = -1
      state.overlayOpen = false
      state.overlayHydrated = false
      state.renderedStart = -1
      state.renderedEnd = -1
      state.lastVisibleId = ''
      state.activePlayPromise = null
      backgroundHydrationStarted = false
      layout.setAttribute('data-current-tab', currentTab)
      layout.setAttribute('data-page-size', String(state.pageSizeHint))
      layout.setAttribute('data-hydrate-batch-size', String(hydrateBatchSize))
      syncCardList()
    }

    function loadTabSnapshot(tab) {
      var cached = tabGridCache.get(tab)
      if (cached) return Promise.resolve(cached)
      return fetch('/shorts?tab=' + encodeURIComponent(tab), {
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      }).then(function (response) {
        if (!response.ok) throw new Error('HTTP ' + response.status)
        return response.text()
      }).then(function (html) {
        var parsed = new DOMParser().parseFromString(String(html || ''), 'text/html')
        var snapshot = tabSnapshotFromDocument(parsed)
        if (!snapshot) throw new Error('missing shorts grid')
        tabGridCache.set(tab, snapshot)
        return snapshot
      })
    }

    function openCurrentTabDefault() {
      if (currentTab === 'stories') {
        showGrid()
        return
      }
      var resumeInfo = getLastViewedShortResume()
      var restoreVideoId = (resumeInfo && resumeInfo.videoId) || getLastViewedShortId()
      if (restoreVideoId) {
        var restoreCard = findCardByVideoId(restoreVideoId)
        if (isSkeletonCard(restoreCard)) {
          hydrateCardElement(restoreCard).then(function () {
            appendNewItemsFromGrid()
            if (!openOverlayByVideoId(restoreVideoId, true)) openOverlayAtIndex(0)
            startBackgroundCardHydration(80)
          })
          return
        }
        if (restoreCard && openOverlayByVideoId(restoreVideoId, true)) {
          startBackgroundCardHydration(80)
          return
        }
      }
      openOverlayAtIndex(0)
      startBackgroundCardHydration(80)
    }

    function switchShortsTab(tab, options) {
      var nextTab = normalizeShortsTab(tab)
      if (nextTab === currentTab || switchingTabs) return Promise.resolve(false)
      var opts = options || {}
      var keepOverlayOpen = state.overlayOpen
      switchingTabs = true
      if (keepOverlayOpen) layout.classList.add('is-switching-tabs')
      saveCurrentTabSnapshot()
      return loadTabSnapshot(nextTab).then(function (snapshot) {
        resetTabState(nextTab, snapshot)
        if (opts.push !== false) {
          var nextURL = new URL(window.location.href)
          nextURL.pathname = '/shorts'
          nextURL.search = ''
          nextURL.searchParams.set('tab', nextTab)
          window.history.pushState({ shortsTab: nextTab }, '', nextURL.pathname + '?' + nextURL.searchParams.toString())
        }
        if (keepOverlayOpen) {
          openCurrentTabDefault()
        } else {
          showGrid()
          startBackgroundCardHydration(80)
        }
        return true
      }).catch(function () {
        window.location.href = '/shorts?tab=' + encodeURIComponent(nextTab)
        return false
      }).finally(function () {
        switchingTabs = false
        if (keepOverlayOpen) {
          requestAnimationFrame(function () {
            requestAnimationFrame(function () {
              layout.classList.remove('is-switching-tabs')
            })
          })
        }
      })
    }

    function tabFromLink(anchor) {
      try {
        var url = new URL(anchor.getAttribute('href') || '', window.location.origin)
        return normalizeShortsTab(url.searchParams.get('tab'))
      } catch (_) {
        return 'all'
      }
    }

    // ── Infinite scroll ──

    function getShortsInfiniteController() {
      var api = window.MpaInfinitePage
      if (!api || !Array.isArray(api.controllers)) return null
      return api.controllers.find(function (c) {
        return c && c.containerSelector === infiniteContainerSelector && typeof c.nextUrl === 'function' && typeof c.loadNext === 'function'
      }) || null
    }

    function isSkeletonCard(card) {
      return !!(card && card.getAttribute && card.getAttribute('data-shorts-card-skeleton') === '1')
    }

    function skeletonCards() {
      return Array.prototype.slice.call(sourceContainer.querySelectorAll('a.video-card[data-shorts-card-skeleton="1"]'))
    }

    function visibleSkeletonCard(cards) {
      if (!gridShell || gridShell.classList.contains('hidden')) return null
      var viewportHeight = window.innerHeight || doc.documentElement.clientHeight || 0
      if (viewportHeight <= 0) return null
      for (var i = 0; i < cards.length; i++) {
        var card = cards[i]
        if (!card || typeof card.getBoundingClientRect !== 'function') continue
        var rect = card.getBoundingClientRect()
        if (rect.bottom >= 0 && rect.top <= viewportHeight) return card
      }
      return null
    }

    function nextSkeletonCardForHydration() {
      var cards = skeletonCards()
      if (!cards.length) return null
      var visible = visibleSkeletonCard(cards)
      if (visible) return visible
      if (state.currentIndex >= 0) {
        var best = null
        var bestDistance = Number.POSITIVE_INFINITY
        for (var i = 0; i < cards.length; i++) {
          var distance = Math.abs(cardOffset(cards[i]) - state.currentIndex)
          if (distance < bestDistance) {
            best = cards[i]
            bestDistance = distance
          }
        }
        if (best) return best
      }
      return cards[cards.length - 1]
    }

    function findCardByVideoId(videoId) {
      var wanted = String(videoId || '').trim()
      if (!wanted) return null
      syncCardList()
      for (var i = 0; i < state.cards.length; i++) {
        if (String(state.cards[i].getAttribute('data-video-id') || '') === wanted) return state.cards[i]
      }
      return null
    }

    function cardOffset(card) {
      var raw = parseInt(card && card.getAttribute ? card.getAttribute('data-card-index') || '' : '', 10)
      if (Number.isFinite(raw) && raw >= 0) return raw
      syncCardList()
      for (var i = 0; i < state.cards.length; i++) {
        if (state.cards[i] === card) return i
      }
      return 0
    }

    function replaceHydratedCards(html) {
      var parsed = new DOMParser().parseFromString(String(html || ''), 'text/html')
      var hydrated = Array.prototype.slice.call(parsed.querySelectorAll('a.video-card[data-video-id]'))
      var replaced = 0
      hydrated.forEach(function (node) {
        var id = String(node.getAttribute('data-video-id') || '').trim()
        if (!id) return
        var current = findCardByVideoId(id)
        if (!current || !current.parentNode) return
        node.setAttribute('data-card-hydrated', '1')
        current.parentNode.replaceChild(node, current)
        replaced += 1
      })
      if (replaced > 0) {
        appendNewItemsFromGrid()
        ensureGridThumbnails()
      }
      return replaced
    }

    function hydrateCardsAtOffset(offset, limit) {
      if (currentTab === 'stories') return Promise.resolve(0)
      var url = '/api/shorts/cards?tab=' + encodeURIComponent(currentTab) +
        '&offset=' + encodeURIComponent(String(Math.max(0, parseInt(offset, 10) || 0))) +
        '&limit=' + encodeURIComponent(String(Math.max(1, parseInt(limit, 10) || hydrateBatchSize)))
      return fetch(url, {
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      }).then(function (response) {
        if (!response.ok) throw new Error('HTTP ' + response.status)
        return response.text()
      }).then(function (html) {
        return replaceHydratedCards(html)
      }).catch(function () {
        return 0
      })
    }

    function hydrateCardElement(card) {
      if (!isSkeletonCard(card)) return Promise.resolve(card || null)
      if (card._shortsHydratePromise) return card._shortsHydratePromise
      card.setAttribute('data-card-hydrating', '1')
      var videoId = card.getAttribute('data-video-id')
      var offset = Math.max(0, cardOffset(card) - Math.floor(hydrateBatchSize / 2))
      card._shortsHydratePromise = hydrateCardsAtOffset(offset, hydrateBatchSize).then(function () {
        return findCardByVideoId(videoId)
      }).finally(function () {
        delete card._shortsHydratePromise
        try { card.removeAttribute('data-card-hydrating') } catch (_) {}
      })
      return card._shortsHydratePromise
    }

    function scheduleHydrationStep(delayMs) {
      var run = function () {
        var next = nextSkeletonCardForHydration()
        if (!next) return
        hydrateCardElement(next).then(function () {
          if (skeletonCards().length > 0) scheduleHydrationStep(90)
        })
      }
      if (delayMs && delayMs > 0) {
        setTimeout(run, delayMs)
      } else if (window.requestIdleCallback) {
        requestIdleCallback(run, { timeout: 800 })
      } else {
        setTimeout(run, 120)
      }
    }

    function startBackgroundCardHydration(delayMs) {
      if (currentTab === 'stories' || state.storyMode) return
      if (backgroundHydrationStarted) return
      backgroundHydrationStarted = true
      scheduleHydrationStep(delayMs || 500)
    }

    function storyBuffer() {
      var buffer = doc.getElementById('story-playlist-buffer')
      if (!buffer) {
        buffer = doc.createElement('div')
        buffer.id = 'story-playlist-buffer'
        buffer.className = 'story-playlist-buffer hidden'
        sourceContainer.appendChild(buffer)
      }
      return buffer
    }

    function captureStoryReturnState() {
      var entry = state.currentIndex >= 0 && state.items[state.currentIndex] ? state.items[state.currentIndex] : null
      var video = entry && entry.refs && entry.refs.video
      var videoTime = 0
      if (video) {
        try { videoTime = Number(video.currentTime || 0) || 0 } catch (_) { videoTime = 0 }
      }
      return {
        overlayOpen: !!state.overlayOpen,
        currentIndex: state.currentIndex,
        scrollTop: Number(shortsContainer.scrollTop || 0),
        scrollLeft: Number(shortsContainer.scrollLeft || 0),
        windowScrollY: Number(window.scrollY || 0),
        autoPlayNext: !!state.autoPlayNext,
        videoTime: videoTime
      }
    }

    function enterStoryMode(channelId, buffer) {
      if (!state.storyMode) state.storyReturnState = captureStoryReturnState()
      state.storyMode = true
      state.storyChannelId = String(channelId || '').trim()
      state.storySourceContainer = buffer
      state.persistLastViewed = false
      state.autoPlayNext = false
      layout.classList.add('shorts-story-mode')
      ensureStoryContainerScrollBehavior()
      closeBookmarkMenu()
      shortsContainer.replaceChildren()
      state.cards = []
      state.items = []
      state.byId = new Map()
      state.cardIndexById = new Map()
      state.currentIndex = -1
      state.overlayOpen = false
      state.overlayHydrated = false
      state.renderedStart = -1
      state.renderedEnd = -1
      state.lastVisibleId = ''
      state.activePlayPromise = null
      syncCardList()
      appendNewItemsFromGrid()
      updateStoryChrome()
      updateStoryGridButton()
    }

    function exitStoryMode(options) {
      if (!state.storyMode) return false
      var opts = options || {}
      var returnState = state.storyReturnState
      state.storyReturnState = null
      state.storyMode = false
      state.storyChannelId = ''
      state.storySourceContainer = null
      state.persistLastViewed = basePersistLastViewed
      state.storyQueue = []
      state.storyQueueIndex = -1
      state.storyContinueAcrossAccounts = false
      if (returnState) state.autoPlayNext = !!returnState.autoPlayNext
      layout.classList.remove('shorts-story-mode', 'shorts-story-account-slide-next', 'shorts-story-account-slide-prev')
      updateStoryGridButton()
      var buffer = doc.getElementById('story-playlist-buffer')
      if (buffer && buffer.parentNode) buffer.parentNode.removeChild(buffer)
      removeStoryChrome()
      ensureContainerScrollBehavior()
      shortsContainer.replaceChildren()
      state.cards = []
      state.items = []
      state.byId = new Map()
      state.cardIndexById = new Map()
      state.currentIndex = -1
      state.overlayHydrated = false
      state.renderedStart = -1
      state.renderedEnd = -1
      syncCardList()
      if (opts.restore === false) return false
      if (returnState && returnState.overlayOpen && state.cards.length) {
        var idx = Math.max(0, Math.min(state.cards.length - 1, Number(returnState.currentIndex || 0)))
        openOverlayAtIndex(idx, true)
        if (returnState.videoTime > 0) {
          requestAnimationFrame(function () {
            var restored = state.currentIndex >= 0 && state.items[state.currentIndex] ? state.items[state.currentIndex] : null
            var video = restored && restored.refs && restored.refs.video
            if (video) {
              try { video.currentTime = returnState.videoTime } catch (_) {}
            }
          })
        }
        return true
      }
      showGrid()
      if (returnState && returnState.windowScrollY) {
        requestAnimationFrame(function () { window.scrollTo(0, returnState.windowScrollY) })
      }
      return true
    }

    function openNextQueuedStory() {
      if (!state.storyContinueAcrossAccounts) return false
      var queue = state.storyQueue || []
      if (!queue.length || state.storyQueueIndex < 0 || state.storyQueueIndex >= queue.length) {
        syncStoryQueueFromTray()
        queue = state.storyQueue || []
      }
      var nextIndex = state.storyQueueIndex + 1
      if (!queue.length || nextIndex < 0 || nextIndex >= queue.length) return false
      var next = queue[nextIndex]
      openStoryChannel(next.channelId, next.firstVideoId, { queue: queue, queueIndex: nextIndex, continueAcrossAccounts: true, continueQueue: true, accountTransition: 'next' })
      return true
    }

    function openPreviousQueuedStory() {
      if (!state.storyContinueAcrossAccounts) return false
      var queue = state.storyQueue || []
      if (!queue.length || state.storyQueueIndex < 0 || state.storyQueueIndex >= queue.length) {
        syncStoryQueueFromTray()
        queue = state.storyQueue || []
      }
      var prevIndex = state.storyQueueIndex - 1
      if (!queue.length || prevIndex < 0 || prevIndex >= queue.length) return false
      var prev = queue[prevIndex]
      openStoryChannel(prev.channelId, '', { queue: queue, queueIndex: prevIndex, continueAcrossAccounts: true, startAtEnd: true, accountTransition: 'prev' })
      return true
    }

    function openStoryChannel(channelId, firstVideoHint, options) {
      var cid = String(channelId || '').trim()
      if (!cid) return false
      var opts = options || {}
      var queue = opts.continueAcrossAccounts ? normalizeStoryQueue(opts.queue || []) : []
      var queueIndex = Number.isFinite(opts.queueIndex) ? opts.queueIndex : queue.findIndex(function (entry) { return entry.channelId === cid })
      if (opts.continueAcrossAccounts && !queue.length) {
        var trayQueue = storyQueueFromTray()
        var trayIndex = trayQueue.findIndex(function (entry) { return entry.channelId === cid })
        if (trayIndex >= 0) {
          queue = trayQueue
          queueIndex = trayIndex
        }
      }
      if (queue.length && queueIndex >= 0) {
        state.storyQueue = queue
        state.storyQueueIndex = queueIndex
      }
      var buffer = storyBuffer()
      buffer.setAttribute('aria-busy', 'true')
      return fetch('/api/stories/' + encodeURIComponent(cid) + '/cards', {
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      }).then(function (response) {
        if (!response.ok) throw new Error('HTTP ' + response.status)
        return response.text()
      }).then(function (html) {
        var parsed = new DOMParser().parseFromString(String(html || ''), 'text/html')
        var cards = Array.prototype.slice.call(parsed.querySelectorAll('a.video-card[data-video-id]'))
        buffer.replaceChildren()
        cards.forEach(function (card, idx) {
          card.setAttribute('data-card-hydrated', '1')
          card.setAttribute('data-card-index', String(idx))
          buffer.appendChild(doc.importNode(card, true))
        })
        buffer.removeAttribute('aria-busy')
        if (!cards.length) {
          if (opts.continueQueue && openNextQueuedStory()) return true
          showToast(t('stories_empty', 'No stories'))
          return false
        }
        state.storyContinueAcrossAccounts = !!opts.continueAcrossAccounts
        if (queue.length) {
          state.storyQueue = queue
          state.storyQueueIndex = queueIndex >= 0 ? queueIndex : 0
        } else {
          state.storyQueue = []
          state.storyQueueIndex = -1
        }
        enterStoryMode(cid, buffer)
        var first = String(firstVideoHint || '').trim()
        var target = null
        if (opts.startAtEnd) {
          target = cards[cards.length - 1]
        }
        if (!target) target = first && buffer.querySelector('a.video-card[data-video-id="' + cssEscape(first) + '"]')
        if (!target) target = buffer.querySelector('a.video-card[data-story-unseen="1"]') || buffer.querySelector('a.video-card')
        var targetId = String(target && target.getAttribute('data-video-id') || '')
        if (targetId) openOverlayByVideoId(targetId, true)
        playStoryAccountTransition(opts.accountTransition)
        return true
      }).catch(function () {
        buffer.removeAttribute('aria-busy')
        if (opts.continueQueue && openNextQueuedStory()) return true
        showToast(t('error_loading_stories', 'Could not load stories'))
        return false
      })
    }

    function handleInfiniteAppend(event) {
      var detail = event && event.detail ? event.detail : {}
      if (String(detail.containerSelector || '') !== String(infiniteContainerSelector)) return
      var before = state.items.length
      var added = appendNewItemsFromGrid()
      if (added > 0 && state.overlayOpen && state.currentIndex >= before - 4) {
        requestMoreIfNeeded()
      }
    }

    function waitForInfiniteAppend(timeoutMs) {
      return new Promise(function (resolve) {
        var done = false
        var timer = setTimeout(finish, Math.max(120, Number(timeoutMs || 1200)))
        function finish() {
          if (done) return
          done = true
          clearTimeout(timer)
          doc.removeEventListener('mpa:infinite-append', onAppend)
          resolve()
        }
        function onAppend(event) {
          var detail = event && event.detail ? event.detail : {}
          if (detail.containerSelector !== '#video-grid') return
          finish()
        }
        doc.addEventListener('mpa:infinite-append', onAppend)
      })
    }

    function loadUntilVideoId(videoId, maxPages) {
      var targetId = String(videoId || '').trim()
      if (!targetId) return Promise.resolve(false)
      if (state.cardIndexById.has(targetId)) return Promise.resolve(true)
      function waitForController(tries) {
        var ctl = getShortsInfiniteController()
        if (ctl) return Promise.resolve(ctl)
        if ((tries || 0) <= 0) return Promise.resolve(null)
        return new Promise(function (resolve) {
          setTimeout(function () { resolve(waitForController((tries || 0) - 1)) }, 120)
        })
      }

      var remaining = Math.max(0, Number(maxPages || 0))
      function step(controller) {
        if (state.cardIndexById.has(targetId)) return Promise.resolve(true)
        if (remaining <= 0) return Promise.resolve(false)
        if (controller.failed) return Promise.resolve(false)
        var nextUrl = String(controller.nextUrl() || '').trim()
        if (!nextUrl) return Promise.resolve(false)
        remaining -= 1

        if (controller.loading) {
          return waitForInfiniteAppend(1800).then(function () { return step(controller) })
        }

        controller.loadNext()
        return waitForInfiniteAppend(2200).then(function () { return step(controller) })
      }

      return waitForController(30).then(function (controller) {
        if (!controller) return false
        return step(controller)
      })
    }

    // ── Event handlers ──

    function onGridClick(event) {
      if (event.defaultPrevented) return
      if (event.button && event.button !== 0) return
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
      var storyRow = event.target && event.target.closest ? event.target.closest('.story-channel-row[data-story-channel-id]') : null
      if (storyRow && sourceContainer.contains(storyRow)) {
        var queued = storyQueueFromRow(storyRow)
        event.preventDefault()
        openStoryChannel(
          storyRow.getAttribute('data-story-channel-id'),
          storyRow.getAttribute('data-story-first-video-id'),
          { queue: queued.queue, queueIndex: queued.index, continueAcrossAccounts: true }
        )
        return
      }
      if (event.target && event.target.closest && event.target.closest('.video-card-action, button, input, textarea, select')) return
      var anchor = event.target && event.target.closest ? event.target.closest('a.video-card') : null
      if (!anchor || !sourceContainer.contains(anchor)) return
      if (anchor.matches && sourceCardSelector && !anchor.matches(sourceCardSelector)) return
      event.preventDefault()
      syncCardList()
      var videoId = String(anchor.getAttribute('data-video-id') || '')
      if (!videoId) return
      if (isSkeletonCard(anchor)) {
        hydrateCardElement(anchor).then(function (hydratedCard) {
          appendNewItemsFromGrid()
          openOverlayByVideoId(videoId, false)
          if (hydratedCard && typeof hydratedCard.scrollIntoView === 'function') {
            try { hydratedCard.scrollIntoView({ behavior: 'auto', block: 'center' }) } catch (_) {}
          }
        })
        startBackgroundCardHydration(400)
        return
      }
      appendNewItemsFromGrid()
      openOverlayByVideoId(videoId, false)
    }

    function onLayoutKeydown(event) {
      if (!state.overlayOpen) return
      if (event.key === 'Escape') {
        if (state.bookmarkMenu) { event.preventDefault(); closeBookmarkMenu(); return }
        if (state.storyMode) { event.preventDefault(); showGrid(); return }
        if (state.storyTray && state.storyTray.classList.contains('open')) { event.preventDefault(); closeStoryTray(); return }
        return
      }
      var tag = event.target && event.target.tagName ? String(event.target.tagName).toLowerCase() : ''
      if (tag === 'input' || tag === 'textarea' || tag === 'select') return
      if (state.storyMode && event.key === 'ArrowRight') {
        event.preventDefault()
        goStoryNextManual()
        return
      }
      if (state.storyMode && event.key === 'ArrowLeft') {
        event.preventDefault()
        goStoryPrevManual()
        return
      }
      if (event.key === 'ArrowDown') {
        event.preventDefault()
        goNext()
        return
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault()
        goPrev()
        return
      }
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault()
        var entry = state.currentIndex >= 0 && state.items[state.currentIndex] ? state.items[state.currentIndex] : null
        var video = entry && entry.refs && entry.refs.video
        if (video) video.currentTime = Math.max(0, Math.min(video.duration || 0, video.currentTime + (event.key === 'ArrowRight' ? 3 : -3)))
        return
      }
      var entry = state.currentIndex >= 0 && state.items[state.currentIndex] ? state.items[state.currentIndex] : null
      var sc = window.cfShortcuts
      if (sc && sc.match('shorts.mute', event.key)) {
        event.preventDefault()
        state.muted = !state.muted
        localStorage.setItem('shortsMuted', state.muted)
        document.querySelectorAll('#shorts-container video').forEach(function (v) { v.muted = state.muted })
        state.items.forEach(function (e) {
          var a = e && e.refs && e.refs.slideshow && e.refs.slideshow.audio
          if (a) a.muted = state.muted
        })
        updateCurrentActionButtons()
        showToast(state.muted ? t('toast_muted', 'Muted') : t('toast_unmuted', 'Unmuted'))
        return
      }
      if (sc && sc.match('shorts.autoplay', event.key)) {
        event.preventDefault()
        if (state.storyMode) return
        state.autoPlayNext = !state.autoPlayNext
        localStorage.setItem('shortsAutoPlayNext', state.autoPlayNext)
        document.querySelectorAll('#shorts-overlay video').forEach(function (v) { v.loop = !state.autoPlayNext })
        state.items.forEach(function (e) {
          var a = e && e.refs && e.refs.slideshow && e.refs.slideshow.audio
          if (a) a.loop = !state.autoPlayNext
        })
        updateCurrentActionButtons()
        showToast(tf('shorts_autoplay_next_state', 'Auto-play next short: %1$s', state.autoPlayNext ? t('state_on', 'ON') : t('state_off', 'OFF')))
        return
      }
      if (sc && sc.match('shorts.bookmark', event.key) && entry) {
        event.preventDefault()
        var bBtn = entry.refs && entry.refs.bookmarkBtn
        if (bBtn) bBtn.click()
        return
      }
      if (sc && sc.match('shorts.share', event.key) && entry) {
        event.preventDefault()
        var sBtn = entry.refs && entry.refs.shareBtn
        if (sBtn) sBtn.click()
        return
      }
      if (sc && sc.match('shorts.grid', event.key)) {
        event.preventDefault()
        showGrid()
        return
      }
    }

    function onWheel(event) {
      if (!state.overlayOpen) return
      if (event.target && event.target.closest && event.target.closest('.bookmark-sheet-overlay')) return
      event.preventDefault()
      if (state.wheelLocked) return
      var primaryDelta = Number(event.deltaY || 0)
      if (state.storyMode && Math.abs(Number(event.deltaX || 0)) > Math.abs(primaryDelta)) {
        primaryDelta = Number(event.deltaX || 0)
      }
      if (Math.abs(primaryDelta) < 12) return
      if (!state.storyMode && Math.abs(primaryDelta) < Math.abs(Number(event.deltaX || 0))) return
      state.wheelLocked = true
      setTimeout(function () { state.wheelLocked = false }, 360)
      if (state.storyMode) {
        if (primaryDelta > 0) goStoryNextManual(); else goStoryPrevManual()
      } else if (primaryDelta > 0) goNext(); else goPrev()
    }

    function onTouchStart(event) {
      if (!state.overlayOpen) return
      if (!event.changedTouches || !event.changedTouches.length) return
      state.touchStartX = Number(event.changedTouches[0].screenX || 0)
      state.touchStartY = Number(event.changedTouches[0].screenY || 0)
      state.touchStartScrollTop = Number(shortsContainer.scrollTop || 0)
    }

    function onTouchEnd(event) {
      if (!state.overlayOpen) return
      if (!event.changedTouches || !event.changedTouches.length) return
      var endX = Number(event.changedTouches[0].screenX || 0)
      var endY = Number(event.changedTouches[0].screenY || 0)
      var diffX = state.touchStartX - endX
      var diffY = state.touchStartY - endY
      if (state.storyMode) {
        if (Math.abs(diffX) < 65 || Math.abs(diffX) < Math.abs(diffY)) return
        if (diffX > 0) goStoryNextManual(); else goStoryPrevManual()
        return
      }
      var diff = diffY
      if (Math.abs(diff) < 65) return
      var scrollDiff = Math.abs(Number(shortsContainer.scrollTop || 0) - Number(state.touchStartScrollTop || 0))
      if (scrollDiff > 30) return
      if (diff > 0) goNext(); else goPrev()
    }

    function onTabClick(event) {
      if (event.defaultPrevented) return
      if (event.button && event.button !== 0) return
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
      var anchor = event.target && event.target.closest ? event.target.closest('a.shorts-player-tab, .shorts-tabs a') : null
      if (!anchor) return
      var tab = tabFromLink(anchor)
      event.preventDefault()
      if (state.overlayOpen && tab === 'stories') {
        openStoryTray()
        return
      }
      switchShortsTab(tab)
    }

    function onStoryPlayerControlClick(event) {
      if (event.defaultPrevented) return
      if (event.button && event.button !== 0) return
      var control = event.target && event.target.closest ? event.target.closest('[data-story-action]') : null
      if (!control) return
      var chrome = control.closest ? control.closest('[data-story-chrome]') : null
      if (!chrome || !state.storyMode) return
      event.preventDefault()
      event.stopPropagation()
      var action = control.getAttribute('data-story-action')
      if (action === 'prev') {
        goStoryPrevManual()
      } else if (action === 'next') {
        goStoryNextManual()
      } else if (action === 'close') {
        showGrid()
      }
    }

    function onStoryAvatarClick(event) {
      if (event.defaultPrevented) return
      var trigger = event.target && event.target.closest ? event.target.closest('[data-story-channel-id][data-story-first-video-id]') : null
      if (!trigger || sourceContainer.contains(trigger)) return
      if (event.key && event.key !== 'Enter' && event.key !== ' ') return
      event.preventDefault()
      if (event.stopImmediatePropagation) event.stopImmediatePropagation()
      openStoryChannel(
        trigger.getAttribute('data-story-channel-id'),
        trigger.getAttribute('data-story-first-video-id'),
        { continueAcrossAccounts: false }
      )
    }

    // ── Init modules ──

    initPlayback(state, goNext)

    initOverlay(state, {
      shortsContainer: shortsContainer,
      gridShell: gridShell,
      layout: layout,
      upToDateOverlay: upToDateOverlay,
      sourceContainer: sourceContainer,
      sourceCardSelector: sourceCardSelector,
      doc: doc
    }, {
      closeBookmarkMenu: closeBookmarkMenu,
      ensureGridThumbnails: ensureGridThumbnails,
      updateTopControls: updateTopControls,
      setLastViewedShortId: setLastViewedShortId,
      setLastViewedShortResume: setLastViewedShortResume,
      markShortViewed: markShortViewed,
      getShortsInfiniteController: getShortsInfiniteController,
      makeShortItem: makeShortItem,
      parseCardData: parseCardData,
      iconSvg: iconSvg,
      exitStoryMode: exitStoryMode,
      handleStoryEnd: openNextQueuedStory
    })

    initItems(state, {
      goNext: goNext,
      updateCurrentActionButtons: updateCurrentActionButtons,
      currentData: currentData,
      openStoryChannel: openStoryChannel
    })

    // ── Buttons + events ──

    function initButtons() {
      initTopControlIcons()
      if (closeBtn) closeBtn.addEventListener('click', function (e) { e.preventDefault(); showGrid() })
      if (reopenBtn) reopenBtn.addEventListener('click', function () {
        if (!state.items.length) return
        openOverlayAtIndex(state.currentIndex >= 0 ? state.currentIndex : 0)
      })
      if (prevBtn) prevBtn.addEventListener('click', function (e) { e.preventDefault(); goPrev() })
      if (nextBtn) nextBtn.addEventListener('click', function (e) { e.preventDefault(); goNext() })
    }

    function initEvents() {
      sourceContainer.addEventListener('click', onGridClick)
      doc.addEventListener('click', onTabClick)
      doc.addEventListener('click', onStoryPlayerControlClick)
      doc.addEventListener('click', onStoryAvatarClick)
      doc.addEventListener('keydown', onStoryAvatarClick)
      doc.addEventListener('keydown', onLayoutKeydown)
      window.addEventListener('popstate', function () {
        var params = new URLSearchParams(window.location.search)
        switchShortsTab(normalizeShortsTab(params.get('tab')), { push: false })
      })
      layout.addEventListener('wheel', onWheel, { passive: false })
      layout.addEventListener('touchstart', onTouchStart, { passive: true })
      layout.addEventListener('touchend', onTouchEnd, { passive: true })
      shortsContainer.addEventListener('scroll', function () {
        if (!state.overlayOpen) return
        requestMoreIfNeeded()
      }, { passive: true })
      doc.addEventListener('mpa:infinite-append', handleInfiniteAppend)
    }

    // ── Main init ──

    async function init() {
      ensureContainerScrollBehavior()
      initButtons()
      initEvents()
      var autoOpenDefault = !/^(0|false|no)$/i.test(String(layout.getAttribute('data-auto-open') || '1').trim())

      window.MpaShortsMode = {
        openByVideoId: function (videoId, immediate) {
          appendNewItemsFromGrid()
          var card = findCardByVideoId(videoId)
          if (isSkeletonCard(card)) {
            hydrateCardElement(card).then(function () {
              appendNewItemsFromGrid()
              openOverlayByVideoId(videoId, immediate !== false)
              startBackgroundCardHydration(80)
            })
            return false
          }
          if (openOverlayByVideoId(videoId, immediate !== false)) return true
          var wanted = String(videoId || '').trim()
          if (!wanted) return false
          loadUntilVideoId(wanted, 10).then(function (found) {
            if (found) openOverlayByVideoId(wanted, true)
          })
          return false
        },
        close: showGrid,
        isOpen: function () { return !!state.overlayOpen },
        refreshSource: appendNewItemsFromGrid
      }

      syncCardList()
      if (!state.cards.length) {
        if (gridShell) gridShell.classList.remove('hidden')
        layout.classList.add('hidden')
        return
      }
      var params = new URLSearchParams(window.location.search)
      var sourceContext = String(params.get('source') || '').trim().toLowerCase()
      var persistAttr = String(layout.getAttribute('data-persist-history') || '').trim().toLowerCase()
      if (persistAttr === '0' || persistAttr === 'false' || persistAttr === 'no') {
        state.persistLastViewed = false
      } else {
        state.persistLastViewed = !(sourceContext === 'creators' || sourceContext === 'bookmarks')
      }
      basePersistLastViewed = state.persistLastViewed
      var pathname = String(window.location.pathname || '')
      var currentPageParam = Math.max(1, parseInt(params.get('page') || '', 10) || state.initialPage || 1)
      var explicitVideoId = String(layout.getAttribute('data-video-hint') || params.get('video') || '').trim()
      if (state.persistLastViewed && !explicitVideoId) {
        mergeShortsCursorWithServer(await fetchShortsCursorFromServer())
      }
      var resumeInfo = getLastViewedShortResume()
      if (!autoOpenDefault) {
        if (gridShell) gridShell.classList.remove('hidden')
        layout.classList.add('hidden')
        startBackgroundCardHydration(250)
        return
      }
      var restoreVideoId = explicitVideoId || (resumeInfo && resumeInfo.videoId) || getLastViewedShortId()
      if (restoreVideoId) {
        var restoreCard = findCardByVideoId(restoreVideoId)
        if (isSkeletonCard(restoreCard)) {
          hydrateCardElement(restoreCard).then(function () {
            appendNewItemsFromGrid()
            if (!openOverlayByVideoId(restoreVideoId, true)) openOverlayAtIndex(0)
            startBackgroundCardHydration(80)
          })
          return
        }
      }
      if (restoreVideoId && openOverlayByVideoId(restoreVideoId, true)) {
        startBackgroundCardHydration(80)
        return
      }
      if (!explicitVideoId && restoreVideoId) {
        loadUntilVideoId(restoreVideoId, 5).then(function (found) {
          if (found && openOverlayByVideoId(restoreVideoId, true)) return
          try {
            localStorage.removeItem(shortsResumeStorageKey())
            if (currentTab === 'all') localStorage.removeItem('shortsLastResumeV2')
          } catch (_) { }
          try { localStorage.removeItem('shortsLastViewedIdV1') } catch (_) { }
          openOverlayAtIndex(0)
          startBackgroundCardHydration(80)
        })
        return
      }
      openOverlayAtIndex(0)
      startBackgroundCardHydration(80)
    }

    init()

    // ── Unified sync: moments cursor from another device ──
    if (window.SyncPoller) {
      window.SyncPoller.on('moment_view', function () {
        scheduleStorySurfaceRefresh(true)
      })
      window.SyncPoller.on('moments_cursor', function (videoId, value) {
        if (!state.persistLastViewed) return
        value = value || {}
        var scope = String(value.scope || 'all').trim().toLowerCase()
        if (scope !== 'all' && scope !== 'following') return
        if (scope !== currentTab) return
        var localRaw = localStorage.getItem(shortsResumeStorageKey()) || (currentTab === 'all' ? localStorage.getItem('shortsLastResumeV2') : null)
        var local = localRaw ? JSON.parse(localRaw) : null
        if (local && local.videoId === videoId) return
        var remote = { videoId: videoId, page: 1, index: 0, ts: Date.now(), scope: currentTab }
        localStorage.setItem(shortsResumeStorageKey(), JSON.stringify(remote))
        if (currentTab === 'all') localStorage.setItem('shortsLastResumeV2', JSON.stringify(remote))
        if (state.overlayOpen) {
          openOverlayByVideoId(videoId, false)
        } else {
          var idx = state.cardIndexById && state.cardIndexById.get(videoId)
          var card = idx !== undefined && state.cards[idx]
          if (card) {
            try { card.scrollIntoView({ behavior: 'smooth', block: 'center' }) } catch (_) {}
          }
        }
      })
    }
  })()
}
