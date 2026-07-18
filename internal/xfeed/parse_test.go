package xfeed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/fxtwitter"
	"github.com/screwys/igloo/internal/model"
)

type fakeTweetFallback struct {
	fetch func(context.Context, string, string) (*fxtwitter.Tweet, error)
}

func (f fakeTweetFallback) FetchTweet(ctx context.Context, handle, tweetID string) (*fxtwitter.Tweet, error) {
	return f.fetch(ctx, handle, tweetID)
}

func TestParseDumpFoldsParentAndQuoteMedia(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"1000000000000000100","content":"parent text","date":"2026-05-09 10:27:49","lang":"en","favorite_count":3,"retweet_count":4,"view_count":5,"author":{"name":"parent_author","nick":"Parent Author","profile_image":"https://pbs.twimg.com/profile_images/a_normal.jpg"},"user":{"name":"source_user","nick":"Source User"},"quote_id":0,"reply_id":0,"retweet_id":0}],
		[3, "https://pbs.twimg.com/media/parent.jpg?format=jpg&name=orig", {"tweet_id":"1000000000000000100","type":"photo","width":1200,"height":800}],
		[2, {"tweet_id":"1000000000000000200","content":"quoted text","date":"2026-05-09 09:00:00","lang":"en","author":{"name":"quote_author","nick":"Quote Author","profile_image":"https://pbs.twimg.com/profile_images/q_normal.jpg"},"user":{"name":"source_user","nick":"Source User"},"quote_id":"1000000000000000100","reply_id":0,"retweet_id":0}],
		[3, "https://video.twimg.com/ext_tw_video/quote.mp4", {"tweet_id":"1000000000000000200","type":"video","width":640,"height":360}]
	]`)

	result := ParseDump(output, "source_user")
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.TweetID != "1000000000000000100" || item.SourceHandle != "source_user" || item.AuthorHandle != "parent_author" {
		t.Fatalf("identity = %#v", item)
	}
	if item.QuoteTweetID != "1000000000000000200" || item.QuoteAuthorHandle != "quote_author" || item.QuoteBodyText != "quoted text" {
		t.Fatalf("quote fields = %#v", item)
	}
	if len(item.Media) != 1 || item.Media[0].URL == "" {
		t.Fatalf("parent media = %#v", item.Media)
	}
	if len(item.QuoteMedia) != 1 || item.QuoteMedia[0].Type != "video" {
		t.Fatalf("quote media = %#v", item.QuoteMedia)
	}
}

func TestParseDumpKeepsSourceAuthoredQuoteExpansionAsDirectItem(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"9000000000000000100","content":"foreign quote","date":"2026-05-09 10:27:49","author":{"name":"sample_parent","nick":"Sample Parent"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":0,"reply_id":0,"retweet_id":0}],
		[2, {"tweet_id":"9000000000000000200","content":"source photo","date":"2026-05-09 09:00:00","quote_by":"sample_parent","author":{"name":"unknown","nick":"Sample Source"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":"9000000000000000100","reply_id":0,"retweet_id":0}],
		[3, "https://pbs.twimg.com/media/source-photo.jpg?format=jpg&name=orig", {"tweet_id":"9000000000000000200","type":"photo","width":1200,"height":800}]
	]`)

	result := ParseDump(output, "sample_source")
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	byID := make(map[string]FeedItem, len(result.Items))
	for _, item := range result.Items {
		byID[item.TweetID] = item
	}

	outer := byID["9000000000000000100"]
	if outer.QuoteTweetID != "9000000000000000200" || len(outer.QuoteMedia) != 1 {
		t.Fatalf("outer quote = %#v", outer)
	}
	source := byID["9000000000000000200"]
	if source.AuthorHandle != "sample_source" || source.SourceHandle != "sample_source" ||
		source.CanonicalURL != "https://x.com/sample_source/status/9000000000000000200" {
		t.Fatalf("source identity = %#v", source)
	}
	if len(source.Media) != 1 || source.Media[0].Type != "photo" {
		t.Fatalf("source media = %#v", source.Media)
	}
	if len(result.MissingQuoteParents) != 0 {
		t.Fatalf("missing quote parents = %#v", result.MissingQuoteParents)
	}
}

