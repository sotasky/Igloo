// Twitter/YouTube/TikTok/Instagram hover card for profile links.
// Triggers:
//   - a.feed-author-wrap            (parent author — data-author-handle on ancestor, twitter-only)
//   - a.feed-overlay-headline       (media overlay poster header — data-feed-channel-id)
//   - a.feed-quote-author-link, .feed-quote-avatar  (quote author, twitter-only)
//   - a.feed-inline-link            (@mentions — href = /channels/<platform>_<id>)
//   - a.feed-repost-link            (retweeter — twitter-only)
//   - a.shorts-channel              (shorts header — data-channel-id on element)
//   - a.shorts-rail-avatar-link     (shorts side-rail avatar — data-channel-id on element)
//   - a.shorts-repost-link          (shorts reposter — data-channel-id on element)

(() => {
	const OPEN_DELAY = 0;
	const CLOSE_DELAY = 300;
	const TRIGGER_SEL = 'a.feed-author-trigger, a.feed-overlay-headline, a.feed-quote-author-link, .feed-quote-avatar, a.feed-inline-link, a.feed-repost-link, a.shorts-channel, a.shorts-rail-avatar-link, a.shorts-repost-link';
	const CHANNELS_HREF_RE = /^\/channels\/(twitter|x|youtube|tiktok|instagram)_([A-Za-z0-9_@.\-]+)$/;
	const retweetMuteStorageKey = 'feedMutedRetweetChannels';
	const legacyRetweetMuteStorageKey = 'mpa-feed-retweet-muted:v1';
	const cache = new Map(); // channel_id -> HTMLElement
	let openTimer = null;
	let closeTimer = null;
	let currentCard = null;
	let currentChannelID = null;
	let currentAnchor = null;
	let openGen = 0;

	function i18nText(key, fallback) {
		const cfg = window.IglooI18n || {};
		const messages = cfg.messages || {};
		const value = messages[key];
		return value == null || value === '' ? String(fallback || key || '') : String(value);
	}

	function i18nFormat(key, fallback, value) {
		return i18nText(key, fallback).replace(/%1\$s/g, String(value == null ? '' : value));
	}

	function isCurrentChannelPath(channelID) {
		const cid = String(channelID || '').trim();
		if (!cid) return false;
		const path = window.location && window.location.pathname ? window.location.pathname : '';
		return path === '/channels/' + cid || path === '/channels/' + encodeURIComponent(cid);
	}

	function channelIDFor(el) {
		if (!el) return null;
		if (el.matches('a.shorts-channel, a.shorts-rail-avatar-link, a.shorts-repost-link')) {
			return el.getAttribute('data-channel-id') || null;
		}
		if (el.matches('a.feed-author-trigger')) {
			const art = el.closest('[data-author-handle]');
			const h = art?.getAttribute('data-author-handle')?.toLowerCase();
			return h ? 'twitter_' + h : null;
		}
		if (el.matches('a.feed-overlay-headline')) {
			const cid = el.getAttribute('data-feed-channel-id') || el.closest('[data-channel-id]')?.getAttribute('data-channel-id');
			if (cid) return cid;
			const href = el.getAttribute('href') || '';
			const m = href.match(CHANNELS_HREF_RE);
			return m ? (m[1] + '_' + m[2]) : null;
		}
		if (el.matches('a.feed-quote-author-link, .feed-quote-avatar')) {
			const quote = el.closest('[data-quote-author-channel-id], [data-quote-author-handle]');
			const cid = quote?.getAttribute('data-quote-author-channel-id');
			if (cid) return cid;
			if (el.matches('a.feed-quote-author-link')) {
				const href = el.getAttribute('href') || '';
				const m = href.match(CHANNELS_HREF_RE);
				if (m) return m[1] + '_' + m[2];
			}
			const localHandle = quote?.getAttribute('data-quote-author-handle')?.toLowerCase();
			if (localHandle) return 'twitter_' + localHandle;
			const art = el.closest('[data-quote-author-handle]');
			const h = art?.getAttribute('data-quote-author-handle')?.toLowerCase();
			return h ? 'twitter_' + h : null;
		}
		if (el.matches('a.feed-repost-link')) {
			const attr = el.getAttribute('data-repost-handle');
			if (attr) return 'twitter_' + attr.toLowerCase();
			const href = el.getAttribute('href') || '';
			const m = href.match(CHANNELS_HREF_RE);
			return m ? (m[1] + '_' + m[2]) : null;
		}
		if (el.matches('a.feed-inline-link')) {
			const href = el.getAttribute('href') || '';
			const m = href.match(CHANNELS_HREF_RE);
			return m ? (m[1] + '_' + m[2]) : null;
		}
		return null;
	}

	function triggerFromTarget(target) {
		if (!target || !target.closest) return null;
		const anchor = target.closest(TRIGGER_SEL);
		if (!anchor) return null;
		if (anchor.matches('a.feed-overlay-headline') && target.closest('.feed-overlay-header-actions')) return null;
		return anchor;
	}

	function clearTimers() {
		if (openTimer) { clearTimeout(openTimer); openTimer = null; }
		if (closeTimer) { clearTimeout(closeTimer); closeTimer = null; }
	}

	function pointInsideElement(el, e) {
		if (!el || !e) return false;
		const x = e.clientX;
		const y = e.clientY;
		if (!Number.isFinite(x) || !Number.isFinite(y)) return false;
		const rect = el.getBoundingClientRect();
		return x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom;
	}

	function eventIsOnCurrentCard(e) {
		if (!currentCard) return false;
		if (currentCard.contains(e.target)) return true;
		return pointInsideElement(currentCard, e);
	}

	function dismiss() {
		++openGen;
		clearTimers();
		if (currentCard) {
			currentCard.remove();
			currentCard = null;
		}
		currentChannelID = null;
		currentAnchor = null;
	}

	function positionCard(card, anchor) {
		const rect = anchor.getBoundingClientRect();
		const cardWidth = card.offsetWidth || 320;
		const cardHeight = card.offsetHeight || 260;
		const margin = 6;
		const vw = window.innerWidth;
		const vh = window.innerHeight;

		let left = rect.left + window.scrollX;
		if (left + cardWidth + 16 > vw + window.scrollX) {
			left = vw + window.scrollX - cardWidth - 16;
		}
		left = Math.max(8, left);

		// Prefer below; flip above only when below would overflow the viewport.
		// When flipping, anchor the card's BOTTOM just above the trigger so the
		// card never overlaps the username/avatar that opened it.
		let top;
		if (rect.bottom + margin + cardHeight <= vh) {
			top = rect.bottom + window.scrollY + margin;
		} else {
			top = rect.top + window.scrollY - cardHeight - margin;
		}

		card.style.left = left + 'px';
		card.style.top = top + 'px';
	}

	async function loadCard(channelID) {
		if (cache.has(channelID)) return cache.get(channelID).cloneNode(true);
		const res = await fetch('/api/profile-card/' + encodeURIComponent(channelID));
		if (!res.ok) return null;
		const refreshing = res.headers.get('X-Igloo-Profile-Refreshing') === '1';
		const html = await res.text();
		const tmp = document.createElement('div');
		tmp.innerHTML = html.trim();
		const node = tmp.firstElementChild;
		if (!node) return null;
		if (!refreshing) cache.set(channelID, node.cloneNode(true));
		return node;
	}

	async function openFor(anchor, channelID) {
		const myGen = ++openGen;
		clearTimers();
		if (currentCard) { currentCard.remove(); currentCard = null; }
		currentChannelID = null;
		currentAnchor = null;

		const card = await loadCard(channelID);
		if (myGen !== openGen) return;
		if (!card) return;

		document.body.appendChild(card);
		positionCard(card, anchor);
		card.addEventListener('mouseenter', clearTimers);
		card.addEventListener('mouseleave', scheduleClose);
		wireProfileCard(card);
		if (window.htmx && typeof window.htmx.process === 'function') window.htmx.process(card);
		currentCard = card;
		currentChannelID = channelID;
		currentAnchor = anchor;
	}

	function subscribeURLFor(channelID, handle) {
		const h = String(handle || '').replace(/^@+/, '');
		if (channelID.startsWith('twitter_') || channelID.startsWith('x_')) return 'https://x.com/' + h;
		if (channelID.startsWith('tiktok_'))  return 'https://www.tiktok.com/@' + h;
		if (channelID.startsWith('instagram_'))  return 'https://www.instagram.com/' + h + '/';
		if (channelID.startsWith('youtube_')) return 'https://www.youtube.com/channel/' + channelID.slice('youtube_'.length);
		return '';
	}

	function syncProfileCardFollowState(channelID, following) {
		const cid = String(channelID || '').trim();
		if (!cid) return;
		const isFollowing = !!following;
		document.querySelectorAll('[data-feed-follow-toggle][data-feed-channel-id="' + CSS.escape(cid) + '"]').forEach((btn) => {
			btn.setAttribute('data-following', following ? '1' : '0');
			btn.classList.toggle('following', isFollowing);
			btn.textContent = following
				? i18nText('action_following', 'Following')
				: i18nText('action_follow', 'Follow');
		});
		document.querySelectorAll('[data-feed-menu-action="unfollow"][data-feed-channel-id="' + CSS.escape(cid) + '"]').forEach((btn) => {
			btn.style.display = isFollowing ? '' : 'none';
		});
		document.querySelectorAll('.profile-card[data-channel-id="' + CSS.escape(cid) + '"]').forEach((card) => {
			card.setAttribute('data-profile-card-following', isFollowing ? '1' : '0');
			card.classList.toggle('profile-card-following', isFollowing);
			card.querySelectorAll('[data-profile-card-menu-action="unfollow"]').forEach((btn) => {
				btn.style.display = isFollowing ? '' : 'none';
			});
			card.querySelectorAll('[data-profile-card-menu], .profile-card-top-actions .feed-star-btn').forEach((el) => {
				el.style.display = isFollowing ? '' : 'none';
			});
		});
	}

	function syncCardFollowButtons(channelID, following) {
		if (window.MpaSiteBase && typeof window.MpaSiteBase.syncChannelFollowState === 'function') {
			window.MpaSiteBase.syncChannelFollowState(channelID, following);
			return;
		}
		syncProfileCardFollowState(channelID, following);
	}

	function feedBundleOwnsFollowButtons() {
		return !!document.querySelector('script[src*="js/dist/feed.js"]');
	}

	function getMutedRepostChannels() {
		let raw = '';
		try {
			raw = localStorage.getItem(retweetMuteStorageKey) || localStorage.getItem(legacyRetweetMuteStorageKey) || '';
		} catch (_) { raw = ''; }
		if (!raw) return new Set();
		try {
			const parsed = JSON.parse(raw);
			if (!Array.isArray(parsed)) return new Set();
			return new Set(parsed.map((v) => String(v || '').trim()).filter(Boolean));
		} catch (_) { return new Set(); }
	}

	function saveMutedRepostChannels(set) {
		const values = JSON.stringify(Array.from(set || []));
		try {
			localStorage.setItem(retweetMuteStorageKey, values);
			localStorage.setItem(legacyRetweetMuteStorageKey, values);
		} catch (_) {}
	}

	function isRepostMuted(channelID) {
		return getMutedRepostChannels().has(String(channelID || '').trim());
	}

	function setRepostMuted(channelID, muted) {
		const cid = String(channelID || '').trim();
		if (!cid) return;
		const set = getMutedRepostChannels();
		if (muted) set.add(cid);
		else set.delete(cid);
		saveMutedRepostChannels(set);
	}

	function setProfileMenuOpen(menu, open) {
		if (!menu) return;
		menu.classList.toggle('open', !!open);
		const card = menu.closest('.profile-card');
		if (card) card.classList.toggle('profile-card-menu-open', !!open);
	}

	function updateProfileMenuLabels(scope) {
		const root = scope || document;
		root.querySelectorAll('[data-profile-card-menu-action="retweets_off"]').forEach((btn) => {
			const channelID = String(btn.getAttribute('data-profile-card-channel-id') || '').trim();
			const offLabel = btn.getAttribute('data-profile-card-repost-off-label') || i18nText('feed_turn_off_retweets', 'Turn off retweets');
			const onLabel = btn.getAttribute('data-profile-card-repost-on-label') || i18nText('feed_turn_on_retweets', 'Turn on retweets');
			btn.textContent = isRepostMuted(channelID) ? onLabel : offLabel;
		});
	}

	function applyRepostMuteFilter(scope) {
		const root = scope || document;
		const muted = getMutedRepostChannels();
		root.querySelectorAll('[data-feed-repost-line]').forEach((line) => {
			const article = line.closest('[data-feed-item]');
			const authorHandle = String((article && article.getAttribute('data-author-handle')) || '').toLowerCase().replace(/^@/, '');
			let anyVisible = false;
			line.querySelectorAll('.feed-repost-link[data-repost-channel-id]').forEach((a) => {
				const channelID = String(a.getAttribute('data-repost-channel-id') || '').trim();
				const handle = String(a.getAttribute('data-repost-handle') || '').toLowerCase();
				const hide = channelID && muted.has(channelID) && !(handle && handle === authorHandle);
				a.style.display = hide ? 'none' : '';
				const sep = a.nextElementSibling;
				if (sep && sep.classList.contains('feed-repost-sep')) sep.style.display = hide ? 'none' : '';
				if (!hide) anyVisible = true;
			});
			if (line.querySelector('.feed-repost-link-inert') || line.querySelector('[data-feed-repost-more]')) anyVisible = true;
			line.style.display = anyVisible ? '' : 'none';
		});

		root.querySelectorAll('[data-feed-item]').forEach((item) => {
			const isRetweet = String(item.getAttribute('data-feed-is-retweet') || '') === '1';
			const sourceChannelID = String(item.getAttribute('data-source-channel-id') || '').trim();
			const authorChannelID = String(item.getAttribute('data-feed-author-channel-id') || item.getAttribute('data-channel-id') || '').trim();
			const quoteTweetID = String(item.getAttribute('data-feed-quote-tweet-id') || '').trim();
			const quoteAuthorChannelID = String(item.getAttribute('data-feed-quote-author-channel-id') || '').trim();
			let hide = false;
			if (isRetweet && sourceChannelID && sourceChannelID !== authorChannelID && muted.has(sourceChannelID)) hide = true;
			if (!hide && quoteTweetID && authorChannelID && authorChannelID !== quoteAuthorChannelID && muted.has(authorChannelID)) hide = true;
			item.classList.toggle('feed-card-muted-retweet', !!hide);
			item.style.display = hide ? 'none' : '';
		});
	}

	function closeProfileMenus(exceptMenu) {
		document.querySelectorAll('[data-profile-card-menu].open').forEach((menu) => {
			if (exceptMenu && menu === exceptMenu) return;
			setProfileMenuOpen(menu, false);
		});
	}

	function wireProfileMenu(card) {
		const scope = card || document;
		if (!scope.querySelectorAll) return;
		updateProfileMenuLabels(scope);
		scope.querySelectorAll('[data-profile-card-menu]').forEach((menu) => {
			if (menu.getAttribute('data-profile-card-menu-wired') === '1') return;
			menu.setAttribute('data-profile-card-menu-wired', '1');
			menu.addEventListener('click', async (e) => {
				const toggle = e.target.closest('[data-profile-card-menu-toggle]');
				if (toggle) {
					e.preventDefault();
					e.stopPropagation();
					const willOpen = !menu.classList.contains('open');
					closeProfileMenus(menu);
					setProfileMenuOpen(menu, willOpen);
					updateProfileMenuLabels(menu);
					return;
				}
				const item = e.target.closest('[data-profile-card-menu-action]');
				if (!item) return;
				e.preventDefault();
				e.stopPropagation();
				const action = String(item.getAttribute('data-profile-card-menu-action') || '').trim();
				const channelID = String(item.getAttribute('data-profile-card-channel-id') || '').trim();
				const label = String(item.getAttribute('data-profile-card-label') || channelID || 'account').trim();
				const platform = String(item.getAttribute('data-profile-card-platform') || '').trim();
				const mpa = window.MpaSiteBase;
				setProfileMenuOpen(menu, false);
				if (!mpa || !channelID) return;

				if (action === 'settings') {
					if (typeof mpa.openChannelSettingsModal === 'function') {
						mpa.openChannelSettingsModal({ channelId: channelID, channelName: label, platform, anchorEl: menu });
					}
					return;
				}

				if (action === 'refresh') {
					item.disabled = true;
					try {
						const payload = await mpa.apiJson('/api/channels/' + encodeURIComponent(channelID) + '/refresh', {
							method: 'POST',
							body: JSON.stringify({}),
						});
						mpa.showToast((payload && payload.message) || i18nFormat('toast_refreshed_channel', 'Refreshed %1$s', label));
						if (isCurrentChannelPath(channelID)) window.location.reload();
					} catch (err) {
						mpa.showToast((err && err.payload && err.payload.error) || i18nText('error_refresh_failed', 'Refresh failed'));
					} finally {
						item.disabled = false;
					}
					return;
				}

				if (action === 'unfollow') {
					const ok = await mpa.askConfirm({
						title: i18nText('confirm_unfollow_channel_title', 'Unfollow Channel'),
						body: i18nFormat('confirm_unfollow_channel_delete_media_body', 'Unfollow "%1$s" and delete local media? This cannot be undone.', label),
						confirmLabel: i18nText('action_unfollow', 'Unfollow'),
						cancelLabel: i18nText('action_cancel', 'Cancel'),
						danger: true,
					});
					if (!ok) return;
					syncCardFollowButtons(channelID, false);
					try {
						await mpa.apiJson('/api/unsubscribe/' + encodeURIComponent(channelID) + '?delete_files=true', { method: 'DELETE' });
						mpa.showToast(i18nFormat('toast_unfollowed_channel', 'Unfollowed %1$s', label));
					} catch (err) {
						syncCardFollowButtons(channelID, true);
						mpa.showToast((err && err.payload && err.payload.error) || i18nText('error_unfollow_failed', 'Failed to unfollow'));
					}
					return;
				}

				if (action === 'retweets_off') {
					const muted = !isRepostMuted(channelID);
					setRepostMuted(channelID, muted);
					updateProfileMenuLabels(document);
					applyRepostMuteFilter(document);
					mpa.apiJson('/api/channels/' + encodeURIComponent(channelID) + '/settings', {
						method: 'POST',
						body: JSON.stringify({ include_reposts: !muted }),
					}).catch(() => {});
					const disabled = platform === 'tiktok' || platform === 'instagram'
						? i18nText('toast_reposts_disabled_for_account', 'Reposts disabled for this account')
						: i18nText('toast_retweets_disabled_for_account', 'Retweets disabled for this account');
					const enabled = platform === 'tiktok' || platform === 'instagram'
						? i18nText('toast_reposts_enabled_for_account', 'Reposts enabled for this account')
						: i18nText('toast_retweets_enabled_for_account', 'Retweets enabled for this account');
					mpa.showToast(muted ? disabled : enabled);
				}
			});
		});
	}

	function wireFollowButton(card) {
		const btn = card.querySelector('button[data-feed-follow-toggle]');
		if (!btn) return;
		if (btn.getAttribute('data-profile-follow-wired') === '1') return;
		btn.setAttribute('data-profile-follow-wired', '1');
		btn.addEventListener('click', async (e) => {
			e.preventDefault();
			e.stopPropagation();
			if (btn.disabled) return;
			const channelID = btn.getAttribute('data-feed-channel-id') || '';
			const handle = (btn.getAttribute('data-feed-handle') || '').replace(/^@+/, '');
			const label = btn.getAttribute('data-feed-label') || handle || channelID;
			const following = btn.getAttribute('data-following') === '1';
			const mpa = window.MpaSiteBase;
			if (!mpa) return;
			btn.disabled = true;
			try {
				if (following) {
					const ok = await mpa.askConfirm({
						title: i18nText('confirm_unfollow_channel_title', 'Unfollow Channel'),
						body: i18nFormat('confirm_unfollow_channel_delete_media_body', 'Unfollow "%1$s" and delete local media? This cannot be undone.', label),
						confirmLabel: i18nText('action_unfollow', 'Unfollow'),
						cancelLabel: i18nText('action_cancel', 'Cancel'),
						danger: true,
					});
					if (!ok) return;
					syncCardFollowButtons(channelID, false);
					await mpa.apiJson('/api/unsubscribe/' + encodeURIComponent(channelID) + '?delete_files=true', { method: 'DELETE' });
					mpa.showToast(i18nFormat('toast_unfollowed_channel', 'Unfollowed %1$s', label));
				} else {
					const url = subscribeURLFor(channelID, handle);
					if (!url) { mpa.showToast(i18nText('error_follow_unknown_platform', 'Cannot follow: unknown platform')); return; }
					await mpa.apiJson('/api/subscribe', { method: 'POST', body: JSON.stringify({ url }) });
					syncCardFollowButtons(channelID, true);
					mpa.showToast(i18nFormat('toast_followed_channel', 'Followed %1$s', label));
				}
			} catch (err) {
				const msg = (err && err.payload && err.payload.error) || (
					following
						? i18nText('error_unfollow_failed', 'Failed to unfollow')
						: i18nText('error_follow_failed', 'Failed to follow')
				);
				if (following) syncCardFollowButtons(channelID, true);
				mpa.showToast(msg);
			} finally {
				btn.disabled = false;
			}
		});
	}

	function wireProfileCard(card) {
		if (!feedBundleOwnsFollowButtons()) wireFollowButton(card);
		wireProfileMenu(card);
	}

	function wireStaticProfileCards(root) {
		const scope = root || document;
		if (scope.matches && scope.matches('.profile-card')) {
			wireProfileCard(scope);
		}
		if (scope.querySelectorAll) {
			scope.querySelectorAll('.profile-card').forEach(wireProfileCard);
		}
	}

	function scheduleOpen(anchor, channelID) {
		clearTimers();
		if (OPEN_DELAY <= 0) {
			openFor(anchor, channelID);
			return;
		}
		openTimer = setTimeout(() => { openFor(anchor, channelID); }, OPEN_DELAY);
	}

	function scheduleClose() {
		if (closeTimer) clearTimeout(closeTimer);
		closeTimer = setTimeout(dismiss, CLOSE_DELAY);
	}

	document.addEventListener('mouseover', (e) => {
		if (eventIsOnCurrentCard(e)) { clearTimers(); return; }
		const anchor = triggerFromTarget(e.target);
		if (!anchor) return;
		const cid = channelIDFor(anchor);
		if (!cid) return;
		if (cid === currentChannelID && currentCard) { clearTimers(); return; }
		scheduleOpen(anchor, cid);
	});

	document.addEventListener('mouseout', (e) => {
		if (eventIsOnCurrentCard(e)) { clearTimers(); return; }
		const anchor = triggerFromTarget(e.target);
		if (!anchor) return;
		const related = e.relatedTarget;
		if (currentCard && related && currentCard.contains(related)) return;
		if (!currentCard) { clearTimers(); return; }
		scheduleClose();
	});

	document.addEventListener('click', (e) => {
		if (e.target.closest && e.target.closest('[data-profile-card-menu]')) return;
		const anchor = triggerFromTarget(e.target);
		if (anchor) {
			const cid = channelIDFor(anchor);
			if (!cid) return;
			if (window.matchMedia('(hover: none)').matches) {
				e.preventDefault();
				if (currentChannelID === cid && currentCard) { dismiss(); return; }
				openFor(anchor, cid);
			}
			return;
		}
		closeProfileMenus();
		if (currentCard && !currentCard.contains(e.target)) dismiss();
	});

	wireStaticProfileCards();
	document.addEventListener('htmx:load', (e) => {
		wireStaticProfileCards(e.detail && e.detail.elt ? e.detail.elt : document);
	});

	window.addEventListener('scroll', dismiss, { passive: true });
	window.addEventListener('resize', dismiss);
})();
