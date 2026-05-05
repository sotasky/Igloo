// Media overlay module — extracted from feed_page.js
// Opens a fullscreen overlay with image/video media + tweet info sidebar.

import {
  makeDraggableSeekbar,
  attachSeekTooltip,
  itemRootFromNode,
  stateBool,
  getFeedActionIconSvg,
  syncFeedActionIcons,
  formatRelative,
  formatAbsolute,
  t,
} from '../utils.js'

// ── Helpers ──

function textContentTrim(node) {
  return String((node && node.textContent) || '').trim()
}

function getRetweetMutedChannels() {
  let raw = ''
  try {
    raw = localStorage.getItem('feedMutedRetweetChannels') || ''
    if (!raw) raw = localStorage.getItem('mpa-feed-retweet-muted:v1') || ''
  } catch (_) { raw = '' }
  if (!raw) return new Set()
  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return new Set()
    return new Set(parsed.map(function (v) { return String(v || '').trim() }).filter(Boolean))
  } catch (_) { return new Set() }
}

function updateRetweetMenuLabels(scope) {
  const root = scope || document
  root.querySelectorAll('[data-feed-menu-action="retweets_off"]').forEach(function (btn) {
    const channelId = String(btn.getAttribute('data-feed-channel-id') || '').trim()
    const muted = channelId ? getRetweetMutedChannels().has(channelId) : false
    btn.textContent = muted
      ? t('feed_turn_on_retweets', 'Turn on retweets')
      : t('feed_turn_off_retweets', 'Turn off retweets')
  })
}

// ── Module state ──

let overlayEl = null
let keyHandler = null

function setFeedMediaOverlayOpen(open) {
  if (!document.body) return
  document.body.classList.toggle('feed-media-overlay-open', !!open)
}

// ── Media source extraction ──

// Extract slide list from any feed item subtree (parent article or quote card).
// Returns { slides, singleVideo } where:
//   slides    = array of { kind, url, streamUrl, posterUrl } (possibly empty)
//   singleVideo = { streamUrl, posterUrl } when root contains exactly one
//                 standalone video wrap (no grid), else null. Used to preserve
//                 the large-video fast path when the overall overlay has only
//                 one slide.
function extractSlidesFromRoot(rootEl) {
  if (!rootEl) return { slides: [], singleVideo: null }

  var rootIsQuote = rootEl.classList && rootEl.classList.contains('feed-quote-card')

  // Prefer grid if present at this scope.
  var grid = null
  var gridCandidates = rootEl.querySelectorAll('.feed-media-wrap-grid')
  for (var gi = 0; gi < gridCandidates.length; gi++) {
    var gc = gridCandidates[gi]
    if (rootIsQuote) { grid = gc; break }
    if (!gc.closest('.feed-quote-card')) { grid = gc; break }
  }

  if (grid) {
    var tiles = grid.querySelectorAll('.feed-media-tile')
    var slides = []
    tiles.forEach(function (tile) {
      var tKind = String(tile.getAttribute('data-feed-media-kind') || 'image').trim().toLowerCase()
      var tUrl = String(tile.getAttribute('data-feed-media-url') || '').trim()
      var tStream = String(tile.getAttribute('data-feed-media-stream') || '').trim()
      var tPoster = String(tile.getAttribute('data-feed-media-preview') || '').trim()
      if (!tUrl) {
        var img = tile.querySelector('.feed-media-image')
        if (img) tUrl = String(img.getAttribute('src') || '').trim()
      }
      slides.push({ kind: tKind, url: tUrl, streamUrl: tStream, posterUrl: tPoster })
    })
    return { slides: slides, singleVideo: null }
  }

  // No grid — look for a single .feed-media-wrap at this scope (not inside a nested quote card).
  var wraps = rootEl.querySelectorAll('.feed-media-wrap')
  var wrap = null
  for (var wi = 0; wi < wraps.length; wi++) {
    var w = wraps[wi]
    if (rootIsQuote) { wrap = w; break }
    if (!w.closest('.feed-quote-card')) { wrap = w; break }
  }
  if (!wrap) return { slides: [], singleVideo: null }

  var wKind = String(wrap.getAttribute('data-feed-media-kind') || '').trim().toLowerCase()
  if (wKind === 'video') {
    var streamUrl = String(wrap.getAttribute('data-feed-media-stream') || '').trim()
    var posterUrl = String(wrap.getAttribute('data-feed-media-preview') || '').trim()
    if (!posterUrl) {
      var vidEl = wrap.querySelector('video')
      if (vidEl) posterUrl = String(vidEl.getAttribute('poster') || '').trim()
    }
    return {
      slides: [{ kind: 'video', url: '', streamUrl: streamUrl, posterUrl: posterUrl }],
      singleVideo: { streamUrl: streamUrl, posterUrl: posterUrl },
    }
  }

  var imgUrl = String(wrap.getAttribute('data-feed-media-url') || '').trim()
  if (!imgUrl) {
    var imgEl = wrap.querySelector('.feed-media-image')
    if (imgEl) imgUrl = String(imgEl.getAttribute('src') || '').trim()
  }
  if (!imgUrl) return { slides: [], singleVideo: null }
  return {
    slides: [{ kind: 'image', url: imgUrl, streamUrl: '', posterUrl: '' }],
    singleVideo: null,
  }
}

