// Translation module — handles translate toggle and auto-translate observer.

import { apiFetch, jsLinkify } from '../utils.js'

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

function translateButtonFor(card, field) {
  if (!card || !field) return null
  var buttons = card.querySelectorAll('.feed-translate-btn[data-feed-action="translate"][data-translate-target-field]')
  for (var i = 0; i < buttons.length; i++) {
    if (buttons[i].getAttribute('data-translate-target-field') === field) return buttons[i]
  }
  return null
}

function translateLabelForButton(button) {
  return button ? button.querySelector('[data-translate-label]') : null
}

function translateContainersForAction(card, actionBtn) {
  var containers = card.querySelectorAll('[data-translate-field][data-lang]')
  var field = actionBtn ? String(actionBtn.getAttribute('data-translate-target-field') || '').trim() : ''
  if (!field) return Array.prototype.slice.call(containers)
  var out = []
  containers.forEach(function (container) {
    if (container.getAttribute('data-translate-field') === field) out.push(container)
  })
  return out
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
    var srcLang = (resp.source_lang || '').trim()
    var tBtn = translateButtonFor(card, field)
    var label = translateLabelForButton(tBtn)
    if (label) label.textContent = srcLang
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
  var containers = translateContainersForAction(card, actionBtn)
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
    var label = translateLabelForButton(actionBtn)
    if (label) label.textContent = label.getAttribute('data-translate-default-label') || ''
  } else {
    containers.forEach(function (c) {
      var lang = (c.getAttribute('data-lang') || '').trim()
      if (lang && lang === translateTarget) return
      translateBlock(card, c)
    })
  }
}

export function queueBackgroundTranslations(scope) {
  // Background translation is owned by the server worker. The browser should
  // not fan out provider calls for every rendered card.
}

// Lazy auto-translate observer: fires before cards enter view.
var translateObserver = null
export function getTranslateObserver() {
  return null
}
