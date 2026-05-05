(function () {
  const SENTINEL_SELECTOR = '.js-infinite-scroll';
  const PREFETCH_PX = 1800;
  const controllers = [];

  function isVisible(el) {
    if (!el) return false;
    if (el.hidden) return false;
    if (el.classList && el.classList.contains('hidden')) return false;
    return el.getClientRects().length > 0;
  }

  function parseHtml(html) {
    return new DOMParser().parseFromString(String(html || ''), 'text/html');
  }

  function uniqueSignature(node) {
    if (!(node instanceof Element)) return '';
    const tweetId = node.getAttribute('data-tweet-id');
    if (tweetId) return 'tweet:' + tweetId;
    const videoId = node.getAttribute('data-video-id');
    if (videoId) return 'video:' + videoId;
    const href = node.getAttribute('href');
    if (href) return 'href:' + href;
    return '';
  }

  function dispatchAppend(detail) {
    try {
      document.dispatchEvent(new CustomEvent('mpa:infinite-append', { detail: detail || {} }));
    } catch (_) { }
  }

  function InfiniteController(sentinel) {
    this.sentinel = sentinel;
    this.containerSelector = String(sentinel.getAttribute('data-container-selector') || '').trim();
    this.itemSelector = String(sentinel.getAttribute('data-item-selector') || '').trim();
    this.loading = false;
    this.failed = false;
    this.isPrefetching = false;
    this.prefetchAttempts = 0;
    this.io = null;
    this._scrollHandler = this.maybeLoad.bind(this);
  }

  InfiniteController.prototype.container = function () {
    return this.containerSelector ? document.querySelector(this.containerSelector) : null;
  };

  InfiniteController.prototype.nextUrl = function () {
    return String(this.sentinel.getAttribute('data-next-url') || '').trim();
  };

  InfiniteController.prototype.setNextUrl = function (url) {
    this.sentinel.setAttribute('data-next-url', String(url || '').trim());
  };

  InfiniteController.prototype.setLoadingState = function (loading) {
    this.loading = !!loading;
    this.sentinel.setAttribute('data-loading', this.loading ? '1' : '0');
  };

  InfiniteController.prototype.maybeLoad = function () {
    if (this.loading || this.failed) return;
    if (!this.nextUrl()) return;
    if (!isVisible(this.sentinel)) return;
    const rect = this.sentinel.getBoundingClientRect();
    const viewportH = window.innerHeight || document.documentElement.clientHeight || 0;
    if (rect.top <= viewportH + PREFETCH_PX) {
      this.loadNext();
    }
  };

  InfiniteController.prototype._extractNodes = function (doc) {
    const nextContainer = this.containerSelector ? doc.querySelector(this.containerSelector) : null;
    if (!nextContainer || !this.itemSelector) return [];
    return Array.prototype.slice.call(nextContainer.querySelectorAll(this.itemSelector));
  };

  InfiniteController.prototype._nextUrlFromDoc = function (doc) {
    const matching = doc.querySelector(SENTINEL_SELECTOR);
    if (!matching) return '';
    return String(matching.getAttribute('data-next-url') || '').trim();
  };

  InfiniteController.prototype.loadNext = function () {
    const self = this;
    const url = self.nextUrl();
    const container = self.container();
    if (!url || !container || self.loading) return;

    self.setLoadingState(true);
    fetch(url, {
      credentials: 'same-origin',
      headers: { 'X-Requested-With': 'XMLHttpRequest' }
    }).then(function (response) {
      if (!response.ok) throw new Error('HTTP ' + response.status);
      return response.text();
    }).then(function (html) {
      const doc = parseHtml(html);
      const newNodes = self._extractNodes(doc);
      const existingKeys = new Set();
      Array.prototype.forEach.call(container.querySelectorAll(self.itemSelector), function (node) {
        const sig = uniqueSignature(node);
        if (sig) existingKeys.add(sig);
      });

      let appended = 0;
      newNodes.forEach(function (node) {
        const sig = uniqueSignature(node);
        if (sig && existingKeys.has(sig)) return;
        if (sig) existingKeys.add(sig);
        container.appendChild(node.cloneNode(true));
        appended += 1;
      });

      self.setNextUrl(self._nextUrlFromDoc(doc));
      dispatchAppend({
        containerSelector: self.containerSelector,
        itemSelector: self.itemSelector,
        appendedCount: appended,
        nextUrl: self.nextUrl()
      });

      if (appended === 0 && self.nextUrl()) {
        // Avoid loops when the next page structure does not match.
        self.failed = true;
      }
    }).catch(function () {
      self.failed = true;
    }).finally(function () {
      self.setLoadingState(false);
      if (self.isPrefetching) {
        setTimeout(function () {
          self.doPrefetchStep();
        }, 150);
      } else {
        self.maybeLoad();
      }
    });
  };

  InfiniteController.prototype.start = function () {
    window.addEventListener('scroll', this._scrollHandler, { passive: true });
    window.addEventListener('resize', this._scrollHandler, { passive: true });
    if ('IntersectionObserver' in window) {
      const self = this;
      this.io = new IntersectionObserver(function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) self.maybeLoad();
        });
      }, { root: null, rootMargin: '1800px 0px 1800px 0px' });
      this.io.observe(this.sentinel);
    }
    this.maybeLoad();
    setTimeout(this._scrollHandler, 250);
  };

  InfiniteController.prototype.doPrefetchStep = function () {
    if (!this.isPrefetching || this.failed || !this.nextUrl() || this.prefetchAttempts >= 1000) return;
    if (!this.loading) {
      this.prefetchAttempts += 1;
      this.loadNext();
    }
  };

  InfiniteController.prototype.prefetchAll = function () {
    var self = this;
    self.isPrefetching = true;
    self.prefetchAttempts = 0;
    // Delay prefetch until the browser is idle after initial render, giving
    // first-page media a head start. requestIdleCallback fires when the main
    // thread has no pending work; timeout ensures it starts within 3 s regardless.
    if (window.requestIdleCallback) {
      requestIdleCallback(function () { self.doPrefetchStep(); }, { timeout: 3000 });
    } else {
      setTimeout(function () { self.doPrefetchStep(); }, 2000);
    }
  };

  function init() {
    const sentinels = Array.prototype.slice.call(document.querySelectorAll(SENTINEL_SELECTOR));
    sentinels.forEach(function (sentinel) {
      const controller = new InfiniteController(sentinel);
      if (!controller.containerSelector || !controller.itemSelector) return;
      if (!controller.container()) return;
      controllers.push(controller);
      controller.start();
      if (!sentinel.hasAttribute('data-no-prefetch')) {
        controller.prefetchAll();
      }
    });

    window.MpaInfinitePage = {
      refreshAll: function () {
        controllers.forEach(function (c) { c.maybeLoad(); });
      },
      controllers: controllers
    };
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }
})();
