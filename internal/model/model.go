package model

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Channel represents a subscribed channel across all platforms.
type Channel struct {
	ID            int64
	ChannelID     string
	SourceID      string
	Name          string
	URL           string
	Platform      string
	IsSubscribed  bool
	IsStarred     bool
	Quality       string
	CheckInterval *int
	LastChecked   *time.Time
	CreatedAt     time.Time
	// Handle is the platform @-style identifier joined from `channel_profiles`.
	// Twitter/TikTok populate this for ~100% of rows; YouTube ~95%. When
	// missing it's "" and the sidebar handle filter simply won't match on
	// this channel's handle (name filter still works). Used for sidebar
	// search so Latin-typed queries can find Japanese/unicode display names.
	Handle string
	// DisplayName is the pretty, human-facing name from `channel_profiles`.
	// For Twitter, 82% of rows have `channels.name == handle` (the handle
	// was stored there at ingest); the real display name is here instead
	// (e.g. "Example Display Name" for @sample_handle_ja). Use
	// ChannelDisplayName() to pick this when non-empty, Name otherwise.
	DisplayName string
	// Per-channel settings live in the `channel_settings` side table;
	// IncludeReposts is surfaced here because the feed enrich path joins
	// it in for retweeter visibility checks. NULL = inherit from global.
	IncludeReposts *bool
	// Computed fields (populated by queries, not stored). AvatarURL is the
	// /api/media/avatar/<channel_id> proxy URL; the cached file lives in the
	// conventional thumbnails/avatars/ disk cache.
	VideoCount int
	AvatarURL  string
	// SyncSeq advances when AddChannel writes the row (#6 bundle delta).
	SyncSeq int64
}

// Pager holds pagination state.
type Pager struct {
	Page    int
	PerPage int
	Total   int
}

func (p Pager) Pages() int {
	if p.Total <= 0 {
		return 1
	}
	return (p.Total + p.PerPage - 1) / p.PerPage
}

func (p Pager) HasPrev() bool { return p.Page > 1 }
func (p Pager) HasNext() bool { return p.Page < p.Pages() }
func (p Pager) Offset() int   { return max(0, (p.Page-1)*p.PerPage) }

// DBStats holds aggregate statistics for the sidebar.
type DBStats struct {
	TotalChannels  int
	TotalVideos    int
	TotalFeedItems int
	DatabaseSizeMB float64
}

// ChannelGroup holds a named group of channels for sidebar display.
type ChannelGroup struct {
	Title       string
	GroupID     string
	PlatformKey string
	StarIcon    bool
	Collapsed   bool
	Count       int
	Channels    []Channel
}

// SidebarContext holds all data needed to render the sidebar.
type SidebarContext struct {
	Username           string
	Channels           []Channel
	Groups             []ChannelGroup
	Stats              DBStats
	CurrentlyWatching  []Video
	CurrentlyAvailable []Video
	PinnedVideos       []Video
}

// FeedSource is a non-account source that can introduce feed items, such as an
// X list or community. Feed items keep their real author/source handles; this
// model records where the item was discovered.
type FeedSource struct {
	SourceID    string     `json:"source_id"`
	Platform    string     `json:"platform"`
	SourceType  string     `json:"source_type"`
	ExternalID  string     `json:"external_id"`
	Label       string     `json:"label"`
	URL         string     `json:"url"`
	Enabled     bool       `json:"enabled"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	LastOK      *time.Time `json:"last_ok,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ItemCount   int        `json:"item_count,omitempty"`
	UnseenCount int        `json:"unseen_count,omitempty"`
}

// Flash holds a flash message with category.
type Flash struct {
	Category string
	Message  string
}

