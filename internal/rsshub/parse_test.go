package rsshub

import (
	"strings"
	"testing"
	"time"
)

const basicRSSXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <item>
      <guid>https://x.com/user_a/status/1000000000000000001</guid>
      <title>First tweet</title>
      <link>https://x.com/user_a/status/1000000000000000001</link>
      <description><![CDATA[<p>Hello world</p>]]></description>
      <pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
      <enclosure url="https://pbs.twimg.com/media/abc123.jpg" type="image/jpeg" length="12345"/>
    </item>
    <item>
      <guid>https://x.com/user_b/status/1000000000000000002</guid>
      <title>Second tweet</title>
      <link>https://x.com/user_b/status/1000000000000000002</link>
      <description><![CDATA[<p>Another tweet</p>]]></description>
      <pubDate>Tue, 02 Jan 2024 08:30:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestParseBasicRSS(t *testing.T) {
	feed, err := Parse(strings.NewReader(basicRSSXML))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if feed.Title != "Test Feed" {
		t.Errorf("feed title: got %q, want %q", feed.Title, "Test Feed")
	}
	if len(feed.Items) != 2 {
		t.Fatalf("item count: got %d, want 2", len(feed.Items))
	}

	item0 := feed.Items[0]
	if item0.Title != "First tweet" {
		t.Errorf("item[0].Title: got %q", item0.Title)
	}
	if item0.Link != "https://x.com/user_a/status/1000000000000000001" {
		t.Errorf("item[0].Link: got %q", item0.Link)
	}
	wantTime0, _ := time.Parse(time.RFC1123Z, "Mon, 01 Jan 2024 12:00:00 +0000")
	if !item0.PubDate.Equal(wantTime0.UTC()) {
		t.Errorf("item[0].PubDate: got %v, want %v", item0.PubDate, wantTime0.UTC())
	}
	if len(item0.Enclosures) != 1 {
		t.Fatalf("item[0] enclosure count: got %d, want 1", len(item0.Enclosures))
	}
	enc := item0.Enclosures[0]
	if enc.URL != "https://pbs.twimg.com/media/abc123.jpg" {
		t.Errorf("enclosure URL: got %q", enc.URL)
	}
	if enc.Type != "image/jpeg" {
		t.Errorf("enclosure Type: got %q", enc.Type)
	}
	if enc.Length != 12345 {
		t.Errorf("enclosure Length: got %d", enc.Length)
	}

	item1 := feed.Items[1]
	if item1.Title != "Second tweet" {
		t.Errorf("item[1].Title: got %q", item1.Title)
	}
	wantTime1, _ := time.Parse(time.RFC1123Z, "Tue, 02 Jan 2024 08:30:00 +0000")
	if !item1.PubDate.Equal(wantTime1.UTC()) {
		t.Errorf("item[1].PubDate: got %v, want %v", item1.PubDate, wantTime1.UTC())
	}
}

const emptyRSSXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Empty Feed</title>
  </channel>
</rss>`

func TestParseEmptyFeed(t *testing.T) {
	feed, err := Parse(strings.NewReader(emptyRSSXML))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if feed.Title != "Empty Feed" {
		t.Errorf("feed title: got %q", feed.Title)
	}
	if len(feed.Items) != 0 {
		t.Errorf("item count: got %d, want 0", len(feed.Items))
	}
}
