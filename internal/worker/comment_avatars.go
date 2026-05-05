package worker

import (
	"log"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func (m *Manager) queueYouTubeCommentAuthorAvatars(comments []db.CommentInput) {
	if len(comments) == 0 {
		return
	}
	if n, err := m.db.SeedYouTubeCommentAuthorProfiles(); err != nil {
		log.Printf("[profile] SeedYouTubeCommentAuthorProfiles: %v", err)
	} else if n > 0 {
		log.Printf("[profile] seeded %d youtube comment author profile rows", n)
	}
	seen := map[string]struct{}{}
	for _, comment := range comments {
		channelID := model.YouTubeCommentAuthorChannelID(comment.AuthorID)
		if channelID == "" {
			continue
		}
		if _, ok := seen[channelID]; ok {
			continue
		}
		seen[channelID] = struct{}{}
		m.RequestAvatar(channelID)
	}
}
