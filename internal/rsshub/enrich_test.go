package rsshub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// buildItem is a helper to construct an RSSItem from description HTML and optional link/pubdate.
func buildItem(link, desc string, pubDate time.Time) RSSItem {
	return RSSItem{
		GUID:        link,
		Title:       "tweet",
		Link:        link,
		Description: desc,
		PubDate:     pubDate,
	}
}

// basicTweetDesc is a typical enriched description for a plain tweet.
// RSSHub enriched format: img has src then hspace="8" (RSSHub's actual attribute order).
const basicTweetDesc = `<a href="https://x.com/user_a">` +
	`<img src="https://pbs.twimg.com/profile_images/111/photo.jpg" hspace="8" />` +
	`<strong>User Alpha</strong></a>: Hello world, this is a test tweet.` +
	`<hr/><small>Posted via RSSHub</small>`

func TestEnrichBasicTweet(t *testing.T) {
	link := "https://x.com/user_a/status/1000000000000000001"
	pub, _ := time.Parse(time.RFC1123Z, "Mon, 01 Jan 2024 12:00:00 +0000")
	raw := buildItem(link, basicTweetDesc, pub.UTC())

	feed := &RSSFeed{Title: "user_a feed", Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_a")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if fi.TweetID != "1000000000000000001" {
		t.Errorf("TweetID: got %q, want %q", fi.TweetID, "1000000000000000001")
	}
	if fi.SourceHandle != "user_a" {
		t.Errorf("SourceHandle: got %q", fi.SourceHandle)
	}
	if fi.AuthorHandle != "user_a" {
		t.Errorf("AuthorHandle: got %q", fi.AuthorHandle)
	}
	if fi.AuthorDisplayName != "User Alpha" {
		t.Errorf("AuthorDisplayName: got %q", fi.AuthorDisplayName)
	}
	// Avatar URL preserved as original twimg URL (rewrite to local proxy happens at render time)
	if fi.AuthorAvatarURL != "https://pbs.twimg.com/profile_images/111/photo.jpg" {
		t.Errorf("AuthorAvatarURL: got %q", fi.AuthorAvatarURL)
	}
	if !strings.Contains(fi.BodyText, "Hello world") {
		t.Errorf("BodyText doesn't contain expected text: %q", fi.BodyText)
	}
	if fi.IsRetweet {
		t.Error("IsRetweet should be false")
	}
	if fi.PublishedAt == nil || fi.PublishedAt.IsZero() {
		t.Error("PublishedAt should be set")
	}
	if len(fi.ContentHash) != 16 {
		t.Errorf("ContentHash length: got %d, want 16", len(fi.ContentHash))
	}
}

// retweetDesc is an enriched description with the 🔁 RT indicator.
// The RT <small> block names the retweeter (user_b), and the author block names the original author (user_a).
const retweetDesc = `<small><a href="https://x.com/user_b"><strong>User Beta</strong></a> 🔁</small>` +
	`<a href="https://x.com/user_a">` +
	`<img src="https://pbs.twimg.com/profile_images/222/photo.jpg" hspace="8" />` +
	`<strong>User Alpha</strong></a>: Original tweet content here.` +
	`<hr/><small>Posted via RSSHub</small>`

func TestEnrichRetweet(t *testing.T) {
	// The RSS link points to the retweeter's URL (user_b retweets user_a's tweet).
	link := "https://x.com/user_b/status/1000000000000000002"
	pub, _ := time.Parse(time.RFC1123Z, "Tue, 02 Jan 2024 10:00:00 +0000")
	raw := buildItem(link, retweetDesc, pub.UTC())

	feed := &RSSFeed{Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_b")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if !fi.IsRetweet {
		t.Error("IsRetweet should be true")
	}
	// Author should be the original author (user_a)
	if fi.AuthorHandle != "user_a" {
		t.Errorf("AuthorHandle (original): got %q, want %q", fi.AuthorHandle, "user_a")
	}
	// RetweetedBy should be user_b
	if fi.RetweetedByHandle != "user_b" {
		t.Errorf("RetweetedByHandle: got %q, want %q", fi.RetweetedByHandle, "user_b")
	}
	if fi.RetweetedByDisplayName != "User Beta" {
		t.Errorf("RetweetedByDisplayName: got %q", fi.RetweetedByDisplayName)
	}
	// Canonical URL rewritten to original author
	wantURL := "https://x.com/user_a/status/1000000000000000002"
	if fi.CanonicalURL != wantURL {
		t.Errorf("CanonicalURL: got %q, want %q", fi.CanonicalURL, wantURL)
	}
}

