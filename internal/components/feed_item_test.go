package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestFeedItemThreadShowsRepostLineOnThreadWrapper(t *testing.T) {
	item := model.FeedItem{
		TweetID:                "leaf_1",
		AuthorHandle:           "author_a",
		AuthorDisplayName:      "Author A",
		BodyText:               "reply body",
		IsRetweet:              true,
		RetweetedByHandle:      "reposter_a",
		RetweetedByDisplayName: "Reposter A",
		ReposterChannelID:      "twitter_reposter_a",
		ThreadChain: []model.FeedItem{
			{
				TweetID:           "root_1",
				AuthorHandle:      "root_author",
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
	if !strings.Contains(html, `class="feed-thread-repost"`) {
		t.Fatalf("thread repost wrapper missing: %s", html)
	}
	if got := strings.Count(html, `data-feed-repost-line`); got != 1 {
		t.Fatalf("data-feed-repost-line count = %d, want 1; html=%s", got, html)
	}
	repostAt := strings.Index(html, `class="feed-thread-repost"`)
	rowAt := strings.Index(html, `data-feed-thread-row`)
	if repostAt < 0 || rowAt < 0 || repostAt > rowAt {
		t.Fatalf("repost line should render before thread rows; repost=%d row=%d html=%s", repostAt, rowAt, html)
	}
	if !strings.Contains(html, `Reposter A`) || !strings.Contains(html, `reposted`) {
		t.Fatalf("repost label missing: %s", html)
	}
}

func TestFeedItemThreadRowsRenderBottomActions(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "leaf_1",
		AuthorHandle: "author_a",
		BodyText:     "leaf body",
		ThreadChain: []model.FeedItem{
			{
				TweetID:      "root_1",
				AuthorHandle: "root_author",
				BodyText:     "root body",
			},
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if got := strings.Count(html, `class="feed-actions"`); got != 2 {
		t.Fatalf("feed action rows = %d, want ancestor + leaf; html=%s", got, html)
	}
	for _, action := range []string{
		`data-feed-action="share"`,
		`data-feed-action="heart"`,
		`data-feed-action="bookmark"`,
		`data-feed-action="openx"`,
	} {
		if got := strings.Count(html, action); got != 2 {
			t.Fatalf("%s count = %d, want 2; html=%s", action, got, html)
		}
	}
	for _, link := range []string{
		`data-feed-link="https://x.com/root_author/status/root_1"`,
		`data-feed-link="https://x.com/author_a/status/leaf_1"`,
		`href="https://x.com/root_author/status/root_1"`,
		`href="https://x.com/author_a/status/leaf_1"`,
	} {
		if !strings.Contains(html, link) {
			t.Fatalf("missing synthesized action link %q; html=%s", link, html)
		}
	}
}

func TestFeedItemThreadCollapsesOlderAncestors(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "leaf_1",
		AuthorHandle: "sample_author",
		BodyText:     "leaf body",
		ThreadChain: []model.FeedItem{
			{TweetID: "root_1", AuthorHandle: "sample_root", BodyText: "root body"},
			{TweetID: "mid_1", AuthorHandle: "sample_author_older", BodyText: "mid body"},
			{TweetID: "parent_1", AuthorHandle: "sample_parent", BodyText: "parent body"},
		},
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if got := strings.Count(html, `data-feed-thread-collapsed="1"`); got != 2 {
		t.Fatalf("collapsed thread rows = %d, want 2; html=%s", got, html)
	}
	if !strings.Contains(html, `data-feed-thread-more`) || !strings.Contains(html, `Load more replies`) {
		t.Fatalf("load-more replies control missing: %s", html)
	}
	buttonAt := strings.Index(html, `data-feed-thread-more`)
	visibleParentAt := strings.Index(html, `data-tweet-id="parent_1"`)
	if buttonAt < 0 || visibleParentAt < 0 || buttonAt > visibleParentAt {
		t.Fatalf("load-more control should render before visible latest replies; button=%d parent=%d html=%s", buttonAt, visibleParentAt, html)
	}
}

func TestFeedItemRendersSingleVideoFromSlideEndpointWhenStreamMissing(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "video_1",
		AuthorHandle: "author_a",
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
	for _, want := range []string{
		`data-feed-media-kind="video"`,
		`data-feed-media-stream="/api/media/slide/video_1/0"`,
		`<source src="/api/media/slide/video_1/0" type="video/mp4">`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
}

func TestFeedItemQuoteVideoTileUsesItsOwnSlideEndpoint(t *testing.T) {
	item := model.FeedItem{
		TweetID:           "parent_1",
		AuthorHandle:      "author_a",
		BodyText:          "parent body",
		QuoteTweetID:      "quote_1",
		QuoteAuthorHandle: "quote_author",
		QuoteBodyText:     "quote body",
		QuoteMedia: []model.MediaRef{
			{Type: "photo", URL: "https://cdn.example/photo.jpg"},
			{Type: "video", URL: "https://cdn.example/video.mp4"},
		},
		QuoteMediaStreamURL: "/api/media/slide/quote_1/0",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `data-feed-media-stream="/api/media/slide/quote_1/1"`) {
		t.Fatalf("quote video tile should use its own slide endpoint: %s", html)
	}
	if strings.Contains(html, `<source src="/api/media/slide/quote_1/0" type="video/mp4">`) {
		t.Fatalf("quote video tile reused the first slide stream: %s", html)
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

func TestFeedItemShowsHandleWhenDisplayNameMissing(t *testing.T) {
	item := model.FeedItem{
		TweetID:      "tweet_1",
		AuthorHandle: "author_a",
		BodyText:     "body",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render feed item: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `class="feed-author-handle">@author_a</span>`) {
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
		AuthorHandle: "author_a",
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
	if !strings.Contains(html, `hx-trigger="intersect once threshold:0.4"`) {
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
		QuoteAuthorHandle:      "quote_author",
		QuoteAuthorDisplayName: "Quote Author",
		QuoteBodyText:          "quote foreign text",
		QuoteLang:              "fr",
	}

	var buf bytes.Buffer
	if err := FeedItem(PageProps{}, item).Render(context.Background(), &buf); err != nil {
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
}

func TestFeedItemQuoteTranslationDoesNotActivateParentTranslatePill(t *testing.T) {
	item := model.FeedItem{
		TweetID:                "tweet_with_translated_quote",
		AuthorHandle:           "parent_author",
		AuthorDisplayName:      "Parent Author",
		BodyText:               "parent text",
		Lang:                   "en",
		QuoteTweetID:           "quoted_status",
		QuoteAuthorHandle:      "quote_author",
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
