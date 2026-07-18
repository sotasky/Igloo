package xfeed

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/language"
	"github.com/screwys/igloo/internal/model"
	"github.com/taruti/langdetect"
)

type FeedItem = model.FeedItem

type StatusRef struct {
	Handle  string
	TweetID string
}

type ParseResult struct {
	Items               []FeedItem
	MissingQuoteParents []StatusRef
}

func (r *ParseResult) Merge(next ParseResult) {
	if r == nil || len(next.Items) == 0 {
		return
	}
	existing := make(map[string]int, len(r.Items))
	for i := range r.Items {
		existing[r.Items[i].TweetID] = i
	}
	for _, item := range next.Items {
		if item.TweetID == "" {
			continue
		}
		if idx, ok := existing[item.TweetID]; ok {
			r.Items[idx] = mergeItem(r.Items[idx], item)
			continue
		}
		existing[item.TweetID] = len(r.Items)
		r.Items = append(r.Items, item)
	}
}

func (r ParseResult) Find(tweetID string) *FeedItem {
	for i := range r.Items {
		if r.Items[i].TweetID == tweetID {
			return &r.Items[i]
		}
	}
	return nil
}

var (
	handleRe      = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)
	tweetIDRe     = regexp.MustCompile(`^[0-9]+$`)
	retweetPrefix = regexp.MustCompile(`(?i)^\s*RT\s+@?([A-Za-z0-9_]{1,15})[:：]\s*`)
	wsRe          = regexp.MustCompile(`\s+`)
	langHashtagRe = regexp.MustCompile(`#\S+`)
	langMentionRe = regexp.MustCompile(`@\S+`)
	langURLRe     = regexp.MustCompile(`https?://\S+`)
)

func NormalizeHandle(handle string) string {
	handle = strings.TrimSpace(strings.TrimPrefix(handle, "@"))
	handle = strings.TrimPrefix(handle, "twitter_")
	return handle
}

func ValidHandle(handle string) bool {
	return handleRe.MatchString(NormalizeHandle(handle))
}

func ValidTweetID(tweetID string) bool {
	return tweetIDRe.MatchString(strings.TrimSpace(tweetID))
}

func ParseDump(output []byte, fallbackSourceHandle string) ParseResult {
	records := parseRecords(output)
	sourceHandle := NormalizeHandle(fallbackSourceHandle)
	metaByID := make(map[string]map[string]any)
	mediaByID := make(map[string][]model.MediaRef)
	var order []string
	primaryIDs := make(map[string]bool)

	for _, rec := range records {
		if rec.Meta != nil {
			tid := tweetID(rec.Meta)
			if tid == "" {
				continue
			}
			if _, ok := metaByID[tid]; !ok {
				order = append(order, tid)
			}
			metaByID[tid] = rec.Meta
			if quoteID(rec.Meta) == "" || sourceOwnsQuoteExpansion(rec.Meta, sourceHandle) {
				primaryIDs[tid] = true
			}
		}
		if rec.MediaURL != "" && rec.Sidecar != nil {
			tid := tweetID(rec.Sidecar)
			if tid == "" || !isTrustedTwitterMediaURL(rec.MediaURL) {
				continue
			}
			if ref, ok := mediaRef(rec.MediaURL, rec.Sidecar); ok {
				mediaByID[tid] = append(mediaByID[tid], ref)
			}
		}
	}

	quoteForParent := make(map[string]map[string]any)
	var missing []StatusRef
	missingSeen := make(map[string]bool)
	for _, d := range metaByID {
		parentID := quoteID(d)
		if parentID == "" {
			continue
		}
		quoteForParent[parentID] = d
		if !primaryIDs[parentID] {
			handle := NormalizeHandle(firstString(d, "quote_by"))
			if handle == "" {
				handle = userHandle(d)
			}
			if ValidHandle(handle) {
				key := handle + "/" + parentID
				if !missingSeen[key] {
					missingSeen[key] = true
					missing = append(missing, StatusRef{Handle: handle, TweetID: parentID})
				}
			}
		}
	}

	var items []FeedItem
	for _, tid := range order {
		if !primaryIDs[tid] {
			continue
		}
		d := metaByID[tid]
		item := feedItemFromMeta(d, fallbackSourceHandle, mediaByID[tid], quoteForParent[tid], mediaByID)
		if item.TweetID == "" || item.AuthorHandle == "" {
			continue
		}
		items = append(items, item)
	}
	return ParseResult{Items: items, MissingQuoteParents: missing}
}

type record struct {
	Meta     map[string]any
	MediaURL string
	Sidecar  map[string]any
}

func parseRecords(output []byte) []record {
	var records []record
	for _, payload := range download.JSONPayloads(output) {
		records = append(records, recordsFromPayload(payload)...)
	}
	return records
}

