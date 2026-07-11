package web

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

var youtubeHandleRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
var youtubeReservedPathSegments = map[string]bool{
	"channel":       true,
	"clip":          true,
	"c":             true,
	"embed":         true,
	"feed":          true,
	"feeds":         true,
	"hashtag":       true,
	"playlist":      true,
	"results":       true,
	"shorts":        true,
	"user":          true,
	"watch":         true,
	"youtubei":      true,
	"youtubei_over": true,
}

func (s *Server) resolveLocalYouTubeSubscribeChannel(rawURL, platform string) (model.Channel, bool, error) {
	if strings.ToLower(strings.TrimSpace(platform)) != "youtube" {
		return model.Channel{}, false, nil
	}
	handle := parseYouTubeHandleFromURL(rawURL)
	if handle == "" {
		return model.Channel{}, false, nil
	}
	profile, err := s.db.GetYouTubeChannelProfileByHandle(handle)
	if err != nil || profile == nil {
		return model.Channel{}, false, err
	}

	if ch, err := s.db.GetChannel(profile.ChannelID); err != nil {
		return model.Channel{}, false, err
	} else if ch != nil {
		ch.IsSubscribed = true
		return *ch, true, nil
	}

	sourceID := strings.TrimPrefix(profile.ChannelID, "youtube_")
	name := strings.TrimSpace(profile.DisplayName)
	if name == "" {
		name = strings.TrimPrefix(profile.Handle, "@")
	}
	if name == "" {
		name = sourceID
	}
	return model.Channel{
		ChannelID:    profile.ChannelID,
		SourceID:     sourceID,
		Name:         name,
		URL:          fmt.Sprintf("https://www.youtube.com/channel/%s", sourceID),
		Platform:     "youtube",
		IsSubscribed: true,
	}, true, nil
}

func parseYouTubeHandleFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if !strings.Contains(rawURL, "://") && strings.Contains(rawURL, ".") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host != "youtube.com" && !strings.HasSuffix(host, ".youtube.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	rawHandle := strings.TrimPrefix(parts[0], "@")
	if !strings.HasPrefix(parts[0], "@") {
		if len(parts) != 1 || youtubeReservedPathSegments[strings.ToLower(parts[0])] {
			return ""
		}
	}
	handle, err := url.PathUnescape(rawHandle)
	if err != nil {
		return ""
	}
	if !youtubeHandleRe.MatchString(handle) {
		return ""
	}
	return strings.ToLower(handle)
}
