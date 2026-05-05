// Translation module — handles translate toggle and auto-translate observer.

import { apiFetch, jsLinkify } from '../utils.js'

var KNOWN_LANGS = {
  zh: true, ja: true, ko: true, ar: true,
  ru: true, tr: true, fr: true, de: true,
  es: true, pt: true, hi: true, en: true,
  id: true, vi: true, th: true, tl: true,
  ms: true, my: true
}
var reTranslateToken = /https?:\/\/\S+|@\S+|#\S+/g
var translateSkipScriptPatterns = {
  ja: /[\u3040-\u30FF\uFF66-\uFF9F]/
}
var translateTarget = (document.querySelector('meta[name="translate-target"]') || {}).content || 'en'
var translateSkipLangs = ((document.querySelector('meta[name="translate-skip-langs"]') || {}).content || '').split(',').filter(Boolean)
var translateBackend = ((document.querySelector('meta[name="translate-backend"]') || {}).content || 'none').trim()
var translateAutoMode = ((document.querySelector('meta[name="translate-auto-mode"]') || {}).content || 'lazy').trim()
var translateLookahead = parseInt(((document.querySelector('meta[name="translate-lookahead"]') || {}).content || '20'), 10)
if (!translateLookahead || translateLookahead < 1) translateLookahead = 20
if (translateLookahead > 100) translateLookahead = 100
var translateSkipSet = {}
translateSkipLangs.forEach(function (l) { translateSkipSet[l.trim().toLowerCase()] = true })
var translatePending = {}
var translateAutoQueued = {}
var translateAutoQueue = []
var translateAutoActive = 0
var translateAutoConcurrency = 2
var translateAutoRetryAttempts = {}
var translateAutoRetryTimers = {}
var translateAutoRetryDelays = [5000, 15000, 30000, 60000]

// setHtmlContent safely sets HTML content using a template element.
// All callers use jsLinkify (which HTML-escapes input) or restore our own saved DOM.
function setHtmlContent(el, html) {
  el.replaceChildren()
  var tmp = document.createElement('template')
  tmp.innerHTML = html // nosec: input is HTML-escaped by jsLinkify or from our own DOM
  el.appendChild(tmp.content)
}

function translateTextElement(container) {
  return container ? container.querySelector('.feed-body-text, .feed-quote-text') : null
}

function hasTranslatableText(container) {
  var textEl = translateTextElement(container)
  if (!textEl) return false
  return String(textEl.textContent || '').replace(reTranslateToken, '').trim() !== ''
}

function hasSkippedLanguageScript(container) {
  var textEl = translateTextElement(container)
  if (!textEl) return false
  var text = String(textEl.textContent || '')
  for (var lang in translateSkipSet) {
    var pattern = translateSkipScriptPatterns[lang]
    if (pattern && pattern.test(text)) return true
  }
  return false
}

function translateBlock(card, container) {
  var tweetId = card.getAttribute('data-tweet-id')
  var field = container.getAttribute('data-translate-field')
  var targetLang = container.getAttribute('data-target-lang') || translateTarget
  var key = tweetId + ':' + field
  if (translatePending[key]) return Promise.resolve('pending')
  translatePending[key] = true

  var textEl = translateTextElement(container)
  if (!textEl) { delete translatePending[key]; return Promise.resolve('skipped') }
  if (!hasTranslatableText(container)) { delete translatePending[key]; return Promise.resolve('skipped') }

  return apiFetch('/api/translate', {
    method: 'POST',
    body: JSON.stringify({ tweet_id: tweetId, field: field, target_lang: targetLang })
  }).then(function (resp) {
    if (!resp || !resp.translated_text) return 'skipped'
    if (!textEl.hasAttribute('data-original-html') && !textEl.hasAttribute('data-original-text')) {
      textEl.setAttribute('data-original-html', textEl.innerHTML)
    }
    setHtmlContent(textEl, jsLinkify(resp.translated_text))
    container.setAttribute('data-translated', '1')
    // Prefer feed item's detected lang (data-lang) over kagi's source_lang —
    // langdetect runs on raw body text, kagi can be confused by mixed content
    var itemLang = (container.getAttribute('data-lang') || '').trim()
    var srcLang = (itemLang && KNOWN_LANGS[itemLang]) ? itemLang : resp.source_lang
    var label = card.querySelector('[data-translate-label]')
    if (label) label.textContent = srcLang ? srcLang.toUpperCase() : ''
    var tBtn = card.querySelector('.feed-translate-btn')
    if (tBtn) tBtn.classList.add('active')
    return 'translated'
  }).catch(function () {
    return 'failed'
  }).finally(function () {
    delete translatePending[key]
  })
}

function clearAutoTranslateRetry(key) {
  if (translateAutoRetryTimers[key]) {
    window.clearTimeout(translateAutoRetryTimers[key])
    delete translateAutoRetryTimers[key]
  }
  delete translateAutoRetryAttempts[key]
}