func TestParseDumpDoesNotUseRequestedSourceAsQuoteExpansionProvenance(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"9000000000000000300","content":"foreign quote","author":{"name":"sample_parent","nick":"Sample Parent"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":0,"reply_id":0,"retweet_id":0}],
		[2, {"tweet_id":"9000000000000000400","content":"unknown expansion","quote_by":"sample_parent","author":{"name":"unknown","nick":"Unknown"},"quote_id":"9000000000000000300","reply_id":0,"retweet_id":0}]
	]`)

	result := ParseDump(output, "sample_source")
	if len(result.Items) != 1 || result.Items[0].TweetID != "9000000000000000300" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestParseDumpDoesNotTreatRetweetQuoteExpansionAsSourcePost(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"9000000000000000500","content":"foreign quote","author":{"name":"sample_parent","nick":"Sample Parent"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":0,"reply_id":0,"retweet_id":0}],
		[2, {"tweet_id":"9000000000000000600","content":"RT @sample_source: source post","quote_by":"sample_parent","author":{"name":"sample_source","nick":"Sample Source"},"user":{"name":"sample_reposter","nick":"Sample Reposter"},"quote_id":"9000000000000000500","reply_id":0,"retweet_id":"9000000000000000700"}]
	]`)

	result := ParseDump(output, "sample_source")
	if len(result.Items) != 1 || result.Items[0].TweetID != "9000000000000000500" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestParseDumpRetweetKeepsWrapperAndStripsPrefix(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"1000000000000000300","retweet_id":"1000000000000000200","content":"RT @original_author: original text","date":"2026-05-09 10:00:00","lang":"en","author":{"name":"original_author","nick":"Original Author","profile_image":"https://pbs.twimg.com/profile_images/o_normal.jpg"},"user":{"name":"reposter","nick":"Reposter"},"quote_id":0,"reply_id":0}]
	]`)

	result := ParseDump(output, "reposter")
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if !item.IsRetweet || item.TweetID != "1000000000000000300" || item.CanonicalTweetID != "1000000000000000200" {
		t.Fatalf("retweet identity = %#v", item)
	}
	if item.BodyText != "original text" {
		t.Fatalf("body = %q", item.BodyText)
	}
	if item.RetweetedByHandle != "reposter" || item.CanonicalURL != "https://x.com/original_author/status/1000000000000000200" {
		t.Fatalf("retweet metadata = %#v", item)
	}
}

func TestParseDumpRequestsMissingQuoteParentStatus(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"1000000000000000200","content":"quoted text","date":"2026-05-09 09:00:00","quote_by":"source_user","quote_id":"1000000000000000100","author":{"name":"quote_author","nick":"Quote Author"},"user":{"name":"source_user","nick":"Source User"},"retweet_id":0}]
	]`)

	result := ParseDump(output, "source_user")
	if len(result.Items) != 0 {
		t.Fatalf("items = %d, want 0", len(result.Items))
	}
	if len(result.MissingQuoteParents) != 1 {
		t.Fatalf("missing parents = %#v", result.MissingQuoteParents)
	}
	if result.MissingQuoteParents[0] != (StatusRef{Handle: "source_user", TweetID: "1000000000000000100"}) {
		t.Fatalf("missing parent = %#v", result.MissingQuoteParents[0])
	}
}

