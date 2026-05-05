package model

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"strings"
)

func NormalizeTwitterHandle(raw string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "@")))
}

func NormalizeTwitterAvatarURL(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func IsInvalidTwitterAvatarURL(raw string) bool {
	normalized := NormalizeTwitterAvatarURL(raw)
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "/status/undefined") {
		return true
	}
	if (strings.HasPrefix(normalized, "https://x.com/") ||
		strings.HasPrefix(normalized, "http://x.com/") ||
		strings.HasPrefix(normalized, "https://twitter.com/") ||
		strings.HasPrefix(normalized, "http://twitter.com/")) &&
		strings.Contains(normalized, "/status/") {
		return true
	}
	return false
}

func CleanFeedAvatarURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if IsInvalidTwitterAvatarURL(trimmed) {
		return ""
	}
	return trimmed
}

func IsRawTwitterProfileAvatar(raw string) bool {
	raw = NormalizeTwitterAvatarURL(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	return host == "pbs.twimg.com" && strings.HasPrefix(u.EscapedPath(), "/profile_images/")
}

func SyntheticTwitterAvatarChannelID(raw string) string {
	normalized := NormalizeTwitterAvatarURL(raw)
	if !IsRawTwitterProfileAvatar(normalized) {
		return ""
	}
	sum := sha1.Sum([]byte(normalized))
	return "twitter_avatarhash_" + hex.EncodeToString(sum[:8])
}

func IsSyntheticTwitterAvatarChannelID(channelID string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(channelID)), "twitter_avatarhash_")
}

func TwitterAvatarChannelID(handle, avatarURL string) string {
	if normalized := NormalizeTwitterHandle(handle); normalized != "" {
		return "twitter_" + normalized
	}
	return SyntheticTwitterAvatarChannelID(avatarURL)
}
