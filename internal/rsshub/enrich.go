package rsshub

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/screwys/igloo/internal/model"
	"github.com/taruti/langdetect"
)

// tweetSnowflakeTime extracts the timestamp from a Twitter snowflake ID.
// Returns nil if the ID is not a valid snowflake.
func tweetSnowflakeTime(id string) *time.Time {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1e15 {
		return nil
	}
	ms := (n >> 22) + 1288834974657
	t := time.UnixMilli(ms).UTC()
	return &t
}

// Compiled package-level regexes matching Python's rsshub_feed.py patterns.
var (
	// Author block: captures (handle, avatar_src, display_name)
	// Note: Python's regex captures src before hspace attribute.
	reAuthor = regexp.MustCompile(
		`(?is)<a\s+href=['"]https?://(?:x\.com|twitter\.com)/([A-Za-z0-9_]{1,15})['"][^>]*>` +
			`\s*<img\b[^>]*src=['"]([^'"]+)['"][^>]*hspace=['"]8['"][^>]*/?\s*>` +
			`\s*<strong>([^<]+)</strong>\s*</a>`,
	)

	// RT indicator: captures (retweeter_handle, retweeter_display_name)
	reRT = regexp.MustCompile(
		`(?is)<small>\s*<a\s+href=['"]https?://(?:x\.com|twitter\.com)/([A-Za-z0-9_]{1,15})['"][^>]*>` +
			`\s*<strong>([^<]+)</strong>\s*</a>[^<]*` + "\U0001F501" + `\s*</small>`,
	)

	// RT body prefix: "RT @handle: " at start of body text
	reRTPrefix = regexp.MustCompile(`(?i)^\s*RT\s+@?([A-Za-z0-9_]{1,15})[:：]\s*`)

	// Quote block: captures inner HTML of <div class="rsshub-quote">
	reQuote = regexp.MustCompile(
		`(?is)<div\s+class=["']rsshub-quote["']>(.*?)</div>\s*(?:<br\b[^>]*/?>.*)?$`,
	)

	// Quote author: captures (handle, avatar_src, display_name)
	reQuoteAuthor = regexp.MustCompile(
		`(?is)<a\s+href=['"]https?://(?:x\.com|twitter\.com)/([A-Za-z0-9_]{1,15})['"][^>]*>` +
			`\s*<img\b[^>]*src=['"]([^'"]+)['"][^>]*/?\s*>` +
			`\s*<strong>([^<]+)</strong>\s*</a>`,
	)

	// Quote status link: captures (handle, tweet_id)
	reQuoteLink = regexp.MustCompile(
		`(?i)https?://(?:x\.com|twitter\.com)/([A-Za-z0-9_]{1,15})/status/(\d+)`,
	)

	// Media: photo via linked image
	reMediaImg = regexp.MustCompile(
		`(?is)<a\b[^>]*href=['"]([^'"]*pbs\.twimg\.com/media/[^'"]+)['"][^>]*>\s*<img\b[^>]*/?\s*>\s*</a>`,
	)

	// Media: video element
	reMediaVideo = regexp.MustCompile(
		`(?is)<video\b[^>]*src=['"]([^'"]*video\.twimg\.com/[^'"]+)['"][^>]*/?\s*>`,
	)

	// Zero-size hidden elements RSSHub adds for RSS readers
	reZeroSize = regexp.MustCompile(
		`(?i)<(?:img|video)\b[^>]*width=['"]0['"][^>]*/?\s*>`,
	)

	// Footer: <hr/> + <small>text</small> at end
	reFooter = regexp.MustCompile(
		`(?is)<hr\s*/?\s*>\s*<small>[^<]*</small>\s*$`,
	)

	// Block-break tags become newlines in stripHTML
	reBlockBreak = regexp.MustCompile(
		`(?i)<\s*br\s*/?\s*>|</\s*(?:p|div|li|blockquote|tr|h[1-6])\s*>|<\s*(?:p|div|li|blockquote|tr|h[1-6])\b[^>]*>`,
	)

	// Any remaining HTML tag
	reTag = regexp.MustCompile(`<[^>]+>`)

	// Whitespace collapse within a line
	reWS = regexp.MustCompile(`\s+`)

	// Tweet status URL for extracting tweet ID
	reTweetURL = regexp.MustCompile(
		`(?i)https?://(?:www\.)?(?:x\.com|twitter\.com)/[A-Za-z0-9_]{1,15}/status/(\d+)`,
	)

	// Reply marker at the start of body text. RSSHub may emit only "↩️ "
	// without a parent handle, so handle extraction is intentionally separate.
	reReplyMarker       = regexp.MustCompile(`(?i)^\s*↩\x{FE0F}?\s*`)
	reReplyHandlePrefix = regexp.MustCompile(`(?i)^@([A-Za-z0-9_]{1,15})(?:\s+|[:：]\s*|$)`)
)

