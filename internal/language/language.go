package language

import (
	"strings"

	bcp47 "golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// DisplayName returns the human-readable source label to show in translated
// feed UI. Provider-supplied names are preserved; provider-supplied BCP-47
// codes are expanded through CLDR instead of an app-owned language table.
func DisplayName(value string) string {
	raw := clean(value)
	if raw == "" {
		return ""
	}
	if tag, ok := parseTag(raw); ok {
		if namer := display.Tags(bcp47.English); namer != nil {
			if name := strings.TrimSpace(namer.Name(tag)); name != "" {
				return name
			}
		}
	}
	return raw
}

func Matches(a, b string) bool {
	for _, av := range Equivalents(a) {
		for _, bv := range Equivalents(b) {
			if av == bv {
				return true
			}
		}
	}
	return false
}

func InSet(value string, set map[string]bool) bool {
	if len(set) == 0 {
		return false
	}
	for _, candidate := range Equivalents(value) {
		if set[candidate] {
			return true
		}
	}
	for candidate := range set {
		if Matches(value, candidate) {
			return true
		}
	}
	return false
}

func IsUnknown(value string) bool {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" {
		return true
	}
	raw = strings.ReplaceAll(raw, "_", "-")
	switch raw {
	case "und", "unknown", "qam", "qct", "qht", "qme", "qst", "zxx":
		return true
	default:
		return strings.HasPrefix(raw, "q") && len(raw) == 3
	}
}

func Equivalents(value string) []string {
	raw := clean(value)
	if raw == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(raw)
	if tag, ok := parseTag(raw); ok {
		if base, _ := tag.Base(); base.String() != "und" {
			add(base.String())
		}
		add(DisplayName(raw))
	}
	return out
}

func clean(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch strings.ToLower(value) {
	case "und", "unknown":
		return ""
	default:
		return value
	}
}

func parseTag(value string) (bcp47.Tag, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return bcp47.Tag{}, false
	}
	value = strings.ReplaceAll(value, "_", "-")
	tag, err := bcp47.Parse(value)
	return tag, err == nil
}
