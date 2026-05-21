package components

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestShortsPageRendersFullSkeletonListWithoutPaging(t *testing.T) {
	p := newTestPageProps()
	p.ActiveNav = "shorts"
	p.ESBundle = "js/dist/shorts.js"
	p.PageScripts = []string{"js/infinite_page.js"}
	videos := []model.Video{
		{VideoID: "short_001", Title: "Short 001", Platform: "tiktok"},
		{VideoID: "short_002", Title: "Short 002", Platform: "tiktok"},
		{VideoID: "short_003", Title: "Short 003", Platform: "tiktok"},
	}
	pager := model.Pager{Page: 1, PerPage: 10000, Total: 3}

	var buf bytes.Buffer
	if err := ShortsPage(p, videos, nil, false, pager, "", "following", 1, 2).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if strings.Contains(html, `class="js-infinite-scroll"`) || strings.Contains(html, `data-next-url`) {
		t.Fatal("shorts page should not use scroll pagination")
	}
	if !strings.Contains(html, `data-hydrate-batch-size="2"`) {
		t.Fatalf("missing background hydration config: %s", html)
	}
	if !strings.Contains(html, `data-video-id="short_001"`) || !strings.Contains(html, `data-video-title="Short 001"`) {
		t.Fatal("initial hydrated card missing")
	}
	for _, id := range []string{"short_002", "short_003"} {
		if !strings.Contains(html, `data-video-id="`+id+`"`) || !strings.Contains(html, `data-shorts-card-skeleton="1"`) {
			t.Fatalf("missing skeleton card for %s", id)
		}
	}
}

func TestShortsPageRendersStoriesTabTrigger(t *testing.T) {
	p := newTestPageProps()
	p.ActiveNav = "shorts"
	p.ESBundle = "js/dist/shorts.js"
	p.PageScripts = []string{"js/infinite_page.js"}
	pager := model.Pager{Page: 1, PerPage: 10000, Total: 0}

	var buf bytes.Buffer
	if err := ShortsPage(p, nil, nil, true, pager, "", "all", 1, 2).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, `href="/shorts?tab=stories"`) {
		t.Fatalf("stories tab trigger missing: %s", html)
	}
	if strings.Contains(html, `shorts-tab-dot`) {
		t.Fatalf("stories tab should not render the dot indicator: %s", html)
	}
}

func TestShortsPlayerHeaderRendersStoriesTabTrigger(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/items.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)

	if !strings.Contains(src, `href="/shorts?tab=stories"`) {
		t.Fatal("shorts player header should include the Stories tab trigger for the story tray")
	}
	if !strings.Contains(src, "(_state && _state.storyMode) ? 'stories'") {
		t.Fatal("shorts player header should select the Stories tab while story playback is active")
	}
}

func TestShortsStoryTrayOpensByDefaultForNormalMoments(t *testing.T) {
	indexBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	overlayBytes, err := os.ReadFile("../../static/js/src/shorts/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	indexSrc := string(indexBytes)
	overlaySrc := string(overlayBytes)

	for _, check := range []string{
		"function openDefaultStoryTray()",
		"if (currentTab === 'stories' || state.storyMode || !state.overlayOpen) return",
		"afterOverlayOpen: openDefaultStoryTray",
	} {
		if !strings.Contains(indexSrc, check) {
			t.Errorf("default story tray wiring missing %q", check)
		}
	}
	if !strings.Contains(overlaySrc, "typeof _fns.afterOverlayOpen === 'function'") {
		t.Fatal("overlay should call the post-open hook after Moments opens")
	}
}

func TestShortsStoryGridButtonLabelsStoryPlaybackAsMoments(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"function storyGridButtonLabel()",
		"state.storyMode ? t('nav_moments', 'Moments') : t('action_grid', 'Grid')",
		"function updateStoryGridButton()",
		"function activateStoryGridButton()",
		"exitStoryMode({ restore: true })",
		"updateStoryGridButton()",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("shorts story grid button wiring missing %q", check)
		}
	}
}

func TestShortsStoryTabOpensFirstTrayStory(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"function openFirstStoryFromTray()",
		"function firstStoryTrayRow()",
		"return Promise.resolve(openStoryRow(row, { continueAcrossAccounts: true }))",
		"if (state.overlayOpen && tab === 'stories')",
		"openFirstStoryFromTray()",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("story tab first-story wiring missing %q", check)
		}
	}
}

