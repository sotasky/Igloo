package dearrow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the production SponsorBlockServer host.
const DefaultBaseURL = "https://sponsor.ajay.app"

// Client talks to the DeArrow branding API (hosted on SponsorBlockServer).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a Client with a 15s HTTP timeout.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Result is the distilled branding for one video. Any combination of fields
// may be nil — caller is expected to fall back to the original when nil.
type Result struct {
	Title          *string  // community-voted non-original title (beats original)
	CasualTitle    *string  // top casual-voted title text
	ThumbTimestamp *float64 // community-voted thumbnail timestamp (seconds)
}

// api* types mirror the wire format. Votes is float64 because the API can
// return fractional values (verification boosts).
type apiTitle struct {
	Title    string  `json:"title"`
	Original bool    `json:"original"`
	Votes    float64 `json:"votes"`
	Locked   bool    `json:"locked"`
}

type apiThumb struct {
	Timestamp *float64 `json:"timestamp"`
	Original  bool     `json:"original"`
	Votes     float64  `json:"votes"`
	Locked    bool     `json:"locked"`
}

type apiCasual struct {
	ID    string  `json:"id"`
	Count float64 `json:"count"`
	Title *string `json:"title"`
}

type apiResp struct {
	Titles      []apiTitle  `json:"titles"`
	Thumbnails  []apiThumb  `json:"thumbnails"`
	CasualVotes []apiCasual `json:"casualVotes"`
}

// Fetch retrieves branding for videoID. 404 is treated as empty (no error).
func (c *Client) Fetch(ctx context.Context, videoID string) (Result, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	u := base + "/api/branding?videoID=" + url.QueryEscape(videoID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return Result{}, nil
	}
	if resp.StatusCode != 200 {
		return Result{}, fmt.Errorf("dearrow: http %d", resp.StatusCode)
	}
	var data apiResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return Result{}, err
	}
	return pickWinners(data), nil
}

// pickWinners distills an apiResp into a Result by the rules documented in
// Fetch's doc: highest-vote non-original title and thumbnail; highest-count
// casual vote that carries a non-empty title.
func pickWinners(data apiResp) Result {
	var r Result

	var bestT *apiTitle
	for i := range data.Titles {
		t := &data.Titles[i]
		if t.Original || t.Votes <= 0 {
			continue
		}
		if bestT == nil || t.Votes > bestT.Votes {
			bestT = t
		}
	}
	if bestT != nil {
		title := bestT.Title
		r.Title = &title
	}

	var bestTh *apiThumb
	for i := range data.Thumbnails {
		th := &data.Thumbnails[i]
		if th.Original || th.Timestamp == nil || th.Votes <= 0 {
			continue
		}
		if bestTh == nil || th.Votes > bestTh.Votes {
			bestTh = th
		}
	}
	if bestTh != nil {
		ts := *bestTh.Timestamp
		r.ThumbTimestamp = &ts
	}

	var bestC *apiCasual
	for i := range data.CasualVotes {
		c := &data.CasualVotes[i]
		if c.Title == nil || *c.Title == "" || c.Count <= 0 {
			continue
		}
		if bestC == nil || c.Count > bestC.Count {
			bestC = c
		}
	}
	if bestC != nil {
		ct := *bestC.Title
		r.CasualTitle = &ct
	}

	return r
}
