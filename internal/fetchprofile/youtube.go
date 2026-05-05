package fetchprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const ytTimeout = 30 * time.Second

type youtubeThumb struct {
	ID     string
	URL    string
	Width  int
	Height int
}

// FetchYouTube invokes yt-dlp to get the channel metadata JSON for rawID
// (channel_id without the 'youtube_' prefix). Uses the same flags pattern
// as the legacy avatar worker.
func FetchYouTube(ctx context.Context, rawID string) (*Profile, error) {
	if strings.TrimSpace(rawID) == "" {
		return nil, ErrNotFound
	}
	cmdCtx, cancel := context.WithTimeout(ctx, ytTimeout)
	defer cancel()
	channelURL := "https://www.youtube.com/channel/" + rawID
	cmd := exec.CommandContext(cmdCtx, "yt-dlp",
		"--dump-single-json",
		"--playlist-items", "0",
		"--skip-download",
		"--no-warnings",
		channelURL,
	)
	out, err := cmd.Output()
	if err != nil {
		// yt-dlp returns non-zero for "channel not found" and transient
		// network errors alike. Map stderr signal to ErrNotFound; else bubble.
		if ee, ok := err.(*exec.ExitError); ok && isYouTubeNotFound(ee.Stderr) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}
	return parseYouTubeDump(rawID, out)
}

func isYouTubeNotFound(stderr []byte) bool {
	s := strings.ToLower(string(stderr))
	return strings.Contains(s, "does not exist") ||
		strings.Contains(s, "channel not found") ||
		strings.Contains(s, "unable to find")
}

// parseYouTubeDump parses the first valid JSON object out of yt-dlp's
// --dump-single-json output.
func parseYouTubeDump(rawID string, out []byte) (*Profile, error) {
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	var meta struct {
		Channel              string         `json:"channel"`
		ChannelURL           string         `json:"channel_url"`
		Uploader             string         `json:"uploader"`
		UploaderID           string         `json:"uploader_id"`
		Title                string         `json:"title"`
		Description          string         `json:"description"`
		ChannelFollowerCount int            `json:"channel_follower_count"`
		ChannelThumbnail     string         `json:"channel_thumbnail"`
		Thumbnail            string         `json:"thumbnail"`
		Thumbnails           []youtubeThumb `json:"thumbnails"`
	}
	// yt-dlp with --dump-single-json emits one object on one line.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if json.Unmarshal([]byte(line), &meta) == nil {
			break
		}
	}

	displayName := meta.Channel
	if displayName == "" {
		displayName = meta.Uploader
	}
	if displayName == "" {
		displayName = meta.Title
	}

	avatarURL, bannerURL := selectYouTubeAvatarAndBanner(
		meta.ChannelThumbnail,
		meta.Thumbnail,
		meta.Thumbnails,
	)

	// Handle — yt-dlp exposes @handle via uploader_id for newer channels.
	handle := ""
	if strings.HasPrefix(meta.UploaderID, "@") {
		handle = meta.UploaderID
	}

	return &Profile{
		ChannelID:   "youtube_" + rawID,
		Platform:    "youtube",
		Handle:      handle,
		DisplayName: displayName,
		Bio:         meta.Description,
		Followers:   meta.ChannelFollowerCount,
		AvatarURL:   avatarURL,
		BannerURL:   bannerURL,
	}, nil
}

func selectYouTubeAvatarAndBanner(
	channelThumbnail string,
	thumbnail string,
	raw []youtubeThumb,
) (string, string) {
	var thumbs []youtubeThumb
	if channelThumbnail != "" {
		thumbs = append(thumbs, youtubeThumb{
			ID:  "channel_thumbnail",
			URL: channelThumbnail,
		})
	}
	if thumbnail != "" {
		thumbs = append(thumbs, youtubeThumb{
			ID:  "thumbnail",
			URL: thumbnail,
		})
	}
	for _, t := range raw {
		if strings.TrimSpace(t.URL) == "" {
			continue
		}
		thumbs = append(thumbs, youtubeThumb{
			ID:     t.ID,
			URL:    t.URL,
			Width:  t.Width,
			Height: t.Height,
		})
	}
	if len(thumbs) == 0 {
		return "", ""
	}

	var avatarCandidates []youtubeThumb
	var bannerCandidates []youtubeThumb
	for _, t := range thumbs {
		id := strings.ToLower(strings.TrimSpace(t.ID))
		if isYouTubeBannerThumb(id, t.Width, t.Height) {
			bannerCandidates = append(bannerCandidates, t)
			continue
		}
		avatarCandidates = append(avatarCandidates, t)
	}
	if len(avatarCandidates) == 0 {
		avatarCandidates = thumbs
	}
	sort.SliceStable(avatarCandidates, func(i, j int) bool {
		return youtubeAvatarScore(avatarCandidates[i]) > youtubeAvatarScore(avatarCandidates[j])
	})
	sort.SliceStable(bannerCandidates, func(i, j int) bool {
		return youtubeBannerScore(bannerCandidates[i]) > youtubeBannerScore(bannerCandidates[j])
	})

	avatarURL := avatarCandidates[0].URL
	bannerURL := ""
	if len(bannerCandidates) > 0 {
		bannerURL = bannerCandidates[0].URL
	}
	return avatarURL, bannerURL
}

func isYouTubeBannerThumb(id string, width, height int) bool {
	if strings.Contains(id, "banner") {
		return true
	}
	return width > 0 && height > 0 && float64(width)/float64(height) >= 2.2
}

func youtubeAvatarScore(t youtubeThumb) int {
	id := strings.ToLower(strings.TrimSpace(t.ID))
	score := 0
	if strings.Contains(id, "avatar") {
		score += 20_000
	}
	if id == "channel_thumbnail" {
		score += 3_000
	}
	if t.Width > 0 && t.Height > 0 {
		delta := t.Width - t.Height
		if delta < 0 {
			delta = -delta
		}
		score -= delta * 10
		if float64(t.Width)/float64(t.Height) <= 1.2 {
			score += 8_000
		}
		if float64(t.Width)/float64(t.Height) >= 2.0 {
			score -= 20_000
		}
		score += min(t.Width*t.Height, 4_000_000) / 500
	}
	return score
}

func youtubeBannerScore(t youtubeThumb) int {
	id := strings.ToLower(strings.TrimSpace(t.ID))
	score := 0
	if strings.Contains(id, "banner") {
		score += 20_000
	}
	if t.Width > 0 && t.Height > 0 {
		score += min(t.Width*t.Height, 8_000_000) / 500
	}
	return score
}
