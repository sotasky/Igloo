// Package subscribe provides platform detection and channel resolution helpers
// used by the subscribe API handler.
package subscribe

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

// DetectPlatform returns the platform name for the given URL/handle.
// If explicitPlatform is non-empty it is returned unchanged.
// Otherwise the URL is inspected:
//
//	youtube.com / youtu.be  → "youtube"
//	tiktok.com / tnktok.com → "tiktok"
//	x.com / twitter.com     → "twitter"
//	instagram.com           → "instagram"
//	bare handle (@foo / foo) → "twitter"
func DetectPlatform(rawURL, explicitPlatform string) string {
	if strings.TrimSpace(explicitPlatform) != "" {
		return strings.ToLower(strings.TrimSpace(explicitPlatform))
	}
	if host, ok := inputURLHost(rawURL); ok {
		switch {
		case isYouTubeHost(host):
			return "youtube"
		case isTikTokHost(host):
			return "tiktok"
		case isTwitterHost(host):
			return "twitter"
		case isInstagramHost(host):
			return "instagram"
		default:
			return ""
		}
	}
	if twitterHandleOnlyRe.MatchString(strings.TrimSpace(rawURL)) {
		return "twitter"
	}
	return ""
}

func ValidateInput(rawURL, platform string) error {
	rawURL = strings.TrimSpace(rawURL)
	platform = strings.ToLower(strings.TrimSpace(platform))
	if rawURL == "" {
		return fmt.Errorf("input required")
	}
	if platform == "" {
		return fmt.Errorf("unsupported URL host")
	}
	if platform == "twitter" && twitterHandleOnlyRe.MatchString(rawURL) {
		return nil
	}
	host, ok := inputURLHost(rawURL)
	if !ok {
		return fmt.Errorf("%s input must be an http(s) URL", platform)
	}
	switch {
	case platform == "youtube" && isYouTubeHost(host):
		return nil
	case platform == "tiktok" && isTikTokHost(host):
		return nil
	case platform == "twitter" && isTwitterHost(host):
		return nil
	case platform == "instagram" && isInstagramHost(host):
		return nil
	default:
		return fmt.Errorf("%s URL host is not supported", platform)
	}
}

var twitterHandleOnlyRe = regexp.MustCompile(`^@?[A-Za-z0-9_]{1,50}$`)
var instagramHandleRe = regexp.MustCompile(`^[A-Za-z0-9_.]{1,64}$`)

// ParseTwitterHandle extracts a clean, lower-case Twitter handle from:
//   - Full URLs: https://x.com/username or https://twitter.com/username/status/123
//   - @-prefixed handles: @username
//   - Bare handles: username
func ParseTwitterHandle(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if _, ok := inputURLHost(input); ok {
		if err := ValidateInput(input, "twitter"); err != nil {
			return ""
		}
		u, err := parseHTTPInput(input)
		if err != nil {
			return ""
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			return ""
		}
		handle := strings.TrimPrefix(parts[0], "@")
		if twitterHandleOnlyRe.MatchString(handle) {
			return strings.ToLower(handle)
		}
		return ""
	}
	// @handle or bare handle
	if !twitterHandleOnlyRe.MatchString(input) {
		return ""
	}
	handle := strings.TrimPrefix(input, "@")
	return strings.ToLower(handle)
}

// tiktokHandleRe matches @username in a TikTok or tnktok URL path.
var tiktokHandleRe = regexp.MustCompile(`(?i)(?:tiktok\.com|tnktok\.com)/@([A-Za-z0-9_.]{1,64})`)