func recordsFromPayload(value any) []record {
	switch v := value.(type) {
	case map[string]any:
		return []record{{Meta: v}}
	case []any:
		if len(v) >= 2 {
			if code, ok := intFromAny(v[0]); ok {
				switch code {
				case 2:
					if obj, ok := v[1].(map[string]any); ok {
						return []record{{Meta: obj}}
					}
				case 3:
					rawURL := stringFromAny(v[1])
					if len(v) >= 3 {
						if obj, ok := v[2].(map[string]any); ok {
							return []record{{MediaURL: rawURL, Sidecar: obj}}
						}
					}
				}
			}
		}
		var out []record
		for _, item := range v {
			out = append(out, recordsFromPayload(item)...)
		}
		return out
	default:
		return nil
	}
}

func feedItemFromMeta(d map[string]any, fallbackSourceHandle string, media []model.MediaRef, quote map[string]any, mediaByID map[string][]model.MediaRef) FeedItem {
	tid := tweetID(d)
	if tid == "" {
		return FeedItem{}
	}
	source := userHandle(d)
	if source == "" {
		source = NormalizeHandle(fallbackSourceHandle)
	}

	retweetID := firstString(d, "retweet_id")
	isRetweet := retweetID != "" && retweetID != "0"
	author := effectiveAuthorHandle(d, fallbackSourceHandle)
	if !ValidHandle(author) {
		return FeedItem{}
	}
	if source == "" {
		source = author
	}
	canonicalTweetID := tid
	if isRetweet && ValidTweetID(retweetID) {
		canonicalTweetID = retweetID
	}

	body := firstString(d, "content", "text", "description", "full_text")
	if isRetweet {
		body = stripRetweetPrefix(body)
	}
	body = stripTrailingTcoURL(body)

	media = dedupeMediaRefs(media)
	mediaJSON := mediaJSON(media)
	now := time.Now().UTC()
	publishedAt := firstTime(d, "date", "created_at", "timestamp")
	if publishedAt == nil {
		publishedAt = tweetSnowflakeTime(tid)
	}

	item := FeedItem{
		TweetID:                tid,
		SourceHandle:           source,
		AuthorHandle:           author,
		AuthorDisplayName:      authorDisplay(d),
		AuthorAvatarURL:        authorAvatar(d),
		BodyText:               body,
		Lang:                   firstString(d, "lang"),
		IsRetweet:              isRetweet,
		RetweetedByHandle:      "",
		RetweetedByDisplayName: "",
		MediaJSON:              mediaJSON,
		CanonicalURL:           "https://x.com/" + author + "/status/" + canonicalTweetID,
		ReplyToHandle:          NormalizeHandle(firstString(d, "reply_to")),
		ReplyToStatus:          nonZeroID(firstString(d, "reply_id")),
		Views:                  firstInt64(d, "view_count", "views"),
		Likes:                  firstInt64(d, "favorite_count", "like_count", "likes"),
		Retweets:               firstInt64(d, "retweet_count", "retweets"),
		PublishedAt:            publishedAt,
		FetchedAt:              now,
		CanonicalTweetID:       canonicalTweetID,
	}
	item.IsReply = item.ReplyToStatus != "" || item.ReplyToHandle != ""
	if language.IsUnknown(item.Lang) {
		item.Lang = DetectLang(body)
	}
	if isRetweet {
		item.RetweetedByHandle = source
		item.RetweetedByDisplayName = userDisplay(d)
	}

	if quote != nil {
		applyQuote(&item, quote, mediaByID[tweetID(quote)])
	}
	item.ParseMedia()
	item.ContentHash = contentHash(item.AuthorHandle, item.BodyText, item.Media)
	return item
}

func applyQuote(item *FeedItem, quote map[string]any, media []model.MediaRef) {
	if item == nil || quote == nil {
		return
	}
	qid := tweetID(quote)
	if qid == "" {
		return
	}
	qbody := stripTrailingTcoURL(firstString(quote, "content", "text", "description", "full_text"))
	media = dedupeMediaRefs(media)
	item.QuoteTweetID = qid
	item.QuoteAuthorHandle = authorHandle(quote)
	item.QuoteAuthorDisplayName = authorDisplay(quote)
	item.QuoteAuthorAvatarURL = authorAvatar(quote)
	item.QuoteBodyText = qbody
	item.QuoteLang = firstString(quote, "lang")
	if language.IsUnknown(item.QuoteLang) {
		item.QuoteLang = DetectLang(qbody)
	}
	item.QuotePublishedAt = firstTime(quote, "date", "created_at", "timestamp")
	if item.QuotePublishedAt == nil {
		item.QuotePublishedAt = tweetSnowflakeTime(qid)
	}
	item.QuoteMediaJSON = mediaJSON(media)
}