function queueAutoTranslateBlock(card, container) {
  var tweetId = card.getAttribute('data-tweet-id')
  var field = container.getAttribute('data-translate-field')
  var key = tweetId + ':' + field
  if (translatePending[key] || translateAutoQueued[key]) return
  translateAutoQueued[key] = true
  translateAutoQueue.push({ card: card, container: container, key: key })
  drainAutoTranslateQueue()
}

function scheduleAutoTranslateRetry(item) {
  if (!autoTranslateAvailable()) return
  if (!item.card.isConnected || !item.container.isConnected || !shouldAutoTranslateContainer(item.container)) return
  var attempt = translateAutoRetryAttempts[item.key] || 0
  if (attempt >= translateAutoRetryDelays.length) {
    delete item.card.dataset.feedTranslateBackground
    return
  }
  var delay = translateAutoRetryDelays[attempt]
  translateAutoRetryAttempts[item.key] = attempt + 1
  if (translateAutoRetryTimers[item.key]) window.clearTimeout(translateAutoRetryTimers[item.key])
  translateAutoRetryTimers[item.key] = window.setTimeout(function () {
    delete translateAutoRetryTimers[item.key]
    if (!item.card.isConnected || !item.container.isConnected || !shouldAutoTranslateContainer(item.container)) return
    queueAutoTranslateBlock(item.card, item.container)
  }, delay)
}

function runAutoTranslateItem(item) {
  translateAutoActive++
  translateBlock(item.card, item.container).then(function (status) {
    if (status === 'failed') {
      scheduleAutoTranslateRetry(item)
    } else if (status === 'translated' || status === 'skipped') {
      clearAutoTranslateRetry(item.key)
    }
  }).finally(function () {
    translateAutoActive--
    drainAutoTranslateQueue()
  })
}

function drainAutoTranslateQueue() {
  while (translateAutoActive < translateAutoConcurrency && translateAutoQueue.length) {
    var item = translateAutoQueue.shift()
    delete translateAutoQueued[item.key]
    if (!item.card.isConnected || !item.container.isConnected || !shouldAutoTranslateContainer(item.container)) continue
    runAutoTranslateItem(item)
  }
}

function autoTranslateAvailable() {
  return translateBackend && translateBackend !== 'none' && translateAutoMode !== 'off'
}

function shouldAutoTranslateContainer(container) {
  if (!hasTranslatableText(container)) return false
  var lang = (container.getAttribute('data-lang') || '').trim().toLowerCase()
  if (lang && lang === translateTarget) return false
  if (lang && translateSkipSet[lang]) return false
  if (hasSkippedLanguageScript(container)) return false
  if (container.getAttribute('data-translated') === '1') return false
  if (container.querySelector('[data-original-html], [data-original-text]')) return false
  return true
}

function translateCard(card) {
  var containers = card.querySelectorAll('[data-translate-field][data-lang]')
  containers.forEach(function (container) {
    if (shouldAutoTranslateContainer(container)) queueAutoTranslateBlock(card, container)
  })
}

export function handleTranslateAction(card, actionBtn) {
  var containers = card.querySelectorAll('[data-translate-field][data-lang]')
  if (!containers.length) return
  var anyTranslated = false
  containers.forEach(function (c) {
    if (c.getAttribute('data-translated') === '1') anyTranslated = true
  })
  if (anyTranslated) {
    containers.forEach(function (c) {
      if (c.getAttribute('data-translated') !== '1') return
      var textEl = c.querySelector('.feed-body-text, .feed-quote-text')
      if (textEl) {
        if (textEl.hasAttribute('data-original-html')) {
          setHtmlContent(textEl, textEl.getAttribute('data-original-html'))
        } else if (textEl.hasAttribute('data-original-text')) {
          setHtmlContent(textEl, jsLinkify(textEl.getAttribute('data-original-text')))
        }
      }
      c.setAttribute('data-translated', '0')
    })
    actionBtn.classList.remove('active')
    var label = card.querySelector('[data-translate-label]')
    if (label) label.textContent = label.getAttribute('data-translate-default-label') || ''
  } else {
    containers.forEach(function (c) {
      var lang = (c.getAttribute('data-lang') || '').trim()
      if (lang && lang === translateTarget) return
      translateBlock(card, c)
    })
    actionBtn.classList.add('active')
  }
}

export function queueBackgroundTranslations(scope) {
  if (!autoTranslateAvailable() || translateAutoMode !== 'background') return
  var root = scope || document
  root.querySelectorAll('[data-feed-item][data-tweet-id]').forEach(function (card) {
    if (!(card instanceof Element)) return
    if (card.dataset.feedTranslateBackground === '1') return
    card.dataset.feedTranslateBackground = '1'
    translateCard(card)
  })
}

// Lazy auto-translate observer: fires before cards enter view.
var translateObserver = null
export function getTranslateObserver() {
  if (!autoTranslateAvailable() || translateAutoMode !== 'lazy') return null
  if (translateObserver) return translateObserver
  translateObserver = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (!entry.isIntersecting) return
      var card = entry.target
      translateCard(card)
      translateObserver.unobserve(card)
    })
  }, { rootMargin: String(translateLookahead * 420) + 'px 0px ' + String(translateLookahead * 420) + 'px 0px' })
  return translateObserver
}
