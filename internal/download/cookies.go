package download

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type CookieSet struct {
	File    string
	Browser string
}

type CookieCandidate struct {
	Path  string
	Label string
}

func ResolveCookieSet(cookiesDir, platform string, fileEnabled bool, browser string) CookieSet {
	for _, set := range ResolveCookieSets(cookiesDir, platform, fileEnabled, browser) {
		return set
	}
	return CookieSet{}
}

func CookieFileAndBrowser(sets []CookieSet) (string, string) {
	file := ""
	browser := ""
	for _, set := range sets {
		if file == "" {
			file = strings.TrimSpace(set.File)
		}
		if browser == "" {
			browser = strings.TrimSpace(set.Browser)
		}
		if file != "" && browser != "" {
			return file, browser
		}
	}
	return file, browser
}

func ResolveCookieSets(cookiesDir, platform string, fileEnabled bool, browser string) []CookieSet {
	var sets []CookieSet
	if fileEnabled {
		for _, candidate := range DiscoverCookieFiles(cookiesDir, platform) {
			sets = append(sets, CookieSet{File: candidate.Path})
		}
	}
	if browser = strings.TrimSpace(browser); browser != "" {
		sets = append(sets, CookieSet{Browser: browser})
	}
	return sets
}

func DiscoverCookieFiles(cookiesDir, platform string) []CookieCandidate {
	cookiesDir = strings.TrimSpace(cookiesDir)
	if cookiesDir == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var paths []string
	for _, pattern := range cookieFilePatterns(platform) {
		for _, p := range globCookieFiles(filepath.Join(cookiesDir, pattern)) {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	out := make([]CookieCandidate, 0, len(paths))
	for _, p := range paths {
		out = append(out, CookieCandidate{Path: p, Label: CookieLabel(p, "")})
	}
	return out
}

func cookieFilePatterns(platform string) []string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "twitter", "x":
		return []string{"x.com_cookies*.txt", "www.x.com_cookies*.txt", "twitter_cookies*.txt", "twitter.com_cookies*.txt", "www.twitter.com_cookies*.txt", "x_cookies*.txt", "cookies*.txt"}
	case "instagram":
		return []string{"instagram_cookies*.txt", "instagram.com_cookies*.txt", "www.instagram.com_cookies*.txt"}
	case "tiktok":
		return []string{"tiktok_cookies*.txt", "tiktok.com_cookies*.txt", "www.tiktok.com_cookies*.txt"}
	case "youtube":
		return []string{"youtube_cookies*.txt", "youtube.com_cookies*.txt", "www.youtube.com_cookies*.txt", "cookies.txt"}
	default:
		return []string{platform + "_cookies*.txt"}
	}
}

func globCookieFiles(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	var out []string
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Size() > 0 {
			out = append(out, p)
		}
	}
	return out
}

func CookieLabel(file, browser string) string {
	if file != "" {
		return filepath.Base(file)
	}
	if browser != "" {
		return "browser:" + strings.TrimSpace(browser)
	}
	return ""
}