func TestParseDumpUsesSourceHandleWhenDirectAuthorIsUnknownPlaceholder(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"9000000000000000600","content":"direct text","date":"2026-05-09 10:00:00","author":{"name":"unknown","nick":"Direct Author"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":0,"reply_id":0,"retweet_id":0}]
	]`)

	result := ParseDump(output, "")
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.AuthorHandle != "sample_source" {
		t.Fatalf("author_handle = %q, want sample_source", item.AuthorHandle)
	}
	if item.CanonicalURL != "https://x.com/sample_source/status/9000000000000000600" {
		t.Fatalf("canonical_url = %q", item.CanonicalURL)
	}
}

func TestParseDumpRejectsUntrustedMediaAndInvalidIDs(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"1000000000000000100","content":"safe text","date":"2026-05-09 10:00:00","author":{"name":"safe_author","nick":"Safe Author"},"user":{"name":"source_user"},"quote_id":0,"reply_id":0,"retweet_id":0}],
		[3, "http://127.0.0.1:8080/internal.jpg", {"tweet_id":"1000000000000000100","type":"photo"}],
		[2, {"tweet_id":"../bad","content":"bad","author":{"name":"safe_author"},"quote_id":0}]
	]`)

	result := ParseDump(output, "source_user")
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	if result.Items[0].MediaJSON != "" {
		t.Fatalf("media json = %q", result.Items[0].MediaJSON)
	}
}

func TestMediaRefRejectsOverflowDimensions(t *testing.T) {
	ref, ok := mediaRef("https://pbs.twimg.com/media/sample.jpg", map[string]any{
		"type":   "photo",
		"width":  "9223372036854775807",
		"height": int64(720),
	})
	if !ok {
		t.Fatal("mediaRef rejected valid media URL")
	}
	if ref.Width != 0 {
		t.Fatalf("Width = %d, want 0 for overflow", ref.Width)
	}
	if ref.Height != 720 {
		t.Fatalf("Height = %d, want 720", ref.Height)
	}
}

func TestClientFetchTimelineRetriesAllCookies(t *testing.T) {
	var used []string
	runner := func(_ context.Context, args []string) ([]byte, error) {
		for i, arg := range args {
			if arg == "--cookies" && i+1 < len(args) {
				used = append(used, args[i+1])
				break
			}
		}
		if len(used) < 3 {
			return nil, errors.New("HTTP 429: Too Many Requests")
		}
		return []byte(`[
			[2, {"tweet_id":"1000000000000000100","content":"cookie retry ok","author":{"name":"source_user","nick":"Source User"},"user":{"name":"source_user"},"quote_id":0,"reply_id":0,"retweet_id":0}]
		]`), nil
	}

	client := &Client{
		Runner:     runner,
		CookiePool: &CookiePool{paths: []string{"/tmp/cookie-a.txt", "/tmp/cookie-b.txt", "/tmp/cookie-c.txt"}},
	}
	items, err := client.FetchTimeline(context.Background(), "source_user", 1)
	if err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	if len(items) != 1 || items[0].BodyText != "cookie retry ok" {
		t.Fatalf("items = %#v", items)
	}
	if strings.Join(used, ",") != "/tmp/cookie-a.txt,/tmp/cookie-b.txt,/tmp/cookie-c.txt" {
		t.Fatalf("cookie attempts = %#v", used)
	}
}

type recordingOperationSink struct {
	ops []model.DownloaderOperation
}

func (s *recordingOperationSink) RecordDownloaderOperation(_ context.Context, op model.DownloaderOperation) error {
	s.ops = append(s.ops, op)
	return nil
}