// Video represents a downloaded video/media file.
type Video struct {
	ID            int64
	VideoID       string
	ChannelID     string
	Title         string
	Description   string
	Duration      int // seconds
	ThumbnailPath string
	FilePath      string
	FileSize      int64
	PublishedAt   *time.Time
	DownloadedAt  time.Time
	Watched       bool
	IsTemp        bool
	IsPinned      bool
	MetadataJSON  string
	// Joined/computed fields
	ChannelName        string
	Platform           string
	IsStarred          bool
	IsSubscribed       bool
	BookmarkCategoryID *int64
	AvatarURL          string
	ThumbnailURL       string
	IsShortForm        bool
	MediaKind          string // video|image|slideshow
	MediaSlideCount    int
	SourceKind         string // ""|story
	PlaybackPosition   float64
	EagerLoad          bool // skip loading="lazy" for above-the-fold images
	Metadata           *VideoMetadata
	// SyncSeq advances on InsertVideo (#6 bundle delta).
	SyncSeq int64
	// Repost fields are joined for Moments. A repost-introduced video still
	// appears once; RepostCount tells the UI how many followed sources surfaced it.
	ReposterChannelID   string
	ReposterHandle      string
	ReposterDisplayName string
	RepostCount         int
	RepostIntroduced    bool
	EffectiveMomentAtMs int64
	StoryState          string
	StoryCount          int
	StoryUnseenCount    int
	StoryFirstVideoID   string
	StoryUnseen         bool
	// DeArrow fields — nullable pointers distinguish "unchecked" from "checked, no data".
	DearrowTitle       *string
	DearrowTitleCasual *string
	DearrowThumbPath   *string
	DearrowCheckedAtMs *int64
}

// VideoRepostSource records a followed TikTok account that introduced a TikTok
// post through reposts. It is separate from videos so repost metadata can
// aggregate while the original post remains a single durable row.
type VideoRepostSource struct {
	VideoID             string `json:"video_id"`
	ReposterChannelID   string `json:"reposter_channel_id"`
	ReposterHandle      string `json:"reposter_handle"`
	ReposterDisplayName string `json:"reposter_display_name,omitempty"`
	RepostAuthorLabel   string `json:"repost_author_label,omitempty"`
	RepostedAtMs        int64  `json:"reposted_at_ms"`
	FirstSeenAtMs       int64  `json:"first_seen_at_ms"`
	UpdatedAtMs         int64  `json:"updated_at_ms"`
}

func RepostAuthorLabel(displayName, handle string) string {
	if name := strings.TrimSpace(displayName); name != "" {
		return name
	}
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return ""
	}
	return "@" + strings.TrimPrefix(handle, "@")
}

const (
	StoryStateNone = "none"
	StoryStateSeen = "seen"
	StoryStateNew  = "new"
)

// StoryStatus is the channel-level ring state derived from active own Moments.
type StoryStatus struct {
	ChannelID          string
	State              string
	Count              int
	UnseenCount        int
	LatestAtMs         int64
	FirstVideoID       string
	FirstUnseenVideoID string
}

// StoryChannel is the Stories tab row for a followed TikTok/Instagram channel.
type StoryChannel struct {
	ChannelID          string
	Platform           string
	DisplayName        string
	Handle             string
	AvatarURL          string
	Count              int
	UnseenCount        int
	LatestAtMs         int64
	FirstVideoID       string
	FirstUnseenVideoID string
	State              string
}

// VideoMetadata holds parsed yt-dlp metadata fields.
type VideoMetadata struct {
	ViewCount      int64              `json:"view_count"`
	ViewCountLabel string             `json:"view_count_label"`
	LikeCount      int64              `json:"like_count"`
	LikeCountLabel string             `json:"like_count_label"`
	Duration       int                `json:"duration"`
	Width          int                `json:"width"`
	Height         int                `json:"height"`
	WebpageURL     string             `json:"webpage_url"`
	UploadDate     string             `json:"upload_date"`
	Slides         []json.RawMessage  `json:"slides"`
	VCodec         string             `json:"vcodec"`
	Coauthors      []InstagramAccount `json:"coauthors"`
	TaggedUsers    []InstagramAccount `json:"tagged_users"`
}

// InstagramAccount is the compact account shape gallery-dl emits for
// collaborator and tagged accounts on Instagram posts.
type InstagramAccount struct {
	Username      string `json:"username"`
	FullName      string `json:"full_name"`
	ProfilePicURL string `json:"profile_pic_url"`
}

// SlideAsMap parses a single slide element into a map.
func (m *VideoMetadata) SlideAsMap(index int) map[string]any {
	if index < 0 || index >= len(m.Slides) {
		return nil
	}
	var s map[string]any
	if err := json.Unmarshal(m.Slides[index], &s); err != nil {
		return nil
	}
	return s
}

// SlidePath returns the "path" field from a slide, or the raw string value.
func (m *VideoMetadata) SlidePath(index int) string {
	if index < 0 || index >= len(m.Slides) {
		return ""
	}
	// Try as map with "path" key
	if s := m.SlideAsMap(index); s != nil {
		if p, ok := s["path"].(string); ok {
			return p
		}
	}
	// Try as bare string
	var s string
	if err := json.Unmarshal(m.Slides[index], &s); err == nil {
		return s
	}
	return ""
}

