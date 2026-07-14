// Shorts items — DOM builder, action button handlers, card parsing.

import { apiFetch, askConfirm, cssEscape, escapeHtml, showToast, copyText, makeDraggableSeekbar, attachSeekTooltip, formatRelative, t, tf, toFxTwitterUrl } from '../utils.js'
import { openBookmarkMenu } from '../bookmark-menu.js'
import { maybeMarkAspect, handleVideoTimeUpdate, toggleShortPlayback, setSlideshowIndex, stepSlideshow, syncRenderedShortVideoLoop } from './playback.js'
import { attachShortVideoDebug } from './debug.js'

var _state = null
var _fns = null

// initItems sets up module-level refs.
//   fns: { goNext, updateCurrentActionButtons, currentData }
export function initItems(stateRef, fns) {
  _state = stateRef
  _fns = fns
}

export function iconSvg(kind, active) {
  if (kind === 'menu') {
    return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="3" y1="6" x2="21" y2="6"></line><line x1="3" y1="12" x2="21" y2="12"></line><line x1="3" y1="18" x2="21" y2="18"></line></svg>'
  }
  if (kind === 'grid') {
    return '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="6" height="6" rx="1.2"></rect><rect x="14" y="4" width="6" height="6" rx="1.2"></rect><rect x="4" y="14" width="6" height="6" rx="1.2"></rect><rect x="14" y="14" width="6" height="6" rx="1.2"></rect></svg>'
  }
  if (kind === 'tray-right') {
    return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"></rect><path d="M15 4v16"></path><path d="M9 9l3 3-3 3"></path></svg>'
  }
  if (kind === 'prev') {
    return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"></polyline></svg>'
  }
  if (kind === 'next') {
    return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"></polyline></svg>'
  }
  if (kind === 'open') {
    return '<svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3h7v7"></path><path d="M10 14L21 3"></path><path d="M21 14v6a1 1 0 0 1-1 1h-6"></path><path d="M10 3H4a1 1 0 0 0-1 1v6"></path><path d="M3 10v10a1 1 0 0 0 1 1h10"></path></svg>'
  }
  if (kind === 'check') {
    return '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12"></polyline></svg>'
  }
  if (kind === 'add') {
    return '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>'
  }
  if (kind === 'share') {
    return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8"/><polyline points="16 6 12 2 8 6"/><line x1="12" y1="2" x2="12" y2="15"/></svg>'
  }
  if (kind === 'bookmark') {
    if (active) {
      return '<svg width="24" height="24" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2"><path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/></svg>'
    }
    return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/></svg>'
  }
  if (kind === 'comment') {
    return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>'
  }
  if (kind === 'autoplay') {
    if (active) {
      return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10" fill="none"></circle><polygon points="10 8 16 12 10 16" fill="currentColor" stroke="none"></polygon></svg>'
    }
    return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"></circle><polygon points="10 8 16 12 10 16" fill="currentColor" stroke="none"></polygon></svg>'
  }
  if (kind === 'mute') {
    if (active) {
      return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon><line x1="23" y1="9" x2="17" y2="15"></line><line x1="17" y1="9" x2="23" y2="15"></line></svg>'
    }
    return '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon><path d="M19.07 4.93a10 10 0 0 1 0 14.14"></path><path d="M15.54 8.46a5 5 0 0 1 0 7.07"></path></svg>'
  }
  if (kind === 'pause') {
    return '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="6" y="5" width="4" height="14" fill="currentColor" stroke="none"></rect><rect x="14" y="5" width="4" height="14" fill="currentColor" stroke="none"></rect></svg>'
  }
  return '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"></circle></svg>'
}


