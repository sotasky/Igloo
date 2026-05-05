package web

import (
	"fmt"

	"github.com/screwys/igloo/internal/db"
)

func androidDashboardRetentionLabel(key string, retention db.AndroidRetentionSettings) string {
	switch key {
	case "videos":
		return retentionDaysLabel(retention.YoutubeDays)
	case "moments":
		return retentionDaysLabel(retention.MomentsDays)
	case "feed":
		return retentionDaysLabel(retention.FeedDays)
	default:
		return ""
	}
}

func retentionDaysLabel(days int) string {
	if days <= 0 {
		return "all"
	}
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