// Comment represents a video comment.
// JSON field names match Python's get_comments() output for JS compatibility.
// Relative time ("2h ago") is computed client-side from PublishedAt — no
// pre-formatted `time_text` string is carried.
type Comment struct {
	ID              int64      `json:"-"`
	VideoID         string     `json:"video_id"`
	CommentID       string     `json:"id"`
	ParentID        string     `json:"parent"`
	AuthorName      string     `json:"author"`
	AuthorID        string     `json:"author_id"`
	AuthorThumbnail string     `json:"author_thumbnail"`
	Text            string     `json:"text"`
	LikeCount       int        `json:"like_count"`
	PublishedAt     *time.Time `json:"-"`
	PublishedAtMs   int64      `json:"published_at"` // unix millis (0 when unknown)
	Platform        string     `json:"platform"`
	FetchedAt       time.Time  `json:"-"`
}

// SetPublishedAtMs populates the unix-millis field from PublishedAt.
func (c *Comment) SetPublishedAtMs() {
	if c.PublishedAt != nil {
		c.PublishedAtMs = c.PublishedAt.UnixMilli()
	} else {
		c.PublishedAtMs = 0
	}
}

// ParseMetadata parses the MetadataJSON field into a VideoMetadata struct.
func (v *Video) ParseMetadata() *VideoMetadata {
	if v.Metadata != nil {
		return v.Metadata
	}
	if v.MetadataJSON == "" {
		return nil
	}
	var m VideoMetadata
	if err := json.Unmarshal([]byte(v.MetadataJSON), &m); err != nil {
		return nil
	}
	v.Metadata = &m
	return &m
}

// EnrichForCard populates computed fields for template rendering.
func (v *Video) EnrichForCard() {
	v.ThumbnailURL = "/api/media/thumbnail/" + v.VideoID
	if v.ChannelID != "" {
		v.AvatarURL = "/api/media/avatar/" + v.ChannelID
	}

	if v.MediaKind != "" {
		// Pre-computed from DB — still need IsShortForm from metadata for player UI
		if m := v.ParseMetadata(); m != nil {
			if m.Duration > 0 && m.Duration <= 90 {
				v.IsShortForm = true
			}
			if m.Width > 0 && m.Height > 0 && float64(m.Height)/float64(m.Width) > 1.3 {
				v.IsShortForm = true
			}
		}
	} else {
		// Legacy path: compute media_kind from metadata
		m := v.ParseMetadata()
		if m != nil {
			if m.Duration > 0 && m.Duration <= 90 {
				v.IsShortForm = true
			}
			if m.Width > 0 && m.Height > 0 && float64(m.Height)/float64(m.Width) > 1.3 {
				v.IsShortForm = true
			}
			v.MediaKind, v.MediaSlideCount = ComputeMediaKind(m, v.FilePath)
		}
		if v.MediaKind == "" {
			v.MediaKind, v.MediaSlideCount = ComputeMediaKind(nil, v.FilePath)
		}
	}

	// Clean up placeholder titles like "X post 12345"
	if xPostPlaceholder.MatchString(v.Title) {
		if v.Description != "" {
			v.Title = v.Description
		} else {
			v.Title = ""
		}
	}
}

var xPostPlaceholder = regexp.MustCompile(`(?i)^x\s+post\s+'?\d+'?$`)

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

// StripVideoMetadata keeps only the fields used by VideoMetadata, dropping
// all other yt-dlp dump fields (formats, thumbnails, http_headers, etc.).
func StripVideoMetadata(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	keep := []string{
		"duration", "width", "height", "vcodec",
		"view_count", "view_count_label", "like_count", "like_count_label", "webpage_url", "upload_date", "slides",
		"coauthors", "tagged_users",
	}
	stripped := make(map[string]any, len(keep))
	for _, k := range keep {
		if v, ok := raw[k]; ok {
			stripped[k] = v
		}
	}
	if len(stripped) == 0 {
		return nil
	}
	return stripped
}