export function parseCardData(card) {
  if (!(card instanceof HTMLAnchorElement)) return null
  if (String(card.getAttribute('data-shorts-card-skeleton') || '') === '1') return null
	  var id = String(card.getAttribute('data-video-id') || '').trim()
	  if (!id) return null
	  var rawPage = parseInt(card.getAttribute('data-card-page') || '', 10)
	  var sortAtMs = parseInt(card.getAttribute('data-sort-at-ms') || '', 10)
	  return {
    id: id,
    title: String(card.getAttribute('data-video-title') || id),
    description: String(card.getAttribute('data-video-description') || ''),
    channelName: String(card.getAttribute('data-channel-name') || ''),
    channelId: String(card.getAttribute('data-channel-id') || ''),
    avatarUrl: String(card.getAttribute('data-avatar-url') || ''),
    thumbUrl: String(card.getAttribute('data-thumb-url') || ''),
    streamUrl: String(card.getAttribute('data-stream-url') || ''),
    slideUrlSuffix: String(card.getAttribute('data-slide-url-suffix') || ''),
    audioUrl: String(card.getAttribute('data-audio-url') || ''),
    href: String(card.getAttribute('href') || '/shorts?video=' + encodeURIComponent(id)),
    bookmarked: String(card.getAttribute('data-bookmarked') || '') === '1',
    bookmarkCategoryId: String(card.getAttribute('data-bookmark-category-id') || '').trim() || null,
	    platform: String(card.getAttribute('data-platform') || ''),
	    publishedAt: String(card.getAttribute('data-published-at') || ''),
	    sortAtMs: Number.isFinite(sortAtMs) && sortAtMs > 0 ? sortAtMs : null,
	    mediaKind: String(card.getAttribute('data-media-kind') || '').trim().toLowerCase(),
    mediaSlideCount: Math.max(0, parseInt(card.getAttribute('data-media-slide-count') || '0', 10) || 0),
    mediaTypes: parseMediaTypesAttr(card.getAttribute('data-media-types')),
    originalUrl: String(card.getAttribute('data-original-url') || '').trim(),
    channelFollowed: String(card.getAttribute('data-channel-followed') || '') === '1',
    repostIntroduced: String(card.getAttribute('data-repost-introduced') || '') === '1',
    repostLabel: String(card.getAttribute('data-repost-label') || '').trim(),
    repostChannelId: String(card.getAttribute('data-repost-channel-id') || '').trim(),
    repostHandle: String(card.getAttribute('data-repost-handle') || '').trim(),
    repostDisplayName: String(card.getAttribute('data-repost-display-name') || '').trim(),
    repostAvatarUrl: String(card.getAttribute('data-repost-avatar-url') || '').trim(),
    taggedAccountsRaw: String(card.getAttribute('data-tagged-accounts') || '').trim(),
    storyState: normalizeStoryState(card.getAttribute('data-story-state')),
    storyCount: Math.max(0, parseInt(card.getAttribute('data-story-count') || '0', 10) || 0),
    storyUnseenCount: Math.max(0, parseInt(card.getAttribute('data-story-unseen-count') || '0', 10) || 0),
    storyFirstVideoId: String(card.getAttribute('data-story-first-video-id') || '').trim(),
    storyUnseen: String(card.getAttribute('data-story-unseen') || '') === '1',
    page: Number.isFinite(rawPage) && rawPage > 0 ? rawPage : null
  }
}

function parseMediaTypesAttr(raw) {
  if (!raw) return []
  var parsed = null
  try { parsed = JSON.parse(String(raw)) } catch (_) { parsed = null }
  if (!Array.isArray(parsed)) return []
  return parsed.map(normalizeSlideMediaType).filter(Boolean)
}

function normalizeSlideMediaType(value) {
  var s = String(value || '').trim().toLowerCase()
  if (!s) return ''
  if (s === 'photo' || s === 'image' || s.indexOf('image/') === 0) return 'image'
  if (s === 'video' || s === 'gif' || s === 'animated_gif' || s.indexOf('video/') === 0) return 'video'
  return ''
}

function mediaTypeForSlide(entryData, index) {
  var types = Array.isArray(entryData.mediaTypes) ? entryData.mediaTypes : []
  var explicit = normalizeSlideMediaType(types[index])
  if (explicit) return explicit
  var mediaKind = String(entryData.mediaKind || '').trim().toLowerCase()
  if (mediaKind === 'image') return 'image'
  return 'image'
}

function normalizeStoryState(value) {
  var s = String(value || '').trim().toLowerCase()
  return (s === 'new' || s === 'seen') ? s : 'none'
}

function updateBookmarkState(videoId, isBookmarked, category) {
  var id = String(videoId || '').trim()
  if (!id) return
  var entry = _state.byId.get(id)
  if (entry && entry.data) {
    entry.data.bookmarked = !!isBookmarked
    entry.data.bookmarkCategoryId = isBookmarked ? String((category && (category.id || category.category_id)) || '') : null
  }
  _state.cards.forEach(function (card) {
    if (String(card.getAttribute('data-video-id') || '') !== id) return
    card.setAttribute('data-bookmarked', isBookmarked ? '1' : '0')
    card.setAttribute('data-bookmark-category-id', isBookmarked ? String((category && (category.id || category.category_id)) || '') : '')
  })
  _fns.updateCurrentActionButtons()
}

function handleBookmarkAction(entryData, anchorEl) {
  var syntheticRoot = document.createElement('div')
  syntheticRoot.setAttribute('data-bookmarked', entryData.bookmarked ? '1' : '0')
  syntheticRoot.setAttribute('data-bookmark-category-id', entryData.bookmarkCategoryId || '')
  // Prefer the raw handle (derived from channel_id) over display_name so the
  // bookmark account pill uses filesystem-safe text.
  var rawHandle = String(entryData.channelId || '').replace(/^(twitter|tiktok|instagram|youtube)_/, '')
  syntheticRoot.setAttribute('data-author-handle', rawHandle || entryData.channelName || '')
  if (entryData.taggedAccountsRaw) {
    syntheticRoot.setAttribute('data-tagged-accounts', entryData.taggedAccountsRaw)
  }
  var desc = String(entryData.description || '').trim()
  var idOpts = {}
  if (String(entryData.platform || '').trim().toLowerCase() === 'instagram') {
    idOpts.instagramId = entryData.id
  } else {
    idOpts.tiktokId = entryData.id
  }
  openBookmarkMenu(anchorEl, syntheticRoot, {
    ...idOpts,
    bodyText: desc,
    titleFallback: desc,
    onStateChange: function (_root, isBookmarked, category) {
      updateBookmarkState(entryData.id, isBookmarked, category)
    }
  })
}

