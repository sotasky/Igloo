// Shorts playback — video and slideshow playback, progress bar, mute, autoplay.

var _state = null
var _goNext = null

export function initPlayback(stateRef, goNextFn) {
  _state = stateRef
  _goNext = goNextFn
}

function currentData() {
  if (_state.currentIndex < 0 || _state.currentIndex >= _state.items.length) return null
  return _state.items[_state.currentIndex] ? _state.items[_state.currentIndex].data : null
}

function autoAdvanceEnabled() {
  return !!(_state && (_state.storyMode || _state.autoPlayNext))
}

export function syncRenderedShortVideoLoop() {
  var loop = !autoAdvanceEnabled()
  document.querySelectorAll('#shorts-container video').forEach(function (video) {
    video.loop = loop
  })
}

function slideshowSlides(slideshow) {
  return (slideshow && (slideshow.slides || slideshow.images)) || []
}

function isVideoSlide(slide) {
  return !!(slide && String(slide.dataset && slide.dataset.slideType || '').toLowerCase() === 'video')
}

function currentSlideshowSlide(slideshow) {
  var slides = slideshowSlides(slideshow)
  return slides[slideshow.index || 0] || null
}

function pauseSlideshowVideos(slideshow, exceptIndex) {
  slideshowSlides(slideshow).forEach(function (slide, idx) {
    if (!isVideoSlide(slide) || idx === exceptIndex) return
    try {
      slide.pause()
      slide.currentTime = 0
    } catch (_) { }
  })
}

export function pauseAllShorts(exceptId) {
  _state.items.forEach(function (entry) {
    var slideshow = entry && entry.refs && entry.refs.slideshow
    if (slideshow && slideshow.timer) {
      try { clearTimeout(slideshow.timer) } catch (_) { }
      slideshow.timer = 0
    }
    if (slideshow && slideshow.audio) {
      try { slideshow.audio.pause(); slideshow.audio.currentTime = 0 } catch (_) { }
    }
    if (slideshow) {
      slideshow.playing = false
      pauseSlideshowVideos(slideshow)
    }
    var video = entry && entry.refs && entry.refs.video
    if (!video) return
    if (exceptId && entry.data.id === exceptId) return
    try { video.pause() } catch (_) { }
  })
}

export function maybeMarkAspect(wrapper, video) {
  if (!wrapper || !video) return
  var w = Number(video.videoWidth || 0)
  var h = Number(video.videoHeight || 0)
  wrapper.classList.remove('is-vertical', 'is-wide')
  if (!w || !h) return
  if (h > w) wrapper.classList.add('is-vertical')
  else if (w > h * 1.2) wrapper.classList.add('is-wide')
}

export function setSlideshowIndex(entry, index) {
  var slideshow = entry && entry.refs && entry.refs.slideshow
  if (!slideshow || !slideshow.count) return
  var next = Math.max(0, parseInt(index, 10) || 0)
  if (next >= slideshow.count) next = slideshow.count - 1
  slideshow.index = next
  slideshowSlides(slideshow).forEach(function (slide, idx) {
    if (!slide) return
    slide.classList.toggle('active', idx === next)
    if (isVideoSlide(slide)) {
      slide.muted = !!(_state && _state.muted)
      if (idx !== next || !slideshow.playing) {
        try { slide.pause() } catch (_) { }
      }
    }
  })
  pauseSlideshowVideos(slideshow, next)
  ;(slideshow.dots || []).forEach(function (dot, idx) {
    if (dot) dot.classList.toggle('active', idx === next)
  })
  if (slideshow.counter) {
    slideshow.counter.textContent = String(next + 1) + ' / ' + String(slideshow.count)
  }
  var current = currentSlideshowSlide(slideshow)
  if (isVideoSlide(current) && slideshow.playing) {
    var p = current.play()
    if (p && typeof p.catch === 'function') p.catch(function () {})
  }
}

export function stepSlideshow(entry, delta) {
  var slideshow = entry && entry.refs && entry.refs.slideshow
  if (!slideshow || slideshow.count <= 1) return false
  var currentIdx = slideshow.index || 0
  var next = currentIdx + (delta > 0 ? 1 : -1)
  if (next < 0 || next >= slideshow.count) return false
  if (slideshow.timer) {
    try { clearTimeout(slideshow.timer) } catch (_) { }
    slideshow.timer = 0
  }
  setSlideshowIndex(entry, next)
  return true
}

