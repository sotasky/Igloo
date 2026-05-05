package model

import "strings"

// PresentedComment is the Android-facing comment shape. It keeps the stored
// comment fields intact while adding server-owned presentation metadata.
type PresentedComment struct {
	Comment
	ThreadOrder    int    `json:"thread_order"`
	ThreadDepth    int    `json:"thread_depth"`
	ParentOrder    int    `json:"parent_order"`
	ReplyToAuthor  string `json:"reply_to_author"`
	IsCreator      bool   `json:"is_creator"`
	LikeCountLabel string `json:"like_count_label"`
}

// PresentComments annotates flat stored comments with a stable preorder tree.
// Missing parents, self-parents, and cycle participants render as root rows.
func PresentComments(comments []Comment, creatorAuthorID string) []PresentedComment {
	if len(comments) == 0 {
		return nil
	}

	nodes := make([]*commentPresentationNode, len(comments))
	byID := make(map[string]*commentPresentationNode, len(comments))
	parentByID := make(map[string]string, len(comments))
	for i := range comments {
		node := &commentPresentationNode{index: i, comment: comments[i]}
		nodes[i] = node
		id := strings.TrimSpace(comments[i].CommentID)
		if id == "" {
			continue
		}
		byID[id] = node
		parentByID[id] = strings.TrimSpace(comments[i].ParentID)
	}

	roots := make([]*commentPresentationNode, 0, len(nodes))
	for _, node := range nodes {
		id := strings.TrimSpace(node.comment.CommentID)
		parentID := strings.TrimSpace(node.comment.ParentID)
		parent, ok := byID[parentID]
		if id != "" && parentID != "" && parentID != id && ok && validCommentParent(id, parentID, parentByID) {
			parent.children = append(parent.children, node)
			node.parent = parent
			continue
		}
		roots = append(roots, node)
	}

	out := make([]PresentedComment, 0, len(comments))
	nextOrder := 1
	var visit func(node *commentPresentationNode, depth int)
	visit = func(node *commentPresentationNode, depth int) {
		parentOrder := 0
		replyToAuthor := ""
		if node.parent != nil {
			parentOrder = node.parent.order
			replyToAuthor = commentAuthorLabel(node.parent.comment.AuthorName)
		}
		node.order = nextOrder
		nextOrder++
		out = append(out, PresentedComment{
			Comment:        node.comment,
			ThreadOrder:    node.order,
			ThreadDepth:    depth,
			ParentOrder:    parentOrder,
			ReplyToAuthor:  replyToAuthor,
			IsCreator:      creatorAuthorID != "" && node.comment.AuthorID == creatorAuthorID,
			LikeCountLabel: commentLikeCountLabel(node.comment.LikeCount),
		})
		for _, child := range node.children {
			visit(child, depth+1)
		}
	}
	for _, root := range roots {
		visit(root, 0)
	}
	return out
}

func CommentCreatorAuthorID(channelID string) string {
	if i := strings.Index(channelID, "_"); i >= 0 {
		return channelID[i+1:]
	}
	return channelID
}

type commentPresentationNode struct {
	index    int
	comment  Comment
	parent   *commentPresentationNode
	order    int
	children []*commentPresentationNode
}

func validCommentParent(selfID, parentID string, parentByID map[string]string) bool {
	seen := map[string]struct{}{}
	for current := parentID; current != ""; current = parentByID[current] {
		if current == selfID {
			return false
		}
		if _, ok := seen[current]; ok {
			return true
		}
		seen[current] = struct{}{}
		if _, ok := parentByID[current]; !ok {
			return true
		}
	}
	return true
}

func commentLikeCountLabel(likeCount int) string {
	if likeCount <= 0 {
		return ""
	}
	return CompactCountLabel(int64(likeCount))
}

func commentAuthorLabel(author string) string {
	return strings.TrimLeft(strings.TrimSpace(author), "@")
}
