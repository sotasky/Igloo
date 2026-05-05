package model

import (
	"regexp"
	"strings"
)

var (
	instagramHandleRe = regexp.MustCompile(`^[a-z0-9._]{1,64}$`)
	tikTokHandleRe    = regexp.MustCompile(`^[a-z0-9_.]{1,64}$`)
	tikTokVideoIDRe   = regexp.MustCompile(`^[0-9]+$`)
)

// NormalizeTikTokHandle trims UI decoration and normalizes TikTok handles for
// channel IDs. TikTok handles are ASCII-only; display names stay elsewhere.
func NormalizeTikTokHandle(raw string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
}

// IsTikTokInternalID identifies TikTok IDs that are not user-facing handles.
// The two forms observed locally are numeric author IDs and secUid values.
func IsTikTokInternalID(raw string) bool {
	value := NormalizeTikTokHandle(raw)
	if value == "" {
		return false
	}
	return isLongNumericID(value) || strings.HasPrefix(value, "ms4wljab")
}

func TikTokChannelIDFromHandle(raw string) string {
	handle := NormalizeTikTokHandle(raw)
	if !IsValidTikTokHandle(handle) {
		return ""
	}
	return "tiktok_" + handle
}

func TikTokHandleFromChannelID(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if len(channelID) <= len("tiktok_") || !strings.HasPrefix(strings.ToLower(channelID), "tiktok_") {
		return ""
	}
	handle := NormalizeTikTokHandle(channelID[len("tiktok_"):])
	if !IsValidTikTokHandle(handle) {
		return ""
	}
	return handle
}

// NormalizeInstagramHandle trims UI decoration and normalizes Instagram handles
// for channel IDs. Instagram handles can contain dots, underscores, and digits;
// do not apply TikTok internal-ID rejection here.
func NormalizeInstagramHandle(raw string) string {
	handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if !instagramHandleRe.MatchString(handle) {
		return ""
	}
	return handle
}

func InstagramChannelIDFromHandle(raw string) string {
	handle := NormalizeInstagramHandle(raw)
	if handle == "" {
		return ""
	}
	return "instagram_" + handle
}

func InstagramHandleFromChannelID(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if len(channelID) <= len("instagram_") || !strings.HasPrefix(strings.ToLower(channelID), "instagram_") {
		return ""
	}
	return NormalizeInstagramHandle(channelID[len("instagram_"):])
}

func isLongNumericID(value string) bool {
	if len(value) < 16 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func IsValidTikTokHandle(raw string) bool {
	handle := NormalizeTikTokHandle(raw)
	if handle == "" || IsTikTokInternalID(handle) {
		return false
	}
	return tikTokHandleRe.MatchString(handle)
}

func IsValidTikTokVideoID(raw string) bool {
	return tikTokVideoIDRe.MatchString(strings.TrimSpace(raw))
}
