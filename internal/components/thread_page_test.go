package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestThreadPageRendersFullChainAndFeedLink(t *testing.T) {
	items := []model.FeedItem{
		{TweetID: "sample_root", AuthorHandle: "sample_root_author", BodyText: "root body"},
		{TweetID: "sample_parent", AuthorHandle: "sample_parent_author", BodyText: "parent body", IsReply: true, ReplyToStatus: "sample_root"},
		{TweetID: "sample_leaf", AuthorHandle: "sample_author", BodyText: "leaf body", IsReply: true, ReplyToStatus: "sample_parent"},
	}

	var buf bytes.Buffer
	if err := ThreadPage(newTestPageProps(), items, "/feed?offset=40").Render(context.Background(), &buf); err != nil {
		t.Fatalf("render thread page: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`class="thread-back-link"`,
		`href="/feed?offset=40"`,
		`data-thread-back-link`,
		`&lt;- Feed`,
		`data-tweet-id="sample_root"`,
		`data-tweet-id="sample_parent"`,
		`data-tweet-id="sample_leaf"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html: %s", want, html)
		}
	}
}
