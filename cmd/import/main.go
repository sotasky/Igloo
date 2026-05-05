// cmd/import reads a Igloo export ZIP and populates the server database.
// Usage: go run ./cmd/import igloo-export.zip
package main

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/screwys/igloo/internal/db"
	_ "modernc.org/sqlite"
)

type ExportPayload struct {
	Channels        []Channel       `json:"channels"`
	LikedPosts      []FeedPost      `json:"liked_posts"`
	BookmarkedPosts []FeedPost      `json:"bookmarked_posts"`
	BookmarkedVids  []BookmarkedVid `json:"bookmarked_videos"`
}

type Channel struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	URL       string `json:"url"`
	IsStarred bool   `json:"is_starred"`
	// Legacy export fields (avatar_url, profile_picture) still appear in
	// old JSON but are ignored — avatars resolve via media_files on the
	// next worker pass, so we don't need to preserve them on import.
	//
	// Accept both the new generic names and the legacy x_* names from
	// exports produced before the channel_settings side-table migration.
	MediaOnly           *bool `json:"media_only"`
	IncludeReposts      *bool `json:"include_reposts"`
	XMediaOnlyOld       *bool `json:"x_media_only"`
	XIncludeRetweetsOld *bool `json:"x_include_retweets"`
}

type FeedPost struct {
	TweetID                string          `json:"tweet_id"`
	SourceHandle           string          `json:"source_handle"`
	AuthorHandle           string          `json:"author_handle"`
	AuthorDisplayName      string          `json:"author_display_name"`
	AvatarURL              string          `json:"avatar_url"`
	BodyText               string          `json:"body_text"`
	Link                   string          `json:"link"`
	CanonicalXLink         string          `json:"canonical_x_link"`
	PublishedAt            string          `json:"published_at"`
	MediaKind              string          `json:"media_kind"`
	MediaURL               string          `json:"media_url"`
	MediaPreviewURL        string          `json:"media_preview_url"`
	MediaStreamURL         string          `json:"media_stream_url"`
	MediaSlideURLs         []string        `json:"media_slide_urls"`
	MediaTypes             []string        `json:"media_types"`
	HasMedia               bool            `json:"has_media"`
	IsLiked                bool            `json:"is_liked"`
	IsBookmarked           bool            `json:"is_bookmarked"`
	BookmarkCategoryID     *int            `json:"bookmark_category_id"`
	BookmarkedAt           string          `json:"bookmarked_at"`
	Platform               string          `json:"platform"`
	IsRetweet              bool            `json:"is_retweet"`
	RepostedBy             string          `json:"reposted_by"`
	RepostedByDisplayName  string          `json:"reposted_by_display_name"`
	QuoteTweetID           *string         `json:"quote_tweet_id"`
	QuoteAuthorHandle      *string         `json:"quote_author_handle"`
	QuoteAuthorDisplayName *string         `json:"quote_author_display_name"`
	QuoteAuthorAvatarURL   *string         `json:"quote_author_avatar_url"`
	QuoteBodyText          *string         `json:"quote_body_text"`
	QuoteMedia             json.RawMessage `json:"quote_media"`
	QuotePublishedAt       *string         `json:"quote_published_at"`
	ChannelID              string          `json:"channel_id"`
}

