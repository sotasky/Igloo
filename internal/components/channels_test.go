package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestChannelsPageRender(t *testing.T) {
	p := newTestPageProps()
	p.ActiveNav = "channels"
	p.PageTitle = "Channels"

	sections := []ChannelWithVideos{
		{
			Channel: model.Channel{ChannelID: "youtube_chan1", Name: "Test Channel", Platform: "youtube", VideoCount: 42, IsStarred: true},
			Videos: []model.Video{
				{VideoID: "v1", Title: "Video One", Duration: 120, ThumbnailURL: "/api/media/thumbnail/v1"},
			},
		},
		{
			Channel: model.Channel{ChannelID: "tt_chan2", Name: "TikTok Creator", Platform: "tiktok", VideoCount: 10},
			Videos: []model.Video{
				{VideoID: "v2", Title: "Short One", ThumbnailURL: "/api/media/thumbnail/v2"},
			},
		},
	}

	var buf bytes.Buffer
	err := ChannelsPage(p, sections, "", false, 0).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	checks := []string{
		`id="channel-tab-search"`,
		`hx-get="/channels"`,
		`Test Channel`,
		`TikTok Creator`,
		`data-channel-id="youtube_chan1"`,
		`data-video-id="v1"`,
		`Video One`,
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing: %s", check)
		}
	}
}

func TestChannelSectionGridClass(t *testing.T) {
	ytSection := ChannelWithVideos{
		Channel: model.Channel{Platform: "youtube"},
		Videos:  []model.Video{{VideoID: "v1", Title: "Test"}},
	}
	ttSection := ChannelWithVideos{
		Channel: model.Channel{Platform: "tiktok"},
		Videos:  []model.Video{{VideoID: "v2", Title: "Test"}},
	}

	var buf1 bytes.Buffer
	_ = ChannelSection(newTestPageProps(), ytSection).Render(context.Background(), &buf1)
	if strings.Contains(buf1.String(), "shorts-grid") {
		t.Error("YouTube channel should not have shorts-grid class")
	}

	var buf2 bytes.Buffer
	_ = ChannelSection(newTestPageProps(), ttSection).Render(context.Background(), &buf2)
	if !strings.Contains(buf2.String(), "shorts-grid") {
		t.Error("TikTok channel should have shorts-grid class")
	}
}

func TestChannelSectionsEmpty(t *testing.T) {
	var buf bytes.Buffer
	_ = ChannelSections(newTestPageProps(), nil).Render(context.Background(), &buf)
	if !strings.Contains(buf.String(), "No channels") {
		t.Error("empty state not rendered")
	}
}

func TestChannelStarButtonStates(t *testing.T) {
	p := newTestPageProps()

	var buf1 bytes.Buffer
	_ = ChannelStarButton(p, "ch1", true).Render(context.Background(), &buf1)
	if !strings.Contains(buf1.String(), "Unstar") {
		t.Error("starred channel should show Unstar")
	}

	var buf2 bytes.Buffer
	_ = ChannelStarButton(p, "ch2", false).Render(context.Background(), &buf2)
	if !strings.Contains(buf2.String(), "Star") {
		t.Error("unstarred channel should show Star")
	}
	if !strings.Contains(buf2.String(), `hx-post="/api/channels/ch2/star"`) {
		t.Error("missing HTMX star endpoint")
	}
}
