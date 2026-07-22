package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestFeedDashboardUnfollowButtonRemovesRowAndFormatsConfirm(t *testing.T) {
	p := newTestPageProps()
	d := FeedDashboardData{
		Sources: []FeedSourceEntry{
			{
				Handle:    "sample_handle",
				Status:    "failing",
				ItemCount: 3,
			},
		},
	}

	var buf bytes.Buffer
	if err := FeedDashboard(p, d).Render(context.Background(), &buf); err != nil {
		t.Fatalf("FeedDashboard render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		`class="feed-unfollow-link"`,
		`hx-delete="/api/unsubscribe/twitter_sample_handle"`,
		`hx-target="closest tr"`,
		`hx-swap="outerHTML"`,
		`@sample_handle`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("FeedDashboard missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "%1$s") {
		t.Fatalf("FeedDashboard confirm prompt still contains raw placeholder:\n%s", html)
	}
}
