package subtitlemeta

import (
	"encoding/json"
	"net/url"
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

// ManualLangs returns languages that have manually uploaded subtitles.
func ManualLangs(info map[string]any) map[string]bool {
	subtitles := metadataMap(info, "subtitles")
	automaticCaptions := metadataMap(info, "automatic_captions")
	langs := make(map[string]bool)
	for lang, value := range subtitles {
		if subtitleListHasManualEntries(metadataRaw(value)) && !subtitleListHasASREntries(metadataRaw(automaticCaptions[lang])) {
			langs[lang] = true
		}
	}
	return langs
}

func IsAuto(info map[string]any, lang string) bool {
	return !ManualLangs(info)[lang]
}

// Language returns the audio language reported by yt-dlp.
func Language(info map[string]any) string {
	language, _ := info["language"].(string)
	return language
}

func metadataMap(info map[string]any, key string) map[string]any {
	if info == nil {
		return nil
	}
	value, _ := info[key].(map[string]any)
	return value
}

func metadataRaw(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
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