// stripHTML replaces block-break tags with newlines, strips all HTML tags,
// unescapes HTML entities, collapses whitespace within lines, and trims.
func stripHTML(s string) string {
	if s == "" {
		return ""
	}
	s = reBlockBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)

	var lines []string
	blankRun := 0
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(reWS.ReplaceAllString(raw, " "))
		if line == "" {
			blankRun++
			if blankRun <= 1 {
				lines = append(lines, "")
			}
			continue
		}
		blankRun = 0
		lines = append(lines, line)
	}
	result := strings.TrimSpace(strings.Join(lines, "\n"))
	// collapse 3+ consecutive newlines to 2
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result
}

// contentHash returns the first 16 hex chars of SHA256(handle|body|mediaKey).
// Inputs are normalised: lowercased, whitespace collapsed. mediaKey is the
// query-stripped URL of the first media item (empty when there is no media),
// which carries the stable Twitter media ID. Including it distinguishes
// independent media-only posts by the same author (where body is empty or
// repeated boilerplate) while still grouping a retweet with its original
// (both reference the same underlying tweet's media ID).
func contentHash(authorHandle, bodyText string, media []model.MediaRef) string {
	normAuthor := strings.ToLower(strings.TrimSpace(authorHandle))
	normBody := strings.ToLower(strings.TrimSpace(reWS.ReplaceAllString(bodyText, " ")))
	var mediaKey string
	if len(media) > 0 {
		u := media[0].URL
		if i := strings.IndexByte(u, '?'); i >= 0 {
			u = u[:i]
		}
		mediaKey = strings.ToLower(u)
	}
	raw := normAuthor + "|" + normBody + "|" + mediaKey
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum)[:16]
}

// extractTweetID extracts the numeric tweet ID from a status URL.
func extractTweetID(link string) string {
	m := reTweetURL.FindStringSubmatch(link)
	if m == nil {
		return ""
	}
	return m[1]
}

// extractQuote parses a <div class="rsshub-quote"> block and returns its fields.
func extractQuote(descHTML string) (tweetID, authorHandle, authorDisplay, authorAvatar, bodyText, mediaJSON string) {
	qm := reQuote.FindStringSubmatchIndex(descHTML)
	if qm == nil {
		return
	}
	block := descHTML[qm[2]:qm[3]]

	// Author within quote block — use Index variant to get correct end position
	// (the match may not start at offset 0 if there's leading whitespace).
	amIdx := reQuoteAuthor.FindStringSubmatchIndex(block)
	if amIdx != nil {
		authorHandle = block[amIdx[2]:amIdx[3]]
		authorAvatar = model.CleanFeedAvatarURL(html.UnescapeString(strings.TrimSpace(block[amIdx[4]:amIdx[5]])))
		authorDisplay = html.UnescapeString(strings.TrimSpace(block[amIdx[6]:amIdx[7]]))
	}

	// Status link for tweet ID
	lm := reQuoteLink.FindStringSubmatch(block)
	if lm != nil {
		tweetID = lm[2]
	}

	// Body: strip author block, then cut at "<small>Link:"
	bodyHTML := block
	if amIdx != nil {
		bodyHTML = block[amIdx[1]:]
	}
	linkPos := strings.Index(strings.ToLower(bodyHTML), "<small>link:")
	if linkPos >= 0 {
		bodyHTML = bodyHTML[:linkPos]
	}
	bodyText = strings.TrimSpace(stripHTML(bodyHTML))
	if strings.HasPrefix(bodyText, ":") {
		bodyText = strings.TrimSpace(bodyText[1:])
	}

	// Media within quote block
	var qMedia []model.MediaRef
	seen := map[string]bool{}
	for _, u := range reMediaImg.FindAllStringSubmatch(bodyHTML, -1) {
		clean := html.UnescapeString(u[1])
		if !seen[clean] {
			seen[clean] = true
			qMedia = append(qMedia, model.MediaRef{URL: clean, Type: "photo"})
		}
	}
	for _, u := range reMediaVideo.FindAllStringSubmatch(bodyHTML, -1) {
		clean := html.UnescapeString(u[1])
		if !seen[clean] {
			seen[clean] = true
			qMedia = append(qMedia, model.MediaRef{URL: clean, Type: "video"})
		}
	}
	if len(qMedia) > 0 {
		b, _ := json.Marshal(qMedia)
		mediaJSON = string(b)
	}
	return
}