// selfRetweetDesc has no 🔁 block — only an "RT @handle:" prefix in the body.
// This happens when RSSHub omits the RT indicator for self-retweets.
const selfRetweetDesc = `<a href="https://x.com/user_d">` +
	`<img src="https://pbs.twimg.com/profile_images/444/photo.jpg" hspace="8" />` +
	`<strong>User Delta</strong></a>: RT @user_d: Self-retweeted content here.`

func TestEnrichSelfRetweet(t *testing.T) {
	link := "https://x.com/user_d/status/1000000000000000004"
	raw := buildItem(link, selfRetweetDesc, time.Now())

	feed := &RSSFeed{Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_d")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if !fi.IsRetweet {
		t.Error("IsRetweet should be true")
	}
	if fi.AuthorHandle != "user_d" {
		t.Errorf("AuthorHandle: got %q, want %q", fi.AuthorHandle, "user_d")
	}
	if fi.RetweetedByHandle != "user_d" {
		t.Errorf("RetweetedByHandle: got %q, want %q", fi.RetweetedByHandle, "user_d")
	}
	if fi.RetweetedByDisplayName != "User Delta" {
		t.Errorf("RetweetedByDisplayName: got %q, want %q", fi.RetweetedByDisplayName, "User Delta")
	}
	if fi.BodyText != "Self-retweeted content here." {
		t.Errorf("BodyText: got %q", fi.BodyText)
	}
}

// mediaDesc has a photo and a video attachment.
const mediaDesc = `<a href="https://x.com/user_c">` +
	`<img hspace="8" src="https://pbs.twimg.com/profile_images/333/photo.jpg" />` +
	`<strong>User Gamma</strong></a>: Check out this media!` +
	`<br/><a href="https://pbs.twimg.com/media/PHOTO1.jpg"><img src="https://pbs.twimg.com/media/PHOTO1.jpg"/></a>` +
	`<br/><video src="https://video.twimg.com/ext_tw_video/123/pu/vid/720x1280/video.mp4"></video>`

