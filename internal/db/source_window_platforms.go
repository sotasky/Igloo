package db

import (
	"fmt"
	"strings"
)

func sourceWindowPlatformEnabledClause(videoAlias string, includeTikTok, includeInstagram bool) string {
	videoAlias = strings.TrimSpace(videoAlias)
	if videoAlias == "" {
		videoAlias = "v"
	}
	return fmt.Sprintf(
		"((%d != 0 AND %s.channel_id LIKE 'tiktok_%%') OR (%d != 0 AND %s.channel_id LIKE 'instagram_%%'))",
		boolToInt(includeTikTok),
		videoAlias,
		boolToInt(includeInstagram),
		videoAlias,
	)
}
