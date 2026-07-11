package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestFeedItemThreadRendersCapsuleBelowReply(t *testing.T) {
	item := model.FeedItem{
		TweetID:                "leaf_1",
		AuthorHandle:           "sample_author_a",
		AuthorDisplayName:      "Author A",
		AuthorAvatarURL:        "/api/media/avatar/twitter_sample_author_a",
		BodyText:               "reply body",
		IsRetweet:              true,
		RetweetedByHandle:      "sample_reposter_a",
		RetweetedByDisplayName: "Reposter A",
		ReposterChannelID:      "twitter_sample_reposter_a",
		ThreadChain: []model.FeedItem{
			{
				TweetID:           "root_1",
				AuthorHandle:      "sample_root_author",
				AuthorDisplayName: "Root Author",
				AuthorAvatarURL:   "/api/media/avatar/twitter_sample_root_author",
				BodyText:          "root body",
			},
			{
				TweetID:           "parent_1",
				AuthorHandle:      "sample_parent_author",
				AuthorDisplayName: "Parent Author",
				AuthorAvatarURL:   "/api/media/avatar/twitter_sample_parent_author",
				BodyText:          "parent body",
				IsReply:           true,
				ReplyToHandle:     "sample_root_author",
				ReplyToStatus:     "root_1",
			},
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`class="feed-thread"`,
		`data-feed-thread-row`,
		`root body`,
		`reply body`,
		`data-feed-thread-capsule`,
		`href="/thread/leaf_1"`,
		`class="feed-thread-capsule-avatar"`,
		`src="/api/media/avatar/twitter_sample_root_author"`,
		`src="/api/media/avatar/twitter_sample_parent_author"`,
		`src="/api/media/avatar/twitter_sample_author_a"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
	if strings.Contains(html, `feed-thread-capsule-dot`) {
		t.Fatalf("thread capsule should render participant avatars, not placeholder dots: %s", html)
	}
	rootAt := strings.Index(html, `data-tweet-id="root_1"`)
	leafAt := strings.Index(html, `data-tweet-id="leaf_1"`)
	capsuleAt := strings.Index(html, `data-feed-thread-capsule`)
	if rootAt < 0 || leafAt < 0 || capsuleAt < 0 || !(rootAt < leafAt && leafAt < capsuleAt) {
		t.Fatalf("thread order should be post, reply, capsule; root=%d leaf=%d capsule=%d html=%s", rootAt, leafAt, capsuleAt, html)
	}
	if got := strings.Count(html, `data-feed-repost-line`); got != 1 {
		t.Fatalf("data-feed-repost-line count = %d, want 1; html=%s", got, html)
	}
	if !strings.Contains(html, `Reposter A`) || !strings.Contains(html, `reposted`) {
		t.Fatalf("repost label missing: %s", html)
	}
}

func TestFeedItemTwoPostThreadOmitsCapsule(t *testing.T) {
	item := model.FeedItem{
		TweetID:           "leaf_1",
		AuthorHandle:      "sample_author_b",
		AuthorDisplayName: "Leaf Author",
		BodyText:          "reply body",
		IsReply:           true,
		ReplyToHandle:     "sample_author_a",
		ReplyToStatus:     "root_1",
		ThreadChain: []model.FeedItem{
			{
				TweetID:           "root_1",
				AuthorHandle:      "sample_author_a",
				AuthorDisplayName: "Root Author",
				BodyText:          "root body",
			},
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`class="feed-thread"`,
		`data-feed-thread-row`,
		`root body`,
		`reply body`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
	for _, notWant := range []string{
		`data-feed-thread-capsule`,
		`data-feed-thread-open`,
		`feed-thread-capsule-text`,
	} {
		if strings.Contains(html, notWant) {
			t.Fatalf("two-post thread should not render capsule %q in html: %s", notWant, html)
		}
	}
}

func TestFeedItemThreadPreviewUsesRootAndLeaf(t *testing.T) {
	item := model.FeedItem{
		TweetID:           "leaf_1",
		AuthorHandle:      "sample_author_b",
		AuthorDisplayName: "Leaf Author",
		BodyText:          "thank you",
		IsReply:           true,
		ReplyToHandle:     "sample_author_c",
		ReplyToStatus:     "parent_1",
		ThreadChain: []model.FeedItem{
			{
				TweetID:           "root_1",
				AuthorHandle:      "sample_author_a",
				AuthorDisplayName: "Root Author",
				BodyText:          "root body",
			},
			{
				TweetID:           "parent_1",
				AuthorHandle:      "sample_author_c",
				AuthorDisplayName: "Parent Author",
				BodyText:          "parent body",
				IsReply:           true,
				ReplyToHandle:     "sample_author_a",
				ReplyToStatus:     "root_1",
			},
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-tweet-id="root_1"`,
		`data-tweet-id="leaf_1"`,
		`thank you`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
	for _, notWant := range []string{
		`data-tweet-id="parent_1"`,
		`parent body`,
		`feed-reply-context`,
		`Replying to @sample_author_c`,
	} {
		if strings.Contains(html, notWant) {
			t.Fatalf("should not render hidden intermediate parent %q in html: %s", notWant, html)
		}
	}
	rootAt := strings.Index(html, `data-tweet-id="root_1"`)
	leafAt := strings.Index(html, `data-tweet-id="leaf_1"`)
	if rootAt < 0 || leafAt < 0 || rootAt > leafAt {
		t.Fatalf("thread preview should render root before leaf; root=%d leaf=%d html=%s", rootAt, leafAt, html)
	}
}

func TestFeedItemActionsDoNotShowStandaloneThreadButton(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "leaf_1",
		AuthorHandle: "sample_author_a",
		BodyText:     "leaf body",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if strings.Contains(html, `data-feed-action="thread"`) || strings.Contains(html, `data-feed-thread-open`) {
		t.Fatalf("standalone action row should not render local thread button: %s", html)
	}
}

func TestFeedItemDoesNotSynthesizeVideoEndpointWhenInventoryIsMissing(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "video_1",
		AuthorHandle: "sample_author_a",
		BodyText:     "video body",
		Media: []model.MediaRef{{
			Type: "video",
			URL:  "https://cdn.example/video.mp4",
		}},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if strings.Contains(html, `data-feed-media`) || strings.Contains(html, `/api/media/slide/video_1/`) {
		t.Fatalf("rendered unavailable canonical media: %s", html)
	}
}

func TestFeedItemRendersArticleCoverAsImage(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "article_1",
		AuthorHandle: "sample_author_a",
		BodyText:     "https://x.com/i/article/1000000000000000001",
		Media: []model.MediaRef{{
			Type: "article:cover",
			URL:  "https://pbs.twimg.com/media/article-cover.jpg",
		}},
		MediaSlideURLs: []string{"/api/media/slide/article_1/0?owner_kind=tweet"},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-feed-media-kind="image"`,
		`<img class="feed-media-image" src="/api/media/slide/article_1/0?owner_kind=tweet"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
	for _, notWant := range []string{
		`data-feed-media-kind="video"`,
		`data-feed-media-stream="/api/media/slide/article_1/0?owner_kind=tweet"`,
		`<video class="feed-media-video"`,
	} {
		if strings.Contains(html, notWant) {
			t.Fatalf("article cover should not render as video %q in html: %s", notWant, html)
		}
	}
}

func TestFeedItemMultiVideoTilesUseIndexedSlideEndpoints(t *testing.T) {
	item := model.FeedItem{
		TweetID:        "video_multi",
		AuthorHandle:   "sample_author_a",
		BodyText:       "video body",
		MediaStreamURL: "/api/media/stream/video_multi",
		MediaSlideURLs: []string{
			"/api/media/slide/video_multi/0?owner_kind=tweet",
			"/api/media/slide/video_multi/1?owner_kind=tweet",
		},
		Media: []model.MediaRef{
			{Type: "video", URL: "https://cdn.example/video_a.mp4"},
			{Type: "video", URL: "https://cdn.example/video_b.mp4"},
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-feed-media-stream="/api/media/slide/video_multi/0?owner_kind=tweet"`,
		`data-feed-media-stream="/api/media/slide/video_multi/1?owner_kind=tweet"`,
		`<source src="/api/media/slide/video_multi/0?owner_kind=tweet">`,
		`<source src="/api/media/slide/video_multi/1?owner_kind=tweet">`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
	if strings.Contains(html, `data-feed-media-stream="/api/media/stream/video_multi"`) {
		t.Fatalf("multi-video tile reused the single stream endpoint: %s", html)
	}
}

func TestFeedItemQuoteVideoTileUsesItsOwnSlideEndpoint(t *testing.T) {
	item := model.FeedItem{
		TweetID:           "parent_1",
		AuthorHandle:      "sample_author_a",
		BodyText:          "parent body",
		QuoteTweetID:      "quote_1",
		QuoteAuthorHandle: "sample_quote_author",
		QuoteBodyText:     "quote body",
		QuoteMedia: []model.MediaRef{
			{Type: "photo", URL: "https://cdn.example/photo.jpg"},
			{Type: "video", URL: "https://cdn.example/video.mp4"},
		},
		QuoteMediaStreamURL: "/api/media/slide/quote_1/0",
		QuoteMediaSlideURLs: []string{
			"/api/media/slide/quote_1/0?owner_kind=tweet",
			"/api/media/slide/quote_1/1?owner_kind=tweet",
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `data-feed-media-stream="/api/media/slide/quote_1/1?owner_kind=tweet"`) {
		t.Fatalf("quote video tile should use its own slide endpoint: %s", html)
	}
	if strings.Contains(html, `<source src="/api/media/slide/quote_1/0?owner_kind=tweet">`) {
		t.Fatalf("quote video tile reused the first slide stream: %s", html)
	}
}

func TestFeedItemQuoteVideoTileDoesNotReuseSingleStreamEndpoint(t *testing.T) {
	item := model.FeedItem{
		TweetID:             "parent_1",
		AuthorHandle:        "sample_author_a",
		BodyText:            "parent body",
		QuoteTweetID:        "quote_1",
		QuoteAuthorHandle:   "sample_quote_author",
		QuoteBodyText:       "quote body",
		QuoteMediaStreamURL: "/api/media/stream/quote_1",
		QuoteMediaSlideURLs: []string{
			"/api/media/slide/quote_1/0?owner_kind=tweet",
			"/api/media/slide/quote_1/1?owner_kind=tweet",
		},
		QuoteMedia: []model.MediaRef{
			{Type: "video", URL: "https://cdn.example/video_a.mp4"},
			{Type: "video", URL: "https://cdn.example/video_b.mp4"},
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-feed-media-stream="/api/media/slide/quote_1/0?owner_kind=tweet"`,
		`data-feed-media-stream="/api/media/slide/quote_1/1?owner_kind=tweet"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
	if strings.Contains(html, `data-feed-media-stream="/api/media/stream/quote_1"`) {
		t.Fatalf("quote video tile reused the single stream endpoint: %s", html)
	}
}

func TestFeedItemQuoteCardCarriesHoverIdentity(t *testing.T) {
	item := model.FeedItem{
		TweetID:                "parent_1",
		AuthorHandle:           "sample_author",
		BodyText:               "parent body",
		QuoteTweetID:           "quote_1",
		QuoteAuthorHandle:      "sample_quote",
		QuoteAuthorDisplayName: "Sample Quote",
		QuoteChannelID:         "twitter_sample_quote",
		QuoteBodyText:          "quote body",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-quote-author-channel-id="twitter_sample_quote"`,
		`data-quote-author-handle="sample_quote"`,
		`class="feed-quote-author feed-quote-author-link" href="/channels/twitter_sample_quote"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
}

func TestFeedExternalURLFallsBackToUniversalStatusPath(t *testing.T) {
	got := feedExternalURL(model.FeedItem{TweetID: "status_1"})
	want := "https://x.com/i/status/status_1"
	if got != want {
		t.Fatalf("feedExternalURL() = %q, want %q", got, want)
	}
}

func TestFeedExternalURLRejectsNonHTTPStoredURL(t *testing.T) {
	got := feedExternalURL(model.FeedItem{
		TweetID:      "status_1",
		AuthorHandle: "sample_author",
		CanonicalURL: "javascript:alert(1)",
	})
	want := "https://x.com/sample_author/status/status_1"
	if got != want {
		t.Fatalf("feedExternalURL() = %q, want %q", got, want)
	}
}

func TestFeedItemShowsHandleWhenDisplayNameMissing(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "tweet_1",
		AuthorHandle: "sample_author_a",
		BodyText:     "body",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `class="feed-author-handle">@sample_author_a</span>`) {
		t.Fatalf("missing author handle in feed header: %s", html)
	}
}

func TestFeedItemFollowActionsUseDisplayedAuthorState(t *testing.T) {
	item := model.FeedItem{
		TweetID:              "post_with_followed_source",
		SourceHandle:         "followed_source",
		AuthorHandle:         "post_author",
		AuthorDisplayName:    "Post Author",
		BodyText:             "body",
		IsRetweet:            true,
		QuoteTweetID:         "quoted_status",
		ChannelID:            "twitter_post_author",
		ReposterChannelID:    "twitter_followed_source",
		ChannelIsFollowed:    true,
		FollowTargetFollowed: false,
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `data-feed-follow-toggle data-feed-channel-id="twitter_post_author" data-feed-handle="post_author"`) {
		t.Fatalf("missing displayed-author follow button: %s", html)
	}
	unfollowAt := strings.Index(html, `data-feed-menu-action="unfollow"`)
	if unfollowAt < 0 {
		t.Fatalf("missing unfollow sync menu item: %s", html)
	}
	tagEnd := strings.Index(html[unfollowAt:], ">")
	if tagEnd < 0 {
		t.Fatalf("malformed unfollow menu item: %s", html[unfollowAt:])
	}
	unfollowTag := html[unfollowAt : unfollowAt+tagEnd]
	if !strings.Contains(unfollowTag, `style="display:none;"`) {
		t.Fatalf("unfollow should be hidden for an unfollowed displayed author, tag=%s html=%s", unfollowTag, html)
	}
}

func TestFeedItemSeenTrackingIsPageGatedHTMX(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "seen_1",
		AuthorHandle: "sample_author_a",
		BodyText:     "body",
	}

	var off bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &off); err != nil {
		t.Fatalf("render feed item without tracking: %v", err)
	}
	if strings.Contains(off.String(), `hx-post="/api/feed/seen`) {
		t.Fatalf("seen HTMX should not render without TrackFeedSeen: %s", off.String())
	}

	var on bytes.Buffer
	if err := FeedItem(PageProps{TrackFeedSeen: true}, item).Render(context.Background(), &on); err != nil {
		t.Fatalf("render feed item with tracking: %v", err)
	}
	html := on.String()
	if !strings.Contains(html, `hx-post="/api/feed/seen?tweet_id=seen_1"`) {
		t.Fatalf("missing seen HTMX post: %s", html)
	}
	if !strings.Contains(html, `hx-trigger="intersect once threshold:0.15"`) {
		t.Fatalf("missing valid intersect trigger: %s", html)
	}
	if strings.Contains(html, `data-feed-seen-url`) {
		t.Fatalf("seen tracking should not use JS-only URL attribute: %s", html)
	}
}

func TestFeedItemRendersIndependentBodyAndQuoteTranslatePills(t *testing.T) {
	item := model.FeedItem{
		TweetID:                "tweet_with_quote_translation",
		AuthorHandle:           "parent_author",
		AuthorDisplayName:      "Parent Author",
		BodyText:               "parent foreign text",
		Lang:                   "ko",
		QuoteTweetID:           "quoted_status",
		QuoteAuthorHandle:      "sample_quote_author",
		QuoteAuthorDisplayName: "Quote Author",
		QuoteBodyText:          "quote foreign text",
		QuoteLang:              "fr",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{TranslateTargetLang: "tr"}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()

	if got := strings.Count(html, `data-feed-action="translate"`); got != 2 {
		t.Fatalf("translate pill count = %d, want body + quote; html=%s", got, html)
	}
	for _, field := range []string{"body", "quote"} {
		if !strings.Contains(html, `data-translate-target-field="`+field+`"`) {
			t.Fatalf("missing %s translate target; html=%s", field, html)
		}
		if !strings.Contains(html, `data-translate-field="`+field+`"`) {
			t.Fatalf("missing %s translate container; html=%s", field, html)
		}
	}
	if got := strings.Count(html, `data-target-lang="tr"`); got != 2 {
		t.Fatalf("target language container count = %d, want 2; html=%s", got, html)
	}
	if strings.Contains(html, `data-target-lang="en"`) {
		t.Fatalf("feed text containers should not hardcode English target; html=%s", html)
	}
}

func TestFeedItemQuoteTranslationDoesNotActivateParentTranslatePill(t *testing.T) {
	item := model.FeedItem{
		TweetID:                "tweet_with_translated_quote",
		AuthorHandle:           "parent_author",
		AuthorDisplayName:      "Parent Author",
		BodyText:               "parent text",
		Lang:                   "en",
		QuoteTweetID:           "quoted_status",
		QuoteAuthorHandle:      "sample_quote_author",
		QuoteAuthorDisplayName: "Quote Author",
		QuoteBodyText:          "texte cite",
		QuoteLang:              "fr",
		QuoteTranslation:       "translated quote",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()

	if strings.Contains(html, `data-translate-target-field="body"`) {
		t.Fatalf("parent translate pill should not render for English body; html=%s", html)
	}
	if got := strings.Count(html, `data-translate-target-field="quote"`); got != 1 {
		t.Fatalf("quote translate target count = %d, want 1; html=%s", got, html)
	}
	quoteTargetAt := strings.Index(html, `data-translate-target-field="quote"`)
	quoteContainerAt := strings.Index(html, `data-translate-field="quote"`)
	if quoteTargetAt < 0 || quoteContainerAt < 0 || quoteTargetAt > quoteContainerAt {
		t.Fatalf("quote translate pill should render with quote metadata before quote text; html=%s", html)
	}
	buttonStart := strings.LastIndex(html[:quoteTargetAt], `<button`)
	buttonEnd := strings.Index(html[quoteTargetAt:], `>`)
	if buttonStart < 0 || buttonEnd < 0 {
		t.Fatalf("malformed quote translate button; html=%s", html)
	}
	buttonTag := html[buttonStart : quoteTargetAt+buttonEnd]
	if !strings.Contains(buttonTag, `active`) {
		t.Fatalf("quote translate pill should be active for cached quote translation, tag=%s html=%s", buttonTag, html)
	}
}

func TestFeedItemTranslatedPillUsesTranslatorSourceLabel(t *testing.T) {
	item := model.FeedItem{
		TweetID:           "tweet_with_korean_translation",
		AuthorHandle:      "author",
		AuthorDisplayName: "Author",
		BodyText:          "안녕하세요",
		Lang:              "kr",
		BodyTranslation:   "hello",
		BodySourceLang:    "Korean",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()

	if strings.Contains(html, `>KR<`) {
		t.Fatalf("translated pill should not render langdetect shorthand; html=%s", html)
	}
	if !strings.Contains(html, `>Korean<`) {
		t.Fatalf("translated pill should render translator source label; html=%s", html)
	}
}
