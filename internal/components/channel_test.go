package components

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestVideoCardRender(t *testing.T) {
	pub := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	bookmarkID := int64(5)
	v := model.Video{
		VideoID:            "vid_abc123",
		ChannelID:          "youtube_testchan",
		Title:              "Test Video Title",
		Description:        "A test description",
		Duration:           125,
		ThumbnailURL:       "/api/media/thumbnail/vid_abc123",
		AvatarURL:          "/api/media/avatar/youtube_testchan",
		ChannelName:        "Test Channel",
		Platform:           "youtube",
		PublishedAt:        &pub,
		Watched:            true,
		IsShortForm:        false,
		BookmarkCategoryID: &bookmarkID,
		MediaKind:          "video",
		MediaSlideCount:    0,
	}

	p := newTestPageProps()
	var buf bytes.Buffer
	err := VideoCard(p, v).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	checks := []struct {
		name  string
		value string
	}{
		{"href", `/player/vid_abc123`},
		{"watched class", `video-card watched`},
		{"data-video-id", `data-video-id="vid_abc123"`},
		{"data-video-title", `data-video-title="Test Video Title"`},
		{"data-channel-name", `data-channel-name="Test Channel"`},
		{"data-channel-id", `data-channel-id="youtube_testchan"`},
		{"data-channel-href", `data-channel-href="/channels/youtube_testchan"`},
		{"data-stream-url", `data-stream-url="/api/media/stream/vid_abc123"`},
		{"data-bookmarked", `data-bookmarked="1"`},
		{"data-bookmark-category-id", `data-bookmark-category-id="5"`},
		{"data-platform", `data-platform="youtube"`},
		{"data-media-kind", `data-media-kind="video"`},
		{"data-shorts-eligible", `data-shorts-eligible="0"`},
		{"duration", `2:05`},
		{"thumbnail img", `src="/api/media/thumbnail/vid_abc123"`},
		{"video title text", `Test Video Title`},
		{"channel avatar", `class="video-channel-avatar"`},
		{"channel name text", `Test Channel`},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(html, c.value) {
				t.Errorf("expected %q in output", c.value)
			}
		})
	}
}

func TestVideoCardRendersMediaTypesForMixedSlides(t *testing.T) {
	v := model.Video{
		VideoID:         "sample_tweet_mixed_media",
		ChannelID:       "twitter_sample_author",
		Title:           "Mixed media",
		Platform:        "twitter",
		MediaKind:       "slideshow",
		MediaSlideCount: 3,
		MediaTypes:      []string{"image", "video", "video"},
	}

	var buf bytes.Buffer
	if err := VideoCard(newTestPageProps(), v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `data-media-types=`) {
		t.Fatal("expected media types data attribute")
	}
	for _, value := range []string{"image", "video"} {
		if !strings.Contains(html, value) {
			t.Fatalf("expected %q in rendered media types, got %s", value, html)
		}
	}
}

func TestVideoCardUsesTweetAssetOwner(t *testing.T) {
	v := model.Video{
		VideoID: "sample_post", OwnerKind: "tweet", MediaKind: "slideshow", MediaSlideCount: 2,
	}
	var buf bytes.Buffer
	if err := VideoCard(newTestPageProps(), v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-stream-url="/api/media/stream/sample_post?owner_kind=tweet"`,
		`data-slide-url-suffix="?owner_kind=tweet"`,
		`src="/api/media/thumbnail/sample_post?owner_kind=tweet"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered card missing %q: %s", want, html)
		}
	}
}

