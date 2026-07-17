import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import vm from 'node:vm';

class FakeClassList {
	constructor(el) {
		this.el = el;
	}

	add(name) {
		this.el.classes.add(name);
	}

	remove(name) {
		this.el.classes.delete(name);
	}

	contains(name) {
		return this.el.classes.has(name);
	}

	toggle(name, force) {
		const on = force === undefined ? !this.contains(name) : !!force;
		if (on) this.add(name);
		else this.remove(name);
		return on;
	}
}

class FakeElement {
	constructor(tagName, opts = {}) {
		this.tagName = String(tagName || 'div').toUpperCase();
		this.attributes = new Map();
		this.classes = new Set(opts.classes || []);
		this.children = [];
		this.parentElement = null;
		this.listeners = new Map();
		this.style = {};
		this.rect = opts.rect || { left: 0, top: 0, right: 0, bottom: 0 };
		this.offsetWidth = opts.offsetWidth || 0;
		this.offsetHeight = opts.offsetHeight || 0;
		this.classList = new FakeClassList(this);
		if (opts.attrs) {
			for (const [key, value] of Object.entries(opts.attrs)) {
				this.setAttribute(key, value);
			}
		}
	}

	setAttribute(name, value) {
		const normalized = String(name);
		const stringValue = String(value);
		if (normalized === 'class') {
			this.classes = new Set(stringValue.split(/\s+/).filter(Boolean));
			return;
		}
		this.attributes.set(normalized, stringValue);
	}

	getAttribute(name) {
		return this.attributes.has(name) ? this.attributes.get(name) : null;
	}

	appendChild(child) {
		child.parentElement = this;
		this.children.push(child);
		return child;
	}

	remove() {
		if (!this.parentElement) return;
		const siblings = this.parentElement.children;
		const idx = siblings.indexOf(this);
		if (idx >= 0) siblings.splice(idx, 1);
		this.parentElement = null;
	}

	addEventListener(type, fn) {
		if (!this.listeners.has(type)) this.listeners.set(type, []);
		this.listeners.get(type).push(fn);
	}

	contains(node) {
		for (let cur = node; cur; cur = cur.parentElement) {
			if (cur === this) return true;
		}
		return false;
	}

	closest(selector) {
		for (let cur = this; cur; cur = cur.parentElement) {
			if (cur.matches(selector)) return cur;
		}
		return null;
	}

	matches(selector) {
		return String(selector || '')
			.split(',')
			.some((part) => matchesSingleSelector(this, part.trim()));
	}

	querySelector(selector) {
		return this.querySelectorAll(selector)[0] || null;
	}

	querySelectorAll(selector) {
		const found = [];
		const visit = (node) => {
			for (const child of node.children) {
				if (child.matches(selector)) found.push(child);
				visit(child);
			}
		};
		visit(this);
		return found;
	}

	getBoundingClientRect() {
		return this.rect;
	}

	cloneNode(deep = false) {
		const clone = new FakeElement(this.tagName, {
			classes: Array.from(this.classes),
			attrs: Object.fromEntries(this.attributes),
			rect: { ...this.rect },
			offsetWidth: this.offsetWidth,
			offsetHeight: this.offsetHeight,
		});
		if (deep) {
			for (const child of this.children) clone.appendChild(child.cloneNode(true));
		}
		return clone;
	}

	set innerHTML(html) {
		const channelMatch = String(html).match(/data-channel-id="([^"]+)"/);
		const card = new FakeElement('div', {
			classes: ['profile-card', 'profile-card--hover'],
			attrs: { 'data-channel-id': channelMatch ? channelMatch[1] : '' },
			rect: { left: 80, top: 80, right: 400, bottom: 340 },
			offsetWidth: 320,
			offsetHeight: 260,
		});
		this.children = [];
		this.appendChild(card);
	}

	get firstElementChild() {
		return this.children[0] || null;
	}
}

class FakeDocument extends FakeElement {
	constructor() {
		super('#document');
		this.body = new FakeElement('body');
		this.appendChild(this.body);
	}

	createElement(tagName) {
		return new FakeElement(tagName);
	}

	dispatch(type, event) {
		for (const fn of this.listeners.get(type) || []) {
			fn(event);
		}
	}
}

function matchesSingleSelector(el, selector) {
	if (!selector || selector.includes(' ')) return false;

	const tag = selector.match(/^[a-zA-Z][a-zA-Z0-9-]*/)?.[0] || '';
	if (tag && el.tagName.toLowerCase() !== tag.toLowerCase()) return false;

	for (const match of selector.matchAll(/\.([a-zA-Z0-9_-]+)/g)) {
		if (!el.classes.has(match[1])) return false;
	}

	for (const match of selector.matchAll(/\[([^\]=*]+)(\*=|=)?(?:"([^"]*)"|([^\]]*))?\]/g)) {
		const name = match[1];
		const op = match[2] || '';
		const expected = match[3] ?? match[4] ?? '';
		const actual = el.getAttribute(name);
		if (!op && actual == null) return false;
		if (op === '=' && actual !== expected) return false;
		if (op === '*=' && (actual == null || !actual.includes(expected))) return false;
	}

	return true;
}

function mouseEvent(target, x, y, relatedTarget = null) {
	return {
		target,
		relatedTarget,
		clientX: x,
		clientY: y,
		preventDefault() {},
		stopPropagation() {},
	};
}

async function flush() {
	await new Promise((resolve) => setImmediate(resolve));
}

