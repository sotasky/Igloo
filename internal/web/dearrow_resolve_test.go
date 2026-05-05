package web

import "testing"

func TestResolveDearrowTitle(t *testing.T) {
	ptr := func(s string) *string { return &s }
	cases := []struct {
		name         string
		mode, orig   string
		da, daCasual *string
		want         string
	}{
		{"off always returns original", "off", "Clickbait!", ptr("Real"), ptr("Casual"), "Clickbait!"},
		{"default picks community", "default", "Clickbait!", ptr("Real"), ptr("Casual"), "Real"},
		{"default falls back to original when no community", "default", "Clickbait!", nil, ptr("Casual"), "Clickbait!"},
		{"default ignores casual", "default", "Clickbait!", nil, ptr("Casual"), "Clickbait!"},
		{"casual prefers casual", "casual", "Clickbait!", ptr("Real"), ptr("Casual"), "Casual"},
		{"casual falls back to community", "casual", "Clickbait!", ptr("Real"), nil, "Real"},
		{"casual falls back to original when neither", "casual", "Clickbait!", nil, nil, "Clickbait!"},
		{"empty community string skipped", "default", "Clickbait!", ptr(""), nil, "Clickbait!"},
		{"empty casual string skipped", "casual", "Clickbait!", ptr("Real"), ptr(""), "Real"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveDearrowTitle(c.mode, c.orig, c.da, c.daCasual)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestResolveDearrowDisplayTitles(t *testing.T) {
	ptr := func(s string) *string { return &s }
	cases := []struct {
		name           string
		original       string
		community      *string
		casual         *string
		wantDefault    string
		wantCasual     string
	}{
		{
			name:        "community and casual",
			original:    "Original",
			community:   ptr("Community"),
			casual:      ptr("Casual"),
			wantDefault: "Community",
			wantCasual:  "Casual",
		},
		{
			name:        "casual falls back to community",
			original:    "Original",
			community:   ptr("Community"),
			casual:      ptr(""),
			wantDefault: "Community",
			wantCasual:  "Community",
		},
		{
			name:        "both fall back to original",
			original:    "Original",
			wantDefault: "Original",
			wantCasual:  "Original",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotDefault, gotCasual := ResolveDearrowDisplayTitles(c.original, c.community, c.casual)
			if gotDefault != c.wantDefault || gotCasual != c.wantCasual {
				t.Fatalf("ResolveDearrowDisplayTitles = (%q, %q), want (%q, %q)", gotDefault, gotCasual, c.wantDefault, c.wantCasual)
			}
		})
	}
}

func TestResolveDearrowThumbURL(t *testing.T) {
	ptr := func(s string) *string { return &s }
	cases := []struct {
		name  string
		mode  string
		thumb *string
		want  string
	}{
		{"off never decorates", "off", ptr("thumbnails/dearrow/v1.jpg"), "/api/media/thumbnail/vid1"},
		{"default with thumb adds ?da=1", "default", ptr("x"), "/api/media/thumbnail/vid1?da=1"},
		{"default without thumb bare", "default", nil, "/api/media/thumbnail/vid1"},
		{"casual with thumb adds ?da=1", "casual", ptr("x"), "/api/media/thumbnail/vid1?da=1"},
		{"empty path treated as no thumb", "default", ptr(""), "/api/media/thumbnail/vid1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveDearrowThumbURL(c.mode, "vid1", c.thumb)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