func TestVideoCardRendersInstagramCoauthorsAsTaggedAccounts(t *testing.T) {
	v := model.Video{
		VideoID:      "instagram_post_POST123",
		ChannelID:    "instagram_author_one",
		ChannelName:  "Author One",
		Platform:     "instagram",
		MetadataJSON: `{"coauthors":[{"username":"author_one","full_name":"Author One"},{"username":"collab.one","full_name":"Collab One"}],"tagged_users":[{"username":"collab.one","full_name":"Duplicate Collab"},{"username":"tagged.two","full_name":"Tagged Two","profile_pic_url":"https://cdn.example/tagged.jpg"}]}`,
	}
	tagged := videoTaggedAccounts(v)
	if len(tagged) != 2 || tagged[0].Handle != "collab.one" || tagged[0].ChannelID != "instagram_collab.one" || tagged[1].Handle != "tagged.two" {
		t.Fatalf("videoTaggedAccounts = %+v", tagged)
	}
	if tagged[1].AvatarURL != "/api/media/avatar/instagram_tagged.two" {
		t.Fatalf("tagged account avatar = %q", tagged[1].AvatarURL)
	}

	var buf bytes.Buffer
	if err := VideoCard(newTestPageProps(), v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `data-tagged-accounts=`) {
		t.Fatal("expected tagged accounts data attribute")
	}
	if !strings.Contains(html, `collab.one`) {
		t.Fatalf("expected collaborator account in tagged data, got %s", html)
	}
	if !strings.Contains(html, `tagged.two`) {
		t.Fatalf("expected tagged account in tagged data, got %s", html)
	}
	if strings.Contains(html, `cdn.example/tagged.jpg`) {
		t.Fatalf("rendered tagged account retained raw avatar URL: %s", html)
	}
	if !strings.Contains(html, `data-original-url="https://www.instagram.com/p/POST123/"`) {
		t.Fatalf("expected instagram canonical original URL, got %s", html)
	}
}

func TestVideoCardTitleFallback(t *testing.T) {
	t.Run("falls back to description", func(t *testing.T) {
		v := model.Video{
			VideoID:     "vid_001",
			Description: "Fallback description",
		}
		var buf bytes.Buffer
		_ = VideoCard(newTestPageProps(), v).Render(context.Background(), &buf)
		if !strings.Contains(buf.String(), "Fallback description") {
			t.Error("expected description as fallback title")
		}
	})

	t.Run("falls back to video ID", func(t *testing.T) {
		v := model.Video{VideoID: "vid_002"}
		var buf bytes.Buffer
		_ = VideoCard(newTestPageProps(), v).Render(context.Background(), &buf)
		if !strings.Contains(buf.String(), "vid_002") {
			t.Error("expected video ID as fallback title")
		}
	})
}

func TestVideoCardNoDuration(t *testing.T) {
	v := model.Video{
		VideoID: "vid_nodur",
		Title:   "No Duration",
	}
	var buf bytes.Buffer
	_ = VideoCard(newTestPageProps(), v).Render(context.Background(), &buf)
	if strings.Contains(buf.String(), "video-duration") {
		t.Error("duration span should not appear when duration is 0")
	}
}