func TestClientFetchTimelineRetriesCookieAfterSemanticAuthorizationError(t *testing.T) {
	var used []string
	runner := func(_ context.Context, args []string) ([]byte, error) {
		for i, arg := range args {
			if arg == "--cookies" && i+1 < len(args) {
				used = append(used, args[i+1])
				break
			}
		}
		if len(used) == 1 {
			return []byte(`[-1, {"error":"AuthorizationError","message":"Account temporarily locked"}]`), nil
		}
		return []byte(`[
			[2, {"tweet_id":"9000000000000000100","content":"second cookie worked","author":{"name":"sample_source","nick":"Sample Source"},"user":{"name":"sample_source"},"quote_id":0,"reply_id":0,"retweet_id":0}]
		]`), nil
	}
	sink := &recordingOperationSink{}
	client := &Client{
		Runner:        runner,
		CookiePool:    &CookiePool{paths: []string{"/tmp/cookie-a.txt", "/tmp/cookie-b.txt"}},
		OperationSink: sink,
	}

	items, err := client.FetchTimeline(context.Background(), "sample_source", 1)
	if err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	if len(items) != 1 || items[0].BodyText != "second cookie worked" {
		t.Fatalf("items = %#v", items)
	}
	if got := strings.Join(used, ","); got != "/tmp/cookie-a.txt,/tmp/cookie-b.txt" {
		t.Fatalf("cookie attempts = %q", got)
	}
	if len(sink.ops) != 2 || sink.ops[0].Status != "failure" || sink.ops[0].ErrorKind != "auth" || sink.ops[1].Status != "success" {
		t.Fatalf("operations = %#v", sink.ops)
	}
}

func TestClientFetchTimelineClassifiesSemanticAuthRequired(t *testing.T) {
	sink := &recordingOperationSink{}
	client := &Client{
		Runner: func(context.Context, []string) ([]byte, error) {
			return []byte(`[-1, {"error":"AuthRequired","message":"Sign in is required"}]`), nil
		},
		OperationSink: sink,
	}

	if _, err := client.FetchTimeline(context.Background(), "sample_source", 1); err == nil {
		t.Fatal("FetchTimeline succeeded after semantic auth failure")
	}
	if len(sink.ops) != 1 || sink.ops[0].Status != "failure" || sink.ops[0].ErrorKind != "auth" {
		t.Fatalf("operations = %#v", sink.ops)
	}
}

func TestClientFetchTimelineEnrichesMissingQuoteParentAndRetweetQuote(t *testing.T) {
	runner := func(_ context.Context, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/source_user/with_replies"):
			return []byte(`[
				[2, {"tweet_id":"1000000000000000200","content":"quoted text","date":"2026-05-09 09:00:00","quote_by":"source_user","quote_id":"1000000000000000100","author":{"name":"quote_author","nick":"Quote Author"},"user":{"name":"source_user","nick":"Source User"},"retweet_id":0}],
				[2, {"tweet_id":"1000000000000000400","retweet_id":"1000000000000000300","content":"RT @original_author: original quote parent","date":"2026-05-09 11:00:00","author":{"name":"original_author","nick":"Original Author"},"user":{"name":"source_user","nick":"Source User"},"quote_id":0,"reply_id":0}]
			]`), nil
		case strings.Contains(joined, "/source_user/status/1000000000000000100"):
			return []byte(`[
				[2, {"tweet_id":"1000000000000000100","content":"parent quote text","date":"2026-05-09 10:00:00","author":{"name":"source_user","nick":"Source User"},"user":{"name":"source_user","nick":"Source User"},"quote_id":0,"reply_id":0,"retweet_id":0}],
				[2, {"tweet_id":"1000000000000000200","content":"quoted text","date":"2026-05-09 09:00:00","author":{"name":"quote_author","nick":"Quote Author"},"user":{"name":"source_user"},"quote_id":"1000000000000000100","retweet_id":0}]
			]`), nil
		case strings.Contains(joined, "/original_author/status/1000000000000000300"):
			return []byte(`[
				[2, {"tweet_id":"1000000000000000300","content":"original quote parent","date":"2026-05-09 10:30:00","author":{"name":"original_author","nick":"Original Author"},"user":{"name":"original_author","nick":"Original Author"},"quote_id":0,"reply_id":0,"retweet_id":0}],
				[2, {"tweet_id":"1000000000000000500","content":"nested quoted text","date":"2026-05-09 10:00:00","author":{"name":"nested_quote","nick":"Nested Quote"},"user":{"name":"original_author"},"quote_id":"1000000000000000300","retweet_id":0}],
				[3, "https://pbs.twimg.com/media/nested.jpg?format=jpg&name=orig", {"tweet_id":"1000000000000000500","type":"photo"}]
			]`), nil
		default:
			t.Fatalf("unexpected gallery-dl args: %v", args)
			return nil, nil
		}
	}

	client := &Client{Runner: runner}
	items, err := client.FetchTimeline(context.Background(), "source_user", 2)
	if err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	byID := map[string]model.FeedItem{}
	for _, item := range items {
		byID[item.TweetID] = item
	}
	parent := byID["1000000000000000100"]
	if parent.QuoteTweetID != "1000000000000000200" || parent.QuoteBodyText != "quoted text" {
		b, _ := json.MarshalIndent(parent, "", "  ")
		t.Fatalf("missing quote parent not enriched: %s", b)
	}
	rt := byID["1000000000000000400"]
	if rt.QuoteTweetID != "1000000000000000500" || len(rt.QuoteMedia) != 1 {
		b, _ := json.MarshalIndent(rt, "", "  ")
		t.Fatalf("retweet quote not enriched: %s", b)
	}
}

