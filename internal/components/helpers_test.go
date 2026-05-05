package components

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestPrefsData_DearrowMode(t *testing.T) {
	// Unset -> off
	p := PrefsData{Settings: map[string]any{}}
	if got := p.DearrowMode(); got != "off" {
		t.Errorf("unset = %q, want off", got)
	}

	// Set to "casual" -> casual
	p.Settings["dearrow_mode"] = "casual"
	if got := p.DearrowMode(); got != "casual" {
		t.Errorf("casual = %q, want casual", got)
	}

	// Set to nonsense -> off
	p.Settings["dearrow_mode"] = "xyz"
	if got := p.DearrowMode(); got != "off" {
		t.Errorf("xyz -> %q, want off", got)
	}
}

func ptr(s string) *string { return &s }

func TestPrefsData_VideoTitle(t *testing.T) {
	cases := []struct {
		name     string
		mode     string
		title    string
		da       *string
		daCasual *string
		want     string
	}{
		{"off mode returns original title", "off", "Original Title", ptr("Community Title"), ptr("Casual Title"), "Original Title"},
		{"default mode with community title", "default", "Original Title", ptr("Community Title"), ptr("Casual Title"), "Community Title"},
		{"casual mode with casual title", "casual", "Original Title", ptr("Community Title"), ptr("Casual Title"), "Casual Title"},
		{"default mode no community falls back to original", "default", "Original Title", nil, nil, "Original Title"},
		{"casual mode no casual falls back to community", "casual", "Original Title", ptr("Community Title"), nil, "Community Title"},
		{"casual mode no dearrow fields falls back to original", "casual", "Original Title", nil, nil, "Original Title"},
		{"empty community string skipped in default", "default", "Original", ptr(""), nil, "Original"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := PrefsData{Settings: map[string]any{"dearrow_mode": c.mode}}
			v := model.Video{
				Title:              c.title,
				DearrowTitle:       c.da,
				DearrowTitleCasual: c.daCasual,
			}
			got := p.VideoTitle(v)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestPrefsData_VideoThumbURL(t *testing.T) {
	cases := []struct {
		name          string
		mode          string
		thumbnailURL  string
		dearrowThumb  *string
		want          string
	}{
		{"off mode returns ThumbnailURL directly", "off", "https://cdn.example.com/thumb.jpg", ptr("path/dearrow.jpg"), "https://cdn.example.com/thumb.jpg"},
		{"off mode no ThumbnailURL returns api path", "off", "", ptr("path/dearrow.jpg"), "/api/media/thumbnail/vid1"},
		{"default with dearrow thumb appends ?da=1", "default", "https://cdn.example.com/thumb.jpg", ptr("path/dearrow.jpg"), "https://cdn.example.com/thumb.jpg?da=1"},
		{"default without dearrow thumb returns ThumbnailURL", "default", "https://cdn.example.com/thumb.jpg", nil, "https://cdn.example.com/thumb.jpg"},
		{"casual with dearrow thumb appends ?da=1", "casual", "https://cdn.example.com/thumb.jpg", ptr("path/dearrow.jpg"), "https://cdn.example.com/thumb.jpg?da=1"},
		{"empty dearrow path treated as no thumb", "default", "https://cdn.example.com/thumb.jpg", ptr(""), "https://cdn.example.com/thumb.jpg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := PrefsData{Settings: map[string]any{"dearrow_mode": c.mode}}
			v := model.Video{
				VideoID:          "vid1",
				ThumbnailURL:     c.thumbnailURL,
				DearrowThumbPath: c.dearrowThumb,
			}
			got := p.VideoThumbURL(v)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