export function startSlideshowPlayback(entry) {
  var slideshow = entry && entry.refs && entry.refs.slideshow
  if (!slideshow || !slideshow.count) return
  slideshow.playing = true
  if (slideshow.timer) {
    try { clearTimeout(slideshow.timer) } catch (_) { }
    slideshow.timer = 0
  }
  setSlideshowIndex(entry, slideshow.index || 0)
  var audio = slideshow.audio
  if (audio && audio.src) {
    audio.preload = 'auto'
    var ap = audio.play()
    if (ap && typeof ap.catch === 'function') ap.catch(function () {})
  }
  if (audio && !audio._endedWired) {
    audio._endedWired = true
    audio.addEventListener('ended', function () {
      if (!_state.overlayOpen) return
      var cur = currentData()
      if (!cur || cur.id !== entry.data.id) return
      if (autoAdvanceEnabled()) _goNext()
      else {
        try { audio.currentTime = 0; audio.play().catch(function () {}) } catch (_) { }
      }
    })
  }
  var activeSlide = currentSlideshowSlide(slideshow)
  if (isVideoSlide(activeSlide)) {
    if (audio) {
      try { audio.pause() } catch (_) { }
    }
    if (!activeSlide._endedWired) {
      activeSlide._endedWired = true
      activeSlide.addEventListener('ended', function () {
        if (!_state.overlayOpen) return
        var cur = currentData()
        if (!cur || cur.id !== entry.data.id) return
        var next = (slideshow.index || 0) + 1
        if (next >= slideshow.count) {
          if (autoAdvanceEnabled()) _goNext()
          else { setSlideshowIndex(entry, 0); startSlideshowPlayback(entry) }
          return
        }
        setSlideshowIndex(entry, next)
        startSlideshowPlayback(entry)
      })
    }
    activeSlide.muted = !!_state.muted
    var vp = activeSlide.play()
    if (vp && typeof vp.catch === 'function') vp.catch(function () {})
    return
  }
  if (slideshow.count <= 1) {
    if (!slideshow.audio || !slideshow.audio.src) {
      slideshow.timer = setTimeout(function () {
        slideshow.timer = 0
        if (!_state.overlayOpen) return
        var cur = currentData()
        if (!cur || cur.id !== entry.data.id) return
        if (autoAdvanceEnabled()) _goNext()
        else { setSlideshowIndex(entry, 0); startSlideshowPlayback(entry) }
      }, 5000)
    }
    return
  }
  slideshow.timer = setTimeout(function () {
    slideshow.timer = 0
    if (!_state.overlayOpen) return
    var current = currentData()
    if (!current || current.id !== entry.data.id) return
    var next = (slideshow.index || 0) + 1
    if (next >= slideshow.count) {
      if (autoAdvanceEnabled()) _goNext()
      else { setSlideshowIndex(entry, 0); startSlideshowPlayback(entry) }
      return
    }
    setSlideshowIndex(entry, next)
    startSlideshowPlayback(entry)
  }, 3200)
}

export function toggleShortPlayback(entry) {
  if (!entry || !entry.refs) return
  var video = entry.refs.video
  if (video) {
    if (video.paused) {
      var p = video.play()
      if (p && typeof p.catch === 'function') p.catch(function () { })
    } else {
      video.pause()
    }
    return
  }
  var slideshow = entry.refs.slideshow
  if (!slideshow || !slideshow.count) return
  var activeSlide = currentSlideshowSlide(slideshow)
  if (isVideoSlide(activeSlide)) {
    if (activeSlide.paused) {
      slideshow.playing = true
      activeSlide.muted = !!(_state && _state.muted)
      var vp = activeSlide.play()
      if (vp && typeof vp.catch === 'function') vp.catch(function () {})
    } else {
      slideshow.playing = false
      try { activeSlide.pause() } catch (_) { }
      if (slideshow.timer) { try { clearTimeout(slideshow.timer) } catch (_) { } slideshow.timer = 0 }
    }
    return
  }
  var audio = slideshow.audio
  if (audio && audio.src && !audio.paused) {
    slideshow.playing = false
    try { audio.pause() } catch (_) { }
    if (slideshow.timer) { try { clearTimeout(slideshow.timer) } catch (_) { } slideshow.timer = 0 }
    return
  }
  if (slideshow.count > 1) {
    var next = (slideshow.index || 0) + 1
    if (next >= slideshow.count) next = 0
    setSlideshowIndex(entry, next)
  }
  startSlideshowPlayback(entry)
}

export function handleVideoTimeUpdate(entry) {
  var video = entry && entry.refs && entry.refs.video
  var bar = entry && entry.refs && entry.refs.progressBar
  if (!video || !bar) return
  var dur = Number(video.duration || 0)
  var cur = Number(video.currentTime || 0)
  var pct = dur > 0 ? Math.max(0, Math.min(100, (cur / dur) * 100)) : 0
  bar.style.width = pct + '%'
}