func TestShortsStoryModePreviousTabExitsWithoutSwitchingTabs(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"if (state.storyMode)",
		"if (tab === currentTab)",
		"exitStoryMode({ restore: true })",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("story mode tab return wiring missing %q", check)
		}
	}
}

func TestShortsStoryAvatarOpenDoesNotReuseSidebarQueue(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"storyContinueAcrossAccounts: false",
		"continueAcrossAccounts: true",
		"continueAcrossAccounts: false",
		"if (!state.storyContinueAcrossAccounts) return false",
		"state.storyContinueAcrossAccounts = !!opts.continueAcrossAccounts",
		"state.storyContinueAcrossAccounts = false",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("shorts story queue ownership missing %q", check)
		}
	}
}

func TestShortsStoryModeAutoAdvancesWithoutChangingMomentAutoplay(t *testing.T) {
	indexBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	playbackBytes, err := os.ReadFile("../../static/js/src/shorts/playback.js")
	if err != nil {
		t.Fatal(err)
	}
	overlayBytes, err := os.ReadFile("../../static/js/src/shorts/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	itemsBytes, err := os.ReadFile("../../static/js/src/shorts/items.js")
	if err != nil {
		t.Fatal(err)
	}
	indexSrc := string(indexBytes)
	playbackSrc := string(playbackBytes)
	overlaySrc := string(overlayBytes)
	itemsSrc := string(itemsBytes)

	if !strings.Contains(indexSrc, "state.autoPlayNext = false") {
		t.Fatal("story mode should preserve the normal moments autoplay setting while it is open")
	}
	for _, check := range []string{
		"return !!(_state && (_state.storyMode || _state.autoPlayNext))",
		"if (autoAdvanceEnabled()) _goNext()",
	} {
		if !strings.Contains(playbackSrc, check) {
			t.Errorf("story playback auto-advance missing %q", check)
		}
	}
	for _, check := range []string{
		"function autoAdvanceEnabled()",
		"return !!(_state && (_state.storyMode || _state.autoPlayNext))",
		"slideshowAudio.loop = !autoAdvanceEnabled()",
		"video.loop = !autoAdvanceEnabled()",
		"if (autoAdvanceEnabled()) _fns.goNext()",
	} {
		if !strings.Contains(itemsSrc, check) {
			t.Errorf("story media auto-advance wiring missing %q", check)
		}
	}
	for _, check := range []string{
		"var autoAdvance = _state.storyMode || _state.autoPlayNext",
		"refs.autoplayBtn.classList.toggle('active', autoAdvance && isCurrent)",
		"autoAdvance ? t('state_on', 'ON') : t('state_off', 'OFF')",
	} {
		if !strings.Contains(overlaySrc, check) {
			t.Errorf("story autoplay display state missing %q", check)
		}
	}
	for _, bad := range []string{
		"if (_state.storyMode && !audio) return",
		"slideshowAudio.loop = !_state.autoPlayNext",
		"video.loop = !_state.autoPlayNext",
		"if (_state.autoPlayNext) _fns.goNext()",
	} {
		if strings.Contains(playbackSrc, bad) || strings.Contains(itemsSrc, bad) {
			t.Fatalf("story media should not loop through normal autoplay-only wiring: found %q", bad)
		}
	}
}

func TestShortsStoryNextExitsWhenQueueIsExhausted(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	start := strings.Index(src, "function goStoryNextManual()")
	if start < 0 {
		t.Fatal("goStoryNextManual missing")
	}
	end := strings.Index(src[start:], "function goStoryPrevManual()")
	if end < 0 {
		t.Fatal("goStoryPrevManual should follow goStoryNextManual")
	}
	fn := src[start : start+end]
	for _, check := range []string{
		"if (state.currentIndex < state.cards.length - 1)",
		"scrollToIndex(state.currentIndex + 1, 'instant')",
		"if (openNextQueuedStory()) return",
		"showGrid()",
	} {
		if !strings.Contains(fn, check) {
			t.Errorf("story next exhaustion behavior missing %q", check)
		}
	}
}

func TestShortsStoryClicksNavigateWithoutVisualArrowButtons(t *testing.T) {
	indexBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	itemsBytes, err := os.ReadFile("../../static/js/src/shorts/items.js")
	if err != nil {
		t.Fatal(err)
	}
	cssBytes, err := os.ReadFile("../../static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	indexSrc := string(indexBytes)
	itemsSrc := string(itemsBytes)
	css := string(cssBytes)

	for _, check := range []string{
		"function navigateStoryFromClick(entry, event)",
		"if (!_state || !_state.storyMode || !_fns) return false",
		"if (navigateStoryFromClick(entryObj, e)) return",
		"if (typeof _fns.goStoryPrev === 'function') _fns.goStoryPrev()",
		"if (typeof _fns.goStoryNext === 'function')",
	} {
		if !strings.Contains(itemsSrc, check) {
			t.Errorf("story click navigation missing %q", check)
		}
	}
	for _, check := range []string{
		"goStoryNext: goStoryNextManual",
		"goStoryPrev: goStoryPrevManual",
		"if (state.storyMode && event.key === 'ArrowRight')",
		"if (state.storyMode && event.key === 'ArrowLeft')",
	} {
		if !strings.Contains(indexSrc, check) {
			t.Errorf("story navigation wiring missing %q", check)
		}
	}
	if !strings.Contains(itemsSrc, "toggleShortPlayback(entryObj)") {
		t.Fatal("normal moments should keep click-to-toggle playback")
	}
	for _, forbidden := range []string{
		"shorts-story-arrow",
		"data-story-action",
		"onStoryPlayerControlClick",
	} {
		if strings.Contains(indexSrc, forbidden) || strings.Contains(itemsSrc, forbidden) || strings.Contains(css, forbidden) {
			t.Errorf("story visual arrow control should be removed; found %q", forbidden)
		}
	}
}

func TestShortsMomentViewSyncRefreshesStorySurfaces(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"function refreshStorySurfaces(silent)",
		"function scheduleStorySurfaceRefresh(silent)",
		"refreshStorySurfaces(true)",
		"window.SyncPoller.on('moment_view'",
		"scheduleStorySurfaceRefresh(true)",
		"tabGridCache.delete('stories')",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("story sync refresh wiring missing %q", check)
		}
	}
	handlerStart := strings.Index(src, "window.SyncPoller.on('moment_view'")
	if handlerStart < 0 {
		t.Fatal("moment_view sync handler missing")
	}
	handlerEnd := strings.Index(src[handlerStart:], "window.SyncPoller.on('moments_cursor'")
	if handlerEnd < 0 {
		t.Fatal("moments_cursor sync handler missing after moment_view handler")
	}
	handler := src[handlerStart : handlerStart+handlerEnd]
	if strings.Contains(handler, "state.viewedIds.add") {
		t.Fatal("remote moment_view sync must not mutate local viewedIds")
	}
}

