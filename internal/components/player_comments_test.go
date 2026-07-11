package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func renderPlayerComments(t *testing.T, comments []model.Comment) string {
	t.Helper()
	var buf bytes.Buffer
	if err := PlayerComments(newTestPageProps(), comments, "").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestPlayerCommentsUsesProjectedCommentAvatar(t *testing.T) {
	html := renderPlayerComments(t, []model.Comment{{
		CommentID:       "comment_1",
		AuthorName:      "Commenter",
		AuthorID:        "UCcommenter123",
		AuthorThumbnail: "/api/media/comment-avatar/youtube_UCcommenter123",
		Text:            "hello",
		Platform:        "youtube",
	}})

	if !strings.Contains(html, `src="/api/media/comment-avatar/youtube_UCcommenter123"`) {
		t.Fatalf("expected canonical comment avatar, got %s", html)
	}
	if strings.Contains(html, `yt3.ggpht.com`) {
		t.Fatalf("rendered upstream comment avatar: %s", html)
	}
}

func TestPlayerCommentsDoesNotRenderRawCommentAvatar(t *testing.T) {
	html := renderPlayerComments(t, []model.Comment{{
		CommentID:       "comment_1",
		AuthorName:      "Commenter",
		AuthorID:        "@handle",
		AuthorThumbnail: "https://yt3.ggpht.com/raw-avatar=s88-c-k-c0x00fff",
		Text:            "hello",
		Platform:        "youtube",
	}})

	if strings.Contains(html, `yt3.ggpht.com`) || strings.Contains(html, `<img class="player-channel-avatar"`) {
		t.Fatalf("rendered raw avatar instead of initial fallback: %s", html)
	}
}

func TestRenderCommentRichTextLinksURLsWithThemeClass(t *testing.T) {
	got := RenderCommentRichText("watch https://example.com?a=1&b=2 and www.example.org/path.")

	if !strings.Contains(got, `href="https://example.com?a=1&amp;b=2" class="inline-rich-link"`) {
		t.Fatalf("expected absolute URL to use themed link class, got %s", got)
	}
	if !strings.Contains(got, `href="https://www.example.org/path" class="inline-rich-link" target="_blank" rel="noopener noreferrer">www.example.org/path</a>.`) {
		t.Fatalf("expected scheme-less URL to be linked without trailing punctuation, got %s", got)
	}
}

func TestRenderCommentRichTextLinksCommonBareDomains(t *testing.T) {
	got := RenderCommentRichText("support me at patreon.com/example")

	if !strings.Contains(got, `href="https://patreon.com/example" class="inline-rich-link"`) {
		t.Fatalf("expected bare domain URL to be linked with https href, got %s", got)
	}
}

func TestRenderCommentRichTextDoesNotLinkEmailDomains(t *testing.T) {
	got := RenderCommentRichText("mail support@example.com about 1:05")

	if strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("email domain should not be linked, got %s", got)
	}
	if !strings.Contains(got, `class="inline-seek-link" data-seek-seconds="65"`) {
		t.Fatalf("timestamp seek link was lost, got %s", got)
	}
}
