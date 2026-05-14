package sponsorblock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the production SponsorBlockServer host.
const DefaultBaseURL = "https://sponsor.ajay.app"

// Categories is the set of SponsorBlock categories we request and accept.
// Mirrors the player's default sponsorblock_categories setting shape.
var Categories = []string{
	"sponsor", "selfpromo", "interaction", "intro",
	"outro", "preview", "filler", "music_offtopic",
}

// Segment is one SponsorBlock skip region, in seconds from the start of the video.
type Segment struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Category string  `json:"category"`
}

// Client talks to the SponsorBlock skipSegments API. Kept narrow on purpose —
// the worker and the web handler share it.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a Client with a 5s HTTP timeout.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Default is a package-level client against DefaultBaseURL, for callers that
// don't need to customize the host.
var Default = NewClient(DefaultBaseURL)

// Fetch retrieves all SponsorBlock segments in Categories for videoID.
// 404 is treated as "no segments" (nil, nil) — not an error.
func (c *Client) Fetch(ctx context.Context, videoID string) ([]Segment, error) {
	catsJSON, _ := json.Marshal(Categories)
	base := strings.TrimRight(c.BaseURL, "/")
	apiURL := fmt.Sprintf(
		"%s/api/skipSegments?videoID=%s&categories=%s",
		base, url.QueryEscape(videoID), url.QueryEscape(string(catsJSON)),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "igloo/1.0")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("sponsorblock: http %d", resp.StatusCode)
	}

	var data []struct {
		Segment  [2]float64 `json:"segment"`
		Category string     `json:"category"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return nil, err
	}

	catSet := make(map[string]bool, len(Categories))
	for _, c := range Categories {
		catSet[c] = true
	}

	var segments []Segment
	for _, item := range data {
		if catSet[item.Category] {
			segments = append(segments, Segment{
				Start:    item.Segment[0],
				End:      item.Segment[1],
				Category: item.Category,
			})
		}
	}
	return segments, nil
}

// Fetch is a convenience for callers that don't need to override the host.
func Fetch(ctx context.Context, videoID string) ([]Segment, error) {
	return Default.Fetch(ctx, videoID)
}
