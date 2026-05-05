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
