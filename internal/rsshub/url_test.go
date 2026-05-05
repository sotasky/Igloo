package rsshub

import (
	"strings"
	"testing"
)

func TestBuildEnrichedURLIncludesReplies(t *testing.T) {
	url := BuildEnrichedURL("http://localhost:1200", "user_alpha")
	if !strings.Contains(url, "include_replies=1") {
		t.Errorf("URL should include include_replies=1: %s", url)
	}
	if !strings.Contains(url, "include_rts=1") {
		t.Errorf("URL should include include_rts=1: %s", url)
	}
	if strings.Contains(url, "?include_") {
		t.Errorf("RSSHub twitter/user options must be route params, not query params: %s", url)
	}
}
