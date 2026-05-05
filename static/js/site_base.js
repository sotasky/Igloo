// Global configurable keyboard shortcuts — backed by server preferences.json
(function () {
  var defaults = {
    'feed.like': 'l', 'feed.bookmark': 'b', 'feed.share': 's', 'feed.translate': 't', 'feed.media': 'f',
    'shorts.mute': 'm', 'shorts.autoplay': 'a', 'shorts.bookmark': 'b', 'shorts.share': 's', 'shorts.grid': 'c',
    'player.fullscreen': 'f', 'player.bookmark': 'b', 'player.share': 's', 'player.autoplay': 'a',
    'global.logs': 'o',
    'global.search': 'h'
  };

  // Server embeds current config via window._cfShortcutConfig
  var serverConfig = window._cfShortcutConfig || {};
  var shortcutLabels = (window.IglooI18n && window.IglooI18n.messages) || {};
  // Current bindings: merge server config over defaults
  var current = {};
  var id;
  for (id in defaults) current[id] = serverConfig[id] || defaults[id];

  var displayMap = {
    'ArrowDown': '\u2193', 'ArrowUp': '\u2191', 'ArrowLeft': '\u2190', 'ArrowRight': '\u2192',
    'Escape': shortcutLabels.shortcuts_key_escape || 'Esc',
    ' ': shortcutLabels.shortcuts_key_space || 'Space',
    'Tab': shortcutLabels.shortcuts_key_tab || 'Tab'
  };

  window.cfShortcuts = {
    defaults: defaults,
    _current: current,
    _dirty: false,
    key: function (id) { return current[id] || defaults[id]; },
    match: function (id, eventKey) {
      var bound = this.key(id);
      if (!bound) return false;
      if (bound.length === 1) return eventKey.toLowerCase() === bound;
      return eventKey === bound;
    },
    displayKey: function (key) {
      if (!key) return '?';
      if (displayMap[key]) return displayMap[key];
      if (key.length === 1) return key.toUpperCase();
      return key;
    },
    set: function (id, key) {
      current[id] = key;
      this._dirty = true;
    },
    reset: function (id) {
      current[id] = defaults[id];
      this._dirty = true;
    },
    resetAll: function () {
      for (var k in defaults) current[k] = defaults[k];
      this._dirty = true;
    },
    isCustom: function (id) { return current[id] !== defaults[id]; },
    allIds: function () { return Object.keys(defaults); },
    /** Return current bindings for inclusion in settings save payload. */
    collect: function () { return JSON.parse(JSON.stringify(current)); }
  };

  // Clean up legacy localStorage if present
  try { localStorage.removeItem('cf_shortcuts'); } catch (e) {}
})();