func ParseInstagramHandle(input string) string {
	u, err := parseHTTPInput(input)
	if err != nil || !isInstagramHost(normalizedHost(u.Host)) {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	var handle string
	switch strings.ToLower(parts[0]) {
	case "p", "reel", "tv":
		handle = ""
	case "stories":
		if len(parts) > 1 {
			handle = parts[1]
		}
	default:
		handle = parts[0]
	}
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if instagramHandleRe.MatchString(handle) {
		return handle
	}
	return ""
}

func looksLikeYouTubeChannelID(id string) bool {
	id = strings.TrimSpace(id)
	return strings.HasPrefix(id, "UC") && len(id) >= 10
}

func parseYouTubeChannelID(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if !isYouTubeHost(normalizedHost(u.Host)) {
		return ""
	}
	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(pathParts); i++ {
		if strings.EqualFold(pathParts[i], "channel") && looksLikeYouTubeChannelID(pathParts[i+1]) {
			return pathParts[i+1]
		}
	}
	if id := u.Query().Get("channel_id"); looksLikeYouTubeChannelID(id) {
		return id
	}
	return ""
}

// ResolveChannel resolves a URL and platform into a model.Channel suitable for
// insertion into the channels table. For Twitter the channel is built locally
// (no network call). For YouTube and TikTok yt-dlp is consulted.
func ResolveChannel(ctx context.Context, rawURL, platform string, dl *download.Downloader) (model.Channel, error) {
	if err := ValidateInput(rawURL, platform); err != nil {
		return model.Channel{}, err
	}
	switch platform {
	case "twitter":
		handle := ParseTwitterHandle(rawURL)
		if handle == "" {
			return model.Channel{}, fmt.Errorf("could not parse Twitter handle from %q", rawURL)
		}
		return model.Channel{
			ChannelID:    "twitter_" + handle,
			SourceID:     handle,
			Name:         handle,
			URL:          "https://x.com/" + handle,
			Platform:     "twitter",
			IsSubscribed: true,
		}, nil

	case "tiktok":
		// Fast path: extract handle directly from URL with regex.
		if m := tiktokHandleRe.FindStringSubmatch(rawURL); len(m) == 2 {
			handle := strings.ToLower(m[1])
			return model.Channel{
				ChannelID:    "tiktok_" + handle,
				SourceID:     handle,
				Name:         handle,
				URL:          "https://www.tiktok.com/@" + handle,
				Platform:     "tiktok",
				IsSubscribed: true,
			}, nil
		}
		// Fallback: ask yt-dlp.
		info, err := dl.YtDlp.ChannelInfo(ctx, rawURL)
		if err != nil {
			return model.Channel{}, fmt.Errorf("resolve tiktok channel: %w", err)
		}
		sourceID := strings.ToLower(info.ID)
		return model.Channel{
			ChannelID:    "tiktok_" + sourceID,
			SourceID:     sourceID,
			Name:         info.Name,
			URL:          info.URL,
			Platform:     "tiktok",
			IsSubscribed: true,
		}, nil

	case "instagram":
		handle := ParseInstagramHandle(rawURL)
		if handle == "" {
			return model.Channel{}, fmt.Errorf("could not parse Instagram handle from %q", rawURL)
		}
		return model.Channel{
			ChannelID:    "instagram_" + handle,
			SourceID:     handle,
			Name:         handle,
			URL:          "https://www.instagram.com/" + handle + "/",
			Platform:     "instagram",
			IsSubscribed: true,
		}, nil

	case "youtube":
		if rawID := parseYouTubeChannelID(rawURL); rawID != "" {
			return model.Channel{
				ChannelID:    "youtube_" + rawID,
				SourceID:     rawID,
				Name:         rawID,
				URL:          "https://www.youtube.com/channel/" + rawID,
				Platform:     "youtube",
				IsSubscribed: true,
			}, nil
		}

		if dl == nil || dl.YtDlp == nil {
			return model.Channel{}, fmt.Errorf("youtube channel URL must include a /channel/UC... id")
		}
		info, err := dl.YtDlp.ChannelInfo(ctx, rawURL)
		if err != nil {
			return model.Channel{}, fmt.Errorf("resolve youtube channel: %w", err)
		}
		sourceID := strings.TrimPrefix(info.ID, "youtube_")
		return model.Channel{
			ChannelID:    info.ID,
			SourceID:     sourceID,
			Name:         info.Name,
			URL:          info.URL,
			Platform:     "youtube",
			IsSubscribed: true,
		}, nil

	default:
		return model.Channel{}, fmt.Errorf("unsupported platform: %q", platform)
	}
}

func inputURLHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return "", false
	}
	u, err := parseHTTPInput(raw)
	if err != nil {
		return "", false
	}
	return normalizedHost(u.Host), true
}

func parseHTTPInput(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") && strings.Contains(raw, ".") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme")
	}
	return u, nil
}

func normalizedHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	if h, err := url.Parse("//" + host); err == nil && h.Hostname() != "" {
		host = h.Hostname()
	}
	return strings.TrimPrefix(host, "www.")
}

func hostIs(host string, allowed ...string) bool {
	host = normalizedHost(host)
	for _, candidate := range allowed {
		candidate = normalizedHost(candidate)
		if host == candidate || strings.HasSuffix(host, "."+candidate) {
			return true
		}
	}
	return false
}

func isYouTubeHost(host string) bool {
	return hostIs(host, "youtube.com", "youtu.be")
}

func isTikTokHost(host string) bool {
	return hostIs(host, "tiktok.com", "tnktok.com")
}

func isTwitterHost(host string) bool {
	return hostIs(host, "x.com", "twitter.com", "fxtwitter.com", "fixupx.com")
}

func isInstagramHost(host string) bool {
	return hostIs(host, "instagram.com")
}
