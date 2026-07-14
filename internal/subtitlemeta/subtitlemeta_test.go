package subtitlemeta

import (
	"encoding/json"
	"testing"
)

func TestTrackLang(t *testing.T) {
	tests := []struct {
		name     string
		stem     string
		filename string
		want     string
	}{
		{name: "explicit english", stem: "video", filename: "video.en.vtt", want: "en"},
		{name: "bare vtt defaults to english", stem: "video", filename: "video.vtt", want: "en"},
		{name: "regional language", stem: "video", filename: "video.eng-US.vtt", want: "eng-US"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TrackLang(tt.stem, tt.filename); got != tt.want {
				t.Fatalf("TrackLang(%q, %q) = %q, want %q", tt.stem, tt.filename, got, tt.want)
			}
		})
	}
}

func TestManualLangsAndIsAuto(t *testing.T) {
	info := testMetadata(t, `{"subtitles":{"en":[{"url":"https://example.test/manual.vtt"}],"fr":[]},"automatic_captions":{"fr":[{"url":"https://example.test/auto.vtt"}]}}`)

	langs := ManualLangs(info)
	if !langs["en"] {
		t.Fatal("expected en to be manual")
	}
	if langs["fr"] {
		t.Fatal("expected empty fr subtitles to be non-manual")
	}
	if IsAuto(info, "en") {
		t.Fatal("manual en subtitles should not be auto")
	}
	if !IsAuto(info, "fr") {
		t.Fatal("empty manual subtitles should fall back to auto")
	}
	if !IsAuto(nil, "en") {
		t.Fatal("missing metadata should be treated as auto")
	}
}

func TestManualLangsTreatsYouTubeASRSubtitleEntriesAsAuto(t *testing.T) {
	info := testMetadata(t, `{
		"subtitles": {
			"en": [
				{"ext":"vtt","url":"https://www.youtube.com/api/timedtext?v=sample&caps=asr&lang=en&fmt=vtt","name":"English"}
			],
			"tr": [
				{"ext":"vtt","url":"https://www.youtube.com/api/timedtext?v=sample&lang=tr&fmt=vtt","name":"Turkish"}
			]
		},
		"automatic_captions": {
			"en": [
				{"ext":"vtt","url":"https://www.youtube.com/api/timedtext?v=sample&kind=asr&lang=en&fmt=vtt","name":"English"}
			]
		}
	}`)

	langs := ManualLangs(info)
	if langs["en"] {
		t.Fatal("ASR-marked subtitles.en should not be treated as manual")
	}
	if !langs["tr"] {
		t.Fatal("non-ASR subtitles.tr should be treated as manual")
	}
	if !IsAuto(info, "en") {
		t.Fatal("ASR-marked subtitles.en should be auto")
	}
	if IsAuto(info, "tr") {
		t.Fatal("non-ASR subtitles.tr should be manual")
	}
}

func TestManualLangsKeepsCapsASRSubtitleManualWithoutASRAutomaticTrack(t *testing.T) {
	info := testMetadata(t, `{
		"subtitles": {
			"en": [
				{"ext":"vtt","url":"https://www.youtube.com/api/timedtext?v=sample&caps=asr&lang=en&fmt=vtt","name":"English"}
			]
		},
		"automatic_captions": {
			"en": [
				{"ext":"vtt","protocol":"m3u8_native","url":"https://manifest.googlevideo.com/api/manifest/hls_timedtext_playlist/tts_params/caps%3Dasr%26lang%3Den/playlist/index.m3u8"}
			]
		}
	}`)

	langs := ManualLangs(info)
	if !langs["en"] {
		t.Fatal("caps=asr subtitles.en without a same-language kind=asr automatic track should be manual")
	}
	if IsAuto(info, "en") {
		t.Fatal("caps=asr subtitles.en without a same-language kind=asr automatic track should not be auto")
	}
}

func TestLanguage(t *testing.T) {
	if got := Language(map[string]any{"language": "tr"}); got != "tr" {
		t.Fatalf("Language = %q, want %q", got, "tr")
	}
	if got := Language(nil); got != "" {
		t.Fatalf("Language(nil) = %q, want empty", got)
	}
	if got := Language(map[string]any{"id": "sample"}); got != "" {
		t.Fatalf("Language(no-language) = %q, want empty", got)
	}
}

func testMetadata(t *testing.T, raw string) map[string]any {
	t.Helper()
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}
