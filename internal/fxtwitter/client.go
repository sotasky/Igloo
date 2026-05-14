// Package fxtwitter is a thin client for the fxtwitter community API.
// Used by the avatar worker (for avatar_url) and the profile worker (for
// everything else). Both callers hit api.fxtwitter.com on their own
// cadences — this package has no shared state beyond a reusable Client.
package fxtwitter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.fxtwitter.com"

// ErrNotFound is returned when fxtwitter returns 404 or an empty body, which is
// its observed behavior for handles that don't exist.
var ErrNotFound = errors.New("fxtwitter: user not found")

// User mirrors the subset of fxtwitter's JSON we use.
type User struct {
	ID           string
	ScreenName   string
	Name         string
	Description  string
	Location     string
	Website      string
	AvatarURL    string
	BannerURL    string
	Followers    int
	Following    int
	Tweets       int
	MediaCount   int
	Likes        int
	Verified     bool
	VerifiedType string
	Protected    bool
	Joined       time.Time
}

// Tweet mirrors the subset of fxtwitter's /status/<id> JSON we use.
type Tweet struct {
	ID                string
	AuthorHandle      string
	AuthorDisplayName string
	AuthorAvatarURL   string
	Text              string
	Lang              string
	ReplyToHandle     string // "" if not a reply
	ReplyToStatus     string // "" if not a reply
	CreatedAt         time.Time
	MediaJSON         string // serialized []model.MediaRef, "" if no media
	Quote             *Tweet
}

// Client wraps HTTP + base URL for easy testing.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Timeout time.Duration
}

// NewClient returns a Client with the production base URL and a 10 s timeout.
func NewClient() *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		HTTP:    http.DefaultClient,
		Timeout: 10 * time.Second,
	}
}

// FetchUser queries fxtwitter for the given handle.
func (c *Client) FetchUser(ctx context.Context, handle string) (*User, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	url := c.BaseURL + "/" + strings.TrimPrefix(handle, "@")
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fxtwitter request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fxtwitter status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, ErrNotFound
	}

	var raw struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		User    *struct {
			ID           string `json:"id"`
			ScreenName   string `json:"screen_name"`
			Name         string `json:"name"`
			Description  string `json:"description"`
			Location     string `json:"location"`
			Website      any    `json:"website"`
			AvatarURL    string `json:"avatar_url"`
			BannerURL    string `json:"banner_url"`
			Followers    int    `json:"followers"`
			Following    int    `json:"following"`
			Tweets       int    `json:"tweets"`
			MediaCount   int    `json:"media_count"`
			Likes        int    `json:"likes"`
			Joined       string `json:"joined"`
			Protected    bool   `json:"protected"`
			Verification struct {
				Verified bool   `json:"verified"`
				Type     string `json:"type"`
			} `json:"verification"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if raw.User == nil {
		return nil, ErrNotFound
	}

	u := &User{
		ID:           raw.User.ID,
		ScreenName:   raw.User.ScreenName,
		Name:         raw.User.Name,
		Description:  raw.User.Description,
		Location:     raw.User.Location,
		AvatarURL:    raw.User.AvatarURL,
		BannerURL:    raw.User.BannerURL,
		Followers:    raw.User.Followers,
		Following:    raw.User.Following,
		Tweets:       raw.User.Tweets,
		MediaCount:   raw.User.MediaCount,
		Likes:        raw.User.Likes,
		Verified:     raw.User.Verification.Verified,
		VerifiedType: raw.User.Verification.Type,
		Protected:    raw.User.Protected,
	}
	if s, ok := raw.User.Website.(string); ok {
		u.Website = s
	}
	if t, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", raw.User.Joined); err == nil {
		u.Joined = t.UTC()
	}
	return u, nil
}

// FetchTweet queries fxtwitter for a single tweet by handle + ID. Returns
// ErrNotFound on 404 / empty body. The handle is required by the fxtwitter
// URL shape but does not need to exactly match the tweet's author — fxtwitter
// resolves the canonical author from the ID.
func (c *Client) FetchTweet(ctx context.Context, handle, tweetID string) (*Tweet, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	url := c.BaseURL + "/" + strings.TrimPrefix(handle, "@") + "/status/" + tweetID
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fxtwitter request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fxtwitter status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, ErrNotFound
	}

	var raw struct {
		Code    int       `json:"code"`
		Message string    `json:"message"`
		Tweet   *rawTweet `json:"tweet"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if raw.Tweet == nil {
		return nil, ErrNotFound
	}

	return tweetFromRaw(raw.Tweet), nil
}

type rawTweet struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Lang   string `json:"lang"`
	Author struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
		AvatarURL  string `json:"avatar_url"`
	} `json:"author"`
	ReplyingTo       string    `json:"replying_to"`
	ReplyingToStatus string    `json:"replying_to_status"`
	CreatedAt        string    `json:"created_at"`
	Media            *rawMedia `json:"media"`
	Quote            *rawTweet `json:"quote"`
}

type rawMedia struct {
	All []struct {
		Type   string `json:"type"`
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"all"`
}

func tweetFromRaw(raw *rawTweet) *Tweet {
	if raw == nil {
		return nil
	}
	out := &Tweet{
		ID:                raw.ID,
		AuthorHandle:      raw.Author.ScreenName,
		AuthorDisplayName: raw.Author.Name,
		AuthorAvatarURL:   raw.Author.AvatarURL,
		Text:              raw.Text,
		Lang:              raw.Lang,
		ReplyToHandle:     raw.ReplyingTo,
		ReplyToStatus:     raw.ReplyingToStatus,
	}
	if t, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", raw.CreatedAt); err == nil {
		out.CreatedAt = t.UTC()
	}

	// Map media.all[] into the same JSON shape feed_items.media_json uses.
	if raw.Media != nil && len(raw.Media.All) > 0 {
		type mediaRef struct {
			URL    string `json:"url"`
			Type   string `json:"type"`
			Width  int    `json:"width,omitempty"`
			Height int    `json:"height,omitempty"`
		}
		refs := make([]mediaRef, 0, len(raw.Media.All))
		for _, m := range raw.Media.All {
			if m.URL == "" {
				continue
			}
			t := m.Type
			if t == "gif" {
				t = "video"
			}
			refs = append(refs, mediaRef{URL: m.URL, Type: t, Width: m.Width, Height: m.Height})
		}
		if len(refs) > 0 {
			b, _ := json.Marshal(refs)
			out.MediaJSON = string(b)
		}
	}
	out.Quote = tweetFromRaw(raw.Quote)

	return out
}

// UpgradeBannerURL appends the 1500x500 size suffix that twimg banner URLs
// accept. Empty in → empty out so callers can call unconditionally.
func UpgradeBannerURL(u string) string {
	if u == "" {
		return ""
	}
	return u + "/1500x500"
}
