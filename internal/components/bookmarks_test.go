package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestBookmarksPageRendersLabelFilterLinks(t *testing.T) {
	p := newTestPageProps()
	categories := []db.BookmarkCategoryRow{{ID: 7, Name: "Archive"}}
	labels := []db.BookmarkLabelCountRow{
		{Label: "", IsNoLabel: true, BookmarkCount: 57},
		{Label: "cinema", BookmarkCount: 15},
	}
	var buf bytes.Buffer
	err := BookmarksPage(
		p,
		nil,
		categories,
		labels,
		0,
		BookmarkLabelSelection{Label: "cinema"},
		model.Pager{Page: 1, PerPage: 200, Total: 15},
	).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("BookmarksPage render: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`href="/bookmarks"`,
		`href="/bookmarks?category_id=7"`,
		`data-bookmark-label-toggle`,
		`href="/bookmarks?label_empty=1"`,
		`href="/bookmarks?label=cinema"`,
		`<span>cinema</span>`,
		`<span class="bookmark-label-result-count">57</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("BookmarksPage missing %s in:\n%s", want, html)
		}
	}
	if strings.Contains(html, `category_id=7&amp;label`) || strings.Contains(html, `label=cinema&amp;category_id`) {
		t.Fatalf("category and label filters should not be combined: %s", html)
	}
}

func TestBookmarksNextURLPreservesActiveFilter(t *testing.T) {
	pager := model.Pager{Page: 1, PerPage: 2, Total: 5}

	if got := bookmarksNextURL(pager, 7, BookmarkLabelSelection{}); got != "/bookmarks?category_id=7&page=2" {
		t.Fatalf("category next URL = %q", got)
	}
	if got := bookmarksNextURL(pager, 7, BookmarkLabelSelection{Label: "cinema"}); got != "/bookmarks?label=cinema&page=2" {
		t.Fatalf("label next URL = %q", got)
	}
	if got := bookmarksNextURL(pager, 7, BookmarkLabelSelection{IsNoLabel: true}); got != "/bookmarks?label_empty=1&page=2" {
		t.Fatalf("no-label next URL = %q", got)
	}
}
