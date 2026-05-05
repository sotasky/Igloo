package xsource

import "testing"

func TestParseURLDetectsListsAndCommunities(t *testing.T) {
	tests := []struct {
		name       string
		rawURL     string
		sourceID   string
		sourceType string
		externalID string
		canonical  string
	}{
		{
			name:       "x list",
			rawURL:     "https://x.com/i/lists/12345",
			sourceID:   "twitter_list_12345",
			sourceType: "list",
			externalID: "12345",
			canonical:  "https://x.com/i/lists/12345",
		},
		{
			name:       "twitter community with query",
			rawURL:     "https://twitter.com/i/communities/98765?ref=nav",
			sourceID:   "twitter_community_98765",
			sourceType: "community",
			externalID: "98765",
			canonical:  "https://x.com/i/communities/98765",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := ParseURL(tt.rawURL)
			if err != nil {
				t.Fatalf("ParseURL: %v", err)
			}
			if src.SourceID != tt.sourceID || src.SourceType != tt.sourceType || src.ExternalID != tt.externalID || src.URL != tt.canonical {
				t.Fatalf("source = %#v", src)
			}
		})
	}
}

func TestParseURLRejectsAccountURL(t *testing.T) {
	if _, err := ParseURL("https://x.com/alice"); err == nil {
		t.Fatal("expected account URL to be rejected")
	}
}
