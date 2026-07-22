package xfeed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/language"
	"github.com/screwys/igloo/internal/model"
)

const defaultTimelineLimit = 100

// Runner executes gallery-dl with the provided args and returns stdout/stderr.
type Runner func(ctx context.Context, args []string) ([]byte, error)

type TweetFallback interface {
	FetchTweet(ctx context.Context, handle, tweetID string) (*fxtwitter.Tweet, error)
}

type StatusEnrichmentKind string

const (
	StatusEnrichmentMissingQuoteParent StatusEnrichmentKind = "missing_quote_parent"
	StatusEnrichmentRetweetQuote       StatusEnrichmentKind = "retweet_quote"
)

type StatusEnrichmentRequest struct {
	Kind          StatusEnrichmentKind
	Ref           StatusRef
	TargetTweetID string
}

// Client fetches X/Twitter timelines through gallery-dl.
type Client struct {
	Runner               Runner
	CookiePool           *CookiePool
	TweetFallback        TweetFallback
	StatusEnrichmentSink func(StatusEnrichmentRequest)
	OperationSink        download.OperationSink
}

// NewClient returns a gallery-dl backed X feed client.
func NewClient(cookiesDir string) *Client {
	return &Client{
		Runner:        runGalleryDL,
		CookiePool:    NewCookiePool(cookiesDir),
		TweetFallback: fxtwitter.NewClient(),
	}
}

// FetchTimeline fetches one account timeline, including replies.
func (c *Client) FetchTimeline(ctx context.Context, handle string, limit int) ([]FeedItem, error) {
	handle = NormalizeHandle(handle)
	if !ValidHandle(handle) {
		return nil, fmt.Errorf("invalid X handle: %q", handle)
	}
	if limit <= 0 {
		limit = defaultTimelineLimit
	}
	rawURL := "https://x.com/" + handle + "/with_replies"
	output, err := c.dump(ctx, rawURL, limit)
	if err != nil {
		return nil, err
	}
	parsed := ParseDump(output, handle)
	if err := c.enrichStatuses(ctx, &parsed); err != nil {
		return nil, err
	}
	return parsed.Items, nil
}

// FetchSource fetches an X list/community URL and parses it into feed items.
func (c *Client) FetchSource(ctx context.Context, rawURL string, limit int) ([]FeedItem, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("x source URL is required")
	}
	if limit <= 0 {
		limit = defaultTimelineLimit
	}
	output, err := c.dump(ctx, rawURL, limit)
	if err != nil {
		return nil, err
	}
	parsed := ParseDump(output, "")
	if err := c.enrichStatuses(ctx, &parsed); err != nil {
		return nil, err
	}
	return parsed.Items, nil
}

// FetchStatus fetches one status URL and parses the returned tweet plus quote
// expansion records.
func (c *Client) FetchStatus(ctx context.Context, handle, tweetID string) (ParseResult, error) {
	handle = NormalizeHandle(handle)
	tweetID = strings.TrimSpace(tweetID)
	if !ValidHandle(handle) {
		return ParseResult{}, fmt.Errorf("invalid X handle: %q", handle)
	}
	if !ValidTweetID(tweetID) {
		return ParseResult{}, fmt.Errorf("invalid X status id: %q", tweetID)
	}
	output, err := c.dump(ctx, "https://x.com/"+handle+"/status/"+tweetID, 5)
	if err != nil {
		return ParseResult{}, err
	}
	parsed := ParseDump(output, handle)
	return parsed, nil
}

