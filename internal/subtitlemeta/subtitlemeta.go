package subtitlemeta

import (
	"encoding/json"
	"os"
	"strings"
)

// TrackLang extracts the language suffix from a VTT filename next to a video.
// "video-id.en.vtt" -> "en"; "video-id.vtt" -> "en".
func TrackLang(videoStem, filename string) string {
	suffix := strings.TrimPrefix(filename, videoStem)
	suffix = strings.TrimSuffix(suffix, ".vtt")
	suffix = strings.TrimPrefix(suffix, ".")
	if suffix == "" {
		return "en"
	}
	return suffix
}

// ManualLangs reads a yt-dlp info.json sidecar and returns languages that have
// manually uploaded subtitles. Auto captions live under automatic_captions and
// should not be enabled by default.
func ManualLangs(infoPath string) map[string]bool {
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return nil
	}
	var info struct {
		Subtitles map[string]json.RawMessage `json:"subtitles"`
	}
	if json.Unmarshal(data, &info) != nil {
		return nil
	}
	langs := make(map[string]bool)
	for lang, raw := range info.Subtitles {
		if subtitleListHasEntries(raw) {
			langs[lang] = true
		}
	}
	return langs
}

func IsAuto(infoPath, lang string) bool {
	return !ManualLangs(infoPath)[lang]
}

// Language returns the audio language reported by yt-dlp's info.json
// (top-level `language` field, e.g. "en-US", "tr", "ja"). Empty string
// when the file is missing, malformed, or the field is absent.
func Language(infoPath string) string {
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return ""
	}
	var info struct {
		Language string `json:"language"`
	}
	if json.Unmarshal(data, &info) != nil {
		return ""
	}
	return info.Language
}

func subtitleListHasEntries(raw json.RawMessage) bool {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err == nil {
		return len(entries) > 0
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return v != nil
}