// ComputeMediaKind determines media_kind and slide_count from metadata and file path.
func ComputeMediaKind(meta *VideoMetadata, filePath string) (string, int) {
	if meta != nil {
		if len(meta.Slides) > 1 {
			return "slideshow", len(meta.Slides)
		}
		if meta.Duration == 0 && len(meta.Slides) == 1 {
			return "image", 1
		}
		if meta.Duration == 0 && len(meta.Slides) == 0 && isImagePath(filePath) {
			return "image", 1
		}
		if meta.VCodec == "none" && len(meta.Slides) == 0 {
			return "slideshow", 0
		}
	}
	if isImagePath(filePath) {
		return "image", 1
	}
	return "video", 0
}

// MediaMode is the canonical display mode for Android media rendering.
func MediaMode(mediaKind string, slideCount int) string {
	kind := strings.ToLower(strings.TrimSpace(mediaKind))
	switch {
	case slideCount > 1 || kind == "slideshow":
		return "slideshow"
	case kind == "image" || kind == "photo":
		return "image"
	default:
		return "video"
	}
}

// FeedItem represents an X/Twitter post from RSSHub ingest.
type FeedItem struct {
	TweetID                string
	SourceHandle           string
	AuthorHandle           string
	AuthorDisplayName      string
	AuthorAvatarURL        string
	BodyText               string
	Lang                   string
	IsRetweet              bool
	RetweetedByHandle      string
	RetweetedByDisplayName string
	QuoteTweetID           string
	QuoteAuthorHandle      string
	QuoteAuthorDisplayName string
	QuoteAuthorAvatarURL   string
	QuoteBodyText          string
	QuoteLang              string
	QuoteMediaJSON         string
	MediaJSON              string
	CanonicalURL           string
	ReplyToHandle          string
	ReplyToStatus          string
	IsReply                bool
	IsGhost                bool
	QuotePublishedAt       *time.Time
	Views                  int64
	Likes                  int64
	Retweets               int64
	PublishedAt            *time.Time
	FetchedAt              time.Time
	ContentHash            string
	CanonicalTweetID       string
	SyncSeq                int64
	// Parsed at runtime
	Media      []MediaRef
	QuoteMedia []MediaRef
	// Enrichment fields (populated by feed.Enrich, not DB)
	IsLiked              bool
	IsSeen               bool
	IsBookmarked         bool
	QuoteIsLiked         bool
	QuoteIsBookmarked    bool
	ChannelID            string
	ChannelIsFollowed    bool
	ChannelIsStarred     bool
	FollowTargetFollowed bool // whether the follow-button target is already followed (not inherited)
	ReposterChannelID    string
	QuoteChannelID       string
	QuoteChannelFollowed bool
	// ThreadChain is the conversation chain for replies, ordered root → parent.
	// The leaf (this FeedItem itself) is NOT included. Empty for non-replies or
	// when chain resolution fails. Populated by feed.EnrichFeedItems for replies.
	ThreadChain []FeedItem
	// Media enrichment
	MediaKind           string // video|image|slideshow
	MediaSlideCount     int
	MediaStatus         string // ready|pending|failed|pruned|cdn
	MediaStreamURL      string
	MediaPreviewURL     string
	MediaSlideURLs      []string
	QuoteMediaStreamURL string
	QuoteCanonicalURL   string
	// Translation
	BodyTranslation  string
	BodySourceLang   string
	QuoteTranslation string
	QuoteSourceLang  string
	// Ranking
	AlgoInterestScore float64
	AlgoFlags         map[string]any
	// Retweet grouping
	Retweeters     []RetweeterInfo
	TaggedAccounts []RetweeterInfo
}

// RetweeterInfo represents a retweeter or tagged account.
type RetweeterInfo struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	ChannelID   string `json:"channel_id"`
	AvatarURL   string `json:"avatar_url"`
	IsTagged    bool   `json:"is_tagged"`
}

