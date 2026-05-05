(function () {
  const doc = document;
  const supportsHover = !!(window.matchMedia && window.matchMedia('(hover: hover)').matches);

  var _previewMuted = true;

  const state = {
    hoverCard: null,
    hoverTimer: null,
    activePreviewCard: null,
    seekRaf: 0
  };

  var subtitleTracksByVideoId = Object.create(null);

  function q(sel, root) {
    return (root || doc).querySelector(sel);
  }

  function qa(sel, root) {
    return Array.prototype.slice.call((root || doc).querySelectorAll(sel));
  }

  function parseAppDate(raw) {
    if (raw === null || raw === undefined || raw === '' || raw === 0) return null;
    if (typeof raw === 'number' && Number.isFinite(raw)) {
      return raw > 0 ? new Date(raw) : null;
    }
    var s = String(raw).trim();
    if (!s) return null;
    if (/^-?\d+$/.test(s)) {
      var n = Number(s);
      return n > 0 ? new Date(n) : null;
    }
    if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/.test(s)) {
      var clean = s.replace(/\s+[+-]\d{4}\s+\w+$/, '');
      var t = Date.parse(clean.replace(' ', 'T') + 'Z');
      if (Number.isFinite(t)) return new Date(t);
    }
    var t2 = Date.parse(s);
    if (Number.isFinite(t2)) return new Date(t2);
    return null;
  }

  function i18nText(key, fallback) {
    var cfg = window.IglooI18n || {};
    var messages = cfg.messages || {};
    var value = messages[key];
    return value == null || value === '' ? String(fallback || key || '') : String(value);
  }

  function i18nFormat(key, fallback, value) {
    return i18nText(key, fallback).replace(/%1\$(?:d|s)/g, String(value));
  }

  function formatRelative(raw) {
    var d = parseAppDate(raw);
    if (!d) return String(raw || '');
    var sec = Math.round((Date.now() - d.getTime()) / 1000);
    var abs = Math.abs(sec);
    var future = sec < 0;
    if (abs < 60) return i18nFormat(future ? 'time_seconds_from_now' : 'time_seconds_ago', future ? '%1$ds from now' : '%1$ds ago', abs);
    var min = Math.round(abs / 60);
    if (min < 60) return i18nFormat(future ? 'time_minutes_from_now' : 'time_minutes_ago', future ? '%1$dm from now' : '%1$dm ago', min);
    var hrs = Math.round(min / 60);
    if (hrs < 24) return i18nFormat(future ? 'time_hours_from_now' : 'time_hours_ago', future ? '%1$dh from now' : '%1$dh ago', hrs);
    var days = Math.round(hrs / 24);
    if (days < 30) return i18nFormat(future ? 'time_days_from_now' : 'time_days_ago', future ? '%1$dd from now' : '%1$dd ago', days);
    var months = Math.round(days / 30);
    if (months < 12) return i18nFormat(future ? 'time_months_from_now' : 'time_months_ago', future ? '%1$dmo from now' : '%1$dmo ago', months);
    return i18nFormat(future ? 'time_years_from_now' : 'time_years_ago', future ? '%1$dy from now' : '%1$dy ago', Math.round(days / 365));
  }

  function formatAbsolute(raw) {
    var d = parseAppDate(raw);
    if (!d) return String(raw || '');
    try {
      return d.toLocaleString();
    } catch (_) {
      return String(raw || '');
    }
  }

  function hydrateCardDates(root) {
    qa('.video-card .video-date[data-video-date]', root || doc).forEach(function (el) {
      var raw = String(el.getAttribute('data-video-date') || '').trim();
      if (!raw) return;
      var rel = formatRelative(raw);
      var abs = formatAbsolute(raw);
      el.setAttribute('data-date-relative', rel);
      el.setAttribute('data-date-absolute', abs);
      if (!el.closest('.video-card:hover')) el.textContent = rel || raw;
      el.title = abs || raw;
    });
  }

  /* ── Preview controls ── */

  var SVG_MUTED = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 5L6 9H2v6h4l5 4V5z"/><line x1="23" y1="9" x2="17" y2="15"/><line x1="17" y1="9" x2="23" y2="15"/></svg>';
  var SVG_UNMUTED = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 5L6 9H2v6h4l5 4V5z"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/><path d="M15.54 8.46a5 5 0 0 1 0 7.07"/></svg>';

  function updateMuteBtn(btn, muted) {
    // Static SVG speaker icons that inherit currentColor from the theme
    btn.innerHTML = muted ? SVG_MUTED : SVG_UNMUTED;
    btn.title = muted
      ? i18nText('action_unmute', 'Unmute')
      : i18nText('action_mute', 'Mute');
  }

  function updateSeekbar(card) {
    var video = q('.video-hover-preview', card);
    var fill = q('.video-preview-seekbar-fill', card);
    if (!video || !fill) return;
    var pct = (video.duration && Number.isFinite(video.duration))
      ? (video.currentTime / video.duration) * 100
      : 0;
    fill.style.width = pct + '%';
    if (card.classList.contains('is-previewing')) {
      state.seekRaf = requestAnimationFrame(function () { updateSeekbar(card); });
    }
  }

  function createPreviewControls(thumb, card, video) {
    // Mute button
    var muteBtn = q('.video-preview-mute-btn', thumb);
    if (!muteBtn) {
      muteBtn = doc.createElement('button');
      muteBtn.className = 'video-preview-mute-btn';
      muteBtn.type = 'button';
      updateMuteBtn(muteBtn, video.muted);
      thumb.appendChild(muteBtn);
    }

    // Seekbar
    var seekbar = q('.video-preview-seekbar', thumb);
    if (!seekbar) {
      seekbar = doc.createElement('div');
      seekbar.className = 'video-preview-seekbar';
      var fill = doc.createElement('div');
      fill.className = 'video-preview-seekbar-fill';
      seekbar.appendChild(fill);
      thumb.appendChild(seekbar);
    }

    // Subtitle track (YouTube only)
    var platform = String(card.getAttribute('data-platform') || '').toLowerCase();
    if (platform === 'youtube' || platform === '') {
      var videoId = String(card.getAttribute('data-video-id') || '').trim();
      ensureManualSubtitleTrack(card, video, videoId);
    }
  }

  function fetchSubtitleTracks(videoId) {
    if (!subtitleTracksByVideoId[videoId]) {
      subtitleTracksByVideoId[videoId] = fetch('/api/videos/' + encodeURIComponent(videoId) + '/subtitles')
        .then(function (res) {
          if (!res.ok) return [];
          return res.json();
        })
        .then(function (payload) {
          return Array.isArray(payload && payload.tracks) ? payload.tracks : [];
        })
        .catch(function () { return []; });
    }
    return subtitleTracksByVideoId[videoId];
  }

  function ensureManualSubtitleTrack(card, video, videoId) {
    if (!videoId || q('track', video) || video.getAttribute('data-subtitle-loading') === videoId) return;
    video.setAttribute('data-subtitle-loading', videoId);
    fetchSubtitleTracks(videoId).then(function (tracks) {
      if (q('track', video)) return;
      var manualTrack = tracks.find(function (track) { return track && !track.is_auto; });
      if (!manualTrack) return;
      var track = doc.createElement('track');
      track.kind = 'subtitles';
      track.label = manualTrack.label || i18nText('language_english', 'English');
      track.srclang = manualTrack.srclang || 'en';
      track.src = '/api/media/subtitle/' + encodeURIComponent(videoId) + '?track=' + encodeURIComponent(manualTrack.track_id || '');
      track.default = true;
      track.addEventListener('load', function () {
        if (state.activePreviewCard === card && video.textTracks && video.textTracks.length > 0) {
          video.textTracks[0].mode = 'showing';
        }
      });
      video.appendChild(track);
    });
  }

  function stopPreview(card) {
    var target = card || state.activePreviewCard;
    if (!target) return;
    var video = q('.video-hover-preview', target);
    target.classList.remove('is-previewing');
    if (video) {
      try { video.pause(); } catch (_) {}
      // Hide subtitles while paused
      if (video.textTracks && video.textTracks.length > 0) {
        video.textTracks[0].mode = 'hidden';
      }
    }
    cancelAnimationFrame(state.seekRaf);
    if (state.activePreviewCard === target) state.activePreviewCard = null;
  }

  function maybeStartPreview(card) {
    if (!supportsHover || !card) return;
    var thumb = q('.video-thumbnail', card);
    var src = String(card.getAttribute('data-stream-url') || '').trim();
    if (!thumb || !src) return;

    var video = q('.video-hover-preview', thumb);
    if (!video) {
      video = doc.createElement('video');
      video.className = 'video-hover-preview';
      video.muted = _previewMuted;
      video.loop = true;
      video.playsInline = true;
      video.preload = 'metadata';
      video.crossOrigin = 'anonymous';
      video.setAttribute('playsinline', '');
      if (_previewMuted) video.setAttribute('muted', '');
      video.src = src;
      video.addEventListener('error', function () {
        stopPreview(card);
      });
      thumb.insertBefore(video, thumb.firstChild);
    } else {
      // Sync mute state with global preference
      video.muted = _previewMuted;
      if (_previewMuted) video.setAttribute('muted', '');
      else video.removeAttribute('muted');
    }

    createPreviewControls(thumb, card, video);

    // Update mute button to reflect current state
    var muteBtn = q('.video-preview-mute-btn', thumb);
    if (muteBtn) updateMuteBtn(muteBtn, video.muted);

    if (state.activePreviewCard && state.activePreviewCard !== card) {
      stopPreview(state.activePreviewCard);
    }
    state.activePreviewCard = card;
    card.classList.add('is-previewing');

    // Start seekbar updates
    updateSeekbar(card);

    // Enable subtitle track if present
    if (video.textTracks && video.textTracks.length > 0) {
      video.textTracks[0].mode = 'showing';
    }

    try {
      // Browsers block unmuted autoplay — start muted, then unmute after play succeeds
      var wantUnmuted = !_previewMuted;
      if (wantUnmuted) {
        video.muted = true;
        video.setAttribute('muted', '');
      }
      var p = video.play();
      if (p && typeof p.then === 'function') {
        p.then(function () {
          if (wantUnmuted) {
            video.muted = false;
            video.removeAttribute('muted');
            if (muteBtn) updateMuteBtn(muteBtn, false);
          }
        }).catch(function () { stopPreview(card); });
      }
    } catch (_) {
      stopPreview(card);
    }
  }

  function formatViewCount(n) {
    if (!n) return '';
    n = parseInt(n, 10);
    if (isNaN(n)) return '';
    if (n >= 1e6) {
      return i18nFormat('video_views_count', '%1$s views', (n / 1e6).toFixed(1).replace(/\.0$/, '') + 'M');
    }
    if (n >= 1e3) {
      return i18nFormat('video_views_count', '%1$s views', Math.round(n / 1e3) + 'K');
    }
    return i18nFormat('video_views_count', '%1$s views', n);
  }

  function setHoverDate(card, hovered) {
    var el = q('.video-date[data-video-date]', card);
    if (!el) return;
    var rel = String(el.getAttribute('data-date-relative') || '').trim();
    var views = formatViewCount(el.getAttribute('data-view-count'));
    if (hovered && views) el.textContent = views;
    else if (rel) el.textContent = rel;
  }

  function onCardEnter(card) {
    if (!card) return;
    state.hoverCard = card;
    clearTimeout(state.hoverTimer);
    setHoverDate(card, true);
    state.hoverTimer = setTimeout(function () {
      if (state.hoverCard === card) maybeStartPreview(card);
    }, 260);
  }

  function onCardLeave(card) {
    if (!card) return;
    if (state.hoverCard === card) state.hoverCard = null;
    clearTimeout(state.hoverTimer);
    setHoverDate(card, false);
    stopPreview(card);
  }

  function onPreviewControlClick(e) {
    var muteBtn = e.target && e.target.closest ? e.target.closest('.video-preview-mute-btn') : null;
    var seekbar = e.target && e.target.closest ? e.target.closest('.video-preview-seekbar') : null;
    if (!muteBtn && !seekbar) return;

    e.preventDefault();
    e.stopPropagation();

    var card = e.target.closest('.video-card');
    if (!card) return;

    if (muteBtn) {
      var video = q('.video-hover-preview', card);
      if (!video) return;
      video.muted = !video.muted;
      _previewMuted = video.muted;
      updateMuteBtn(muteBtn, video.muted);
      // If unmuting, need to ensure playback (some browsers pause on unmute)
      if (!video.muted && video.paused) {
        try { video.play(); } catch (_) {}
      }
    }

    if (seekbar) {
      var video = q('.video-hover-preview', card);
      if (!video || !video.duration || !Number.isFinite(video.duration)) return;
      var rect = seekbar.getBoundingClientRect();
      var pct = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
      video.currentTime = pct * video.duration;
    }
  }

  function onPreviewControlMousedown(e) {
    // Prevent drag on seekbar/mute from propagating to card link
    var seekbar = e.target && e.target.closest ? e.target.closest('.video-preview-seekbar') : null;
    var muteBtn = e.target && e.target.closest ? e.target.closest('.video-preview-mute-btn') : null;
    if (seekbar || muteBtn) {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  function bind() {
    if (window.location && window.location.pathname === '/videos') {
      doc.body.classList.add('route-videos-tab');
    }
    if (window.location && /^\/channels(\/|$)/.test(window.location.pathname)) {
      doc.body.classList.add('route-channel-detail');
    }
    hydrateCardDates(doc);

    doc.addEventListener('mouseover', function (e) {
      if (!supportsHover) return;
      var card = e.target && e.target.closest ? e.target.closest('.video-card') : null;
      if (!card) return;
      var from = e.relatedTarget && e.relatedTarget.closest ? e.relatedTarget.closest('.video-card') : null;
      if (from === card) return;
      onCardEnter(card);
    });

    doc.addEventListener('mouseout', function (e) {
      if (!supportsHover) return;
      var card = e.target && e.target.closest ? e.target.closest('.video-card') : null;
      if (!card) return;
      var to = e.relatedTarget && e.relatedTarget.closest ? e.relatedTarget.closest('.video-card') : null;
      if (to === card) return;
      onCardLeave(card);
    });

    doc.addEventListener('click', function (e) {
      // Preview controls take priority
      var previewCtrl = e.target && e.target.closest
        ? (e.target.closest('.video-preview-mute-btn') || e.target.closest('.video-preview-seekbar'))
        : null;
      if (previewCtrl) {
        onPreviewControlClick(e);
        return;
      }

      // For YouTube videos being previewed, append ?t= to resume from hover position
      var card = e.target && e.target.closest ? e.target.closest('.video-card') : null;
      if (card && card.classList.contains('is-previewing')) {
        var platform = String(card.getAttribute('data-platform') || '').toLowerCase();
        if (platform === 'youtube' || platform === '') {
          var video = q('.video-hover-preview', card);
          if (video && video.currentTime > 1 && Number.isFinite(video.currentTime)) {
            var href = card.getAttribute('data-original-href') || card.getAttribute('href') || '';
            if (href) {
              // Store original href for re-hover scenarios
              if (!card.getAttribute('data-original-href')) {
                card.setAttribute('data-original-href', href);
              }
              var sep = href.indexOf('?') >= 0 ? '&' : '?';
              card.setAttribute('href', href + sep + 't=' + Math.floor(video.currentTime));
            }
          }
        }
      }
    });

    // Prevent mousedown on preview controls from triggering navigation
    doc.addEventListener('mousedown', onPreviewControlMousedown);

    doc.addEventListener('mpa:infinite-append', function () {
      hydrateCardDates(doc);
    });
  }

  if (doc.readyState === 'loading') {
    doc.addEventListener('DOMContentLoaded', bind, { once: true });
  } else {
    bind();
  }
})();
