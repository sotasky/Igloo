package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestPlayerControlLabelsEscapeLocalizedAttributes(t *testing.T) {
	p := newTestPageProps()
	p.Text = map[string]string{
		"action_toggle_fullscreen":          `Full" onmouseover="alert(1)`,
		"player_back_10_seconds":           `Back" onmouseover="alert(1)`,
		"player_seek_backward_10_seconds":  `Seek back" onmouseover="alert(1)`,
		"player_forward_10_seconds":        `Forward" onmouseover="alert(1)`,
		"player_seek_forward_10_seconds":   `Seek forward" onmouseover="alert(1)`,
		"action_subscribe_channel":         `Subscribe" onmouseover="alert(1)`,
		"action_unsubscribe_channel":       `Unsubscribe" onmouseover="alert(1)`,
		"action_copy_link":                 `Copy" onmouseover="alert(1)`,
		"player_autoplay_next":             `Autoplay" onmouseover="alert(1)`,
		"player_delete_video_title":        `Delete" onmouseover="alert(1)`,
		"action_bookmark":                  `Bookmark" onmouseover="alert(1)`,
		"action_pin_temp_video":            `Pin" onmouseover="alert(1)`,
		"action_star_channel":              `Star" onmouseover="alert(1)`,
		"player_subtitles":                 `Subtitles" onmouseover="alert(1)`,
		"player_playback_speed":            `Speed" onmouseover="alert(1)`,
		"player_playback_speed_menu":       `Speed menu" onmouseover="alert(1)`,
	}
	video := model.Video{
		VideoID:      "video1",
		ChannelID:    "youtube_channel1",
		ChannelName:  "Test Channel",
		Title:        "Test Video",
		Platform:     "youtube",
		FilePath:     "/api/media/stream/video1",
		ThumbnailURL: "/api/media/thumbnail/video1",
		IsTemp:       true,
	}

	var buf bytes.Buffer
	if err := PlayerPage(p, video, nil, nil, nil, "").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if strings.Contains(html, `onmouseover="alert(1)"`) {
		t.Fatalf("rendered localized label into executable attribute: %s", html)
	}
	if !strings.Contains(html, `Full&#34; onmouseover=&#34;alert(1)`) {
		t.Fatalf("expected escaped fullscreen label in output")
	}
	if !strings.Contains(html, `Subscribe&#34; onmouseover=&#34;alert(1)`) {
		t.Fatalf("expected escaped subscribe label in output")
	}
	if !strings.Contains(html, `Delete&#34; onmouseover=&#34;alert(1)`) {
		t.Fatalf("expected escaped delete label in output")
	}
}