func TestVideoCardMediaTypeBadge(t *testing.T) {
	p := newTestPageProps()
	v := model.Video{
		VideoID:         "slide_001",
		Title:           "Slideshow",
		MediaKind:       "slideshow",
		MediaSlideCount: 3,
	}

	var buf bytes.Buffer
	if err := VideoCard(p, v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		`video-media-type-badge`,
		`video-media-type-slideshow`,
		`aria-label="Slideshow"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected bookmark media badge %q in output", want)
		}
	}

	if got := videoCardMediaTypeIcon("slideshow"); strings.Count(got, "<rect") < 2 {
		t.Fatalf("slideshow icon should show stacked image files, got %q", got)
	}
}

func TestVideoCardDefaultThumb(t *testing.T) {
	v := model.Video{
		VideoID: "vid_nothumb",
		Title:   "No Thumb",
	}
	var buf bytes.Buffer
	_ = VideoCard(newTestPageProps(), v).Render(context.Background(), &buf)
	if !strings.Contains(buf.String(), "/static/default_thumb.png") {
		t.Error("expected default thumbnail URL")
	}
}

func TestVideoGridEmpty(t *testing.T) {
	ch := model.Channel{ChannelID: "youtube_empty", Name: "Empty Channel"}
	pager := model.Pager{Page: 1, PerPage: 40, Total: 0}

	var buf bytes.Buffer
	err := VideoGrid(newTestPageProps(), nil, ch, pager, "", false, false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, "No Videos") {
		t.Error("expected empty state message")
	}
	if !strings.Contains(html, "No channel videos found") {
		t.Error("expected empty state description")
	}
}

func TestVideoGridEmptyPartial(t *testing.T) {
	ch := model.Channel{ChannelID: "youtube_empty", Name: "Empty Channel"}
	pager := model.Pager{Page: 1, PerPage: 40, Total: 0}

	var buf bytes.Buffer
	err := VideoGrid(newTestPageProps(), nil, ch, pager, "", false, true).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if strings.Contains(html, "No Videos") {
		t.Error("partial response should not show empty state")
	}
}

func TestVideoGridInfiniteScroll(t *testing.T) {
	ch := model.Channel{ChannelID: "youtube_chan1", Name: "Channel One"}
	videos := []model.Video{
		{VideoID: "v1", Title: "Video 1"},
		{VideoID: "v2", Title: "Video 2"},
	}
	pager := model.Pager{Page: 1, PerPage: 2, Total: 5}

	var buf bytes.Buffer
	err := VideoGrid(newTestPageProps(), videos, ch, pager, "", false, false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, `hx-get="/channels/youtube_chan1?page=2"`) {
		t.Error("expected infinite scroll trigger with next page URL")
	}
	if !strings.Contains(html, `hx-trigger="revealed"`) {
		t.Error("expected revealed trigger")
	}
	if !strings.Contains(html, `hx-swap="outerHTML"`) {
		t.Error("expected outerHTML swap")
	}
}

func TestVideoGridNoInfiniteScrollOnLastPage(t *testing.T) {
	ch := model.Channel{ChannelID: "youtube_chan1", Name: "Channel One"}
	videos := []model.Video{
		{VideoID: "v1", Title: "Video 1"},
	}
	pager := model.Pager{Page: 1, PerPage: 40, Total: 1}

	var buf bytes.Buffer
	err := VideoGrid(newTestPageProps(), videos, ch, pager, "", false, false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if strings.Contains(html, "hx-get") {
		t.Error("should not have infinite scroll trigger on last page")
	}
}

func TestVideoGridInfiniteScrollWithSearch(t *testing.T) {
	ch := model.Channel{ChannelID: "youtube_chan1", Name: "Channel One"}
	videos := []model.Video{{VideoID: "v1", Title: "Video 1"}}
	pager := model.Pager{Page: 1, PerPage: 1, Total: 3}

	var buf bytes.Buffer
	err := VideoGrid(newTestPageProps(), videos, ch, pager, "test query", false, false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, `hx-get="/channels/youtube_chan1?page=2&amp;q=test query"`) {
		t.Error("expected search query in infinite scroll URL")
	}
}

func TestChannelPageStructure(t *testing.T) {
	p := newTestPageProps()
	p.PageTitle = "Test Channel"
	p.ActiveNav = "videos"

	ch := model.Channel{
		ChannelID:    "youtube_testchan",
		Name:         "Test Channel",
		Platform:     "youtube",
		IsSubscribed: true,
		IsStarred:    false,
	}
	videos := []model.Video{
		{VideoID: "v1", Title: "First Video", ChannelID: "youtube_testchan", ChannelName: "Test Channel"},
	}
	pager := model.Pager{Page: 1, PerPage: 40, Total: 1}

	var buf bytes.Buffer
	err := ChannelPage(p, ch, nil, videos, pager, "", "/api/media/avatar/youtube_testchan", false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	checks := []string{
		`class="page-actions-bar"`,
		`class="page-actions-avatar"`,
		`class="page-actions-name"`,
		`Test Channel`,
		`Settings`,
		`Unsubscribe`,
		`First Video`,
		`id="video-grid"`,
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing: %s", check)
		}
	}
}

func TestChannelPageShortsMode(t *testing.T) {
	p := newTestPageProps()
	p.PageScripts = []string{"js/infinite_page.js", "js/shorts_page.js"}
	ch := model.Channel{
		ChannelID:    "tt_testchan",
		Name:         "Shorts Channel",
		Platform:     "tiktok",
		IsSubscribed: true,
		IsStarred:    true,
	}
	profile := &model.ChannelProfile{
		ChannelID:   "tt_testchan",
		Platform:    "tiktok",
		Handle:      "shorts_channel",
		DisplayName: "Shorts Channel",
		Bio:         "Short videos",
		BannerURL:   "https://example.com/banner.jpg",
	}
	pager := model.Pager{Page: 1, PerPage: 40, Total: 0}

	var buf bytes.Buffer
	err := ChannelPage(p, ch, profile, nil, pager, "", "/api/media/avatar/tt_testchan", true).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, "profile-card--hero") {
		t.Error("expected tiktok profile hero")
	}
	if !strings.Contains(html, "/api/channels/tt_testchan/star?ctx=profile") {
		t.Error("expected profile hero star button")
	}
	if !strings.Contains(html, `data-profile-card-menu`) {
		t.Error("expected profile hero action menu")
	}
	if !strings.Contains(html, `data-profile-card-menu-action="settings"`) {
		t.Error("expected profile hero settings menu item")
	}
	if !strings.Contains(html, `data-profile-card-menu-action="refresh"`) {
		t.Error("expected profile hero refresh menu item")
	}
	if strings.Contains(html, `class="page-actions-bar"`) {
		t.Error("tiktok profile hero should replace page-actions-bar")
	}
	// Shorts mode should have shorts-grid class
	if !strings.Contains(html, "shorts-grid") {
		t.Error("expected shorts-grid class for shorts mode")
	}
	// Shorts mode should NOT have search bar
	if strings.Contains(html, "Search this channel") {
		t.Error("search bar should be hidden in shorts mode")
	}
	// Shorts mode should have shorts layout
	if !strings.Contains(html, `id="shorts-layout"`) {
		t.Error("expected shorts layout div")
	}
	// Shorts mode should load shorts_page.js
	if !strings.Contains(html, "shorts_page.js") {
		t.Error("expected shorts_page.js script")
	}
}

func TestChannelPageNonShortsNoShortsLayout(t *testing.T) {
	p := newTestPageProps()
	ch := model.Channel{
		ChannelID: "youtube_testchan",
		Name:      "Regular Channel",
		Platform:  "youtube",
	}
	pager := model.Pager{Page: 1, PerPage: 40, Total: 0}

	var buf bytes.Buffer
	err := ChannelPage(p, ch, nil, nil, pager, "", "/api/media/avatar/youtube_testchan", false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if strings.Contains(html, `id="shorts-layout"`) {
		t.Error("non-shorts channel should not have shorts layout")
	}
	if strings.Contains(html, "shorts_page.js") {
		t.Error("non-shorts channel should not load shorts_page.js")
	}
}

func TestChannelPageYouTubeUsesProfileHero(t *testing.T) {
	p := newTestPageProps()
	ch := model.Channel{
		ChannelID:    "youtube_UCtestchan",
		Name:         "Regular Channel",
		Platform:     "youtube",
		IsSubscribed: true,
		IsStarred:    true,
	}
	profile := &model.ChannelProfile{
		ChannelID:   "youtube_UCtestchan",
		Platform:    "youtube",
		Handle:      "@regularchannel",
		DisplayName: "Regular Channel",
		Bio:         "Uploads and streams",
		Followers:   42000,
		BannerURL:   "https://example.com/youtube-banner.jpg",
	}
	pager := model.Pager{Page: 1, PerPage: 40, Total: 0}

	var buf bytes.Buffer
	err := ChannelPage(p, ch, profile, nil, pager, "", "/api/media/avatar/youtube_UCtestchan", false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, "profile-card--hero") {
		t.Error("expected youtube profile hero")
	}
	if !strings.Contains(html, "/api/channels/youtube_UCtestchan/star?ctx=profile") {
		t.Error("expected youtube profile hero star button")
	}
	if !strings.Contains(html, `data-profile-card-menu`) {
		t.Error("expected youtube profile hero action menu")
	}
	if !strings.Contains(html, `data-profile-card-menu-action="settings"`) {
		t.Error("expected youtube profile hero settings menu item")
	}
	if !strings.Contains(html, `data-profile-card-menu-action="refresh"`) {
		t.Error("expected youtube profile hero refresh menu item")
	}
	if strings.Contains(html, `class="page-actions-bar"`) {
		t.Error("youtube profile hero should replace page-actions-bar")
	}
}

func TestVideoCardCarriesRepostProfileData(t *testing.T) {
	p := newTestPageProps()
	v := model.Video{
		VideoID:             "clip1",
		Title:               "Clip",
		ChannelID:           "tiktok_author",
		Platform:            "tiktok",
		RepostIntroduced:    true,
		RepostCount:         1,
		ReposterChannelID:   "tiktok_reposter",
		ReposterHandle:      "reposter",
		ReposterDisplayName: "Reposter",
	}

	var buf bytes.Buffer
	if err := VideoCard(p, v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-repost-label="Reposter reposted"`,
		`data-repost-channel-id="tiktok_reposter"`,
		`data-repost-handle="reposter"`,
		`data-repost-display-name="Reposter"`,
		`data-repost-avatar-url="/api/media/avatar/tiktok_reposter"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %s in %s", want, html)
		}
	}
}

func TestProfileHoverOwnsStaticProfileCardFollowWhenFeedBundleAbsent(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/profile-hover.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"wireStaticProfileCards",
		"syncProfileCardFollowState",
		"wireProfileMenu",
		".profile-card",
		"data-profile-follow-wired",
		"data-profile-card-menu-action",
		"action === 'refresh'",
		"/api/channels/' + encodeURIComponent(channelID) + '/refresh",
		"data-profile-card-menu-action=\"unfollow\"",
		"data-feed-menu-action=\"unfollow\"",
		"MpaSiteBase.syncChannelFollowState",
		"js/dist/feed.js",
		"data-quote-author-channel-id",
		"CHANNELS_HREF_RE",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("profile-hover.js missing %q", check)
		}
	}
}

func TestShortsPlayerInstagramHoverTriggers(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/shorts/items.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"p === 'twitter' || p === 'tiktok' || p === 'instagram'",
		"classList.add('shorts-channel')",
		"setAttribute('data-channel-id', entryData.channelId)",
		"instagram_",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("shorts items missing %q", check)
		}
	}
}

func TestShortsAuthorHoverTargetDoesNotStretchAcrossOverlay(t *testing.T) {
	cssBytes, err := os.ReadFile("../../static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	body := cssRuleBody(t, string(cssBytes), ".shorts-author-name")
	for _, check := range []string{
		"align-self: flex-start",
		"width: fit-content",
	} {
		if !strings.Contains(body, check) {
			t.Errorf(".shorts-author-name missing %q", check)
		}
	}
}

func cssRuleBody(t *testing.T, css, selector string) string {
	t.Helper()
	start := strings.Index(css, selector+" {")
	if start < 0 {
		t.Fatalf("missing CSS rule for %s", selector)
	}
	open := strings.Index(css[start:], "{")
	if open < 0 {
		t.Fatalf("missing CSS rule body for %s", selector)
	}
	bodyStart := start + open + 1
	close := strings.Index(css[bodyStart:], "}")
	if close < 0 {
		t.Fatalf("missing CSS rule close for %s", selector)
	}
	return css[bodyStart : bodyStart+close]
}
