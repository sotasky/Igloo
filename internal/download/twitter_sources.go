package download

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const galleryDLURLKey = "__gallery_dl_url"

var twitterHandleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)
var tweetIDRe = regexp.MustCompile(`^[0-9]+$`)

// TwitterSource fetches X list/community metadata through gallery-dl without
// downloading media.
func (g *GalleryDLWrapper) TwitterSource(ctx context.Context, rawURL string, limit int, cookiesFile string) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 100
	}
	args := []string{
		"--dump-json",
		"--simulate",
		"--range", "1-" + strconv.Itoa(limit),
	}
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	}
	args = append(args, rawURL)
	cmd := exec.CommandContext(ctx, "gallery-dl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gallery-dl X source: %w: %s", err, output)
	}
	return ParseTwitterSourceDump(output, rawURL), nil
}

// ParseTwitterSourceDump converts gallery-dl's twitter list/community JSON
// objects into feed items. The discovery source is not stored in source_handle;
// source attribution lives in feed_item_sources.
func ParseTwitterSourceDump(output []byte, _ string) []model.FeedItem {
	records := map[string]*twitterSourceRecord{}
	var order []string
	for _, payload := range galleryDLJSONPayloads(output) {
		for _, obj := range twitterSourceObjects(payload) {
			item := twitterFeedItemFromGalleryDLObject(obj)
			if item.TweetID == "" || item.AuthorHandle == "" {
				continue
			}
			record := records[item.TweetID]
			if record == nil {
				record = &twitterSourceRecord{item: item}
				records[item.TweetID] = record
				order = append(order, item.TweetID)
			} else {
				record.item = mergeTwitterSourceItem(record.item, item)
			}
			record.media = append(record.media, twitterMediaRefsFromGalleryDLObject(obj)...)
		}
	}
	items := make([]model.FeedItem, 0, len(order))
	for _, tweetID := range order {
		record := records[tweetID]
		if len(record.media) > 0 {
			record.item.MediaJSON = twitterMediaRefsJSON(record.media)
			record.item.Media = nil
			record.item.ParseMedia()
		}
		items = append(items, record.item)
	}
	return items
}

type twitterSourceRecord struct {
	item  model.FeedItem
	media []model.MediaRef
}

func twitterSourceObjects(value any) []map[string]any {
	switch v := value.(type) {
	case []any:
		if len(v) >= 3 {
			rawURL, _ := v[1].(string)
			if obj, ok := v[2].(map[string]any); ok {
				cp := cloneStringAnyMap(obj)
				if rawURL != "" && stringFromAny(cp[galleryDLURLKey]) == "" && isTrustedTwitterMediaURL(rawURL) {
					cp[galleryDLURLKey] = rawURL
				}
				return []map[string]any{cp}
			}
		}
		var out []map[string]any
		for _, item := range v {
			out = append(out, twitterSourceObjects(item)...)
		}
		return out
	case map[string]any:
		return []map[string]any{v}
	default:
		return nil
	}
}