func TestShortsOverlayPrewarmsNearbyVideosBeforeScrollActivation(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"function warmShortVideo(entry, eager)",
		"function warmNearbyShortVideos(index)",
		"video.preload = eager ? 'auto' : 'metadata'",
		"var start = Math.max(0, index - 2)",
		"var end = Math.min(_state.items.length - 1, index + 5)",
		"warmShortVideo(entry, i >= index && i <= index + 4)",
		"video._shortsPrewarmStarted = true",
		"warmNearbyShortVideos(index)",
		"warmNearbyShortVideos(centerIndex)",
		"warmNearbyShortVideos(_state.currentIndex)",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("shorts overlay video prewarm wiring missing %q", check)
		}
	}
}

func TestShortsItemsDoNotAnimateViewportSizeDuringScrollSnap(t *testing.T) {
	cssBytes, err := os.ReadFile("../../static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	body := cssRuleBody(t, string(cssBytes), ".shorts-item")
	for _, bad := range []string{
		"transform:",
		"transition:",
		"opacity: 0.",
	} {
		if strings.Contains(body, bad) {
			t.Errorf(".shorts-item should not animate viewport size during scroll snap; found %q in %s", bad, body)
		}
	}
}

func TestShortsMediaEdgesDoNotExposeWrapperBackgroundDuringScrollSnap(t *testing.T) {
	cssBytes, err := os.ReadFile("../../static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	wrapperBody := cssRuleBody(t, css, ".shorts-video-wrapper")
	videoBody := cssRuleBody(t, css, ".shorts-video-wrapper video")
	nativeVideoBody := cssRuleBody(t, css, ".native-short-video")
	slideImageBody := cssRuleBody(t, css, ".slide-image")

	for _, check := range []string{
		"overflow: hidden",
		"border-radius: 0",
		"background: #000",
	} {
		if !strings.Contains(wrapperBody, check) {
			t.Errorf(".shorts-video-wrapper should avoid visible clipped edges; missing %q in %s", check, wrapperBody)
		}
	}
	for _, check := range []string{
		"display: block",
		"width: 100%",
		"height: 100%",
	} {
		if !strings.Contains(videoBody, check) {
			t.Errorf(".shorts-video-wrapper video should fill without inline baseline gaps; missing %q in %s", check, videoBody)
		}
		if !strings.Contains(nativeVideoBody, check) {
			t.Errorf(".native-short-video should fill without inline baseline gaps; missing %q in %s", check, nativeVideoBody)
		}
		if !strings.Contains(slideImageBody, check) {
			t.Errorf(".slide-image should fill without inline baseline gaps; missing %q in %s", check, slideImageBody)
		}
	}
	if !strings.Contains(slideImageBody, "inset: 0") {
		t.Errorf(".slide-image should pin every absolute slide to the wrapper; missing %q in %s", "inset: 0", slideImageBody)
	}
}

func TestShortsActivationDoesNotWaitForSnapBeforePlayback(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	for _, check := range []string{
		"function snapOffset(entry)",
		"function isSnapSettled(entry)",
		"function activateVisibleShort(index)",
		"recordShortsDebugEvent(entry, 'activate:pre-snap'",
		"activateIndex(index, { force: false, snapSettled: settled })",
		"activateVisibleShort(index)",
		"recordShortsDebugEvent(entry, 'activate', { snapSettled: !!snapSettled })",
		"scheduleSnapSettleCorrection(entry)",
		"recordShortsDebugEvent(entry, 'scroll:settle-align'",
	} {
		if !strings.Contains(src, check) {
			t.Errorf("shorts activation immediate snap reporting missing %q", check)
		}
	}
	for _, forbidden := range []string{
		"_snapActivationFrame",
		"pendingSnapActivation",
		"activate:wait-snap",
		"scheduleSnapSettledActivation",
		"performance.now() - pending.startedAt > 350",
	} {
		if strings.Contains(src, forbidden) {
			t.Errorf("shorts activation should not wait for snap before playback; found %q", forbidden)
		}
	}
}

func TestShortsPreSnapActivationCorrectsExposedEdgesWithoutWaitingToPlay(t *testing.T) {
	overlayBytes, err := os.ReadFile("../../static/js/src/shorts/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	indexBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	overlaySrc := string(overlayBytes)
	indexSrc := string(indexBytes)

	for _, check := range []string{
		"function scheduleSnapSettleCorrection(entry)",
		"function alignVerticalActiveItem(entry, reason)",
		"_state.pendingSnapSettleEntry = entry",
		"(_state.touchActive || quietFrames < 2) && t - startedAt < 420",
		"alignVerticalActiveItem(entry, _state.touchActive ? 'timeout' : 'idle')",
		"if (!_state.storyMode && !snapSettled) scheduleSnapSettleCorrection(entry)",
		"playShortVideoFromStart(entry)",
	} {
		if !strings.Contains(overlaySrc, check) {
			t.Errorf("shorts pre-snap settle correction missing %q", check)
		}
	}
	for _, check := range []string{
		"touchActive: false",
		"state.touchActive = true",
		"function releaseTouchSoon(delayMs)",
		"releaseTouchSoon(90)",
		"layout.addEventListener('touchcancel', onTouchCancel",
	} {
		if !strings.Contains(indexSrc, check) {
			t.Errorf("shorts touch settle guard missing %q", check)
		}
	}
}

func TestShortsVideoPlaybackStartsImmediatelyWithPosterUntilFirstFrame(t *testing.T) {
	overlayBytes, err := os.ReadFile("../../static/js/src/shorts/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	itemsBytes, err := os.ReadFile("../../static/js/src/shorts/items.js")
	if err != nil {
		t.Fatal(err)
	}
	cssBytes, err := os.ReadFile("../../static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	overlaySrc := string(overlayBytes)
	itemsSrc := string(itemsBytes)
	css := string(cssBytes)

	for _, check := range []string{
		"function revealShortVideoIfReady(entry, video)",
		"function playShortVideo(entry, video)",
		"function playShortVideoFromStart(entry)",
		"try {\n    video.currentTime = 0",
		"playShortVideo(entry, video)",
	} {
		if !strings.Contains(overlaySrc, check) {
			t.Errorf("shorts overlay immediate playback missing %q", check)
		}
	}
	for _, forbidden := range []string{
		"function scheduleSettledShortPlayback(entry)",
		"pendingPlayTimer",
		"is-settling-playback",
	} {
		if strings.Contains(overlaySrc, forbidden) {
			t.Errorf("shorts overlay should not delay playback with %q", forbidden)
		}
	}
	if !strings.Contains(itemsSrc, "shorts-video-poster-frame") {
		t.Fatal("shorts video items should render a poster layer for first-frame fallback")
	}
	for _, check := range []string{
		"video.preload = 'metadata'",
		"wrapper.classList.add('is-awaiting-first-frame')",
		"function revealVideoFrame()",
		"video.addEventListener('loadeddata', revealVideoFrame)",
		"video.addEventListener('playing', revealVideoFrame)",
	} {
		if !strings.Contains(itemsSrc, check) {
			t.Errorf("shorts video items first-frame fallback missing %q", check)
		}
	}
	if !strings.Contains(css, ".shorts-video-wrapper.is-awaiting-first-frame video") ||
		!strings.Contains(css, "opacity: 0;") {
		t.Fatal("first-frame fallback CSS should hide video over the stable poster")
	}
}

func TestShortsDebugToolsExposeOptInMediaSnapshots(t *testing.T) {
	debugBytes, err := os.ReadFile("../../static/js/src/shorts/debug.js")
	if err != nil {
		t.Fatal(err)
	}
	indexBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	itemsBytes, err := os.ReadFile("../../static/js/src/shorts/items.js")
	if err != nil {
		t.Fatal(err)
	}
	overlayBytes, err := os.ReadFile("../../static/js/src/shorts/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	debugSrc := string(debugBytes)
	for _, check := range []string{
		"import { apiFetch } from '../utils.js'",
		"window.MpaShortsDebug",
		"shorts_debug=1",
		"shorts_debug=0",
		"localStorage.getItem('shortsDebug')",
		"_serverLog = '~/.local/share/igloo/logs/moments/debug.jsonl'",
		"event: 'moments_video_debug'",
		"flush: flush",
		"download: function ()",
		"status: function ()",
		"current: function ()",
		"recent: function ()",
		"copy: function ()",
		"function sampleBands(video)",
		"buffered: rangesOf(video.buffered)",
		"containerRect: rectOf(container)",
		"itemRect: rectOf(entry.el)",
		"snapDelta: snapDeltaOf(entry)",
		"visibleTopPx: visible.visibleTopPx",
		"visibleBottomPx: visible.visibleBottomPx",
		"visibleRatio: visible.visibleRatio",
		"visible: visible",
		"wrapperRect: rectOf(wrapper)",
		"videoRect: rectOf(video)",
		"infoRect: rectOf(info)",
		"authorRect: rectOf(author)",
		"titleRect: rectOf(title)",
		"actionsRect: rectOf(actions)",
		"progressRect: rectOf(progress)",
		"chrome: chromeSnapshot(entry)",
		"isSkeletonCard: !!(entry.data && entry.data.isSkeleton)",
		"containerScroll: container ?",
		"wrapperRadius: wrapperStyle && wrapperStyle.borderRadius",
		"videoDisplay: videoStyle && videoStyle.display",
		"requestVideoFrameCallback",
	} {
		if !strings.Contains(debugSrc, check) {
			t.Errorf("shorts debug tool missing %q", check)
		}
	}
	if !strings.Contains(string(indexBytes), "initShortsDebug(state)") {
		t.Fatal("shorts debug should initialize with player state")
	}
	if !strings.Contains(string(itemsBytes), "attachShortVideoDebug(entryObj)") {
		t.Fatal("shorts items should attach video event diagnostics")
	}
	for _, check := range []string{
		"recordShortsDebugEvent(entry, 'intersect:candidate'",
		"recordShortsDebugEvent(entry, 'activate:pre-snap'",
		"recordShortsDebugEvent(entry, 'activate'",
		"recordShortsDebugEvent(entry, 'play:attempt')",
		"recordShortsDebugEvent(entry, 'snap:settled'",
		"recordShortsDebugEvent(entry, 'chrome:snapshot'",
	} {
		if !strings.Contains(string(overlayBytes), check) {
			t.Errorf("shorts overlay debug event missing %q", check)
		}
	}
}
