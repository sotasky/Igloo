package model

import (
	"encoding/json"
	"testing"
)

func TestStripVideoMetadata(t *testing.T) {
	raw := map[string]any{
		"duration":     30.0,
		"width":        1080,
		"height":       1920,
		"vcodec":       "h264",
		"view_count":   5000.0,
		"like_count":   100.0,
		"webpage_url":  "https://example.com/video",
		"upload_date":  "20260101",
		"slides":       []any{map[string]any{"path": "a.jpg"}},
		"coauthors":    []any{map[string]any{"username": "collab_one", "full_name": "Collab One"}},
		"tagged_users": []any{map[string]any{"username": "tagged_one", "full_name": "Tagged One"}},
		"formats":      []any{"a", "b"},
		"thumbnails":   []any{"x"},
		"http_headers": map[string]any{"User-Agent": "test"},
		"description":  "long text",
		"_type":        "video",
		"channel":      "someone",
	}

	stripped := StripVideoMetadata(raw)

	if len(stripped) != 11 {
		t.Errorf("expected 11 keys, got %d", len(stripped))
	}
	if stripped["duration"] != 30.0 {
		t.Errorf("duration = %v", stripped["duration"])
	}
	if _, ok := stripped["formats"]; ok {
		t.Error("formats should be stripped")
	}
	if _, ok := stripped["http_headers"]; ok {
		t.Error("http_headers should be stripped")
	}
	if _, ok := stripped["coauthors"]; !ok {
		t.Error("coauthors should be preserved for bookmark account pills")
	}
	if _, ok := stripped["tagged_users"]; !ok {
		t.Error("tagged_users should be preserved for account pills")
	}

	strippedJSON, _ := json.Marshal(stripped)
	if len(strippedJSON) > 520 {
		t.Errorf("stripped JSON should be small, got %d bytes", len(strippedJSON))
	}
}

func TestVideoMetadataParsesTaggedUsers(t *testing.T) {
	v := Video{
		MetadataJSON: `{"tagged_users":[{"username":"tagged.one","full_name":"Tagged One","profile_pic_url":"https://cdn.example/tagged.jpg"}]}`,
	}
	meta := v.ParseMetadata()
	if meta == nil || len(meta.TaggedUsers) != 1 {
		t.Fatalf("TaggedUsers = %#v", meta)
	}
	if meta.TaggedUsers[0].Username != "tagged.one" || meta.TaggedUsers[0].FullName != "Tagged One" {
		t.Fatalf("tagged user = %#v", meta.TaggedUsers[0])
	}
}

func TestStripVideoMetadata_Nil(t *testing.T) {
	result := StripVideoMetadata(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestCompactCountLabel(t *testing.T) {
	for _, tc := range []struct {
		value int64
		want  string
	}{
		{999, "999"},
		{1_000, "1K"},
		{12_345, "12.3K"},
		{182_191, "182K"},
		{1_234_567, "1.2M"},
	} {
		if got := CompactCountLabel(tc.value); got != tc.want {
			t.Fatalf("CompactCountLabel(%d) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestVideoMetadataJSONWithCountLabels(t *testing.T) {
	got := VideoMetadataJSONWithCountLabels(`{"view_count":182191,"like_count":9051,"duration":15}`)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if decoded["view_count_label"] != "182K" || decoded["like_count_label"] != "9.1K" {
		t.Fatalf("count labels = %#v", decoded)
	}
	if decoded["duration"] != float64(15) {
		t.Fatalf("duration should be preserved, got %#v", decoded["duration"])
	}
}

func TestProfileCountLabel(t *testing.T) {
	for _, tc := range []struct {
		value int
		want  string
	}{
		{999, "999"},
		{2_203, "2,203"},
		{76_123, "76.1K"},
		{1_234_567, "1.2M"},
	} {
		if got := ProfileCountLabel(tc.value); got != tc.want {
			t.Fatalf("ProfileCountLabel(%d) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestDurationLabel(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    string
	}{
		{0, ""},
		{-5, ""},
		{45, "0:45"},
		{754, "12:34"},
		{3_726, "1:02:06"},
	} {
		if got := DurationLabel(tc.seconds); got != tc.want {
			t.Fatalf("DurationLabel(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestComputeMediaKind_Slideshow(t *testing.T) {
	meta := &VideoMetadata{Slides: make([]json.RawMessage, 3)}
	kind, count := ComputeMediaKind(meta, "video.mp4")
	if kind != "slideshow" || count != 3 {
		t.Errorf("expected slideshow/3, got %s/%d", kind, count)
	}
}

func TestComputeMediaKind_SingleImage(t *testing.T) {
	meta := &VideoMetadata{Duration: 0, Slides: make([]json.RawMessage, 1)}
	kind, count := ComputeMediaKind(meta, "photo.jpg")
	if kind != "image" || count != 1 {
		t.Errorf("expected image/1, got %s/%d", kind, count)
	}
}

func TestComputeMediaKind_Video(t *testing.T) {
	meta := &VideoMetadata{Duration: 30}
	kind, count := ComputeMediaKind(meta, "video.mp4")
	if kind != "video" || count != 0 {
		t.Errorf("expected video/0, got %s/%d", kind, count)
	}
}

func TestComputeMediaKind_TikTokSlideshow(t *testing.T) {
	meta := &VideoMetadata{VCodec: "none"}
	kind, _ := ComputeMediaKind(meta, "video.mp4")
	if kind != "slideshow" {
		t.Errorf("expected slideshow, got %s", kind)
	}
}

func TestComputeMediaKind_ImageByPath(t *testing.T) {
	kind, count := ComputeMediaKind(nil, "photo.webp")
	if kind != "image" || count != 1 {
		t.Errorf("expected image/1, got %s/%d", kind, count)
	}
}

func TestComputeMediaKind_FallbackVideo(t *testing.T) {
	kind, count := ComputeMediaKind(nil, "clip.mp4")
	if kind != "video" || count != 0 {
		t.Errorf("expected video/0, got %s/%d", kind, count)
	}
}

func TestMediaMode(t *testing.T) {
	cases := []struct {
		name       string
		mediaKind  string
		slideCount int
		want       string
	}{
		{"video fallback", "", 0, "video"},
		{"explicit video", "video", 0, "video"},
		{"image", "image", 1, "image"},
		{"photo alias", "photo", 1, "image"},
		{"explicit slideshow", "slideshow", 1, "slideshow"},
		{"slide count wins", "video", 2, "slideshow"},
		{"trim and lowercase", " Image ", 1, "image"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MediaMode(c.mediaKind, c.slideCount); got != c.want {
				t.Fatalf("MediaMode(%q, %d) = %q, want %q", c.mediaKind, c.slideCount, got, c.want)
			}
		})
	}
}