func isTrustedTwitterMediaURL(rawURL string) bool {
	host, _, ok := httpURLParts(rawURL)
	if !ok {
		return false
	}
	return host == "pbs.twimg.com" || host == "video.twimg.com"
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeTwitterSourceItem(base, next model.FeedItem) model.FeedItem {
	if base.BodyText == "" {
		base.BodyText = next.BodyText
	}
	if base.CanonicalURL == "" {
		base.CanonicalURL = next.CanonicalURL
	}
	if base.AuthorDisplayName == "" {
		base.AuthorDisplayName = next.AuthorDisplayName
	}
	if base.AuthorAvatarURL == "" {
		base.AuthorAvatarURL = next.AuthorAvatarURL
	}
	if base.PublishedAt == nil {
		base.PublishedAt = next.PublishedAt
	}
	if base.Views == 0 {
		base.Views = next.Views
	}
	if base.Likes == 0 {
		base.Likes = next.Likes
	}
	if base.Retweets == 0 {
		base.Retweets = next.Retweets
	}
	return base
}

func twitterFeedItemFromGalleryDLObject(obj map[string]any) model.FeedItem {
	author := twitterAuthorMap(obj)
	handle := firstString(author, "nick", "screen_name", "username", "name")
	if handle == "" {
		handle = firstString(obj, "author_nick", "author_screen_name", "username", "uploader_id")
	}
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if !twitterHandleRe.MatchString(handle) {
		handle = ""
	}

	tweetID := firstString(obj, "tweet_id", "id", "rest_id")
	canonicalURL := firstString(obj, "url", "tweet_url", "webpage_url", "permalink")
	if tweetID == "" {
		tweetID = tweetIDFromURL(canonicalURL)
	}
	if !tweetIDRe.MatchString(tweetID) {
		tweetID = ""
	}
	if canonicalURL == "" && handle != "" && tweetID != "" {
		canonicalURL = "https://x.com/" + handle + "/status/" + tweetID
	}

	publishedAt := firstTime(obj, "date", "created_at", "timestamp")
	mediaJSON := twitterMediaJSON(obj)
	body := firstString(obj, "content", "text", "description", "full_text")
	now := time.Now()

	item := model.FeedItem{
		TweetID:           tweetID,
		SourceHandle:      handle,
		AuthorHandle:      handle,
		AuthorDisplayName: firstString(author, "name", "display_name"),
		AuthorAvatarURL:   firstString(author, "profile_image", "profile_image_url", "avatar_url"),
		BodyText:          body,
		MediaJSON:         mediaJSON,
		CanonicalURL:      canonicalURL,
		Views:             firstInt64(obj, "view_count", "views"),
		Likes:             firstInt64(obj, "favorite_count", "like_count", "likes"),
		Retweets:          firstInt64(obj, "retweet_count", "retweets"),
		PublishedAt:       publishedAt,
		FetchedAt:         now,
	}
	item.ParseMedia()
	return item
}

func twitterMediaRefsFromGalleryDLObject(obj map[string]any) []model.MediaRef {
	var media []model.MediaRef
	if raw, ok := obj["media"]; ok {
		if entries, ok := raw.([]any); ok {
			for _, entry := range entries {
				if m, ok := entry.(map[string]any); ok {
					if ref, ok := twitterMediaRefFromMap(m, true); ok {
						media = append(media, ref)
					}
				}
			}
		}
	}
	if ref, ok := twitterMediaRefFromMap(obj, false); ok {
		media = append(media, ref)
	}
	return dedupeTwitterMediaRefs(media)
}

func twitterMediaRefFromMap(m map[string]any, allowPlainURL bool) (model.MediaRef, bool) {
	rawURL := firstTopLevelString(m, galleryDLURLKey, "media_url", "download_url")
	if rawURL == "" && allowPlainURL {
		rawURL = firstTopLevelString(m, "url")
	}
	ref := model.MediaRef{
		URL:          rawURL,
		Type:         firstTopLevelString(m, "type", "media_type"),
		ThumbnailURL: firstTopLevelString(m, "thumbnail", "thumbnail_url", "preview_image_url"),
		AltText:      firstTopLevelString(m, "alt", "alt_text"),
	}
	if ref.Type == "" {
		ref.Type = "photo"
	}
	ref.Width = int(firstInt64(m, "width", "w"))
	ref.Height = int(firstInt64(m, "height", "h"))
	if ref.URL == "" && ref.ThumbnailURL == "" {
		return model.MediaRef{}, false
	}
	return ref, true
}

func firstTopLevelString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			if s := stringFromAny(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func twitterAuthorMap(obj map[string]any) map[string]any {
	for _, key := range []string{"author", "user"} {
		if nested, ok := obj[key].(map[string]any); ok {
			return nested
		}
	}
	return map[string]any{}
}

func firstTime(obj map[string]any, keys ...string) *time.Time {
	for _, key := range keys {
		v, ok := obj[key]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case time.Time:
			return &t
		case json.Number:
			if parsed := unixTimeFromString(t.String()); parsed != nil {
				return parsed
			}
		case float64:
			if t > 0 {
				tt := time.Unix(int64(t), 0)
				return &tt
			}
		case string:
			if parsed := parseTwitterTime(t); parsed != nil {
				return parsed
			}
		}
	}
	return nil
}

func parseTwitterTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if parsed := unixTimeFromString(raw); parsed != nil {
		return parsed
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"Mon Jan 02 15:04:05 -0700 2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}
	return nil
}

func unixTimeFromString(raw string) *time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	if n > 100000000000 {
		n /= 1000
	}
	t := time.Unix(n, 0)
	return &t
}

func twitterMediaJSON(obj map[string]any) string {
	media := twitterMediaRefsFromGalleryDLObject(obj)
	if len(media) == 0 {
		return ""
	}
	return twitterMediaRefsJSON(media)
}

func twitterMediaRefsJSON(media []model.MediaRef) string {
	data, _ := json.Marshal(dedupeTwitterMediaRefs(media))
	return string(data)
}

func dedupeTwitterMediaRefs(media []model.MediaRef) []model.MediaRef {
	seen := map[string]struct{}{}
	out := media[:0]
	for _, ref := range media {
		key := ref.URL
		if key == "" {
			key = ref.ThumbnailURL
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func firstInt64(item map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			switch n := v.(type) {
			case json.Number:
				if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
					return i
				}
			case float64:
				return int64(n)
			case int:
				return int64(n)
			case int64:
				return n
			case string:
				if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
					return i
				}
			}
		}
	}
	return 0
}

func tweetIDFromURL(raw string) string {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	for i, part := range parts {
		if part == "status" && i+1 < len(parts) {
			id := strings.Split(parts[i+1], "?")[0]
			if id != "" {
				return id
			}
		}
	}
	return ""
}
