package model

import "testing"

func TestPresentCommentsBuildsStableThreadMetadata(t *testing.T) {
	comments := []Comment{
		{CommentID: "root-a", AuthorName: "@Author A", AuthorID: "creator", LikeCount: 10},
		{CommentID: "reply-a", ParentID: "root-a", AuthorName: "@Reply A", AuthorID: "reply", LikeCount: 9},
		{CommentID: "nested-a", ParentID: "reply-a", AuthorName: "@Nested A", AuthorID: "creator", LikeCount: 8},
		{CommentID: "orphan", ParentID: "missing", AuthorName: "@Orphan", AuthorID: "orphan", LikeCount: 7},
	}

	got := PresentComments(comments, "creator")
	if ids := presentedCommentIDs(got); !equalStrings(ids, []string{"root-a", "reply-a", "nested-a", "orphan"}) {
		t.Fatalf("comment order = %v", ids)
	}

	assertPresentedComment(t, got[0], "root-a", 1, 0, 0, "", true)
	assertPresentedComment(t, got[1], "reply-a", 2, 1, 1, "Author A", false)
	assertPresentedComment(t, got[2], "nested-a", 3, 2, 2, "Reply A", true)
	assertPresentedComment(t, got[3], "orphan", 4, 0, 0, "", false)
}

func TestPresentCommentsPromotesCyclesToRoots(t *testing.T) {
	comments := []Comment{
		{CommentID: "a", ParentID: "b", AuthorName: "@A"},
		{CommentID: "b", ParentID: "a", AuthorName: "@B"},
		{CommentID: "child", ParentID: "a", AuthorName: "@Child"},
	}

	got := PresentComments(comments, "")
	if ids := presentedCommentIDs(got); !equalStrings(ids, []string{"a", "child", "b"}) {
		t.Fatalf("comment order = %v", ids)
	}
	assertPresentedComment(t, got[0], "a", 1, 0, 0, "", false)
	assertPresentedComment(t, got[1], "child", 2, 1, 1, "A", false)
	assertPresentedComment(t, got[2], "b", 3, 0, 0, "", false)
}

func assertPresentedComment(
	t *testing.T,
	got PresentedComment,
	id string,
	order int,
	depth int,
	parentOrder int,
	replyToAuthor string,
	isCreator bool,
) {
	t.Helper()
	if got.CommentID != id ||
		got.ThreadOrder != order ||
		got.ThreadDepth != depth ||
		got.ParentOrder != parentOrder ||
		got.ReplyToAuthor != replyToAuthor ||
		got.IsCreator != isCreator {
		t.Fatalf("presented comment = %+v, want id=%s order=%d depth=%d parent=%d reply=%q creator=%v",
			got, id, order, depth, parentOrder, replyToAuthor, isCreator)
	}
}

func presentedCommentIDs(comments []PresentedComment) []string {
	out := make([]string, 0, len(comments))
	for _, c := range comments {
		out = append(out, c.CommentID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