func TestEnrichWithMedia(t *testing.T) {
	link := "https://x.com/user_c/status/1000000000000000003"
	raw := buildItem(link, mediaDesc, time.Now())

	feed := &RSSFeed{Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_c")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if fi.MediaJSON == "" {
		t.Fatal("MediaJSON should be populated")
	}
	var refs []model.MediaRef
	if err := json.Unmarshal([]byte(fi.MediaJSON), &refs); err != nil {
		t.Fatalf("MediaJSON unmarshal: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("media count: got %d, want 2", len(refs))
	}
	if refs[0].Type != "photo" {
		t.Errorf("refs[0].Type: got %q, want photo", refs[0].Type)
	}
	if !strings.Contains(refs[0].URL, "pbs.twimg.com/media") {
		t.Errorf("refs[0].URL unexpected: %q", refs[0].URL)
	}
	if refs[1].Type != "video" {
		t.Errorf("refs[1].Type: got %q, want video", refs[1].Type)
	}
	if !strings.Contains(refs[1].URL, "video.twimg.com") {
		t.Errorf("refs[1].URL unexpected: %q", refs[1].URL)
	}
}

// quoteTweetDesc has an embedded rsshub-quote div.
const quoteTweetDesc = `<a href="https://x.com/user_d">` +
	`<img hspace="8" src="https://pbs.twimg.com/profile_images/444/photo.jpg" />` +
	`<strong>User Delta</strong></a>: My take on this.` +
	`<div class="rsshub-quote">` +
	`<a href="https://x.com/user_e">` +
	`<img src="https://pbs.twimg.com/profile_images/555/photo.jpg"/>` +
	`<strong>User Epsilon</strong></a>: ` +
	`<a href="https://x.com/user_e/status/9000000000000000001">link</a>` +
	`The quoted tweet body here.` +
	`<small>Link: https://x.com/user_e/status/9000000000000000001</small>` +
	`</div>`

func TestEnrichQuoteTweet(t *testing.T) {
	link := "https://x.com/user_d/status/1000000000000000004"
	raw := buildItem(link, quoteTweetDesc, time.Now())

	feed := &RSSFeed{Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_d")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if fi.QuoteTweetID != "9000000000000000001" {
		t.Errorf("QuoteTweetID: got %q, want %q", fi.QuoteTweetID, "9000000000000000001")
	}
	if fi.QuoteAuthorHandle != "user_e" {
		t.Errorf("QuoteAuthorHandle: got %q", fi.QuoteAuthorHandle)
	}
	if fi.QuoteAuthorDisplayName != "User Epsilon" {
		t.Errorf("QuoteAuthorDisplayName: got %q", fi.QuoteAuthorDisplayName)
	}
	if !strings.Contains(fi.QuoteBodyText, "quoted tweet body") {
		t.Errorf("QuoteBodyText: got %q", fi.QuoteBodyText)
	}
	// Author of parent tweet
	if fi.AuthorHandle != "user_d" {
		t.Errorf("AuthorHandle: got %q", fi.AuthorHandle)
	}
	// Parent body should not contain quote block content
	if strings.Contains(fi.BodyText, "quoted tweet body") {
		t.Errorf("BodyText should not contain quote content: %q", fi.BodyText)
	}
}

func TestEnrichDropsStatusUndefinedAvatarURLs(t *testing.T) {
	desc := `<a href="https://x.com/user_bad">` +
		`<img src="https://x.com/user_bad/status/undefined" hspace="8" />` +
		`<strong>User Bad</strong></a>: parent text.` +
		`<div class="rsshub-quote">` +
		`<a href="https://x.com/quote_bad">` +
		`<img src="https://x.com/quote_bad/status/undefined"/>` +
		`<strong>Quote Bad</strong></a>: ` +
		`<a href="https://x.com/quote_bad/status/9000000000000000002">link</a>` +
		`quoted text` +
		`<small>Link: https://x.com/quote_bad/status/9000000000000000002</small>` +
		`</div>`
	raw := buildItem("https://x.com/user_bad/status/1000000000000000099", desc, time.Now())

	items := ToFeedItems(&RSSFeed{Items: []RSSItem{raw}}, "user_bad")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].AuthorAvatarURL != "" {
		t.Fatalf("bad author avatar survived: %q", items[0].AuthorAvatarURL)
	}
	if items[0].QuoteAuthorAvatarURL != "" {
		t.Fatalf("bad quote avatar survived: %q", items[0].QuoteAuthorAvatarURL)
	}
	if items[0].QuoteAuthorHandle != "quote_bad" {
		t.Fatalf("quote handle should still be preserved, got %q", items[0].QuoteAuthorHandle)
	}
}

// quoteTweetLeadingWhitespace reproduces the bug where leading whitespace
// inside the rsshub-quote div caused the author img attributes to leak into
// quote_body_text (e.g. "24" src="https://pbs.twimg.com/...").
const quoteTweetLeadingWhitespace = `<a href="https://x.com/user_f">` +
	`<img src="https://pbs.twimg.com/profile_images/666/photo.jpg" hspace="8" />` +
	`<strong>User Foxtrot</strong></a>: My comment on this.` +
	`<div class="rsshub-quote">` +
	"\n  " +
	`<a href="https://x.com/user_g">` +
	`<img height="24" src="https://pbs.twimg.com/profile_images/777/photo.jpg" hspace="8" vspace="8" align="left" referrerpolicy="no-referrer"/>` +
	`<strong>User Golf</strong></a>: example quote content here` +
	`<small>Link: https://x.com/user_g/status/8000000000000000001</small>` +
	`</div>`