func TestClientFetchTimelineUsesFXTwitterBeforeGalleryDLForMissingQuoteParent(t *testing.T) {
	runner := func(_ context.Context, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/sample_source/with_replies"):
			return []byte(`[
				[2, {"tweet_id":"9000000000000000200","content":"quoted text","date":"2026-05-09 09:00:00","quote_by":"sample_source","quote_id":"9000000000000000100","author":{"name":"sample_quote","nick":"Sample Quote"},"user":{"name":"sample_source","nick":"Sample Source"},"retweet_id":0}]
			]`), nil
		case strings.Contains(joined, "/status/"):
			t.Fatalf("status gallery-dl should not be called when fxtwitter supplies the parent: %v", args)
			return nil, nil
		default:
			t.Fatalf("unexpected gallery-dl args: %v", args)
			return nil, nil
		}
	}
	fallback := fakeTweetFallback{fetch: func(_ context.Context, handle, tweetID string) (*fxtwitter.Tweet, error) {
		if handle != "sample_source" || tweetID != "9000000000000000100" {
			t.Fatalf("fallback fetch = %s/%s", handle, tweetID)
		}
		return &fxtwitter.Tweet{
			ID:                "9000000000000000100",
			AuthorHandle:      "sample_source",
			AuthorDisplayName: "Sample Source",
			Text:              "parent quote text",
			Quote: &fxtwitter.Tweet{
				ID:                "9000000000000000200",
				AuthorHandle:      "sample_quote",
				AuthorDisplayName: "Sample Quote",
				Text:              "quoted text",
				MediaJSON:         `[{"url":"https://pbs.twimg.com/media/fallback-parent.jpg?name=orig","type":"photo"}]`,
			},
		}, nil
	}}

	var deferred []StatusEnrichmentRequest
	client := &Client{
		Runner:        runner,
		TweetFallback: fallback,
		StatusEnrichmentSink: func(req StatusEnrichmentRequest) {
			deferred = append(deferred, req)
		},
	}
	items, err := client.FetchTimeline(context.Background(), "sample_source", 1)
	if err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	if len(deferred) != 0 {
		t.Fatalf("deferred status enrichment = %#v", deferred)
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0]
	if item.TweetID != "9000000000000000100" || item.QuoteTweetID != "9000000000000000200" {
		b, _ := json.MarshalIndent(item, "", "  ")
		t.Fatalf("missing parent not enriched from fxtwitter: %s", b)
	}
	if len(item.QuoteMedia) != 1 || item.QuoteMedia[0].URL == "" {
		t.Fatalf("quote media = %#v", item.QuoteMedia)
	}
}

