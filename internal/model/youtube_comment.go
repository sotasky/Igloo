package model

import "strings"

// YouTubeCommentAuthorChannelID converts yt-dlp's comment author_id into the
// server-owned channel id used by profile/avatar storage.
func YouTubeCommentAuthorChannelID(authorID string) string {
	raw := strings.TrimSpace(authorID)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "youtube_") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "youtube_"))
	}
	if !strings.HasPrefix(raw, "UC") {
		return ""
	}
	return "youtube_" + raw
}
