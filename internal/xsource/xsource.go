package xsource

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

// ParseURL returns the Igloo feed-source identity for X list/community URLs.
// Account URLs are intentionally rejected so they continue through the normal
// channel follow flow.
func ParseURL(rawURL string) (model.FeedSource, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return model.FeedSource{}, fmt.Errorf("parse url: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host != "x.com" && host != "twitter.com" && host != "www.x.com" && host != "www.twitter.com" {
		return model.FeedSource{}, fmt.Errorf("unsupported X source host")
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 3 || parts[0] != "i" {
		return model.FeedSource{}, fmt.Errorf("not an X list or community URL")
	}
	sourceType := ""
	switch parts[1] {
	case "lists":
		sourceType = "list"
	case "communities":
		sourceType = "community"
	default:
		return model.FeedSource{}, fmt.Errorf("not an X list or community URL")
	}
	externalID, err := url.PathUnescape(parts[2])
	if err != nil || strings.TrimSpace(externalID) == "" {
		return model.FeedSource{}, fmt.Errorf("missing X source id")
	}
	externalID = strings.TrimSpace(externalID)
	prefix := "twitter_list_"
	pathKind := "lists"
	label := "X List " + externalID
	if sourceType == "community" {
		prefix = "twitter_community_"
		pathKind = "communities"
		label = "X Community " + externalID
	}
	return model.FeedSource{
		SourceID:   prefix + externalID,
		Platform:   "twitter",
		SourceType: sourceType,
		ExternalID: externalID,
		Label:      label,
		URL:        "https://x.com/i/" + pathKind + "/" + url.PathEscape(externalID),
		Enabled:    true,
	}, nil
}