func mergeItem(base, next FeedItem) FeedItem {
	if base.BodyText == "" {
		base.BodyText = next.BodyText
	}
	if base.AuthorDisplayName == "" {
		base.AuthorDisplayName = next.AuthorDisplayName
	}
	if base.AuthorAvatarURL == "" {
		base.AuthorAvatarURL = next.AuthorAvatarURL
	}
	if base.MediaJSON == "" {
		base.MediaJSON = next.MediaJSON
	}
	if base.QuoteTweetID == "" {
		copyQuoteFields(&base, next)
	}
	if base.PublishedAt == nil {
		base.PublishedAt = next.PublishedAt
	}
	if base.CanonicalURL == "" {
		base.CanonicalURL = next.CanonicalURL
	}
	if base.ContentHash == "" {
		base.ContentHash = next.ContentHash
	}
	base.ParseMedia()
	return base
}

func authorMap(d map[string]any) map[string]any {
	if nested, ok := d["author"].(map[string]any); ok {
		return nested
	}
	return map[string]any{}
}

func userMap(d map[string]any) map[string]any {
	if nested, ok := d["user"].(map[string]any); ok {
		return nested
	}
	return map[string]any{}
}

func authorHandle(d map[string]any) string {
	return NormalizeHandle(firstString(authorMap(d), "name", "screen_name", "username"))
}

func effectiveAuthorHandle(d map[string]any, fallbackSourceHandle string) string {
	source := userHandle(d)
	if source == "" {
		source = NormalizeHandle(fallbackSourceHandle)
	}
	retweetID := firstString(d, "retweet_id")
	isRetweet := retweetID != "" && retweetID != "0"
	return NormalizeHandle(model.EffectiveTwitterAuthorHandle(authorHandle(d), source, isRetweet))
}

func sourceOwnsQuoteExpansion(d map[string]any, sourceHandle string) bool {
	sourceHandle = NormalizeHandle(sourceHandle)
	if sourceHandle == "" {
		return false
	}
	retweetID := firstString(d, "retweet_id")
	if retweetID != "" && retweetID != "0" {
		return false
	}
	author := authorHandle(d)
	if strings.EqualFold(author, sourceHandle) {
		return true
	}
	source := userHandle(d)
	if source == "" {
		return false
	}
	effectiveAuthor := model.EffectiveTwitterAuthorHandle(author, source, false)
	return strings.EqualFold(NormalizeHandle(effectiveAuthor), sourceHandle)
}

func userHandle(d map[string]any) string {
	return NormalizeHandle(firstString(userMap(d), "name", "screen_name", "username"))
}

func authorDisplay(d map[string]any) string {
	if s := firstString(authorMap(d), "nick", "display_name"); s != "" {
		return s
	}
	return authorHandle(d)
}

func userDisplay(d map[string]any) string {
	if s := firstString(userMap(d), "nick", "display_name"); s != "" {
		return s
	}
	return userHandle(d)
}

func authorAvatar(d map[string]any) string {
	return model.CleanFeedAvatarURL(firstString(authorMap(d), "profile_image", "profile_image_url", "avatar_url"))
}

func tweetID(d map[string]any) string {
	id := nonZeroID(firstString(d, "tweet_id", "id", "rest_id"))
	if id == "" {
		id = tweetIDFromURL(firstString(d, "url", "tweet_url", "webpage_url", "permalink"))
	}
	if !ValidTweetID(id) {
		return ""
	}
	return id
}

func quoteID(d map[string]any) string {
	return nonZeroID(firstString(d, "quote_id"))
}

func nonZeroID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || id == "0" || !ValidTweetID(id) {
		return ""
	}
	return id
}

func mediaRef(rawURL string, d map[string]any) (model.MediaRef, bool) {
	ref := model.MediaRef{
		URL:          rawURL,
		Type:         firstString(d, "type", "media_type"),
		ThumbnailURL: firstString(d, "thumbnail", "thumbnail_url", "preview_image_url"),
		AltText:      firstString(d, "description", "alt", "alt_text"),
		Width:        mediaDimension(d, "width", "w"),
		Height:       mediaDimension(d, "height", "h"),
	}
	if ref.Type == "animated_gif" || ref.Type == "gif" {
		ref.Type = "video"
	}
	if ref.Type == "" {
		switch strings.ToLower(firstString(d, "extension", "ext")) {
		case "mp4", "m3u8":
			ref.Type = "video"
		default:
			ref.Type = "photo"
		}
	}
	if ref.URL == "" && ref.ThumbnailURL == "" {
		return model.MediaRef{}, false
	}
	return ref, true
}

