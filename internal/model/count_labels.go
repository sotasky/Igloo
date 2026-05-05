package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CompactCountLabel renders count presentation labels for synced clients.
func CompactCountLabel(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	if n < 1_000_000 {
		k := float64(n) / 1000.0
		if k >= 100 {
			return strconv.Itoa(int(k+0.5)) + "K"
		}
		return trimCountZero(fmt.Sprintf("%.1f", k)) + "K"
	}
	m := float64(n) / 1_000_000.0
	if m >= 100 {
		return strconv.Itoa(int(m+0.5)) + "M"
	}
	return trimCountZero(fmt.Sprintf("%.1f", m)) + "M"
}

// ProfileCountLabel renders profile follower/following counts like the web
// profile card contract: 76.1K, 1.2M, or comma-grouped small thousands.
func ProfileCountLabel(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	case n >= 1_000:
		return addThousandsSep(n)
	default:
		return strconv.Itoa(n)
	}
}

// DurationLabel renders persisted video durations for static synced UI badges.
func DurationLabel(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// VideoMetadataJSONWithCountLabels adds compact count labels to Android-facing
// metadata JSON while preserving all existing metadata fields.
func VideoMetadataJSONWithCountLabels(metadataJSON string) string {
	if strings.TrimSpace(metadataJSON) == "" {
		return metadataJSON
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return metadataJSON
	}
	if v, ok := jsonInt64(metadata["view_count"]); ok {
		metadata["view_count_label"] = CompactCountLabel(v)
	}
	if v, ok := jsonInt64(metadata["like_count"]); ok {
		metadata["like_count_label"] = CompactCountLabel(v)
	}
	out, err := json.Marshal(metadata)
	if err != nil {
		return metadataJSON
	}
	return string(out)
}

func jsonInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func trimCountZero(s string) string {
	return strings.TrimSuffix(s, ".0")
}

func addThousandsSep(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	first := len(s) % 3
	if first > 0 {
		b.WriteString(s[:first])
	}
	for i := first; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