func (c *Client) enrichStatuses(ctx context.Context, parsed *ParseResult) error {
	if parsed == nil {
		return nil
	}
	if c == nil {
		return nil
	}
	hasTweetFallback := c.TweetFallback != nil

	seen := make(map[string]bool)
	for _, ref := range parsed.MissingQuoteParents {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		key := ref.Handle + "/" + ref.TweetID
		if seen[key] || ref.Handle == "" || ref.TweetID == "" {
			continue
		}
		seen[key] = true
		req := StatusEnrichmentRequest{
			Kind: StatusEnrichmentMissingQuoteParent,
			Ref:  ref,
		}
		if fallback, ok := c.fallbackFeedItem(ctx, ref.Handle, ref.TweetID); ok {
			parsed.Merge(ParseResult{Items: []FeedItem{fallback}})
			if parent := parsed.Find(ref.TweetID); parent != nil && parent.QuoteTweetID != "" {
				continue
			}
		}
		if hasTweetFallback {
			c.deferStatusEnrichment(req)
			continue
		}
		status, err := c.FetchStatus(ctx, ref.Handle, ref.TweetID)
		if err == nil {
			parsed.Merge(status)
			if parent := parsed.Find(ref.TweetID); parent != nil && parent.QuoteTweetID != "" {
				continue
			}
		}
		c.deferStatusEnrichment(req)
	}

	for i := range parsed.Items {
		item := &parsed.Items[i]
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if item.IsReply && (item.ReplyToStatus == "" || item.ReplyToHandle == "") {
			if fallback, ok := c.fallbackFeedItem(ctx, item.AuthorHandle, item.TweetID); ok {
				applyFallbackDetails(item, fallback)
			}
		}
		if !item.IsRetweet || item.QuoteTweetID != "" || item.CanonicalTweetID == "" || item.AuthorHandle == "" {
			continue
		}
		key := item.AuthorHandle + "/" + item.CanonicalTweetID
		if seen[key] {
			continue
		}
		seen[key] = true
		ref := StatusRef{Handle: item.AuthorHandle, TweetID: item.CanonicalTweetID}
		req := StatusEnrichmentRequest{
			Kind:          StatusEnrichmentRetweetQuote,
			Ref:           ref,
			TargetTweetID: item.TweetID,
		}
		if fallback, ok := c.fallbackFeedItem(ctx, item.AuthorHandle, item.CanonicalTweetID); ok {
			if fallback.QuoteTweetID != "" {
				copyQuoteFields(item, fallback)
			}
			continue
		}
		if hasTweetFallback {
			c.deferStatusEnrichment(req)
			continue
		}
		status, err := c.FetchStatus(ctx, item.AuthorHandle, item.CanonicalTweetID)
		if err == nil {
			if rich := status.Find(item.CanonicalTweetID); rich != nil && rich.QuoteTweetID != "" {
				copyQuoteFields(item, *rich)
				continue
			}
			continue
		}
		c.deferStatusEnrichment(req)
	}
	return nil
}

func (c *Client) deferStatusEnrichment(req StatusEnrichmentRequest) {
	if c == nil || c.StatusEnrichmentSink == nil {
		return
	}
	if !ValidHandle(req.Ref.Handle) || !ValidTweetID(req.Ref.TweetID) {
		return
	}
	c.StatusEnrichmentSink(req)
}

func (c *Client) fallbackFeedItem(ctx context.Context, handle, tweetID string) (FeedItem, bool) {
	if c == nil || c.TweetFallback == nil || !ValidHandle(handle) || !ValidTweetID(tweetID) {
		return FeedItem{}, false
	}
	tweet, err := c.TweetFallback.FetchTweet(ctx, handle, tweetID)
	if err != nil {
		return FeedItem{}, false
	}
	item := feedItemFromFallbackTweet(tweet, handle)
	return item, item.TweetID != ""
}

func applyFallbackDetails(item *FeedItem, fallback FeedItem) {
	if item == nil {
		return
	}
	if item.ReplyToStatus == "" {
		item.ReplyToStatus = fallback.ReplyToStatus
	}
	if item.ReplyToHandle == "" {
		item.ReplyToHandle = fallback.ReplyToHandle
	}
	item.IsReply = item.IsReply || fallback.IsReply
	if item.BodyText == "" {
		item.BodyText = fallback.BodyText
	}
	if item.MediaJSON == "" {
		item.MediaJSON = fallback.MediaJSON
		item.Media = fallback.Media
	}
	if item.QuoteTweetID == "" {
		copyQuoteFields(item, fallback)
	}
}

func copyQuoteFields(dst *FeedItem, src FeedItem) {
	dst.QuoteTweetID = src.QuoteTweetID
	dst.QuoteAuthorHandle = src.QuoteAuthorHandle
	dst.QuoteAuthorDisplayName = src.QuoteAuthorDisplayName
	dst.QuoteAuthorAvatarURL = src.QuoteAuthorAvatarURL
	dst.QuoteBodyText = src.QuoteBodyText
	dst.QuoteLang = src.QuoteLang
	dst.QuotePublishedAt = src.QuotePublishedAt
	dst.QuoteMediaJSON = src.QuoteMediaJSON
	dst.QuoteMedia = src.QuoteMedia
}

