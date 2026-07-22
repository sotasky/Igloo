package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/subscribe"
)

// TempDownloadResult holds the outcome of a temp download.
type TempDownloadResult struct {
	Success    bool
	Message    string
	VideoID    string
	PlaylistID string
}

// DownloadTemp handles an ad-hoc URL download.
func (m *Manager) DownloadTemp(ctx context.Context, rawURL string, saveChannel bool) TempDownloadResult {
	platform := subscribe.DetectPlatform(rawURL, "")
	if err := subscribe.ValidateInput(rawURL, platform); err != nil {
		return TempDownloadResult{Message: "Unsupported download URL"}
	}
	if m.cfg != nil && !m.cfg.PlatformEnabled(platform) {
		return TempDownloadResult{Message: fmt.Sprintf("%s is not enabled on this Igloo server", platform)}
	}

	cookiesFile, cookiesBrowser := m.cookiesFor(platform)
	cookieSets := m.cookieSetsFor(platform)
	authOpts := download.Opts{
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		CookieAlternates:   cookieSets,
	}

	// Check for YouTube playlist.
	if platform == "youtube" {
		if playlistID := extractPlaylistID(rawURL); playlistID != "" {
			return m.downloadPlaylist(ctx, rawURL, playlistID, authOpts)
		}
	}

	// Fetch metadata.
	info, err := m.downloader.YtDlp.FetchInfo(ctx, rawURL, authOpts)
	if err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Could not fetch info: %v", err)}
	}

	videoID, _ := info["id"].(string)
	if videoID == "" {
		return TempDownloadResult{Message: "No video ID in metadata"}
	}
	title, _ := info["title"].(string)
	if title == "" {
		title = videoID
	}

	channelID, _ := info["channel_id"].(string)
	if channelID == "" {
		if v, ok := info["uploader_id"].(string); ok {
			channelID = v
		}
	}
	channelName, _ := info["channel"].(string)
	if channelName == "" {
		if v, ok := info["uploader"].(string); ok {
			channelName = v
		}
	}
	if channelID == "" {
		channelID = "temp"
	}
	if channelName == "" {
		channelName = "Temp"
	}
	ownerKind, ok := db.VideoOwnerKindForPlatform(platform)
	if !ok || ownerKind == "tweet" {
		return TempDownloadResult{Message: fmt.Sprintf("Unsupported platform: %s", platform)}
	}

	// Normalize channel_id to the platform_id convention used by every other
	// channel in the DB, so avatar resolution and channel_name stripping helpers
	// work consistently.
	channelURL := firstNonEmptyString(
		stringFromMap(info, "channel_url"),
		stringFromMap(info, "uploader_url"),
	)
	if platform == "youtube" {
		channelID = download.CanonicalizeYouTubeChannelID(channelID, channelURL, rawURL)
		if channelURL == "" && strings.HasPrefix(channelID, "youtube_UC") {
			channelURL = "https://www.youtube.com/channel/" + strings.TrimPrefix(channelID, "youtube_")
		}
	} else {
		channelID = normalizeChannelID(platform, channelID)
	}

	// Download to temp dir.
	tempDir, err := m.cfg.Storage.WritePath("media/temp")
	if err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Storage path: %v", err)}
	}
	if err := m.downloader.RunMedia(ctx, download.MediaLaneBulkForeground, func() error { return os.MkdirAll(tempDir, 0o755) }); err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Create storage directory: %v", err)}
	}
	outputID, err := downloadOutputID(videoID)
	if err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Download output: %v", err)}
	}
	subtitleDir, err := m.cfg.Storage.WritePath("subtitles/" + platform)
	if err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Subtitle storage: %v", err)}
	}

	opts := download.Opts{
		OutputDir:          tempDir,
		ID:                 outputID,
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		CookieAlternates:   cookieSets,
		Subtitles:          true,
		SubtitleDir:        subtitleDir,
	}

	completed, dlErr := m.downloader.DownloadCompleted(ctx, download.MediaLaneBulkForeground, rawURL, "video", opts)
	if dlErr != nil || len(completed.MediaPaths) == 0 {
		m.removeFailedAttempt(ctx, download.MediaLaneBulkForeground, completedVideoFiles{}, completed)
		msg := "Download failed"
		if dlErr != nil {
			msg = dlErr.Error()
		}
		return TempDownloadResult{Message: msg}
	}

	files, err := m.prepareCompletedVideoFiles(ctx, download.MediaLaneBulkForeground, completed)
	if err != nil {
		m.removeFailedAttempt(ctx, download.MediaLaneBulkForeground, files, completed)
		return TempDownloadResult{Message: fmt.Sprintf("Prepare completed outputs: %v", err)}
	}

	publishedAt := extractPublishedAt(info)
	description, _ := info["description"].(string)
	duration := extractDurationFromMetadata(info)
	if len(files.imageKeys) > 1 {
		slides := make([]any, len(files.imageKeys))
		for i, key := range files.imageKeys {
			slides[i] = map[string]any{"path": key}
		}
		if info == nil {
			info = map[string]any{}
		}
		info["slides"] = slides
		info["vcodec"] = "none"
	}

	metadataJSON := ""
	var mediaKind string
	var slideCount int
	if info != nil {
		stripped := model.StripVideoMetadata(info)
		if stripped != nil {
			if b, err := json.Marshal(stripped); err == nil {
				metadataJSON = string(b)
			}
		}
	}
	if metadataJSON != "" {
		var meta model.VideoMetadata
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			mediaKind, slideCount = model.ComputeMediaKind(&meta, files.primaryKey)
		}
	}
	if mediaKind == "" {
		mediaKind, slideCount = model.ComputeMediaKind(nil, files.primaryKey)
	}

	// Upsert channel.
	_ = m.db.AddChannel(model.Channel{
		ChannelID:    channelID,
		SourceID:     strings.TrimPrefix(stringFromMap(info, "uploader_id"), "@"),
		Name:         channelName,
		URL:          channelURL,
		Platform:     platform,
		IsSubscribed: saveChannel,
	})

	if err := m.db.StoreCompletedVideo(db.CompletedVideo{
		VideoID: videoID, ChannelID: channelID, OwnerKind: ownerKind, Title: title, Description: description,
		Duration: duration, PublishedAtMs: publishedAt, MetadataJSON: metadataJSON,
		MediaKind: mediaKind, SlideCount: slideCount, IsTemp: true,
		Assets: files.assets,
	}); err != nil {
		m.removeFailedAttempt(ctx, download.MediaLaneBulkForeground, files, completed)
		return TempDownloadResult{Message: fmt.Sprintf("DB insert: %v", err)}
	}
	if err := m.publishCompletedVideoThumbnail(ctx, download.MediaLaneBulkForeground, videoID, platform, outputID, files); err != nil {
		log.Printf("[temp] thumbnail publish failed for %s: %v", videoID, err)
	}
	if err := m.storeCompletedSubtitles(ctx, videoID, files, completed); err != nil {
		log.Printf("[temp] subtitle publish failed for %s: %v", videoID, err)
	}
	m.removeTransientFiles(ctx, download.MediaLaneBulkForeground, files)

	if platform == "youtube" {
		m.RequestVideoPreview(videoID)
	}

	// Channel creation owns the durable profile job. Wake its consumer without
	// creating a synchronous render-time identity path.
	m.KickProfileJobs()

	// Synchronously fetch comments so they appear on first player page load.
	commentsCtx, commentsCancel := context.WithTimeout(ctx, 2*time.Minute)
	comments, commentsErr := m.downloader.YtDlp.FetchComments(commentsCtx, rawURL, download.DefaultCommentFetchLimit, opts)
	commentsCancel()
	if commentsErr != nil {
		log.Printf("[temp] comments fetch failed for %s: %v", videoID, commentsErr)
	} else if len(comments) > 0 {
		inserted, err := m.db.AddComments(videoID, comments)
		if err != nil {
			log.Printf("[temp] store comments for %s: %v", videoID, err)
		} else {
			m.KickMediaWork()
			log.Printf("[temp] fetched %d comments for %s", inserted, videoID)
		}
	}

	return TempDownloadResult{
		Success: true,
		Message: fmt.Sprintf("Downloaded: %s", title),
		VideoID: videoID,
	}
}

