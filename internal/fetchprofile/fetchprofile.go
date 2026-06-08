// Package fetchprofile fetches platform-generic profile data for a channel.
// Each platform uses its own upstream tool (fxtwitter HTTP for Twitter,
// yt-dlp for YouTube, gallery-dl for TikTok) but all return the same
// Profile struct so callers stay platform-polymorphic.
package fetchprofile

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound signals that the upstream definitively reports the handle
// doesn't exist (empty fxtwitter body, yt-dlp "channel not found",
// gallery-dl empty user object). Callers should tombstone.
var ErrNotFound = errors.New("fetchprofile: profile not found upstream")

// ErrIdentityMismatch signals that an upstream profile response resolved to a
// different platform identity than the channel_id being refreshed. Callers
// should treat this as transient and preserve existing local data.
var ErrIdentityMismatch = errors.New("fetchprofile: profile identity mismatch")

// Profile is the platform-generic profile record.
type Profile struct {
	ChannelID    string
	Platform     string
	Handle       string
	DisplayName  string
	Bio          string
	Website      string
	Followers    int // 0 if unavailable for platform
	Following    int // 0 if unavailable
	Verified     bool
	VerifiedType string // twitter only
	Protected    bool   // twitter only
	AvatarURL    string
	BannerURL    string // "" if platform has no banner concept
}

func ValidateChannelIdentity(channelID string, p *Profile) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || p == nil {
		return nil
	}
	if p.ChannelID != "" && !strings.EqualFold(strings.TrimSpace(p.ChannelID), channelID) {
		return fmt.Errorf("%w: requested %s returned %s", ErrIdentityMismatch, channelID, p.ChannelID)
	}
	if handle, ok := strings.CutPrefix(strings.ToLower(channelID), "twitter_"); ok {
		got := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(p.Handle, "@")))
		if got != "" && got != handle {
			return fmt.Errorf("%w: requested @%s returned @%s", ErrIdentityMismatch, handle, got)
		}
	}
	return nil
}

// normalizeURL prepends "https://" to a URL that lacks a scheme so it renders
// as an absolute link, not a path relative to the current page.
func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return "https://" + u
}

// Fetch dispatches by channel_id prefix.
func Fetch(ctx context.Context, channelID string) (*Profile, error) {
	switch {
	case strings.HasPrefix(channelID, "twitter_"):
		return FetchTwitter(ctx, strings.TrimPrefix(channelID, "twitter_"))
	case strings.HasPrefix(channelID, "youtube_"):
		return FetchYouTube(ctx, strings.TrimPrefix(channelID, "youtube_"))
	case strings.HasPrefix(channelID, "tiktok_"):
		return FetchTikTok(ctx, strings.TrimPrefix(channelID, "tiktok_"))
	case strings.HasPrefix(channelID, "instagram_"):
		handle := strings.TrimPrefix(channelID, "instagram_")
		return &Profile{
			ChannelID:   channelID,
			Platform:    "instagram",
			Handle:      handle,
			DisplayName: handle,
		}, nil
	default:
		return nil, fmt.Errorf("fetchprofile: unknown platform for channel_id %q", channelID)
	}
}