type BookmarkedVid struct {
	VideoID            string  `json:"video_id"`
	Title              string  `json:"title"`
	ChannelID          string  `json:"channel_id"`
	ChannelName        string  `json:"channel_name"`
	Platform           string  `json:"platform"`
	Duration           float64 `json:"duration"`
	ThumbnailURL       string  `json:"thumbnail_url"`
	PublishedAt        string  `json:"published_at"`
	Description        string  `json:"description"`
	BookmarkCategoryID *int    `json:"bookmark_category_id"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: import <igloo-export.zip>")
		os.Exit(1)
	}
	zipPath := os.Args[1]

	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".local", "share", "igloo")
	dbPath := filepath.Join(dataDir, "igloo.db")

	// Read export ZIP
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		fatal("open zip: %v", err)
	}
	defer r.Close()

	var payload ExportPayload
	for _, f := range r.File {
		if f.Name == "data.json" {
			rc, err := f.Open()
			if err != nil {
				fatal("open data.json: %v", err)
			}
			if err := json.NewDecoder(rc).Decode(&payload); err != nil {
				fatal("decode data.json: %v", err)
			}
			rc.Close()
			break
		}
	}

	fmt.Printf("Export data: %d channels, %d liked, %d bookmarked posts, %d bookmarked videos\n",
		len(payload.Channels), len(payload.LikedPosts), len(payload.BookmarkedPosts), len(payload.BookmarkedVids))

	// Open DB (creates all tables via EnsureSchema)
	d, err := db.Open(dbPath, dataDir)
	if err != nil {
		fatal("open db: %v", err)
	}
	defer d.Close()

	// Import channels
	var chAdded, chSkipped int
	for _, ch := range payload.Channels {
		sourceID := extractSourceID(ch.ChannelID, ch.Platform)
		url := ch.URL
		if url == "" {
			url = buildURL(ch.ChannelID, ch.Platform, sourceID)
		}

		err := d.ExecRaw(`
			INSERT OR IGNORE INTO channels
				(channel_id, source_id, name, url, platform, is_subscribed, is_starred)
			VALUES (?, ?, ?, ?, ?, 1, ?)
		`,
			ch.ChannelID, sourceID, ch.Name, url, ch.Platform,
			boolToInt(ch.IsStarred),
		)
		if err == nil {
			// Per-channel settings land in the side table. Accept both generic
			// and legacy x_* keys from older exports; prefer the generic one.
			mediaOnly := ch.MediaOnly
			if mediaOnly == nil {
				mediaOnly = ch.XMediaOnlyOld
			}
			includeReposts := ch.IncludeReposts
			if includeReposts == nil {
				includeReposts = ch.XIncludeRetweetsOld
			}
			if mediaOnly != nil || includeReposts != nil {
				_ = d.ExecRaw(`
					INSERT OR IGNORE INTO channel_settings
						(channel_id, media_only, include_reposts, updated_at)
					VALUES (?, ?, ?, 0)
				`,
					ch.ChannelID,
					nullBool(mediaOnly), nullBool(includeReposts),
				)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  channel %s: %v\n", ch.ChannelID, err)
			chSkipped++
		} else {
			chAdded++
		}
	}
	fmt.Printf("Channels: %d added, %d skipped\n", chAdded, chSkipped)

	// Import liked posts → feed_likes + feed_items
	username := "admin" // default user
	var likeAdded int
	for _, p := range payload.LikedPosts {
		if p.TweetID == "" {
			continue
		}
		upsertFeedItem(d, p)
		mediaJSON := buildMediaJSON(p)
		quoteJSON := buildQuoteJSON(p)
		err := d.ExecRaw(`
			INSERT OR IGNORE INTO feed_likes
				(username, tweet_id, source_handle, author_handle, author_display_name,
				 body_text, link, canonical_x_link, published_at, media_url, avatar_url,
				 media_json, platform, quote_payload_json, liked_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		`,
			username, p.TweetID, p.SourceHandle, p.AuthorHandle, p.AuthorDisplayName,
			p.BodyText, p.Link, p.CanonicalXLink, p.PublishedAt,
			p.MediaURL, p.AvatarURL, mediaJSON, p.Platform, quoteJSON,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  like %s: %v\n", p.TweetID, err)
		} else {
			likeAdded++
		}
	}
	fmt.Printf("Liked posts: %d added\n", likeAdded)

	// Import bookmarked posts → bookmarks (as tweet_id-based) + feed_items
	var bmAdded int
	for _, p := range payload.BookmarkedPosts {
		if p.TweetID == "" {
			continue
		}
		upsertFeedItem(d, p)
		catID := 0
		if p.BookmarkCategoryID != nil {
			catID = *p.BookmarkCategoryID
		}
		err := d.ExecRaw(`
			INSERT OR IGNORE INTO bookmarks
				(user_id, video_id, category_id, bookmarked_at)
			VALUES (?, ?, ?, COALESCE(NULLIF(?,''), datetime('now')))
		`, username, p.TweetID, catID, p.BookmarkedAt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  bookmark %s: %v\n", p.TweetID, err)
		} else {
			bmAdded++
		}
	}
	fmt.Printf("Bookmarked posts: %d added\n", bmAdded)

	// Import bookmarked videos → videos + bookmarks
	var vidAdded int
	for _, v := range payload.BookmarkedVids {
		if v.VideoID == "" {
			continue
		}
		d.ExecRaw(`
			INSERT OR IGNORE INTO videos
				(video_id, channel_id, title, description, duration, thumbnail_path, published_at, file_path)
			VALUES (?, ?, ?, ?, ?, ?, ?, '')
		`, v.VideoID, v.ChannelID, v.Title, v.Description, int(v.Duration), v.ThumbnailURL, v.PublishedAt)

		catID := 0
		if v.BookmarkCategoryID != nil {
			catID = *v.BookmarkCategoryID
		}
		err := d.ExecRaw(`
			INSERT OR IGNORE INTO bookmarks
				(user_id, video_id, category_id, bookmarked_at)
			VALUES (?, ?, ?, datetime('now'))
		`, username, v.VideoID, catID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  video bookmark %s: %v\n", v.VideoID, err)
		} else {
			vidAdded++
		}
	}
	fmt.Printf("Bookmarked videos: %d added\n", vidAdded)

	fmt.Println("\nImport complete.")
}

func upsertFeedItem(d *db.DB, p FeedPost) {
	mediaJSON := buildMediaJSON(p)
	quoteMediaJSON := ""
	if p.QuoteMedia != nil && string(p.QuoteMedia) != "null" {
		quoteMediaJSON = string(p.QuoteMedia)
	}

	err := d.ExecRaw(`
		INSERT OR IGNORE INTO feed_items
			(tweet_id, source_handle, author_handle, author_display_name,
			 author_avatar_url, body_text, is_retweet,
			 retweeted_by_handle, retweeted_by_display_name,
			 quote_tweet_id, quote_author_handle, quote_author_display_name,
			 quote_author_avatar_url, quote_body_text, quote_media_json,
			 media_json, canonical_url, quote_published_at,
			 published_at, fetched_at)
		VALUES (?, ?, ?, ?,
			?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, datetime('now'))
	`,
		p.TweetID, p.SourceHandle, p.AuthorHandle, p.AuthorDisplayName,
		p.AvatarURL, p.BodyText, boolToInt(p.IsRetweet),
		p.RepostedBy, p.RepostedByDisplayName,
		nilStr(p.QuoteTweetID), nilStr(p.QuoteAuthorHandle), nilStr(p.QuoteAuthorDisplayName),
		nilStr(p.QuoteAuthorAvatarURL), nilStr(p.QuoteBodyText), nilIfEmpty(quoteMediaJSON),
		nilIfEmpty(mediaJSON), p.CanonicalXLink, nilStr(p.QuotePublishedAt),
		p.PublishedAt,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  feed_item %s: %v\n", p.TweetID, err)
	}
}

func buildMediaJSON(p FeedPost) string {
	if !p.HasMedia {
		return ""
	}
	// Build a media_json array from slide URLs or single media URL
	if len(p.MediaSlideURLs) > 0 {
		items := make([]map[string]string, len(p.MediaSlideURLs))
		for i, u := range p.MediaSlideURLs {
			typ := "photo"
			if i < len(p.MediaTypes) {
				typ = p.MediaTypes[i]
			}
			items[i] = map[string]string{"url": u, "type": typ}
		}
		b, _ := json.Marshal(items)
		return string(b)
	}
	if p.MediaURL != "" {
		b, _ := json.Marshal([]map[string]string{{"url": p.MediaURL, "type": p.MediaKind}})
		return string(b)
	}
	return ""
}

func buildQuoteJSON(p FeedPost) string {
	if p.QuoteTweetID == nil || *p.QuoteTweetID == "" {
		return ""
	}
	obj := map[string]any{
		"tweet_id":            *p.QuoteTweetID,
		"author_handle":       deref(p.QuoteAuthorHandle),
		"author_display_name": deref(p.QuoteAuthorDisplayName),
		"avatar_url":          deref(p.QuoteAuthorAvatarURL),
		"body_text":           deref(p.QuoteBodyText),
		"published_at":        deref(p.QuotePublishedAt),
	}
	if p.QuoteMedia != nil && string(p.QuoteMedia) != "null" {
		obj["media"] = json.RawMessage(p.QuoteMedia)
	}
	b, _ := json.Marshal(obj)
	return string(b)
}

func extractSourceID(channelID, platform string) string {
	switch platform {
	case "twitter":
		return strings.TrimPrefix(channelID, "twitter_")
	case "tiktok":
		return strings.TrimPrefix(channelID, "tiktok_")
	case "youtube":
		return strings.TrimPrefix(channelID, "youtube_")
	}
	return channelID
}

func buildURL(channelID, platform, sourceID string) string {
	switch platform {
	case "twitter":
		return "https://x.com/" + sourceID
	case "tiktok":
		return "https://www.tiktok.com/@" + sourceID
	case "youtube":
		return "https://www.youtube.com/channel/" + sourceID
	}
	return ""
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullBool(b *bool) any {
	if b == nil {
		return sql.NullInt64{}
	}
	return boolToInt(*b)
}

func nilStr(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