func mediaDimension(item map[string]any, keys ...string) int {
	n := firstInt64(item, keys...)
	if n <= 0 {
		return 0
	}
	const maxMediaDimension = int64(1<<31 - 1)
	if n > maxMediaDimension {
		return 0
	}
	return int(n)
}

func mediaJSON(media []model.MediaRef) string {
	if len(media) == 0 {
		return ""
	}
	data, _ := json.Marshal(dedupeMediaRefs(media))
	return string(data)
}

func dedupeMediaRefs(media []model.MediaRef) []model.MediaRef {
	seen := map[string]bool{}
	out := media[:0]
	for _, ref := range media {
		key := ref.URL
		if key == "" {
			key = ref.ThumbnailURL
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	return out
}

func isTrustedTwitterMediaURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	return strings.EqualFold(u.Hostname(), "pbs.twimg.com") || strings.EqualFold(u.Hostname(), "video.twimg.com")
}

func stripRetweetPrefix(content string) string {
	return strings.TrimSpace(retweetPrefix.ReplaceAllString(strings.TrimSpace(content), ""))
}

func stripTrailingTcoURL(content string) string {
	content = strings.TrimSpace(content)
	for {
		fields := strings.Fields(content)
		if len(fields) == 0 || !strings.HasPrefix(fields[len(fields)-1], "https://t.co/") {
			return content
		}
		content = strings.TrimSpace(strings.TrimSuffix(content, fields[len(fields)-1]))
	}
}

func contentHash(authorHandle, bodyText string, media []model.MediaRef) string {
	normAuthor := strings.ToLower(strings.TrimSpace(authorHandle))
	normBody := strings.ToLower(strings.TrimSpace(wsRe.ReplaceAllString(bodyText, " ")))
	var mediaKey string
	if len(media) > 0 {
		mediaKey = media[0].URL
		if i := strings.IndexByte(mediaKey, '?'); i >= 0 {
			mediaKey = mediaKey[:i]
		}
		mediaKey = strings.ToLower(mediaKey)
	}
	sum := sha256.Sum256([]byte(normAuthor + "|" + normBody + "|" + mediaKey))
	return fmt.Sprintf("%x", sum)[:16]
}

func DetectLang(text string) string {
	if len(strings.TrimSpace(text)) < 4 {
		return ""
	}
	clean := langHashtagRe.ReplaceAllString(text, "")
	clean = langMentionRe.ReplaceAllString(clean, "")
	clean = langURLRe.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(clean)
	if len(clean) < 4 {
		return ""
	}
	lang := langdetect.DetectLanguage([]byte(clean), "")
	code := strings.TrimRight(lang.String(), "\x00")
	if code == "" || code == "\x00\x00" {
		return ""
	}
	code = strings.ToLower(code)
	code = strings.ReplaceAll(code, "zh-cn", "zh")
	return code
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			if s := stringFromAny(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		if v.String() == "0" {
			return ""
		}
		return v.String()
	case float64:
		if v == 0 {
			return ""
		}
		return strconv.FormatInt(int64(v), 10)
	case int:
		if v == 0 {
			return ""
		}
		return strconv.Itoa(v)
	case int64:
		if v == 0 {
			return ""
		}
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

func intFromAny(value any) (int, bool) {
	switch v := value.(type) {
	case json.Number:
		n, err := strconv.Atoi(v.String())
		return n, err == nil
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func firstInt64(item map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			switch n := v.(type) {
			case json.Number:
				i, _ := strconv.ParseInt(n.String(), 10, 64)
				return i
			case float64:
				return int64(n)
			case int:
				return int64(n)
			case int64:
				return n
			case string:
				i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
				return i
			}
		}
	}
	return 0
}

func firstTime(item map[string]any, keys ...string) *time.Time {
	for _, key := range keys {
		v, ok := item[key]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case json.Number:
			if parsed := unixTimeFromString(t.String()); parsed != nil {
				return parsed
			}
		case float64:
			if t > 0 {
				tt := time.Unix(int64(t), 0).UTC()
				return &tt
			}
		case string:
			if parsed := parseTime(t); parsed != nil {
				return parsed
			}
		}
	}
	return nil
}

func parseTime(raw string) *time.Time {
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
			tt := t.UTC()
			return &tt
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
	t := time.Unix(n, 0).UTC()
	return &t
}

func tweetSnowflakeTime(id string) *time.Time {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1e15 {
		return nil
	}
	ms := (n >> 22) + 1288834974657
	t := time.UnixMilli(ms).UTC()
	return &t
}

func tweetIDFromURL(raw string) string {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	for i, part := range parts {
		if part == "status" && i+1 < len(parts) {
			id := strings.Split(parts[i+1], "?")[0]
			return nonZeroID(id)
		}
	}
	return ""
}