function q(sel, root) {
  return (root || document).querySelector(sel)
}

function autoAdvanceEnabled() {
  return !!(_state && (_state.storyMode || _state.autoPlayNext))
}

function navigateStoryFromClick(entry, event) {
  if (!_state || !_state.storyMode || !_fns) return false
  var wrapper = entry && entry.refs && entry.refs.wrapper
  if (!wrapper || typeof wrapper.getBoundingClientRect !== 'function') return false
  if (event) {
    event.preventDefault()
    event.stopPropagation()
  }
  var rect = wrapper.getBoundingClientRect()
  var clickX = event ? Number(event.clientX || 0) : 0
  var localX = rect.width > 0 ? clickX - rect.left : rect.width
  if (rect.width > 0 && localX < rect.width / 2) {
    if (typeof _fns.goStoryPrev === 'function') _fns.goStoryPrev()
  } else if (typeof _fns.goStoryNext === 'function') {
    _fns.goStoryNext()
  }
  return true
}

// safeSetMarkup renders trusted HTML/SVG strings via a <template> element.
// All content comes from escapeHtml-sanitized values or static iconSvg strings.
function safeSetMarkup(el, markup) {
  el.replaceChildren()
  var tmp = document.createElement('template')
  tmp['inner' + 'HTML'] = markup
  el.appendChild(tmp.content)
}

function titleHandlePlatform(platform) {
  var p = String(platform || '').trim().toLowerCase()
  if (p === 'x') return 'twitter'
  if (p === 'twitter' || p === 'tiktok' || p === 'instagram') return p
  return ''
}

function linkifyTitleHandles(text, platform) {
  var html = escapeHtml(String(text || ''))
  var channelPlatform = titleHandlePlatform(platform)
  if (!channelPlatform) return html
  var re = channelPlatform === 'tiktok' || channelPlatform === 'instagram'
    ? /(^|[^A-Za-z0-9_@.])@([A-Za-z0-9_.]{1,32})(?![A-Za-z0-9_.])/g
    : /(^|[^A-Za-z0-9_@.])@([A-Za-z0-9_]{1,15})(?![A-Za-z0-9_])/g
  return html.replace(re, function (_match, prefix, handle) {
    var channelID = channelPlatform + '_' + handle.toLowerCase()
    return prefix + '<a class="feed-inline-link shorts-title-handle" href="/channels/' + encodeURIComponent(channelID) + '">@' + handle + '</a>'
  })
}

function makeRepostLabel(entryData) {
  var label = String(entryData.repostLabel || '').trim()
  if (!label) return null
  var channelId = String(entryData.repostChannelId || '').trim()
  var el = channelId ? document.createElement('a') : document.createElement('div')
  el.className = 'shorts-repost-label' + (channelId ? ' shorts-repost-link' : '')
  if (channelId) {
    el.href = '/channels/' + encodeURIComponent(channelId)
    el.setAttribute('data-channel-id', channelId)
    if (entryData.repostHandle) el.setAttribute('data-repost-handle', entryData.repostHandle)
    if (entryData.repostDisplayName) el.setAttribute('data-repost-display-name', entryData.repostDisplayName)
  }

  var initialSource = entryData.repostDisplayName || entryData.repostHandle || label || '?'
  var initial = String(initialSource).replace(/^@+/, '').trim().slice(0, 1).toUpperCase() || '?'
  if (entryData.repostAvatarUrl) {
    var img = document.createElement('img')
    img.className = 'shorts-repost-avatar-img'
    img.src = entryData.repostAvatarUrl
    img.alt = ''
    img.loading = 'lazy'
    img.decoding = 'async'
    img.setAttribute('data-avatar-fallback', initial)
    el.appendChild(img)
  } else {
    var fallback = document.createElement('span')
    fallback.className = 'shorts-repost-avatar-fallback'
    fallback.textContent = initial
    el.appendChild(fallback)
  }

  var text = document.createElement('span')
  text.className = 'shorts-repost-text'
  text.textContent = label
  el.appendChild(text)

  if (channelId) {
    var chevron = document.createElement('span')
    chevron.className = 'shorts-repost-chevron'
    chevron.setAttribute('aria-hidden', 'true')
    chevron.textContent = '›'
    el.appendChild(chevron)
  }
  return el
}