function getMediaSources(card, clickedEl) {
  const root = itemRootFromNode(card) || card
  if (!root) return null
  const trigger = clickedEl && clickedEl.closest ? clickedEl.closest('[data-feed-media]') : null
  const triggerInQuote = !!(trigger && trigger.closest && trigger.closest('.feed-quote-card'))

  const quoteCardEl = root.querySelector('.feed-quote-card')
  const parentExtract = extractSlidesFromRoot(root)
  const quoteExtract = quoteCardEl ? extractSlidesFromRoot(quoteCardEl) : { slides: [], singleVideo: null }

  const parentSlides = parentExtract.slides.map(function (s) { return Object.assign({}, s, { source: 'parent' }) })
  const quoteSlides = quoteExtract.slides.map(function (s) { return Object.assign({}, s, { source: 'quote' }) })
  const slides = parentSlides.concat(quoteSlides)

  if (slides.length === 0) return null

  // Preserve the single-standalone-video fast path only when the entire overlay
  // is that one video (no grid, no other side). Mixed / multi-slide cases always
  // use the slides[] path.
  if (slides.length === 1) {
    var only = slides[0]
    var onlyFromParent = parentSlides.length === 1
    var onlySingleVideo = onlyFromParent ? parentExtract.singleVideo : quoteExtract.singleVideo
    if (only.kind === 'video' && onlySingleVideo) {
      return {
        kind: 'video',
        streamUrl: onlySingleVideo.streamUrl,
        posterUrl: onlySingleVideo.posterUrl,
        urls: [],
        slides: slides,
        startIndex: 0,
      }
    }
  }

  // Pick start index based on click origin.
  var startIndex = 0
  if (trigger) {
    var triggerUrl = String(trigger.getAttribute('data-feed-media-url') || '').trim()
    var triggerStream = String(trigger.getAttribute('data-feed-media-stream') || '').trim()
    var rangeFrom = triggerInQuote ? parentSlides.length : 0
    var rangeTo = triggerInQuote ? slides.length : parentSlides.length
    for (var si = rangeFrom; si < rangeTo; si++) {
      var s = slides[si]
      if (triggerUrl && s.url === triggerUrl) { startIndex = si; break }
      if (triggerStream && s.streamUrl === triggerStream) { startIndex = si; break }
    }
    if (rangeFrom >= rangeTo) startIndex = 0
  }

  const anyVideo = slides.some(function (s) { return s.kind === 'video' })
  const urls = slides.map(function (s) { return s.url })

  return {
    kind: anyVideo || slides.length > 1 ? 'mixed' : 'image',
    slides: slides,
    urls: urls,
    streamUrl: '',
    posterUrl: '',
    startIndex: startIndex,
  }
}

// ── Close overlay ──

export function closeMediaOverlay() {
  if (overlayEl) {
    var srcVid = overlayEl._sourceVideo
    var ovVid = overlayEl._overlayVideo
    if (srcVid && ovVid) {
      try { srcVid.currentTime = ovVid.currentTime } catch (_) { }
      srcVid.play().catch(function () { })
    }
    if (overlayEl.parentNode) overlayEl.remove()
  }
  overlayEl = null
  setFeedMediaOverlayOpen(false)
  if (keyHandler) {
    document.removeEventListener('keydown', keyHandler)
    keyHandler = null
  }
}

// ── Open overlay ──

