package worker

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

const (
	youtubeCommentAvatarCacheConcurrency = 4
	youtubeCommentAvatarMaxBytes         = 2 << 20
	youtubeCommentAvatarTimeout          = 15 * time.Second
)

// CacheYouTubeCommentAvatars downloads the public thumbnail URLs that yt-dlp
// already returned with YouTube comments. Commenters stay out of channel_profiles;
// the bytes use the conventional avatar cache so web and Android share one path.
func (m *Manager) CacheYouTubeCommentAvatars(ctx context.Context, comments []db.CommentInput) int {
	if m == nil || m.cfg == nil || m.downloader == nil || m.downloader.HTTP == nil || len(comments) == 0 {
		return 0
	}
	avatarDir := filepath.Join(m.cfg.DataDir, "thumbnails", "avatars")
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		log.Printf("[youtube-comments] mkdir avatar dir: %v", err)
		return 0
	}

	candidates := make(map[string]string)
	for _, comment := range comments {
		channelID := model.YouTubeCommentAuthorChannelID(comment.AuthorID)
		if channelID == "" || comment.AuthorThumbnail == "" {
			continue
		}
		if !canDownloadStoredAvatar(channelID, comment.AuthorThumbnail) {
			continue
		}
		if _, ok := candidates[channelID]; !ok {
			candidates[channelID] = comment.AuthorThumbnail
		}
	}
	if len(candidates) == 0 {
		return 0
	}

	work := make(chan struct {
		channelID string
		url       string
	})
	var downloaded atomic.Int64
	var wg sync.WaitGroup
	workers := youtubeCommentAvatarCacheConcurrency
	if len(candidates) < workers {
		workers = len(candidates)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for candidate := range work {
				if ctx.Err() != nil {
					continue
				}
				if m.cacheYouTubeCommentAvatar(ctx, avatarDir, candidate.channelID, candidate.url) {
					downloaded.Add(1)
				}
			}
		}()
	}
sendLoop:
	for channelID, url := range candidates {
		if hasConventionalMediaFile(avatarDir, channelID) {
			continue
		}
		select {
		case work <- struct {
			channelID string
			url       string
		}{channelID: channelID, url: url}:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(work)
	wg.Wait()
	return int(downloaded.Load())
}

func (m *Manager) cacheYouTubeCommentAvatar(ctx context.Context, avatarDir, channelID, rawURL string) bool {
	if hasConventionalMediaFile(avatarDir, channelID) {
		return false
	}
	tmpPath, err := m.downloader.HTTP.DownloadFileWithOptions(
		ctx,
		rawURL,
		avatarDir,
		channelID+".download",
		download.HTTPDownloadOptions{
			MaxBytes: youtubeCommentAvatarMaxBytes,
			Timeout:  youtubeCommentAvatarTimeout,
		},
	)
	if err != nil {
		log.Printf("[youtube-comments] avatar download failed for %s: %v", channelID, err)
		return false
	}
	if _, err := normalizeDownloadedImage(tmpPath, avatarDir, channelID); err != nil {
		log.Printf("[youtube-comments] avatar normalize failed for %s: %v", channelID, err)
		return false
	}
	return true
}
