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

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/fetchprofile"
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
	authOpts := download.Opts{
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
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
	tempDir := filepath.Join(m.cfg.DataDir, "media", "temp")
	os.MkdirAll(tempDir, 0755)

	opts := download.Opts{
		OutputDir:          tempDir,
		ID:                 videoID,
		Cookies:            cookiesFile,
		CookiesFromBrowser: cookiesBrowser,
		Subtitles:          true,
	}

	paths, dlErr := m.downloader.Download(ctx, rawURL, "video", opts)
	if dlErr != nil || len(paths) == 0 {
		msg := "Download failed"
		if dlErr != nil {
			msg = dlErr.Error()
		}
		return TempDownloadResult{Message: msg}
	}

	filePath := paths[0]
	relPath := toRelPath(m.cfg.DataDir, filePath)
	thumbPath := findSiblingThumbnail(filePath, videoID)
	relThumb := ""
	if thumbPath != "" {
		relThumb = toRelPath(m.cfg.DataDir, thumbPath)
	}

	publishedAt := extractPublishedAt(info)
	description, _ := info["description"].(string)
	duration := extractDurationFromMetadata(info)
	fileSize := int64(0)
	if fi, err := os.Stat(filePath); err == nil {
		fileSize = fi.Size()
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
			mediaKind, slideCount = model.ComputeMediaKind(&meta, relPath)
		}
	}
	if mediaKind == "" {
		mediaKind, slideCount = model.ComputeMediaKind(nil, relPath)
	}

	// Upsert channel.
	m.db.AddChannel(model.Channel{
		ChannelID:    channelID,
		SourceID:     strings.TrimPrefix(stringFromMap(info, "uploader_id"), "@"),
		Name:         channelName,
		URL:          channelURL,
		Platform:     platform,
		IsSubscribed: saveChannel,
	})

	// Insert video.
	if err := m.db.InsertVideo(videoID, channelID, title, description,
		duration, relThumb, relPath, fileSize, publishedAt, metadataJSON, mediaKind, slideCount, true); err != nil {
		return TempDownloadResult{Message: fmt.Sprintf("DB insert: %v", err)}
	}

	// Enqueue preview.
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(m.cfg.DataDir, absPath)
	}
	m.EnqueuePreview(PreviewRequest{
		VideoID:  videoID,
		FilePath: absPath,
		Duration: float64(duration),
	})

	// Synchronously fetch avatar before returning so the player page shows it
	// on first load. Async RequestAvatar leaves a window where the player page
	// loads before the file exists — see internal/web/pages.go handlePagePlayer.
	avatarCtx, avatarCancel := context.WithTimeout(ctx, 20*time.Second)
	avDir := filepath.Join(m.cfg.DataDir, "thumbnails", "avatars")
	bnDir := filepath.Join(m.cfg.DataDir, "thumbnails", "banners")
	if err := os.MkdirAll(avDir, 0o755); err != nil {
		log.Printf("[temp] mkdir avatar dir: %v", err)
	}
	m.refreshProfile(avatarCtx, fetchprofile.Fetch, channelID, avDir, bnDir)
	avatarCancel()

	// Synchronously fetch comments so they appear on first player page load.
	commentsCtx, commentsCancel := context.WithTimeout(ctx, 2*time.Minute)
	comments, commentsErr := m.downloader.YtDlp.FetchComments(commentsCtx, rawURL, download.DefaultCommentFetchLimit, opts)
	commentsCancel()
	if commentsErr != nil {
		log.Printf("[temp] comments fetch failed for %s: %v", videoID, commentsErr)
	} else if len(comments) > 0 {
		inserted, _ := m.db.AddComments(videoID, comments, platform)
		m.queueYouTubeCommentAuthorAvatars(comments)
		log.Printf("[temp] fetched %d comments for %s", inserted, videoID)
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

	targetDir := filepath.Join(m.cfg.DataDir, "media", "playlists", safeFolderName(playlistTitle))
	os.MkdirAll(targetDir, 0755)

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
		opts := download.Opts{
			OutputDir:          targetDir,
			ID:                 videoID,
			Cookies:            authOpts.Cookies,
			CookiesFromBrowser: authOpts.CookiesFromBrowser,
		}
		paths, dlErr := m.downloader.YtDlp.Download(ctx, videoURL, opts)
		if dlErr != nil || len(paths) == 0 {
			log.Printf("[temp] playlist item %s failed: %v", videoID, dlErr)
			failed++
			continue
		}

		filePath := paths[0]
		relPath := toRelPath(m.cfg.DataDir, filePath)
		thumbPath := findSiblingThumbnail(filePath, videoID)
		relThumb := ""
		if thumbPath != "" {
			relThumb = toRelPath(m.cfg.DataDir, thumbPath)
		}
		metadata := loadInfoJSON(filePath, videoID)
		publishedAt := extractPublishedAt(metadata)
		description, _ := metadata["description"].(string)
		duration := 0
		if d, ok := metadata["duration"].(float64); ok {
			duration = int(d)
		}
		fileSize := int64(0)
		if fi, err := os.Stat(filePath); err == nil {
			fileSize = fi.Size()
		}
		metaJSON := ""
		if b, err := json.Marshal(metadata); err == nil {
			metaJSON = string(b)
		}

		m.db.InsertVideo(videoID, playlistChannelID, entryTitle, description,
			duration, relThumb, relPath, fileSize, publishedAt, metaJSON, "", 0, false)
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
	if name == "" {
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
