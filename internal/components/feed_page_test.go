package components

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestFeedPageDoesNotRenderFeedSourceRail(t *testing.T) {
	p := newTestPageProps()
	p.ActiveNav = "feed"
	var buf bytes.Buffer
	err := FeedPage(p, nil, false, "", true, true, nil, "anchor").Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("FeedPage render: %v", err)
	}
	if strings.Contains(buf.String(), `class="feed-source-rail"`) {
		t.Fatal("source rail should not render inside the feed page")
	}
}

func TestFeedKeyboardNavigationShortcuts(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/src/feed/index.js")
	if err != nil {
		t.Fatalf("read feed source: %v", err)
	}
	src := string(srcBytes)
	for _, want := range []string{
		"function scrollFeedCardBy(delta)",
		"function visibleFeedEntries()",
		"child.matches('[data-feed-thread]')",
		"return entry.querySelector('.feed-thread-leaf[data-feed-item]')",
		"if (event.key === 'j' || event.key === 'J')",
		"if (event.key === 'k' || event.key === 'K')",
		"next.scrollIntoView({ behavior: 'smooth', block: 'start' })",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("feed keyboard navigation missing %q", want)
		}
	}
}