// MediaRef represents a single media item (photo/video/gif).
type MediaRef struct {
	URL          string `json:"url"`
	Type         string `json:"type"`
	ThumbnailURL string `json:"thumbnail_url"`
	AltText      string `json:"alt_text"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

// FeedLike represents a liked post with denormalized snapshot.
type FeedLike struct {
	Username          string
	TweetID           string
	LikedAt           time.Time
	SourceHandle      string
	AuthorHandle      string
	AuthorDisplayName string
	Link              string
	CanonicalXLink    string
	BodyText          string
	PublishedAt       *time.Time
	MediaURL          string
	AvatarURL         string
	MediaJSON         string
	Platform          string
	QuotePayloadJSON  string
}

// FeedMediaJob holds media download job status (minimal for enrichment).
type FeedMediaJob struct {
	TweetID    string
	Status     string // queued|processing|completed|failed|pruned
	MediaKind  string // video|image|unknown
	SlideCount int
}

// MediaFile is a single source of truth for all media paths on disk.
type MediaFile struct {
	OwnerType  string // feed_media|avatar|thumbnail|preview_sprite
	OwnerID    string // tweet_id, channel_id, or video_id
	MediaIndex int    // 0 for single files, 0-N for multi-image
	FilePath   string // relative to DataDir
	MediaType  string // photo|video|gif|avatar|thumbnail|preview_sprite
	SourceURL  string // original CDN URL
	FileSize   int64
	CreatedAt  time.Time
}

// IngestState tracks per-handle RSSHub fetch health.
type IngestState struct {
	Handle         string
	FailCount      int
	NextRetryAt    float64 // unix timestamp
	LastSuccessAt  float64
	LastAttemptAt  float64
	LastError      string
	LastHTTPStatus int
	AvgLatencyMs   float64
	UpdatedAt      time.Time
}

// ParseMedia parses MediaJSON into the Media slice.
func (f *FeedItem) ParseMedia() {
	if f.MediaJSON != "" && f.Media == nil {
		json.Unmarshal([]byte(f.MediaJSON), &f.Media)
	}
	if f.QuoteMediaJSON != "" && f.QuoteMedia == nil {
		json.Unmarshal([]byte(f.QuoteMediaJSON), &f.QuoteMedia)
	}
}

// ManifestEntry is a single asset identity from the #9 media manifest.
// Each entry tells Android where to download one binary (thumbnail,
// post media, avatar, video stream, subtitle, …). asset_id is stable
// across requests until the underlying asset is replaced.
type ManifestEntry struct {
	AssetID     string `json:"asset_id"`
	AssetKind   string `json:"asset_kind"`
	OwnerID     string `json:"owner_id"`
	OwnerKind   string `json:"owner_kind"`
	Scope       string `json:"scope"`
	ServerURL   string `json:"server_url"`
	Bucket      string `json:"bucket"`
	SizeHint    int64  `json:"size_hint,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	IsAuto      *bool  `json:"is_auto,omitempty"`
	// AudioLanguage is the BCP-47 audio language of the underlying video
	// (e.g. "en-US", "tr"). Only populated for subtitle entries. Clients
	// auto-enable subtitles when this is non-English.
	AudioLanguage      string `json:"audio_language,omitempty"`
	EffectiveRecencyMs int64  `json:"effective_recency_ms"`
	// ManifestSeq is the server-internal monotonic used for cursor
	// pagination — opaque to clients.
	ManifestSeq int64 `json:"-"`

	// ── legacy fields retained for the v1 media_files query helpers;
	// not emitted on the wire (json tag "-"). Remove with v1 cleanup.
	MediaType  string `json:"-"`
	MediaIndex int    `json:"-"`
}

// NewPosterAvatar represents one unique new poster surfaced in the
// feed "new posts" bar avatar stack.
type NewPosterAvatar struct {
	AuthorHandle      string
	AuthorDisplayName string
	AuthorAvatarURL   string
}

// FeedCursor holds cursor state for keyset pagination.
// BeforePublishedAtMs is unix milliseconds (0 means "no filter").
type FeedCursor struct {
	BeforePublishedAtMs int64
	BeforeTweetID       string
}

// ParseFeedCursor parses a cursor token "<millis>|<tweet_id>" into components.
// Falls back to parsing legacy "YYYY-MM-DD HH:MM:SS" style timestamps into
// millis so cursors emitted by older clients still resolve.
func ParseFeedCursor(token string) FeedCursor {
	if token == "" {
		return FeedCursor{}
	}
	parts := strings.SplitN(token, "|", 2)
	first := parts[0]
	var tweetID string
	if len(parts) == 2 {
		tweetID = parts[1]
	}
	if n, err := strconv.ParseInt(first, 10, 64); err == nil {
		return FeedCursor{BeforePublishedAtMs: n, BeforeTweetID: tweetID}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, first); err == nil {
			return FeedCursor{BeforePublishedAtMs: t.UnixMilli(), BeforeTweetID: tweetID}
		}
	}
	return FeedCursor{BeforeTweetID: tweetID}
}
