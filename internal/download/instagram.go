package download

import (
	"net/url"
	"strings"
)

// IsInstagramURL reports whether u looks like an Instagram URL.
func IsInstagramURL(u string) bool {
	host, _, ok := httpURLParts(u)
	return ok && hostMatches(host, "instagram.com")
}

func canonicalInstagramURL(raw string) string {
	if !IsInstagramURL(raw) {
		return raw
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.Scheme = "https"
	u.Host = "www.instagram.com"
	return u.String()
}