func TestClientFetchTimelineUsesFXTwitterBeforeGalleryDLForRetweetQuote(t *testing.T) {
	runner := func(_ context.Context, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/sample_source/with_replies"):
			return []byte(`[
				[2, {"tweet_id":"9000000000000000400","retweet_id":"9000000000000000300","content":"RT @sample_author: original parent text","author":{"name":"sample_author","nick":"Sample Author"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":0,"reply_id":0}]
			]`), nil
		case strings.Contains(joined, "/status/"):
			t.Fatalf("status gallery-dl should not be called when fxtwitter supplies the retweet quote: %v", args)
			return nil, nil
		default:
			t.Fatalf("unexpected gallery-dl args: %v", args)
			return nil, nil
		}
	}
	fallback := fakeTweetFallback{fetch: func(_ context.Context, handle, tweetID string) (*fxtwitter.Tweet, error) {
		if handle != "sample_author" || tweetID != "9000000000000000300" {
			t.Fatalf("fallback fetch = %s/%s", handle, tweetID)
		}
		return &fxtwitter.Tweet{
			ID:                "9000000000000000300",
			AuthorHandle:      "sample_author",
			AuthorDisplayName: "Sample Author",
			Text:              "original parent text",
			Quote: &fxtwitter.Tweet{
				ID:                "9000000000000000500",
				AuthorHandle:      "sample_quote",
				AuthorDisplayName: "Sample Quote",
				Text:              "quote fallback text",
				MediaJSON:         `[{"url":"https://pbs.twimg.com/media/fallback.jpg?name=orig","type":"photo"}]`,
			},
		}, nil
	}}

	var deferred []StatusEnrichmentRequest
	client := &Client{
		Runner:        runner,
		TweetFallback: fallback,
		StatusEnrichmentSink: func(req StatusEnrichmentRequest) {
			deferred = append(deferred, req)
		},
	}
	items, err := client.FetchTimeline(context.Background(), "sample_source", 1)
	if err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	if len(deferred) != 0 {
		t.Fatalf("deferred status enrichment = %#v", deferred)
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0]
	if item.QuoteTweetID != "9000000000000000500" || item.QuoteBodyText != "quote fallback text" {
		b, _ := json.MarshalIndent(item, "", "  ")
		t.Fatalf("quote fallback missing: %s", b)
	}
	if len(item.QuoteMedia) != 1 || item.QuoteMedia[0].URL == "" {
		t.Fatalf("quote media = %#v", item.QuoteMedia)
	}
}

func TestClientFetchTimelineDefersMissingQuoteParentWhenFXTwitterLacksQuote(t *testing.T) {
	runner := func(_ context.Context, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/sample_source/with_replies"):
			return []byte(`[
				[2, {"tweet_id":"9000000000000000200","content":"quoted text","date":"2026-05-09 09:00:00","quote_by":"sample_source","quote_id":"9000000000000000100","author":{"name":"sample_quote","nick":"Sample Quote"},"user":{"name":"sample_source","nick":"Sample Source"},"retweet_id":0}]
			]`), nil
		case strings.Contains(joined, "/status/"):
			t.Fatalf("status gallery-dl should not be called before deferred enrichment: %v", args)
			return nil, nil
		default:
			t.Fatalf("unexpected gallery-dl args: %v", args)
			return nil, nil
		}
	}
	fallback := fakeTweetFallback{fetch: func(_ context.Context, handle, tweetID string) (*fxtwitter.Tweet, error) {
		if handle != "sample_source" || tweetID != "9000000000000000100" {
			t.Fatalf("fallback fetch = %s/%s", handle, tweetID)
		}
		return &fxtwitter.Tweet{
			ID:                "9000000000000000100",
			AuthorHandle:      "sample_source",
			AuthorDisplayName: "Sample Source",
			Text:              "parent without quote",
		}, nil
	}}

	var deferred []StatusEnrichmentRequest
	client := &Client{
		Runner:        runner,
		TweetFallback: fallback,
		StatusEnrichmentSink: func(req StatusEnrichmentRequest) {
			deferred = append(deferred, req)
		},
	}
	if _, err := client.FetchTimeline(context.Background(), "sample_source", 1); err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	if len(deferred) != 1 ||
		deferred[0].Kind != StatusEnrichmentMissingQuoteParent ||
		deferred[0].Ref.Handle != "sample_source" ||
		deferred[0].Ref.TweetID != "9000000000000000100" {
		t.Fatalf("deferred status enrichment = %#v", deferred)
	}
}