// ToFeedItems converts a parsed RSSFeed into a slice of FeedItem values.
func ToFeedItems(feed *RSSFeed, sourceHandle string) []model.FeedItem {
	items := make([]model.FeedItem, 0, len(feed.Items))
	for _, raw := range feed.Items {
		item := enrichItem(raw, sourceHandle)
		if item == nil || item.TweetID == "" {
			continue
		}
		items = append(items, *item)
	}
	return items
}

func enrichItem(raw RSSItem, sourceHandle string) *model.FeedItem {
	desc := raw.Description
	tweetID := extractTweetID(raw.Link)
	if tweetID == "" {
		// No tweet ID — can't identify this item; skip it.
		return nil
	}

	// --- Author ---
	authorHandle, authorDisplay, authorAvatarRaw := "", "", ""
	am := reAuthor.FindStringSubmatch(desc)
	if am != nil {
		authorHandle = am[1]
		authorAvatarRaw = html.UnescapeString(strings.TrimSpace(am[2]))
		authorDisplay = html.UnescapeString(strings.TrimSpace(am[3]))
	}
	// Fallback: extract handle from link URL
	if authorHandle == "" {
		lm := reQuoteLink.FindStringSubmatch(raw.Link)
		if lm != nil {
			authorHandle = lm[1]
		}
	}

	// --- Retweet (emoji 🔁 block) ---
	rtHandle, rtDisplay := "", ""
	rtm := reRT.FindStringSubmatch(desc)
	if rtm != nil {
		rtHandle = rtm[1]
		rtDisplay = html.UnescapeString(strings.TrimSpace(rtm[2]))
	}
	isRetweet := rtHandle != ""

	// --- Quote ---
	qTweetID, qAuthorHandle, qAuthorDisplay, qAuthorAvatar, qBodyText, qMediaJSON :=
		extractQuote(desc)

	// --- Body ---
	bodyHTML := desc
	bodyHTML = reRT.ReplaceAllString(bodyHTML, "")
	bodyHTML = reAuthor.ReplaceAllString(bodyHTML, "")
	bodyHTML = reQuote.ReplaceAllString(bodyHTML, "")
	bodyHTML = reMediaImg.ReplaceAllString(bodyHTML, "")
	bodyHTML = reMediaVideo.ReplaceAllString(bodyHTML, "")
	bodyHTML = reFooter.ReplaceAllString(bodyHTML, "")
	bodyText := strings.TrimSpace(stripHTML(bodyHTML))
	if strings.HasPrefix(bodyText, ":") {
		bodyText = strings.TrimSpace(bodyText[1:])
	}
	// Remove en-space character (U+2002) used by RSSHub as separator
	bodyText = strings.ReplaceAll(bodyText, "\u2002", "")
	bodyText = strings.TrimSpace(bodyText)

	// --- Fallback RT detection: body starts with "RT @handle:" ---
	if !isRetweet && bodyText != "" {
		pfx := reRTPrefix.FindStringSubmatch(bodyText)
		if pfx != nil {
			if strings.EqualFold(authorHandle, pfx[1]) {
				// Author block already contains the original author — retweeter is the feed owner.
				// Keep authorDisplay and authorAvatarRaw intact; retweeter info comes from sourceHandle.
				rtHandle = sourceHandle
				if strings.EqualFold(sourceHandle, authorHandle) {
					// Self-retweet: author IS the retweeter, use their display name.
					rtDisplay = authorDisplay
				}
			} else {
				// Author block is the retweeter; RT prefix holds the original author.
				rtHandle = authorHandle
				rtDisplay = authorDisplay
				authorHandle = pfx[1]
				authorDisplay = "" // not available from text prefix alone
				authorAvatarRaw = ""
			}
			isRetweet = true
			// Strip "RT @handle: " prefix from body
			bodyText = strings.TrimSpace(bodyText[utf8.RuneCountInString(pfx[0]):])
		}
	}

	// --- Reply detection (looks at the cleaned body, after RT-prefix stripping) ---
	isReply := false
	replyToHandle := ""
	if marker := reReplyMarker.FindString(bodyText); marker != "" {
		isReply = true
		bodyText = strings.TrimSpace(bodyText[len(marker):])
		if pfx := reReplyHandlePrefix.FindStringSubmatchIndex(bodyText); pfx != nil {
			replyToHandle = bodyText[pfx[2]:pfx[3]]
			bodyText = strings.TrimSpace(bodyText[pfx[1]:])
		}
	}

	// --- For RTs, rewrite canonical URL to original author's tweet ---
	canonicalURL := raw.Link
	if isRetweet && authorHandle != "" && tweetID != "" {
		canonicalURL = "https://x.com/" + authorHandle + "/status/" + tweetID
	}

	// Pass through original avatar URLs unchanged — rewriting to local proxy
	// happens at render time in feed.EnrichFeedItems / annotateChannelFlags.
	authorAvatar := model.CleanFeedAvatarURL(authorAvatarRaw)

	// --- Parent media (excluding quote block) ---
	searchHTML := desc
	qm := reQuote.FindStringIndex(desc)
	if qm != nil {
		searchHTML = desc[:qm[0]] + desc[qm[1]:]
	}
	searchHTML = reAuthor.ReplaceAllString(searchHTML, "")
	searchHTML = reZeroSize.ReplaceAllString(searchHTML, "")

	var media []model.MediaRef
	seenURLs := map[string]bool{}

	// Build a set of quote media URL stems for dedup
	qURLStems := map[string]bool{}
	if qMediaJSON != "" {
		var qmedia []model.MediaRef
		if json.Unmarshal([]byte(qMediaJSON), &qmedia) == nil {
			for _, m := range qmedia {
				stem := strings.SplitN(m.URL, "?", 2)[0]
				qURLStems[strings.ToLower(stem)] = true
			}
		}
	}

	for _, u := range reMediaImg.FindAllStringSubmatch(searchHTML, -1) {
		clean := html.UnescapeString(u[1])
		stem := strings.ToLower(strings.SplitN(clean, "?", 2)[0])
		if seenURLs[clean] || qURLStems[stem] {
			continue
		}
		seenURLs[clean] = true
		media = append(media, model.MediaRef{URL: clean, Type: "photo"})
	}
	for _, u := range reMediaVideo.FindAllStringSubmatch(searchHTML, -1) {
		clean := html.UnescapeString(u[1])
		stem := strings.ToLower(strings.SplitN(clean, "?", 2)[0])
		if seenURLs[clean] || qURLStems[stem] {
			continue
		}
		seenURLs[clean] = true
		media = append(media, model.MediaRef{URL: clean, Type: "video"})
	}

	var mediaJSON string
	if len(media) > 0 {
		b, _ := json.Marshal(media)
		mediaJSON = string(b)
	}

	// --- Published time ---
	var pubAt *time.Time
	if !raw.PubDate.IsZero() {
		t := raw.PubDate.UTC()
		pubAt = &t
	}

	// --- Content hash ---
	hash := contentHash(authorHandle, bodyText, media)

	return &model.FeedItem{
		TweetID:                tweetID,
		SourceHandle:           sourceHandle,
		AuthorHandle:           authorHandle,
		AuthorDisplayName:      authorDisplay,
		AuthorAvatarURL:        authorAvatar,
		BodyText:               bodyText,
		Lang:                   DetectLang(bodyText),
		IsRetweet:              isRetweet,
		RetweetedByHandle:      rtHandle,
		RetweetedByDisplayName: rtDisplay,
		QuoteTweetID:           qTweetID,
		QuoteAuthorHandle:      qAuthorHandle,
		QuoteAuthorDisplayName: qAuthorDisplay,
		QuoteAuthorAvatarURL:   qAuthorAvatar,
		QuoteBodyText:          qBodyText,
		QuoteLang:              DetectLang(qBodyText),
		QuotePublishedAt:       tweetSnowflakeTime(qTweetID),
		QuoteMediaJSON:         qMediaJSON,
		MediaJSON:              mediaJSON,
		CanonicalURL:           canonicalURL,
		ReplyToHandle:          replyToHandle,
		IsReply:                isReply,
		PublishedAt:            pubAt,
		ContentHash:            hash,
		FetchedAt:              time.Now().UTC(),
	}
}

var (
	reLangHashtag = regexp.MustCompile(`#\S+`)
	reLangMention = regexp.MustCompile(`@\S+`)
	reLangURL     = regexp.MustCompile(`https?://\S+`)
)

// DetectLang detects the language of text, stripping hashtags/mentions/URLs first.
// Returns ISO 639-1 code or empty string.
func DetectLang(text string) string {
	if len(strings.TrimSpace(text)) < 4 {
		return ""
	}
	clean := reLangHashtag.ReplaceAllString(text, "")
	clean = reLangMention.ReplaceAllString(clean, "")
	clean = reLangURL.ReplaceAllString(clean, "")
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
