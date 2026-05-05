(function () {
    function getControllerRoot(media) {
        if (!media) return null;
        return media.closest('media-controller') || media.parentElement;
    }

    function makeListenerRecord(target, eventName, handler, wrapped, options) {
        return { target, eventName, handler, wrapped, options };
    }

    function NativeVideoJsCompat(mediaEl, options) {
        this.media = mediaEl;
        this.options = options || {};
        this._listeners = [];
        this._disposed = false;
        this._controller = getControllerRoot(mediaEl);
        this._applyInitOptions();
    }

    NativeVideoJsCompat.prototype._applyInitOptions = function () {
        if (!this.media) return;
        this.media.controls = false;
        this.media.setAttribute('playsinline', '');
        if (this.options.preload) this.media.preload = this.options.preload;
        if (this.options.autoplay) this.media.autoplay = true;
        if (this.options.poster) this.media.poster = this.options.poster;
        if (Array.isArray(this.options.playbackRates) && this._controller) {
            const rateBtn = this._controller.querySelector('media-playback-rate-button');
            if (rateBtn) {
                // Media Chrome expects a space-delimited list in the "rates" attribute.
                rateBtn.setAttribute('rates', this.options.playbackRates.join(' '));
            }
        }
        if (this.options.aspectRatio) {
            this.aspectRatio(this.options.aspectRatio);
        }
    };

    NativeVideoJsCompat.prototype._addListener = function (eventName, handler, once) {
        if (!this.media || typeof handler !== 'function') return this;
        var wrapped = handler;
        if (once) {
            var self = this;
            wrapped = function () {
                self.off(eventName, handler);
                return handler.apply(this, arguments);
            };
        }
        this.media.addEventListener(eventName, wrapped);
        this._listeners.push(makeListenerRecord(this.media, eventName, handler, wrapped, undefined));
        return this;
    };

    NativeVideoJsCompat.prototype.on = function (eventName, handler) {
        return this._addListener(eventName, handler, false);
    };

    NativeVideoJsCompat.prototype.one = function (eventName, handler) {
        return this._addListener(eventName, handler, true);
    };

    NativeVideoJsCompat.prototype.off = function (eventName, handler) {
        if (!this._listeners || !this._listeners.length) return this;
        this._listeners = this._listeners.filter(function (rec) {
            var matchEvent = !eventName || rec.eventName === eventName;
            var matchHandler = !handler || rec.handler === handler;
            if (matchEvent && matchHandler) {
                rec.target.removeEventListener(rec.eventName, rec.wrapped, rec.options);
                return false;
            }
            return true;
        });
        return this;
    };

    NativeVideoJsCompat.prototype.ready = function (cb) {
        if (typeof cb !== 'function') return this;
        var self = this;
        setTimeout(function () {
            if (!self._disposed) cb.call(self);
        }, 0);
        return this;
    };

    NativeVideoJsCompat.prototype.readyState = function () {
        return this.media ? this.media.readyState : 0;
    };

    NativeVideoJsCompat.prototype.el = function () {
        return this._controller || this.media;
    };

    NativeVideoJsCompat.prototype.src = function (srcSpec) {
        if (!this.media) return this;
        var src = srcSpec;
        if (srcSpec && typeof srcSpec === 'object') {
            src = srcSpec.src || '';
            if (srcSpec.type) this.media.type = srcSpec.type;
        }
        try {
            this.media.querySelectorAll('track[data-dashboard-remote-track="1"]').forEach(function (t) { t.remove(); });
        } catch (_) { }
        this.media.src = src || '';
        try { this.media.load(); } catch (_) { }
        return this;
    };

    NativeVideoJsCompat.prototype.poster = function (value) {
        if (!this.media) return '';
        if (typeof value !== 'undefined') {
            this.media.poster = value || '';
            return this;
        }
        return this.media.poster;
    };

    NativeVideoJsCompat.prototype.currentTime = function (value) {
        if (!this.media) return 0;
        if (typeof value !== 'undefined') {
            try { this.media.currentTime = Number(value) || 0; } catch (_) { }
            return this;
        }
        return this.media.currentTime || 0;
    };

    NativeVideoJsCompat.prototype.duration = function () {
        if (!this.media) return 0;
        var d = this.media.duration;
        return Number.isFinite(d) ? d : 0;
    };

    NativeVideoJsCompat.prototype.play = function () {
        if (!this.media || typeof this.media.play !== 'function') return Promise.resolve();
        return this.media.play();
    };

    NativeVideoJsCompat.prototype.pause = function () {
        if (this.media && typeof this.media.pause === 'function') this.media.pause();
        return this;
    };

    NativeVideoJsCompat.prototype.paused = function () {
        return this.media ? !!this.media.paused : true;
    };

    NativeVideoJsCompat.prototype.volume = function (value) {
        if (!this.media) return 1;
        if (typeof value !== 'undefined') {
            var v = Math.max(0, Math.min(1, Number(value)));
            if (Number.isFinite(v)) this.media.volume = v;
            return this;
        }
        return typeof this.media.volume === 'number' ? this.media.volume : 1;
    };

    NativeVideoJsCompat.prototype.playbackRate = function (value) {
        if (!this.media) return 1;
        if (typeof value !== 'undefined') {
            var r = Number(value);
            if (Number.isFinite(r) && r > 0) this.media.playbackRate = r;
            return this;
        }
        return typeof this.media.playbackRate === 'number' ? this.media.playbackRate : 1;
    };

    NativeVideoJsCompat.prototype.aspectRatio = function (ratio) {
        if (!ratio || !this._controller) return this;
        this._controller.style.setProperty('--dashboard-media-aspect-ratio', String(ratio).replace(':', ' / '));
        return this;
    };

    NativeVideoJsCompat.prototype.addRemoteTextTrack = function (trackSpec) {
        if (!this.media || !trackSpec) return null;
        var track = document.createElement('track');
        if (trackSpec.kind) track.kind = trackSpec.kind;
        if (trackSpec.label) track.label = trackSpec.label;
        if (trackSpec.srclang) track.srclang = trackSpec.srclang;
        if (trackSpec.src) track.src = trackSpec.src;
        if (trackSpec.default) track.default = true;
        track.dataset.dashboardRemoteTrack = '1';
        this.media.appendChild(track);
        return { track: track };
    };

    NativeVideoJsCompat.prototype.dispose = function () {
        this._disposed = true;
        this.off();
        if (!this.media) return;
        try { this.media.pause(); } catch (_) { }
        try {
            this.media.querySelectorAll('track[data-dashboard-remote-track="1"]').forEach(function (t) { t.remove(); });
        } catch (_) { }
    };

    function videojsCompat(elementOrId, options) {
        var mediaEl = elementOrId;
        if (typeof elementOrId === 'string') {
            mediaEl = document.getElementById(elementOrId) || document.querySelector(elementOrId);
        }
        if (!mediaEl) throw new Error('videojs compat: media element not found');
        return new NativeVideoJsCompat(mediaEl, options || {});
    }

    window.videojs = videojsCompat;
})();