func TestEnrichQuoteTweetLeadingWhitespace(t *testing.T) {
	link := "https://x.com/user_f/status/1000000000000000005"
	raw := buildItem(link, quoteTweetLeadingWhitespace, time.Now())
	feed := &RSSFeed{Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_f")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if fi.QuoteAuthorHandle != "user_g" {
		t.Errorf("QuoteAuthorHandle: got %q, want %q", fi.QuoteAuthorHandle, "user_g")
	}
	if fi.QuoteAuthorDisplayName != "User Golf" {
		t.Errorf("QuoteAuthorDisplayName: got %q", fi.QuoteAuthorDisplayName)
	}
	// The key assertion: body must NOT contain raw HTML attributes
	if strings.Contains(fi.QuoteBodyText, "twimg.com") {
		t.Errorf("QuoteBodyText contains raw HTML (twimg URL leak): %q", fi.QuoteBodyText)
	}
	if strings.Contains(fi.QuoteBodyText, `"24"`) {
		t.Errorf("QuoteBodyText contains raw img attribute: %q", fi.QuoteBodyText)
	}
	if !strings.Contains(fi.QuoteBodyText, "example quote content") {
		t.Errorf("QuoteBodyText missing expected text: %q", fi.QuoteBodyText)
	}
}

func TestContentHash(t *testing.T) {
	h1 := contentHash("user_a", "hello world", nil)
	h2 := contentHash("user_a", "hello world", nil)
	if h1 != h2 {
		t.Error("same inputs should produce same hash")
	}
	if len(h1) != 16 {
		t.Errorf("hash length: got %d, want 16", len(h1))
	}

	h3 := contentHash("user_b", "hello world", nil)
	if h1 == h3 {
		t.Error("different author should produce different hash")
	}

	// Whitespace normalization: extra spaces should produce same hash
	h4 := contentHash("user_a", "hello   world", nil)
	if h1 != h4 {
		t.Errorf("whitespace should be normalized: %q != %q", h1, h4)
	}

	// Media-only posts by the same author with empty body but different
	// media should produce different hashes (the bug this guards against:
	// liking one media-only post marked every other empty-body post by the
	// same author as liked).
	mA := []model.MediaRef{{URL: "https://pbs.twimg.com/media/AAA111?format=jpg&name=small", Type: "photo"}}
	mB := []model.MediaRef{{URL: "https://pbs.twimg.com/media/BBB222?format=jpg&name=small", Type: "photo"}}
	h5 := contentHash("user_a", "", mA)
	h6 := contentHash("user_a", "", mB)
	if h5 == h6 {
		t.Error("empty body + different media should produce different hashes")
	}

	// Same author + same body + same underlying media ID should still
	// group retweets with originals. Query string differences (format/name)
	// must not affect the hash — only the stable path matters.
	mAPrime := []model.MediaRef{{URL: "https://pbs.twimg.com/media/AAA111?format=png&name=orig", Type: "photo"}}
	h7 := contentHash("user_a", "hello world", mA)
	h8 := contentHash("user_a", "hello world", mAPrime)
	if h7 != h8 {
		t.Errorf("query-string variations on same media should not change hash: %q != %q", h7, h8)
	}
	if h7 == h1 {
		t.Error("adding media should change hash vs. text-only input")
	}
}

// replyDesc is an enriched description with the ↩️ reply indicator at the
// start of the body. The ↩ codepoint is U+21A9 followed by U+FE0F variation
// selector — RSSHub emits both.
const replyDesc = `<a href="https://x.com/user_a">` +
	`<img src="https://pbs.twimg.com/profile_images/111/photo.jpg" hspace="8" />` +
	`<strong>User Alpha</strong></a>: ↩️ @user_b Replying to user_b's tweet here.` +
	`<hr/><small>Posted via RSSHub</small>`

func TestEnrichReply(t *testing.T) {
	link := "https://x.com/user_a/status/1000000000000000010"
	pub, _ := time.Parse(time.RFC1123Z, "Mon, 01 Jan 2024 12:00:00 +0000")
	raw := buildItem(link, replyDesc, pub.UTC())

	feed := &RSSFeed{Title: "user_a feed", Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_a")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if !fi.IsReply {
		t.Error("IsReply should be true")
	}
	if fi.ReplyToHandle != "user_b" {
		t.Errorf("ReplyToHandle: got %q, want %q", fi.ReplyToHandle, "user_b")
	}
	if fi.ReplyToStatus != "" {
		t.Errorf("ReplyToStatus should be empty at parse time (filled by fxtwitter resolver), got %q", fi.ReplyToStatus)
	}
	if strings.Contains(fi.BodyText, "↩") {
		t.Errorf("BodyText should not contain ↩ marker: %q", fi.BodyText)
	}
	if strings.HasPrefix(fi.BodyText, "@user_b") {
		t.Errorf("BodyText should not begin with the parent mention: %q", fi.BodyText)
	}
	if !strings.Contains(fi.BodyText, "Replying to user_b's tweet") {
		t.Errorf("BodyText missing actual content: %q", fi.BodyText)
	}
}