func feedItemFromFallbackTweet(tweet *fxtwitter.Tweet, sourceHandle string) FeedItem {
	if tweet == nil || !ValidTweetID(tweet.ID) {
		return FeedItem{}
	}
	author := NormalizeHandle(tweet.AuthorHandle)
	if !ValidHandle(author) {
		return FeedItem{}
	}
	source := NormalizeHandle(sourceHandle)
	if source == "" {
		source = author
	}
	media := trustedMediaFromJSON(tweet.MediaJSON)
	publishedAt := timePtr(tweet.CreatedAt)
	now := time.Now().UTC()
	item := FeedItem{
		TweetID:           tweet.ID,
		SourceHandle:      source,
		AuthorHandle:      author,
		AuthorDisplayName: tweet.AuthorDisplayName,
		AuthorAvatarURL:   model.CleanFeedAvatarURL(tweet.AuthorAvatarURL),
		BodyText:          stripTrailingTcoURL(tweet.Text),
		Lang:              tweet.Lang,
		MediaJSON:         mediaJSON(media),
		CanonicalURL:      "https://x.com/" + author + "/status/" + tweet.ID,
		ReplyToHandle:     NormalizeHandle(tweet.ReplyToHandle),
		ReplyToStatus:     nonZeroID(tweet.ReplyToStatus),
		PublishedAt:       publishedAt,
		FetchedAt:         now,
		CanonicalTweetID:  tweet.ID,
	}
	item.IsReply = item.ReplyToStatus != "" || item.ReplyToHandle != ""
	if language.IsUnknown(item.Lang) {
		item.Lang = DetectLang(item.BodyText)
	}
	if tweet.Quote != nil {
		applyFallbackQuote(&item, tweet.Quote)
	}
	item.ParseMedia()
	item.ContentHash = contentHash(item.AuthorHandle, item.BodyText, item.Media)
	return item
}

func applyFallbackQuote(item *FeedItem, quote *fxtwitter.Tweet) {
	if item == nil || quote == nil || !ValidTweetID(quote.ID) {
		return
	}
	author := NormalizeHandle(quote.AuthorHandle)
	if !ValidHandle(author) {
		return
	}
	media := trustedMediaFromJSON(quote.MediaJSON)
	item.QuoteTweetID = quote.ID
	item.QuoteAuthorHandle = author
	item.QuoteAuthorDisplayName = quote.AuthorDisplayName
	item.QuoteAuthorAvatarURL = model.CleanFeedAvatarURL(quote.AuthorAvatarURL)
	item.QuoteBodyText = stripTrailingTcoURL(quote.Text)
	item.QuoteLang = quote.Lang
	if language.IsUnknown(item.QuoteLang) {
		item.QuoteLang = DetectLang(item.QuoteBodyText)
	}
	item.QuotePublishedAt = timePtr(quote.CreatedAt)
	item.QuoteMediaJSON = mediaJSON(media)
	item.ParseMedia()
}

func trustedMediaFromJSON(raw string) []model.MediaRef {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var refs []model.MediaRef
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	out := refs[:0]
	for _, ref := range refs {
		urlOK := ref.URL == "" || isTrustedTwitterMediaURL(ref.URL)
		thumbOK := ref.ThumbnailURL == "" || isTrustedTwitterMediaURL(ref.ThumbnailURL)
		if !urlOK || !thumbOK || (ref.URL == "" && ref.ThumbnailURL == "") {
			continue
		}
		out = append(out, ref)
	}
	return dedupeMediaRefs(out)
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	tt := t.UTC()
	return &tt
}