func (m *Manager) downloadPlaylist(ctx context.Context, rawURL, playlistID string, authOpts download.Opts) TempDownloadResult {
	info, err := m.downloader.YtDlp.FetchPlaylistInfo(ctx, rawURL, authOpts)
	if err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Could not inspect playlist: %v", err)}
	}

	entries, _ := info["entries"].([]any)
	if len(entries) == 0 {
		return TempDownloadResult{Message: "Playlist has no entries"}
	}

	playlistTitle, _ := info["title"].(string)
	if playlistTitle == "" {
		playlistTitle = "Playlist " + playlistID
	}

	targetDir, err := m.cfg.Storage.WritePath("media/playlists/" + safeFolderName(playlistTitle))
	if err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Storage path: %v", err)}
	}
	if err := m.downloader.RunMedia(ctx, download.MediaLaneBulkForeground, func() error { return os.MkdirAll(targetDir, 0o755) }); err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("Create storage directory: %v", err)}
	}

	playlistChannelID := "playlist_" + playlistID

	downloaded := 0
	failed := 0
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		videoID, _ := entryMap["id"].(string)
		if videoID == "" {
			continue
		}
		entryTitle, _ := entryMap["title"].(string)
		if entryTitle == "" {
			entryTitle = videoID
		}

		if ok, _ := m.db.IsVideoDownloaded(videoID); ok {
			downloaded++
			continue
		}

		videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
		outputID, attemptErr := downloadOutputID(videoID)
		if attemptErr != nil {
			log.Printf("[temp] playlist item %s output preparation failed: %v", videoID, attemptErr)
			failed++
			continue
		}
		opts := download.Opts{
			OutputDir:          targetDir,
			ID:                 outputID,
			Cookies:            authOpts.Cookies,
			CookiesFromBrowser: authOpts.CookiesFromBrowser,
		}
		completed, dlErr := m.downloader.DownloadCompleted(ctx, download.MediaLaneBulkForeground, videoURL, "video", opts)
		if dlErr != nil || len(completed.MediaPaths) == 0 {
			m.removeFailedAttempt(ctx, download.MediaLaneBulkForeground, completedVideoFiles{}, completed)
			log.Printf("[temp] playlist item %s failed: %v", videoID, dlErr)
			failed++
			continue
		}

		files, prepareErr := m.prepareCompletedVideoFiles(ctx, download.MediaLaneBulkForeground, completed)
		if prepareErr != nil {
			m.removeFailedAttempt(ctx, download.MediaLaneBulkForeground, files, completed)
			log.Printf("[temp] playlist item %s output preparation failed: %v", videoID, prepareErr)
			failed++
			continue
		}
		metadata := completed.Metadata
		publishedAt := extractPublishedAt(metadata)
		description, _ := metadata["description"].(string)
		duration := extractDurationFromMetadata(metadata)
		metaJSON := ""
		if b, err := json.Marshal(metadata); err == nil {
			metaJSON = string(b)
		}
		if err := m.db.StoreCompletedVideo(db.CompletedVideo{
			VideoID: videoID, ChannelID: playlistChannelID, OwnerKind: "youtube_video", Title: entryTitle, Description: description,
			Duration: duration, PublishedAtMs: publishedAt, MetadataJSON: metaJSON,
			SourceKind: "playlist", Assets: files.assets,
		}); err != nil {
			m.removeFailedAttempt(ctx, download.MediaLaneBulkForeground, files, completed)
			log.Printf("[temp] playlist item %s DB insert failed: %v", videoID, err)
			failed++
			continue
		}
		if err := m.publishCompletedVideoThumbnail(ctx, download.MediaLaneBulkForeground, videoID, "youtube", outputID, files); err != nil {
			log.Printf("[temp] playlist item %s thumbnail publish failed: %v", videoID, err)
		}
		m.removeTransientFiles(ctx, download.MediaLaneBulkForeground, files)
		m.RequestVideoPreview(videoID)
		downloaded++
	}

	if downloaded == 0 {
		return TempDownloadResult{Message: "Playlist download failed for all videos"}
	}

	msg := fmt.Sprintf("Playlist ready: %s (%d/%d)", playlistTitle, downloaded, len(entries))
	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}
	return TempDownloadResult{
		Success:    true,
		Message:    msg,
		PlaylistID: playlistID,
	}
}

// normalizeChannelID returns the channel_id in the platform_id convention used
// throughout the DB (e.g. "youtube_UCxxx", "twitter_handle"). yt-dlp returns
// raw IDs without the platform prefix, so we add it here to keep lookups,
// avatar resolution, and display helpers consistent.
func normalizeChannelID(platform, raw string) string {
	if raw == "" || raw == "temp" {
		return raw
	}
	prefix := platform + "_"
	if strings.HasPrefix(raw, prefix) {
		return raw
	}
	switch platform {
	case "twitter", "tiktok":
		return prefix + strings.ToLower(strings.TrimPrefix(raw, "@"))
	default:
		return prefix + raw
	}
}

func extractPlaylistID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("list")
}

func safeFolderName(raw string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "?", "_", "*", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name := replacer.Replace(strings.TrimSpace(raw))
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || filepath.Clean(name) != name {
		return "playlist"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

func stringFromMap(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
