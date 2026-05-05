package download

import (
	"encoding/json"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestParseTwitterSourceDumpKeepsRealAuthorSource(t *testing.T) {
	output := []byte(`{"tweet_id":"111","author":{"name":"Alice Example","nick":"alice","profile_image":"https://pbs.twimg.com/avatar.jpg"},"content":"hello from a list","date":"2026-04-30 12:00:00","url":"https://x.com/alice/status/111","media":[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"}],"favorite_count":7,"retweet_count":2}`)

	items := ParseTwitterSourceDump(output, "twitter_list_123")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	item := items[0]
	if item.TweetID != "111" || item.AuthorHandle != "alice" || item.SourceHandle != "alice" {
		t.Fatalf("item identity = %#v", item)
	}
	if item.BodyText != "hello from a list" || item.CanonicalURL != "https://x.com/alice/status/111" {
		t.Fatalf("item body/url = %#v", item)
	}
	if item.MediaJSON == "" {
		t.Fatal("expected media json")
	}
}

func TestParseTwitterSourceDumpMergesGalleryDLSidecarMedia(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"222","author":{"name":"Example Author","nick":"example"},"content":"hello with sidecar media","date":"2026-04-30 12:00:00","url":"https://x.com/example/status/222"}],
		[3, "https://pbs.twimg.com/media/sidecar.jpg?format=jpg&name=orig", {"tweet_id":"222","author":{"name":"Example Author","nick":"example"},"content":"hello with sidecar media","date":"2026-04-30 12:00:00","type":"photo","width":1200,"height":800}]
	]`)

	items := ParseTwitterSourceDump(output, "twitter_list_123")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].MediaJSON == "" {
		t.Fatal("expected merged sidecar media json")
	}
	var media []model.MediaRef
	if err := json.Unmarshal([]byte(items[0].MediaJSON), &media); err != nil {
		t.Fatalf("unmarshal media: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("len(media) = %d", len(media))
	}
	if media[0].URL != "https://pbs.twimg.com/media/sidecar.jpg?format=jpg&name=orig" {
		t.Fatalf("media url = %q", media[0].URL)
	}
	if media[0].Width != 1200 || media[0].Height != 800 {
		t.Fatalf("media dimensions = %#v", media[0])
	}
}

func TestParseTwitterSourceDumpIgnoresUntrustedGalleryDLSidecarMediaURL(t *testing.T) {
	output := []byte(`[
		[2, {"tweet_id":"333","author":{"name":"Example Author","nick":"example"},"content":"hello with bad sidecar media","date":"2026-04-30 12:00:00","url":"https://x.com/example/status/333"}],
		[3, "http://127.0.0.1:9999/internal.jpg", {"tweet_id":"333","author":{"name":"Example Author","nick":"example"},"content":"hello with bad sidecar media","date":"2026-04-30 12:00:00","type":"photo"}]
	]`)

	items := ParseTwitterSourceDump(output, "twitter_list_123")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].MediaJSON != "" {
		t.Fatalf("media json = %q", items[0].MediaJSON)
	}
}

func TestParseTwitterSourceDumpRejectsInvalidHandleAndTweetID(t *testing.T) {
	output := []byte(`{"tweet_id":"../bad","author":{"name":"../../../escape"},"content":"bad ids","media":[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"}]}`)

	items := ParseTwitterSourceDump(output, "twitter_list_123")
	if len(items) != 0 {
		t.Fatalf("len(items) = %d", len(items))
	}
}
