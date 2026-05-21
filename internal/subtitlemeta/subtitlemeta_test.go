package subtitlemeta

import (
	"os"
	"path/filepath"
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
	dir := t.TempDir()
	infoPath := filepath.Join(dir, "video.info.json")
	body := `{"subtitles":{"en":[{"url":"https://example.test/manual.vtt"}],"fr":[]},"automatic_captions":{"fr":[{"url":"https://example.test/auto.vtt"}]}}`
	if err := os.WriteFile(infoPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write info: %v", err)
	}

	langs := ManualLangs(infoPath)
	if !langs["en"] {
		t.Fatal("expected en to be manual")
	}
	if langs["fr"] {
		t.Fatal("expected empty fr subtitles to be non-manual")
	}
	if IsAuto(infoPath, "en") {
		t.Fatal("manual en subtitles should not be auto")
	}
	if !IsAuto(infoPath, "fr") {
		t.Fatal("empty manual subtitles should fall back to auto")
	}
	if !IsAuto(filepath.Join(dir, "missing.info.json"), "en") {
		t.Fatal("missing info.json should be treated as auto")
	}
}

func TestManualLangsTreatsYouTubeASRSubtitleEntriesAsAuto(t *testing.T) {
	dir := t.TempDir()
	infoPath := filepath.Join(dir, "video.info.json")
	body := `{
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
	}`
	if err := os.WriteFile(infoPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write info: %v", err)
	}

	langs := ManualLangs(infoPath)
	if langs["en"] {
		t.Fatal("ASR-marked subtitles.en should not be treated as manual")
	}
	if !langs["tr"] {
		t.Fatal("non-ASR subtitles.tr should be treated as manual")
	}
	if !IsAuto(infoPath, "en") {
		t.Fatal("ASR-marked subtitles.en should be auto")
	}
	if IsAuto(infoPath, "tr") {
		t.Fatal("non-ASR subtitles.tr should be manual")
	}
}

func TestManualLangsKeepsCapsASRSubtitleManualWithoutASRAutomaticTrack(t *testing.T) {
	dir := t.TempDir()
	infoPath := filepath.Join(dir, "video.info.json")
	body := `{
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
	}`
	if err := os.WriteFile(infoPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write info: %v", err)
	}

	langs := ManualLangs(infoPath)
	if !langs["en"] {
		t.Fatal("caps=asr subtitles.en without a same-language kind=asr automatic track should be manual")
	}
	if IsAuto(infoPath, "en") {
		t.Fatal("caps=asr subtitles.en without a same-language kind=asr automatic track should not be auto")
	}
}

func TestLanguage(t *testing.T) {
	dir := t.TempDir()
	infoPath := filepath.Join(dir, "video.info.json")
	if err := os.WriteFile(infoPath, []byte(`{"language":"tr"}`), 0o644); err != nil {
		t.Fatalf("write info: %v", err)
	}
	if got := Language(infoPath); got != "tr" {
		t.Fatalf("Language = %q, want %q", got, "tr")
	}
	if got := Language(filepath.Join(dir, "missing.info.json")); got != "" {
		t.Fatalf("Language(missing) = %q, want empty", got)
	}
	noLangPath := filepath.Join(dir, "nolang.info.json")
	if err := os.WriteFile(noLangPath, []byte(`{"id":"abc"}`), 0o644); err != nil {
		t.Fatalf("write nolang: %v", err)
	}
	if got := Language(noLangPath); got != "" {
		t.Fatalf("Language(no-language) = %q, want empty", got)
	}
}