async function loadProfileHover() {
	const document = new FakeDocument();
	const requests = [];
	const window = {
		document,
		innerWidth: 1024,
		innerHeight: 768,
		scrollX: 0,
		scrollY: 0,
		addEventListener() {},
		matchMedia: () => ({ matches: false }),
	};
	const context = vm.createContext({
		CSS: { escape: (value) => String(value).replace(/"/g, '\\"') },
		document,
		window,
		localStorage: {
			getItem: () => '',
			setItem: () => {},
		},
		fetch: async (url) => {
			requests.push(url);
			const channelID = decodeURIComponent(String(url).split('/').pop() || '');
			return {
				ok: true,
				headers: { get: () => '0' },
				text: async () => `<div class="profile-card profile-card--hover" data-channel-id="${channelID}"></div>`,
			};
		},
		setTimeout,
		clearTimeout,
		console,
	});
	const source = await readFile(new URL('./profile-hover.js', import.meta.url), 'utf8');
	vm.runInContext(source, context, { filename: 'profile-hover.js' });
	return { document, requests };
}

function addFeedTargets(document) {
	const article = new FakeElement('article', {
		attrs: { 'data-author-handle': 'op' },
	});
	const repost = new FakeElement('a', {
		classes: ['feed-repost-link'],
		attrs: { 'data-repost-handle': 'reposter' },
		rect: { left: 8, top: 8, right: 90, bottom: 28 },
	});
	const author = new FakeElement('a', {
		classes: ['feed-author-trigger'],
		rect: { left: 8, top: 42, right: 150, bottom: 66 },
	});
	article.appendChild(repost);
	article.appendChild(author);
	document.body.appendChild(article);
	return { repost, author };
}

function addOverlayHeadline(document) {
	const top = new FakeElement('div', {
		classes: ['feed-media-overlay-top'],
		attrs: { 'data-channel-id': 'twitter_poster' },
	});
	const headline = new FakeElement('a', {
		classes: ['feed-overlay-headline'],
		attrs: {
			'data-feed-channel-id': 'twitter_poster',
			href: '/channels/twitter_poster',
		},
		rect: { left: 16, top: 20, right: 220, bottom: 64 },
	});
	const author = new FakeElement('div', {
		classes: ['feed-overlay-author'],
		rect: { left: 64, top: 26, right: 180, bottom: 44 },
	});
	const actions = new FakeElement('div', {
		classes: ['feed-header-actions', 'feed-overlay-header-actions'],
		rect: { left: 180, top: 20, right: 220, bottom: 48 },
	});
	const actionButton = new FakeElement('button', {
		classes: ['feed-star-btn'],
		rect: { left: 184, top: 22, right: 212, bottom: 46 },
	});
	actions.appendChild(actionButton);
	headline.appendChild(author);
	headline.appendChild(actions);
	top.appendChild(headline);
	document.body.appendChild(top);
	return { headline, author, actionButton };
}

function addShortsRepostTarget(document) {
	const repost = new FakeElement('a', {
		classes: ['shorts-repost-link'],
		attrs: { 'data-channel-id': 'tiktok_reposter' },
		rect: { left: 8, top: 8, right: 120, bottom: 36 },
	});
	document.body.appendChild(repost);
	return repost;
}

test('profile hover ignores underlying triggers when the pointer is inside the open card', async () => {
	const { document, requests } = await loadProfileHover();
	const { repost, author } = addFeedTargets(document);

	document.dispatch('mousemove', mouseEvent(repost, 12, 18));
	await flush();

	assert.deepEqual(requests, ['/api/profile-card/twitter_reposter']);

	document.dispatch('mousemove', mouseEvent(author, 120, 120, repost));
	await flush();

	assert.deepEqual(requests, ['/api/profile-card/twitter_reposter']);
});

test('profile hover can still switch targets after the pointer leaves the open card', async () => {
	const { document, requests } = await loadProfileHover();
	const { repost, author } = addFeedTargets(document);

	document.dispatch('mousemove', mouseEvent(repost, 12, 18));
	await flush();

	document.dispatch('mousemove', mouseEvent(author, 20, 420, repost));
	await flush();

	assert.deepEqual(requests, [
		'/api/profile-card/twitter_reposter',
		'/api/profile-card/twitter_op',
	]);
});

test('profile hover opens from feed media overlay poster headline', async () => {
	const { document, requests } = await loadProfileHover();
	const { author } = addOverlayHeadline(document);

	document.dispatch('mousemove', mouseEvent(author, 70, 32));
	await flush();

	assert.deepEqual(requests, ['/api/profile-card/twitter_poster']);
});

test('profile hover ignores feed media overlay poster action buttons', async () => {
	const { document, requests } = await loadProfileHover();
	const { actionButton } = addOverlayHeadline(document);

	document.dispatch('mousemove', mouseEvent(actionButton, 190, 30));
	await flush();

	assert.deepEqual(requests, []);
});

test('profile hover ignores a Moments repost that moves under a stationary pointer', async () => {
	const { document, requests } = await loadProfileHover();
	const repost = addShortsRepostTarget(document);

	document.dispatch('mouseover', mouseEvent(repost, 20, 20));
	await flush();

	assert.deepEqual(requests, []);

	document.dispatch('mousemove', mouseEvent(repost, 21, 20));
	await flush();

	assert.deepEqual(requests, ['/api/profile-card/tiktok_reposter']);
});
