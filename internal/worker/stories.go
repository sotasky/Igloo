package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
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
	cookiesFile, cookiesBrowser := m.cookiesFor(platform)
	if platform == "instagram" {
		cookiesFile, cookiesBrowser = m.cookieFileAndBrowserFor(platform)
	}
	var (
		refs []download.StoryRef
		err  error
	)
	switch platform {
	case "tiktok":
		refs, err = m.downloader.GalleryDL.TikTokStories(ctx, handle, nativeStoryFetchLimit, cookiesFile)
	case "instagram":
		refs, err = m.downloader.GalleryDL.InstagramStories(ctx, handle, nativeStoryFetchLimit, cookiesFile, cookiesBrowser)
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
	ownerKind, ok := db.VideoOwnerKindForPlatform(platform)
	if !ok || ownerKind == "tweet" {
		return fmt.Errorf("unsupported story platform %q", platform)
	}
	videoDir, err := m.cfg.Storage.WritePath("media/" + platform + "/" + handle + "/stories")
	if err != nil {
		return fmt.Errorf("storage path: %w", err)
	}
	if err := m.downloader.RunMedia(ctx, download.MediaLaneBulk, func() error { return os.MkdirAll(videoDir, 0o755) }); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	attemptID, err := newDownloadAttemptID(ref.VideoID)
	if err != nil {
		return fmt.Errorf("allocate download attempt: %w", err)
	}
	cookiesFile, cookiesBrowser := m.cookiesFor(platform)
	opts := download.Opts{
		OutputDir:          videoDir,
		ID:                 attemptID,
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		CookieAlternates:   m.cookieSetsFor(platform),
		Format:             resolveFormatString(platform, ""),
	}
	completed, err := m.downloader.DownloadCompleted(ctx, ref.URL, "video", opts)
	if err != nil {
		m.removeFailedAttempt(ctx, completedVideoFiles{}, completed)
		return err
	}
	if len(completed.MediaPaths) == 0 {
		m.removeFailedAttempt(ctx, completedVideoFiles{}, completed)
		return fmt.Errorf("no files returned")
	}

	files, err := m.prepareCompletedVideoFiles(ctx, platform, attemptID, completed)
	if err != nil {
		m.removeFailedAttempt(ctx, files, completed)
		return fmt.Errorf("prepare completed outputs: %w", err)
	}
	metadata := loadInfoJSONFile(completed.InfoJSONPath)

	if (platform == "tiktok" || platform == "instagram") && len(files.imageKeys) > 1 {
		slides := make([]any, len(files.imageKeys))
		for i, key := range files.imageKeys {
			slides[i] = map[string]any{"path": key}
		}
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["slides"] = slides
		metadata["vcodec"] = "none"
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
			mediaKind, slideCount = model.ComputeMediaKind(&meta, files.primaryKey)
		}
	}
	if mediaKind == "" {
		mediaKind, slideCount = model.ComputeMediaKind(nil, files.primaryKey)
	}
	title := videoTitleFromMetadata(metadata, ref.Title)
	if title == "" {
		title = strings.TrimSpace(ref.AuthorDisplayName)
	}
	if title == "" {
		title = platform + " story"
	}
	err = m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: ref.VideoID, ChannelID: ref.ChannelID, OwnerKind: ownerKind, Title: title, Description: description,
		Duration: duration, PublishedAtMs: publishedAt, MetadataJSON: metadataJSON,
		MediaKind: mediaKind, SlideCount: slideCount, SourceKind: "story", Assets: files.assets,
	})
	if err == nil {
		m.removeTransientFiles(ctx, files)
		m.enqueueCompletedVideoPreview(ref.VideoID, platform, files.primaryPath, float64(duration))
	} else {
		m.removeFailedAttempt(ctx, files, completed)
	}
	return err
}
