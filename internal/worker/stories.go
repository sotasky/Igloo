package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

const nativeStoryFetchLimit = 20

func (m *Manager) refreshNativeStoriesForChannel(ctx context.Context, channelID, platform, handle, displayName string) int {
	if m == nil || m.db == nil || m.cfg == nil || m.downloader == nil || m.downloader.GalleryDL == nil {
		return 0
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if handle == "" {
		switch platform {
		case "tiktok":
			handle = strings.TrimPrefix(channelID, "tiktok_")
		case "instagram":
			handle = strings.TrimPrefix(channelID, "instagram_")
		}
	}
	if platform == "" {
		switch {
		case strings.HasPrefix(channelID, "tiktok_"):
			platform = "tiktok"
		case strings.HasPrefix(channelID, "instagram_"):
			platform = "instagram"
		}
	}
	if platform != "tiktok" && platform != "instagram" {
		return 0
	}
	if handle == "" {
		return 0
	}
	cookiesFile, _ := m.cookiesFor(platform)
	var (
		refs []download.StoryRef
		err  error
	)
	switch platform {
	case "tiktok":
		refs, err = m.downloader.GalleryDL.TikTokStories(ctx, handle, nativeStoryFetchLimit, cookiesFile)
	case "instagram":
		refs, err = m.downloader.GalleryDL.InstagramStories(ctx, handle, nativeStoryFetchLimit, cookiesFile)
	}
	if err != nil {
		log.Printf("[stories] check %s_%s: %v", platform, handle, err)
		return 0
	}
	if len(refs) == 0 {
		return 0
	}
	added := 0
	for _, ref := range refs {
		if ctx.Err() != nil {
			return added
		}
		if ref.ChannelID == "" {
			ref.ChannelID = channelID
		}
		if ref.ChannelID == "" {
			ref.ChannelID = platform + "_" + handle
		}
		if ref.AuthorHandle == "" {
			ref.AuthorHandle = handle
		}
		if ref.AuthorDisplayName == "" {
			ref.AuthorDisplayName = displayName
		}
		if ref.URL == "" || ref.VideoID == "" {
			continue
		}
		ok, err := m.db.IsVideoDownloaded(ref.VideoID)
		if err == nil && ok {
			continue
		}
		if err := m.downloadNativeStory(ctx, platform, handle, ref); err != nil {
			log.Printf("[stories] download %s: %v", ref.VideoID, err)
			continue
		}
		added++
	}
	if added > 0 {
		log.Printf("[stories] downloaded %d native stories for %s_%s", added, platform, handle)
	}
	return added
}

func (m *Manager) downloadNativeStory(ctx context.Context, platform, handle string, ref download.StoryRef) error {
	videoDir := filepath.Join(m.cfg.DataDir, "media", platform, handle, "stories")
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	cookiesFile, cookiesBrowser := m.cookiesFor(platform)
	opts := download.Opts{
		OutputDir:          videoDir,
		ID:                 ref.VideoID,
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		Format:             resolveFormatString(platform, ""),
	}
	paths, err := m.downloader.Download(ctx, ref.URL, "video", opts)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no files returned")
	}

	videoPath := paths[0]
	relVideoPath := toRelPath(m.cfg.DataDir, videoPath)
	var fileSize int64
	if fi, err := os.Stat(videoPath); err == nil {
		fileSize = fi.Size()
	}
	thumbPath := findSiblingThumbnail(videoPath, ref.VideoID)
	metadata := loadInfoJSON(videoPath, ref.VideoID)
	if thumbPath == "" {
		thumbPath = m.downloadSiblingThumbnailFromMetadata(ctx, videoPath, ref.VideoID, metadata)
	}
	relThumbPath := ""
	if thumbPath != "" {
		relThumbPath = toRelPath(m.cfg.DataDir, thumbPath)
	}

	if (platform == "tiktok" || platform == "instagram") && len(paths) > 1 {
		var imagePaths []string
		for _, p := range paths {
			switch strings.ToLower(filepath.Ext(p)) {
			case ".jpg", ".jpeg", ".png", ".webp":
				imagePaths = append(imagePaths, p)
			}
		}
		if len(imagePaths) > 1 {
			slides := make([]any, len(imagePaths))
			for i, p := range imagePaths {
				slides[i] = map[string]any{"path": toRelPath(m.cfg.DataDir, p)}
			}
			if metadata == nil {
				metadata = map[string]any{}
			}
			metadata["slides"] = slides
			metadata["vcodec"] = "none"
		}
	}

	publishedAt := ref.PublishedAtMs
	if fromMetadata := extractPublishedAt(metadata); fromMetadata > 0 {
		publishedAt = fromMetadata
	}
	if publishedAt <= 0 {
		publishedAt = time.Now().UnixMilli()
	}
	duration := extractDurationFromMetadata(metadata)
	metadataJSON := ""
	description := videoDescriptionFromMetadata(metadata)
	if metadata != nil {
		metadata["igloo_source_kind"] = "story"
		metadata["igloo_story_native_id"] = ref.NativeID
		metadata["igloo_story_url"] = ref.URL
		stripped := model.StripVideoMetadata(metadata)
		if stripped != nil {
			if data, err := json.Marshal(stripped); err == nil {
				metadataJSON = string(data)
			}
		}
	}
	mediaKind, slideCount := "", 0
	if metadataJSON != "" {
		var meta model.VideoMetadata
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			mediaKind, slideCount = model.ComputeMediaKind(&meta, relVideoPath)
		}
	}
	if mediaKind == "" {
		mediaKind, slideCount = model.ComputeMediaKind(nil, relVideoPath)
	}
	title := videoTitleFromMetadata(metadata, ref.Title)
	if title == "" {
		title = strings.TrimSpace(ref.AuthorDisplayName)
	}
	if title == "" {
		title = platform + " story"
	}
	return m.db.InsertVideoWithSourceKind(
		ref.VideoID, ref.ChannelID, title, description,
		duration, relThumbPath, relVideoPath, fileSize,
		publishedAt, metadataJSON, mediaKind, slideCount, false, "story",
	)
}
