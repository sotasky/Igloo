package download

import (
	"net/url"
	"strings"
)

func httpURLParts(raw string) (host, path string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", false
	}
	return normalizedHost(u.Host), u.EscapedPath(), true
}

func normalizedHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	if h, err := url.Parse("//" + host); err == nil && h.Hostname() != "" {
		host = h.Hostname()
	}
	return strings.TrimPrefix(host, "www.")
}

func hostMatches(host string, allowed ...string) bool {
	host = normalizedHost(host)
	for _, candidate := range allowed {
		candidate = normalizedHost(candidate)
		if host == candidate || strings.HasSuffix(host, "."+candidate) {
			return true
		}
	}
	return false
}
