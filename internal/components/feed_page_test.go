package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestFeedPageDoesNotRenderFeedSourceRail(t *testing.T) {
	p := newTestPageProps()
	p.ActiveNav = "feed"
	var buf bytes.Buffer
	err := FeedPage(p, nil, false, "", true, true, nil).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("FeedPage render: %v", err)
	}
	if strings.Contains(buf.String(), `class="feed-source-rail"`) {
		t.Fatal("source rail should not render inside the feed page")
	}
}