const replyWithoutHandleDesc = `<a href="https://x.com/user_a">` +
	`<img src="https://pbs.twimg.com/profile_images/111/photo.jpg" hspace="8" />` +
	`<strong>User Alpha</strong></a>: ↩️ Absolutely not, where would you ever get that idea` +
	`<hr/><small>Posted via RSSHub</small>`

func TestEnrichReplyWithoutHandlePreservesText(t *testing.T) {
	link := "https://x.com/user_a/status/1000000000000000011"
	pub, _ := time.Parse(time.RFC1123Z, "Mon, 01 Jan 2024 12:00:00 +0000")
	raw := buildItem(link, replyWithoutHandleDesc, pub.UTC())

	feed := &RSSFeed{Title: "user_a feed", Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_a")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]
	if !fi.IsReply {
		t.Error("IsReply should be true")
	}
	if fi.ReplyToHandle != "" {
		t.Errorf("ReplyToHandle should be empty when RSSHub omits @handle, got %q", fi.ReplyToHandle)
	}
	if strings.HasPrefix(fi.BodyText, "↩") {
		t.Errorf("BodyText should not contain the reply marker: %q", fi.BodyText)
	}
	if !strings.HasPrefix(fi.BodyText, "Absolutely not") {
		t.Errorf("BodyText should preserve text after a bare reply marker, got %q", fi.BodyText)
	}
}

// rtOfReplyDesc: a retweet of a reply. The 🔁 RT block names the retweeter,
// the author block names the original author, and the body opens with ↩️ @x
// indicating the original tweet was a reply.
const rtOfReplyDesc = `<small><a href="https://x.com/user_b"><strong>User Beta</strong></a> 🔁</small>` +
	`<a href="https://x.com/user_a">` +
	`<img src="https://pbs.twimg.com/profile_images/222/photo.jpg" hspace="8" />` +
	`<strong>User Alpha</strong></a>: ↩️ @user_c original reply content.` +
	`<hr/><small>Posted via RSSHub</small>`

func TestEnrichRetweetOfReply(t *testing.T) {
	link := "https://x.com/user_a/status/1000000000000000020"
	pub, _ := time.Parse(time.RFC1123Z, "Mon, 01 Jan 2024 12:00:00 +0000")
	raw := buildItem(link, rtOfReplyDesc, pub.UTC())

	feed := &RSSFeed{Title: "user_b feed", Items: []RSSItem{raw}}
	items := ToFeedItems(feed, "user_b")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	fi := items[0]

	if !fi.IsRetweet {
		t.Error("IsRetweet should be true")
	}
	if !fi.IsReply {
		t.Error("IsReply should be true (the retweeted tweet was a reply)")
	}
	if fi.ReplyToHandle != "user_c" {
		t.Errorf("ReplyToHandle: got %q, want user_c", fi.ReplyToHandle)
	}
	if fi.AuthorHandle != "user_a" {
		t.Errorf("AuthorHandle should be original author user_a, got %q", fi.AuthorHandle)
	}
	if fi.RetweetedByHandle != "user_b" {
		t.Errorf("RetweetedByHandle should be retweeter user_b, got %q", fi.RetweetedByHandle)
	}
}

func TestDetectLang(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"", ""},
		{"hi", ""}, // too short
		{"This is a fairly long English sentence for detection purposes", "en"},
		{"これは日本語のテストです。十分な長さが必要です。", "ja"},
		{"#golang @user_a check this out", "en"}, // hashtags/mentions stripped
		{"#日本語 #タグ", ""},                         // only hashtags, nothing left
	}
	for _, tt := range tests {
		got := DetectLang(tt.text)
		if got != tt.want {
			t.Errorf("DetectLang(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}