export function openMediaOverlay(root, triggerEl) {
  if (window.matchMedia && window.matchMedia('(max-width: 768px)').matches) return
  const media = getMediaSources(root, triggerEl)
  if (!media) return
  closeMediaOverlay()
  const article = itemRootFromNode(root) || root
  const tweetId = String(article.getAttribute('data-tweet-id') || '').trim()
  const link = String(article.getAttribute('data-feed-link') || '').trim()
  const channelId = String(article.getAttribute('data-channel-id') || '').trim()
  const channelName = String(article.getAttribute('data-channel-name') || '').trim()
  const channelPlatform = String(article.getAttribute('data-channel-platform') || 'twitter').trim() || 'twitter'

  const triggerQuoteCard = triggerEl && triggerEl.closest ? triggerEl.closest('.feed-quote-card') : null
  const quoteCardEl = triggerQuoteCard || article.querySelector('.feed-quote-card')
  const isQuoteMedia = !!triggerQuoteCard
  const quoteTweetId = quoteCardEl ? String(quoteCardEl.getAttribute('data-quote-tweet-id') || '').trim() : ''
  const quoteLink = quoteCardEl ? String(quoteCardEl.getAttribute('data-quote-link') || '').trim() : ''

  let currentIndex = Math.max(0, Number(media.startIndex || 0))
  const overlay = document.createElement('div')
  overlay.className = 'feed-media-overlay'
  if (tweetId) overlay.setAttribute('data-feed-overlay-tweet-id', tweetId)
  // Quote overlay attribute is toggled per slide by renderSidebar.
  // Static overlay shell template — no user input
  overlay.innerHTML = '' + // eslint-disable-line no-unsanitized/property
    '<div class="feed-media-overlay-shell">' +
    '<button class="feed-media-overlay-close" type="button" aria-label="' + t('action_close', 'Close') + '">\u00d7</button>' +
    '<div class="feed-media-overlay-main">' +
    '<div class="feed-media-overlay-left">' +
    '<button class="feed-media-overlay-nav prev" type="button" aria-label="' + t('action_previous', 'Previous') + '">\u2039</button>' +
    '<div class="feed-media-overlay-media"></div>' +
    '<button class="feed-media-overlay-nav next" type="button" aria-label="' + t('action_next', 'Next') + '">\u203a</button>' +
    '<div class="feed-media-overlay-counter"></div>' +
    '</div>' +
    '<div class="feed-media-overlay-right">' +
    '<div class="feed-media-overlay-top"></div>' +
    '<div class="feed-media-overlay-bottom"></div>' +
    '</div>' +
    '</div>' +
    '</div>'
  document.body.appendChild(overlay)
  overlayEl = overlay
  setFeedMediaOverlayOpen(true)

  const top = overlay.querySelector('.feed-media-overlay-top')
  const host = overlay.querySelector('.feed-media-overlay-media')
  const counter = overlay.querySelector('.feed-media-overlay-counter')
  const prev = overlay.querySelector('.feed-media-overlay-nav.prev')
  const next = overlay.querySelector('.feed-media-overlay-nav.next')
  const bottom = overlay.querySelector('.feed-media-overlay-bottom')

  function renderSidebar(sourceKind) {
    const isQuote = sourceKind === 'quote' && !!quoteCardEl
    const sourceCard = isQuote ? quoteCardEl : article

    // ── Ownership attribute — downstream handlers in feed/index.js read this.
    if (isQuote && quoteTweetId) {
      overlay.setAttribute('data-feed-overlay-quote-tweet-id', quoteTweetId)
    } else {
      overlay.removeAttribute('data-feed-overlay-quote-tweet-id')
    }

    // ── Top (author / body / date / header actions) ──
    if (top) {
      while (top.firstChild) top.removeChild(top.firstChild)
      top.classList.add('channel-row')

      var authorLabel, authorHandleRaw, showAuthorHandle, dateText, dateAbsolute, bodySourceEl, titleText, summaryText, repostText
      var overlayChannelId = channelId

      if (isQuote) {
        authorLabel = textContentTrim(sourceCard.querySelector('.feed-quote-author')) || t('feed_quoted_post', 'Quoted post')
        authorHandleRaw = textContentTrim(sourceCard.querySelector('.feed-author-handle')).replace(/^@+/, '')
        overlayChannelId = 'twitter_' + authorHandleRaw
        bodySourceEl = sourceCard.querySelector('.feed-quote-text')
        dateText = ''
        dateAbsolute = ''
        titleText = ''
        summaryText = ''
        repostText = ''
      } else {
        authorLabel = textContentTrim(article.querySelector('.feed-author'))
          || String(article.getAttribute('data-feed-author') || '').trim()
          || channelName
          || t('feed_x_post', 'X post')
        authorHandleRaw = textContentTrim(article.querySelector('.feed-author-handle')).replace(/^@+/, '')
        var dateEl = article.querySelector('.feed-date-inline')
        var dateRaw = String((dateEl && dateEl.getAttribute('data-feed-date-raw')) || article.getAttribute('data-feed-date') || '').trim()
        dateText = textContentTrim(dateEl).replace(/^·\s*/, '') || formatRelative(dateRaw) || dateRaw
        dateAbsolute = formatAbsolute(dateRaw)
        bodySourceEl = article.querySelector('.feed-body-text')
        titleText = textContentTrim(article.querySelector('.feed-text'))
        summaryText = textContentTrim(article.querySelector('.feed-summary'))
        repostText = textContentTrim(article.querySelector('.feed-repost-line'))
      }
      showAuthorHandle = !!(authorHandleRaw && authorLabel.toLowerCase() !== authorHandleRaw.toLowerCase())

      if (overlayChannelId) top.setAttribute('data-channel-id', overlayChannelId)
      if (channelPlatform) top.setAttribute('data-channel-platform', channelPlatform)

      if (repostText) {
        const repostEl = document.createElement('div')
        repostEl.className = 'feed-overlay-repost'
        repostEl.textContent = repostText
        top.appendChild(repostEl)
      }

      var headlineId = isQuote ? overlayChannelId : channelId
      const headline = document.createElement((headlineId || link) ? 'a' : 'div')
      headline.className = 'feed-overlay-headline'
      if (headline.tagName === 'A') {
        headline.href = headlineId ? ('/channels/' + encodeURIComponent(headlineId)) : link
        if (!headlineId && link) {
          headline.target = '_blank'
          headline.rel = 'noopener noreferrer'
        }
      }
      if (headlineId) headline.setAttribute('data-feed-channel-id', headlineId)
      var avatarSource = isQuote ? sourceCard.querySelector('.feed-quote-avatar') : article.querySelector('.feed-avatar')
      if (avatarSource) {
        var avatarClone = avatarSource.cloneNode(true)
        avatarClone.className = 'feed-avatar'
        headline.appendChild(avatarClone)
      } else {
        const fallbackAvatar = document.createElement('div')
        fallbackAvatar.className = 'feed-avatar'
        const fallbackChar = document.createElement('span')
        fallbackChar.className = 'feed-avatar-fallback'
        fallbackChar.textContent = (authorLabel.charAt(0) || 'X').toUpperCase()
        fallbackAvatar.appendChild(fallbackChar)
        headline.appendChild(fallbackAvatar)
      }
      const authorMeta = document.createElement('div')
      authorMeta.className = 'feed-overlay-author-meta'
      const authorEl = document.createElement('div')
      authorEl.className = 'feed-overlay-author'
      authorEl.textContent = authorLabel
      authorMeta.appendChild(authorEl)
      var subLine = document.createElement('div')
      subLine.className = 'feed-overlay-sub'
      if (showAuthorHandle) {
        var handleLink = document.createElement('a')
        handleLink.className = 'feed-author-handle feed-inline-link'
        handleLink.href = '/channels/' + encodeURIComponent(overlayChannelId)
        handleLink.textContent = '@' + authorHandleRaw
        subLine.appendChild(handleLink)
      }
      if (dateText) {
        if (showAuthorHandle) {
          subLine.appendChild(document.createTextNode(' \u00b7 '))
        }
        var dateSpan = document.createElement('span')
        dateSpan.className = 'feed-overlay-date'
        dateSpan.textContent = dateText
        if (dateAbsolute && dateAbsolute !== dateText) dateSpan.title = dateAbsolute
        subLine.appendChild(dateSpan)
      }
      if (subLine.childNodes.length) authorMeta.appendChild(subLine)
      headline.appendChild(authorMeta)

      if (!isQuote) {
        var headerActions = article.querySelector('.feed-header-actions')
        if (headerActions) {
          var actionsClone = headerActions.cloneNode(true)
          actionsClone.classList.add('feed-overlay-header-actions')
          headline.appendChild(actionsClone)
        }
      } else {
        var qFollowBtn = sourceCard ? sourceCard.querySelector('.feed-quote-follow-btn') : null
        if (qFollowBtn) {
          var qActionsWrap = document.createElement('div')
          qActionsWrap.className = 'feed-header-actions feed-overlay-header-actions'
          qActionsWrap.appendChild(qFollowBtn.cloneNode(true))
          headline.appendChild(qActionsWrap)
        }
      }
      top.appendChild(headline)

      if (bodySourceEl && textContentTrim(bodySourceEl)) {
        const bodyEl = document.createElement('p')
        bodyEl.className = 'feed-overlay-text'
        // Clone children to preserve @mention links and other HTML
        var childNodes = bodySourceEl.childNodes
        for (var ci = 0; ci < childNodes.length; ci++) {
          bodyEl.appendChild(childNodes[ci].cloneNode(true))
        }
        top.appendChild(bodyEl)
      } else {
        if (titleText) {
          const titleEl = document.createElement('p')
          titleEl.className = 'feed-overlay-text'
          titleEl.textContent = titleText
          top.appendChild(titleEl)
        }
        if (summaryText) {
          const summaryEl = document.createElement('p')
          summaryEl.className = 'feed-overlay-summary'
          summaryEl.textContent = summaryText
          top.appendChild(summaryEl)
        }
      }

      updateRetweetMenuLabels(top)
      syncFeedActionIcons(top)
    }

    // ── Bottom (share / heart / bookmark / open-on-X) ──
    if (bottom) {
      while (bottom.firstChild) bottom.removeChild(bottom.firstChild)
      const actionsWrap = document.createElement('div')
      actionsWrap.className = 'feed-overlay-actions'

      const shareBtn = document.createElement('button')
      shareBtn.className = 'feed-action-btn'
      shareBtn.type = 'button'
      shareBtn.setAttribute('data-feed-overlay-action', 'share')
      shareBtn.title = t('action_copy_link', 'Copy link')
      shareBtn.setAttribute('aria-label', shareBtn.title)
      // Static SVG — no user input
      shareBtn.innerHTML = getFeedActionIconSvg('share') // eslint-disable-line no-unsanitized/property
      actionsWrap.appendChild(shareBtn)

      var liked = isQuote
        ? String(sourceCard.getAttribute('data-quote-liked') || '0') === '1'
        : stateBool(article, 'liked')
      var heartBtn = document.createElement('button')
      heartBtn.className = 'feed-action-btn' + (liked ? ' active' : '')
      heartBtn.type = 'button'
      heartBtn.setAttribute('data-feed-overlay-action', 'heart')
      heartBtn.title = liked ? t('action_unlike', 'Unlike') : t('action_like', 'Like')
      heartBtn.setAttribute('aria-label', heartBtn.title)
      // Static SVG — no user input
      heartBtn.innerHTML = getFeedActionIconSvg('heart', liked) // eslint-disable-line no-unsanitized/property
      actionsWrap.appendChild(heartBtn)

      var bookmarked = isQuote
        ? String(sourceCard.getAttribute('data-quote-bookmarked') || '0') === '1'
        : stateBool(article, 'bookmarked')
      var bmBtn = document.createElement('button')
      bmBtn.className = 'feed-action-btn' + (bookmarked ? ' active' : '')
      bmBtn.type = 'button'
      bmBtn.setAttribute('data-feed-overlay-action', 'bookmark')
      bmBtn.title = bookmarked ? t('action_unbookmark', 'Unbookmark') : t('action_bookmark', 'Bookmark')
      bmBtn.setAttribute('aria-label', bmBtn.title)
      // Static SVG — no user input
      bmBtn.innerHTML = getFeedActionIconSvg('bookmark', bookmarked) // eslint-disable-line no-unsanitized/property
      actionsWrap.appendChild(bmBtn)

      var overlayLink = isQuote ? (quoteLink || link) : link
      if (overlayLink) {
        var openX = document.createElement('a')
        openX.className = 'feed-action-btn'
        openX.href = overlayLink
        openX.target = '_blank'
        openX.rel = 'noopener noreferrer'
        openX.title = t('action_open_on_x', 'Open on X')
        openX.setAttribute('aria-label', openX.title)
        openX.setAttribute('data-feed-overlay-action', 'openx')
        // Static SVG — no user input
        openX.innerHTML = getFeedActionIconSvg('link') // eslint-disable-line no-unsanitized/property
        actionsWrap.appendChild(openX)
      }
      bottom.appendChild(actionsWrap)
      syncFeedActionIcons(bottom)
    }
  }

  function render() {
    var activeSlide = (media.slides && media.slides[currentIndex]) || null
    var activeSource = activeSlide ? activeSlide.source : (isQuoteMedia ? 'quote' : 'parent')
    renderSidebar(activeSource)
    if (!host) return
    while (host.firstChild) host.removeChild(host.firstChild)
    overlay._overlayVideo = null
    var slideInfo = (media.kind === 'mixed' && media.slides && media.slides[currentIndex]) || null
    var isMixedVideo = slideInfo && slideInfo.kind === 'video' && slideInfo.streamUrl
    var isStandaloneVideo = !slideInfo && media.kind === 'video' && media.streamUrl
    var isVideo = isMixedVideo || isStandaloneVideo
    var activeStreamUrl = slideInfo ? slideInfo.streamUrl : media.streamUrl
    var activePosterUrl = slideInfo ? (slideInfo.posterUrl || slideInfo.url) : media.posterUrl

    if (isMixedVideo) {
      // Wrap the video in a sized container so the seekbar sits flush against
      // the video's bottom edge (not at the bottom of the host's padding box).
      // Same pattern as the pure-video case below — keeps both seekbars
      // visually identical regardless of slideshow context.
      var mixedWrap = document.createElement('div')
      mixedWrap.className = 'feed-overlay-video-wrap'

      var v = document.createElement('video')
      v.className = 'feed-overlay-image'
      v.autoplay = true
      v.playsInline = true
      v.loop = true
      v.muted = false
      if (activePosterUrl) v.poster = activePosterUrl
      var source = document.createElement('source')
      source.src = activeStreamUrl
      source.type = 'video/mp4'
      v.appendChild(source)
      v.addEventListener('click', function (e) {
        e.stopPropagation()
        if (v.paused) v.play().catch(function () {}); else v.pause()
      })

      var bar = document.createElement('div')
      bar.className = 'feed-video-progress feed-overlay-progress'
      var fill = document.createElement('div')
      fill.className = 'feed-video-progress-fill'
      bar.appendChild(fill)
      v.addEventListener('timeupdate', function () {
        var pct = v.duration > 0 ? (v.currentTime / v.duration) * 100 : 0
        fill.style.width = pct + '%'
      })
      makeDraggableSeekbar(bar, fill, v)
      attachSeekTooltip(bar, v)

      mixedWrap.appendChild(v)
      mixedWrap.appendChild(bar)
      host.appendChild(mixedWrap)
    } else if (isVideo) {
      var videoWrap = document.createElement('div')
      videoWrap.className = 'feed-overlay-video-wrap'

      const v = document.createElement('video')
      v.className = 'feed-overlay-video'
      v.autoplay = true
      v.playsInline = true
      v.loop = true
      if (activePosterUrl) v.poster = activePosterUrl
      const source = document.createElement('source')
      source.src = activeStreamUrl
      source.type = 'video/mp4'
      v.appendChild(source)
      v.muted = false
      overlay._overlayVideo = v

      // Static SVG markup for mute/unmute icons
      var svgUnmuted = '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/><path d="M15.54 8.46a5 5 0 0 1 0 7.07"/></svg>'
      var svgMuted = '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><line x1="23" y1="9" x2="17" y2="15"/><line x1="17" y1="9" x2="23" y2="15"/></svg>'
      var muteBtn = document.createElement('button')
      muteBtn.className = 'feed-overlay-mute-btn'
      muteBtn.type = 'button'
      muteBtn.setAttribute('aria-label', t('action_mute', 'Mute'))
      muteBtn.innerHTML = svgUnmuted // eslint-disable-line no-unsanitized/property
      muteBtn.addEventListener('click', function (e) {
        e.stopPropagation()
        v.muted = !v.muted
        muteBtn.innerHTML = v.muted ? svgMuted : svgUnmuted // eslint-disable-line no-unsanitized/property
        muteBtn.setAttribute('aria-label', v.muted ? t('action_unmute', 'Unmute') : t('action_mute', 'Mute'))
      })

      var progressBar = document.createElement('div')
      progressBar.className = 'feed-video-progress feed-overlay-progress'
      var progressFill = document.createElement('div')
      progressFill.className = 'feed-video-progress-fill'
      progressBar.appendChild(progressFill)
      v.addEventListener('timeupdate', function () {
        var dur = Number(v.duration || 0)
        var cur = Number(v.currentTime || 0)
        var pct = dur > 0 ? Math.max(0, Math.min(100, (cur / dur) * 100)) : 0
        progressFill.style.width = pct + '%'
      })
      makeDraggableSeekbar(progressBar, progressFill, v)
      attachSeekTooltip(progressBar, v)

      v.addEventListener('click', function (e) {
        e.stopPropagation()
        if (v.paused) v.play().catch(function () {}); else v.pause()
      })

      videoWrap.appendChild(v)
      videoWrap.appendChild(muteBtn)
      videoWrap.appendChild(progressBar)

      // Sync with inline video — pick up playback position
      var feedList = document.getElementById('feed-list')
      var allTriggers = feedList ? feedList.querySelectorAll('[data-feed-media][data-feed-media-kind="video"]') : []
      var inlineTrigger = null
      for (var ti = 0; ti < allTriggers.length; ti++) {
        if (allTriggers[ti].getAttribute('data-feed-media-stream') === activeStreamUrl) {
          inlineTrigger = allTriggers[ti]
          break
        }
      }
      var inlineVid = inlineTrigger ? inlineTrigger.querySelector('video[data-feed-inline-video]') : null
      if (inlineVid) {
        overlay._sourceVideo = inlineVid
        inlineVid.pause()
        var startTime = inlineVid.currentTime
        if (startTime > 0) {
          v.addEventListener('loadedmetadata', function () {
            v.currentTime = startTime
          }, { once: true })
        }
      }
      host.appendChild(videoWrap)
    } else {
      const urls = Array.isArray(media.urls) ? media.urls : []
      const img = document.createElement('img')
      img.className = 'feed-overlay-image'
      img.alt = ''
      img.loading = 'eager'
      img.src = urls[currentIndex] || urls[0] || ''
      host.appendChild(img)
    }
    const total = media.kind === 'video' ? 1 : (media.kind === 'mixed' && media.slides ? media.slides.length : Math.max(1, (media.urls || []).length))
    if (counter) counter.textContent = total > 1 ? (String(currentIndex + 1) + ' / ' + String(total)) : ''
    if (prev) prev.style.display = total > 1 ? '' : 'none'
    if (next) next.style.display = total > 1 ? '' : 'none'
  }

  function step(dir) {
    const total = media.kind === 'mixed' && media.slides ? media.slides.length : Math.max(1, (media.urls || []).length)
    if (media.kind === 'video' || total <= 1) return
    currentIndex = (currentIndex + dir + total) % total
    render()
  }

  overlay.addEventListener('click', function (event) {
    if (event.target === overlay) closeMediaOverlay()
    const closeBtn = event.target && event.target.closest ? event.target.closest('.feed-media-overlay-close') : null
    if (closeBtn) {
      event.preventDefault()
      closeMediaOverlay()
      return
    }
    const prevBtn = event.target && event.target.closest ? event.target.closest('.feed-media-overlay-nav.prev') : null
    if (prevBtn) { event.preventDefault(); step(-1); return }
    const nextBtn = event.target && event.target.closest ? event.target.closest('.feed-media-overlay-nav.next') : null
    if (nextBtn) { event.preventDefault(); step(1); return }
  })
  if (prev) prev.addEventListener('click', function (event) { event.preventDefault(); event.stopPropagation(); step(-1) })
  if (next) next.addEventListener('click', function (event) { event.preventDefault(); event.stopPropagation(); step(1) })

  keyHandler = function (event) {
    if (event.key === 'Escape') closeMediaOverlay()
    else if (event.key === 'ArrowLeft') step(-1)
    else if (event.key === 'ArrowRight') step(1)
  }
  document.addEventListener('keydown', keyHandler)
  render()
}

// ── Global bridge ──
// Other modules access the overlay element for like/bookmark sync and keyboard guards.

export function getOverlayElement() {
  return overlayEl
}

window.FeedMediaOverlay = {
  get element() { return overlayEl },
  open: openMediaOverlay,
  close: closeMediaOverlay,
}
