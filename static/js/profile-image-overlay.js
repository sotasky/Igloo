// Click-to-enlarge for the channel-page hero avatar and banner.
// Hover-card variant is excluded — clicks there go to /channels/<id> as usual.

(() => {
	function open(url) {
		const overlay = document.createElement('div');
		overlay.className = 'profile-image-overlay';
		const img = document.createElement('img');
		img.src = url;
		img.alt = '';
		overlay.appendChild(img);
		document.body.appendChild(overlay);

		function close() {
			overlay.remove();
			document.removeEventListener('keydown', onKey);
		}
		function onKey(e) {
			if (e.key === 'Escape') close();
		}
		overlay.addEventListener('click', close);
		document.addEventListener('keydown', onKey);
	}

	document.addEventListener('click', (e) => {
		const hero = e.target.closest('.profile-card--hero');
		if (!hero) return;
		const channelID = hero.getAttribute('data-channel-id');
		if (!channelID) return;
		if (e.target.closest('[data-story-channel-id][data-story-first-video-id]')) return;
		if (e.target.matches('.profile-card-avatar')) {
			e.preventDefault();
			open('/api/media/avatar/' + channelID);
			return;
		}
		if (e.target.matches('.profile-card-banner img')) {
			e.preventDefault();
			open('/api/media/banner/' + channelID);
		}
	});
})();