(function () {
  const doc = document;
  const body = doc.body;
  const csrfToken = ((doc.querySelector('meta[name="csrf-token"]') || {}).content || '').trim();
  const i18n = window.IglooI18n || {};
  var previewLanguageSeq = 0;

  function formatMessage(message, args) {
    var out = String(message == null ? '' : message);
    if (!args || !args.length) return out;
    args.forEach(function (arg, index) {
      out = out.split('%' + (index + 1) + '$s').join(String(arg));
    });
    return out;
  }

  function translate(key, fallback, args) {
    key = String(key == null ? '' : key);
    const messages = i18n && i18n.messages;
    var message = messages && messages[key] ? messages[key] : (fallback || key);
    return formatMessage(message, args);
  }

  function t(message) {
    return translate(message, message);
  }

  function i18nArgs(el, prefix) {
    var raw = el.getAttribute(prefix + '-args') || '';
    if (!raw) return [];
    return raw.split('|');
  }

  function i18nTextMap(previousMessages, nextMessages) {
    var mapped = {};
    var blocked = {};
    Object.keys(previousMessages || {}).forEach(function (key) {
      var previous = previousMessages[key];
      var next = nextMessages && nextMessages[key];
      if (!previous || !next) return;
      previous = String(previous);
      if (!previous.trim()) return;
      if (mapped[previous] && mapped[previous] !== key && nextMessages[mapped[previous]] !== next) {
        blocked[previous] = true;
        return;
      }
      mapped[previous] = key;
    });
    Object.keys(blocked).forEach(function (value) { delete mapped[value]; });
    return mapped;
  }

  function translatedText(value, textMap, nextMessages) {
    if (!value) return null;
    var leading = (value.match(/^\s*/) || [''])[0];
    var trailing = (value.match(/\s*$/) || [''])[0];
    var inner = value.slice(leading.length, value.length - trailing.length);
    if (!inner) return null;
    var key = textMap[inner];
    if (!key || !nextMessages || !nextMessages[key]) return null;
    return leading + String(nextMessages[key]) + trailing;
  }

  function applyI18nScopedText(root, previousMessages, nextMessages) {
    if (!previousMessages || !nextMessages) return;
    var textMap = i18nTextMap(previousMessages, nextMessages);
    var scopes = [];
    if (root && root.nodeType === 1 && root.matches && root.matches('[data-i18n-scope]')) scopes.push(root);
    qa('[data-i18n-scope]', root || doc).forEach(function (scope) { scopes.push(scope); });
    scopes.forEach(function (scope) {
      var walker = doc.createTreeWalker(scope, NodeFilter.SHOW_TEXT, {
        acceptNode: function (node) {
          var parent = node.parentElement;
          if (!parent) return NodeFilter.FILTER_REJECT;
          if (/^(script|style|textarea)$/i.test(parent.tagName || '')) return NodeFilter.FILTER_REJECT;
          return NodeFilter.FILTER_ACCEPT;
        }
      });
      var updates = [];
      while (walker.nextNode()) {
        var replacement = translatedText(walker.currentNode.nodeValue, textMap, nextMessages);
        if (replacement != null && replacement !== walker.currentNode.nodeValue) {
          updates.push([walker.currentNode, replacement]);
        }
      }
      updates.forEach(function (update) { update[0].nodeValue = update[1]; });
      qa('[title],[aria-label],[placeholder]', scope).forEach(function (el) {
        ['title', 'aria-label', 'placeholder'].forEach(function (attr) {
          if (!el.hasAttribute(attr)) return;
          var replacement = translatedText(el.getAttribute(attr), textMap, nextMessages);
          if (replacement != null) el.setAttribute(attr, replacement);
        });
      });
    });
  }

  function applyI18nAttributes(root) {
    root = root || doc;
    qa('[data-i18n]', root).forEach(function (el) {
      var key = el.getAttribute('data-i18n');
      el.textContent = translate(key, el.getAttribute('data-i18n-fallback'), i18nArgs(el, 'data-i18n'));
    });
    qa('[data-i18n-title]', root).forEach(function (el) {
      var key = el.getAttribute('data-i18n-title');
      el.title = translate(key, el.getAttribute('data-i18n-title-fallback'), i18nArgs(el, 'data-i18n-title'));
    });
    qa('[data-i18n-aria-label]', root).forEach(function (el) {
      var key = el.getAttribute('data-i18n-aria-label');
      el.setAttribute('aria-label', translate(key, el.getAttribute('data-i18n-aria-label-fallback'), i18nArgs(el, 'data-i18n-aria-label')));
    });
    qa('[data-i18n-placeholder]', root).forEach(function (el) {
      var key = el.getAttribute('data-i18n-placeholder');
      el.placeholder = translate(key, el.getAttribute('data-i18n-placeholder-fallback'), i18nArgs(el, 'data-i18n-placeholder'));
    });
  }

  function setCatalog(catalog) {
    if (!catalog || !catalog.messages) return;
    var previousMessages = i18n.messages || {};
    i18n.language = catalog.language || i18n.language;
    i18n.messages = catalog.messages;
    if (i18n.language) doc.documentElement.lang = i18n.language;
    applyI18nAttributes(doc);
    applyI18nScopedText(doc, previousMessages, i18n.messages);
    try {
      doc.dispatchEvent(new CustomEvent('igloo:i18n:changed', { detail: { language: i18n.language } }));
    } catch (e) {}
  }

  function applyLanguage(lang) {
    var value = lang || 'auto';
    var seq = ++previewLanguageSeq;
    return fetch('/api/i18n/catalog?lang=' + encodeURIComponent(value), {
      headers: { 'Accept': 'application/json' }
    }).then(function (response) {
      if (!response.ok) throw new Error('i18n catalog request failed');
      return response.json();
    }).then(function (catalog) {
      if (seq === previewLanguageSeq) setCatalog(catalog);
    }).catch(function () {});
  }

  function previewLanguage(lang) {
    return applyLanguage(lang);
  }

  function showPrefsStatus(kind, text) {
    var status = doc.getElementById('prefs-status');
    if (!status) return null;
    status.className = 'status-msg ' + kind;
    status.textContent = text;
    return status;
  }

  function clearPrefsStatusLater(text) {
    setTimeout(function () {
      var status = doc.getElementById('prefs-status');
      if (status && status.textContent === text) {
        status.textContent = '';
        status.className = 'status-msg';
      }
    }, 2500);
  }

  function serializePrefsForm(form) {
    if (!form) return '';
    var parts = [];
    qa('input,select,textarea', form).forEach(function (field) {
      if (!field.name || field.disabled) return;
      var name = encodeURIComponent(field.name);
      var type = String(field.type || '').toLowerCase();
      if (type === 'checkbox' || type === 'radio') {
        parts.push(name + '=' + (field.checked ? encodeURIComponent(field.value || 'on') : ''));
        return;
      }
      if (field.tagName && field.tagName.toLowerCase() === 'select' && field.multiple) {
        qa('option', field).forEach(function (option) {
          if (option.selected) parts.push(name + '=' + encodeURIComponent(option.value));
        });
        return;
      }
      parts.push(name + '=' + encodeURIComponent(field.value || ''));
    });
    return parts.join('&');
  }

  function setPrefsReminder(form, visible) {
    form = form || doc.getElementById('prefs-form');
    var reminder = doc.getElementById('prefs-unsaved-reminder');
    if (!reminder) return;
    reminder.classList.toggle('hidden', !visible);
    reminder.setAttribute('aria-hidden', visible ? 'false' : 'true');
  }

  function updatePrefsDirtyState(form) {
    form = form || doc.getElementById('prefs-form');
    if (!form) return;
    if (!form.dataset.initialState) form.dataset.initialState = serializePrefsForm(form);
    setPrefsReminder(form, serializePrefsForm(form) !== form.dataset.initialState);
  }

  function resetPrefsDirtyState(form) {
    form = form || doc.getElementById('prefs-form');
    if (!form) return;
    form.dataset.initialState = serializePrefsForm(form);
    setPrefsReminder(form, false);
  }

  function initPrefsDirtyState(root) {
    var form = root && root.id === 'prefs-form' ? root : (root || doc).querySelector && (root || doc).querySelector('#prefs-form');
    if (form) resetPrefsDirtyState(form);
  }

  function handlePrefsAfterRequest(event, savedFallback, failedFallback) {
    if (event.detail && event.detail.elt && event.detail.elt !== this) return;
    if (!event.detail || !event.detail.successful) {
      if (window.IglooWebTheme && window.IglooWebTheme.revertPreview) window.IglooWebTheme.revertPreview();
      showPrefsStatus('error', translate('status_preferences_save_failed', failedFallback || 'Failed to save preferences'));
      return;
    }

    var speed = doc.querySelector('#prefs-form [name=youtube_default_playback_speed]');
    if (speed) {
      try {
        localStorage.setItem('youtube_default_playback_rate', speed.value);
        localStorage.setItem('youtube_playback_rate', speed.value);
      } catch (e) {}
    }

    var form = doc.getElementById('prefs-form');
    var shareEmbedFriendly = form && form.querySelector('[name=share_embed_friendly_links]');
    if (form) {
      window.IglooPreferences = Object.assign({}, window.IglooPreferences || {}, {
        shareEmbedFriendlyLinks: !!(shareEmbedFriendly && shareEmbedFriendly.checked)
      });
    }
    var lang = form && form.querySelector('[name=ui_language]');
    var previousLang = form ? form.dataset.persistedUiLanguage : '';
    var nextLang = lang ? (lang.value || 'auto') : '';
    var changed = !!(form && lang && nextLang !== previousLang);
    if (form && lang) form.dataset.persistedUiLanguage = nextLang;
    if (window.IglooWebTheme && window.IglooWebTheme.commitAfterSave) window.IglooWebTheme.commitAfterSave();

    function done() {
      resetPrefsDirtyState(form);
      var text = translate('status_preferences_saved', savedFallback || 'Preferences saved');
      showPrefsStatus('success', text);
      clearPrefsStatusLater(text);
    }

    function refreshForm() {
      if (changed && window.htmx) {
        var req = window.htmx.ajax('GET', '/api/settings/form', { target: '#prefs-body', swap: 'innerHTML' });
        if (req && req.then) {
          req.then(done, done);
          return;
        }
      }
      done();
    }

    if (changed && nextLang) {
      var apply = applyLanguage(nextLang);
      if (apply && apply.then) {
        apply.then(refreshForm, refreshForm);
        return;
      }
    }
    refreshForm();
  }

  i18n.t = t;
  i18n.apply = applyI18nAttributes;
  i18n.setCatalog = setCatalog;
  i18n.applyLanguage = applyLanguage;
  i18n.previewLanguage = previewLanguage;
  i18n.handlePrefsAfterRequest = handlePrefsAfterRequest;
  window.IglooI18n = i18n;

  body.addEventListener('htmx:afterSettle', function (event) {
    var elt = event.detail && event.detail.elt;
    var root = elt && elt.parentElement ? elt.parentElement : doc;
    applyI18nAttributes(root);
    initPrefsDirtyState(root);
  });

  doc.addEventListener('input', function (event) {
    if (event.target && event.target.closest && event.target.closest('#prefs-form')) {
      updatePrefsDirtyState(event.target.closest('#prefs-form'));
    }
  });

  doc.addEventListener('change', function (event) {
    if (event.target && event.target.closest && event.target.closest('#prefs-form')) {
      updatePrefsDirtyState(event.target.closest('#prefs-form'));
    }
  });

  function q(sel, root) {
    return (root || doc).querySelector(sel);
  }

  function qa(sel, root) {
    return Array.prototype.slice.call((root || doc).querySelectorAll(sel));
  }

  function setText(el, value) {
    if (!el) return;
    el.textContent = String(value == null ? '' : value);
  }

  function setHtml(el, html) {
    if (!el) return;
    el.innerHTML = String(html || '');
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function avatarFallbackEl(img) {
    return img && img.nextElementSibling ? img.nextElementSibling : null;
  }

  function avatarFallbackDisplay(fallback) {
    if (!fallback) return 'inline-flex';
    return fallback.tagName === 'DIV' ? 'flex' : 'inline-flex';
  }

  function showAvatarFallback(img) {
    if (!img) return;
    img.style.display = 'none';
    var fallback = avatarFallbackEl(img);
    if (fallback) fallback.style.display = avatarFallbackDisplay(fallback);
  }

  function hideAvatarFallback(img) {
    if (!img) return;
    img.style.display = '';
    var fallback = avatarFallbackEl(img);
    if (fallback) fallback.style.display = 'none';
  }

  function clearAvatarRetry(img) {
    if (!img || !img._avatarRetryTimer) return;
    clearTimeout(img._avatarRetryTimer);
    img._avatarRetryTimer = null;
  }

  function avatarBaseSrc(src) {
    src = String(src || '').trim();
    if (!src) return '';
    try {
      var url = new URL(src, window.location.origin);
      url.searchParams.delete('avatar_retry');
      url.searchParams.delete('avatar_refresh');
      return url.pathname + url.search + url.hash;
    } catch (_) {
      return src
        .replace(/([?&])avatar_retry=\d+(&|$)/, '$1')
        .replace(/([?&])avatar_refresh=\d+(&|$)/, '$1')
        .replace(/[?&]$/, '');
    }
  }

  function avatarImageBaseSrc(img) {
    if (!img) return '';
    return avatarBaseSrc(img.dataset.avatarBaseSrc || img.getAttribute('src') || img.src || '');
  }

  function retryAvatarNow(img, baseSrc) {
    if (!img || !baseSrc || !img.isConnected) return;
    clearAvatarRetry(img);
    img.dataset.avatarBaseSrc = baseSrc;
    delete img.dataset.avatarRetryCount;
    var sep = baseSrc.indexOf('?') === -1 ? '?' : '&';
    img.src = baseSrc + sep + 'avatar_refresh=' + Date.now();
  }

  function recoverableProfileMediaSrc(src) {
    src = String(src || '');
    return src.indexOf('/api/media/avatar/') !== -1 || src.indexOf('/api/media/banner/') !== -1;
  }

  function refreshMatchingAvatars(loadedImg) {
    var baseSrc = avatarImageBaseSrc(loadedImg);
    if (!recoverableProfileMediaSrc(baseSrc)) return;
    qa('img').forEach(function (img) {
      if (img === loadedImg) return;
      if (avatarImageBaseSrc(img) !== baseSrc) return;
      if (img.complete && img.naturalWidth > 0 && img.style.display !== 'none') return;
      retryAvatarNow(img, baseSrc);
    });
  }

  function avatarLoad(img) {
    clearAvatarRetry(img);
    if (img) delete img.dataset.avatarRetryCount;
    hideAvatarFallback(img);
    refreshMatchingAvatars(img);
  }

  function avatarError(img) {
    showAvatarFallback(img);
    if (!img) return true;

    var src = String(img.getAttribute('src') || img.src || '').trim();
    if (!recoverableProfileMediaSrc(src)) return true;

    var retryCount = Number(img.dataset.avatarRetryCount || '0');
    var delays = [1200, 3500, 8000, 16000];
    if (retryCount >= delays.length) return true;

    var baseSrc = avatarBaseSrc(img.dataset.avatarBaseSrc || src);
    img.dataset.avatarBaseSrc = baseSrc;
    img.dataset.avatarRetryCount = String(retryCount + 1);
    clearAvatarRetry(img);
    img._avatarRetryTimer = setTimeout(function () {
      if (!img.isConnected) return;
      var sep = baseSrc.indexOf('?') === -1 ? '?' : '&';
      img.src = baseSrc + sep + 'avatar_retry=' + Date.now();
    }, delays[retryCount]);
    return true;
  }

  function endpointToUrl(endpointOrUrl) {
    const s = String(endpointOrUrl || '').trim();
    if (!s) return '';
    if (/^https?:\/\//i.test(s)) return s;
    if (s.indexOf('/api/') === 0) return s;
    if (s.charAt(0) === '/') return s;
    return '/api/' + s.replace(/^\/+/, '');
  }

  function apiJson(endpointOrUrl, options) {
    const url = endpointToUrl(endpointOrUrl);
    const opts = Object.assign({ credentials: 'same-origin', headers: {} }, options || {});
    opts.headers = Object.assign({}, opts.headers || {});
    if (csrfToken) {
      opts.headers['X-CSRF-Token'] = csrfToken;
    }
    if (opts.body != null && !opts.headers['Content-Type']) {
      opts.headers['Content-Type'] = 'application/json';
    }
    return fetch(url, opts).then(function (response) {
      if (response.status === 401) {
        window.location.href = '/login';
        throw new Error('Unauthorized');
      }
      return response.text().then(function (text) {
        let payload = null;
        try {
          payload = text ? JSON.parse(text) : null;
        } catch (_) {
          payload = null;
        }
        if (!response.ok) {
          const err = new Error('HTTP ' + response.status);
          err.status = response.status;
          err.payload = payload;
          throw err;
        }
        return payload;
      });
    });
  }

  function showToast(message, timeoutMs) {
    const text = String(message || '').trim();
    if (!text) return;
    let host = q('#app-toast');
    if (!host) {
      host = doc.createElement('div');
      host.id = 'app-toast';
      host.style.position = 'fixed';
      host.style.left = '50%';
      host.style.bottom = '18px';
      host.style.transform = 'translateX(-50%)';
      host.style.zIndex = '99999';
      host.style.padding = '10px 14px';
      host.style.borderRadius = '10px';
      host.style.background = 'rgba(0,0,0,0.85)';
      host.style.color = '#fff';
      host.style.fontSize = '13px';
      host.style.lineHeight = '1.35';
      host.style.maxWidth = '88vw';
      host.style.textAlign = 'center';
      host.style.boxShadow = '0 6px 18px rgba(0,0,0,0.35)';
      host.style.opacity = '0';
      host.style.pointerEvents = 'none';
      host.style.transition = 'opacity .16s ease';
      doc.body.appendChild(host);
    }
    host.textContent = text;
    host.style.opacity = '1';
    clearTimeout(showToast._timer);
    showToast._timer = setTimeout(function () {
      if (host) host.style.opacity = '0';
    }, Math.max(900, Number(timeoutMs) || 2200));
  }

  function copyText(text) {
    const value = String(text || '');
    if (!value) return Promise.reject(new Error('empty'));
    return navigator.clipboard.writeText(value);
  }

  // Sidebar toggle
  const sidebarToggle = q('#sidebar-toggle');
  const sidebarOverlay = q('#sidebar-overlay');
  if (sidebarToggle) {
    sidebarToggle.addEventListener('click', function () {
      body.classList.toggle('sidebar-open');
    });
  }
  if (sidebarOverlay) {
    sidebarOverlay.addEventListener('click', function () {
      body.classList.remove('sidebar-open');
    });
  }

  // Modal helpers
  function openModal(modal) {
    if (!modal) return;
    modal.classList.remove('hidden');
    modal.setAttribute('aria-hidden', 'false');
    doc.body.style.overflow = 'hidden';
  }

  function closeModal(modal) {
    if (!modal) return;
    modal.classList.add('hidden');
    modal.setAttribute('aria-hidden', 'true');
    // Restore body scroll if no other modals are open
    var anyOpen = false;
    qa('.modal').forEach(function(m) { if (!m.classList.contains('hidden')) anyOpen = true; });
    if (!anyOpen) doc.body.style.overflow = '';
    if (modal.id === 'logs-modal') stopLogsPolling();
  }

  qa('.modal').forEach(function (modal) {
    modal.addEventListener('click', function (event) {
      if (event.target && event.target.classList && event.target.classList.contains('modal-backdrop')) {
        closeModal(modal);
      }
    });
    qa('.modal-close', modal).forEach(function (btn) {
      btn.addEventListener('click', function () {
        closeModal(modal);
      });
    });
  });

  doc.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') return;
    const open = q('.modal:not(.hidden)');
    if (open) closeModal(open);
  });

  // Status polling — now HTMX-driven via #sidebar-status (hx-trigger="every 4s")
  // Kick an immediate poll after actions (refresh, star, etc.)
  function schedulePoll() {
    var el = doc.getElementById('sidebar-status');
    if (el && typeof htmx !== 'undefined') htmx.trigger(el, 'poll');
  }

  // Stop/Play toggle — now HTMX-driven via header button

  // ── Tabbed Logs Modal ──
  var logsModal = q('#logs-modal');

  var logsActiveTab = 'server';
  var logsPollTimer = 0;
  var _serverTzOffsetSec = 0; // populated from API responses

  // Convert UTC epoch seconds to HH:MM:SS in server local time
  function serverHMS(epochSec) {
    var ts = new Date((epochSec + _serverTzOffsetSec) * 1000);
    return {
      hh: String(ts.getUTCHours()).padStart(2, '0'),
      mm: String(ts.getUTCMinutes()).padStart(2, '0'),
      ss: String(ts.getUTCSeconds()).padStart(2, '0')
    };
  }

  function logsPanelSelector(tab) {
    if (tab === 'downloads') return '#dl-panel-content';
    if (tab === 'android') return '#an-panel-content';
    if (tab === 'twitter') return '#feed-panel-content';
    return '#sv-panel-content';
  }

  function pollActiveLogsPanel() {
    if (!logsModal || logsModal.classList.contains('hidden')) return;
    if (typeof htmx === 'undefined') return;
    var el = doc.querySelector(logsPanelSelector(logsActiveTab));
    if (el) htmx.trigger(el, 'logs-poll');
  }

  function stopLogsPolling() {
    if (!logsPollTimer) return;
    clearInterval(logsPollTimer);
    logsPollTimer = 0;
  }

  function startLogsPolling(immediate) {
    stopLogsPolling();
    if (immediate !== false) pollActiveLogsPanel();
    logsPollTimer = setInterval(pollActiveLogsPanel, 3000);
  }

  function switchLogsTab(tab) {
    logsActiveTab = tab;
    qa('[data-logs-tab]').forEach(function (btn) {
      btn.classList.toggle('active', btn.getAttribute('data-logs-tab') === tab);
    });
    qa('[data-logs-panel]').forEach(function (panel) {
      panel.classList.toggle('active', panel.getAttribute('data-logs-panel') === tab);
    });
    pollActiveLogsPanel();
  }

  // Tab click handlers
  qa('[data-logs-tab]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      switchLogsTab(btn.getAttribute('data-logs-tab'));
    });
  });

  // Left/Right arrow keys to switch logs tabs
  doc.addEventListener('keydown', function (e) {
    if (!logsModal || logsModal.classList.contains('hidden')) return;
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    var tag = (e.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea') return;
    e.preventDefault();
    var tabs = qa('[data-logs-tab]').map(function (b) { return b.getAttribute('data-logs-tab'); });
    var idx = tabs.indexOf(logsActiveTab);
    if (idx === -1) return;
    var next = idx + (e.key === 'ArrowRight' ? 1 : -1);
    if (next >= 0 && next < tabs.length) switchLogsTab(tabs[next]);
  });

  // ── Downloads Dashboard — extracted to dashboard_downloads.js ──

  // ── Server Dashboard — extracted to dashboard_server.js ──

  // ── Android Dashboard — extracted to dashboard_android.js ──

  // ── Feed Dashboard — search + scroll persistence across HTMX swaps ──
  var _feedSourceSearch = '';
  var _feedSourceScrollTop = 0;

  function applyFeedSourceSearch(val) {
    var tbody = doc.querySelector('#feed-panel-content .feed-source-table tbody');
    if (!tbody) return;
    var lower = val.toLowerCase();
    tbody.querySelectorAll('tr').forEach(function (tr) {
      var link = tr.querySelector('.feed-handle-link');
      var text = link ? link.textContent.toLowerCase() : '';
      tr.style.display = (!lower || text.indexOf(lower) !== -1) ? '' : 'none';
    });
  }

  function feedHasActiveSelection() {
    var sel = window.getSelection && window.getSelection();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) return false;
    var panel = doc.getElementById('feed-panel-content');
    if (!panel) return false;
    var node = sel.anchorNode;
    return node && panel.contains(node);
  }

  doc.addEventListener('htmx:beforeSwap', function (e) {
    if (!e.target || e.target.id !== 'feed-panel-content') return;
    // Don't wipe the user's text selection mid-copy.
    if (feedHasActiveSelection()) { e.detail.shouldSwap = false; return; }
    // Don't interrupt typing in the search box.
    var active = doc.activeElement;
    if (active && active.id === 'feed-source-search') { e.detail.shouldSwap = false; return; }
    var inp = doc.getElementById('feed-source-search');
    if (inp) _feedSourceSearch = inp.value;
    var body = doc.getElementById('feed-sources-body');
    if (body) _feedSourceScrollTop = body.scrollTop;
  });

  doc.addEventListener('htmx:afterSettle', function (e) {
    if (!e.target || e.target.id !== 'feed-panel-content') return;
    var inp = doc.getElementById('feed-source-search');
    if (inp) {
      if (_feedSourceSearch) { inp.value = _feedSourceSearch; applyFeedSourceSearch(_feedSourceSearch); }
    }
    var body = doc.getElementById('feed-sources-body');
    if (body && _feedSourceScrollTop) body.scrollTop = _feedSourceScrollTop;
  });

  doc.addEventListener('input', function (e) {
    if (!e.target || e.target.id !== 'feed-source-search') return;
    _feedSourceSearch = e.target.value;
    applyFeedSourceSearch(_feedSourceSearch);
  });

  function toggleLogsModal() {
    if (!logsModal) return;
    if (!logsModal.classList.contains('hidden')) {
      closeModal(logsModal);
      return;
    }
    openModal(logsModal);
    startLogsPolling(true);
  }
  function openRsshubDiagnosticsModal() {
    if (!logsModal) return;
    openModal(logsModal);
    switchLogsTab('twitter');
    startLogsPolling(false);
  }

  // Preferences modal
  const prefsModal = q('#prefs-modal');
  const prefsBtn = q('#prefs-btn');

  // ── Custom select dropdown logic ──
  (function initCustomSelects() {
    qa('.custom-select').forEach(function (wrap) {
      var trigger = wrap.querySelector('.custom-select-trigger');
      var dropdown = wrap.querySelector('.custom-select-dropdown');
      var label = wrap.querySelector('.custom-select-label');
      var hidden = wrap.nextElementSibling && wrap.nextElementSibling.type === 'hidden'
        ? wrap.nextElementSibling
        : wrap.parentNode.querySelector('input[type="hidden"]');
      if (!trigger || !dropdown) return;

      trigger.addEventListener('click', function (e) {
        e.stopPropagation();
        var wasOpen = wrap.classList.contains('open');
        // Close all other custom selects
        qa('.custom-select.open').forEach(function (other) {
          if (other !== wrap) { other.classList.remove('open'); other.querySelector('.custom-select-dropdown').classList.add('hidden'); }
        });
        if (wasOpen) {
          wrap.classList.remove('open');
          dropdown.classList.add('hidden');
        } else {
          wrap.classList.add('open');
          dropdown.classList.remove('hidden');
        }
      });

      dropdown.addEventListener('click', function (e) {
        var opt = e.target.closest('.custom-select-option');
        if (!opt) return;
        var val = opt.dataset.value;
        // Update active state
        dropdown.querySelectorAll('.custom-select-option').forEach(function (o) { o.classList.remove('active'); });
        opt.classList.add('active');
        // Update label — use first text node content (before the desc span)
        if (label) label.textContent = opt.childNodes[0].textContent.trim();
        // Update hidden input
        if (hidden) { hidden.value = val; hidden.dispatchEvent(new Event('change')); }
        // Close
        wrap.classList.remove('open');
        dropdown.classList.add('hidden');
      });
    });

    // Close on outside click
    document.addEventListener('click', function () {
      qa('.custom-select.open').forEach(function (wrap) {
        wrap.classList.remove('open');
        wrap.querySelector('.custom-select-dropdown').classList.add('hidden');
      });
    });

    // Toggle API config visibility on provider change
    function toggleApiConfig() {
      var backendHidden = q('#global-setting-translate-backend');
      var apiConfig = q('#translate-api-config');
      var apiHint = q('#translate-api-key-hint');
      var modelConfig = q('#translate-model-config');
      if (!backendHidden) return;
      var showApiFields = backendHidden.value === 'google' || backendHidden.value === 'deepl' || backendHidden.value === 'openai_compat';
      var showAPIKeyHint = backendHidden.value === 'google' || backendHidden.value === 'deepl';
      if (apiConfig) apiConfig.style.display = showApiFields ? '' : 'none';
      if (apiHint) apiHint.style.display = showAPIKeyHint ? 'block' : 'none';
      if (modelConfig) modelConfig.style.display = backendHidden.value === 'openai_compat' ? '' : 'none';
    }
    function toggleTranslateLookahead() {
      var autoMode = q('#global-setting-translate-auto-mode');
      var lookaheadConfig = q('#translate-lookahead-config');
      if (!autoMode || !lookaheadConfig) return;
      lookaheadConfig.style.display = autoMode.value === 'lazy' ? '' : 'none';
    }
    function toggleTranslateConfig() {
      toggleApiConfig();
      toggleTranslateLookahead();
    }
    doc.addEventListener('change', function (e) {
      if (e.target && e.target.id === 'global-setting-translate-backend') toggleApiConfig();
      if (e.target && e.target.id === 'global-setting-translate-auto-mode') toggleTranslateLookahead();
    });
    doc.addEventListener('htmx:afterSettle', function (e) {
      if (e.target && e.target.id === 'prefs-body') toggleTranslateConfig();
    });
    toggleTranslateConfig();
  })();

  // Native select popups are browser-owned and ignore most theme styling. Keep
  // the real select for forms/HTMX, but drive it through a themed listbox.
  (function initThemedNativeSelects() {
    var activeWrap = null;
    var uid = 0;

    function ensureID(el, prefix) {
      if (el.id) return el.id;
      uid += 1;
      el.id = prefix + '-' + Date.now().toString(36) + '-' + uid;
      return el.id;
    }

    function optionLabel(option) {
      return option ? String(option.textContent || option.label || option.value || '').trim() : '';
    }

    function selectedOption(select) {
      if (!select) return null;
      if (select.selectedIndex >= 0 && select.options[select.selectedIndex]) return select.options[select.selectedIndex];
      return select.options.length ? select.options[0] : null;
    }

    function closeThemedSelect(wrap) {
      if (!wrap) return;
      var trigger = wrap.querySelector('.themed-select-trigger');
      var menu = themedMenuFor(wrap);
      wrap.classList.remove('open');
      if (trigger) trigger.setAttribute('aria-expanded', 'false');
      if (menu) menu.classList.add('hidden');
      if (activeWrap === wrap) activeWrap = null;
    }

    function closeAllThemedSelects(except) {
      qa('.themed-select.open').forEach(function (wrap) {
        if (wrap !== except) closeThemedSelect(wrap);
      });
    }

    function themedMenuFor(wrap) {
      var id = wrap && wrap.getAttribute('data-themed-menu-id');
      return id ? doc.getElementById(id) : null;
    }

    function positionMenu(wrap) {
      var trigger = wrap && wrap.querySelector('.themed-select-trigger');
      var menu = themedMenuFor(wrap);
      if (!trigger || !menu || menu.classList.contains('hidden')) return;
      var rect = trigger.getBoundingClientRect();
      var viewportH = window.innerHeight || doc.documentElement.clientHeight || 0;
      var gap = 6;
      var below = Math.max(0, viewportH - rect.bottom - gap);
      var above = Math.max(0, rect.top - gap);
      var openAbove = below < 180 && above > below;
      var maxHeight = Math.max(120, Math.min(320, openAbove ? above : below));
      menu.style.left = Math.max(8, rect.left) + 'px';
      menu.style.width = Math.max(140, rect.width) + 'px';
      menu.style.maxHeight = maxHeight + 'px';
      if (openAbove) {
        menu.style.top = '';
        menu.style.bottom = Math.max(gap, viewportH - rect.top + gap) + 'px';
      } else {
        menu.style.bottom = '';
        menu.style.top = Math.min(viewportH - gap, rect.bottom + gap) + 'px';
      }
    }

    function syncThemedSelect(wrap) {
      var select = wrap && wrap.querySelector('select.input');
      var trigger = wrap && wrap.querySelector('.themed-select-trigger');
      var label = wrap && wrap.querySelector('.themed-select-value');
      var menu = themedMenuFor(wrap);
      var selected = selectedOption(select);
      if (label) label.textContent = optionLabel(selected);
      if (trigger && select) trigger.disabled = select.disabled;
      if (!menu || !select) return;
      qa('.themed-select-option', menu).forEach(function (item) {
        var active = item.getAttribute('data-value') === select.value;
        item.classList.toggle('active', active);
        item.setAttribute('aria-selected', active ? 'true' : 'false');
      });
    }

    function renderOptions(wrap) {
      var select = wrap && wrap.querySelector('select.input');
      var menu = themedMenuFor(wrap);
      if (!select || !menu) return;
      menu.textContent = '';
      Array.prototype.forEach.call(select.options || [], function (option) {
        if (option.hidden) return;
        var item = doc.createElement('button');
        item.type = 'button';
        item.className = 'themed-select-option';
        item.setAttribute('role', 'option');
        item.setAttribute('data-value', option.value);
        item.textContent = optionLabel(option);
        if (option.disabled) {
          item.disabled = true;
          item.setAttribute('aria-disabled', 'true');
        }
        menu.appendChild(item);
      });
      syncThemedSelect(wrap);
    }

    function selectThemedOption(wrap, item) {
      var select = wrap && wrap.querySelector('select.input');
      if (!select || !item || item.disabled) return;
      var previous = select.value;
      select.value = item.getAttribute('data-value') || '';
      syncThemedSelect(wrap);
      closeThemedSelect(wrap);
      if (select.value !== previous) {
        select.dispatchEvent(new Event('input', { bubbles: true }));
        select.dispatchEvent(new Event('change', { bubbles: true }));
      }
    }

    function focusOptionByDelta(wrap, delta) {
      var menu = themedMenuFor(wrap);
      if (!menu) return;
      var items = qa('.themed-select-option:not(:disabled)', menu);
      if (!items.length) return;
      var index = items.findIndex(function (item) { return item.classList.contains('active'); });
      if (index < 0) index = 0;
      else index = Math.max(0, Math.min(items.length - 1, index + delta));
      items.forEach(function (item, i) { item.classList.toggle('is-highlighted', i === index); });
      items[index].scrollIntoView({ block: 'nearest' });
    }

    function highlightedOption(wrap) {
      var menu = themedMenuFor(wrap);
      return menu && (menu.querySelector('.themed-select-option.is-highlighted') || menu.querySelector('.themed-select-option.active'));
    }

    function openThemedSelect(wrap) {
      var trigger = wrap && wrap.querySelector('.themed-select-trigger');
      var menu = themedMenuFor(wrap);
      if (!wrap || !trigger || !menu || trigger.disabled) return;
      closeAllThemedSelects(wrap);
      activeWrap = wrap;
      renderOptions(wrap);
      wrap.classList.add('open');
      trigger.setAttribute('aria-expanded', 'true');
      menu.classList.remove('hidden');
      positionMenu(wrap);
      var active = menu.querySelector('.themed-select-option.active');
      if (active) {
        active.classList.add('is-highlighted');
        active.scrollIntoView({ block: 'nearest' });
      }
    }

    function cleanupThemedSelects() {
      qa('.themed-select-menu[data-select-id]').forEach(function (menu) {
        var select = doc.getElementById(menu.getAttribute('data-select-id') || '');
        if (!select || !select.isConnected) menu.remove();
      });
    }

    function enhanceSelect(select) {
      if (!select || select.dataset.themedSelectReady === '1') return;
      if (select.multiple || select.size > 1 || select.closest('.themed-select')) return;
      if (select.getAttribute('data-native-select') === 'true') return;

      var selectID = ensureID(select, 'themed-native-select');
      var menuID = selectID + '-menu';
      var wrap = doc.createElement('div');
      wrap.className = 'themed-select';
      if (select.style && select.style.width === 'auto') wrap.classList.add('themed-select-inline');
      wrap.setAttribute('data-themed-menu-id', menuID);

      var trigger = doc.createElement('button');
      trigger.type = 'button';
      trigger.id = selectID + '-trigger';
      trigger.className = 'themed-select-trigger input';
      trigger.setAttribute('aria-haspopup', 'listbox');
      trigger.setAttribute('aria-expanded', 'false');
      trigger.setAttribute('aria-controls', menuID);
      var labelText = Array.prototype.map.call(select.labels || [], function (label) {
        return String(label.textContent || '').trim();
      }).filter(Boolean).join(' ');
      trigger.setAttribute('aria-label', labelText || select.getAttribute('aria-label') || select.name || 'Select');
      if (select.getAttribute('style')) trigger.setAttribute('style', select.getAttribute('style'));

      var value = doc.createElement('span');
      value.className = 'themed-select-value';
      trigger.appendChild(value);

      var menu = doc.createElement('div');
      menu.id = menuID;
      menu.className = 'themed-select-menu hidden';
      menu.setAttribute('role', 'listbox');
      menu.setAttribute('data-select-id', selectID);

      select.parentNode.insertBefore(wrap, select);
      wrap.appendChild(select);
      wrap.appendChild(trigger);
      doc.body.appendChild(menu);
      select.dataset.themedSelectReady = '1';
      select.classList.add('themed-select-native');
      select.setAttribute('aria-hidden', 'true');
      select.tabIndex = -1;

      renderOptions(wrap);

      Array.prototype.forEach.call(select.labels || [], function (label) {
        label.addEventListener('click', function (event) {
          event.preventDefault();
          event.stopPropagation();
          trigger.focus();
          openThemedSelect(wrap);
        });
      });

      trigger.addEventListener('click', function (event) {
        event.preventDefault();
        event.stopPropagation();
        if (wrap.classList.contains('open')) closeThemedSelect(wrap);
        else openThemedSelect(wrap);
      });

      trigger.addEventListener('keydown', function (event) {
        if (event.key === 'Escape') {
          closeThemedSelect(wrap);
          return;
        }
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          event.preventDefault();
          if (!wrap.classList.contains('open')) openThemedSelect(wrap);
          focusOptionByDelta(wrap, event.key === 'ArrowDown' ? 1 : -1);
          return;
        }
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          if (!wrap.classList.contains('open')) {
            openThemedSelect(wrap);
          } else {
            selectThemedOption(wrap, highlightedOption(wrap));
          }
        }
      });

      menu.addEventListener('click', function (event) {
        var item = event.target && event.target.closest ? event.target.closest('.themed-select-option') : null;
        if (!item || !menu.contains(item)) return;
        event.preventDefault();
        selectThemedOption(wrap, item);
      });

      menu.addEventListener('mousemove', function (event) {
        var item = event.target && event.target.closest ? event.target.closest('.themed-select-option') : null;
        if (!item || item.disabled) return;
        qa('.themed-select-option', menu).forEach(function (opt) { opt.classList.toggle('is-highlighted', opt === item); });
      });

      select.addEventListener('change', function () { syncThemedSelect(wrap); });
      if (window.MutationObserver) {
        var observer = new MutationObserver(function () { renderOptions(wrap); });
        observer.observe(select, { childList: true, subtree: true, characterData: true, attributes: true });
      }
    }

    function enhanceThemedSelects(root) {
      cleanupThemedSelects();
      root = root || doc;
      if (root.nodeType === 1 && root.matches && root.matches('select.input')) enhanceSelect(root);
      qa('select.input', root).forEach(enhanceSelect);
    }

    doc.addEventListener('click', function (event) {
      if (event.target && event.target.closest && event.target.closest('.themed-select')) return;
      if (event.target && event.target.closest && event.target.closest('.themed-select-menu')) return;
      closeAllThemedSelects();
    });
    doc.addEventListener('keydown', function (event) {
      if (event.key === 'Escape') closeAllThemedSelects();
    });
    window.addEventListener('resize', function () { if (activeWrap) positionMenu(activeWrap); });
    window.addEventListener('scroll', function () { if (activeWrap) positionMenu(activeWrap); }, true);
    doc.addEventListener('htmx:afterSettle', function (event) {
      var elt = event.detail && event.detail.elt;
      enhanceThemedSelects(elt && elt.parentElement ? elt.parentElement : doc);
    });
    doc.addEventListener('igloo:i18n:changed', function () { enhanceThemedSelects(doc); });
    enhanceThemedSelects(doc);

    window.IglooThemedSelects = {
      init: enhanceThemedSelects,
      close: closeAllThemedSelects
    };
  })();

  // Skip-language pill widget — delegated so it works after HTMX swaps.
  // Handles autocomplete suggestions, pill add/remove, and syncing the
  // translation target dropdown with the user's skip list.
  (function initSkipLangPills() {
    var LANG_CODES = [
      'en', 'es', 'fr', 'de', 'it', 'pt', 'nl', 'ru', 'uk', 'zh', 'ja', 'ko',
      'ar', 'he', 'fa', 'hi', 'bn', 'ur', 'pa', 'ta', 'te', 'kn', 'ml', 'gu',
      'mr', 'si', 'ne', 'tr', 'az', 'kk', 'ky', 'uz', 'tk', 'mn', 'hy', 'ka',
      'th', 'lo', 'km', 'my', 'vi', 'id', 'ms', 'tl', 'jv', 'su', 'pl', 'cs',
      'sk', 'hu', 'ro', 'bg', 'sr', 'hr', 'bs', 'sl', 'mk', 'sq', 'el', 'mt',
      'fi', 'et', 'lv', 'lt', 'sv', 'no', 'da', 'is', 'fo', 'ga', 'gd', 'cy',
      'br', 'eu', 'ca', 'gl', 'oc', 'co', 'la', 'eo', 'be', 'tt', 'ba', 'cv',
      'sah', 'af', 'sw', 'am', 'ti', 'om', 'so', 'ha', 'yo', 'ig', 'zu', 'xh',
      'st', 'tn', 'rw', 'sn', 'mg', 'ny', 'haw', 'mi', 'sm', 'to', 'fj', 'yi',
      'dv', 'ps', 'sd', 'ku', 'ug', 'bo', 'dz', 'as', 'or'
    ];
    var LANG_CATALOG = [];
    var CODE_TO_NAME = {};
    var NAME_TO_CODE = {};

    function normalizeLanguageCode(raw) {
      return String(raw || '').trim().toLowerCase().replace(/_/g, '-');
    }

    function activeUILanguage() {
      return normalizeLanguageCode(
        (window.IglooI18n && window.IglooI18n.language) ||
        doc.documentElement.getAttribute('lang') ||
        'en'
      ) || 'en';
    }

    function makeDisplayNames(locale) {
      if (!window.Intl || typeof window.Intl.DisplayNames !== 'function') return null;
      try {
        return new window.Intl.DisplayNames([locale], { type: 'language' });
      } catch (e) {
        return null;
      }
    }

    function nameForCode(code, localDisplay, englishDisplay) {
      var normalized = normalizeLanguageCode(code);
      if (!normalized) return '';
      var localName = localDisplay && typeof localDisplay.of === 'function' ? localDisplay.of(normalized) : '';
      if (localName && normalizeLanguageCode(localName) !== normalized) return localName;
      var englishName = englishDisplay && typeof englishDisplay.of === 'function' ? englishDisplay.of(normalized) : '';
      if (englishName && normalizeLanguageCode(englishName) !== normalized) return englishName;
      return normalized;
    }

    function indexLanguageAlias(alias, code) {
      var key = String(alias || '').trim().toLowerCase();
      if (!key || NAME_TO_CODE[key]) return;
      NAME_TO_CODE[key] = code;
    }

    function rebuildLanguageCatalog() {
      LANG_CATALOG = [];
      CODE_TO_NAME = {};
      NAME_TO_CODE = {};
      var localDisplay = makeDisplayNames(activeUILanguage());
      var englishDisplay = makeDisplayNames('en');
      LANG_CODES.forEach(function (code) {
        var normalized = normalizeLanguageCode(code);
        if (!normalized) return;
        var name = nameForCode(normalized, localDisplay, englishDisplay);
        CODE_TO_NAME[normalized] = name;
        indexLanguageAlias(normalized, normalized);
        indexLanguageAlias(name, normalized);
        if (englishDisplay && typeof englishDisplay.of === 'function') {
          indexLanguageAlias(englishDisplay.of(normalized), normalized);
        }
        LANG_CATALOG.push({ c: normalized, n: name });
      });
    }

    rebuildLanguageCatalog();

    function labelForCode(code) {
      var normalized = normalizeLanguageCode(code);
      if (!normalized) return String(code || '');
      var name = CODE_TO_NAME[normalized];
      if (!name || normalizeLanguageCode(name) === normalized) return normalized;
      return name + ' (' + normalized + ')';
    }

    function resolveInput(raw) {
      var original = String(raw || '').trim();
      if (!original) return '';
      var normalized = normalizeLanguageCode(original);
      if (CODE_TO_NAME[normalized]) return normalized;
      var byName = NAME_TO_CODE[original.toLowerCase()];
      if (byName) return byName;
      // Accept free-form codes (2-6 chars, letters and hyphens) even if unknown.
      if (/^[a-z]{2,3}(-[a-z0-9]{2,4})?$/.test(normalized)) return normalized;
      return '';
    }

    function syncHidden(widget) {
      var codes = [];
      widget.querySelectorAll('.skip-lang-pill').forEach(function (p) {
        var c = (p.getAttribute('data-code') || '').trim();
        if (c) codes.push(c);
      });
      var hidden = widget.querySelector('.skip-lang-hidden');
      if (hidden) hidden.value = codes.join(',');
      syncTargetDropdown(codes);
    }

    function syncTargetDropdown(skipCodes) {
      var sel = doc.getElementById('global-setting-translate-target');
      if (!sel) return;
      var current = sel.value || 'en';
      var wanted = ['en'];
      skipCodes.forEach(function (c) { if (wanted.indexOf(c) === -1) wanted.push(c); });
      if (wanted.indexOf(current) === -1) wanted.push(current);
      // Remove options not in wanted.
      Array.prototype.slice.call(sel.options).forEach(function (opt) {
        if (wanted.indexOf(opt.value) === -1) sel.removeChild(opt);
      });
      // Add missing options, preserving order.
      wanted.forEach(function (code) {
        var exists = false;
        for (var i = 0; i < sel.options.length; i++) {
          if (sel.options[i].value === code) { exists = true; break; }
        }
        if (!exists) {
          var opt = doc.createElement('option');
          opt.value = code;
          opt.textContent = labelForCode(code);
          sel.appendChild(opt);
        }
      });
      // Update all labels (codes may have been beautified after initial SSR).
      Array.prototype.slice.call(sel.options).forEach(function (opt) {
        opt.textContent = labelForCode(opt.value);
      });
      if (current && Array.prototype.slice.call(sel.options).some(function (o) { return o.value === current; })) {
        sel.value = current;
      }
    }

    function beautifyPill(pill) {
      var code = (pill.getAttribute('data-code') || '').trim();
      if (!code) return;
      // Replace first text node with beautified label, keep the remove button.
      var btn = pill.querySelector('.skip-lang-remove');
      pill.textContent = '';
      pill.appendChild(doc.createTextNode(labelForCode(code) + ' '));
      if (btn) pill.appendChild(btn);
      else {
        var b = doc.createElement('button');
        b.type = 'button';
        b.className = 'skip-lang-remove';
        b.setAttribute('aria-label', t('action_remove', 'Remove'));
        b.style.cssText = 'background:none; border:0; color:var(--text-secondary); cursor:pointer; padding:0; line-height:1;';
        b.textContent = '×';
        pill.appendChild(b);
      }
    }

    function addCode(widget, raw) {
      var code = resolveInput(raw);
      if (!code) return;
      var exists = false;
      widget.querySelectorAll('.skip-lang-pill').forEach(function (p) {
        if ((p.getAttribute('data-code') || '').trim() === code) exists = true;
      });
      if (exists) return;
      var pills = widget.querySelector('.skip-lang-pills');
      if (!pills) return;
      var pill = doc.createElement('span');
      pill.className = 'skip-lang-pill';
      pill.setAttribute('data-code', code);
      pill.style.cssText = 'display:inline-flex; align-items:center; gap:0.35rem; padding:0.2rem 0.55rem; background:var(--bg-tertiary); border-radius:12px; font-size:0.8rem;';
      pill.appendChild(doc.createTextNode(labelForCode(code) + ' '));
      var btn = doc.createElement('button');
      btn.type = 'button';
      btn.className = 'skip-lang-remove';
      btn.setAttribute('aria-label', t('action_remove', 'Remove'));
      btn.style.cssText = 'background:none; border:0; color:var(--text-secondary); cursor:pointer; padding:0; line-height:1;';
      btn.textContent = '×';
      pill.appendChild(btn);
      pills.appendChild(pill);
      syncHidden(widget);
    }

    function renderSuggestions(widget, query) {
      var list = widget.querySelector('.skip-lang-suggestions');
      if (!list) return;
      var q = String(query || '').trim().toLowerCase();
      var already = {};
      widget.querySelectorAll('.skip-lang-pill').forEach(function (p) {
        already[(p.getAttribute('data-code') || '').trim()] = true;
      });
      var matches = [];
      LANG_CATALOG.forEach(function (e) {
        if (already[e.c]) return;
        if (!q) { matches.push(e); return; }
        var nl = e.n.toLowerCase();
        if (e.c.indexOf(q) === 0 || nl.indexOf(q) === 0 || nl.indexOf(' ' + q) >= 0) matches.push(e);
      });
      matches = matches.slice(0, 10);
      list.innerHTML = '';
      if (!matches.length) { list.classList.add('hidden'); return; }
      matches.forEach(function (e, i) {
        var li = doc.createElement('li');
        li.className = 'skip-lang-suggestion';
        li.setAttribute('data-code', e.c);
        li.style.cssText = 'padding:0.4rem 0.75rem; cursor:pointer; font-size:0.85rem;' + (i === 0 ? ' background:var(--bg-tertiary);' : '');
        li.textContent = labelForCode(e.c);
        list.appendChild(li);
      });
      list.classList.remove('hidden');
    }

    function hideSuggestions(widget) {
      var list = widget.querySelector('.skip-lang-suggestions');
      if (list) list.classList.add('hidden');
    }

    function moveActive(widget, dir) {
      var list = widget.querySelector('.skip-lang-suggestions');
      if (!list || list.classList.contains('hidden')) return;
      var items = list.querySelectorAll('.skip-lang-suggestion');
      if (!items.length) return;
      var activeIdx = -1;
      items.forEach(function (li, i) {
        if (li.style.background) activeIdx = i;
        li.style.background = '';
      });
      var next = activeIdx + dir;
      if (next < 0) next = items.length - 1;
      if (next >= items.length) next = 0;
      items[next].style.background = 'var(--bg-tertiary)';
      items[next].scrollIntoView({ block: 'nearest' });
    }

    function getActive(widget) {
      var list = widget.querySelector('.skip-lang-suggestions');
      if (!list || list.classList.contains('hidden')) return null;
      var items = list.querySelectorAll('.skip-lang-suggestion');
      for (var i = 0; i < items.length; i++) {
        if (items[i].style.background) return items[i];
      }
      return items[0] || null;
    }

    doc.addEventListener('click', function (e) {
      var sugg = e.target.closest && e.target.closest('.skip-lang-suggestion');
      if (sugg) {
        var w = sugg.closest('#translate-skip-langs-widget');
        if (!w) return;
        var code = sugg.getAttribute('data-code') || '';
        addCode(w, code);
        var input = w.querySelector('.skip-lang-input');
        if (input) { input.value = ''; input.focus(); }
        hideSuggestions(w);
        return;
      }
      var addBtn = e.target.closest && e.target.closest('.skip-lang-add');
      if (addBtn) {
        var widget = addBtn.closest('#translate-skip-langs-widget');
        if (!widget) return;
        var inp = widget.querySelector('.skip-lang-input');
        if (!inp) return;
        addCode(widget, inp.value);
        inp.value = '';
        hideSuggestions(widget);
        inp.focus();
        return;
      }
      var rmBtn = e.target.closest && e.target.closest('.skip-lang-remove');
      if (rmBtn) {
        var widget2 = rmBtn.closest('#translate-skip-langs-widget');
        var pill = rmBtn.closest('.skip-lang-pill');
        if (!widget2 || !pill) return;
        pill.remove();
        syncHidden(widget2);
        return;
      }
      // Click outside widget closes suggestions.
      var anyWidget = doc.getElementById('translate-skip-langs-widget');
      if (anyWidget && !anyWidget.contains(e.target)) hideSuggestions(anyWidget);
    });

    doc.addEventListener('input', function (e) {
      if (!e.target || !e.target.classList || !e.target.classList.contains('skip-lang-input')) return;
      var widget = e.target.closest('#translate-skip-langs-widget');
      if (!widget) return;
      renderSuggestions(widget, e.target.value);
    });

    doc.addEventListener('focus', function (e) {
      if (!e.target || !e.target.classList || !e.target.classList.contains('skip-lang-input')) return;
      var widget = e.target.closest('#translate-skip-langs-widget');
      if (!widget) return;
      renderSuggestions(widget, e.target.value);
    }, true);

    doc.addEventListener('keydown', function (e) {
      if (!e.target || !e.target.classList || !e.target.classList.contains('skip-lang-input')) return;
      var widget = e.target.closest('#translate-skip-langs-widget');
      if (!widget) return;
      if (e.key === 'ArrowDown') { e.preventDefault(); moveActive(widget, 1); return; }
      if (e.key === 'ArrowUp') { e.preventDefault(); moveActive(widget, -1); return; }
      if (e.key === 'Escape') { hideSuggestions(widget); return; }
      if (e.key === 'Enter') {
        e.preventDefault();
        var active = getActive(widget);
        if (active) {
          addCode(widget, active.getAttribute('data-code') || '');
        } else {
          addCode(widget, e.target.value);
        }
        e.target.value = '';
        hideSuggestions(widget);
      }
    });

    // On initial render (prefs body freshly swapped in), beautify SSR'd pills
    // and the target dropdown labels. Observe the prefs body for swap-ins.
    function beautifyWidget() {
      var widget = doc.getElementById('translate-skip-langs-widget');
      if (!widget) return;
      widget.querySelectorAll('.skip-lang-pill').forEach(beautifyPill);
      var codes = [];
      widget.querySelectorAll('.skip-lang-pill').forEach(function (p) {
        var c = (p.getAttribute('data-code') || '').trim();
        if (c) codes.push(c);
      });
      syncTargetDropdown(codes);
    }
    doc.addEventListener('htmx:afterSettle', function (e) {
      if (e.target && e.target.id === 'prefs-body') beautifyWidget();
    });
    doc.addEventListener('igloo:i18n:changed', function () {
      rebuildLanguageCatalog();
      beautifyWidget();
    });
    beautifyWidget();
  })();


  // Prefs tab switching — event delegation on the modal (tabs are HTMX-loaded)
  function switchPrefsTab(target) {
    var tabs = qa('.prefs-tab[data-prefs-tab]');
    tabs.forEach(function (t) { t.classList.toggle('active', t.getAttribute('data-prefs-tab') === target); });
    qa('.prefs-tab-panel').forEach(function (p) {
      p.style.display = p.getAttribute('data-prefs-panel') === target ? '' : 'none';
    });
    var shortcutsSubcats = q('#shortcuts-subcats');
    if (shortcutsSubcats) {
      shortcutsSubcats.style.display = target === 'shortcuts' ? '' : 'none';
    }
    if (target === 'shortcuts') initShortcutsTab();
  }

  function switchShortcutsSub(target) {
    qa('.prefs-subtab').forEach(function (s) { s.classList.toggle('active', s.getAttribute('data-shortcuts-sub') === target); });
    qa('.shortcuts-sub-panel').forEach(function (p) {
      p.style.display = p.getAttribute('data-shortcuts-panel') === target ? '' : 'none';
    });
  }

  if (prefsModal) {
    prefsModal.addEventListener('click', function (e) {
      var tab = e.target.closest('.prefs-tab[data-prefs-tab]');
      if (tab) {
        switchPrefsTab(tab.getAttribute('data-prefs-tab'));
        return;
      }
      var sub = e.target.closest('.prefs-subtab[data-shortcuts-sub]');
      if (sub) {
        switchShortcutsSub(sub.getAttribute('data-shortcuts-sub'));
        return;
      }
    });
  }

  // Up/Down arrow keys to navigate prefs tabs (and shortcuts sub-tabs)
  doc.addEventListener('keydown', function (e) {
    if (!prefsModal || prefsModal.classList.contains('hidden')) return;
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return;
    var tag = (e.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return;
    e.preventDefault();
    var dir = e.key === 'ArrowDown' ? 1 : -1;

    // If shortcuts tab is active and has sub-tabs, navigate sub-tabs
    var activeTab = q('.prefs-tab.active');
    if (activeTab && activeTab.getAttribute('data-prefs-tab') === 'shortcuts') {
      var subs = qa('.prefs-subtab[data-shortcuts-sub]').map(function (s) { return s.getAttribute('data-shortcuts-sub'); });
      var activeSub = q('.prefs-subtab.active');
      var subIdx = activeSub ? subs.indexOf(activeSub.getAttribute('data-shortcuts-sub')) : 0;
      var nextSub = subIdx + dir;
      if (nextSub >= 0 && nextSub < subs.length) {
        switchShortcutsSub(subs[nextSub]);
        return;
      }
      // Past first/last sub-tab: fall through to navigate main tabs
    }

    // Navigate main tabs
    var tabs = qa('.prefs-tab[data-prefs-tab]').map(function (t) { return t.getAttribute('data-prefs-tab'); });
    var curTab = activeTab ? activeTab.getAttribute('data-prefs-tab') : tabs[0];
    var idx = tabs.indexOf(curTab);
    var next = idx + dir;
    if (next >= 0 && next < tabs.length) switchPrefsTab(tabs[next]);
  });

  // Shortcuts tab: populate, edit, reset
  var navShortcutsPopulated = false;
  var scListening = null; // { row, kbd, id } when capturing a key

  function initShortcutsTab() {
    // Populate nav shortcuts once
    if (!navShortcutsPopulated) {
      navShortcutsPopulated = true;
      var container = q('#shortcuts-nav-list');
      if (container) {
        var navItems = qa('.sidebar .nav .nav-item');
        navItems.forEach(function (item, i) {
          if (i > 8) return;
          var row = doc.createElement('div');
          row.className = 'shortcut-row';
          var kbd1 = doc.createElement('kbd');
          kbd1.textContent = (window.IglooI18n && window.IglooI18n.messages && window.IglooI18n.messages.shortcuts_key_ctrl) || 'Ctrl';
          var kbd2 = doc.createElement('kbd');
          kbd2.textContent = String(i + 1);
          var span = doc.createElement('span');
          span.textContent = item.textContent.trim();
          row.appendChild(kbd1);
          row.appendChild(doc.createTextNode(' + '));
          row.appendChild(kbd2);
          row.appendChild(span);
          container.appendChild(row);
        });
      }
    }
    // Refresh all editable shortcut displays
    refreshShortcutKbds();
  }

  function refreshShortcutKbds() {
    qa('.shortcut-row[data-sc]').forEach(function (row) {
      var id = row.getAttribute('data-sc');
      var kbd = row.querySelector('kbd');
      if (!kbd) return;
      var key = window.cfShortcuts.key(id);
      kbd.textContent = window.cfShortcuts.displayKey(key);
      kbd.classList.toggle('sc-custom', window.cfShortcuts.isCustom(id));
    });
    // Update title attributes that embed a shortcut key
    qa('[data-sc-title]').forEach(function (el) {
      var id = el.getAttribute('data-sc-title');
      var tpl = el.getAttribute('data-sc-title-template') || '';
      var key = window.cfShortcuts.displayKey(window.cfShortcuts.key(id));
      el.title = tpl.replace('{key}', key);
    });
  }

  function cancelListening() {
    if (!scListening) return;
    scListening.kbd.classList.remove('sc-listening');
    scListening = null;
  }

  // Click a kbd in an editable row to start listening
  doc.addEventListener('click', function (e) {
    var kbd = e.target.closest('.shortcut-row[data-sc] kbd');
    if (!kbd) { cancelListening(); return; }
    var row = kbd.closest('.shortcut-row[data-sc]');
    if (!row) return;
    e.preventDefault();
    cancelListening();
    var id = row.getAttribute('data-sc');
    kbd.textContent = '\u2026';
    kbd.classList.add('sc-listening');
    scListening = { row: row, kbd: kbd, id: id };
  });

  // Capture key when listening
  doc.addEventListener('keydown', function (e) {
    if (!scListening) return;
    e.preventDefault();
    e.stopPropagation();
    if (e.key === 'Escape') { // cancel
      scListening.kbd.classList.remove('sc-listening');
      var id = scListening.id;
      scListening.kbd.textContent = window.cfShortcuts.displayKey(window.cfShortcuts.key(id));
      scListening = null;
      return;
    }
    // Ignore lone modifiers
    if (['Control', 'Shift', 'Alt', 'Meta'].indexOf(e.key) >= 0) return;
    var newKey = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    window.cfShortcuts.set(scListening.id, newKey);
    scListening.kbd.classList.remove('sc-listening');
    scListening = null;
    refreshShortcutKbds();
  }, true);

  // Reset all button
  var resetBtn = q('#shortcuts-reset-btn');
  if (resetBtn) {
    resetBtn.addEventListener('click', function () {
      window.cfShortcuts.resetAll();
      refreshShortcutKbds();
    });
  }

  // Global Ctrl+Number navigation shortcuts
  doc.addEventListener('keydown', function (event) {
    if (!event.ctrlKey || event.shiftKey || event.altKey || event.metaKey) return;
    var digit = parseInt(event.key, 10);
    if (isNaN(digit) || digit < 1 || digit > 9) return;
    var navItems = qa('.sidebar .nav .nav-item');
    var target = navItems[digit - 1];
    if (!target) return;
    event.preventDefault();
    var href = target.getAttribute('href');
    if (href) window.location.href = href;
  });

  // Global shortcut to toggle logs modal (default "O")
  doc.addEventListener('keydown', function (event) {
    if (event.ctrlKey || event.shiftKey || event.altKey || event.metaKey) return;
    var tag = (event.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return;
    if (event.target.isContentEditable) return;
    if (!window.cfShortcuts.match('global.logs', event.key)) return;
    if (!logsModal) return;
    if (logsModal.classList.contains('hidden') && q('.modal:not(.hidden)')) return;
    event.preventDefault();
    toggleLogsModal();
  });

  // -- Zen-style centered search overlay (default "H") --
  var searchOverlay = q('#search-overlay');
  var searchOverlayInput = q('#search-overlay-input');
  var searchOverlayResults = q('#search-overlay-results');
  var searchOverlayClear = q('#search-overlay-clear');
  var _soDebounce = null;
  var _soActiveIdx = -1;

  // Map platform keys from the server ("youtube"/"twitter"/"tiktok") to the
  // proper brand labels shown in the search overlay.
  function platformLabel(p) {
    var k = (p || 'youtube').toLowerCase();
    if (k === 'youtube') return t('platform_youtube', 'YouTube');
    if (k === 'tiktok') return t('platform_tiktok', 'TikTok');
    if (k === 'instagram') return t('platform_instagram', 'Instagram');
    if (k === 'twitter' || k === 'x') return t('platform_x', 'X');
    return k;
  }

  function searchChannelIdentityHtml(ch) {
    var name = escapeHtml(ch.name || ch.channel_id || '');
    var rawHandle = String((ch && ch.handle) || '').trim().replace(/^@+/, '');
    var handle = rawHandle ? '<span class="sdi-handle">@' + escapeHtml(rawHandle) + '</span>' : '';
    return '<span class="sdi-identity"><span class="sdi-name">' + name + '</span>' + handle + '</span>';
  }

  function openSearchOverlay() {
    if (!searchOverlay) return;
    searchOverlay.classList.remove('hidden');
    if (searchOverlayInput) {
      searchOverlayInput.value = '';
      searchOverlayInput.focus();
    }
    if (searchOverlayResults) setHtml(searchOverlayResults, '');
    if (searchOverlayClear) searchOverlayClear.classList.add('hidden');
    _soActiveIdx = -1;
  }

  function closeSearchOverlay() {
    if (!searchOverlay) return;
    searchOverlay.classList.add('hidden');
    if (searchOverlayInput) searchOverlayInput.blur();
    if (searchOverlayResults) setHtml(searchOverlayResults, '');
    _soActiveIdx = -1;
  }

  function renderOverlayResults(channels, videos) {
    if (!searchOverlayResults) return;
    var html = '';
    var idx = 0;
    var noResultsSearch = t('search_no_results_press_enter_to_search_youtube');
    var pressEnterSearch = t('search_press_enter_to_search_youtube');
    var channelsLabel = t('nav_channels');
    var videosLabel = t('nav_videos');
    if (channels.length === 0 && videos.length === 0) {
      html = '<div class="search-dropdown-footer" style="border-top:none;margin-top:0;">' + escapeHtml(noResultsSearch) + '</div>';
    } else {
      if (channels.length > 0) {
        html += '<div class="search-dropdown-section">' + escapeHtml(channelsLabel) + '</div>';
        for (var i = 0; i < channels.length; i++) {
          var ch = channels[i];
          var plat = escapeHtml(platformLabel(ch.platform));
          var avatarUrl = ch.channel_id ? '/api/media/avatar/' + encodeURIComponent(ch.channel_id) : '';
          var avatarHtml = avatarUrl
            ? '<img class="sdi-avatar" src="' + escapeHtml(avatarUrl) + '" alt="" onload="window.MpaSiteBase&&window.MpaSiteBase.avatarLoad(this)" onerror="if(window.MpaSiteBase&&window.MpaSiteBase.avatarError(this))return;this.style.display=\'none\';this.nextElementSibling.style.display=\'flex\'"><div class="sdi-avatar-fallback" style="display:none">' + escapeHtml((ch.name || '?').charAt(0).toUpperCase()) + '</div>'
            : '<div class="sdi-avatar-fallback">' + escapeHtml((ch.name || '?').charAt(0).toUpperCase()) + '</div>';
          html += '<a class="search-dropdown-item" href="/channels/' + encodeURIComponent(ch.channel_id) + '" data-idx="' + idx + '">'
            + avatarHtml
            + searchChannelIdentityHtml(ch)
            + '<span class="sdi-platform">' + plat + '</span>'
            + '</a>';
          idx++;
        }
      }
      if (videos.length > 0) {
        html += '<div class="search-dropdown-section">' + escapeHtml(videosLabel) + '</div>';
        for (var j = 0; j < videos.length; j++) {
          var v = videos[j];
          var vTitle = escapeHtml(v.title || v.video_id || '');
          var vChannel = escapeHtml(v.channel_name || '');
          var thumbUrl = v.thumbnail_url || '';
          var dur = formatDuration(v.duration);
          var thumbHtml = thumbUrl
            ? '<img class="sdi-thumb" src="' + escapeHtml(thumbUrl) + '" alt="" onerror="this.style.display=\'none\'">'
            : '';
          html += '<a class="search-dropdown-item" href="/player/' + encodeURIComponent(v.video_id) + '" data-idx="' + idx + '">'
            + thumbHtml
            + '<span class="sdi-name">' + vTitle + '</span>'
            + (vChannel ? '<span class="sdi-platform">' + vChannel + '</span>' : '')
            + (dur ? '<span class="sdi-dur">' + dur + '</span>' : '')
            + '</a>';
          idx++;
        }
      }
      html += '<div class="search-dropdown-footer">' + escapeHtml(pressEnterSearch) + '</div>';
    }
    setHtml(searchOverlayResults, html);
    _soActiveIdx = -1;
  }

  function moveOverlayActive(delta) {
    var items = searchOverlayResults ? qa('.search-dropdown-item', searchOverlayResults) : [];
    if (!items.length) return;
    items.forEach(function (el) { el.classList.remove('active'); });
    _soActiveIdx += delta;
    if (_soActiveIdx < -1) _soActiveIdx = items.length - 1;
    if (_soActiveIdx >= items.length) _soActiveIdx = -1;
    if (_soActiveIdx >= 0) {
      items[_soActiveIdx].classList.add('active');
      items[_soActiveIdx].scrollIntoView({ block: 'nearest' });
    }
  }

  // Open overlay via shortcut
  doc.addEventListener('keydown', function (event) {
    if (event.ctrlKey || event.shiftKey || event.altKey || event.metaKey) return;
    var tag = (event.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return;
    if (event.target.isContentEditable) return;
    if (!window.cfShortcuts.match('global.search', event.key)) return;
    // Don't open if another modal is open
    if (q('.modal:not(.hidden)')) return;
    if (searchOverlay && !searchOverlay.classList.contains('hidden')) return;
    event.preventDefault();
    openSearchOverlay();
  });

  // Close on backdrop click
  if (searchOverlay) {
    searchOverlay.addEventListener('mousedown', function (e) {
      if (e.target === searchOverlay) closeSearchOverlay();
    });
  }

  // Input + keyboard handling
  if (searchOverlayInput) {
    searchOverlayInput.addEventListener('input', function () {
      clearTimeout(_soDebounce);
      var term = searchOverlayInput.value.trim();
      if (searchOverlayClear) searchOverlayClear.classList.toggle('hidden', term.length < 1);
      if (term.length < 1) {
        if (searchOverlayResults) setHtml(searchOverlayResults, '');
        _soActiveIdx = -1;
        return;
      }
      _soDebounce = setTimeout(function () {
        apiJson('/api/search/suggest?q=' + encodeURIComponent(term) + '&channel_limit=5&video_limit=5')
          .then(function (data) {
            if (searchOverlayInput.value.trim() === term) {
              renderOverlayResults(data.channels || [], data.youtube_videos || []);
            }
          })
          .catch(function () {
            if (searchOverlayResults) setHtml(searchOverlayResults, '');
          });
      }, 250);
    });

    searchOverlayInput.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown') { e.preventDefault(); moveOverlayActive(1); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); moveOverlayActive(-1); }
      else if (e.key === 'Escape') { e.preventDefault(); closeSearchOverlay(); }
      else if (e.key === 'Enter') {
        e.preventDefault();
        var items = searchOverlayResults ? qa('.search-dropdown-item', searchOverlayResults) : [];
        if (_soActiveIdx >= 0 && _soActiveIdx < items.length) {
          window.location.href = items[_soActiveIdx].getAttribute('href');
        } else {
          var term = searchOverlayInput.value.trim();
          if (term) window.location.href = '/search/youtube?q=' + encodeURIComponent(term);
        }
      }
    });
  }

  if (searchOverlayClear) {
    searchOverlayClear.addEventListener('click', function () {
      if (searchOverlayInput) {
        searchOverlayInput.value = '';
        searchOverlayInput.focus();
      }
      if (searchOverlayResults) setHtml(searchOverlayResults, '');
      searchOverlayClear.classList.add('hidden');
      _soActiveIdx = -1;
    });
  }

  // Preferences modal — form rendering + save now HTMX-driven.
  // Only the open handler remains (to trigger the modal).
  if (prefsBtn && prefsModal) {
    prefsBtn.addEventListener('click', function () {
      openModal(prefsModal);
    });
  }

  // ── Export / Import ───────────────────────────────────────────────
  function configuredBackupFolder() {
    var input = q('#global-setting-backup-dir');
    return !!(input && String(input.defaultValue || '').trim());
  }

  function triggerDownload(url) {
    var a = doc.createElement('a');
    a.href = url;
    a.download = '';
    a.style.display = 'none';
    doc.body.appendChild(a);
    a.click();
    a.remove();
  }

  function downloadBlob(blob, filename) {
    var url = URL.createObjectURL(blob);
    var a = doc.createElement('a');
    a.href = url;
    a.download = filename || 'igloo-export';
    a.style.display = 'none';
    doc.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
  }

  function filenameFromDisposition(disposition) {
    var match = /filename="?([^";]+)"?/i.exec(disposition || '');
    return match ? match[1] : '';
  }

  function runConfiguredExport(btn) {
    var url = btn && btn.getAttribute('data-config-export-url');
    if (!url) return;
    if (!configuredBackupFolder()) {
      triggerDownload(url);
      return;
    }
    btn.disabled = true;
    showPrefsStatus('info', translate('status_export_saving', 'Saving export...'));
    fetch(url, { credentials: 'same-origin' })
      .then(function (resp) {
        if (!resp.ok) throw new Error('export failed');
        var disposition = resp.headers.get('Content-Disposition') || '';
        if (/attachment/i.test(disposition)) {
          return resp.blob().then(function (blob) {
            downloadBlob(blob, filenameFromDisposition(disposition));
            return { downloaded: true };
          });
        }
        return resp.json();
      })
      .then(function (data) {
        if (data && data.downloaded) return;
        var text = translate('status_export_saved_to', 'Export saved to %1$s', [data && data.path || '']);
        showPrefsStatus('success', text);
        clearPrefsStatusLater(text);
      })
      .catch(function () {
        showPrefsStatus('error', translate('status_export_failed', 'Export failed'));
      })
      .finally(function () {
        btn.disabled = false;
      });
  }

  doc.addEventListener('click', function (event) {
    var btn = event.target && event.target.closest && event.target.closest('[data-config-export-url]');
    if (!btn) return;
    event.preventDefault();
    runConfiguredExport(btn);
  });

  var importConfigBtn = q('#import-config-btn');
  var importConfigFile = q('#import-config-file');
  var importConfigModal = q('#import-config-modal');
  var importConfigFilename = q('#import-config-filename');

  if (importConfigBtn && importConfigFile) {
    importConfigBtn.addEventListener('click', function () {
      importConfigFile.value = '';
      importConfigFile.click();
    });
    importConfigFile.addEventListener('change', function () {
      if (!importConfigFile.files || !importConfigFile.files[0]) return;
      if (importConfigFilename) importConfigFilename.textContent = t('label_file_colon', 'File:') + ' ' + importConfigFile.files[0].name;
      if (importConfigModal) openModal(importConfigModal);
    });
  }

  // Channel settings popover — form loaded via HTMX, only open/close/position in JS
  const settingsModal = q('#channel-settings-popover');
  const settingsChannelName = q('#settings-channel-name');
  const settingsBody = q('#channel-settings-body');
  window._csToast = function () { showToast(t('toast_channel_settings_saved', 'Channel settings saved')); };

  function closeChannelSettingsPopover() {
    if (settingsModal) settingsModal.classList.add('hidden');
  }

  function openChannelSettingsModal(meta) {
    if (!settingsModal || !settingsBody) return;
    var channelId = String(meta && meta.channelId || '').trim();
    var channelName = String(meta && meta.channelName || meta && meta.channelId || t('label_channel', 'Channel'));
    var platform = String(meta && meta.platform || '').trim();
    if (!channelId) return;
    if (settingsChannelName) settingsChannelName.textContent = channelName;

    // Position popover near the triggering element
    var anchorRow = meta && meta.anchorEl;
    if (anchorRow) {
      var rect = anchorRow.getBoundingClientRect();
      var modalWidth = 280;
      var gap = 8;
      var top = rect.bottom + 8;
      var maxTop = window.innerHeight - 360;
      if (top > maxTop) top = maxTop;
      if (top < 8) top = 8;
      settingsModal.style.top = top + 'px';
      var left = rect.right - modalWidth;
      var minLeft = Math.max(8, rect.left - modalWidth + rect.width + gap);
      if (left < minLeft) left = minLeft;
      var maxLeft = window.innerWidth - modalWidth - 8;
      if (left > maxLeft) left = maxLeft;
      if (left < 8) left = 8;
      settingsModal.style.left = left + 'px';
    } else {
      settingsModal.style.top = '80px';
      settingsModal.style.left = 'calc(var(--sidebar-width) + 8px)';
    }

    // Load form via HTMX
    settingsBody.innerHTML = '<div style="padding:1rem;color:var(--text-secondary);">' + escapeHtml(t('status_loading_ellipsis', 'Loading...')) + '</div>';
    settingsModal.classList.remove('hidden');
    htmx.ajax('GET', '/api/channels/' + encodeURIComponent(channelId) + '/settings?platform=' + encodeURIComponent(platform), {target: settingsBody, swap: 'innerHTML'});
  }

  // Close popover on outside click
  doc.addEventListener('mousedown', function (e) {
    if (!settingsModal || settingsModal.classList.contains('hidden')) return;
    if (!settingsModal.contains(e.target)) closeChannelSettingsPopover();
  });

  // Close button
  var closePopoverBtn = q('#channel-settings-close');
  if (closePopoverBtn) {
    closePopoverBtn.addEventListener('click', closeChannelSettingsPopover);
  }

  // Sidebar groups + channel actions
  const channelList = q('#channel-list');
  const groupStoreKeyPrefix = 'mpa-sidebar-group:';

  function groupStorageKey(groupId) {
    return groupStoreKeyPrefix + String(groupId || '');
  }

  function setGroupCollapsed(group, collapsed) {
    if (!group) return;
    group.classList.toggle('collapsed', !!collapsed);
    const groupId = group.getAttribute('data-group-id');
    if (groupId) {
      try {
        localStorage.setItem(groupStorageKey(groupId), collapsed ? '1' : '0');
      } catch (_) {}
    }
  }

  function restoreGroupCollapsedState() {
    qa('.channel-group[data-group-id]', channelList || doc).forEach(function (group) {
      const groupId = group.getAttribute('data-group-id');
      if (!groupId) return;
      let stored = null;
      try {
        stored = localStorage.getItem(groupStorageKey(groupId));
      } catch (_) {
        stored = null;
      }
      if (stored == null) return;
      group.classList.toggle('collapsed', stored === '1');
    });
  }

  // Channel actions — star, refresh, remove now HTMX-driven on the buttons.
  // Only settings remains JS (opens popover modal).
  function handleChannelAction(btn, row) {
    if (!btn || !row) return;
    const channelId = String(btn.getAttribute('data-id') || row.getAttribute('data-channel-id') || '').trim();
    const action = String(btn.getAttribute('data-action') || '').trim();
    const channelName = String(row.getAttribute('data-channel-name') || channelId || 'channel');
    const platform = String(row.getAttribute('data-channel-platform') || '').trim();
    if (!channelId || !action) return;

    if (action === 'settings') {
      openChannelSettingsModal({ channelId: channelId, channelName: channelName, platform: platform, anchorEl: row });
    }
  }

  function loadSidebarGroup(group) {
    if (!group || group.getAttribute('data-sidebar-deferred') !== '1') return Promise.resolve(group);
    if (group._sidebarLoadPromise) return group._sidebarLoadPromise;
    const list = q('[data-sidebar-group-items]', group);
    const url = String(group.getAttribute('data-sidebar-load-url') || '').trim();
    if (!list || !url) return Promise.resolve(group);
    group.setAttribute('data-sidebar-loading', '1');
    group._sidebarLoadPromise = fetch(url, {
      credentials: 'same-origin',
      headers: csrfToken ? { 'X-CSRF-Token': csrfToken } : {}
    }).then(function (response) {
      if (response.status === 401) {
        window.location.href = '/login';
        throw new Error('Unauthorized');
      }
      if (!response.ok) throw new Error('HTTP ' + response.status);
      return response.text();
    }).then(function (html) {
      list.innerHTML = html;
      group.removeAttribute('data-sidebar-deferred');
      group.removeAttribute('data-sidebar-load-url');
      group.setAttribute('data-sidebar-loaded', '1');
      if (window.htmx && typeof window.htmx.process === 'function') window.htmx.process(list);
      applyI18nAttributes(list);
      return group;
    }).catch(function (err) {
      showToast(translate('error_load_channels_failed', 'Failed to load channels'));
      throw err;
    }).finally(function () {
      group.removeAttribute('data-sidebar-loading');
      group._sidebarLoadPromise = null;
    });
    return group._sidebarLoadPromise;
  }

  function loadDeferredSidebarGroups() {
    if (!channelList) return Promise.resolve();
    var groups = qa('.channel-group[data-sidebar-deferred="1"]', channelList);
    if (!groups.length) return Promise.resolve();
    return Promise.all(groups.map(loadSidebarGroup));
  }

  if (channelList) {
    restoreGroupCollapsedState();
    qa('.channel-group[data-sidebar-deferred="1"]:not(.collapsed)', channelList).forEach(function (group) {
      loadSidebarGroup(group).catch(function () {});
    });

    channelList.addEventListener('click', function (event) {
      const target = event.target;

      const platformRefreshBtn = target && target.closest ? target.closest('.nav-refresh-btn') : null;
      if (platformRefreshBtn) {
        return;
      }

      const groupHeader = target && target.closest ? target.closest('[data-channel-group-toggle]') : null;
      if (groupHeader) {
        const group = groupHeader.closest('.channel-group');
        if (group) {
          const willOpen = group.classList.contains('collapsed');
          setGroupCollapsed(group, !willOpen);
          if (willOpen) loadSidebarGroup(group).catch(function () {});
        }
        return;
      }

      const actionBtn = target && target.closest ? target.closest('.channel-btn') : null;
      if (actionBtn) {
        event.preventDefault();
        event.stopPropagation();
        const row = actionBtn.closest('.channel-item[data-channel-id], .channel-row[data-channel-id]');
        handleChannelAction(actionBtn, row);
        return;
      }

      const rowActionBtn = target && target.closest ? target.closest('.channel-action-btn') : null;
      if (rowActionBtn) {
        event.preventDefault();
        event.stopPropagation();
        const row = rowActionBtn.closest('.channel-row[data-channel-id], .channel-item[data-channel-id]');
        handleChannelAction(rowActionBtn, row);
        return;
      }
    });
  }

  // Platform search filter
  var platformSearchInput = q('#platform-search');
  if (platformSearchInput && channelList) {
    var preSearchCollapsed = new Map();
    var prevSearchTerm = '';

    function applyPlatformSearchFilter(term) {
      if (term !== '' && prevSearchTerm === '') {
        qa('.channel-group[data-group-id]', channelList).forEach(function (group) {
          preSearchCollapsed.set(group.getAttribute('data-group-id'), group.classList.contains('collapsed'));
        });
      }

      if (term === '') {
        qa('.channel-group[data-group-id]', channelList).forEach(function (group) {
          var groupId = group.getAttribute('data-group-id');
          var wasCollapsed = preSearchCollapsed.has(groupId) ? preSearchCollapsed.get(groupId) : group.classList.contains('collapsed');
          group.classList.toggle('collapsed', wasCollapsed);
          qa('.channel-item', group).forEach(function (item) { item.style.display = ''; });
          group.style.display = '';
        });
        preSearchCollapsed.clear();
      } else {
        qa('.channel-group[data-group-id]', channelList).forEach(function (group) {
          var hasMatch = false;
          qa('.channel-item', group).forEach(function (item) {
            var name = (item.getAttribute('data-channel-name') || '').toLowerCase();
            // Handle match lets Latin queries find Japanese/unicode display
            // names (e.g. typing "mirai" hits "@sampleHandle" → "Example records - Example display").
            // YouTube channels without a populated handle silently skip the handle
            // check — the name check above still applies.
            var handle = (item.getAttribute('data-channel-handle') || '').toLowerCase();
            var matches = name.includes(term) || (handle !== '' && handle.includes(term));
            item.style.display = matches ? '' : 'none';
            if (matches) hasMatch = true;
          });
          group.classList.remove('collapsed');
          group.style.display = hasMatch ? '' : 'none';
        });
      }

      prevSearchTerm = term;

      // Toggle clear button
      var clearBtn = q('.input-clear-btn[data-clear-for="platform-search"]');
      if (clearBtn) clearBtn.classList.toggle('hidden', term === '');
    }

    platformSearchInput.addEventListener('input', function () {
      var term = this.value.trim().toLowerCase();
      if (term !== '') {
        loadDeferredSidebarGroups().then(function () {
          if (platformSearchInput.value.trim().toLowerCase() === term) applyPlatformSearchFilter(term);
        }).catch(function () {});
      } else {
        applyPlatformSearchFilter(term);
      }
    });
  }

  // Clear buttons for clearable inputs
  doc.addEventListener('click', function (e) {
    var clearBtn = e.target.closest('.input-clear-btn[data-clear-for]');
    if (!clearBtn) return;
    var inputId = clearBtn.getAttribute('data-clear-for');
    var input = q('#' + inputId);
    if (input) {
      input.value = '';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.focus();
    }
    clearBtn.classList.add('hidden');
  });

  doc.addEventListener('click', function (event) {
    const rowActionBtn = event.target && event.target.closest ? event.target.closest('.channel-action-btn') : null;
    if (rowActionBtn && (!channelList || !channelList.contains(rowActionBtn))) {
      event.preventDefault();
      event.stopPropagation();
      const row = rowActionBtn.closest('.channel-row[data-channel-id], .channel-item[data-channel-id]');
      handleChannelAction(rowActionBtn, row);
      return;
    }

    const btn = event.target && event.target.closest ? event.target.closest('.js-open-channel-settings') : null;
    if (!btn) return;
    event.preventDefault();
    openChannelSettingsModal({
      channelId: btn.getAttribute('data-channel-id'),
      channelName: btn.getAttribute('data-channel-name'),
      platform: btn.getAttribute('data-channel-platform'),
      anchorEl: btn
    });
  });

  // Channel page unsubscribe button
  // Channel page unsubscribe — now HTMX-driven

  var _confirmModal = q('#confirm-modal');
  var _confirmTitle = q('#confirm-title');
  var _confirmBody = q('#confirm-body');
  var _confirmActions = q('#confirm-actions');

  function askConfirm(opts) {
    var title = String((opts && opts.title) || t('action_confirm', 'Confirm')).trim() || t('action_confirm', 'Confirm');
    var body = String((opts && opts.body) || '').trim();
    var confirmLabel = String((opts && opts.confirmLabel) || t('action_confirm', 'Confirm')).trim() || t('action_confirm', 'Confirm');
    var cancelLabel = String((opts && opts.cancelLabel) || t('action_cancel', 'Cancel')).trim() || t('action_cancel', 'Cancel');
    var danger = !opts || opts.danger !== false;

    if (!_confirmModal || !_confirmTitle || !_confirmBody || !_confirmActions) {
      return Promise.resolve(window.confirm(body || title));
    }

    return new Promise(function (resolve) {
      var settled = false;
      var onDismissClick = null;
      var onDismissKey = null;
      function cleanup() {
        if (onDismissClick) _confirmModal.removeEventListener('click', onDismissClick, true);
        if (onDismissKey) document.removeEventListener('keydown', onDismissKey, true);
        onDismissClick = null;
        onDismissKey = null;
      }
      function done(value) {
        if (settled) return;
        settled = true;
        cleanup();
        closeModal(_confirmModal);
        resolve(!!value);
      }
      _confirmTitle.textContent = title;
      _confirmBody.textContent = body;
      while (_confirmActions.firstChild) _confirmActions.removeChild(_confirmActions.firstChild);

      var cancelBtn = document.createElement('button');
      cancelBtn.type = 'button';
      cancelBtn.className = 'btn btn-secondary';
      cancelBtn.textContent = cancelLabel;
      cancelBtn.addEventListener('click', function () { done(false); });

      var confirmBtn = document.createElement('button');
      confirmBtn.type = 'button';
      confirmBtn.className = danger ? 'btn btn-danger' : 'btn btn-primary';
      confirmBtn.textContent = confirmLabel;
      confirmBtn.addEventListener('click', function () { done(true); });

      _confirmActions.appendChild(cancelBtn);
      _confirmActions.appendChild(confirmBtn);

      onDismissClick = function (event) {
        var closeBtn = event.target && event.target.closest ? event.target.closest('.modal-close') : null;
        var backdrop = event.target && event.target.classList && event.target.classList.contains('modal-backdrop');
        if (closeBtn || backdrop) done(false);
      };
      onDismissKey = function (event) {
        if (event.key === 'Escape') done(false);
      };
      _confirmModal.addEventListener('click', onDismissClick, true);
      document.addEventListener('keydown', onDismissKey, true);
      openModal(_confirmModal);
      window.setTimeout(function () {
        try { confirmBtn.focus(); } catch (_) { }
      }, 0);
    });
  }

  // -- Header Global Search Typeahead --
  var globalSearchInput = q('#global-search-input');
  var searchDropdown = q('#search-dropdown');
  var _searchDebounce = null;
  var _searchActiveIdx = -1;

  function formatDuration(secs) {
    if (!secs || secs <= 0) return '';
    var m = Math.floor(secs / 60), s = secs % 60;
    return m + ':' + (s < 10 ? '0' : '') + s;
  }

  function renderSearchDropdown(channels, videos) {
    if (!searchDropdown) return;
    var html = '';
    var idx = 0;
    var noResultsSearch = t('search_no_results_press_enter_to_search_youtube');
    var pressEnterSearch = t('search_press_enter_to_search_youtube');
    var channelsLabel = t('nav_channels');
    var videosLabel = t('nav_videos');
    if (channels.length === 0 && videos.length === 0) {
      html = '<div class="search-dropdown-footer" style="border-top:none;margin-top:0;">' + escapeHtml(noResultsSearch) + '</div>';
    } else {
      if (channels.length > 0) {
        html += '<div class="search-dropdown-section">' + escapeHtml(channelsLabel) + '</div>';
        for (var i = 0; i < channels.length; i++) {
          var ch = channels[i];
          var plat = escapeHtml(platformLabel(ch.platform));
          var avatarUrl = ch.channel_id ? '/api/media/avatar/' + encodeURIComponent(ch.channel_id) : '';
          var avatarHtml = avatarUrl
            ? '<img class="sdi-avatar" src="' + escapeHtml(avatarUrl) + '" alt="" onload="window.MpaSiteBase&&window.MpaSiteBase.avatarLoad(this)" onerror="if(window.MpaSiteBase&&window.MpaSiteBase.avatarError(this))return;this.style.display=\'none\';this.nextElementSibling.style.display=\'flex\'"><div class="sdi-avatar-fallback" style="display:none">' + escapeHtml((ch.name || '?').charAt(0).toUpperCase()) + '</div>'
            : '<div class="sdi-avatar-fallback">' + escapeHtml((ch.name || '?').charAt(0).toUpperCase()) + '</div>';
          html += '<a class="search-dropdown-item" href="/channels/' + encodeURIComponent(ch.channel_id) + '" data-idx="' + idx + '">'
            + avatarHtml
            + searchChannelIdentityHtml(ch)
            + '<span class="sdi-platform">' + plat + '</span>'
            + '</a>';
          idx++;
        }
      }
      if (videos.length > 0) {
        html += '<div class="search-dropdown-section">' + escapeHtml(videosLabel) + '</div>';
        for (var j = 0; j < videos.length; j++) {
          var v = videos[j];
          var vTitle = escapeHtml(v.title || v.video_id || '');
          var vChannel = escapeHtml(v.channel_name || '');
          var thumbUrl = v.thumbnail_url || '';
          var dur = formatDuration(v.duration);
          var thumbHtml = thumbUrl
            ? '<img class="sdi-thumb" src="' + escapeHtml(thumbUrl) + '" alt="" onerror="this.style.display=\'none\'">'
            : '';
          html += '<a class="search-dropdown-item" href="/player/' + encodeURIComponent(v.video_id) + '" data-idx="' + idx + '">'
            + thumbHtml
            + '<span class="sdi-name">' + vTitle + '</span>'
            + (vChannel ? '<span class="sdi-platform">' + vChannel + '</span>' : '')
            + (dur ? '<span class="sdi-dur">' + dur + '</span>' : '')
            + '</a>';
          idx++;
        }
      }
      html += '<div class="search-dropdown-footer">' + escapeHtml(pressEnterSearch) + '</div>';
    }
    setHtml(searchDropdown, html);
    searchDropdown.classList.remove('hidden');
    _searchActiveIdx = -1;
  }

  function hideSearchDropdown() {
    if (searchDropdown) searchDropdown.classList.add('hidden');
    _searchActiveIdx = -1;
  }

  function moveSearchActive(delta) {
    var items = searchDropdown ? qa('.search-dropdown-item', searchDropdown) : [];
    if (!items.length) return;
    items.forEach(function (el) { el.classList.remove('active'); });
    _searchActiveIdx += delta;
    if (_searchActiveIdx < -1) _searchActiveIdx = items.length - 1;
    if (_searchActiveIdx >= items.length) _searchActiveIdx = -1;
    if (_searchActiveIdx >= 0) items[_searchActiveIdx].classList.add('active');
  }

  var globalSearchClear = q('#global-search-clear');
  function toggleGlobalSearchClear() {
    if (globalSearchClear) globalSearchClear.classList.toggle('hidden', !globalSearchInput.value);
  }

  if (globalSearchClear) {
    globalSearchClear.addEventListener('click', function () {
      globalSearchInput.value = '';
      toggleGlobalSearchClear();
      hideSearchDropdown();
      globalSearchInput.focus();
    });
  }

  if (globalSearchInput) {
    toggleGlobalSearchClear();

    globalSearchInput.addEventListener('focus', function () {
      var hs = q('#header-search');
      if (hs) hs.classList.add('is-expanded');
    });

    globalSearchInput.addEventListener('blur', function () {
      setTimeout(function () {
        var hs = q('#header-search');
        if (hs) hs.classList.remove('is-expanded');
        hideSearchDropdown();
      }, 200);
    });

    globalSearchInput.addEventListener('input', function () {
      clearTimeout(_searchDebounce);
      toggleGlobalSearchClear();
      var term = globalSearchInput.value.trim();
      if (term.length < 1) { hideSearchDropdown(); return; }
      _searchDebounce = setTimeout(function () {
        apiJson('/api/search/suggest?q=' + encodeURIComponent(term) + '&channel_limit=5&video_limit=5')
          .then(function (data) {
            if (globalSearchInput.value.trim() === term) {
              renderSearchDropdown(data.channels || [], data.youtube_videos || [], term);
            }
          })
          .catch(function () { hideSearchDropdown(); });
      }, 250);
    });

    globalSearchInput.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown') { e.preventDefault(); moveSearchActive(1); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); moveSearchActive(-1); }
      else if (e.key === 'Escape') { hideSearchDropdown(); globalSearchInput.blur(); }
      else if (e.key === 'Enter') {
        e.preventDefault();
        var items = searchDropdown ? qa('.search-dropdown-item', searchDropdown) : [];
        if (_searchActiveIdx >= 0 && _searchActiveIdx < items.length) {
          window.location.href = items[_searchActiveIdx].getAttribute('href');
        } else {
          var term = globalSearchInput.value.trim();
          if (term) window.location.href = '/search/youtube?q=' + encodeURIComponent(term);
        }
      }
    });
  }

  // -- Search Results Page: Platform Filters --
  var platformFilters = qa('.platform-filter');
  if (platformFilters.length) {
    function applyPlatformFilter(platform) {
      platformFilters.forEach(function (btn) {
        btn.classList.toggle('active', btn.getAttribute('data-platform') === platform);
      });
      qa('.search-section').forEach(function (sec) {
        if (platform === 'all') {
          sec.classList.remove('search-hidden');
        } else {
          sec.classList.toggle('search-hidden', sec.getAttribute('data-platform') !== platform);
        }
      });
    }

    platformFilters.forEach(function (btn) {
      btn.addEventListener('click', function () {
        var plat = btn.getAttribute('data-platform');
        applyPlatformFilter(plat);
        history.replaceState(null, '', plat === 'all' ? location.pathname + location.search : location.pathname + location.search + '#' + plat);
      });
    });

    // Restore from hash on load
    var hashPlat = (location.hash || '').replace('#', '');
    if (hashPlat && qa('.platform-filter[data-platform="' + hashPlat + '"]').length) {
      applyPlatformFilter(hashPlat);
    }
  }

  // -- Channels/Creators Tab Search Filter --
  var channelTabSearch = q('#channel-tab-search');
  var channelTabCount = q('#channel-tab-search-count');

  if (channelTabSearch) {
    var _channelFilterTimer = null;

    channelTabSearch.addEventListener('input', function () {
      clearTimeout(_channelFilterTimer);
      _channelFilterTimer = setTimeout(function () {
        var term = channelTabSearch.value.trim().toLowerCase();
        var rows = qa('.channel-row');
        var visibleCount = 0;

        rows.forEach(function (row) {
          if (!term) {
            row.classList.remove('search-hidden');
            qa('.video-card', row).forEach(function (card) { card.classList.remove('search-hidden'); });
            visibleCount++;
            return;
          }

          var channelName = (row.getAttribute('data-channel-name') || '').toLowerCase();
          var channelMatch = channelName.indexOf(term) !== -1;

          if (channelMatch) {
            row.classList.remove('search-hidden');
            qa('.video-card', row).forEach(function (card) { card.classList.remove('search-hidden'); });
            visibleCount++;
            return;
          }

          // Check video titles
          var cards = qa('.video-card', row);
          var anyVideoMatch = false;
          cards.forEach(function (card) {
            var title = (card.getAttribute('data-video-title') || '').toLowerCase();
            if (title.indexOf(term) !== -1) {
              card.classList.remove('search-hidden');
              anyVideoMatch = true;
            } else {
              card.classList.add('search-hidden');
            }
          });

          if (anyVideoMatch) {
            row.classList.remove('search-hidden');
            visibleCount++;
          } else {
            row.classList.add('search-hidden');
          }
        });

        if (channelTabCount) {
          setText(
            channelTabCount,
            term
              ? translate(
                visibleCount === 1 ? 'search_channels_found_single' : 'search_channels_found_many',
                visibleCount === 1 ? '%1$s channel found' : '%1$s channels found',
                [String(visibleCount)],
              )
              : '',
          );
        }
      }, 150);
    });
  }

  window.MpaSiteBase = {
    openChannelSettingsModal: openChannelSettingsModal,
    openRsshubDiagnosticsModal: openRsshubDiagnosticsModal,
    openModal: openModal,
    closeModal: closeModal,
    showToast: showToast,
    t: t,
    apiJson: apiJson,
    askConfirm: askConfirm,
    copyText: copyText,
    avatarLoad: avatarLoad,
    avatarError: avatarError,
    pollNow: function () { schedulePoll(); },
    _toggleLogsModal: toggleLogsModal,
    // Internal helpers for extracted dashboard files
    _h: {
      q: q, qa: qa, setHtml: setHtml, escapeHtml: escapeHtml, serverHMS: serverHMS, doc: doc,
      logsModal: function () { return logsModal; },
      logsActiveTab: function () { return logsActiveTab; },
      setTzOffset: function (v) { _serverTzOffsetSec = v; },
    },
  };

  // Route all hx-confirm dialogs through the site modal instead of window.confirm.
  // issueRequest(true) skips HTMX's built-in window.confirm check.
  doc.addEventListener('htmx:confirm', function (e) {
    var question = e.detail.question;
    if (!question) return;
    e.preventDefault();
    askConfirm({ title: t('action_confirm', 'Confirm'), body: question, confirmLabel: t('action_confirm', 'Confirm'), danger: true })
      .then(function (ok) { if (ok) e.detail.issueRequest(true); });
  });

  // ── Lazy-load channel previews — extracted to channel_previews.js ──
  // ── Retweeters dialog — extracted to src/feed/retweeters.js ──
})();
