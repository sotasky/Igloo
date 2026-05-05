package download

import (
	"regexp"
)

var tiktokIDRe = regexp.MustCompile(`/(video|photo)/(\d+)`)

// IsTikTokURL reports whether u looks like a TikTok or tnktok URL.
func IsTikTokURL(u string) bool {
	host, _, ok := httpURLParts(u)
	return ok && hostMatches(host, "tiktok.com", "tnktok.com")
}

// extractTikTokID extracts the numeric post ID from a TikTok URL.
// Returns "" if not found.
func extractTikTokID(rawURL string) string {
	m := tiktokIDRe.FindStringSubmatch(rawURL)
	if len(m) < 3 {
		return ""
	}
	return m[2]
}