func TestClientFetchTimelineDefersRetweetQuoteWhenFXTwitterFails(t *testing.T) {
	runner := func(_ context.Context, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/sample_source/with_replies"):
			return []byte(`[
				[2, {"tweet_id":"9000000000000000400","retweet_id":"9000000000000000300","content":"RT @sample_author: original parent text","author":{"name":"sample_author","nick":"Sample Author"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":0,"reply_id":0}]
			]`), nil
		case strings.Contains(joined, "/status/"):
			t.Fatalf("status gallery-dl should not be called before deferred enrichment: %v", args)
			return nil, nil
		default:
			t.Fatalf("unexpected gallery-dl args: %v", args)
			return nil, nil
		}
	}
	fallback := fakeTweetFallback{fetch: func(context.Context, string, string) (*fxtwitter.Tweet, error) {
		return nil, errors.New("temporary fxtwitter failure")
	}}

	var deferred []StatusEnrichmentRequest
	client := &Client{
		Runner:        runner,
		TweetFallback: fallback,
		StatusEnrichmentSink: func(req StatusEnrichmentRequest) {
			deferred = append(deferred, req)
		},
	}
	if _, err := client.FetchTimeline(context.Background(), "sample_source", 1); err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	if len(deferred) != 1 ||
		deferred[0].Kind != StatusEnrichmentRetweetQuote ||
		deferred[0].Ref.Handle != "sample_author" ||
		deferred[0].Ref.TweetID != "9000000000000000300" ||
		deferred[0].TargetTweetID != "9000000000000000400" {
		t.Fatalf("deferred status enrichment = %#v", deferred)
	}
}

func TestClientFetchTimelineDoesNotDeferPlainRetweetAfterFXTwitter(t *testing.T) {
	runner := func(_ context.Context, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/sample_source/with_replies"):
			return []byte(`[
				[2, {"tweet_id":"9000000000000000400","retweet_id":"9000000000000000300","content":"RT @sample_author: original parent text","author":{"name":"sample_author","nick":"Sample Author"},"user":{"name":"sample_source","nick":"Sample Source"},"quote_id":0,"reply_id":0}]
			]`), nil
		case strings.Contains(joined, "/status/"):
			t.Fatalf("status gallery-dl should not be called after fxtwitter returns a plain retweet: %v", args)
			return nil, nil
		default:
			t.Fatalf("unexpected gallery-dl args: %v", args)
			return nil, nil
		}
	}
	fallback := fakeTweetFallback{fetch: func(_ context.Context, handle, tweetID string) (*fxtwitter.Tweet, error) {
		if handle != "sample_author" || tweetID != "9000000000000000300" {
			t.Fatalf("fallback fetch = %s/%s", handle, tweetID)
		}
		return &fxtwitter.Tweet{
			ID:                "9000000000000000300",
			AuthorHandle:      "sample_author",
			AuthorDisplayName: "Sample Author",
			Text:              "original parent text",
		}, nil
	}}

	var deferred []StatusEnrichmentRequest
	client := &Client{
		Runner:        runner,
		TweetFallback: fallback,
		StatusEnrichmentSink: func(req StatusEnrichmentRequest) {
			deferred = append(deferred, req)
		},
	}
	items, err := client.FetchTimeline(context.Background(), "sample_source", 1)
	if err != nil {
		t.Fatalf("FetchTimeline: %v", err)
	}
	if len(deferred) != 0 {
		t.Fatalf("deferred status enrichment = %#v", deferred)
	}
	if len(items) != 1 || items[0].QuoteTweetID != "" {
		t.Fatalf("items = %#v", items)
	}
}
