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

func TestPlayerCommentsUsesLocalAvatarForYouTubeCommentAuthor(t *testing.T) {
	html := renderPlayerComments(t, []model.Comment{{
		CommentID:       "comment_1",
		AuthorName:      "Commenter",
		AuthorID:        "UCcommenter123",
		AuthorThumbnail: "https://yt3.ggpht.com/raw-avatar=s88-c-k-c0x00fff",
		Text:            "hello",
		Platform:        "youtube",
	}})

	if !strings.Contains(html, `src="/api/media/avatar/youtube_UCcommenter123"`) {
		t.Fatalf("expected local commenter avatar path, got %s", html)
	}
	if strings.Contains(html, "yt3.ggpht.com/raw-avatar") {
		t.Fatalf("rendered raw YouTube commenter avatar URL: %s", html)
	}
}

func TestPlayerCommentsKeepsRawAvatarWhenAuthorIDIsNotCanonical(t *testing.T) {
	html := renderPlayerComments(t, []model.Comment{{
		CommentID:       "comment_1",
		AuthorName:      "Commenter",
		AuthorID:        "@handle",
		AuthorThumbnail: "https://yt3.ggpht.com/raw-avatar=s88-c-k-c0x00fff",
		Text:            "hello",
		Platform:        "youtube",
	}})

	if !strings.Contains(html, `src="https://yt3.ggpht.com/raw-avatar=s88-c-k-c0x00fff"`) {
		t.Fatalf("expected raw avatar fallback for non-canonical author id, got %s", html)
	}
}