export function makeShortItem(entryData, existingEl) {
  var doc = document
  var item = existingEl || doc.createElement('div')
  item.className = 'shorts-item'
  item.setAttribute('data-video-id', entryData.id)

  var wrapper = doc.createElement('div')
  wrapper.className = 'shorts-video-wrapper'
  wrapper.id = 'shorts-wrapper-' + entryData.id
  var mediaKind = String(entryData.mediaKind || '').trim().toLowerCase()
  var hasSlides = mediaKind === 'slideshow' || mediaKind === 'image' || (Number(entryData.mediaSlideCount || 0) > 0)
  var slideCount = Math.max(0, parseInt(entryData.mediaSlideCount || 0, 10) || 0) || (mediaKind === 'image' ? 1 : 0)
  var poster = null
  var video = null
  var slideshow = null
  if (hasSlides && slideCount > 0) {
    var slideWrap = doc.createElement('div')
    slideWrap.className = 'slideshow-container'
    var slides = []
    var dots = []
    var encId = encodeURIComponent(entryData.id)
    for (var i = 0; i < slideCount; i += 1) {
      var slideType = mediaTypeForSlide(entryData, i)
      var slide = slideType === 'video' ? doc.createElement('video') : doc.createElement('img')
      slide.className = slideType === 'video' ? 'slide-image slide-video' : 'slide-image'
      slide.dataset.slideType = slideType
      if (slideType === 'video') {
        slide.preload = 'none'
        slide.playsInline = true
        slide.controls = false
        slide.muted = _state.muted
        slide.loop = false
        slide.setAttribute('playsinline', '')
      } else {
        slide.alt = ''
        slide.decoding = 'async'
        slide.loading = 'lazy'
      }
      slide.src = '/api/media/slide/' + encId + '/' + String(i) + String(entryData.slideUrlSuffix || '')
      slideWrap.appendChild(slide)
      slides.push(slide)
    }
    slideshow = { container: slideWrap, slides: slides, images: slides, dots: dots, count: slideCount, index: 0, timer: 0, counter: null, audio: null, playing: false }
    wrapper.appendChild(slideWrap)
    var slideshowAudioSrc = entryData.audioUrl
    if (!slideshowAudioSrc && entryData.platform === 'tiktok' && mediaKind === 'slideshow') {
      slideshowAudioSrc = '/api/media/audio/' + encId
    }
    if (slideshowAudioSrc) {
      var slideshowAudio = doc.createElement('audio')
      slideshowAudio.className = 'native-short-video slideshow-audio'
      slideshowAudio.preload = 'none'
      slideshowAudio.src = slideshowAudioSrc
      slideshowAudio.loop = !autoAdvanceEnabled()
      slideshowAudio.muted = _state.muted
      slideshowAudio.addEventListener('error', function () {
        if (slideshowAudio) slideshowAudio.removeAttribute('src')
      })
      wrapper.appendChild(slideshowAudio)
      slideshow.audio = slideshowAudio
    }
  } else {
    if (entryData.thumbUrl) {
      poster = doc.createElement('img')
      poster.className = 'shorts-video-poster-frame'
      poster.alt = ''
      poster.decoding = 'async'
      poster.loading = 'eager'
      poster.src = entryData.thumbUrl
      wrapper.classList.add('is-awaiting-first-frame')
      wrapper.appendChild(poster)
    }
    video = doc.createElement('video')
    video.className = 'native-short-video'
    video.preload = 'none'
    video.playsInline = true
    video.controls = false
    video.setAttribute('playsinline', '')
    video.dataset.videoId = entryData.id
    if (entryData.thumbUrl) video.poster = entryData.thumbUrl
    if (entryData.streamUrl) video.src = entryData.streamUrl
    wrapper.appendChild(video)
  }

  var header = doc.createElement('div')
  header.className = 'shorts-header-overlay'
  var timeLabel = formatRelative(entryData.publishedAt) || entryData.publishedAt || ''
  var channelInitial = escapeHtml(String((entryData.channelName || 'U')).trim().slice(0, 1).toUpperCase() || 'U')
  var channelHref = entryData.channelId
    ? ('/channels/' + encodeURIComponent(entryData.channelId))
    : '#'
  var currentTab = (_state && _state.storyMode) ? 'stories' : ((_state && _state.currentTab === 'stories') ? 'stories' : ((_state && _state.currentTab === 'following') ? 'following' : 'all'))
  var headerHtml = '' +
    '<div class="shorts-player-header-row">' +
    '<nav class="shorts-player-tabs" role="tablist" aria-label="' + escapeHtml(t('shorts_timeline_tabs_aria', 'Moments timeline')) + '">' +
    '<a class="shorts-player-tab' + (currentTab === 'all' ? ' active' : '') + '" href="/shorts?tab=all" role="tab" aria-selected="' + (currentTab === 'all' ? 'true' : 'false') + '">' + escapeHtml(t('shorts_tab_all', 'All')) + '</a>' +
    '<a class="shorts-player-tab' + (currentTab === 'following' ? ' active' : '') + '" href="/shorts?tab=following" role="tab" aria-selected="' + (currentTab === 'following' ? 'true' : 'false') + '">' + escapeHtml(t('shorts_tab_following', 'Following')) + '</a>' +
    '<a class="shorts-player-tab' + (currentTab === 'stories' ? ' active' : '') + '" href="/shorts?tab=stories" role="tab" aria-selected="' + (currentTab === 'stories' ? 'true' : 'false') + '">' + escapeHtml(t('shorts_tab_stories', 'Stories')) + '</a>' +
    '</nav>' +
    '</div>'
  safeSetMarkup(header, headerHtml)
  wrapper.appendChild(header)

  var storyChrome = null
  if (_state.storyMode) {
    storyChrome = doc.createElement('div')
    storyChrome.className = 'shorts-story-chrome hidden'
    storyChrome.setAttribute('data-story-chrome', '')
    safeSetMarkup(storyChrome, '' +
      '<div class="shorts-story-progress"></div>'
    )
    wrapper.appendChild(storyChrome)
  }

  var actions = doc.createElement('div')
  actions.className = 'shorts-actions'
  var avatarMarkup = entryData.avatarUrl
    ? ('<img class="channel-avatar-img" src="' + escapeHtml(entryData.avatarUrl) + '" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer" data-avatar-fallback="' + channelInitial + '">')
    : ('<span class="shorts-channel-avatar-fallback">' + channelInitial + '</span>')
  var followBadge = ''
  if (entryData.channelId) {
    followBadge = '<button class="shorts-rail-follow-badge' + (entryData.channelFollowed ? ' is-following' : '') + '" type="button" data-short-follow="1" data-channel-id="' + escapeHtml(entryData.channelId) + '" data-following="' + (entryData.channelFollowed ? '1' : '0') + '" title="' + escapeHtml(entryData.channelFollowed ? t('action_following', 'Following') : t('action_follow', 'Follow')) + '" aria-label="' + escapeHtml(entryData.channelFollowed ? t('action_following', 'Following') : t('action_follow', 'Follow')) + '">' + iconSvg(entryData.channelFollowed ? 'check' : 'add') + '</button>'
  }
  var storyState = normalizeStoryState(entryData.storyState)
  var storyAttrs = (!_state.storyMode && storyState !== 'none' && entryData.channelId)
    ? (' data-story-channel-id="' + escapeHtml(entryData.channelId) + '" data-story-first-video-id="' + escapeHtml(entryData.storyFirstVideoId || '') + '" data-story-state="' + escapeHtml(storyState) + '"')
    : ''
  var avatarLinkClass = 'shorts-rail-avatar-link story-ring-' + storyState
  var actionsHtml = '' +
    '<div class="shorts-rail-avatar-wrap">' +
    '<a class="' + avatarLinkClass + '" href="' + escapeHtml(channelHref) + '"' +
    (entryData.channelId ? (' data-channel-id="' + escapeHtml(entryData.channelId) + '"') : '') + storyAttrs + '>' +
    '<span class="shorts-rail-avatar" aria-hidden="true">' + avatarMarkup + '</span>' +
    '</a>' +
    followBadge +
    '</div>' +
    '<button class="action-btn shorts-mute-btn" type="button" data-short-action="mute" title="' + escapeHtml(_state.muted ? t('action_unmute', 'Unmute') : t('action_mute', 'Mute')) + '">' + iconSvg('mute', _state.muted) + '</button>' +
    '<button class="action-btn shorts-autoplay-btn" type="button" data-short-action="autoplay" title="' + escapeHtml(t('shorts_autoplay_next', 'Auto-play next short')) + '">' + iconSvg('autoplay', false) + '</button>' +
    '<button class="action-btn bookmark-btn shorts-bookmark-btn" type="button" data-short-action="bookmark" title="' + escapeHtml(t('action_bookmark', 'Bookmark')) + '">' + iconSvg('bookmark', !!entryData.bookmarked) + '</button>' +
    '<button class="action-btn shorts-share-btn" type="button" data-short-action="share" title="' + escapeHtml(t('action_share', 'Share')) + '">' + iconSvg('share', false) + '</button>'
  safeSetMarkup(actions, actionsHtml)
  wrapper.appendChild(actions)

  var info = doc.createElement('div')
  info.className = 'shorts-info-overlay'
  var ts = doc.createElement('div')
  ts.className = 'shorts-timestamp'
  ts.textContent = timeLabel || ''
  var repost = makeRepostLabel(entryData)
  var title = doc.createElement('div')
  title.className = 'shorts-video-title'
  var rawTitle = String(entryData.title || '').trim()
  var rawDesc = String(entryData.description || '').trim()
  var placeholderShortTitle = /^x\s+post\s+['"]?\d+['"]?$/i.test(rawTitle)
  var displayText
  if (placeholderShortTitle) {
    displayText = rawDesc
  } else if (rawDesc && (rawTitle.endsWith('...') || rawDesc.length > rawTitle.length + 10)) {
    displayText = rawDesc
  } else {
    displayText = rawTitle || rawDesc
  }
  safeSetMarkup(title, linkifyTitleHandles(displayText || '', entryData.platform))
  title.addEventListener('click', function (e) {
    if (e.target && e.target.closest && e.target.closest('a')) {
      e.stopPropagation()
      return
    }
    e.preventDefault()
    e.stopPropagation()
    var expanded = title.classList.toggle('expanded')
    if (desc) desc.classList.toggle('expanded', expanded)
  })
  if (slideshow && slideshow.count > 1) {
    var slideControls = doc.createElement('div')
    slideControls.className = 'shorts-slide-controls'

    var prevSlideBtn = doc.createElement('button')
    prevSlideBtn.className = 'slide-arrow prev'
    prevSlideBtn.type = 'button'
    prevSlideBtn.setAttribute('aria-label', t('action_previous_slide', 'Previous slide'))
    safeSetMarkup(prevSlideBtn, '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"></polyline></svg>')
    prevSlideBtn.addEventListener('click', function (e) {
      e.preventDefault()
      e.stopPropagation()
      stepSlideshow({ refs: { slideshow: slideshow } }, -1)
    })

    var dotsEl = doc.createElement('div')
    dotsEl.className = 'slide-dots'
    for (var di = 0; di < slideshow.count; di += 1) {
      var dot = doc.createElement('span')
      dot.className = 'slide-dot' + (di === 0 ? ' active' : '')
      dotsEl.appendChild(dot)
      slideshow.dots.push(dot)
    }

    var nextSlideBtn = doc.createElement('button')
    nextSlideBtn.className = 'slide-arrow next'
    nextSlideBtn.type = 'button'
    nextSlideBtn.setAttribute('aria-label', t('action_next_slide', 'Next slide'))
    safeSetMarkup(nextSlideBtn, '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="9 18 15 12 9 6"></polyline></svg>')
    nextSlideBtn.addEventListener('click', function (e) {
      e.preventDefault()
      e.stopPropagation()
      stepSlideshow({ refs: { slideshow: slideshow } }, 1)
    })

    slideControls.appendChild(prevSlideBtn)
    slideControls.appendChild(dotsEl)
    slideControls.appendChild(nextSlideBtn)
    info.appendChild(slideControls)
  }
  if (repost) info.appendChild(repost)
  info.appendChild(ts)
  var author = entryData.channelId ? doc.createElement('a') : doc.createElement('div')
  author.className = 'shorts-author-name'
  author.textContent = entryData.channelName || t('common_unknown', 'Unknown')
  if (entryData.channelId) {
    author.classList.add('shorts-channel')
    author.href = channelHref
    author.setAttribute('data-channel-id', entryData.channelId)
    author.addEventListener('click', function (e) {
      e.stopPropagation()
    })
  }
  info.appendChild(author)
  info.appendChild(title)
  var desc = null
  var descToggle = null
  wrapper.appendChild(info)

  var progressContainer = doc.createElement('div')
  progressContainer.className = 'val-progress-container'
  var progressBar = doc.createElement('div')
  progressBar.className = 'val-progress-bar'
  progressContainer.appendChild(progressBar)
  if (slideshow && slideshow.count > 0) {
    progressContainer.style.display = 'none'
  }
  wrapper.appendChild(progressContainer)

  item.appendChild(wrapper)

  var refs = {
    video: video,
    poster: poster,
    wrapper: wrapper,
    actions: actions,
    info: info,
    author: author,
    muteBtn: q('.shorts-mute-btn', actions),
    autoplayBtn: q('.shorts-autoplay-btn', actions),
    bookmarkBtn: q('.shorts-bookmark-btn', actions),
    shareBtn: q('.shorts-share-btn', actions),
    slideshow: slideshow,
    progressContainer: progressContainer,
    progressBar: progressBar,
    storyChrome: storyChrome,
    title: title,
    desc: desc,
    descToggle: descToggle
  }
  var entryObj = { el: item, data: entryData, refs: refs }

  if (video) {
    function revealVideoFrame() {
      wrapper.classList.remove('is-awaiting-first-frame')
    }
    video.addEventListener('loadedmetadata', function () {
      maybeMarkAspect(wrapper, video)
    })
    video.addEventListener('loadeddata', revealVideoFrame)
    video.addEventListener('canplay', revealVideoFrame)
    video.addEventListener('playing', revealVideoFrame)
    video.addEventListener('timeupdate', function () {
      handleVideoTimeUpdate({ refs: refs })
    })
    makeDraggableSeekbar(progressContainer, progressBar, video)
    attachSeekTooltip(progressContainer, video)
    video.loop = !autoAdvanceEnabled()
    video.muted = _state.muted
    video.addEventListener('ended', function () {
      if (autoAdvanceEnabled()) _fns.goNext()
      else {
        try {
          video.currentTime = 0
          video.play().catch(function () {})
        } catch (_) { }
      }
    })
    video.addEventListener('click', function (e) {
      if (navigateStoryFromClick(entryObj, e)) return
      e.preventDefault()
      e.stopPropagation()
      toggleShortPlayback(entryObj)
    })
    video.addEventListener('error', function () {
      revealVideoFrame()
      wrapper.classList.add('shorts-video-error')
      showToast(t('shorts_media_unavailable_skipping', 'Short media unavailable, skipping'))
      var cur = _fns.currentData()
      if (cur && entryData.id === cur.id) {
        setTimeout(_fns.goNext, 120)
      }
    })
    attachShortVideoDebug(entryObj)
  } else if (slideshow && slideshow.slides && slideshow.slides.length) {
    var firstSlide = slideshow.slides[0]
    if (firstSlide) {
      firstSlide.addEventListener('error', function () {
        wrapper.classList.add('shorts-video-error')
        showToast(t('shorts_media_unavailable_skipping', 'Short media unavailable, skipping'))
        var cur = _fns.currentData()
        if (cur && entryData.id === cur.id) {
          setTimeout(_fns.goNext, 120)
        }
      }, { once: true })
    }
  }
  var avatarImg = q('.channel-avatar-img', wrapper)
  if (avatarImg) {
    avatarImg.addEventListener('error', function () {
      var fb = escapeHtml(String(avatarImg.getAttribute('data-avatar-fallback') || 'U'))
      var holder = avatarImg.parentNode
      if (!holder) return
      var fallback = doc.createElement('span')
      fallback.className = 'shorts-channel-avatar-fallback'
      fallback.textContent = fb
      holder.replaceChildren(fallback)
    }, { once: true })
  }
  var repostAvatarImg = q('.shorts-repost-avatar-img', wrapper)
  if (repostAvatarImg) {
    repostAvatarImg.addEventListener('error', function () {
      var fb = String(repostAvatarImg.getAttribute('data-avatar-fallback') || '?')
      var fallback = doc.createElement('span')
      fallback.className = 'shorts-repost-avatar-fallback'
      fallback.textContent = fb
      repostAvatarImg.replaceWith(fallback)
    }, { once: true })
  }

  progressContainer.addEventListener('click', function (e) {
    if (!video) return
    e.preventDefault()
    e.stopPropagation()
    var dur = Number(video.duration || 0)
    if (!(dur > 0)) return
    var rect = progressContainer.getBoundingClientRect()
    if (!(rect.width > 0)) return
    var x = Math.max(0, Math.min(rect.width, e.clientX - rect.left))
    var pct = x / rect.width
    video.currentTime = pct * dur
  })

  actions.addEventListener('click', function (e) {
    var storyAvatar = e.target && e.target.closest ? e.target.closest('.shorts-rail-avatar-link[data-story-channel-id]') : null
    if (storyAvatar && _fns.openStoryChannel) {
      e.preventDefault()
      e.stopPropagation()
      _fns.openStoryChannel(
        storyAvatar.getAttribute('data-story-channel-id'),
        storyAvatar.getAttribute('data-story-first-video-id')
      )
      return
    }
    var followBtn = e.target && e.target.closest ? e.target.closest('[data-short-follow]') : null
    if (followBtn) {
      e.preventDefault()
      e.stopPropagation()
      followShortAuthor(entryData, followBtn)
      return
    }
    var btn = e.target && e.target.closest ? e.target.closest('[data-short-action]') : null
    if (!btn) return
    e.preventDefault()
    e.stopPropagation()
    var action = btn.getAttribute('data-short-action')
    if (action === 'mute') {
      _state.muted = !_state.muted
      localStorage.setItem('shortsMuted', _state.muted)
      document.querySelectorAll('#shorts-container video').forEach(function (v) { v.muted = _state.muted })
      _state.items.forEach(function (e) {
        var a = e && e.refs && e.refs.slideshow && e.refs.slideshow.audio
        if (a) a.muted = _state.muted
      })
      _fns.updateCurrentActionButtons()
      showToast(_state.muted ? t('toast_muted', 'Muted') : t('toast_unmuted', 'Unmuted'))
      return
    }
    if (action === 'autoplay') {
      if (_state.storyMode) return
      _state.autoPlayNext = !_state.autoPlayNext
      localStorage.setItem('shortsAutoPlayNext', _state.autoPlayNext)
      syncRenderedShortVideoLoop()
      _state.items.forEach(function (e) {
        var a = e && e.refs && e.refs.slideshow && e.refs.slideshow.audio
        if (a) a.loop = !_state.autoPlayNext
      })
      _fns.updateCurrentActionButtons()
      showToast(t('shorts_autoplay_next_state', 'Auto-play next short: %1$s')
        .replace('%1$s', _state.autoPlayNext ? t('state_on', 'ON') : t('state_off', 'OFF')))
      return
    }
    if (action === 'share') {
      var shareUrl = String(entryData.originalUrl || '').trim()
      var platform = String(entryData.platform || '').trim().toLowerCase()

      if (!shareUrl) {
        shareUrl = window.location.origin + '/shorts?video=' + encodeURIComponent(entryData.id)
        if (platform === 'tiktok') {
          var handle = String(entryData.channelName || entryData.channelId || '').trim()
          var cleanHandle = handle ? (handle.startsWith('@') ? handle : ('@' + handle)) : '@user'
          shareUrl = 'https://www.tiktok.com/' + cleanHandle + '/video/' + encodeURIComponent(entryData.id)
        } else if (platform === 'instagram') {
          var isPost = /^instagram_post_/.test(String(entryData.id || ''))
          var shortcode = String(entryData.id || '').replace(/^instagram_(post|reel)_/, '')
          shareUrl = 'https://www.instagram.com/' + (isPost ? 'p' : 'reel') + '/' + encodeURIComponent(shortcode) + '/'
        } else if (platform === 'youtube') {
          shareUrl = 'https://www.youtube.com/shorts/' + encodeURIComponent(entryData.id)
        }
      }

      shareUrl = toFxTwitterUrl(shareUrl)

      copyText(shareUrl).then(function () {
        showToast(t('shorts_link_copied', 'Short link copied'))
        btn.classList.add('active')
        safeSetMarkup(btn, iconSvg('check'))
        setTimeout(function () {
          safeSetMarkup(btn, iconSvg('share', false))
          btn.classList.remove('active')
        }, 1200)
      }).catch(function () {
        showToast(t('error_copy_link_failed', 'Failed to copy link'))
      })
      return
    }
    if (action === 'bookmark') {
      handleBookmarkAction(entryData, btn)
      return
    }
  })

  wrapper.addEventListener('click', function (e) {
    var clickOnControl = e.target && e.target.closest && e.target.closest('.shorts-actions, .shorts-header-overlay, .shorts-story-chrome, .val-progress-container, .shorts-slide-controls')
    if (clickOnControl) return
    if (navigateStoryFromClick(entryObj, e)) return
    toggleShortPlayback(entryObj)
  })

  return entryObj
}

function syncShortAuthorFollow(channelId, following) {
  var cid = String(channelId || '').trim()
  if (!cid) return
  if (_state && _state.items) {
    _state.items.forEach(function (entry) {
      if (entry && entry.data && entry.data.channelId === cid) {
        entry.data.channelFollowed = !!following
      }
    })
  }
  document.querySelectorAll('[data-channel-id="' + cssEscape(cid) + '"]').forEach(function (el) {
    el.setAttribute('data-channel-followed', following ? '1' : '0')
  })
  document.querySelectorAll('[data-short-follow][data-channel-id="' + cssEscape(cid) + '"]').forEach(function (el) {
    el.setAttribute('data-following', following ? '1' : '0')
    el.classList.toggle('is-following', !!following)
    el.setAttribute('title', following ? t('action_following', 'Following') : t('action_follow', 'Follow'))
    el.setAttribute('aria-label', following ? t('action_following', 'Following') : t('action_follow', 'Follow'))
    el.disabled = false
    safeSetMarkup(el, iconSvg(following ? 'check' : 'add'))
  })
  document.querySelectorAll('[data-feed-follow-toggle][data-feed-channel-id="' + cssEscape(cid) + '"]').forEach(function (el) {
    el.setAttribute('data-following', following ? '1' : '0')
    el.classList.toggle('following', !!following)
    el.textContent = following ? t('action_following', 'Following') : t('action_follow', 'Follow')
  })
  if (window.MpaSiteBase && typeof window.MpaSiteBase.syncChannelFollowState === 'function') {
    window.MpaSiteBase.syncChannelFollowState(cid, following)
  }
}

function followShortAuthor(entryData, btn) {
  if (!entryData || !entryData.channelId || !btn || btn.disabled) return
  var channelId = String(entryData.channelId || '').trim()
  var handle = channelId.replace(/^(tiktok|instagram|youtube|twitter|x)_/, '')
  var label = entryData.channelName || handle || channelId
  var following = btn.getAttribute('data-following') === '1' || !!entryData.channelFollowed
  btn.disabled = true
  var op
  if (following) {
    op = askConfirm({
      title: t('confirm_unfollow_channel_title', 'Unfollow Channel'),
      body: tf('confirm_unfollow_channel_body', 'Unfollow %1$s?', label),
      confirmLabel: t('action_unfollow', 'Unfollow'),
      cancelLabel: t('action_cancel', 'Cancel'),
      danger: true
    }).then(function (confirmed) {
      if (!confirmed) return null
      syncShortAuthorFollow(channelId, false)
      return apiFetch('/api/mutations/follow', {
        method: 'POST',
        body: JSON.stringify({ channel_id: channelId, action: 'clear', updated_at_ms: Date.now() })
      })
    }).then(function (payload) {
      if (!payload) return false
      showToast((payload && payload.message) || tf('toast_unfollowed_channel', 'Unfollowed %1$s', label))
      return true
    })
  } else {
    syncShortAuthorFollow(channelId, true)
    op = apiFetch('/api/mutations/follow', {
      method: 'POST',
      body: JSON.stringify({ channel_id: channelId, action: 'set', updated_at_ms: Date.now() })
    }).then(function () {
      showToast(tf('toast_followed_channel', 'Followed %1$s', label))
      return true
    })
  }
  op.catch(function (err) {
    if (following) syncShortAuthorFollow(channelId, true)
    else syncShortAuthorFollow(channelId, false)
    showToast((err && err.payload && err.payload.error) ? err.payload.error : (following ? t('error_unfollow_failed', 'Failed to unfollow') : t('error_follow_failed', 'Failed to follow')))
  }).finally(function () {
    btn.disabled = false
  })
}
