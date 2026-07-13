package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

const nativeStoryFetchLimit = 20

func (m *Manager) nativeStoryWindow(ctx context.Context, channel model.Channel) (download.SourceWindow, error) {
	window := download.SourceWindow{Component: sourceComponentStories}
	if m == nil || m.downloader == nil || m.downloader.GalleryDL == nil {
		return window, fmt.Errorf("%s story checker is unavailable", channel.Platform)
	}

	platform := strings.ToLower(strings.TrimSpace(channel.Platform))
	var handle string
	switch platform {
	case "tiktok":
		handle = tiktokHandleForChannel(channel)
	case "instagram":
		handle = instagramHandleForChannel(channel)
	default:
		return window, fmt.Errorf("unsupported story platform %q", platform)
	}
	if handle == "" {
		return window, fmt.Errorf("%s story handle is empty", platform)
	}

	var (
		refs []download.StoryRef
		err  error
	)
	if platform == "tiktok" {
		cookiesFile, _ := m.cookiesFor(platform)
		refs, err = m.downloader.GalleryDL.TikTokStories(ctx, handle, nativeStoryFetchLimit, cookiesFile)
	} else {
		cookiesFile, cookiesBrowser := m.cookieFileAndBrowserFor(platform)
		refs, err = m.downloader.GalleryDL.InstagramStories(
			ctx, handle, nativeStoryFetchLimit, cookiesFile, cookiesBrowser,
		)
	}
	if err != nil {
		return window, err
	}

	window.Complete = true
	window.Refs = make([]download.VideoRef, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.VideoID) == "" {
			continue
		}
		window.Refs = append(window.Refs, download.VideoRef{
			VideoID:           ref.VideoID,
			Title:             ref.Title,
			URL:               ref.URL,
			ChannelID:         ref.ChannelID,
			AuthorHandle:      ref.AuthorHandle,
			AuthorDisplayName: ref.AuthorDisplayName,
			AuthorAvatarURL:   ref.AuthorAvatarURL,
			PublishedAtMs:     ref.PublishedAtMs,
		})
	}
	return window, nil
}