func (c *Client) dump(ctx context.Context, rawURL string, limit int) ([]byte, error) {
	started := time.Now()
	args := galleryDLArgs(rawURL, limit)
	cookies := c.cookies()
	attempts := 0
	usedArgs := args
	usedCookie := ""
	var output []byte
	var err error
	defer func() {
		op := model.DownloaderOperation{
			Operation:   "x.gallerydl.dump",
			Platform:    "twitter",
			Subject:     rawURL,
			Tool:        "gallery-dl",
			StartedAtMs: started.UnixMilli(),
			EndedAtMs:   time.Now().UnixMilli(),
			Status:      download.OperationStatusSuccess,
			CookieLabel: download.CookieLabel(usedCookie, ""),
			SummaryJSON: download.RedactText(mustJSON(map[string]any{
				"args":     download.RedactArgs(usedArgs),
				"attempts": attempts,
			})),
		}
		if err != nil {
			op.Status = download.OperationStatusFailure
			op.ErrorKind = download.ClassifyError(err, output)
			op.Error = download.RedactText(err.Error())
		}
		if c != nil && c.OperationSink != nil {
			_ = c.OperationSink.RecordDownloaderOperation(ctx, op)
		}
	}()
	if len(cookies) == 0 {
		attempts = 1
		output, err = c.run(ctx, args)
		return output, err
	}
	for i := 0; i < len(cookies); i++ {
		usedCookie = cookies[i]
		usedArgs = append([]string{"--cookies", usedCookie}, args...)
		attempts++
		output, err = c.run(ctx, usedArgs)
		if err == nil {
			return output, nil
		}
		kind := download.ClassifyError(err, output)
		if kind != download.ErrorKindAuth && kind != download.ErrorKindRateLimit {
			break
		}
	}
	return nil, err
}

func (c *Client) run(ctx context.Context, args []string) ([]byte, error) {
	runner := runGalleryDL
	if c != nil && c.Runner != nil {
		runner = c.Runner
	}
	out, err := runner(ctx, args)
	if err == nil {
		err = galleryDLSemanticError(out)
	}
	if err != nil {
		return out, fmt.Errorf("gallery-dl X feed: %w: %s", err, out)
	}
	return out, nil
}

func galleryDLSemanticError(output []byte) error {
	for _, payload := range download.JSONPayloads(output) {
		if err := galleryDLSemanticErrorFromPayload(payload); err != nil {
			return err
		}
	}
	return nil
}

func galleryDLSemanticErrorFromPayload(payload any) error {
	values, ok := payload.([]any)
	if !ok {
		return nil
	}
	if len(values) >= 2 {
		if code, ok := intFromAny(values[0]); ok && code == -1 {
			if detail, ok := values[1].(map[string]any); ok {
				name := strings.TrimSpace(firstString(detail, "error", "type", "code"))
				message := strings.TrimSpace(firstString(detail, "message", "detail", "description"))
				if name != "" && message != "" {
					return fmt.Errorf("gallery-dl reported %s: %s", name, message)
				}
				if name != "" {
					return fmt.Errorf("gallery-dl reported %s", name)
				}
				if message != "" {
					return fmt.Errorf("gallery-dl reported: %s", message)
				}
			}
			return fmt.Errorf("gallery-dl reported an error")
		}
	}
	for _, value := range values {
		if err := galleryDLSemanticErrorFromPayload(value); err != nil {
			return err
		}
	}
	return nil
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func galleryDLArgs(rawURL string, limit int) []string {
	if limit <= 0 {
		limit = defaultTimelineLimit
	}
	return []string{
		"--dump-json",
		"--simulate",
		"--no-input",
		"-o", "extractor.twitter.text-tweets=true",
		"-o", "extractor.twitter.retweets=true",
		"-o", "extractor.twitter.quoted=true",
		"--range", "1-" + strconv.Itoa(limit),
		rawURL,
	}
}

func runGalleryDL(ctx context.Context, args []string) ([]byte, error) {
	result := download.CommandRunner{}.Run(ctx, "gallery-dl", args, download.CommandOptions{Timeout: 2 * time.Minute})
	return result.CombinedOutput(), result.Err
}

func (c *Client) cookies() []string {
	if c == nil || c.CookiePool == nil {
		return nil
	}
	return c.CookiePool.NextBatch()
}

// CookiePool rotates gallery-dl cookie files across requests.
type CookiePool struct {
	paths []string
	next  atomic.Uint64
}

func NewCookiePool(cookiesDir string) *CookiePool {
	return &CookiePool{paths: DiscoverCookieFiles(cookiesDir)}
}

func (p *CookiePool) NextBatch() []string {
	if p == nil || len(p.paths) == 0 {
		return nil
	}
	start := int(p.next.Add(1)-1) % len(p.paths)
	out := make([]string, 0, len(p.paths))
	for i := 0; i < len(p.paths); i++ {
		out = append(out, p.paths[(start+i)%len(p.paths)])
	}
	return out
}

func DiscoverCookieFiles(cookiesDir string) []string {
	candidates := download.DiscoverCookieFiles(strings.TrimSpace(cookiesDir), "twitter")
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Path)
	}
	return out
}
