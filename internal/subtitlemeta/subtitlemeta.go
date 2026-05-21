package subtitlemeta

import (
	"encoding/json"
	"net/url"
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
		Subtitles         map[string]json.RawMessage `json:"subtitles"`
		AutomaticCaptions map[string]json.RawMessage `json:"automatic_captions"`
	}
	if json.Unmarshal(data, &info) != nil {
		return nil
	}
	langs := make(map[string]bool)
	for lang, raw := range info.Subtitles {
		if subtitleListHasManualEntries(raw) && !subtitleListHasASREntries(info.AutomaticCaptions[lang]) {
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

type subtitleFormat struct {
	URL  string `json:"url"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func subtitleListHasManualEntries(raw json.RawMessage) bool {
	var entries []subtitleFormat
	if err := json.Unmarshal(raw, &entries); err == nil {
		for _, entry := range entries {
			if !subtitleFormatLooksAuto(entry) {
				return true
			}
		}
		return false
	}
	var entry subtitleFormat
	if err := json.Unmarshal(raw, &entry); err == nil && (entry.URL != "" || entry.Name != "" || entry.Kind != "") {
		return !subtitleFormatLooksAuto(entry)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return v != nil
}

func subtitleListHasASREntries(raw json.RawMessage) bool {
	var entries []subtitleFormat
	if err := json.Unmarshal(raw, &entries); err == nil {
		for _, entry := range entries {
			if subtitleFormatLooksAuto(entry) {
				return true
			}
		}
		return false
	}
	var entry subtitleFormat
	if err := json.Unmarshal(raw, &entry); err == nil {
		return subtitleFormatLooksAuto(entry)
	}
	return false
}

func subtitleFormatLooksAuto(entry subtitleFormat) bool {
	if strings.EqualFold(strings.TrimSpace(entry.Kind), "asr") {
		return true
	}
	name := strings.ToLower(entry.Name)
	if strings.Contains(name, "auto-generated") || strings.Contains(name, "automatic") {
		return true
	}
	return subtitleURLHasASRSignal(entry.URL)
}

func subtitleURLHasASRSignal(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	if parsed, err := url.Parse(rawURL); err == nil {
		query := parsed.Query()
		if strings.EqualFold(query.Get("kind"), "asr") {
			return true
		}
	}
	lower := strings.ToLower(rawURL)
	return strings.Contains(lower, "kind=asr") ||
		strings.Contains(lower, "kind%3dasr")
}
