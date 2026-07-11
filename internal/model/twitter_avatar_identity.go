package model

import (
	"net/url"
	"strings"
)

func NormalizeTwitterHandle(raw string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "@")))
}

// TwitterChannelIDFromHandle returns the canonical persisted identity for a
// usable X handle. Placeholder or malformed handles are not identities.
func TwitterChannelIDFromHandle(raw string) string {
	handle := NormalizeTwitterHandle(raw)
	if IsPlaceholderTwitterHandle(handle) || len(handle) > 15 {
		return ""
	}
	for _, r := range handle {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return ""
		}
	}
	return "twitter_" + handle
}

func IsPlaceholderTwitterHandle(raw string) bool {
	normalized := NormalizeTwitterHandle(raw)
	return normalized == "" || normalized == "unknown" || normalized == "undefined"
}

func EffectiveTwitterAuthorHandle(author, source string, isRetweet bool) string {
	trimmedAuthor := strings.TrimSpace(strings.TrimPrefix(author, "@"))
	if !isRetweet && IsPlaceholderTwitterHandle(trimmedAuthor) {
		trimmedSource := strings.TrimSpace(strings.TrimPrefix(source, "@"))
		if !IsPlaceholderTwitterHandle(trimmedSource) {
			return trimmedSource
		}
	}
	return trimmedAuthor
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

func TwitterStatusIDFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "x.com", "twitter.com", "fxtwitter.com", "vxtwitter.com":
	default:
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if !strings.EqualFold(parts[i], "status") {
			continue
		}
		id := strings.TrimSpace(parts[i+1])
		end := 0
		for end < len(id) && id[end] >= '0' && id[end] <= '9' {
			end++
		}
		return id[:end]
	}
	return ""
}
