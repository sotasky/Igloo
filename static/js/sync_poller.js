/**
 * Unified interaction sync poller.
 *
 * Polls GET /api/sync/changes?since=N every 5s (visible) / 15s (hidden).
 * Dispatches changes to page-specific handlers.
 *
 * Usage:
 *   window.SyncPoller.on('like', function(itemId, value) { ... });
 *   window.SyncPoller.advance(newVersion);
 */
(function (win, doc) {
  'use strict';

  var localVersion = 0;
  var handlers = {};
  var pollTimer = 0;
  var pollInFlight = false;
  var apiJson = null;

  function pollDelay() {
    return doc.hidden ? 15000 : 5000;
  }

  function schedule(immediate) {
    clearTimeout(pollTimer);
    pollTimer = setTimeout(poll, immediate ? 100 : pollDelay());
  }

  function poll() {
    if (!apiJson || pollInFlight) { schedule(false); return; }
    pollInFlight = true;
    apiJson('/api/sync/changes?since=' + localVersion)
      .then(function (data) {
        if (!data) return;
        if (data.full_refresh_needed) {
          win.location.reload();
          return;
        }
        var changes = data.changes || [];
        for (var i = 0; i < changes.length; i++) {
          dispatch(changes[i]);
        }
        if (data.version) localVersion = data.version;
      })
      .catch(function () { /* retry next cycle */ })
      .finally(function () {
        pollInFlight = false;
        schedule(false);
      });
  }

  function dispatch(change) {
    var fns = handlers[change.type];
    if (!fns) return;
    for (var i = 0; i < fns.length; i++) {
      try { fns[i](change.item_id, change.value); }
      catch (e) { if (win.console) win.console.error('[SyncPoller]', e); }
    }
  }

  function on(type, fn) {
    if (!handlers[type]) handlers[type] = [];
    handlers[type].push(fn);
  }

  function advance(version) {
    if (typeof version === 'number' && version > localVersion) {
      localVersion = version;
    }
  }

  doc.addEventListener('visibilitychange', function () { schedule(true); });
  setTimeout(function () {
    if (!apiJson && win.MpaSiteBase) apiJson = win.MpaSiteBase.apiJson;
    if (apiJson) schedule(true);
  }, 500);

  win.SyncPoller = { on: on, advance: advance };
})(window, document);
