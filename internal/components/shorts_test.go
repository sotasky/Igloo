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

func TestShortsStoryModeDoesNotForceAutoplay(t *testing.T) {
	indexBytes, err := os.ReadFile("../../static/js/src/shorts/index.js")
	if err != nil {
		t.Fatal(err)
	}
	playbackBytes, err := os.ReadFile("../../static/js/src/shorts/playback.js")
	if err != nil {
		t.Fatal(err)
	}
	itemsBytes, err := os.ReadFile("../../static/js/src/shorts/items.js")
	if err != nil {
		t.Fatal(err)
	}
	indexSrc := string(indexBytes)
	playbackSrc := string(playbackBytes)
	itemsSrc := string(itemsBytes)

	if !strings.Contains(indexSrc, "state.autoPlayNext = false") {
		t.Fatal("story mode should not force autoplay when it opens")
	}
	for _, bad := range []string{"_state.storyMode || _state.autoPlayNext"} {
		if strings.Contains(playbackSrc, bad) || strings.Contains(itemsSrc, bad) {
			t.Fatalf("story mode should not be treated as autoplay: found %q", bad)
		}
	}
	if !strings.Contains(playbackSrc, "function autoAdvanceEnabled()") {
		t.Fatal("playback should route auto-advance through the explicit autoplay flag")
	}
	if !strings.Contains(itemsSrc, "if (_state.autoPlayNext) _fns.goNext()") {
		t.Fatal("media-ended advance should use the explicit autoplay flag")
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
