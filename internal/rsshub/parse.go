// Package rsshub parses and enriches RSSHub RSS feeds into FeedItem structs.
package rsshub

import (
	"encoding/xml"
	"fmt"
	"io"
	"time"
)

// RSSFeed is the top-level parsed RSS document.
type RSSFeed struct {
	Title string
	Items []RSSItem
}

// RSSItem is a single <item> from the RSS feed.
type RSSItem struct {
	GUID        string
	Title       string
	Link        string
	Description string
	PubDate     time.Time
	Enclosures  []Enclosure
}

// Enclosure is an <enclosure> element within an <item>.
type Enclosure struct {
	URL    string
	Type   string
	Length int64
}

// rssEnvelope mirrors the raw RSS XML structure for decoding.
type rssEnvelope struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	GUID        string        `xml:"guid"`
	Title       string        `xml:"title"`
	Link        string        `xml:"link"`
	Description string        `xml:"description"`
	PubDate     string        `xml:"pubDate"`
	Enclosures  []rssEncl     `xml:"enclosure"`
}

type rssEncl struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length int64  `xml:"length,attr"`
}

// pubDateFormats lists formats tried in order for RSS pubDate parsing.
var pubDateFormats = []string{
	time.RFC1123,
	time.RFC1123Z,
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05+00:00",
}

func parsePubDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date string")
	}
	for _, layout := range pubDateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date format: %q", s)
}

// Parse decodes an RSS 2.0 document from r into an RSSFeed.
func Parse(r io.Reader) (*RSSFeed, error) {
	var env rssEnvelope
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("rsshub.Parse: xml decode: %w", err)
	}

	feed := &RSSFeed{
		Title: env.Channel.Title,
		Items: make([]RSSItem, 0, len(env.Channel.Items)),
	}

	for _, raw := range env.Channel.Items {
		item := RSSItem{
			GUID:        raw.GUID,
			Title:       raw.Title,
			Link:        raw.Link,
			Description: raw.Description,
		}
		if t, err := parsePubDate(raw.PubDate); err == nil {
			item.PubDate = t
		}
		for _, e := range raw.Enclosures {
			item.Enclosures = append(item.Enclosures, Enclosure{
				URL:    e.URL,
				Type:   e.Type,
				Length: e.Length,
			})
		}
		feed.Items = append(feed.Items, item)
	}

	return feed, nil
}
