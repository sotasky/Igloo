package components

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

// CommentNode wraps a Comment with its children for tree rendering.
type CommentNode struct {
	Comment  model.Comment
	Children []*CommentNode
}

// BuildCommentTree groups comments by parent_id and returns the roots.
func BuildCommentTree(comments []model.Comment) []*CommentNode {
	nodes := make([]*CommentNode, len(comments))
	byID := make(map[string]*CommentNode, len(comments))
	for i := range comments {
		n := &CommentNode{Comment: comments[i]}
		nodes[i] = n
		id := strings.TrimSpace(comments[i].CommentID)
		if id != "" {
			byID[id] = n
		}
	}
	roots := make([]*CommentNode, 0, len(nodes))
	for _, n := range nodes {
		parent := strings.TrimSpace(n.Comment.ParentID)
		self := strings.TrimSpace(n.Comment.CommentID)
		if parent != "" && parent != self {
			if p, ok := byID[parent]; ok {
				p.Children = append(p.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	return roots
}

// CommentAuthorNameText returns a localized display name for comment authors.
func CommentAuthorNameText(p PageProps, c model.Comment) string {
	name := strings.TrimSpace(c.AuthorName)
	name = strings.TrimLeft(name, "@")
	if name == "" {
		return L(p, "player_comment_author_unknown", "User")
	}
	return name
}

// CommentAuthorInitialText returns the first rune of the localized display name.
func CommentAuthorInitialText(p PageProps, c model.Comment) string {
	name := CommentAuthorNameText(p, c)
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "U"
}

// CommentAuthorAvatarURL resolves YouTube commenter avatars through the local
// profile media endpoint when yt-dlp provided a canonical channel ID.
func CommentAuthorAvatarURL(c model.Comment) string {
	platform := strings.ToLower(strings.TrimSpace(c.Platform))
	if platform == "" || platform == "youtube" {
		if channelID := model.YouTubeCommentAuthorChannelID(c.AuthorID); channelID != "" {
			return "/api/media/avatar/" + channelID
		}
	}
	return strings.TrimSpace(c.AuthorThumbnail)
}

// CommentIndentPx returns the left indent for a reply depth.
func CommentIndentPx(depth int) int {
	if depth <= 0 {
		return 0
	}
	px := depth * 15
	if px > 52 {
		px = 52
	}
	return px
}

// CommentMetaText builds the meta line ("3h ago • 42 likes"). Relative
// time is always computed from PublishedAt — the scraped, pre-formatted
// platform string is gone.
func CommentMetaText(p PageProps, c model.Comment) string {
	var parts []string
	if c.PublishedAt != nil {
		if rel := RelativeTimeText(p, c.PublishedAt); rel != "" {
			parts = append(parts, rel)
		}
	}
	if c.LikeCount != 0 {
		parts = append(parts, LF(p, "comments_likes_count", "%1$d likes", c.LikeCount))
	}
	return strings.Join(parts, " • ")
}

// FormatCompactNumber renders 1234 → "1.2K", 2_400_000 → "2.4M".
func FormatCompactNumber(n int64) string {
	return model.CompactCountLabel(n)
}

// CommentCreatorAuthorID strips the "<platform>_" prefix. The player marks
// comments whose author_id matches the video's creator channel with is-creator.
func CommentCreatorAuthorID(channelID string) string {
	if i := strings.Index(channelID, "_"); i >= 0 {
		return channelID[i+1:]
	}
	return channelID
}

// CommentIsCreator reports whether this comment's author is the video's creator.
func CommentIsCreator(c model.Comment, creatorAuthorID string) bool {
	return creatorAuthorID != "" && c.AuthorID == creatorAuthorID
}

var (
	commentTimestampRe = regexp.MustCompile(`\b(?:\d{1,2}:)?\d{1,2}:\d{2}\b`)
	commentURLRe       = regexp.MustCompile(`\bhttps?://[^\s<]+`)
)

type textMatch struct {
	start int
	end   int
	html  string
}

// RenderCommentRichText returns HTML with timestamps as seek-buttons and URLs
// as anchors. Unmatched text is HTML-escaped and newlines become <br>.
// Safe for templ.Raw: all user content is escaped.
func RenderCommentRichText(text string) string {
	if text == "" {
		return ""
	}
	var matches []textMatch

	// Collect URL matches first so they win on overlap.
	for _, loc := range commentURLRe.FindAllStringIndex(text, -1) {
		raw := text[loc[0]:loc[1]]
		escaped := html.EscapeString(raw)
		matches = append(matches, textMatch{
			start: loc[0],
			end:   loc[1],
			html:  fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer">%s</a>`, escaped, escaped),
		})
	}

	for _, loc := range commentTimestampRe.FindAllStringIndex(text, -1) {
		// Skip timestamps that fall inside a URL match.
		overlaps := false
		for _, m := range matches {
			if loc[0] < m.end && loc[1] > m.start {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		token := text[loc[0]:loc[1]]
		seconds := parseTimestampToken(token)
		if seconds < 0 {
			continue
		}
		matches = append(matches, textMatch{
			start: loc[0],
			end:   loc[1],
			html:  fmt.Sprintf(`<button type="button" class="inline-seek-link" data-seek-seconds="%d">%s</button>`, seconds, html.EscapeString(token)),
		})
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].start < matches[j].start })

	var b strings.Builder
	pos := 0
	for _, m := range matches {
		if m.start < pos {
			continue
		}
		b.WriteString(escapeWithBreaks(text[pos:m.start]))
		b.WriteString(m.html)
		pos = m.end
	}
	b.WriteString(escapeWithBreaks(text[pos:]))
	return b.String()
}

func escapeWithBreaks(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

func parseTimestampToken(token string) int {
	parts := strings.Split(token, ":")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return -1
		}
		nums = append(nums, n)
	}
	switch len(nums) {
	case 2:
		return nums[0]*60 + nums[1]
	case 3:
		return nums[0]*3600 + nums[1]*60 + nums[2]
	}
	return -1
}
